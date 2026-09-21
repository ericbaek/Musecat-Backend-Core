package arcadeinternal

import (
	"errors"
	"strings"

	"github.com/pocketbase/pocketbase/core"
)

var ErrArcadeWriteForbidden = errors.New("arcade editing is not permitted")

// CanWriteArcade allows authenticated users to edit public arcades and limits
// private drafts to their creator or a strict reviewer. Routes check active status.
func CanWriteArcade(auth, arcade *core.Record) bool {
	if auth == nil || arcade == nil {
		return false
	}
	if arcade.GetBool("public") || arcade.GetString("createdBy") == auth.Id {
		return true
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
