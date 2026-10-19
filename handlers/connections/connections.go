package connections

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/ericbaek/musecat-backend-core/connections"
	"github.com/ericbaek/musecat-backend-core/handlers/user"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/router"
)

func RegisterRoutes(se *core.ServeEvent, manager *connections.Manager) {
	se.Router.GET("/integrations/providers", func(re *core.RequestEvent) error {
		re.Response.Header().Set("Cache-Control", "no-store")
		return re.JSON(http.StatusOK, map[string]any{"items": manager.Providers()})
	})
	// Public profile records: only services the user published and the partner marks public.
	se.Router.GET("/user/records", func(re *core.RequestEvent) error {
		userID := re.Request.URL.Query().Get("user")
		if len(userID) != 15 {
			return re.BadRequestError("Missing user id.", nil)
		}
		target, err := re.App.FindRecordById("user", userID)
		if err != nil || target.GetBool("withdrawn") {
			return re.NotFoundError("User not found.", nil)
		}
		items, err := manager.PublicRecords(re.App, userID)
		if err != nil {
			return respondError(re, err)
		}
		re.Response.Header().Set("Cache-Control", "public, max-age=300")
		return re.JSON(http.StatusOK, map[string]any{"items": items})
	})
	routes := se.Router.Group("/user/connections").Bind(apis.RequireAuth("user"), user.RequireActiveUser())
	routes.GET("", func(re *core.RequestEvent) error {
		items, err := connections.List(re.App, re.Auth.Id)
		if err != nil {
			return respondError(re, err)
		}
		re.Response.Header().Set("Cache-Control", "no-store")
		return re.JSON(http.StatusOK, map[string]any{"items": items})
	})
	routes.POST("/start", func(re *core.RequestEvent) error {
		var input connections.StartInput
		if err := decode(re, &input); err != nil {
			return err
		}
		result, err := manager.Start(re.App, actor(re), input)
		if err != nil {
			return respondError(re, err)
		}
		re.Response.Header().Set("Cache-Control", "no-store")
		return re.JSON(http.StatusOK, result)
	}).Bind(apis.BodyLimit(16384))
	routes.POST("/complete", func(re *core.RequestEvent) error {
		var input connections.CompleteInput
		if err := decode(re, &input); err != nil {
			return err
		}
		result, err := manager.Complete(re.Request.Context(), re.App, actor(re), input)
		if err != nil {
			return respondError(re, err)
		}
		re.Response.Header().Set("Cache-Control", "no-store")
		return re.JSON(http.StatusOK, result)
	}).Bind(apis.BodyLimit(16384))
	routes.GET("/{id}/records/{service}", func(re *core.RequestEvent) error {
		refresh := re.Request.URL.Query().Get("refresh") == "true"
		view, err := manager.ReadRecord(re.Request.Context(), re.App, actor(re), re.Request.PathValue("id"), re.Request.PathValue("service"), refresh)
		if err != nil {
			return respondError(re, err)
		}
		re.Response.Header().Set("Cache-Control", "no-store")
		return re.JSON(http.StatusOK, view)
	})
	routes.PUT("/{id}/services/{service}", func(re *core.RequestEvent) error {
		var input struct {
			Visibility string `json:"visibility"`
		}
		if err := decode(re, &input); err != nil {
			return err
		}
		item, err := connections.SetServiceVisibility(re.App, actor(re), re.Request.PathValue("id"), re.Request.PathValue("service"), input.Visibility)
		if err != nil {
			return respondError(re, err)
		}
		re.Response.Header().Set("Cache-Control", "no-store")
		return re.JSON(http.StatusOK, item)
	}).Bind(apis.BodyLimit(1024))
	routes.DELETE("/{id}", func(re *core.RequestEvent) error {
		if err := connections.Disconnect(re.App, actor(re), re.Request.PathValue("id")); err != nil {
			return respondError(re, err)
		}
		re.Response.Header().Set("Cache-Control", "no-store")
		return re.NoContent(http.StatusNoContent)
	})
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
	// PocketBase rewinds on EOF, including a final read that also returns data.
	raw, err := io.ReadAll(io.LimitReader(re.Request.Body, 16384+1))
	if err != nil {
		return err
	}
	if len(raw) > 16384 {
		return apis.ErrRequestEntityTooLarge
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return re.BadRequestError("Invalid connection request.", nil)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return re.BadRequestError("Invalid connection request.", nil)
	}
	return nil
}

func respondError(re *core.RequestEvent, err error) error {
	switch {
	case errors.Is(err, connections.ErrInvalid):
		return re.BadRequestError(err.Error(), nil)
	case errors.Is(err, connections.ErrConflict):
		return re.JSON(http.StatusConflict, map[string]string{"error": err.Error(), "code": "external_account_conflict"})
	case errors.Is(err, connections.ErrUnavailable):
		return re.JSON(http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
	case errors.Is(err, connections.ErrProvider):
		return re.JSON(http.StatusBadGateway, map[string]string{"error": err.Error()})
	case errors.Is(err, sql.ErrNoRows):
		return re.NotFoundError("Connection not found.", nil)
	default:
		var apiError *router.ApiError
		if errors.As(err, &apiError) {
			return err
		}
		return re.InternalServerError("Connection operation failed.", nil)
	}
}
