package nativeauth

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/ericbaek/musecat-backend-core/handlers/arcade/basic"
	"github.com/ericbaek/musecat-backend-core/handlers/arcade/gtk"
	"github.com/ericbaek/musecat-backend-core/handlers/arcade/hour"
	"github.com/ericbaek/musecat-backend-core/handlers/arcade/sns"
	user "github.com/ericbaek/musecat-backend-core/handlers/user"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
)

func RegisterRoutes(se *core.ServeEvent, clients []Client) {
	if len(clients) == 0 {
		return
	}
	s := Service{Clients: clients}
	headers := &hook.Handler[*core.RequestEvent]{Func: func(e *core.RequestEvent) error {
		e.Response.Header().Set("Cache-Control", "no-store")
		return e.Next()
	}}
	g := se.Router.Group("/native").Bind(apis.BodyLimit(4096), headers)
	g.POST("/session/code", func(e *core.RequestEvent) error {
		var in struct {
			Client    string `json:"client_id"`
			Callback  string `json:"redirect_uri"`
			Challenge string `json:"code_challenge"`
		}
		if err := json.NewDecoder(e.Request.Body).Decode(&in); err != nil {
			return e.BadRequestError("Invalid authorization request", nil)
		}
		code, err := s.IssueCode(e.App, e.Auth, in.Client, in.Callback, in.Challenge)
		if err != nil {
			return e.BadRequestError("Invalid authorization request", nil)
		}
		return e.JSON(200, map[string]string{"code": code})
	}).Bind(apis.RequireAuth("user"), user.RequireActiveUser())
	g.POST("/session/exchange", func(e *core.RequestEvent) error {
		var in struct {
			Code     string `json:"code"`
			Verifier string `json:"code_verifier"`
			Client   string `json:"client_id"`
			Callback string `json:"redirect_uri"`
		}
		if err := json.NewDecoder(e.Request.Body).Decode(&in); err != nil {
			return e.BadRequestError("Invalid exchange", nil)
		}
		out, err := s.Exchange(e.App, in.Code, in.Verifier, in.Client, in.Callback)
		if err != nil {
			return e.UnauthorizedError("Invalid or expired authorization", nil)
		}
		return activeCredentials(e, s, out)
	})
	g.POST("/session/refresh", func(e *core.RequestEvent) error {
		var in struct {
			Refresh string `json:"refresh_token"`
		}
		if err := json.NewDecoder(e.Request.Body).Decode(&in); err != nil {
			return e.BadRequestError("Invalid refresh", nil)
		}
		out, err := s.Refresh(e.App, in.Refresh)
		if err != nil {
			return e.UnauthorizedError("Session expired", nil)
		}
		return activeCredentials(e, s, out)
	})
	g.POST("/session/revoke", func(e *core.RequestEvent) error {
		var in struct {
			Refresh string `json:"refresh_token"`
		}
		if err := json.NewDecoder(e.Request.Body).Decode(&in); err != nil {
			return e.BadRequestError("Invalid revoke", nil)
		}
		if err := s.Revoke(e.App, in.Refresh); err != nil {
			return e.BadRequestError("Invalid revoke", nil)
		}
		return e.NoContent(204)
	})
	guard := &hook.Handler[*core.RequestEvent]{Func: func(e *core.RequestEvent) error {
		auth := e.Request.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			return e.UnauthorizedError("Native session required", nil)
		}
		u, err := s.Authenticate(e.App, strings.TrimPrefix(auth, "Bearer "))
		if err != nil {
			return e.UnauthorizedError("Native session required", nil)
		}
		e.Auth = u
		return e.Next()
	}}
	secured := g.Group("").Bind(guard, user.RequireActiveUser())
	secured.GET("/v1/arcades/{id}/basic", basic.GetNativeBasicV1)
	secured.PATCH("/v1/arcades/{id}/basic", basic.PatchNativeBasicV1)
	secured.GET("/v1/arcades/{id}/contacts", sns.GetNativeSNSV1)
	secured.PATCH("/v1/arcades/{id}/contacts", sns.PatchNativeSNSV1)
	secured.GET("/v1/arcades/{id}/hours", hour.GetNativeHourV1)
	secured.PATCH("/v1/arcades/{id}/hours", hour.PatchNativeHourV1)
	secured.GET("/v1/arcades/{id}/gtk", gtk.GetNativeGTKV1)
	secured.PATCH("/v1/arcades/{id}/gtk", gtk.PatchNativeGTKV1)
	secured.GET("/user/me", user.GetMe)
	secured.GET("/user/visits", user.GetMyVisits)
	secured.GET("/user/passport", user.GetMyPassport)
	secured.PUT("/arcade/favorite", user.UpdateArcadeFavorite)
	secured.PUT("/arcade/favorite/order", user.UpdateArcadeFavoriteOrder)
	secured.POST("/arcade/visit", user.VisitArcade)
}
func activeCredentials(e *core.RequestEvent, s Service, out Credentials) error {
	u, err := s.Authenticate(e.App, out.AccessToken)
	if err != nil {
		return e.UnauthorizedError("Invalid session", nil)
	}
	e.Auth = u
	// Reuse the same account-ban/withdrawal guard before returning any credentials.
	guard := user.RequireActiveUser()
	h := hook.Hook[*core.RequestEvent]{}
	h.Bind(guard)
	return h.Trigger(e, func(e *core.RequestEvent) error { return e.JSON(http.StatusOK, out) })
}
