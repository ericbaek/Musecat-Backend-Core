package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

const userReportWaitingStatus = "waiting"

type CreateUserReportBody struct {
	User   string `json:"user"`
	Reason string `json:"reason"`
}

func parseCreateUserReportBody(re *core.RequestEvent) (CreateUserReportBody, error) {
	var body CreateUserReportBody
	if err := json.NewDecoder(re.Request.Body).Decode(&body); err != nil {
		return body, err
	}

	body.User = strings.TrimSpace(body.User)
	body.Reason = strings.TrimSpace(body.Reason)
	return body, nil
}

func validateCreateUserReportBody(body CreateUserReportBody, authUserID string) error {
	if body.User == "" {
		return fmt.Errorf("user is required")
	}
	if len(body.User) != 15 {
		return fmt.Errorf("user must be a valid user id")
	}
	if body.User == strings.TrimSpace(authUserID) {
		return fmt.Errorf("cannot report yourself")
	}
	if body.Reason == "" {
		return fmt.Errorf("reason is required")
	}
	return nil
}

func CreateUserReport(re *core.RequestEvent) error {
	body, err := parseCreateUserReportBody(re)
	if err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "invalid JSON body",
			"details": err.Error(),
		})
	}
	if err := validateCreateUserReportBody(body, re.Auth.Id); err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "validation failed",
			"details": err.Error(),
		})
	}

	if _, err := re.App.FindRecordById(collectionUser, body.User); err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "validation failed",
			"details": "user not found",
		})
	}

	coll, err := re.App.FindCollectionByNameOrId(collectionUserReport)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "failed to create user_report",
			"details": fmt.Sprintf("failed to find user_report collection: %v", err),
		})
	}

	rec := core.NewRecord(coll)
	rec.Set("user", body.User)
	rec.Set("reason", body.Reason)
	rec.Set("status", userReportWaitingStatus)
	rec.Set("createdBy", re.Auth.Id)

	if err := re.App.Save(rec); err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "failed to create user_report",
			"details": fmt.Sprintf("failed to save user_report record: %v", err),
		})
	}

	return re.JSON(http.StatusOK, map[string]any{
		"id":        rec.Id,
		"user":      rec.GetString("user"),
		"reason":    rec.GetString("reason"),
		"status":    rec.GetString("status"),
		"createdBy": rec.GetString("createdBy"),
	})
}

func ListUserReport(re *core.RequestEvent) error {
	reportedUserID := strings.TrimSpace(re.Request.URL.Query().Get("user"))
	filter := "createdBy = {:createdBy}"
	params := dbx.Params{"createdBy": re.Auth.Id}
	if reportedUserID != "" {
		filter += " && user = {:user}"
		params["user"] = reportedUserID
	}

	recs, err := re.App.FindRecordsByFilter(
		collectionUserReport,
		filter,
		"-created",
		0,
		0,
		params,
	)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "failed to list user_report",
			"details": err.Error(),
		})
	}

	items := make([]map[string]any, 0, len(recs))
	for _, rec := range recs {
		items = append(items, map[string]any{
			"id":        rec.Id,
			"user":      rec.GetString("user"),
			"reason":    rec.GetString("reason"),
			"status":    rec.GetString("status"),
			"createdBy": rec.GetString("createdBy"),
			"created":   rec.GetString("created"),
			"updated":   rec.GetString("updated"),
		})
	}

	return re.JSON(http.StatusOK, map[string]any{
		"items": items,
		"total": len(items),
	})
}
