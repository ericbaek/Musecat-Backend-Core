package arcade_test

import (
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/ericbaek/musecat-backend-core/service/messaging"
	"github.com/pocketbase/pocketbase/tools/filesystem"
)

func TestArcadeInquiryPersonalChatVisibilityRequiresExplicitMention(t *testing.T) {
	for _, guest := range []bool{false, true} {
		t.Run(fmt.Sprintf("guest=%t", guest), func(t *testing.T) {
			app := newArcadeTestApp(t)
			_, requester := createAuthUserWithTags(t, app, []string{})
			ownerToken, owner := createAuthUserWithTags(t, app, []string{"arcade_owner"})
			developerToken, developer := createAuthUserWithTags(t, app, []string{"developer"})
			moderatorToken, moderator := createAuthUserWithTags(t, app, []string{"moderator"})
			arcadeID, _ := seedPublicArcade(t, app, requester.Id, arcadeSeed{Name: "Private inquiry", Address: "Address", Location: location{Lat: 37.5, Lon: 127}})
			owner.Set("owns", []string{arcadeID})
			if err := app.Save(owner); err != nil {
				t.Fatal(err)
			}
			response := executeJSONRequest(t, app, "PUT", "/arcade/inquiry-settings", fmt.Sprintf(`{"arcade":%q,"mode":"internal"}`, arcadeID), map[string]string{"Authorization": "Bearer " + ownerToken})
			response.Body.Close()
			if response.StatusCode != 200 {
				t.Fatal("configure intake", response.StatusCode)
			}
			viewer := messaging.Principal{UserID: requester.Id}
			if guest {
				viewer = messaging.Principal{GuestKey: strings.Repeat("e", 64)}
			}
			photo, err := filesystem.NewFileFromBytes(append([]byte("GIF89a\x01\x00\x01\x00"), make([]byte, 64)...), "private.gif")
			if err != nil {
				t.Fatal(err)
			}
			input := messaging.CreateInput{Kind: "inquiry", Inbox: messaging.Identity{Type: "arcade", ID: arcadeID}, InitialMessage: messaging.SendInput{Text: "ordinary @뮤즈캣 @Musecat text", ClientMessageID: "initial", Photos: []*filesystem.File{photo}}}
			c, _, err := messaging.CreateFor(app, viewer, input)
			if err != nil {
				t.Fatal(err)
			}
			input.InitialMessage = messaging.SendInput{Text: "another private inquiry", ClientMessageID: "separate"}
			other, _, err := messaging.CreateFor(app, viewer, input)
			if err != nil {
				t.Fatal(err)
			}
			for _, token := range []string{developerToken, moderatorToken} {
				res := executeJSONRequest(t, app, "GET", "/user/feedback/thread?id="+c.ID, "", map[string]string{"Authorization": "Bearer " + token})
				res.Body.Close()
				if res.StatusCode != 200 {
					t.Fatal("dashboard staff cannot open inquiry", res.StatusCode)
				}
			}
			for _, id := range []string{developer.Id, moderator.Id} {
				listed, err := messaging.ListConversations(app, id, url.Values{})
				if err != nil || len(listed.Items) != 0 {
					t.Fatal("unmentioned inquiries leaked into staff personal chats", listed, err)
				}
				dashboard, err := messaging.ListConversations(app, id, url.Values{"inbox": {"arcade:" + arcadeID}})
				if err != nil || len(dashboard.Items) != 2 {
					t.Fatal("staff dashboard lost inquiries", dashboard, err)
				}
				contact, err := messaging.ListConversations(app, id, url.Values{"inbox": {"arcade:" + arcadeID}, "requesterOnly": {"true"}})
				if err != nil || len(contact.Items) != 0 {
					t.Fatal("contact entry exposed dashboard inquiries", contact, err)
				}
				if _, _, err := messaging.AttachmentPath(app, id, c.ID, c.LastMessage.ID, c.LastMessage.Photos[0].Name); err != nil {
					t.Fatal("dashboard staff cannot read attachment", err)
				}
			}
			personal, err := messaging.ListConversations(app, owner.Id, url.Values{})
			if err != nil || len(personal.Items) != 0 {
				t.Fatal("ordinary inquiries leaked into official personal chats", personal, err)
			}
			dashboard, err := messaging.ListConversations(app, owner.Id, url.Values{"inbox": {"arcade:" + arcadeID}})
			if err != nil || len(dashboard.Items) != 2 {
				t.Fatal("arcade dashboard lost its inquiries", dashboard, err)
			}
			if _, err := messaging.Get(app, owner.Id, c.ID); err != nil {
				t.Fatal("official lost access", err)
			}
			notification, err := app.FindFirstRecordByFilter("user_notification", "conversation={:c} && user={:u}", map[string]any{"c": c.ID, "u": owner.Id})
			if err != nil || messaging.NotificationItem(app, notification)["url"] != "/arcade/"+arcadeID {
				t.Fatal("ordinary inquiry notification must open arcade page", err)
			}
			if _, _, err := messaging.Send(app, developer.Id, c.ID, messaging.SendInput{Text: "dashboard answer", ClientMessageID: "dashboard-answer"}); err != nil {
				t.Fatal("dashboard reply failed", err)
			}
			for _, id := range []string{owner.Id, developer.Id} {
				list, err := messaging.ListConversations(app, id, url.Values{})
				if err != nil || len(list.Items) != 0 {
					t.Fatal("plain dashboard reply surfaced personal chat", list, err)
				}
			}
			sub := messaging.Events(app).Subscribe(developer.Id)
			defer messaging.Events(app).Unsubscribe(sub)
			if _, _, err := messaging.SendFor(app, viewer, c.ID, messaging.SendInput{Text: "plain follow-up", ClientMessageID: "plain"}); err != nil {
				t.Fatal(err)
			}
			if len(sub.Events) != 0 {
				t.Fatal("uninvited staff received SSE")
			}
			if _, _, err := messaging.SendFor(app, viewer, c.ID, messaging.SendInput{Text: "please help", ClientMessageID: "invite", Mentions: []string{"musecat"}}); err != nil {
				t.Fatal(err)
			}
			if len(sub.Events) == 0 {
				t.Fatal("invited staff missed SSE")
			}
			if messaging.NotificationItem(app, notification)["url"] != "/messages?conversation="+c.ID {
				t.Fatal("mentioned conversation notification route")
			}
			personal, err = messaging.ListConversations(app, owner.Id, url.Values{})
			if err != nil || len(personal.Items) != 1 || personal.Items[0].ID != c.ID {
				t.Fatal("invited conversation personal list", personal, err)
			}

			for _, id := range []string{developer.Id, moderator.Id} {
				listed, err := messaging.ListConversations(app, id, url.Values{})
				if err != nil || len(listed.Items) != 1 || listed.Items[0].ID != c.ID || !listed.Items[0].CanSend {
					t.Fatal("incorrect invited list", listed, err)
				}
				if _, err := messaging.Get(app, id, other.ID); err != nil {
					t.Fatal("dashboard cannot open other managed inquiry", err)
				}
				if _, _, err := messaging.AttachmentPath(app, id, c.ID, c.LastMessage.ID, c.LastMessage.Photos[0].Name); err != nil {
					t.Fatal("invited staff cannot read attachment", err)
				}
			}
			if _, _, err := messaging.Send(app, developer.Id, c.ID, messaging.SendInput{Text: "team answer", ClientMessageID: "answer"}); err != nil {
				t.Fatal(err)
			}
			if _, _, err := messaging.SendFor(app, viewer, c.ID, messaging.SendInput{Text: "later without mention", ClientMessageID: "later"}); err != nil {
				t.Fatal(err)
			}
			if _, err := messaging.Get(app, developer.Id, c.ID); err != nil {
				t.Fatal("invitation lost after follow-up", err)
			}
			developer.Set("tags", []string{})
			if err := app.Save(developer); err != nil {
				t.Fatal(err)
			}
			if _, err := messaging.Get(app, developer.Id, c.ID); err == nil {
				t.Fatal("revoked staff retained invitation access")
			}
		})
	}
}
