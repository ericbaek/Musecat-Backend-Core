// Package joint exposes the one-step reciprocal connection: the user-facing consent
// flow under /user/joint and the partner-facing status/revocation endpoints under
// /partner/v1 authenticated with the Musecat-issued partner key.
package joint

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/router"

	"github.com/ericbaek/musecat-backend-core/connections"
	"github.com/ericbaek/musecat-backend-core/handlers/user"
)

func RegisterRoutes(se *core.ServeEvent, manager *connections.JointManager) {
	se.Router.GET("/integrations/joint", func(re *core.RequestEvent) error {
		re.Response.Header().Set("Cache-Control", "no-store")
		userID := ""
		if re.Auth != nil && re.Auth.Collection().Name == "user" && !re.Auth.GetBool("withdrawn") {
			userID = re.Auth.Id
		}
		return re.JSON(http.StatusOK, map[string]any{"items": manager.PartnersFor(userID)})
	})

	routes := se.Router.Group("/user/joint").Bind(apis.RequireAuth("user"), user.RequireActiveUser())
	routes.GET("", func(re *core.RequestEvent) error {
		items, err := manager.UserPairs(re.App, actor(re))
		return respond(re, map[string]any{"items": items}, err)
	})
	routes.POST("/begin", func(re *core.RequestEvent) error {
		var in struct {
			Partner  string `json:"partner"`
			ReturnTo string `json:"return_to"`
		}
		if err := decode(re, &in); err != nil {
			return err
		}
		target, correlation, err := manager.BeginURLFor(re.Auth.Id, in.Partner, in.ReturnTo)
		return respond(re, map[string]string{"start_url": target, "correlation_id": correlation}, err)
	}).Bind(apis.BodyLimit(4 << 10))
	routes.POST("/lookup", func(re *core.RequestEvent) error {
		var in struct {
			Partner string `json:"partner"`
			Intent  string `json:"intent"`
		}
		if err := decode(re, &in); err != nil {
			return err
		}
		out, err := manager.Lookup(re.Request.Context(), re.App, actor(re), in.Partner, in.Intent)
		return respond(re, out, err)
	}).Bind(apis.BodyLimit(4 << 10))
	routes.GET("/{id}", func(re *core.RequestEvent) error {
		out, err := manager.Read(re.App, actor(re), re.Request.PathValue("id"))
		return respond(re, out, err)
	})
	routes.GET("/{id}/status", func(re *core.RequestEvent) error {
		out, err := manager.Status(re.App, actor(re), re.Request.PathValue("id"))
		return respond(re, out, err)
	})
	routes.POST("/{id}/approve", func(re *core.RequestEvent) error {
		target, err := manager.Approve(re.Request.Context(), re.App, actor(re), re.Request.PathValue("id"))
		return respond(re, map[string]string{"redirect_uri": target}, err)
	})
	routes.POST("/{id}/deny", func(re *core.RequestEvent) error {
		target, err := manager.Deny(re.App, actor(re), re.Request.PathValue("id"))
		return respond(re, map[string]string{"redirect_uri": target}, err)
	})
	routes.DELETE("/{id}", func(re *core.RequestEvent) error {
		if err := manager.DisconnectPair(re.App, actor(re), re.Request.PathValue("id")); err != nil {
			return respondError(re, err)
		}
		re.Response.Header().Set("Cache-Control", "no-store")
		return re.NoContent(http.StatusNoContent)
	})

	partner := se.Router.Group("/partner/v1").BindFunc(requirePartner(manager))
	partner.GET("/joint/{correlation}", func(re *core.RequestEvent) error {

		out, err := manager.PartnerRequest(re.App, re.Get("partner").(string), re.Request.Header.Get("X-Request-Id"), re.Request.Method+re.Request.URL.Path, func(tx core.App) (any, error) {
			return manager.PartnerStatus(tx, re.Get("partner").(string), re.Request.PathValue("correlation"))
		})
		if errors.Is(err, sql.ErrNoRows) {
			return re.JSON(http.StatusNotFound, map[string]string{"error": "invalid_request"})
		}
		return respond(re, out, err)
	})
	partner.POST("/revocations", func(re *core.RequestEvent) error {
		var in struct {
			PairID     string `json:"pair_id"`
			Generation string `json:"generation"`
			Reason     string `json:"reason"`
			RevokedAt  string `json:"revoked_at"`
		}
		if err := decode(re, &in); err != nil {
			return re.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		}

		if _, err := time.Parse(time.RFC3339, in.RevokedAt); err != nil || in.Generation == "" || len(in.Generation) > 64 || !slices.Contains([]string{"user", "security", "admin", "superseded"}, in.Reason) {
			return re.JSON(400, map[string]string{"error": "invalid_request"})
		}
		raw, _ := json.Marshal(in)
		out, err := manager.PartnerRequest(re.App, re.Get("partner").(string), re.Request.Header.Get("X-Request-Id"), re.Request.Method+re.Request.URL.Path+string(raw), func(tx core.App) (any, error) {
			status, err := manager.ReceiveRevocation(tx, re.Get("partner").(string), in.PairID, in.Generation)
			return map[string]string{"status": status}, err
		})
		return respond(re, out, err)
	}).Bind(apis.BodyLimit(4 << 10))
}

// requirePartner resolves the bearer key to a registered partner (spec section 4).
func requirePartner(manager *connections.JointManager) func(*core.RequestEvent) error {
	return func(re *core.RequestEvent) error {
		parts := strings.Fields(re.Request.Header.Get("Authorization"))
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			return re.JSON(http.StatusUnauthorized, map[string]string{"error": "unauthorized_partner"})
		}
		id, ok := manager.AuthenticatePartner(parts[1])
		if !ok {
			return re.JSON(http.StatusUnauthorized, map[string]string{"error": "unauthorized_partner"})
		}
		re.Set("partner", id)
		re.Response.Header().Set("Cache-Control", "no-store")
		return re.Next()
	}
}

func actor(re *core.RequestEvent) connections.Actor {
	parts := strings.Fields(re.Request.Header.Get("Authorization"))
	token := ""
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		token = parts[1]
	}
	return connections.Actor{UserID: re.Auth.Id, Session: token, Check: func(app core.App) error {
		fresh, err := app.FindAuthRecordByToken(token, core.TokenTypeAuth)
		if err != nil || fresh.Collection().Name != "user" || fresh.Id != re.Auth.Id {
			return apis.NewUnauthorizedError("A current user session is required.", nil)
		}
		return user.CheckActiveUser(app, fresh)
	}}
}

func decode(re *core.RequestEvent, target any) error {
	raw, err := io.ReadAll(io.LimitReader(re.Request.Body, (4<<10)+1))
	if err != nil {
		return err
	}
	if len(raw) > 4<<10 {
		return apis.ErrRequestEntityTooLarge
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return re.BadRequestError("Invalid joint connection request.", nil)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return re.BadRequestError("Invalid joint connection request.", nil)
	}
	return nil
}

func respond(re *core.RequestEvent, body any, err error) error {
	if err != nil {
		return respondError(re, err)
	}
	re.Response.Header().Set("Cache-Control", "no-store")
	return re.JSON(http.StatusOK, body)
}

func respondError(re *core.RequestEvent, err error) error {
	switch {
	case errors.Is(err, connections.ErrInvalid):
		return re.BadRequestError(err.Error(), nil)
	case errors.Is(err, connections.ErrConflict):
		return re.JSON(http.StatusConflict, map[string]string{"error": err.Error()})
	case errors.Is(err, connections.ErrUnavailable):
		return re.JSON(http.StatusServiceUnavailable, map[string]string{"error": "joint_unavailable"})
	case errors.Is(err, connections.ErrProvider):
		return re.JSON(http.StatusBadGateway, map[string]string{"error": "partner_error"})
	case errors.Is(err, sql.ErrNoRows):
		return re.NotFoundError("Joint connection not found.", nil)
	default:
		var apiError *router.ApiError
		if errors.As(err, &apiError) {
			return err
		}
		return re.InternalServerError("Joint connection operation failed.", nil)
	}
}
