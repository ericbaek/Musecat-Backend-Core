package messaging

import (
	"database/sql"
	"errors"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/router"
)

// PlazaID identifies the single members-only public conversation. Access is
// derived from current account status, never a materialized membership roster.
const PlazaID = "musecatplaza001"

func OpenPlaza(app core.App, user string) (Summary, error) {
	if _, err := ActiveUser(app, user); err != nil {
		return Summary{}, err
	}
	if c, err := app.FindRecordById(Conversations, PlazaID); err == nil {
		if c.GetString("kind") != "plaza" {
			return Summary{}, router.NewApiError(409, "Plaza identity is unavailable.", nil)
		}
		return plazaSummary(c, user), nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return Summary{}, err
	}
	err := app.RunInTransaction(func(tx core.App) error {
		if _, err := ActiveUser(tx, user); err != nil {
			return err
		}
		c, err := tx.FindRecordById(Conversations, PlazaID)
		if err == nil {
			if c.GetString("kind") != "plaza" {
				return router.NewApiError(409, "Plaza identity is unavailable.", nil)
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		_, err = record(tx, Conversations, map[string]any{"id": PlazaID, "kind": "plaza"})
		return err
	})
	if err != nil {
		return Summary{}, err
	}
	return Get(app, user, PlazaID)
}

// A public room has no private inbox preview or per-member unread/read receipts.
// Its summary reads no message rows, member roster or historical COUNT aggregate.
func plazaSummary(c *core.Record, user string) Summary {
	s := Summary{
		ID: c.Id, Kind: "plaza", CreatedAt: c.GetString("created"),
		Counterpart:    Party{Type: "team", ID: "plaza", Name: "광장", RoleTags: []string{}},
		LatestSequence: c.GetInt("last_sequence"), CanSend: true,
		AllowedSenderIdentities: []Identity{{"user", user}}, AllowedStatusTransitions: []string{},
	}
	if at := c.GetString("last_message_at"); at != "" {
		s.LastMessageAt = ptr(at)
	}
	return s
}
