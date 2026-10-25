package messaging

import (
	"encoding/json"
	"errors"
	"fmt"
	userhandler "github.com/ericbaek/musecat-backend-core/handlers/user"
	"github.com/ericbaek/musecat-backend-core/service/messaging"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/router"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const MaxBodyBytes = 3*15_000_000 + (1 << 20)

func RegisterRoutes(se *core.ServeEvent) {
	review := se.Router.Group("/moderation/messages/reports").Bind(apis.RequireAuth("user"), userhandler.RequireActiveUser())
	review.GET("", ReviewReports)
	review.GET("/{id}", ReviewReport)
	review.GET("/{id}/attachments/{file}", ReviewPhoto)
	api := se.Router.Group("/user").Bind(apis.RequireAuth("user"), userhandler.RequireActiveUser())
	api.GET("/conversations", List)
	api.POST("/conversations", Create).Bind(apis.BodyLimit(MaxBodyBytes))
	api.GET("/conversations/{id}", Get)
	api.GET("/conversations/{id}/messages", History)
	api.POST("/conversations/{id}/messages", Send).Bind(apis.BodyLimit(MaxBodyBytes))
	api.PUT("/conversations/{id}/read", Read).Bind(apis.BodyLimit(4096))
	api.PATCH("/conversations/{id}/inquiry", Status).Bind(apis.BodyLimit(4096))
	api.GET("/conversations/{id}/attachments/{messageId}/{file}", Photo)
	api.GET("/messaging/preferences", Preferences)
	api.PUT("/messaging/preferences", SetPreferences).Bind(apis.BodyLimit(4096))
	api.GET("/messaging/blocks", Blocks)
	api.PUT("/messaging/blocks/{userId}", Block)
	api.DELETE("/messaging/blocks/{userId}", Unblock)
	api.GET("/messaging/recipients", Recipients)
	api.POST("/messages/{messageId}/report", Report).Bind(apis.BodyLimit(8192))
	api.GET("/events", Events)
	if se.App.Store().Get("messaging.hooks") == nil {
		messaging.RegisterEvents(se.App)
		se.App.Store().Set("messaging.hooks", true)
	}
}
func reply(re *core.RequestEvent, status int, v any, err error) error {
	re.Response.Header().Set("Cache-Control", "private, no-store")
	if err != nil {
		var api *router.ApiError
		if errors.As(err, &api) && api.Status == 429 {
			re.Response.Header().Set("Retry-After", "60")
		}
		return err
	}
	return re.JSON(status, v)
}
func List(re *core.RequestEvent) error {
	out, err := messaging.ListConversations(re.App, re.Auth.Id, re.Request.URL.Query())
	return reply(re, 200, out, err)
}
func Get(re *core.RequestEvent) error {
	out, err := messaging.Get(re.App, re.Auth.Id, re.Request.PathValue("id"))
	return reply(re, 200, out, err)
}
func parseSend(re *core.RequestEvent) (messaging.SendInput, error) {
	var in messaging.SendInput
	if !strings.HasPrefix(strings.ToLower(re.Request.Header.Get("Content-Type")), "multipart/form-data") {
		return in, re.BindBody(&in)
	}
	if err := re.Request.ParseMultipartForm(1 << 20); err != nil {
		return in, err
	}
	in.Text = re.Request.FormValue("text")
	in.ClientMessageID = re.Request.FormValue("clientMessageId")
	if v := re.Request.FormValue("actingAs"); v != "" {
		if err := json.Unmarshal([]byte(v), &in.ActingAs); err != nil {
			return in, err
		}
	}
	files, err := re.FindUploadedFiles("photos")
	if errors.Is(err, http.ErrMissingFile) {
		err = nil
	}
	in.Photos = files
	return in, err
}
func cleanup(re *core.RequestEvent) {
	if re.Request.MultipartForm != nil {
		_ = re.Request.MultipartForm.RemoveAll()
	}
}
func Create(re *core.RequestEvent) error {
	var in messaging.CreateInput
	var err error
	if strings.HasPrefix(strings.ToLower(re.Request.Header.Get("Content-Type")), "multipart/form-data") {
		in.InitialMessage, err = parseSend(re)
		in.Kind = re.Request.FormValue("kind")
		in.PeerUserID = re.Request.FormValue("peerUserId")
		in.Inbox = messaging.Identity{Type: re.Request.FormValue("inboxType"), ID: re.Request.FormValue("inboxId")}
	} else {
		err = re.BindBody(&in)
	}
	defer cleanup(re)
	if err != nil {
		return re.BadRequestError("Invalid conversation.", err)
	}
	out, replay, err := messaging.CreateWithReplay(re.App, re.Auth.Id, in)
	status := 201
	if in.Kind == "dm" || replay {
		status = 200
	}
	return reply(re, status, out, err)
}
func Send(re *core.RequestEvent) error {
	in, err := parseSend(re)
	defer cleanup(re)
	if err != nil {
		return re.BadRequestError("Invalid message.", err)
	}
	out, replay, err := messaging.Send(re.App, re.Auth.Id, re.Request.PathValue("id"), in)
	status := 201
	if replay {
		status = 200
	}
	return reply(re, status, out, err)
}
func History(re *core.RequestEvent) error {
	q := re.Request.URL.Query()
	before, after, limit := 0, -1, 50
	var err error
	if q.Has("beforeSequence") && q.Has("afterSequence") {
		return re.BadRequestError("Choose one pagination direction.", nil)
	}
	for key, dest := range map[string]*int{"beforeSequence": &before, "afterSequence": &after, "limit": &limit} {
		if q.Has(key) {
			*dest, err = strconv.Atoi(q.Get(key))
			if err != nil || *dest < 0 {
				return re.BadRequestError("Invalid pagination.", nil)
			}
		}
	}
	out, err := messaging.History(re.App, re.Auth.Id, re.Request.PathValue("id"), before, after, limit)
	return reply(re, 200, out, err)
}
func Read(re *core.RequestEvent) error {
	var in struct {
		ThroughSequence *int `json:"throughSequence"`
	}
	if err := re.BindBody(&in); err != nil || in.ThroughSequence == nil {
		return re.BadRequestError("Invalid read cursor.", nil)
	}
	out, err := messaging.SetRead(re.App, re.Auth.Id, re.Request.PathValue("id"), *in.ThroughSequence)
	return reply(re, 200, out, err)
}
func Status(re *core.RequestEvent) error {
	var in struct {
		Status string `json:"status"`
	}
	if err := re.BindBody(&in); err != nil {
		return re.BadRequestError("Invalid status.", nil)
	}
	out, err := messaging.SetStatus(re.App, re.Auth.Id, re.Request.PathValue("id"), in.Status)
	return reply(re, 200, out, err)
}
func Preferences(re *core.RequestEvent) error {
	return reply(re, 200, map[string]any{"receive": messaging.Receive(re.App, re.Auth.Id)}, nil)
}
func SetPreferences(re *core.RequestEvent) error {
	var in struct {
		Receive string `json:"receive"`
	}
	if err := re.BindBody(&in); err != nil {
		return re.BadRequestError("Invalid preferences.", nil)
	}
	err := messaging.Preference(re.App, re.Auth.Id, in.Receive)
	return reply(re, 200, in, err)
}
func Blocks(re *core.RequestEvent) error {
	items, err := messaging.BlockList(re.App, re.Auth.Id)
	return reply(re, 200, map[string]any{"items": items}, err)
}
func Block(re *core.RequestEvent) error {
	if err := messaging.Block(re.App, re.Auth.Id, re.Request.PathValue("userId"), false); err != nil {
		return reply(re, 204, nil, err)
	}
	return re.NoContent(204)
}
func Unblock(re *core.RequestEvent) error {
	if err := messaging.Block(re.App, re.Auth.Id, re.Request.PathValue("userId"), true); err != nil {
		return err
	}
	return re.NoContent(204)
}
func Recipients(re *core.RequestEvent) error {
	limit := 10
	var err error
	if q := re.Request.URL.Query().Get("limit"); q != "" {
		limit, err = strconv.Atoi(q)
		if err != nil {
			return re.BadRequestError("Invalid limit.", nil)
		}
	}
	items, err := messaging.Recipients(re.App, re.Auth.Id, re.Request.URL.Query().Get("q"), limit)
	return reply(re, 200, map[string]any{"items": items}, err)
}
func Report(re *core.RequestEvent) error {
	var in struct {
		Reason string `json:"reason"`
	}
	if err := re.BindBody(&in); err != nil {
		return re.BadRequestError("Invalid report.", nil)
	}
	id, exists, err := messaging.Report(re.App, re.Auth.Id, re.Request.PathValue("messageId"), in.Reason)
	status := 201
	if exists {
		status = 200
	}
	return reply(re, status, map[string]any{"id": id}, err)
}
func Photo(re *core.RequestEvent) error {
	storage, filename, err := messaging.AttachmentPath(re.App, re.Auth.Id, re.Request.PathValue("id"), re.Request.PathValue("messageId"), re.Request.PathValue("file"))
	if err != nil {
		return err
	}
	fs, err := re.App.NewFilesystem()
	if err != nil {
		return err
	}
	defer fs.Close()
	re.Response.Header().Set("Cache-Control", "private, no-store")
	re.Response.Header().Set("X-Content-Type-Options", "nosniff")
	return fs.Serve(re.Response, re.Request, storage, filename)
}
func Events(re *core.RequestEvent) error {
	hub := messaging.Events(re.App)
	sub := hub.Subscribe(re.Auth.Id)
	if sub == nil {
		re.Response.Header().Set("Retry-After", "15")
		return router.NewApiError(429, "Too many streams.", nil)
	}
	defer hub.Unsubscribe(sub)
	re.Response.Header().Set("Content-Type", "text/event-stream")
	re.Response.Header().Set("Cache-Control", "private, no-store")
	re.Response.Header().Set("X-Accel-Buffering", "no")
	re.Response.Header().Set("Connection", "keep-alive")
	controller := http.NewResponseController(re.Response)
	write := func(event messaging.Event) error {
		_ = controller.SetWriteDeadline(time.Now().Add(10 * time.Second))
		raw, _ := json.Marshal(event)
		if _, err := fmt.Fprintf(re.Response, "event: %s\ndata: %s\n\n", event.Name, raw); err != nil {
			return err
		}
		return controller.Flush()
	}
	valid := func() bool {
		token := strings.TrimPrefix(re.Request.Header.Get("Authorization"), "Bearer ")
		u, err := re.App.FindAuthRecordByToken(token, core.TokenTypeAuth)
		if err != nil || u.Id != re.Auth.Id {
			return false
		}
		_, err = messaging.ActiveUser(re.App, u.Id)
		return err == nil
	}
	if err := write(messaging.Event{Name: "ready"}); err != nil {
		return nil
	}
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	lifetime := time.NewTimer(25 * time.Minute)
	defer lifetime.Stop()
	for {
		select {
		case <-re.Request.Context().Done():
			return nil
		case <-lifetime.C:
			return nil
		case <-sub.Overflow:
			_ = write(messaging.Event{Name: "resync"})
			return nil
		case <-heartbeat.C:
			if !valid() {
				_ = write(messaging.Event{Name: "session.expired"})
				return nil
			}
			_ = controller.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if _, err := fmt.Fprint(re.Response, ": heartbeat\n\n"); err != nil {
				return nil
			}
			if err := controller.Flush(); err != nil {
				return nil
			}
		case event := <-sub.Events:
			if !valid() {
				_ = write(messaging.Event{Name: "session.expired"})
				return nil
			}
			if event.ID != "" {
				if _, _, err := messaging.Access(re.App, re.Auth.Id, event.ID); err != nil {
					continue
				}
			}
			if err := write(event); err != nil {
				return nil
			}
		}
	}
}
