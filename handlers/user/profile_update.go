package user

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"
)

const maxProfileImageSize = 15_000_000

type profileInput struct {
	nickname           string
	bio                string
	sns                json.RawMessage
	position           json.RawMessage
	avatar             *filesystem.File
	background         *filesystem.File
	avatarDelete       bool
	backgroundDelete   bool
	backgroundProvided bool
	positionProvided   bool
}

func UpdateProfile(re *core.RequestEvent) error {
	if re.Auth == nil {
		return re.JSON(http.StatusUnauthorized, map[string]any{"error": "authentication required"})
	}
	in, err := parseProfileInput(re)
	if err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "invalid profile", "details": err.Error()})
	}
	in.nickname = strings.TrimSpace(in.nickname)
	if in.nickname == "" || utf8.RuneCountInString(in.nickname) > 25 {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "nickname must be 1 to 25 characters"})
	}
	for _, r := range in.nickname {
		if (r >= 0x1F1E6 && r <= 0x1F1FF) || (r >= 0xE0020 && r <= 0xE007F) {
			return re.JSON(http.StatusBadRequest, map[string]any{"error": "nickname cannot contain flag emoji"})
		}
	}
	in.bio = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(in.bio, "\r\n", "\n"), "\r", "\n"))
	if utf8.RuneCountInString(in.bio) > 100 || strings.Count(in.bio, "\n") >= 5 {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "bio must be at most 100 characters and 5 lines"})
	}
	if len(in.sns) > 0 {
		var sns struct {
			Items []ProfileSNSItem `json:"items"`
		}
		if err := json.Unmarshal(in.sns, &sns); err != nil || sns.Items == nil {
			return re.JSON(http.StatusBadRequest, map[string]any{"error": "sns.items must be an array"})
		}
		allowed := map[string]bool{"website": true, "twitter": true, "instagram": true, "facebook": true, "discord": true, "threads": true}
		seen := map[string]bool{}
		for i := range sns.Items {
			item := &sns.Items[i]
			item.Type = strings.TrimSpace(item.Type)
			item.Link = strings.TrimSpace(item.Link)
			if !allowed[item.Type] || seen[item.Type] || item.Link == "" {
				return re.JSON(http.StatusBadRequest, map[string]any{"error": "invalid or duplicate social link"})
			}
			if item.Type != "discord" {
				u, err := url.Parse(item.Link)
				if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
					return re.JSON(http.StatusBadRequest, map[string]any{"error": "invalid social link URL"})
				}
			}
			seen[item.Type] = true
		}
		in.sns, _ = json.Marshal(sns)
	}
	if in.backgroundProvided || in.positionProvided {
		exp, err := LoadCurrentExp(re.App, re.Auth.Id)
		if err != nil {
			return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to load user level"})
		}
		if LevelFromExp(exp) < multiProfileCountriesLevel {
			return re.JSON(http.StatusForbidden, map[string]any{"error": "profile background requires level 15"})
		}
	}
	if in.positionProvided {
		var position struct {
			X *float64 `json:"x"`
			Y *float64 `json:"y"`
		}
		if err := json.Unmarshal(in.position, &position); err != nil || position.X == nil || position.Y == nil || math.IsNaN(*position.X) || math.IsNaN(*position.Y) || math.IsInf(*position.X, 0) || math.IsInf(*position.Y, 0) || *position.X < 0 || *position.X > 100 || *position.Y < 0 || *position.Y > 100 {
			return re.JSON(http.StatusBadRequest, map[string]any{"error": "background_position must have x and y between 0 and 100"})
		}
	}
	rec, err := re.App.FindRecordById(CollectionUserInfo, re.Auth.Id)
	if err != nil {
		return re.JSON(http.StatusConflict, map[string]any{"error": "user_info is required"})
	}
	rec.Set("nickname", in.nickname)
	rec.Set("bio", in.bio)
	if len(in.sns) > 0 {
		rec.Set("sns", string(in.sns))
	}
	if in.positionProvided {
		rec.Set("background_position", string(in.position))
	}
	if in.avatarDelete {
		rec.Set("avatar", []string{})
	} else if in.avatar != nil {
		rec.Set("avatar", in.avatar)
	}
	if in.backgroundDelete {
		rec.Set("background", []string{})
	} else if in.background != nil {
		rec.Set("background", in.background)
	}
	if err := re.App.Save(rec); err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to update profile", "details": err.Error()})
	}
	profile, err := BuildProfileFromAuth(re.App, re.Auth)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to load updated profile"})
	}
	return re.JSON(http.StatusOK, profile)
}

func parseProfileInput(re *core.RequestEvent) (profileInput, error) {
	contentType := strings.ToLower(re.Request.Header.Get("Content-Type"))
	if strings.HasPrefix(contentType, "multipart/form-data") {
		if err := re.Request.ParseMultipartForm(35 << 20); err != nil {
			return profileInput{}, err
		}
		form := re.Request.MultipartForm
		in := profileInput{nickname: re.Request.FormValue("nickname"), bio: re.Request.FormValue("bio")}
		if values, ok := form.Value["sns"]; ok {
			in.sns = json.RawMessage(values[0])
		}
		if values, ok := form.Value["background_position"]; ok {
			in.positionProvided = true
			in.position = json.RawMessage(values[0])
		}
		for _, field := range []string{"avatar", "background"} {
			values, textPresent := form.Value[field]
			files, err := re.FindUploadedFiles(field)
			if err != nil && !errors.Is(err, http.ErrMissingFile) {
				return profileInput{}, err
			}
			if len(files) > 1 || (textPresent && len(files) > 0) {
				return profileInput{}, fmt.Errorf("%s must have one file or a deletion marker", field)
			}
			if textPresent && (len(values) != 1 || (values[0] != "" && values[0] != "null")) {
				return profileInput{}, fmt.Errorf("invalid %s deletion marker", field)
			}
			if len(files) == 1 {
				if err := validateProfileImage(field, files[0]); err != nil {
					return profileInput{}, err
				}
			}
			if field == "avatar" {
				in.avatarDelete = textPresent
				if len(files) == 1 {
					in.avatar = files[0]
				}
			} else {
				in.backgroundProvided = textPresent || len(files) == 1
				in.backgroundDelete = textPresent
				if len(files) == 1 {
					in.background = files[0]
				}
			}
		}
		return in, nil
	}
	if !strings.HasPrefix(contentType, "application/json") {
		return profileInput{}, fmt.Errorf("content type must be application/json or multipart/form-data")
	}
	var body map[string]json.RawMessage
	if err := json.NewDecoder(re.Request.Body).Decode(&body); err != nil {
		return profileInput{}, err
	}
	var in profileInput
	if err := json.Unmarshal(body["nickname"], &in.nickname); err != nil {
		return profileInput{}, fmt.Errorf("nickname must be a string")
	}
	if raw, ok := body["bio"]; ok {
		if string(raw) == "null" || json.Unmarshal(raw, &in.bio) != nil {
			return profileInput{}, fmt.Errorf("bio must be a string")
		}
	}
	in.sns = body["sns"]
	if raw, ok := body["background_position"]; ok {
		in.positionProvided = true
		in.position = raw
	}
	for _, field := range []string{"avatar", "background"} {
		if raw, ok := body[field]; ok {
			if string(raw) != "null" && string(raw) != `""` {
				return profileInput{}, fmt.Errorf("%s must be null or empty", field)
			}
			if field == "avatar" {
				in.avatarDelete = true
			} else {
				in.backgroundDelete = true
				in.backgroundProvided = true
			}
		}
	}
	return in, nil
}

func validateProfileImage(field string, file *filesystem.File) error {
	if file.Size <= 0 || file.Size > maxProfileImageSize {
		return fmt.Errorf("%s must be at most 15 MB", field)
	}
	reader, err := file.Reader.Open()
	if err != nil {
		return err
	}
	defer reader.Close()
	buf := make([]byte, 512)
	n, err := reader.Read(buf)
	if err != nil && err != io.EOF {
		return err
	}
	mime := http.DetectContentType(buf[:n])
	if mime != "image/png" && mime != "image/jpeg" {
		return fmt.Errorf("%s must be PNG or JPEG", field)
	}
	return nil
}
