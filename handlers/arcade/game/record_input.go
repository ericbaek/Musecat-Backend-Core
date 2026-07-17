package game

import (
	"fmt"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
)

// BuildGameAtomInputFromRecord converts a stored arcade_game_atoms record into an input shape
// suitable for version-cloning flows.
func BuildGameAtomInputFromRecord(atom *core.Record) (GameAtomInput, error) {
	if atom == nil {
		return GameAtomInput{}, fmt.Errorf("atom is required")
	}

	input := GameAtomInput{
		Game:      strings.TrimSpace(atom.GetString("game")),
		Location:  strings.TrimSpace(atom.GetString("location")),
		Quantity:  atom.GetInt("quantity"),
		Uncertain: atom.GetBool("uncertain"),
		PrevGame:  strings.TrimSpace(atom.GetString("prev_game")),
		RawPrice:  atom.Get("price"),
		RawTag:    atom.Get("tag"),
	}

	return input, nil
}

// BuildUpdateBodyFromCurrentState clones the selected immutable state into an
// update body. Callers mutate only the returned in-memory rows; UpdateArcadeGameTx
// then creates a new batch rather than touching any legacy atom or revision.
func BuildUpdateBodyFromCurrentState(app core.App, arcadeID string) (UpdateArcadeGameBody, error) {
	arcade, err := app.FindRecordById(arcadeinternal.CollectionArcade, arcadeID)
	if err != nil {
		return UpdateArcadeGameBody{}, fmt.Errorf("arcade not found: %w", err)
	}
	stateID := strings.TrimSpace(arcade.GetString("game_state"))
	if stateID == "" {
		return UpdateArcadeGameBody{}, fmt.Errorf("arcade.game_state is empty")
	}
	revisions, err := app.FindRecordsByFilter(arcadeinternal.CollectionArcadeGameRevision, "batch={:batch}", "created", 0, 0, dbx.Params{"batch": stateID})
	if err != nil {
		return UpdateArcadeGameBody{}, err
	}
	body := UpdateArcadeGameBody{Arcade: arcadeID, BaseStateID: stateID, Games: make([]GameAtomInput, 0, len(revisions))}
	for _, revision := range revisions {
		body.Games = append(body.Games, GameAtomInput{ID: revision.GetString("entry"), Game: revision.GetString("version"), Location: revision.GetString("location"), Quantity: revision.GetInt("quantity"), Uncertain: revision.GetBool("uncertain"), PrevGame: revision.GetString("previous_version"), RawPrice: revision.Get("price"), RawTag: revision.Get("tag")})
	}
	return body, nil
}
