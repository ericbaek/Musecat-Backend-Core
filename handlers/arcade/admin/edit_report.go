package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
)

const (
	editReportKind         = "edit_report"
	rollbackReportKind     = "rollback_report"
	maxEditReportMessage   = 1200
	maxEditReviewNote      = 1200
	adminRequestDoneStatus = "done"
)

var allowedReviewOutcomes = map[string]struct{}{
	"upheld":    {},
	"dismissed": {},
	"actioned":  {},
}

type CreateArcadeEditReportBody struct {
	Arcade    string `json:"arcade"`
	Changelog string `json:"changelog"`
	Urgency   string `json:"urgency"`
	Message   string `json:"message"`
}

type ReviewArcadeEditReportBody struct {
	ID      string `json:"id"`
	Outcome string `json:"outcome"`
	Note    string `json:"note"`
}

type editReportError struct {
	status  int
	message string
}

func (e *editReportError) Error() string { return e.message }

var (
	errEditReportDuplicate = &editReportError{status: http.StatusConflict, message: "an unresolved report already exists for this changelog"}
	errEditReportNotFound  = &editReportError{status: http.StatusNotFound, message: "arcade or changelog not found"}
)

func CreateArcadeEditReport(re *core.RequestEvent) error {
	var body CreateArcadeEditReportBody
	if err := json.NewDecoder(re.Request.Body).Decode(&body); err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "invalid JSON body", "details": err.Error()})
	}
	normalizeEditReportBody(&body)
	if err := validateEditReportBody(body); err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "validation failed", "details": err.Error()})
	}

	var report *core.Record
	err := re.App.RunInTransaction(func(txApp core.App) error {
		var err error
		report, err = createArcadeEditReportTx(txApp, body, re.Auth, editReportKind)
		return err
	})
	if err != nil {
		return writeEditReportError(re, err)
	}
	return re.JSON(http.StatusOK, editReportPayload(report))
}

func normalizeEditReportBody(body *CreateArcadeEditReportBody) {
	body.Arcade = strings.TrimSpace(body.Arcade)
	body.Changelog = strings.TrimSpace(body.Changelog)
	body.Urgency = strings.ToLower(strings.TrimSpace(body.Urgency))
	body.Message = strings.TrimSpace(body.Message)
	if body.Urgency == "" {
		body.Urgency = defaultAdminRequestUrgency
	}
}

func validateEditReportBody(body CreateArcadeEditReportBody) error {
	if len(body.Arcade) != 15 {
		return fmt.Errorf("arcade must be a valid arcade id")
	}
	if len(body.Changelog) != 15 {
		return fmt.Errorf("changelog must be a valid changelog id")
	}
	if body.Message == "" {
		return fmt.Errorf("message is required")
	}
	if utf8.RuneCountInString(body.Message) > maxEditReportMessage {
		return fmt.Errorf("message must be at most %d characters", maxEditReportMessage)
	}
	if _, ok := allowedAdminRequestUrgency[body.Urgency]; !ok {
		return fmt.Errorf("urgency must be one of high, medium, low")
	}
	return nil
}

// createArcadeEditReportTx verifies the cited changelog belongs to the arcade.
// The reported editor is derived from that immutable row, not client input.
func createArcadeEditReportTx(app core.App, body CreateArcadeEditReportBody, reporter *core.Record, kind string) (*core.Record, error) {
	if reporter == nil {
		return nil, &editReportError{status: http.StatusUnauthorized, message: "authentication required"}
	}
	if kind != editReportKind && kind != rollbackReportKind {
		return nil, &editReportError{status: http.StatusBadRequest, message: "invalid report kind"}
	}
	arcade, err := app.FindRecordById(arcadeinternal.CollectionArcade, body.Arcade)
	if err != nil {
		return nil, errEditReportNotFound
	}
	if !arcade.GetBool("public") && arcade.GetString("createdBy") != reporter.Id && !hasStrictReviewerAccess(reporter) {
		return nil, errEditReportNotFound
	}
	changelog, err := app.FindRecordById(arcadeinternal.CollectionArcadeChangelog, body.Changelog)
	if err != nil || changelog.GetString("arcade") != arcade.Id {
		return nil, errEditReportNotFound
	}
	reportedEditor := strings.TrimSpace(changelog.GetString("by"))
	if reportedEditor == "" {
		return nil, &editReportError{status: http.StatusConflict, message: "the cited changelog has no reportable editor"}
	}

	existing, err := app.FindRecordsByFilter(
		arcadeinternal.CollectionArcadeRequestAdmin,
		"arcade = {:arcade} && changelog = {:changelog} && status != 'done' && (kind = 'edit_report' || kind = 'rollback_report')",
		"",
		1,
		0,
		dbx.Params{"arcade": arcade.Id, "changelog": changelog.Id},
	)
	if err != nil {
		return nil, fmt.Errorf("check duplicate edit report: %w", err)
	}
	if len(existing) > 0 {
		return nil, errEditReportDuplicate
	}

	collection, err := app.FindCollectionByNameOrId(arcadeinternal.CollectionArcadeRequestAdmin)
	if err != nil {
		return nil, fmt.Errorf("find arcade_request_admin: %w", err)
	}
	record := core.NewRecord(collection)
	record.Set("arcade", arcade.Id)
	record.Set("changelog", changelog.Id)
	record.Set("kind", kind)
	record.Set("reported_editor", reportedEditor)
	record.Set("urgency", body.Urgency)
	record.Set("message", body.Message)
	record.Set("status", adminRequestWaitingStatus)
	record.Set("createdBy", reporter.Id)
	if err := app.Save(record); err != nil {
		if strings.Contains(err.Error(), "idx_arcade_request_admin_open_changelog_report") {
			return nil, errEditReportDuplicate
		}
		return nil, fmt.Errorf("save edit report: %w", err)
	}
	return record, nil
}

func ListArcadeEditReports(re *core.RequestEvent) error {
	status := strings.ToLower(strings.TrimSpace(re.Request.URL.Query().Get("status")))
	filter := "kind = 'edit_report' || kind = 'rollback_report'"
	params := dbx.Params{}
	if status != "" {
		switch status {
		case "waiting", "processing", "done":
			filter = "(" + filter + ") && status = {:status}"
			params["status"] = status
		default:
			return re.JSON(http.StatusBadRequest, map[string]any{"error": "status must be waiting, processing, or done"})
		}
	}
	records, err := re.App.FindRecordsByFilter(arcadeinternal.CollectionArcadeRequestAdmin, filter, "created", 0, 0, params)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to list edit reports", "details": err.Error()})
	}
	items := make([]map[string]any, 0, len(records))
	for _, record := range records {
		items = append(items, editReportPayload(record))
	}
	return re.JSON(http.StatusOK, map[string]any{"items": items, "total": len(items)})
}

func ReviewArcadeEditReport(re *core.RequestEvent) error {
	var body ReviewArcadeEditReportBody
	if err := json.NewDecoder(re.Request.Body).Decode(&body); err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "invalid JSON body", "details": err.Error()})
	}
	body.ID = strings.TrimSpace(body.ID)
	body.Outcome = strings.ToLower(strings.TrimSpace(body.Outcome))
	body.Note = strings.TrimSpace(body.Note)
	if len(body.ID) != 15 {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "validation failed", "details": "id must be a valid report id"})
	}
	if _, ok := allowedReviewOutcomes[body.Outcome]; !ok {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "validation failed", "details": "outcome must be upheld, dismissed, or actioned"})
	}
	if utf8.RuneCountInString(body.Note) > maxEditReviewNote {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "validation failed", "details": fmt.Sprintf("note must be at most %d characters", maxEditReviewNote)})
	}

	var report *core.Record
	err := re.App.RunInTransaction(func(txApp core.App) error {
		var err error
		report, err = txApp.FindRecordById(arcadeinternal.CollectionArcadeRequestAdmin, body.ID)
		if err != nil {
			return errEditReportNotFound
		}
		if report.GetString("kind") != editReportKind && report.GetString("kind") != rollbackReportKind {
			return errEditReportNotFound
		}
		if report.GetString("status") == adminRequestDoneStatus {
			return &editReportError{status: http.StatusConflict, message: "edit report is already resolved"}
		}
		report.Set("status", adminRequestDoneStatus)
		report.Set("reviewed_by", re.Auth.Id)
		report.Set("reviewed_at", time.Now().UTC())
		report.Set("review_outcome", body.Outcome)
		report.Set("review_note", body.Note)
		return txApp.Save(report)
	})
	if err != nil {
		return writeEditReportError(re, err)
	}
	return re.JSON(http.StatusOK, editReportPayload(report))
}

func writeEditReportError(re *core.RequestEvent, err error) error {
	var reportErr *editReportError
	if errors.As(err, &reportErr) {
		return re.JSON(reportErr.status, map[string]any{"error": reportErr.message})
	}
	return re.JSON(http.StatusBadGateway, map[string]any{"error": "edit report operation failed", "details": err.Error()})
}

func editReportPayload(record *core.Record) map[string]any {
	return map[string]any{
		"id":              record.Id,
		"kind":            record.GetString("kind"),
		"arcade":          record.GetString("arcade"),
		"changelog":       record.GetString("changelog"),
		"reported_editor": record.GetString("reported_editor"),
		"urgency":         record.GetString("urgency"),
		"message":         record.GetString("message"),
		"status":          record.GetString("status"),
		"createdBy":       record.GetString("createdBy"),
		"reviewed_by":     record.GetString("reviewed_by"),
		"reviewed_at":     record.Get("reviewed_at"),
		"review_outcome":  record.GetString("review_outcome"),
		"review_note":     record.GetString("review_note"),
		"created":         record.Get("created"),
		"updated":         record.Get("updated"),
	}
}

func hasStrictReviewerAccess(auth *core.Record) bool {
	if auth == nil {
		return false
	}
	for _, tags := range [][]string{auth.GetStringSlice("tag"), auth.GetStringSlice("tags")} {
		for _, tag := range tags {
			switch strings.ToLower(strings.TrimSpace(tag)) {
			case "developer", "moderator":
				return true
			}
		}
	}
	return false
}
