package admin

import (
	"crypto/rand"
	"encoding/hex"
	"github.com/ericbaek/musecat-backend-core/service/messaging"
	"github.com/ericbaek/musecat-backend-core/service/supportfeedback"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/security"
)

func createMessagingFeedback(re *core.RequestEvent, body CreateSupportFeedbackBody) error {
	if re.Auth == nil && re.Request.Header.Get(supportfeedback.GuestHeader) == "" {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return err
		}
		re.Request.Header.Set(supportfeedback.GuestHeader, hex.EncodeToString(key))
	}

	inbox := messaging.Identity{Type: "team", ID: "musecat"}
	if body.Arcade != "" {
		inbox = messaging.Identity{Type: "arcade", ID: body.Arcade}
	}
	s, _, err := messaging.CreateFor(re.App, feedbackPrincipal(re), messaging.CreateInput{Kind: "inquiry", Inbox: inbox, InitialMessage: messaging.SendInput{Text: body.Message, ClientMessageID: "legacy:" + security.RandomString(24), Photos: body.Photos, LegacyOriginal: true}})
	if err != nil {
		return err
	}
	item, err := messaging.LegacyItem(re.App, s)
	if err != nil {
		return err
	}
	return re.JSON(200, item)
}
func feedbackPrincipal(re *core.RequestEvent) messaging.Principal {
	if re.Auth != nil {
		return messaging.Principal{UserID: re.Auth.Id}
	}
	return messaging.Principal{GuestKey: re.Request.Header.Get(supportfeedback.GuestHeader)}
}
func mappedFeedback(re *core.RequestEvent, id string) string {
	if cid, err := messaging.LegacyConversation(re.App, id); err == nil {
		return cid
	}
	return id
}
func messagingFeedbackPhoto(re *core.RequestEvent) (bool, error) {
	q := re.Request.URL.Query()
	cid, mid := "", ""
	if id := q.Get("feedbackId"); id != "" {
		cid = mappedFeedback(re, id)
		if cid != "" {
			m, err := re.App.FindFirstRecordByFilter(messaging.Messages, "conversation={:c} && sequence=1", dbx.Params{"c": cid})
			if err != nil {
				return true, re.NotFoundError("Photo not found.", nil)
			}
			mid = m.Id
		}
	} else if id := q.Get("id"); id != "" {
		m, err := re.App.FindFirstRecordByFilter(messaging.Messages, "id={:id} || legacy_message_id={:id}", dbx.Params{"id": id})
		if err == nil {
			cid, mid = m.GetString("conversation"), m.Id
		}
	}
	if cid == "" {
		return false, nil
	}
	storage, file, err := messaging.AttachmentPathFor(re.App, feedbackPrincipal(re), cid, mid, q.Get("file"))
	if err != nil {
		return true, err
	}
	fs, err := re.App.NewFilesystem()
	if err != nil {
		return true, err
	}
	defer fs.Close()
	re.Response.Header().Set("Cache-Control", "private, no-store")
	re.Response.Header().Set("X-Content-Type-Options", "nosniff")
	return true, fs.Serve(re.Response, re.Request, storage, file)
}
