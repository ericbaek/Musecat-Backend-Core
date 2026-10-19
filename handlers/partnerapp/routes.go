// Package partnerapp exposes the Developer Center custom API. Owner routes manage a
// user's own partner applications; operator routes approve, reject, suspend and
// reinstate them. These routes are not part of the delegated account allowlist.
package partnerapp

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/router"

	apidocs "github.com/ericbaek/musecat-backend-core/docs"
	arcadequery "github.com/ericbaek/musecat-backend-core/handlers/arcade/query"
	"github.com/ericbaek/musecat-backend-core/handlers/user"
	"github.com/ericbaek/musecat-backend-core/partnerapp"
)

const bodyLimit = 64 << 10

type reviewInput struct {
	Note string `json:"note"`
}

func RegisterRoutes(se *core.ServeEvent, service *partnerapp.Service) {
	se.Router.GET("/developer/integration-spec.md", func(re *core.RequestEvent) error {
		return re.Blob(http.StatusOK, "text/markdown; charset=utf-8", apidocs.PartnerIntegrationSpec())
	})

	se.Router.GET("/developer/services", func(re *core.RequestEvent) error {
		re.Response.Header().Set("Cache-Control", "public, max-age=3600")
		return re.JSON(http.StatusOK, map[string]any{"spec": partnerapp.FormSpec, "manifests": []string{partnerapp.JointManifest}, "services": partnerapp.ServiceRegistry})
	})

	owner := se.Router.Group("/developer/apps").Bind(apis.RequireAuth("user"), user.RequireActiveUser())
	owner.GET("", func(re *core.RequestEvent) error {
		items, err := service.List(re.App, re.Auth.Id)
		return respond(re, map[string]any{"items": items}, err)
	})
	owner.POST("", func(re *core.RequestEvent) error {
		var form partnerapp.Form
		if err := decode(re, &form); err != nil {
			return err
		}
		app, err := service.Create(re.App, re.Auth.Id, form)
		return respondStatus(re, http.StatusCreated, app, err)
	}).Bind(apis.BodyLimit(bodyLimit))
	owner.GET("/{id}", func(re *core.RequestEvent) error {
		app, err := service.Get(re.App, re.Auth.Id, re.Request.PathValue("id"))
		return respond(re, app, err)
	})
	owner.PUT("/{id}", func(re *core.RequestEvent) error {
		var form partnerapp.Form
		if err := decode(re, &form); err != nil {
			return err
		}
		app, err := service.Update(re.App, re.Auth.Id, re.Request.PathValue("id"), form)
		return respond(re, app, err)
	}).Bind(apis.BodyLimit(bodyLimit))
	owner.POST("/{id}/submit", func(re *core.RequestEvent) error {
		app, err := service.Submit(re.App, re.Auth.Id, re.Request.PathValue("id"))
		return respond(re, app, err)
	})
	owner.POST("/{id}/secret", func(re *core.RequestEvent) error {
		clientID, secret, err := service.GenerateSecret(re.App, re.Auth.Id, re.Request.PathValue("id"))
		if err != nil {
			return respondError(re, err)
		}
		re.Response.Header().Set("Cache-Control", "no-store")
		re.Response.Header().Set("Referrer-Policy", "no-referrer")
		return re.JSON(http.StatusOK, map[string]string{"client_id": clientID, "client_secret": secret})
	})
	owner.PUT("/{id}/credentials", func(re *core.RequestEvent) error {
		var in partnerapp.Credentials
		if err := decode(re, &in); err != nil {
			return err
		}
		app, err := service.SetCredentials(re.App, re.Auth.Id, re.Request.PathValue("id"), in)
		return respond(re, app, err)
	}).Bind(apis.BodyLimit(8 << 10))
	owner.DELETE("/{id}/credentials", func(re *core.RequestEvent) error {
		app, err := service.ClearCredentials(re.App, re.Auth.Id, re.Request.PathValue("id"))
		return respond(re, app, err)
	})
	owner.PUT("/{id}/joint-key", func(re *core.RequestEvent) error {
		var in struct {
			Key string `json:"key"`
		}
		if err := decode(re, &in); err != nil {
			return err
		}
		app, err := service.SetJointKey(re.App, re.Auth.Id, re.Request.PathValue("id"), in.Key)
		return respond(re, app, err)
	}).Bind(apis.BodyLimit(4 << 10))
	owner.POST("/{id}/partner-key", func(re *core.RequestEvent) error {
		partnerID, key, err := service.GeneratePartnerKey(re.App, re.Auth.Id, re.Request.PathValue("id"))
		if err != nil {
			return respondError(re, err)
		}
		re.Response.Header().Set("Cache-Control", "no-store")
		re.Response.Header().Set("Referrer-Policy", "no-referrer")
		return re.JSON(http.StatusOK, map[string]string{"partner_id": partnerID, "key": key})
	})
	owner.POST("/{id}/records-key", func(re *core.RequestEvent) error {
		partnerID, key, err := service.GenerateRecordsKey(re.App, re.Auth.Id, re.Request.PathValue("id"))
		if err != nil {
			return respondError(re, err)
		}
		re.Response.Header().Set("Cache-Control", "no-store")
		re.Response.Header().Set("Referrer-Policy", "no-referrer")
		return re.JSON(http.StatusOK, map[string]string{"partner_id": partnerID, "key": key})
	})
	owner.GET("/{id}/checks", func(re *core.RequestEvent) error {
		items, err := service.CheckReports(re.App, re.Auth.Id, re.Request.PathValue("id"), false)
		return respond(re, map[string]any{"items": items}, err)
	})
	owner.POST("/{id}/checks", func(re *core.RequestEvent) error {
		var in partnerapp.CheckFixtures
		if re.Request.ContentLength != 0 {
			if err := decode(re, &in); err != nil {
				return err
			}
		}
		out, err := service.QueueChecks(re.App, re.Auth.Id, re.Request.PathValue("id"), false, in)
		return respondStatus(re, http.StatusAccepted, out, err)
	}).Bind(apis.BodyLimit(16 << 10))
	owner.GET("/{id}/console", func(re *core.RequestEvent) error {
		page := 1
		if raw := re.Request.URL.Query().Get("page"); raw != "" {
			var err error
			page, err = strconv.Atoi(raw)
			if err != nil || page < 1 || page > 10000 {
				return re.BadRequestError("Invalid page.", nil)
			}
		}
		out, err := service.Console(re.App, re.Auth.Id, re.Request.PathValue("id"), page)
		return respond(re, out, err)
	})
	owner.GET("/{id}/events", func(re *core.RequestEvent) error {
		items, err := service.OwnerEvents(re.App, re.Auth.Id, re.Request.PathValue("id"))
		return respond(re, map[string]any{"items": items}, err)
	})

	operator := se.Router.Group("/moderation/developer/apps").Bind(apis.RequireAuth("user"), user.RequireActiveUser(), arcadequery.RequireStrictReviewerAccess())
	operator.GET("", func(re *core.RequestEvent) error {
		items, err := service.Queue(re.App, re.Request.URL.Query().Get("status"))
		return respond(re, map[string]any{"items": items}, err)
	})
	operator.GET("/{id}", func(re *core.RequestEvent) error {
		app, err := service.Inspect(re.App, re.Request.PathValue("id"))
		return respond(re, app, err)
	})
	operator.GET("/{id}/checks", func(re *core.RequestEvent) error {
		items, err := service.CheckReports(re.App, re.Auth.Id, re.Request.PathValue("id"), true)
		return respond(re, map[string]any{"items": items}, err)
	})
	operator.POST("/{id}/checks", func(re *core.RequestEvent) error {
		var in partnerapp.CheckFixtures
		if re.Request.ContentLength != 0 {
			if err := decode(re, &in); err != nil {
				return err
			}
		}
		out, err := service.QueueChecks(re.App, re.Auth.Id, re.Request.PathValue("id"), true, in)
		return respondStatus(re, http.StatusAccepted, out, err)
	}).Bind(apis.BodyLimit(16 << 10))
	operator.GET("/{id}/events", func(re *core.RequestEvent) error {
		items, err := service.EventsFor(re.App, re.Request.PathValue("id"))
		return respond(re, map[string]any{"items": items}, err)
	})
	type action func(core.App, string, string, string) (partnerapp.Application, error)
	for path, fn := range map[string]action{"approve": service.Approve, "reject": service.Reject, "suspend": service.Suspend, "reinstate": service.Reinstate} {
		operator.POST("/{id}/"+path, func(re *core.RequestEvent) error {
			var in reviewInput
			if re.Request.ContentLength != 0 {
				if err := decode(re, &in); err != nil {
					return err
				}
			}
			if len(in.Note) > 2000 {
				return re.BadRequestError("Review note is too long.", nil)
			}
			app, err := fn(re.App, re.Auth.Id, re.Request.PathValue("id"), in.Note)
			return respond(re, app, err)
		}).Bind(apis.BodyLimit(8 << 10))
	}
}

func decode(re *core.RequestEvent, target any) error {
	// PocketBase rewinds on EOF, including a final read that also returns data.
	raw, err := io.ReadAll(io.LimitReader(re.Request.Body, bodyLimit+1))
	if err != nil {
		return err
	}
	if len(raw) > bodyLimit {
		return apis.ErrRequestEntityTooLarge
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return re.BadRequestError("Invalid developer application request.", nil)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return re.BadRequestError("Invalid developer application request.", nil)
	}
	return nil
}

func respond(re *core.RequestEvent, body any, err error) error {
	return respondStatus(re, http.StatusOK, body, err)
}

func respondStatus(re *core.RequestEvent, status int, body any, err error) error {
	if err != nil {
		return respondError(re, err)
	}
	re.Response.Header().Set("Cache-Control", "no-store")
	return re.JSON(status, body)
}

func respondError(re *core.RequestEvent, err error) error {
	var fields partnerapp.FieldErrors
	switch {
	case errors.As(err, &fields):
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "invalid_form", "fields": fields})
	case errors.Is(err, partnerapp.ErrChecksBusy):
		return re.JSON(http.StatusTooManyRequests, map[string]string{"error": "checks_busy"})
	case errors.Is(err, partnerapp.ErrNotFound):
		return re.NotFoundError("Application not found.", nil)
	case errors.Is(err, partnerapp.ErrConflict):
		return re.JSON(http.StatusConflict, map[string]string{"error": "partner_id_conflict"})
	case errors.Is(err, partnerapp.ErrState):
		return re.JSON(http.StatusConflict, map[string]string{"error": "invalid_state"})
	case errors.Is(err, partnerapp.ErrUnavailable):
		return re.JSON(http.StatusServiceUnavailable, map[string]string{"error": "oauth_not_configured"})
	case errors.Is(err, partnerapp.ErrNoConnectionKey):
		return re.JSON(http.StatusServiceUnavailable, map[string]string{"error": "connections_not_configured"})
	default:
		var apiError *router.ApiError
		if errors.As(err, &apiError) {
			return err
		}
		return re.InternalServerError("Developer application operation failed.", nil)
	}
}
