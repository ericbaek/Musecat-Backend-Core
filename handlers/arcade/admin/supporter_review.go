package admin

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// Registered behind the active developer/moderator guard.
func ListSupporterRequests(re *core.RequestEvent) error {
	records, err := re.App.FindRecordsByFilter(arcadeinternal.CollectionSupporterRequest, "", "-created", 0, 0)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to list supporter requests"})
	}
	items := make([]map[string]any, 0, len(records))
	for _, record := range records {
		items = append(items, supporterRequestPayload(record))
	}
	return re.JSON(http.StatusOK, map[string]any{"items": items})
}

func supporterRequestPayload(record *core.Record) map[string]any {
	return map[string]any{"id": record.Id, "user": record.GetString("user"), "status": record.GetString("status"), "created": record.GetString("created"), "score_total": record.GetInt("score_total"), "decision_reason": record.GetString("decision_reason")}
}

func GetSupporterRequest(re *core.RequestEvent) error {
	record, err := re.App.FindRecordById(arcadeinternal.CollectionSupporterRequest, strings.TrimSpace(re.Request.URL.Query().Get("id")))
	if err != nil {
		return re.JSON(http.StatusNotFound, map[string]any{"error": "supporter request not found"})
	}
	user, err := re.App.FindRecordById("user", record.GetString("user"))
	if err != nil {
		return re.JSON(http.StatusNotFound, map[string]any{"error": "applicant not found"})
	}
	score, err := arcadeinternal.BuildSupporterScore(re.App, user.Id)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to load applicant XP"})
	}
	// Count actual changes rather than XP grants, which can be rate-limited.
	var arcades []struct {
		ID        string `db:"id" json:"id"`
		Name      string `db:"name" json:"name"`
		EditCount int    `db:"edit_count" json:"edit_count"`
	}
	err = re.App.DB().NewQuery(`SELECT a.id, COALESCE(b.name, '') AS name, COUNT(*) AS edit_count
 FROM arcade_changelog c JOIN arcade a ON a.id = c.arcade
 LEFT JOIN arcade_basic b ON b.id = a.basic
 WHERE c."by" = {:user} GROUP BY a.id ORDER BY edit_count DESC, a.id ASC`).Bind(dbx.Params{"user": user.Id}).All(&arcades)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to load applicant edits"})
	}
	if arcades == nil {
		arcades = make([]struct {
			ID        string `db:"id" json:"id"`
			Name      string `db:"name" json:"name"`
			EditCount int    `db:"edit_count" json:"edit_count"`
		}, 0)
	}
	total := 0
	for _, arcade := range arcades {
		total += arcade.EditCount
	}
	payload := supporterRequestPayload(record)
	payload["joined"] = user.GetString("created")
	payload["score"] = score
	payload["edit_count"] = total
	payload["arcades"] = arcades
	return re.JSON(http.StatusOK, payload)
}

func ReviewSupporterRequest(re *core.RequestEvent) error {
	var body struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(re.Request.Body).Decode(&body); err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
	}
	body.ID, body.Reason = strings.TrimSpace(body.ID), strings.TrimSpace(body.Reason)
	if len(body.ID) != 15 || (body.Status != "approved" && body.Status != "rejected") || utf8.RuneCountInString(body.Reason) > 1200 {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "invalid supporter review"})
	}
	var request *core.Record
	err := re.App.RunInTransaction(func(app core.App) error {
		var err error
		request, err = app.FindRecordById(arcadeinternal.CollectionSupporterRequest, body.ID)
		if err != nil {
			return &editReportError{status: http.StatusNotFound, message: "supporter request not found"}
		}
		if request.GetString("status") != "pending" {
			return &editReportError{status: http.StatusConflict, message: "supporter request already reviewed"}
		}
		applicant, err := app.FindRecordById("user", request.GetString("user"))
		if err != nil || applicant.GetBool("withdrawn") {
			return &editReportError{status: http.StatusConflict, message: "applicant is unavailable"}
		}
		if body.Status == "approved" {
			tags := applicant.GetStringSlice("tags")
			if !slices.Contains(tags, "founding_supporter") {
				tags = append(tags, "founding_supporter")
			}
			applicant.Set("tags", tags)
			if err := app.Save(applicant); err != nil {
				return err
			}
		}
		request.Set("status", body.Status)
		request.Set("decision_reason", body.Reason)
		return app.Save(request)
	})
	if err != nil {
		return writeEditReportError(re, err)
	}
	return re.JSON(http.StatusOK, supporterRequestPayload(request))
}
