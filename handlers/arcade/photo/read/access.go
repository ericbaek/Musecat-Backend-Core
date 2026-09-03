package read

import (
	"net/url"
	"strings"

	"github.com/pocketbase/pocketbase/core"
)

func FileURL(id string) string {
	return "/arcade/photo/file?id=" + url.QueryEscape(id)
}

func CanReadAtom(auth, arcade, atom *core.Record) bool {
	return arcade != nil && atom != nil && atom.GetString("arcade") == arcade.Id &&
		((arcade.GetBool("public") && atom.GetBool("public")) || CanAccessAtoms(auth, arcade))
}

func CanAccessAtoms(auth, arcade *core.Record) bool {
	return auth != nil && arcade != nil &&
		(arcade.GetBool("public") || arcade.GetString("createdBy") == auth.Id || HasStrictReviewerTag(auth))
}

func HasStrictReviewerTag(auth *core.Record) bool {
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
