package arcade_test

import (
	"fmt"
	"github.com/ericbaek/musecat-backend-core/service/messaging"
	"strings"
	"testing"
)

func TestInquiryStatusHistoryAndSendingAfterResolution(t *testing.T) {
	for _, guest := range []bool{false, true} {
		for _, official := range []bool{false, true} {
			t.Run(fmt.Sprintf("guest=%t/official=%t", guest, official), func(t *testing.T) {
				app := newArcadeTestApp(t)
				_, requester := createAuthUserWithTags(t, app, []string{})
				tags := []string{"moderator"}
				inbox := messaging.Identity{Type: "team", ID: "musecat"}
				if official {
					tags = []string{"arcade_owner"}
				}
				_, responder := createAuthUserWithTags(t, app, tags)
				if official {
					arcadeID, _ := seedPublicArcade(t, app, requester.Id, arcadeSeed{Name: "Status history", Address: "Address", Location: location{Lat: 37.5, Lon: 127}})
					responder.Set("owns", []string{arcadeID})
					if err := app.Save(responder); err != nil {
						t.Fatal(err)
					}
					arcade, _ := app.FindRecordById("arcade", arcadeID)
					arcade.Set("inquiry_settings", map[string]any{"mode": "internal"})
					if err := app.Save(arcade); err != nil {
						t.Fatal(err)
					}
					inbox = messaging.Identity{Type: "arcade", ID: arcadeID}
				}
				viewer := messaging.Principal{UserID: requester.Id}
				if guest {
					viewer = messaging.Principal{GuestKey: strings.Repeat("d", 64)}
				}
				room, _, err := messaging.CreateFor(app, viewer, messaging.CreateInput{Kind: "inquiry", Inbox: inbox, InitialMessage: messaging.SendInput{Text: "Question", ClientMessageID: "initial"}})
				if err != nil {
					t.Fatal(err)
				}
				for _, status := range []string{"waiting", "recognised", "solved"} {
					if _, err := messaging.SetStatusFor(app, viewer, room.ID, status); err == nil {
						t.Fatal("requester mutated internal status")
					}
				}
				subscription := messaging.Events(app).Subscribe(room.Requester.ID)
				defer messaging.Events(app).Unsubscribe(subscription)
				room, err = messaging.SetStatus(app, responder.Id, room.ID, "solved")
				if err != nil || room.LatestSequence != 2 || !room.CanSend {
					t.Fatal(room, err)
				}
				if len(subscription.Events) != 0 {
					t.Fatal("internal status event reached requester")
				}
				adminViewer := messaging.Principal{UserID: responder.Id}
				history, err := messaging.HistoryFor(app, adminViewer, room.ID, 0, -1, 50)
				if err != nil || len(history.Items) != 2 {
					t.Fatal(history, err)
				}
				event := history.Items[1]
				change := event.StatusChange
				if change == nil || change.From != "waiting" || change.To != "solved" || event.SenderUserID != responder.Id || event.ActingAs.Type != inbox.Type {
					t.Fatal("missing attributed history", room.LastMessage)
				}
				// Retrying the same action and rejected requester writes leave no extra rows.
				if same, err := messaging.SetStatus(app, responder.Id, room.ID, "solved"); err != nil || same.LatestSequence != 2 {
					t.Fatal(same, err)
				}
				if _, err := messaging.SetStatusFor(app, viewer, room.ID, "solved"); err == nil {
					t.Fatal("requester bypassed permission on same status")
				}
				current, err := messaging.GetFor(app, viewer, room.ID)
				if err != nil || !current.CanSend || current.Status != nil || current.CanManageStatus || len(current.AllowedStatusTransitions) != 0 || current.LatestSequence != 1 || current.UnreadCount != 0 || current.LastMessage.Sequence != 1 || current.LastMessage.StatusChange != nil {
					t.Fatal("completed composer disabled", current, err)
				}
				input := messaging.SendInput{Text: "Follow-up after completion", ClientMessageID: "follow-up"}
				sent, _, err := messaging.SendFor(app, viewer, room.ID, input)
				if err != nil || sent.StatusChange != nil {
					t.Fatal("completed requester send failed", sent, err)
				}
				if _, replayed, err := messaging.SendFor(app, viewer, room.ID, input); err != nil || !replayed {
					t.Fatal("retry duplicated history", err)
				}
				if _, err := messaging.SetStatus(app, responder.Id, room.ID, "solved"); err != nil {
					t.Fatal(err)
				}
				sent, _, err = messaging.Send(app, responder.Id, room.ID, messaging.SendInput{Text: "Responder follow-up", ClientMessageID: "reply"})
				if err != nil || sent.StatusChange == nil || sent.StatusChange.To != "recognised" {
					t.Fatal("completed responder send failed", sent, err)
				}
				page, err := messaging.HistoryFor(app, adminViewer, room.ID, 0, -1, 50)
				if err != nil || len(page.Items) != 5 {
					t.Fatal("history not durable", page, err)
				}
				if page.Items[1].StatusChange == nil || page.Items[3].StatusChange == nil {
					t.Fatal("explicit transition lost on reload", page)
				}
				public, err := messaging.HistoryFor(app, viewer, room.ID, 0, -1, 1)
				if err != nil || len(public.Items) != 1 || public.Items[0].Text != "Responder follow-up" || public.Items[0].StatusChange != nil {
					t.Fatal("internal history exposed", public, err)
				}
				for cursor := public.NextBeforeSequence; cursor != nil; {
					public, err = messaging.HistoryFor(app, viewer, room.ID, *cursor, -1, 1)
					if err != nil || len(public.Items) != 1 || public.Items[0].Text == "" || public.Items[0].StatusChange != nil {
						t.Fatal("internal history exposed through pagination", public, err)
					}
					cursor = public.NextBeforeSequence
				}
				legacy, err := messaging.LegacyThreadFor(app, viewer, room.ID, "")
				if err != nil || legacy["feedback"].(map[string]any)["status"] != nil {
					t.Fatal("legacy status exposed", legacy, err)
				}
				for i, item := range page.Items {
					if item.Sequence != i+1 {
						t.Fatal("unordered history", page)
					}
				}
			})
		}
	}
}

func TestInquiryRequesterReplyKeepsInProgressStatus(t *testing.T) {
	app := newArcadeTestApp(t)
	_, requester := createAuthUserWithTags(t, app, []string{})
	_, responder := createAuthUserWithTags(t, app, []string{"moderator"})
	viewer := messaging.Principal{UserID: requester.Id}
	admin := messaging.Principal{UserID: responder.Id}
	inbox := messaging.Identity{Type: "team", ID: "musecat"}
	room, _, err := messaging.CreateFor(app, viewer, messaging.CreateInput{Kind: "inquiry", Inbox: inbox, InitialMessage: messaging.SendInput{Text: "Question", ClientMessageID: "initial"}})
	if err != nil {
		t.Fatal(err)
	}
	status := func() string {
		t.Helper()
		current, err := messaging.GetFor(app, admin, room.ID)
		if err != nil || current.Status == nil {
			t.Fatal(current, err)
		}
		return *current.Status
	}
	lastChange := func() *messaging.InquiryStatusChange {
		t.Helper()
		page, err := messaging.HistoryFor(app, admin, room.ID, 0, -1, 50)
		if err != nil || len(page.Items) == 0 {
			t.Fatal(page, err)
		}
		return page.Items[len(page.Items)-1].StatusChange
	}
	if status() != "waiting" {
		t.Fatal("new inquiry should wait for a reply")
	}
	if _, _, err := messaging.Send(app, responder.Id, room.ID, messaging.SendInput{Text: "Looking into it", ClientMessageID: "staff-1"}); err != nil {
		t.Fatal(err)
	}
	if status() != "recognised" {
		t.Fatal("responder reply should mark the inquiry in progress")
	}
	for i := range 2 {
		if _, _, err := messaging.SendFor(app, viewer, room.ID, messaging.SendInput{Text: "Thanks", ClientMessageID: fmt.Sprintf("thanks-%d", i)}); err != nil {
			t.Fatal(err)
		}
		if status() != "recognised" || lastChange() != nil {
			t.Fatal("requester reply bounced an in-progress inquiry")
		}
		if _, _, err := messaging.Send(app, responder.Id, room.ID, messaging.SendInput{Text: "Sure", ClientMessageID: fmt.Sprintf("staff-ack-%d", i)}); err != nil {
			t.Fatal(err)
		}
		if lastChange() != nil {
			t.Fatal("responder reply re-recorded an unchanged status")
		}
	}
	if _, err := messaging.SetStatus(app, responder.Id, room.ID, "solved"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := messaging.SendFor(app, viewer, room.ID, messaging.SendInput{Text: "One more thing", ClientMessageID: "reopen"}); err != nil {
		t.Fatal(err)
	}
	if change := lastChange(); status() != "waiting" || change == nil || change.From != "solved" || change.To != "waiting" {
		t.Fatal("requester reply should reopen a resolved inquiry", change)
	}
}
