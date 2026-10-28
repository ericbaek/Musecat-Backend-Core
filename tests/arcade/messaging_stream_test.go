package arcade_test

import (
	"bufio"
	"context"
	"errors"
	"github.com/ericbaek/musecat-backend-core/service/messaging"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMessagingStreamCommitAccountRevocationAndCleanup(t *testing.T) {
	app := newArcadeTestApp(t)
	_, a := createAuthUserWithTags(t, app, []string{})
	token, b := createAuthUserWithTags(t, app, []string{})
	_ = messaging.Preference(app, b.Id, "everyone")
	c, err := messaging.Create(app, a.Id, messaging.CreateInput{Kind: "dm", PeerUserID: b.Id})
	if err != nil {
		t.Fatal(err)
	}
	r, err := apis.NewRouter(app)
	if err != nil {
		t.Fatal(err)
	}
	event := &core.ServeEvent{App: app, Router: r}
	if err = app.OnServe().Trigger(event, func(e *core.ServeEvent) error { return e.Next() }); err != nil {
		t.Fatal(err)
	}
	mux, err := r.BuildMux()
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(mux)
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/user/events", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 || response.Header.Get("X-Accel-Buffering") != "no" || response.Header.Get("Cache-Control") != "private, no-store" {
		t.Fatal(response.StatusCode, response.Header)
	}
	frames := make(chan string, 16)
	done := make(chan struct{})
	go func() {
		defer close(done)
		reader := bufio.NewReader(response.Body)
		frame := ""
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			frame += line
			if line == "\n" {
				select {
				case frames <- frame:
				case <-ctx.Done():
					return
				}
				frame = ""
			}
		}
	}()
	await := func(name string) string {
		t.Helper()
		timer := time.NewTimer(3 * time.Second)
		defer timer.Stop()
		for {
			select {
			case frame := <-frames:
				if strings.Contains(frame, "event: "+name+"\n") {
					return frame
				}
			case <-timer.C:
				t.Fatal("missing stream event", name)
				return ""
			}
		}
	}
	await("ready")
	m, _, err := messaging.Send(app, a.Id, c.ID, messaging.SendInput{Text: "private content", ClientMessageID: "stream"})
	if err != nil {
		t.Fatal(err)
	}
	frame := await("conversation.changed")
	if !strings.Contains(frame, c.ID) || strings.Contains(frame, m.Text) {
		t.Fatal("event payload", frame)
	}
	b.Set("withdrawn", true)
	if err = app.Save(b); err != nil {
		t.Fatal(err)
	}
	await("session.expired")
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("revoked stream did not close")
	}
	hub := messaging.Events(app)
	subs := []*messaging.Subscription{}
	for i := 0; i < 4; i++ {
		s := hub.Subscribe(b.Id)
		if s == nil {
			t.Fatal("stream slot leaked")
		}
		subs = append(subs, s)
	}
	if hub.Subscribe(b.Id) != nil {
		t.Fatal("stream limit missing")
	}
	for _, s := range subs {
		hub.Unsubscribe(s)
	}
}
func TestMessagingSlowConsumerQueueBounded(t *testing.T) {
	app := newArcadeTestApp(t)
	hub := messaging.Events(app)
	s := hub.Subscribe("recipient")
	defer hub.Unsubscribe(s)
	for i := 0; i < 100; i++ {
		hub.Publish(messaging.Event{Name: "conversation.changed", ID: "conversation", LatestSequence: i, Audience: []string{"recipient"}})
	}
	if len(s.Events) != 32 {
		t.Fatalf("unbounded queue %d", len(s.Events))
	}
	select {
	case <-s.Overflow:
	default:
		t.Fatal("slow subscriber must resync")
	}
}

func TestMessagingStreamCancellationReleasesSlot(t *testing.T) {
	app := newArcadeTestApp(t)
	token, user := createAuthUserWithTags(t, app, []string{})
	r, err := apis.NewRouter(app)
	if err != nil {
		t.Fatal(err)
	}
	event := &core.ServeEvent{App: app, Router: r}
	if err = app.OnServe().Trigger(event, func(e *core.ServeEvent) error { return e.Next() }); err != nil {
		t.Fatal(err)
	}
	mux, err := r.BuildMux()
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(mux)
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/user/events", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}
	cancel()
	response.Body.Close()
	hub := messaging.Events(app)
	deadline := time.Now().Add(time.Second)
	for {
		n := 0
		subscriptions := []*messaging.Subscription{}
		for i := 0; i < 4; i++ {
			s := hub.Subscribe(user.Id)
			if s != nil {
				n++
				subscriptions = append(subscriptions, s)
			}
		}
		for _, s := range subscriptions {
			hub.Unsubscribe(s)
		}
		if n == 4 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("cancelled stream retained its slot")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestMessagingReadReceiptInvalidatesOnlyDMPeer(t *testing.T) {
	app := newArcadeTestApp(t)
	_, a := createAuthUserWithTags(t, app, []string{})
	_, b := createAuthUserWithTags(t, app, []string{})
	_, outsider := createAuthUserWithTags(t, app, []string{"moderator"})
	c, err := messaging.Create(app, a.Id, messaging.CreateInput{Kind: "dm", PeerUserID: b.Id})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = messaging.Send(app, a.Id, c.ID, messaging.SendInput{Text: "read me", ClientMessageID: "receipt"}); err != nil {
		t.Fatal(err)
	}
	hub := messaging.Events(app)
	sender, reader, unrelated := hub.Subscribe(a.Id), hub.Subscribe(b.Id), hub.Subscribe(outsider.Id)
	defer hub.Unsubscribe(sender)
	defer hub.Unsubscribe(reader)
	defer hub.Unsubscribe(unrelated)
	rollback := errors.New("rollback read")
	if err = app.RunInTransaction(func(tx core.App) error {
		if _, err := messaging.SetRead(tx, b.Id, c.ID, 1); err != nil {
			return err
		}
		return rollback
	}); !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	if len(sender.Events) != 0 || len(reader.Events) != 0 {
		t.Fatal("rolled-back read emitted an event")
	}
	if summary, err := messaging.Get(app, a.Id, c.ID); err != nil || summary.CounterpartReadSequence == nil || *summary.CounterpartReadSequence != 0 {
		t.Fatal("rolled-back read persisted", summary, err)
	}
	if _, err = messaging.SetRead(app, b.Id, c.ID, 1); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-sender.Events:
		if event.Name != "conversation.changed" || event.Reason != "read" || event.ID != c.ID || event.LatestSequence != 1 {
			t.Fatalf("wrong receipt event: %+v", event)
		}
	default:
		t.Fatal("sender did not receive read receipt invalidation")
	}
	select {
	case event := <-reader.Events:
		if event.Name != "conversation.read" || event.LastReadSequence != 1 {
			t.Fatalf("own read event changed: %+v", event)
		}
	default:
		t.Fatal("reader did not receive own cursor")
	}
	if len(unrelated.Events) != 0 {
		t.Fatal("read receipt leaked to nonparticipant")
	}
	for _, sequence := range []int{1, 0} {
		if _, err = messaging.SetRead(app, b.Id, c.ID, sequence); err != nil {
			t.Fatal(err)
		}
	}
	if len(sender.Events) != 0 {
		t.Fatal("unchanged cursor must not notify peer")
	}

	inquiry, err := messaging.Create(app, a.Id, messaging.CreateInput{Kind: "inquiry", Inbox: messaging.Identity{Type: "team", ID: "musecat"}, InitialMessage: messaging.SendInput{Text: "help", ClientMessageID: "inquiry"}})
	if err != nil {
		t.Fatal(err)
	}
	for len(sender.Events) > 0 {
		<-sender.Events
	}
	if _, err = messaging.SetRead(app, outsider.Id, inquiry.ID, 1); err != nil {
		t.Fatal(err)
	}
	if len(sender.Events) != 0 {
		t.Fatal("inquiry must not expose responder reads")
	}
	if summary, err := messaging.Get(app, a.Id, inquiry.ID); err != nil || summary.CounterpartReadSequence != nil {
		t.Fatal("inquiry receipt must be null", summary, err)
	}
}
