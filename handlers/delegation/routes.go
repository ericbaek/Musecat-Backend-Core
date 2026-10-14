package delegation

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/ericbaek/musecat-backend-core/delegation"
	apidocs "github.com/ericbaek/musecat-backend-core/docs"
	campaign "github.com/ericbaek/musecat-backend-core/handlers/arcade/campaign"
	flag "github.com/ericbaek/musecat-backend-core/handlers/arcade/flag"
	query "github.com/ericbaek/musecat-backend-core/handlers/arcade/query"
	"github.com/ericbaek/musecat-backend-core/handlers/user"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
)

func respond(re *core.RequestEvent, err error) error {
	var oe *delegation.OAuthError
	if errors.As(err, &oe) {
		if oe.Status == 401 {
			challenge := `Bearer realm="Musecat"`
			if oe.Code == "invalid_client" {
				challenge = `Basic realm="Musecat OAuth"`
			}
			re.Response.Header().Set("WWW-Authenticate", challenge)
		}
		return re.JSON(oe.Status, map[string]string{"error": oe.Code})
	}
	if errors.Is(err, sql.ErrNoRows) {
		return re.JSON(404, map[string]string{"error": "not_found"})
	}
	return re.JSON(500, map[string]string{"error": "server_error"})
}
func invalid(re *core.RequestEvent) error {
	return re.JSON(400, map[string]string{"error": "invalid_request"})
}
func noStore(re *core.RequestEvent) error {
	re.Response.Header().Set("Cache-Control", "private, no-store")
	re.Response.Header().Set("Pragma", "no-cache")
	re.Response.Header().Set("Referrer-Policy", "no-referrer")
	return re.Next()
}
func requireSession(re *core.RequestEvent) error {
	if re.Auth == nil || re.Auth.Collection().Name != "user" {
		return respond(re, &delegation.OAuthError{Code: "access_denied", Status: 401})
	}
	if err := user.CheckActiveUser(re.App, re.Auth); err != nil {
		return respond(re, &delegation.OAuthError{Code: "access_denied", Status: 403})
	}
	return re.Next()
}
func actor(re *core.RequestEvent) delegation.Actor {
	return delegation.Actor{UserID: re.Auth.Id, Session: bearer(re.Request)}
}
func bearer(r *http.Request) string {
	if len(r.Header.Values("Authorization")) != 1 {
		return ""
	}
	p := strings.Fields(r.Header.Get("Authorization"))
	if len(p) != 2 || !strings.EqualFold(p[0], "Bearer") {
		return ""
	}
	return p[1]
}
func strictJSON(re *core.RequestEvent, v any) error {
	// PocketBase rewinds request bodies on EOF. Decode a bounded snapshot so
	// a final (n, EOF) read cannot make the trailing-data check replay the body.
	raw, err := io.ReadAll(io.LimitReader(re.Request.Body, 16385))
	if err != nil {
		return err
	}
	if len(raw) > 16384 {
		return apis.ErrRequestEntityTooLarge
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return e
	}
	if e := d.Decode(new(any)); e != io.EOF {
		return errors.New("invalid JSON")
	}
	return nil
}
func tokenForm(re *core.RequestEvent, allowed ...string) (url.Values, error) {
	mt, _, e := mime.ParseMediaType(re.Request.Header.Get("Content-Type"))
	if e != nil || mt != "application/x-www-form-urlencoded" || re.Request.URL.RawQuery != "" {
		return nil, errors.New("invalid form")
	}
	if e = re.Request.ParseForm(); e != nil {
		return nil, e
	}
	for k, v := range re.Request.PostForm {
		ok := false
		for _, a := range allowed {
			if a == k {
				ok = true
			}
		}
		if !ok || len(v) != 1 {
			return nil, errors.New("invalid form")
		}
	}
	return re.Request.PostForm, nil
}
func client(re *core.RequestEvent, m *delegation.Manager) (string, error) {
	id, secret, ok := re.Request.BasicAuth()
	if !ok || len(re.Request.Header.Values("Authorization")) != 1 {
		return "", &delegation.OAuthError{Code: "invalid_client", Status: 401}
	}
	id, e := url.QueryUnescape(id)
	if e != nil {
		return "", &delegation.OAuthError{Code: "invalid_client", Status: 401}
	}
	secret, e = url.QueryUnescape(secret)
	if e != nil {
		return "", &delegation.OAuthError{Code: "invalid_client", Status: 401}
	}
	return id, m.AuthenticateClient(id, secret)
}
func RegisterRoutes(se *core.ServeEvent, m *delegation.Manager) {
	se.Router.Bind(&hook.Handler[*core.RequestEvent]{Id: "oauthResponsePrivacy", Priority: apis.DefaultSecurityHeadersMiddlewarePriority + 1, Func: func(re *core.RequestEvent) error {
		path := re.Request.URL.Path
		if strings.HasPrefix(path, "/oauth/") || strings.HasPrefix(path, "/developer/v1/") || path == "/user/authorizations" || strings.HasPrefix(path, "/user/authorizations/") || path == "/.well-known/oauth-authorization-server" {
			// Run before auth, rate and body-limit errors; redact before the outer activity logger.
			re.Response.Header().Set("X-Frame-Options", "DENY")
			defer func() {
				u := *re.Request.URL
				u.RawQuery = ""
				re.Request.URL = &u
				re.Request.RequestURI = u.Path
				re.Request.Header.Del("Referer")
			}()
			return noStore(re)
		}
		return re.Next()
	}})
	se.Router.GET("/developer/openapi.yaml", func(re *core.RequestEvent) error {
		return re.Blob(200, "application/yaml; charset=utf-8", apidocs.DeveloperOpenAPI())
	})
	se.Router.GET("/developer", func(re *core.RequestEvent) error {
		return re.Blob(200, "text/html; charset=utf-8", apidocs.DeveloperHTML())
	})
	se.Router.GET("/developer/guide.md", func(re *core.RequestEvent) error {
		return re.Blob(200, "text/markdown; charset=utf-8", apidocs.DeveloperGuide())
	})
	se.Router.GET("/.well-known/oauth-authorization-server", func(re *core.RequestEvent) error {
		re.Response.Header().Set("Cache-Control", "no-store")
		if m == nil {
			return respond(re, &delegation.OAuthError{Code: "temporarily_unavailable", Status: 503})
		}
		return re.JSON(200, m.Metadata())
	})
	oauth := se.Router.Group("/oauth")
	oauth.GET("/authorize", func(re *core.RequestEvent) error {
		q, e := url.ParseQuery(re.Request.URL.RawQuery)
		if e != nil {
			return invalid(re)
		}
		for k, v := range q {
			if len(v) != 1 || !strings.Contains("|client_id|redirect_uri|response_type|scope|state|code_challenge|code_challenge_method|", "|"+k+"|") {
				return invalid(re)
			}
		}
		target, e := m.Begin(re.App, delegation.AuthorizationInput{ClientID: q.Get("client_id"), RedirectURI: q.Get("redirect_uri"), ResponseType: q.Get("response_type"), Scope: strings.Fields(q.Get("scope")), State: q.Get("state"), Challenge: q.Get("code_challenge"), ChallengeMethod: q.Get("code_challenge_method")})
		if e != nil {
			if target != "" {
				return re.Redirect(302, target)
			}
			return respond(re, e)
		}
		return re.Redirect(302, target)
	})
	consent := oauth.Group("/consent").BindFunc(requireSession)
	consent.GET("", func(re *core.RequestEvent) error {
		q, e := url.ParseQuery(re.Request.URL.RawQuery)
		if e != nil || len(q) != 1 || len(q["request"]) != 1 {
			return invalid(re)
		}
		out, e := m.ReadConsent(re.App, actor(re), q.Get("request"))
		if e != nil {
			return respond(re, e)
		}
		return re.JSON(200, out)
	})
	consent.POST("", func(re *core.RequestEvent) error {
		var in struct {
			Request string   `json:"request"`
			Approve *bool    `json:"approve"`
			Scopes  []string `json:"scopes"`
		}
		if e := strictJSON(re, &in); e != nil {
			if errors.Is(e, apis.ErrRequestEntityTooLarge) {
				return e
			}
			return invalid(re)
		}
		if in.Approve == nil {
			return invalid(re)
		}
		target, e := m.Decide(re.App, actor(re), delegation.Decision{Request: in.Request, Approve: *in.Approve, Scopes: in.Scopes})
		if e != nil {
			return respond(re, e)
		}
		return re.JSON(200, map[string]string{"redirect_uri": target})
	}).Bind(apis.BodyLimit(16384))
	oauth.POST("/token", func(re *core.RequestEvent) error {
		id, e := client(re, m)
		if e != nil {
			return respond(re, e)
		}
		f, e := tokenForm(re, "grant_type", "code", "redirect_uri", "code_verifier", "refresh_token")
		if e != nil {
			if errors.Is(e, apis.ErrRequestEntityTooLarge) {
				return e
			}
			return invalid(re)
		}
		out, e := m.Exchange(re.App, id, delegation.TokenInput{GrantType: f.Get("grant_type"), Code: f.Get("code"), RedirectURI: f.Get("redirect_uri"), Verifier: f.Get("code_verifier"), RefreshToken: f.Get("refresh_token")})
		if e != nil {
			return respond(re, e)
		}
		return re.JSON(200, out)
	}).Bind(apis.BodyLimit(16384))
	oauth.POST("/revoke", func(re *core.RequestEvent) error {
		id, e := client(re, m)
		if e != nil {
			return respond(re, e)
		}
		f, e := tokenForm(re, "token", "token_type_hint")
		if errors.Is(e, apis.ErrRequestEntityTooLarge) {
			return e
		}
		if e != nil || f.Get("token") == "" || len(f.Get("token")) > 512 {
			return invalid(re)
		}
		if e = m.RevokeToken(re.App, id, f.Get("token")); e != nil {
			return respond(re, e)
		}
		return re.NoContent(200)
	}).Bind(apis.BodyLimit(16384))
	grants := se.Router.Group("/user/authorizations").BindFunc(requireSession)
	grants.GET("", func(re *core.RequestEvent) error {
		out, e := m.ListGrants(re.App, re.Auth.Id)
		if e != nil {
			return respond(re, e)
		}
		return re.JSON(200, map[string]any{"items": out})
	})
	grants.DELETE("/{id}", func(re *core.RequestEvent) error {
		if e := delegation.RevokeGrant(re.App, re.Auth.Id, re.Request.PathValue("id")); e != nil {
			return respond(re, e)
		}
		return re.NoContent(204)
	})
	api := se.Router.Group("/developer/v1")
	api.GET("/me", resource(m, "profile:read", func(re *core.RequestEvent, sub string) error {
		nickname := ""
		if info, e := re.App.FindRecordById("user_info", re.Auth.Id); e == nil {
			nickname = info.GetString("nickname")
		}
		return re.JSON(200, map[string]string{"sub": sub, "username": re.Auth.GetString("username"), "nickname": nickname})
	}))
	for path, handler := range map[string]func(*core.RequestEvent) error{"/arcades": query.ListArcades, "/arcade": query.GetArcadeValues, "/arcades/nearby": query.ListArcadesBySeriesAndLocation} {
		api.GET(path, resource(m, "arcade:read", domain(handler)))
	}
	api.GET("/campaigns", resource(m, "campaign:read", domain(campaign.ListCampaigns)))
	api.GET("/campaign", resource(m, "campaign:read", domain(campaign.GetCampaign)))
	api.GET("/arcade/campaigns", resource(m, "campaign:read", domain(campaign.ListArcadeCampaigns)))
	api.POST("/arcade/flag", resource(m, "arcade:report:create", domain(flag.CreateArcadeFlag))).Bind(apis.BodyLimit(flag.MaxFlagBodyBytes))
	api.POST("/arcade/flag/reaction", resource(m, "arcade:report:react", domain(flag.UpdateArcadeFlagReaction))).Bind(apis.BodyLimit(16384))
	api.POST("/campaign/check", resource(m, "campaign:respond", domain(campaign.CheckCampaign))).Bind(apis.BodyLimit(16384))
}
func domain(h func(*core.RequestEvent) error) func(*core.RequestEvent, string) error {
	return func(re *core.RequestEvent, _ string) error { return h(re) }
}
func resource(m *delegation.Manager, scope string, h func(*core.RequestEvent, string) error) func(*core.RequestEvent) error {
	return func(re *core.RequestEvent) error {
		u, sub, e := m.ResolveAccess(re.App, bearer(re.Request), scope)
		if e != nil {
			var oe *delegation.OAuthError
			if errors.As(e, &oe) {
				re.Response.Header().Set("WWW-Authenticate", `Bearer error="`+oe.Code+`", scope="`+scope+`"`)
				return re.JSON(oe.Status, map[string]string{"error": oe.Code})
			}
			return respond(re, e)
		}
		re.Auth = u
		return h(re, sub)
	}
}
