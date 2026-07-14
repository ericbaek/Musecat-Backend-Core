package user

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"
)

var (
	errSignupUsernameExists = fmt.Errorf("signup_username_exists")
	errSignupUserInfoExists = fmt.Errorf("signup_user_info_exists")
	errSignupWithdrawn      = fmt.Errorf("signup_withdrawn_user")
	errSignupUsernameTaken  = fmt.Errorf("signup_username_taken")
)

var signupUsernamePattern = regexp.MustCompile(`^[A-Za-z0-9]+$`)

const signupUsernameMaxLength = 15

type signUpBody struct {
	Username string `json:"username"`
	Nickname string `json:"nickname"`
	Bio      string `json:"bio"`
}

// SignUp initializes a one-time username and user_info for authenticated users.
// It only succeeds when both username and user_info record are missing.
func SignUp(re *core.RequestEvent) error {
	if re.Auth == nil {
		return re.JSON(http.StatusUnauthorized, map[string]any{
			"error": "authentication required",
		})
	}

	input, status, badReq := parseSignUpInput(re)
	if badReq != nil {
		return re.JSON(status, badReq)
	}

	username := strings.TrimSpace(input.Username)
	if username == "" {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error": "username is required",
		})
	}
	if len(username) < 4 {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error": "username must be at least 4 characters",
		})
	}
	if len(username) > signupUsernameMaxLength {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error": "username must be at most 15 characters",
		})
	}
	if !signupUsernamePattern.MatchString(username) {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error": "username must contain only letters and digits",
		})
	}

	nickname := strings.TrimSpace(input.Nickname)
	if nickname == "" {
		nickname = username
	}
	bio := strings.TrimSpace(input.Bio)

	var profile *Profile
	err := re.App.RunInTransaction(func(txApp core.App) error {
		userRec, err := txApp.FindRecordById(CollectionUser, re.Auth.Id)
		if err != nil {
			return fmt.Errorf("failed to load user: %w", err)
		}

		if userRec.GetBool("withdrawn") {
			return errSignupWithdrawn
		}

		if strings.TrimSpace(userRec.GetString("username")) != "" {
			return errSignupUsernameExists
		}

		if _, err := txApp.FindRecordById(CollectionUserInfo, userRec.Id); err == nil {
			return errSignupUserInfoExists
		} else if !isNotFoundError(err) {
			return fmt.Errorf("failed to check user_info: %w", err)
		}

		userRec.Set("username", username)
		if saveErr := txApp.Save(userRec); saveErr != nil {
			if isUsernameUniqueConstraintError(saveErr) {
				return errSignupUsernameTaken
			}
			return fmt.Errorf("failed to update user username: %w", saveErr)
		}

		userInfoColl, err := txApp.FindCollectionByNameOrId(CollectionUserInfo)
		if err != nil {
			return fmt.Errorf("failed to load user_info collection: %w", err)
		}
		userInfoRec := core.NewRecord(userInfoColl)
		userInfoRec.Set("id", userRec.Id)
		userInfoRec.Set("nickname", nickname)
		userInfoRec.Set("bio", bio)
		userInfoRec.Set("series_public", true)
		userInfoRec.Set("visit_visibility", "summary")
		if input.Avatar == nil {
			userInfoRec.Set("avatar", []string{})
		} else {
			userInfoRec.Set("avatar", input.Avatar)
		}
		if err := txApp.Save(userInfoRec); err != nil {
			return fmt.Errorf("failed to create user_info: %w", err)
		}

		profile = mergeProfileFromRecords(txApp, userRec, userInfoRec, false)
		return nil
	})
	if err != nil {
		switch err {
		case errSignupUsernameExists:
			return re.JSON(http.StatusConflict, map[string]any{
				"error": "signup requires empty username",
			})
		case errSignupUserInfoExists:
			return re.JSON(http.StatusConflict, map[string]any{
				"error": "signup requires missing user_info",
			})
		case errSignupWithdrawn:
			return re.JSON(http.StatusForbidden, map[string]any{
				"error": "withdrawn account cannot sign up",
			})
		case errSignupUsernameTaken:
			return re.JSON(http.StatusConflict, map[string]any{
				"error": "username already taken",
			})
		default:
			return re.JSON(http.StatusBadGateway, map[string]any{
				"error":   "signup failed",
				"details": err.Error(),
			})
		}
	}

	return re.JSON(http.StatusOK, map[string]any{
		"success": true,
		"profile": profile,
	})
}

type signUpInput struct {
	Username string
	Nickname string
	Bio      string
	Avatar   *filesystem.File
}

func parseSignUpInput(re *core.RequestEvent) (signUpInput, int, map[string]any) {
	contentType := strings.ToLower(strings.TrimSpace(re.Request.Header.Get("Content-Type")))
	if strings.HasPrefix(contentType, "multipart/form-data") {
		if err := re.Request.ParseMultipartForm(10 << 20); err != nil {
			return signUpInput{}, http.StatusBadRequest, map[string]any{
				"error":   "invalid multipart body",
				"details": err.Error(),
			}
		}

		in := signUpInput{
			Username: strings.TrimSpace(re.Request.FormValue("username")),
			Nickname: strings.TrimSpace(re.Request.FormValue("nickname")),
			Bio:      strings.TrimSpace(re.Request.FormValue("bio")),
		}

		files, err := re.FindUploadedFiles("avatar")
		if err != nil {
			if errors.Is(err, http.ErrMissingFile) {
				return in, 0, nil
			}
			return signUpInput{}, http.StatusBadRequest, map[string]any{
				"error":   "invalid multipart body",
				"details": err.Error(),
			}
		}
		if len(files) > 1 {
			return signUpInput{}, http.StatusBadRequest, map[string]any{
				"error": "avatar must have at most one file",
			}
		}
		if len(files) == 1 {
			in.Avatar = files[0]
		}

		return in, 0, nil
	}

	var body signUpBody
	if err := json.NewDecoder(re.Request.Body).Decode(&body); err != nil {
		return signUpInput{}, http.StatusBadRequest, map[string]any{
			"error":   "invalid JSON body",
			"details": err.Error(),
		}
	}

	return signUpInput{
		Username: strings.TrimSpace(body.Username),
		Nickname: strings.TrimSpace(body.Nickname),
		Bio:      strings.TrimSpace(body.Bio),
	}, 0, nil
}

func isUsernameUniqueConstraintError(err error) bool {
	if err == nil {
		return false
	}

	msg := err.Error()
	lowerMsg := strings.ToLower(msg)
	return strings.Contains(msg, "idx_username_lower__pb_users_auth_") ||
		strings.Contains(msg, "UNIQUE constraint failed: index") ||
		(strings.Contains(lowerMsg, "username") && strings.Contains(lowerMsg, "value must be unique"))
}
