package nativeauth

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	user "github.com/ericbaek/musecat-backend-core/handlers/user"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
	"github.com/pocketbase/pocketbase/tools/router"
)

// RegisterRoutes returns the active-native-session group for domain route composition.
// A disabled client allowlist returns nil and installs no routes.
func RegisterRoutes(se *core.ServeEvent, clients []Client, webClients ...WebClient) *router.RouterGroup[*core.RequestEvent] {
	if len(clients) == 0 {
		return nil
	}
	s := Service{Clients: clients, WebClients: webClients}
	headers := &hook.Handler[*core.RequestEvent]{Id: "nativeNoStore", Priority: apis.DefaultRateLimitMiddlewarePriority - 1, Func: func(e *core.RequestEvent) error {
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
	secured.GET("/user/me", user.GetMe)
	if len(webClients) > 0 {
		secured.POST("/session/web/code", func(e *core.RequestEvent) error {
			var in struct {
				Client    string `json:"client_id"`
				Issuer    string `json:"issuer"`
				Challenge string `json:"code_challenge"`
			}
			if err := json.NewDecoder(e.Request.Body).Decode(&in); err != nil || !challengePattern.MatchString(in.Challenge) {
				return e.BadRequestError("Invalid web authorization request", nil)
			}
			out, err := s.IssueWebCode(e.App, strings.TrimPrefix(e.Request.Header.Get("Authorization"), "Bearer "), in.Client, in.Issuer, in.Challenge)
			if err != nil {
				return webHandoffError(e, err)
			}
			return e.JSON(200, out)
		})
		g.POST("/session/web/exchange", func(e *core.RequestEvent) error {
			client, secret, basic := e.Request.BasicAuth()
			if !basic {
				return e.UnauthorizedError("Web server authentication required", nil)
			}
			var in struct {
				Code     string `json:"code"`
				Verifier string `json:"code_verifier"`
				Client   string `json:"client_id"`
				Issuer   string `json:"issuer"`
			}
			if err := json.NewDecoder(e.Request.Body).Decode(&in); err != nil {
				return e.BadRequestError("Invalid web exchange", nil)
			}
			if client != in.Client {
				return e.UnauthorizedError("Invalid web server authentication", nil)
			}
			out, err := s.ExchangeWebCode(e.App, in.Code, in.Verifier, in.Client, in.Issuer, secret)
			if err != nil {
				return webHandoffError(e, err)
			}
			return e.JSON(200, out)
		})
	}
	return secured
}
func webHandoffError(e *core.RequestEvent, err error) error {
	var forbidden *router.ApiError
	if errors.As(err, &forbidden) && forbidden.Status == http.StatusForbidden {
		return forbidden
	}
	return e.UnauthorizedError("Invalid or expired web authorization", nil)
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
