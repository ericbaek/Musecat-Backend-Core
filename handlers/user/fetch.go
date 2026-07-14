package user

import (
	"database/sql"
	"errors"
	"io/fs"
	"net/http"
	"strings"

	"github.com/pocketbase/pocketbase/core"
)

// GetMe handles GET /user/me.
func GetMe(re *core.RequestEvent) error {
	if re.Auth == nil {
		return re.JSON(http.StatusUnauthorized, map[string]any{
			"error": "authentication required",
		})
	}

	profile, err := BuildProfileFromAuth(re.App, re.Auth)
	if err != nil {
		if isNotFoundError(err) {
			return re.JSON(http.StatusNotFound, map[string]any{
				"error": "user not found",
			})
		}

		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "failed to load user profile",
			"details": err.Error(),
		})
	}

	if profile == nil {
		return re.JSON(http.StatusNotFound, map[string]any{
			"error": "user not found",
		})
	}

	return re.JSON(http.StatusOK, profile)
}

// GetUserByID handles GET /user?id=<userId> or GET /user?username=<username>.
func GetUserByID(re *core.RequestEvent) error {
	id := strings.TrimSpace(re.Request.URL.Query().Get("id"))
	username := strings.TrimSpace(re.Request.URL.Query().Get("username"))

	var (
		profile *Profile
		err     error
	)
	switch {
	case id != "":
		profile, err = FetchMergedProfile(re.App, id)
	case username != "":
		profile, err = FetchMergedProfileByUsername(re.App, username)
	default:
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error": "missing required query param 'id' or 'username'",
		})
	}

	if err != nil {
		if isNotFoundError(err) {
			return re.JSON(http.StatusNotFound, map[string]any{
				"error": "user not found",
			})
		}

		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "failed to load user profile",
			"details": err.Error(),
		})
	}

	return re.JSON(http.StatusOK, profile)
}

func isNotFoundError(err error) bool {
	return errors.Is(err, sql.ErrNoRows) || errors.Is(err, fs.ErrNotExist)
}
