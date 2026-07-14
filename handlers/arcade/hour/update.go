package hour

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/pocketbase/pocketbase/core"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
	userhandler "github.com/ericbaek/musecat-backend-core/handlers/user"
)

// DayHours supports either an object {start,end} or a bare number 499 (closed).
type DayHours struct {
	Start   *int `json:"start,omitempty"`
	End     *int `json:"end,omitempty"`
	Closed  bool `json:"-"`
	Unknown bool `json:"-"`
}

type dayHoursParseError struct {
	message string
}

func (e *dayHoursParseError) Error() string {
	return e.message
}

func (d *DayHours) UnmarshalJSON(b []byte) error {
	// null => unknown (reporter doesn't know)
	if string(b) == "null" {
		d.Unknown = true
		d.Closed = false
		d.Start, d.End = nil, nil
		return nil
	}
	// accept bare number 499
	var n int
	if err := json.Unmarshal(b, &n); err == nil {
		if n == 499 {
			d.Closed = true
			d.Start, d.End = nil, nil
			d.Unknown = false
			return nil
		}
		return &dayHoursParseError{
			message: "day hours must be 499 for closed, null for unknown, or {\"start\":...,\"end\":...} for open hours",
		}
	}
	// accept object with start/end
	var aux struct {
		Start *int `json:"start"`
		End   *int `json:"end"`
	}
	if err := json.Unmarshal(b, &aux); err != nil {
		return err
	}
	d.Start = aux.Start
	d.End = aux.End
	d.Closed = false
	d.Unknown = false
	return nil
}

// UpdateArcadeHourBody represents the request body for updating arcade hours.
type UpdateArcadeHourBody struct {
	Arcade    string    `json:"arcade"`
	Monday    *DayHours `json:"Monday,omitempty"`
	Tuesday   *DayHours `json:"Tuesday,omitempty"`
	Wednesday *DayHours `json:"Wednesday,omitempty"`
	Thursday  *DayHours `json:"Thursday,omitempty"`
	Friday    *DayHours `json:"Friday,omitempty"`
	Saturday  *DayHours `json:"Saturday,omitempty"`
	Sunday    *DayHours `json:"Sunday,omitempty"`
	Note      string    `json:"note,omitempty"`
}

func parseUpdateHourBody(re *core.RequestEvent) (UpdateArcadeHourBody, error) {
	var body UpdateArcadeHourBody
	err := json.NewDecoder(re.Request.Body).Decode(&body)
	return body, err
}

func validateDay(label string, d DayHours) error {
	if d.Unknown {
		// unknown is allowed – no validation
		return nil
	}
	if d.Closed {
		return nil
	}
	if d.Start == nil || d.End == nil {
		return fmt.Errorf("%s must be 499 for closed, or an object with both start and end", label)
	}
	s, e := *d.Start, *d.End

	// 24-hour operation can be represented as either 00:00-00:00 or 00:00-24:00.
	if s == 0 && (e == 0 || e == 2400) {
		return nil
	}

	if s < 0 || s > 2359 || e < 0 || e > 2400 {
		return fmt.Errorf("%s start/end must be between 0000 and 2359; end may be 2400 only for 24-hour operation", label)
	}
	if s%100 >= 60 || e%100 >= 60 {
		return fmt.Errorf("%s start/end minutes must be < 60", label)
	}
	// s > e means the place closes after midnight (next day), which is valid.
	if s == e {
		return fmt.Errorf("%s start and end must differ; use 499 for closed or 00:00-00:00 / 00:00-24:00 for 24-hour operation", label)
	}
	return nil
}

func validateUpdateHourBody(body UpdateArcadeHourBody) error {
	if body.Arcade == "" {
		return fmt.Errorf("arcade is required")
	}
	if body.Monday != nil {
		if err := validateDay("Monday", *body.Monday); err != nil {
			return err
		}
	}
	if body.Tuesday != nil {
		if err := validateDay("Tuesday", *body.Tuesday); err != nil {
			return err
		}
	}
	if body.Wednesday != nil {
		if err := validateDay("Wednesday", *body.Wednesday); err != nil {
			return err
		}
	}
	if body.Thursday != nil {
		if err := validateDay("Thursday", *body.Thursday); err != nil {
			return err
		}
	}
	if body.Friday != nil {
		if err := validateDay("Friday", *body.Friday); err != nil {
			return err
		}
	}
	if body.Saturday != nil {
		if err := validateDay("Saturday", *body.Saturday); err != nil {
			return err
		}
	}
	if body.Sunday != nil {
		if err := validateDay("Sunday", *body.Sunday); err != nil {
			return err
		}
	}
	return nil
}

// UpdateArcadeHour 는 새 arcade_hour 레코드를 만들고 arcade.hour를 그 레코드로 갱신한다.
// 즉시 반영되는 경로이며, arcade 업데이트 실패 시에는 가능한 범위에서 롤백을 시도한다.
func UpdateArcadeHour(re *core.RequestEvent) error {
	body, err := parseUpdateHourBody(re)
	if err != nil {
		var dayErr *dayHoursParseError
		if errors.As(err, &dayErr) {
			return re.JSON(http.StatusBadRequest, map[string]any{
				"error":   "validation failed",
				"details": dayErr.Error(),
			})
		}
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "invalid JSON body",
			"details": err.Error(),
		})
	}
	if err := validateUpdateHourBody(body); err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "validation failed",
			"details": err.Error(),
		})
	}

	var hourValue map[string]any
	var xpFeedback userhandler.ExpFeedback

	if err := re.App.RunInTransaction(func(txApp core.App) error {
		// 먼저 arcade가 실제로 존재하는지 확인한 뒤, 기존 hour를 읽어 변경 전 상태를 잡아둔다.
		arcadeRec, err := txApp.FindRecordById(arcadeinternal.CollectionArcade, body.Arcade)
		if err != nil {
			return fmt.Errorf("arcade not found: %w", err)
		}
		var prevHourRec *core.Record
		oldHourID := strings.TrimSpace(arcadeRec.GetString("hour"))
		if oldHourID != "" {
			prevHourRec, err = txApp.FindRecordById(arcadeinternal.CollectionArcadeHour, oldHourID)
			if err != nil {
				return fmt.Errorf("failed to load previous arcade_hour: %w", err)
			}
		}

		// 새 arcade_hour 레코드를 생성한다. 이 레코드가 이번 요청의 결과값이 된다.
		hourColl, err := txApp.FindCollectionByNameOrId(arcadeinternal.CollectionArcadeHour)
		if err != nil {
			return fmt.Errorf("failed to find arcade_hour: %w", err)
		}
		baseExp, err := userhandler.LoadCurrentExp(txApp, re.Auth.Id)
		if err != nil {
			return fmt.Errorf("failed to load current exp: %w", err)
		}
		currentExp := baseExp
		hourRec := core.NewRecord(hourColl)
		hourRec.Set("arcade", body.Arcade)
		// unknown/null, closed(499), open({start,end})를 명시적으로 구분해 저장한다.
		setDay := func(field string, d *DayHours) {
			if d == nil || d.Unknown {
				hourRec.Set(field, nil)
				return
			}
			if d.Closed {
				hourRec.Set(field, 499)
			} else {
				hourRec.Set(field, map[string]int{"start": *d.Start, "end": *d.End})
			}
		}
		setDay("Monday", body.Monday)
		setDay("Tuesday", body.Tuesday)
		setDay("Wednesday", body.Wednesday)
		setDay("Thursday", body.Thursday)
		setDay("Friday", body.Friday)
		setDay("Saturday", body.Saturday)
		setDay("Sunday", body.Sunday)
		if body.Note != "" {
			hourRec.Set("Note", body.Note)
			hourRec.Set("note", body.Note)
		}
		hourRec.Set("createdBy", re.Auth.Id)

		if err := txApp.Save(hourRec); err != nil {
			return fmt.Errorf("failed to create arcade_hour: %w", err)
		}

		// changelog는 이전 값과 새 값을 비교해야 하므로, body와 기존 레코드 스냅샷을 둘 다 만든다.
		prevSnapshot := buildHourSnapshotFromRecord(prevHourRec)
		nextSnapshot := buildHourSnapshotFromBody(body)
		providedFields := buildHourProvidedFieldSet(body)
		hourChangeLog := arcadeinternal.BuildChangelogEnvelope("hour", []hourDiffLogItem{
			buildHourDiffLogItem(prevSnapshot, nextSnapshot, providedFields, prevHourRec != nil),
		})
		if err := arcadeinternal.UpdateArcadeFieldsTxWithLogs(
			txApp,
			body.Arcade,
			map[string]any{"hour": hourRec.Id},
			map[string]any{"hour": hourChangeLog},
			re.Auth.Id,
		); err != nil {
			return fmt.Errorf("failed to update arcade.hour: %w", err)
		}
		if arcadeRec.GetBool("public") {
			nextExp, _, err := userhandler.AwardArcadeEditExpTx(txApp, re.Auth.Id, body.Arcade, "hour", 3, baseExp, time.Now().UTC())
			if err != nil {
				return err
			}
			currentExp = nextExp
		}

		hourValue = BuildArcadeHourExpandedValue(hourRec)
		xpFeedback = userhandler.BuildExpFeedback(baseExp, currentExp)
		return nil
	}); err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "transaction failed",
			"details": err.Error(),
		})
	}

	return re.JSON(http.StatusOK, map[string]any{
		"arcade":      body.Arcade,
		"hour":        hourValue,
		"xp_feedback": xpFeedback,
	})
}
