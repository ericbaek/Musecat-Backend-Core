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
	"fixed": {}, "issue_persist": {}, "wrong": {},
}

func reactionExp(reaction string) int {
	switch strings.TrimSpace(reaction) {
	case "issue_persist":
		return 2
	case "fixed":
		return 3
	default:
		// A wrong vote is useful moderation input but is not an XP action.
		return 0
	}
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
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "invalid JSON body", "details": err.Error()})
	}
	if err := validateUpdateArcadeFlagReactionBody(&body); err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "validation failed", "details": err.Error()})
	}

	var reactionID string
	var xpFeedback userhandler.ExpFeedback
	now := time.Now().UTC()
	err = re.App.RunInTransaction(func(txApp core.App) error {
		flagRec, err := txApp.FindRecordById(arcadeinternal.CollectionArcadeFlag, body.Flag)
		if err != nil {
			return fmt.Errorf("flag not found: %w", err)
		}
		if flagRec.GetBool("solved") {
			return fmt.Errorf("flag is already solved")
		}
		if solved, err := arcadeinternal.ReconcileFlagResolutionTx(txApp, flagRec, now, false); err != nil {
			return err
		} else if solved {
			return fmt.Errorf("flag is already solved")
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
			if body.Reaction == "issue_persist" {
				if flagRec.GetString("resolution_vote_state") == arcadeinternal.FlagResolutionStateActive {
					return fmt.Errorf("issue_persist is only available before resolution voting starts")
				}
				if err := ensureIssuePersistAvailable(txApp, re.Auth.Id, body.Flag, now); err != nil {
					return err
				}
				if err := createFlagReactionTx(txApp, re.Auth.Id, body.Flag, body.Reaction, "", 0, &reactionID); err != nil {
					return err
				}
			} else {
				if body.Reaction == "wrong" && flagRec.GetString("resolution_vote_state") != arcadeinternal.FlagResolutionStateActive {
					return fmt.Errorf("wrong vote is only available after resolution voting starts")
				}
				if flagRec.GetString("resolution_vote_state") != arcadeinternal.FlagResolutionStateActive {
					if err := arcadeinternal.StartFlagResolutionTx(txApp, flagRec, now); err != nil {
						return err
					}
				}
				round := flagRec.GetString("resolution_vote_round")
				currentVote, err := findCurrentUserVote(txApp, body.Flag, round, re.Auth.Id)
				if err != nil {
					return err
				}
				if currentVote != nil {
					if currentVote.Reaction == body.Reaction {
						return fmt.Errorf("reaction %s already exists for this user", body.Reaction)
					}
					if err := removeReactionRecordTx(txApp, currentVote.Record, re.Auth.Id, baseExp, &currentExp); err != nil {
						return err
					}
				}
				if flagRec.GetString("resolution_vote_mode") == arcadeinternal.FlagResolutionModeStale {
					flagRec.Set("resolution_vote_mode", arcadeinternal.FlagResolutionModeStandard)
					if err := txApp.Save(flagRec); err != nil {
						return fmt.Errorf("failed to activate stale resolution vote: %w", err)
					}
				}
				level, err := arcadeinternal.LevelSnapshot(txApp, re.Auth.Id)
				if err != nil {
					return fmt.Errorf("failed to snapshot user level: %w", err)
				}
				if err := createFlagReactionTx(txApp, re.Auth.Id, body.Flag, body.Reaction, round, level, &reactionID); err != nil {
					return err
				}
			}
			if arcadeRec.GetBool("public") {
				nextExp, _, err := userhandler.AwardExpTx(txApp, re.Auth.Id, userhandler.FlagReactionKind(reactionID), reactionExp(body.Reaction), currentExp)
				if err != nil {
					return err
				}
				currentExp = nextExp
			}
			xpFeedback = userhandler.BuildExpFeedback(baseExp, currentExp)
			if _, err = arcadeinternal.ReconcileFlagResolutionTx(txApp, flagRec, now, true); err != nil {
				return err
			}
			return touchFlagActivityTx(txApp, flagRec)
		}

		var target *core.Record
		if body.Reaction == "issue_persist" {
			target, err = findLatestReaction(txApp, re.Auth.Id, body.Flag, body.Reaction, "", "")
		} else {
			round := flagRec.GetString("resolution_vote_round")
			vote, voteErr := findCurrentUserVote(txApp, body.Flag, round, re.Auth.Id)
			if voteErr != nil {
				return voteErr
			}
			if vote != nil && vote.Reaction == body.Reaction {
				target = vote.Record
			}
		}
		if err != nil {
			return err
		}
		if target == nil {
			return fmt.Errorf("no deletable reaction found")
		}
		createdAt := target.GetDateTime("created").Time().UTC()
		if createdAt.IsZero() {
			createdAt = now
		}
		if now.Sub(createdAt) > reactionDeleteWindow {
			return fmt.Errorf("reaction can only be deleted within 15 minutes of creation")
		}
		reactionID = target.Id
		if err := removeReactionRecordTx(txApp, target, re.Auth.Id, baseExp, &currentExp); err != nil {
			return err
		}
		xpFeedback = userhandler.BuildExpFeedback(baseExp, currentExp)
		if body.Reaction != "issue_persist" {
			if _, err = arcadeinternal.ReconcileFlagResolutionTx(txApp, flagRec, now, true); err != nil {
				return err
			}
		}
		return touchFlagActivityTx(txApp, flagRec)
	})
	if err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "reaction update failed", "details": err.Error()})
	}

	flagRec, err := re.App.FindRecordById(arcadeinternal.CollectionArcadeFlag, body.Flag)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to load updated flag", "details": err.Error()})
	}
	resolution, err := arcadeinternal.BuildFlagResolutionValueForUser(re.App, flagRec, re.Auth.Id)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to build flag resolution", "details": err.Error()})
	}
	reportHistory, err := arcadeinternal.BuildFlagReportHistoryValue(re.App, flagRec)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to build flag report history", "details": err.Error()})
	}

	out := map[string]any{
		"flag": body.Flag, "reaction": body.Reaction, "action": body.Action,
		"reaction_id": reactionID, "solved": flagRec.GetBool("solved"),
		"xp_feedback": xpFeedback, "resolution": resolution, "reportHistory": reportHistory,
	}
	gameValue, _ := arcadeinternal.BuildExpandedGameValueForArcadeFlag(re.App, flagRec.GetString("arcade"), body.Flag)
	out["game"] = gameValue
	return re.JSON(http.StatusOK, out)
}

func touchFlagActivityTx(app core.App, flagRec *core.Record) error {
	if err := app.Save(flagRec); err != nil {
		return fmt.Errorf("failed to update flag activity timestamp: %w", err)
	}
	return nil
}

func ensureIssuePersistAvailable(app core.App, userID, flagID string, now time.Time) error {
	last, err := findLatestReaction(app, userID, flagID, "issue_persist", "", "")
	if err != nil {
		return err
	}
	if last == nil {
		return nil
	}
	lastCreated := last.GetDateTime("created").Time().UTC()
	if !lastCreated.IsZero() && now.Sub(lastCreated) < issuePersistCooldown {
		return fmt.Errorf("issue_persist can be reported again only after 24 hours")
	}
	return nil
}

func findLatestReaction(app core.App, userID, flagID, reaction, context, round string) (*core.Record, error) {
	filter := "flag={:flag} && reaction={:reaction} && createdBy={:user}"
	params := dbx.Params{"flag": flagID, "reaction": reaction, "user": userID}
	if context != "" {
		filter += " && resolution_context={:context}"
		params["context"] = context
	}
	if round != "" {
		filter += " && vote_round={:round}"
		params["round"] = round
	}
	records, err := app.FindRecordsByFilter(arcadeinternal.CollectionArcadeFlagReaction, filter, "-created", 1, 0, params)
	if err != nil {
		return nil, fmt.Errorf("failed to query existing reaction: %w", err)
	}
	if len(records) == 0 {
		return nil, nil
	}
	return records[0], nil
}

func findCurrentUserVote(app core.App, flagID, round, userID string) (*arcadeinternal.FlagResolutionVote, error) {
	votes, err := arcadeinternal.FindCurrentFlagVotes(app, flagID, round)
	if err != nil {
		return nil, err
	}
	for _, vote := range votes {
		if vote.UserID == userID {
			copy := vote
			return &copy, nil
		}
	}
	return nil, nil
}

func createFlagReactionTx(app core.App, userID, flagID, reaction, round string, level int, outReactionID *string) error {
	collection, err := app.FindCollectionByNameOrId(arcadeinternal.CollectionArcadeFlagReaction)
	if err != nil {
		return fmt.Errorf("failed to find arcade_flag_reaction: %w", err)
	}
	rec := core.NewRecord(collection)
	rec.Set("flag", flagID)
	rec.Set("reaction", reaction)
	rec.Set("createdBy", userID)
	if reaction == "issue_persist" {
		rec.Set("resolution_context", arcadeinternal.FlagResolutionContextReport)
	} else {
		rec.Set("resolution_context", arcadeinternal.FlagResolutionContextVote)
		rec.Set("vote_round", round)
		rec.Set("level_snapshot", level)
	}
	if err := app.Save(rec); err != nil {
		return fmt.Errorf("failed to create reaction: %w", err)
	}
	*outReactionID = rec.Id
	return nil
}

func removeReactionRecordTx(app core.App, target *core.Record, userID string, baseExp int, currentExp *int) error {
	reactionID := target.Id
	if err := app.Delete(target); err != nil {
		return fmt.Errorf("failed to delete reaction: %w", err)
	}
	wasAwarded, err := userhandler.HasLevelLogKind(app, userID, userhandler.FlagReactionKind(reactionID))
	if err != nil {
		return err
	}
	if !wasAwarded {
		return nil
	}
	// Read the original grant so deleting a reaction also remains correct for
	// rows awarded under an earlier policy. New wrong votes have no positive
	// ledger row and therefore reach the !wasAwarded branch above.
	positiveRows, err := app.FindRecordsByFilter(
		userhandler.CollectionUserLevelLog,
		"user={:user} && kind={:kind}",
		"",
		1,
		0,
		dbx.Params{"user": userID, "kind": userhandler.FlagReactionKind(reactionID)},
	)
	if err != nil {
		return fmt.Errorf("failed to load reaction xp grant: %w", err)
	}
	if len(positiveRows) == 0 || positiveRows[0].GetInt("diff_exp") <= 0 {
		return nil
	}
	grant := positiveRows[0].GetInt("diff_exp")
	nextExp, _, err := userhandler.AwardExpTx(app, userID, "xp:flag-reaction-delete:"+reactionID, -grant, baseExp)
	if err != nil {
		return err
	}
	*currentExp = nextExp
	return nil
}
