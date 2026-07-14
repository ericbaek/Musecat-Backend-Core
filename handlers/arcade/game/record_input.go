package game

import (
	"fmt"
	"strings"

	"github.com/pocketbase/pocketbase/core"
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
