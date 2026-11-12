package arcade_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ericbaek/musecat-backend-core/service/messaging"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
)

type nativeEventFrame struct {
	name string
	data map[string]any
	raw  string
}
type nativeEventStream struct {
	frames <-chan nativeEventFrame
	done   <-chan struct{}
	cancel context.CancelFunc
	body   io.ReadCloser
}

func nativeEventServer(t *testing.T, app core.App) *httptest.Server {
	t.Helper()
	r, err := apis.NewRouter(app)
	if err != nil {
		t.Fatal(err)
	}
	if err = app.OnServe().Trigger(&core.ServeEvent{App: app, Router: r}, func(e *core.ServeEvent) error { return e.Next() }); err != nil {
		t.Fatal(err)
	}
	mux, err := r.BuildMux()
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}
func nativeEventOpen(t *testing.T, server *httptest.Server, token string) *nativeEventStream {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r, err := http.NewRequestWithContext(ctx, "GET", server.URL+"/user/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer "+token)
	response, err := server.Client().Do(r)
	if err != nil {
		cancel()
		t.Fatal("stream request failed")
	}
	if response.StatusCode != 200 {
		response.Body.Close()
		cancel()
		t.Fatalf("stream status %d", response.StatusCode)
	}
	if response.Header.Get("Cache-Control") != "private, no-store" || response.Header.Get("X-Accel-Buffering") != "no" || response.Header.Get("Content-Type") != "text/event-stream" {
		response.Body.Close()
		cancel()
		t.Fatal("shared stream headers changed")
	}
	frames := make(chan nativeEventFrame, 64)
	done := make(chan struct{})
	stream := &nativeEventStream{frames: frames, done: done, cancel: cancel, body: response.Body}
	t.Cleanup(func() { cancel(); response.Body.Close() })
	go func() {
		defer close(done)
		defer close(frames)
		reader := bufio.NewReader(response.Body)
		name, data := "", ""
		for {
			line, e := reader.ReadString('\n')
			if e != nil {
				return
			}
			line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
			if strings.HasPrefix(line, "event: ") {
				name = strings.TrimPrefix(line, "event: ")
			}
			if strings.HasPrefix(line, "data: ") {
				data = strings.TrimPrefix(line, "data: ")
			}
			if line == "" && name != "" {
				var value map[string]any
				if json.Unmarshal([]byte(data), &value) != nil {
					return
				}
				select {
				case frames <- nativeEventFrame{name: name, data: value, raw: data}:
				case <-ctx.Done():
					return
				}
				name, data = "", ""
			}
		}
	}()
	return stream
}
func (s *nativeEventStream) await(t *testing.T, name string, timeout time.Duration) []nativeEventFrame {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var all []nativeEventFrame
	for {
		select {
		case frame, ok := <-s.frames:
			if !ok {
				t.Fatal("stream closed before expected event")
				return nil
			}
			all = append(all, frame)
			if frame.name == name {
				return all
			}
		case <-timer.C:
			t.Fatal("missing expected stream event", name)
			return nil
		}
	}
}
func (s *nativeEventStream) closed(t *testing.T) {
	t.Helper()
	select {
	case <-s.done:
	case <-time.After(3 * time.Second):
		t.Fatal("stream did not close")
	}
}
func nativeEventStatus(t *testing.T, server *httptest.Server, path, token, cookie string, want int) *http.Response {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	r, err := http.NewRequestWithContext(ctx, "GET", server.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if cookie != "" {
		r.Header.Set("Cookie", "pb_auth="+cookie)
	}
	response, err := server.Client().Do(r)
	if err != nil {
		t.Fatal("status request failed")
	}
	t.Cleanup(func() { response.Body.Close() })
	if response.StatusCode != want {
		t.Fatalf("status %d want %d", response.StatusCode, want)
	}
	return response
}
func nativeEventWrite(t *testing.T, server *httptest.Server, token, path string, body any) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	r, err := http.NewRequest("POST", server.URL+path, strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	response, err := server.Client().Do(r)
	if err != nil {
		t.Fatal("canonical message request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != 201 {
		t.Fatalf("canonical send status %d", response.StatusCode)
	}
}
func TestCanonicalNativeMessagingEventsMatchWebCommittedFramesAndCancel(t *testing.T) {
	app := nativeNearbyApp(t, true)
	aWeb, a := createAuthUser(t, app)
	bWeb, b := createAuthUser(t, app)
	_, credentials := nativeNearbySession(t, app, b)
	room, err := messaging.Create(app, a.Id, messaging.CreateInput{Kind: "dm", PeerUserID: b.Id})
	if err != nil {
		t.Fatal(err)
	}
	server := nativeEventServer(t, app)
	web := nativeEventOpen(t, server, bWeb)
	native := nativeEventOpen(t, server, credentials.AccessToken)
	readyWeb, readyNative := web.await(t, "ready", 3*time.Second), native.await(t, "ready", 3*time.Second)
	exportNativeEventFrame(t, "ready", readyNative[len(readyNative)-1])
	if !reflect.DeepEqual(readyWeb, readyNative) {
		t.Fatal("ready Web/Native differs")
	}
	privateText := "Synthetic private text absent from events"
	nativeEventWrite(t, server, aWeb, "/user/conversations/"+room.ID+"/messages", map[string]any{"text": privateText, "clientMessageId": "sse-canonical-first"})
	webFrames := web.await(t, "conversation.changed", 3*time.Second)
	nativeFrames := native.await(t, "conversation.changed", 3*time.Second)
	if !reflect.DeepEqual(webFrames, nativeFrames) {
		t.Fatal("committed Web/Native event DTO differs")
	}
	event := nativeFrames[len(nativeFrames)-1]
	if event.data["id"] != room.ID || event.data["latestSequence"] != float64(1) {
		t.Fatal("canonical event sequence changed")
	}
	for _, frame := range nativeFrames {
		raw, _ := json.Marshal(frame.data)
		for _, secret := range []string{privateText, credentials.AccessToken, credentials.RefreshToken, aWeb, bWeb} {
			if strings.Contains(string(raw), secret) {
				t.Fatal("private value leaked in event")
			}
		}
	}
	exportNativeEventFrame(t, "conversation-changed", event)
	native.cancel()
	native.body.Close()
	native.closed(t)
	web.cancel()
	web.body.Close()
	web.closed(t)
	// Both transports release exactly the same per-user slots.
	hub := messaging.Events(app)
	deadline := time.Now().Add(time.Second)
	for {
		var subscriptions []*messaging.Subscription
		for range 4 {
			if sub := hub.Subscribe(b.Id); sub != nil {
				subscriptions = append(subscriptions, sub)
			}
		}
		for _, sub := range subscriptions {
			hub.Unsubscribe(sub)
		}
		if len(subscriptions) == 4 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cancel retained a subscription")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
func TestCanonicalNativeMessagingEventsRevalidateEachFrame(t *testing.T) {
	for _, scenario := range []string{"revoke", "refresh", "access-expired", "family-expired", "client-removed", "account-key", "withdrawn", "ban"} {
		t.Run(scenario, func(t *testing.T) {
			app := nativeNearbyApp(t, true)
			_, user := createAuthUser(t, app)
			service, credentials := nativeNearbySession(t, app, user)
			server := nativeEventServer(t, app)
			stream := nativeEventOpen(t, server, credentials.AccessToken)
			stream.await(t, "ready", 3*time.Second)
			switch scenario {
			case "revoke":
				if err := service.Revoke(app, credentials.RefreshToken); err != nil {
					t.Fatal(err)
				}
			case "refresh":
				rotated, err := service.Refresh(app, credentials.RefreshToken)
				if err != nil {
					t.Fatal(err)
				}
				if got, err := service.Authenticate(app, rotated.AccessToken); err != nil || got.Id != user.Id {
					t.Fatal("rotated credential invalid")
				}
			case "account-key":
				user.RefreshTokenKey()
				if err := app.Save(user); err != nil {
					t.Fatal(err)
				}
			case "withdrawn":
				user.Set("withdrawn", true)
				if err := app.Save(user); err != nil {
					t.Fatal(err)
				}
			case "ban":
				seedUserBan(t, app, user.Id, "", "native_event", time.Now().Add(time.Hour))
			default:
				change := map[string]string{"access-expired": "access_expires=0", "family-expired": "expires=0", "client-removed": "client='removed-client'"}[scenario]
				if _, err := app.DB().NewQuery("UPDATE native_auth_session SET " + change + " WHERE user_id={:user}").Bind(dbx.Params{"user": user.Id}).Execute(); err != nil {
					t.Fatal(err)
				}
			}
			messaging.Events(app).Publish(messaging.Event{Name: "resync", UserID: user.Id})
			frames := stream.await(t, "session.expired", 3*time.Second)
			if scenario == "revoke" {
				exportNativeEventFrame(t, "session-expired", frames[len(frames)-1])
			}
			for _, f := range frames {
				if f.name != "session.expired" {
					t.Fatal("revoked session received another data frame")
				}
			}
			stream.closed(t)
		})
	}
}
func TestCanonicalNativeMessagingEventsHeartbeatRevalidatesWithoutEvents(t *testing.T) {
	app := nativeNearbyApp(t, true)
	_, user := createAuthUser(t, app)
	service, credentials := nativeNearbySession(t, app, user)
	server := nativeEventServer(t, app)
	stream := nativeEventOpen(t, server, credentials.AccessToken)
	stream.await(t, "ready", 3*time.Second)
	if err := service.Revoke(app, credentials.RefreshToken); err != nil {
		t.Fatal(err)
	}
	// Revoke writes no Hub event. Exercise the unchanged real15-second heartbeat.
	stream.await(t, "session.expired", 18*time.Second)
	stream.closed(t)
}
func TestCanonicalNativeMessagingEventsDenyInvalidDisabledAndUnselectedRoutes(t *testing.T) {
	app := nativeNearbyApp(t, true)
	web, user := createAuthUser(t, app)
	service, credentials := nativeNearbySession(t, app, user)
	server := nativeEventServer(t, app)
	nativeEventStatus(t, server, "/user/events", "", "", 401)
	nativeEventStatus(t, server, "/user/events", strings.Repeat("z", 43), web, 401)
	nativeEventStatus(t, server, "/native/user/events", credentials.AccessToken, "", 404)
	nativeEventStatus(t, server, "/user/messaging/preferences", credentials.AccessToken, "", 401)
	nativeEventStatus(t, server, "/moderation/messages/reports", credentials.AccessToken, "", 401)
	nativeEventStatus(t, server, "/api/collections/messaging_message/records", credentials.AccessToken, "", 403)
	seedUserBan(t, app, user.Id, "", "native_event_entry", time.Now().Add(time.Hour))
	nativeEventStatus(t, server, "/user/events", credentials.AccessToken, "", 403)
	if err := service.Revoke(app, credentials.RefreshToken); err != nil {
		t.Fatal(err)
	}
	nativeEventStatus(t, server, "/user/events", credentials.AccessToken, "", 401)
	disabled := nativeNearbyApp(t, false)
	disabledWeb, owner := createAuthUser(t, disabled)
	s, issued := nativeNearbySession(t, disabled, owner)
	if got, err := s.Authenticate(disabled, issued.AccessToken); err != nil || got.Id != owner.Id {
		t.Fatal("same-DB disabled credential invalid before test")
	}
	disabledServer := nativeEventServer(t, disabled)
	nativeEventStatus(t, disabledServer, "/user/events", issued.AccessToken, "", 401)
	stream := nativeEventOpen(t, disabledServer, disabledWeb)
	stream.await(t, "ready", 3*time.Second)
}
func TestCanonicalNativeMessagingEventsKeepCurrentACLAndSharedLimits(t *testing.T) {
	app := nativeNearbyApp(t, true)
	_, a := createAuthUser(t, app)
	_, b := createAuthUser(t, app)
	staffWeb, staff := createAuthUserWithTags(t, app, []string{"moderator"})
	_, session := nativeNearbySession(t, app, staff)
	dm, err := messaging.Create(app, a.Id, messaging.CreateInput{Kind: "dm", PeerUserID: b.Id})
	if err != nil {
		t.Fatal(err)
	}
	server := nativeEventServer(t, app)
	native := nativeEventOpen(t, server, session.AccessToken)
	native.await(t, "ready", 3*time.Second)
	// Even a mistakenly overbroad Hub audience cannot override the current DM ACL.
	hub := messaging.Events(app)
	hub.Publish(messaging.Event{Name: "conversation.changed", ID: dm.ID, Audience: []string{staff.Id}})
	hub.Publish(messaging.Event{Name: "ready", UserID: staff.Id})
	for _, frame := range native.await(t, "ready", 3*time.Second) {
		if frame.name == "conversation.changed" {
			t.Fatal("staff bypassed DM ACL")
		}
	}
	streams := []*nativeEventStream{native}
	for _, token := range []string{staffWeb, session.AccessToken, staffWeb} {
		s := nativeEventOpen(t, server, token)
		s.await(t, "ready", 3*time.Second)
		streams = append(streams, s)
	}
	response := nativeEventStatus(t, server, "/user/events", session.AccessToken, "", 429)
	if response.Header.Get("Retry-After") != "15" {
		t.Fatal("stream limit retry changed")
	}
	for _, s := range streams {
		s.cancel()
		s.body.Close()
		s.closed(t)
	}
	app.Settings().RateLimits = core.RateLimitsConfig{Enabled: true, Rules: []core.RateLimitRule{{Label: "GET /user/events", MaxRequests: 1, Duration: 60}}}
	s := nativeEventOpen(t, server, session.AccessToken)
	s.await(t, "ready", 3*time.Second)
	nativeEventStatus(t, server, "/user/events", session.AccessToken, "", 429)
}

func exportNativeEventFrame(t *testing.T, name string, frame nativeEventFrame) {
	t.Helper()
	dir := os.Getenv("MUSECAT_CANONICAL_MESSAGING_EVENT_FIXTURES")
	if dir == "" {
		return
	}
	for key := range frame.data {
		if key != "id" && key != "latestSequence" && key != "lastReadSequence" && key != "reason" {
			t.Fatal("unexpected private event field")
		}
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "messaging-events-"+name+".sse"), []byte("event: "+frame.name+"\ndata: "+frame.raw+"\n\n"), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestCanonicalNativeMessagingEventsDropFormerOfficialInboxACL(t *testing.T) {
	app := nativeNearbyApp(t, true)
	_, requester := createAuthUser(t, app)
	_, owner := createAuthUserWithTags(t, app, []string{"arcade_owner"})
	arcade, _ := seedPublicArcade(t, app, requester.Id, arcadeSeed{Name: "Synthetic stream venue", Address: "Synthetic", Location: location{Lat: 37.5, Lon: 127}})
	owner.Set("owns", []string{arcade})
	if err := app.Save(owner); err != nil {
		t.Fatal(err)
	}
	venue, err := app.FindRecordById("arcade", arcade)
	if err != nil {
		t.Fatal(err)
	}
	venue.Set("inquiry_settings", map[string]any{"mode": "internal"})
	if err = app.Save(venue); err != nil {
		t.Fatal(err)
	}
	room, err := messaging.Create(app, requester.Id, messaging.CreateInput{Kind: "inquiry", Inbox: messaging.Identity{Type: "arcade", ID: arcade}, InitialMessage: messaging.SendInput{Text: "Synthetic inbox", ClientMessageID: "stream-inbox"}})
	if err != nil {
		t.Fatal(err)
	}
	_, credentials := nativeNearbySession(t, app, owner)
	server := nativeEventServer(t, app)
	stream := nativeEventOpen(t, server, credentials.AccessToken)
	stream.await(t, "ready", 3*time.Second)
	hub := messaging.Events(app)
	hub.Publish(messaging.Event{Name: "conversation.changed", ID: room.ID, Audience: []string{owner.Id}})
	stream.await(t, "conversation.changed", 3*time.Second)
	owner.Set("owns", []string{})
	if err = app.Save(owner); err != nil {
		t.Fatal(err)
	}
	hub.Publish(messaging.Event{Name: "conversation.changed", ID: room.ID, Audience: []string{owner.Id}})
	hub.Publish(messaging.Event{Name: "ready", UserID: owner.Id})
	for _, frame := range stream.await(t, "ready", 3*time.Second) {
		if frame.name == "conversation.changed" {
			t.Fatal("former official retained inbox event access")
		}
	}
}
