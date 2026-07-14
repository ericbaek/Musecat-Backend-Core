package flag

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
	userhandler "github.com/ericbaek/musecat-backend-core/handlers/user"
)

const (
	reactionDeleteWindow = 15 * time.Minute
	issuePersistCooldown = 24 * time.Hour
)

var validReactionTypes = map[string]struct{}{
	"fixed":         {},
	"issue_persist": {},
	"wrong":         {},
}

type UpdateArcadeFlagReactionBody struct {
	Flag     string `json:"flag"`
	Reaction string `json:"reaction"`
	Action   string `json:"action"` // add | delete
}

func parseUpdateArcadeFlagReactionBody(re *core.RequestEvent) (UpdateArcadeFlagReactionBody, error) {
	var body UpdateArcadeFlagReactionBody
	err := json.NewDecoder(re.Request.Body).Decode(&body)
	return body, err
}

func validateUpdateArcadeFlagReactionBody(body *UpdateArcadeFlagReactionBody) error {
	body.Flag = strings.TrimSpace(body.Flag)
	body.Reaction = strings.TrimSpace(body.Reaction)
	body.Action = strings.TrimSpace(strings.ToLower(body.Action))

	if body.Flag == "" {
		return fmt.Errorf("flag is required")
	}
	if body.Reaction == "" {
		return fmt.Errorf("reaction is required")
	}
	if _, ok := validReactionTypes[body.Reaction]; !ok {
		return fmt.Errorf("reaction must be one of fixed, issue_persist, wrong")
	}
	if body.Action == "" {
		return fmt.Errorf("action is required")
	}
	if body.Action != "add" && body.Action != "delete" {
		return fmt.Errorf("action must be one of add, delete")
	}

	return nil
}

func UpdateArcadeFlagReaction(re *core.RequestEvent) error {
	body, err := parseUpdateArcadeFlagReactionBody(re)
	if err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "invalid JSON body",
			"details": err.Error(),
		})
	}
	if err := validateUpdateArcadeFlagReactionBody(&body); err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "validation failed",
			"details": err.Error(),
		})
	}

	var reactionID string
	var xpFeedback userhandler.ExpFeedback
	now := time.Now().UTC()
	immediateSolve := body.Action == "add" && (body.Reaction == "fixed" || body.Reaction == "wrong") && hasAnyAuthTag(re.Auth)

	if err := re.App.RunInTransaction(func(txApp core.App) error {
		flagRec, err := txApp.FindRecordById(arcadeinternal.CollectionArcadeFlag, body.Flag)
		if err != nil {
			return fmt.Errorf("flag not found: %w", err)
		}
		arcadeRec, err := txApp.FindRecordById(arcadeinternal.CollectionArcade, flagRec.GetString("arcade"))
		if err != nil {
			return fmt.Errorf("arcade not found: %w", err)
		}
		baseExp, err := userhandler.LoadCurrentExp(txApp, re.Auth.Id)
		if err != nil {
			return fmt.Errorf("failed to load current exp: %w", err)
		}
		currentExp := baseExp

		if body.Action == "add" {
			if err := addFlagReactionTx(txApp, re.Auth.Id, body.Flag, body.Reaction, now, &reactionID); err != nil {
				return err
			}
			if arcadeRec.GetBool("public") {
				nextExp, _, err := userhandler.AwardExpTx(txApp, re.Auth.Id, userhandler.FlagReactionKind(reactionID), 3, baseExp)
				if err != nil {
					return err
				}
				currentExp = nextExp
			}
			xpFeedback = userhandler.BuildExpFeedback(baseExp, currentExp)
			if immediateSolve {
				return solveFlagImmediatelyTx(txApp, body.Flag)
			}
			return nil
		}
		if err := deleteFlagReactionTx(txApp, re.Auth.Id, body.Flag, body.Reaction, now, &reactionID); err != nil {
			return err
		}
		if reactionID != "" {
			positiveKind := userhandler.FlagReactionKind(reactionID)
			wasAwarded, err := userhandler.HasLevelLogKind(txApp, re.Auth.Id, positiveKind)
			if err != nil {
				return err
			}
			if wasAwarded {
				nextExp, _, err := userhandler.AwardExpTx(txApp, re.Auth.Id, "xp:flag-reaction-delete:"+reactionID, -3, baseExp)
				if err != nil {
					return err
				}
				currentExp = nextExp
			}
		}
		xpFeedback = userhandler.BuildExpFeedback(baseExp, currentExp)
		return nil
	}); err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "reaction update failed",
			"details": err.Error(),
		})
	}

	if body.Action == "add" && !immediateSolve {
		if _, err := RunAutoSolveForFlag(re.App, body.Flag, now); err != nil {
			return re.JSON(http.StatusBadGateway, map[string]any{
				"error":   "failed to evaluate flag solved state",
				"details": err.Error(),
			})
		}
	}

	flagRec, err := re.App.FindRecordById(arcadeinternal.CollectionArcadeFlag, body.Flag)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "failed to load updated flag",
			"details": err.Error(),
		})
	}

	out := map[string]any{
		"flag":        body.Flag,
		"reaction":    body.Reaction,
		"action":      body.Action,
		"reaction_id": reactionID,
		"solved":      flagRec.GetBool("solved"),
		"xp_feedback": xpFeedback,
	}
	gameValue, _ := arcadeinternal.BuildExpandedGameValueForArcadeFlag(re.App, flagRec.GetString("arcade"), body.Flag)
	out["game"] = gameValue

	return re.JSON(http.StatusOK, out)
}

func hasAnyAuthTag(auth *core.Record) bool {
	if auth == nil {
		return false
	}

	return len(auth.GetStringSlice("tag")) > 0 || len(auth.GetStringSlice("tags")) > 0
}

func solveFlagImmediatelyTx(txApp core.App, flagID string) error {
	flagRec, err := txApp.FindRecordById(arcadeinternal.CollectionArcadeFlag, flagID)
	if err != nil {
		return fmt.Errorf("failed to reload flag for immediate solve: %w", err)
	}

	if flagRec.GetBool("solved") {
		return nil
	}

	flagRec.Set("solved", true)
	if err := txApp.Save(flagRec); err != nil {
		return fmt.Errorf("failed to solve flag immediately: %w", err)
	}

	return nil
}

func addFlagReactionTx(txApp core.App, userID, flagID, reaction string, now time.Time, outReactionID *string) error {
	existing, err := txApp.FindRecordsByFilter(
		arcadeinternal.CollectionArcadeFlagReaction,
		"flag={:flag} && reaction={:reaction} && createdBy={:user}",
		"-created",
		1,
		0,
		dbx.Params{"flag": flagID, "reaction": reaction, "user": userID},
	)
	if err != nil {
		return fmt.Errorf("failed to query existing reaction: %w", err)
	}

	if reaction == "fixed" || reaction == "wrong" {
		if len(existing) > 0 {
			return fmt.Errorf("reaction %s already exists for this user", reaction)
		}
	}

	if reaction == "issue_persist" && len(existing) > 0 {
		lastCreated := existing[0].GetDateTime("created").Time().UTC()
		if !lastCreated.IsZero() {
			if now.Sub(lastCreated) < issuePersistCooldown {
				return fmt.Errorf("issue_persist can be reported again only after 24 hours")
			}
		}
	}

	reactionColl, err := txApp.FindCollectionByNameOrId(arcadeinternal.CollectionArcadeFlagReaction)
	if err != nil {
		return fmt.Errorf("failed to find arcade_flag_reaction: %w", err)
	}

	rec := core.NewRecord(reactionColl)
	rec.Set("flag", flagID)
	rec.Set("reaction", reaction)
	rec.Set("createdBy", userID)
	if err := txApp.Save(rec); err != nil {
		return fmt.Errorf("failed to create reaction: %w", err)
	}

	*outReactionID = rec.Id
	return nil
}

func deleteFlagReactionTx(txApp core.App, userID, flagID, reaction string, now time.Time, outReactionID *string) error {
	existing, err := txApp.FindRecordsByFilter(
		arcadeinternal.CollectionArcadeFlagReaction,
		"flag={:flag} && reaction={:reaction} && createdBy={:user}",
		"-created",
		1,
		0,
		dbx.Params{"flag": flagID, "reaction": reaction, "user": userID},
	)
	if err != nil {
		return fmt.Errorf("failed to query reaction for delete: %w", err)
	}
	if len(existing) == 0 {
		return fmt.Errorf("no deletable reaction found")
	}

	target := existing[0]
	createdAt := target.GetDateTime("created").Time().UTC()
	if createdAt.IsZero() {
		createdAt = now
	}
	if now.Sub(createdAt) > reactionDeleteWindow {
		return fmt.Errorf("reaction can only be deleted within 15 minutes of creation")
	}

	if err := txApp.Delete(target); err != nil {
		return fmt.Errorf("failed to delete reaction: %w", err)
	}

	*outReactionID = target.Id
	return nil
}
