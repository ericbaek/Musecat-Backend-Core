package basic

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/pocketbase/pocketbase/core"

	"github.com/ericbaek/musecat-backend-core/geo"
	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
)

type NewArcadeBody struct {
	Name       string                   `json:"name"`
	Location   *arcadeinternal.Location `json:"location"`
	Address    string                   `json:"address"`   // 도로명 주소
	Direction  string                   `json:"direction"` // 실내, 찾기 어려운 경우 부연설명
	Nickname   []string                 `json:"nickname"`
	SubwayLine []string                 `json:"subway_line"`
}

func parseRequestBody(re *core.RequestEvent) (NewArcadeBody, error) {
	var body NewArcadeBody
	if err := json.NewDecoder(re.Request.Body).Decode(&body); err != nil {
		return body, err
	}
	if body.SubwayLine == nil {
		body.SubwayLine = []string{}
	}
	return body, nil
}

func saveArcadeRecord(re *core.RequestEvent, body NewArcadeBody, res geo.Result) (string, error) {
	var arcadeID string
	err := re.App.RunInTransaction(func(txApp core.App) error {
		arcadeColl, err := txApp.FindCollectionByNameOrId(arcadeinternal.CollectionArcade)
		if err != nil {
			return fmt.Errorf("failed to reach arcade collection: %w", err)
		}

		arcade := core.NewRecord(arcadeColl)
		arcade.Set("country", res.Country)
		arcade.Set("timezone", res.Timezone)
		arcade.Set("createdBy", re.Auth.Id)

		if err := txApp.Save(arcade); err != nil {
			return fmt.Errorf("failed to create arcade: %w", err)
		}

		// 2) create arcade_basic referencing the arcade id
		basicColl, err := txApp.FindCollectionByNameOrId(arcadeinternal.CollectionArcadeBasic)
		if err != nil {
			return fmt.Errorf("failed to reach arcade_basic collection: %w", err)
		}

		basic := core.NewRecord(basicColl)
		basic.Set("name", body.Name)
		basic.Set("location", map[string]any{"lat": body.Location.Lat, "lon": body.Location.Lon})
		basic.Set("address", body.Address)
		basic.Set("direction", body.Direction)
		basic.Set("nickname", body.Nickname)
		basic.Set("subway_line", body.SubwayLine)
		basic.Set("createdBy", re.Auth.Id)
		basic.Set("arcade", arcade.Id)

		if err := txApp.Save(basic); err != nil {
			return fmt.Errorf("failed to create arcade_basic: %w", err)
		}

		// 3) link back: set arcade.basic to the basic id (with changelog)
		initialBasicFields := BasicFields{
			Name:       body.Name,
			Address:    body.Address,
			Direction:  body.Direction,
			Nickname:   body.Nickname,
			SubwayLine: body.SubwayLine,
			Lat:        body.Location.Lat,
			Lon:        body.Location.Lon,
		}
		basicChangeLog := arcadeinternal.BuildChangelogEnvelope("basic", []basicDiffLogItem{
			buildBasicDiffLogItem(nil, initialBasicFields),
		})
		if err := arcadeinternal.UpdateArcadeFieldsTxWithLogs(
			txApp,
			arcade.Id,
			map[string]any{"basic": basic.Id},
			map[string]any{"basic": basicChangeLog},
			re.Auth.Id,
		); err != nil {
			return fmt.Errorf("failed to link arcade.basic: %w", err)
		}

		arcadeID = arcade.Id
		return nil
	})

	return arcadeID, err
}

func validateRequestBody(body NewArcadeBody) error {
	if body.Name == "" {
		return fmt.Errorf("name is required")
	}
	if body.Address == "" {
		return fmt.Errorf("address is required")
	}
	return arcadeinternal.ValidateRequiredLocation(body.Location)
}

func NewArcade(re *core.RequestEvent) error {
	// 1. 요청 파싱
	body, err := parseRequestBody(re)
	if err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "invalid JSON body",
			"details": err.Error(),
		})
	}

	// 2. 필수 필드 검증
	if err := validateRequestBody(body); err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "validation failed",
			"details": err.Error(),
		})
	}

	// 3. 위치 정보 조회
	res, err := geo.LookupCountryAndTimezone(re.Request.Context(), body.Location.Lat, body.Location.Lon)
	if err != nil {
		return re.JSON(http.StatusServiceUnavailable, map[string]any{
			"error":   "geo lookup failed",
			"details": err.Error(),
		})
	}

	// 4. 오락실 데이터 생성
	id, err := saveArcadeRecord(re, body, res)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "failed to generate arcade",
			"details": err.Error(),
		})
	}

	// 5. 성공 응답
	return re.JSON(http.StatusOK, map[string]any{
		"id":          id,
		"name":        body.Name,
		"location":    body.Location,
		"address":     body.Address,
		"direction":   body.Direction,
		"nickname":    body.Nickname,
		"subway_line": body.SubwayLine,
		"country":     res.Country,
		"timezone":    res.Timezone,
	})
}
