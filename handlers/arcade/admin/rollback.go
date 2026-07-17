package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/pocketbase/pocketbase/core"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
)

var rollbackPartCollections = map[string]string{
	"basic": arcadeinternal.CollectionArcadeBasic,
	"hour":  arcadeinternal.CollectionArcadeHour,
	"sns":   arcadeinternal.CollectionArcadeSNS,
	"gtk":   arcadeinternal.CollectionArcadeGTK,
	"game":  arcadeinternal.CollectionArcadeGameRevisionBatch,
	"photo": arcadeinternal.CollectionArcadePhoto,
}

type RollbackArcadeBody struct {
	Arcade        string `json:"arcade"`
	Part          string `json:"part"`
	Value         string `json:"value"`
	PreviousValue string `json:"previous_value"`
	Report        bool   `json:"report"`
	Changelog     string `json:"changelog"`
	ReportMessage string `json:"report_message"`
}

type rollbackValidationError struct {
	message string
}

type rollbackDiffLogItem struct {
	ChangeType string                      `json:"change_type"`
	Message    string                      `json:"message"`
	Bullets    []arcadeinternal.I18nBullet `json:"bullets"`
	Diff       []map[string]any            `json:"diff,omitempty"`
}

func (e *rollbackValidationError) Error() string {
	return e.message
}

func buildRollbackDiffLogItem(part, fromValue, toValue string) rollbackDiffLogItem {
	return rollbackDiffLogItem{
		ChangeType: "updated",
		Message:    fmt.Sprintf("Rollback applied for %s (%s -> %s)", part, fromValue, toValue),
		Bullets: []arcadeinternal.I18nBullet{
			arcadeinternal.BuildI18nBullet("arcade.changelog.rollback.applied", map[string]any{
				"part": part,
				"from": fromValue,
				"to":   toValue,
			}),
		},
		Diff: []map[string]any{
			{
				"field": part,
				"from":  fromValue,
				"to":    toValue,
			},
		},
	}
}

func parseRollbackArcadeBody(re *core.RequestEvent) (RollbackArcadeBody, error) {
	var body RollbackArcadeBody
	if err := json.NewDecoder(re.Request.Body).Decode(&body); err != nil {
		return body, err
	}
	body.Arcade = strings.TrimSpace(body.Arcade)
	body.Part = strings.ToLower(strings.TrimSpace(body.Part))
	body.Value = strings.TrimSpace(body.Value)
	body.PreviousValue = strings.TrimSpace(body.PreviousValue)
	body.Changelog = strings.TrimSpace(body.Changelog)
	body.ReportMessage = strings.TrimSpace(body.ReportMessage)
	if body.Value == "" {
		body.Value = body.PreviousValue
	}
	return body, nil
}

func validateRollbackArcadeBody(body RollbackArcadeBody) error {
	if body.Arcade == "" {
		return fmt.Errorf("arcade is required")
	}
	if len(body.Arcade) != 15 {
		return fmt.Errorf("arcade must be a valid arcade id")
	}
	if _, ok := rollbackPartCollections[body.Part]; !ok {
		return fmt.Errorf("part must be one of basic, hour, sns, gtk, game, photo")
	}
	if body.Value == "" {
		return fmt.Errorf("value is required")
	}
	if len(body.Value) != 15 {
		return fmt.Errorf("value must be a valid relation id")
	}
	if body.Report {
		if len(body.Changelog) != 15 {
			return fmt.Errorf("changelog is required and must be a valid changelog id when report is true")
		}
		if body.ReportMessage == "" {
			return fmt.Errorf("report_message is required when report is true")
		}
		if utf8.RuneCountInString(body.ReportMessage) > maxEditReportMessage {
			return fmt.Errorf("report_message must be at most %d characters", maxEditReportMessage)
		}
	}
	return nil
}

func RollbackArcadePart(re *core.RequestEvent) error {
	body, err := parseRollbackArcadeBody(re)
	if err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "invalid JSON body",
			"details": err.Error(),
		})
	}
	if err := validateRollbackArcadeBody(body); err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "validation failed",
			"details": err.Error(),
		})
	}

	var fromValue string
	var toValue string
	var requestAdminID string

	err = re.App.RunInTransaction(func(txApp core.App) error {
		arcadeRec, err := txApp.FindRecordById(arcadeinternal.CollectionArcade, body.Arcade)
		if err != nil {
			return &rollbackValidationError{message: "arcade not found"}
		}

		field := body.Part
		if body.Part == "game" {
			field = "game_state"
		}
		fromValue = strings.TrimSpace(arcadeRec.GetString(field))
		if fromValue == "" {
			return &rollbackValidationError{message: fmt.Sprintf("arcade.%s is empty", body.Part)}
		}
		toValue = body.Value
		if toValue == fromValue {
			return &rollbackValidationError{message: fmt.Sprintf("arcade.%s is already set to value", body.Part)}
		}

		partCollection := rollbackPartCollections[body.Part]
		prevRec, err := txApp.FindRecordById(partCollection, toValue)
		if err != nil {
			return &rollbackValidationError{message: fmt.Sprintf("previous %s value not found", body.Part)}
		}
		if strings.TrimSpace(prevRec.GetString("arcade")) != body.Arcade {
			return &rollbackValidationError{message: fmt.Sprintf("previous %s value does not belong to arcade", body.Part)}
		}

		rollbackLog := arcadeinternal.BuildChangelogEnvelope(body.Part, []rollbackDiffLogItem{
			buildRollbackDiffLogItem(body.Part, fromValue, toValue),
		})
		if err := arcadeinternal.UpdateArcadeFieldsTxWithLogs(
			txApp,
			body.Arcade,
			map[string]any{field: toValue},
			map[string]any{body.Part: rollbackLog},
			re.Auth.Id,
		); err != nil {
			return err
		}

		if body.Report {
			report, reportErr := createArcadeEditReportTx(txApp, CreateArcadeEditReportBody{
				Arcade:    body.Arcade,
				Changelog: body.Changelog,
				Urgency:   "high",
				Message:   body.ReportMessage,
			}, re.Auth, rollbackReportKind)
			if reportErr != nil {
				return reportErr
			}
			requestAdminID = report.Id
		}

		return nil
	})
	if err != nil {
		var validationErr *rollbackValidationError
		if errors.As(err, &validationErr) {
			return re.JSON(http.StatusBadRequest, map[string]any{
				"error":   "validation failed",
				"details": validationErr.Error(),
			})
		}
		var reportErr *editReportError
		if errors.As(err, &reportErr) {
			return re.JSON(reportErr.status, map[string]any{
				"error": reportErr.message,
			})
		}
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "rollback failed",
			"details": err.Error(),
		})
	}

	return re.JSON(http.StatusOK, map[string]any{
		"arcade":             body.Arcade,
		"part":               body.Part,
		"from":               fromValue,
		"to":                 toValue,
		body.Part:            toValue,
		"reported":           body.Report,
		"request_admin_id":   requestAdminID,
		"request_admin_sent": requestAdminID != "",
	})
}
