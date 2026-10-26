package admin

import (
	"github.com/ericbaek/musecat-backend-core/service/messaging"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/security"
)

func createMessagingFeedback(re *core.RequestEvent, body CreateSupportFeedbackBody) error {
	inbox := messaging.Identity{Type: "team", ID: "musecat"}
	if body.Arcade != "" {
		inbox = messaging.Identity{Type: "arcade", ID: body.Arcade}
	}
	s, err := messaging.Create(re.App, re.Auth.Id, messaging.CreateInput{Kind: "inquiry", Inbox: inbox, InitialMessage: messaging.SendInput{Text: body.Message, ClientMessageID: "legacy:" + security.RandomString(24), Photos: body.Photos, LegacyOriginal: true}})
	if err != nil {
		return err
	}
	item, err := messaging.LegacyItem(re.App, s)
	if err != nil {
		return err
	}
	return re.JSON(200, item)
}
func mappedFeedback(re *core.RequestEvent, id string) string {
	if re.Auth == nil {
		return ""
	}
	cid, _ := messaging.LegacyConversation(re.App, id)
	return cid
}
func messagingFeedbackPhoto(re *core.RequestEvent) (bool, error) {
	if re.Auth == nil {
		return false, nil
	}
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
	storage, file, err := messaging.AttachmentPath(re.App, re.Auth.Id, cid, mid, q.Get("file"))
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
