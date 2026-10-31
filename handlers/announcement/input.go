package announcement

import (
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/ericbaek/musecat-backend-core/service/announcement"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"
	_ "golang.org/x/image/webp"
)

type inputSlide struct {
	Image string `json:"image"`
	Alt   string `json:"alt"`
}
type inputLocalization struct {
	Title   string       `json:"title"`
	Summary string       `json:"summary"`
	Slides  []inputSlide `json:"slides"`
}
type mutationInput struct {
	Localizations map[string]inputLocalization  `json:"localizations"`
	Uploads       map[string][]*filesystem.File `json:"-"`
}

func decode(re *core.RequestEvent) (mutationInput, error) {
	var input mutationInput
	input.Uploads = map[string][]*filesystem.File{}
	if strings.HasPrefix(re.Request.Header.Get("Content-Type"), "multipart/form-data") {
		if err := re.Request.ParseMultipartForm(8 << 20); err != nil {
			if errors.Is(err, apis.ErrRequestEntityTooLarge) {
				return input, err
			}
			return input, re.BadRequestError("invalid multipart body", err)
		}
		if err := json.Unmarshal([]byte(re.Request.FormValue("localizations")), &input.Localizations); err != nil {
			return input, re.BadRequestError("invalid localizations", err)
		}
		for _, locale := range announcement.Locales {
			files, err := re.FindUploadedFiles("images_" + locale)
			if err != nil && !errors.Is(err, http.ErrMissingFile) {
				return input, re.BadRequestError("invalid images", err)
			}
			if len(files) > announcement.MaxSlides {
				return input, re.BadRequestError("too many images", nil)
			}
			for _, file := range files {
				if file.Size > announcement.MaxImageBytes {
					return input, re.Error(http.StatusRequestEntityTooLarge, "image exceeds 5 MiB", nil)
				}
				if err := validateImage(file); err != nil {
					return input, re.BadRequestError(err.Error(), nil)
				}
			}
			input.Uploads[locale] = files
		}
	} else if err := re.BindBody(&input); err != nil {
		if errors.Is(err, apis.ErrRequestEntityTooLarge) {
			return input, err
		}
		return input, re.BadRequestError("invalid announcement body", err)
	}
	if input.Localizations == nil {
		return input, re.BadRequestError("localizations is required", nil)
	}
	return input, nil
}

func validateImage(file *filesystem.File) error {
	reader, err := file.Reader.Open()
	if err != nil {
		return err
	}
	defer reader.Close()
	config, format, err := image.DecodeConfig(reader)
	if err != nil || !slices.Contains([]string{"png", "jpeg", "webp"}, format) {
		return fmt.Errorf("images must be valid PNG, JPEG or WebP files")
	}
	if config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 40000000 {
		return fmt.Errorf("image dimensions exceed 40 megapixels")
	}
	return nil
}

func applyInput(row *core.Record, input mutationInput) error {
	localized := map[string]announcement.Localization{}
	files := []any{}
	seen := map[string]bool{}
	for locale, content := range input.Localizations {
		if !validLocale(locale) {
			return fmt.Errorf("unsupported locale %q", locale)
		}
		content.Title, content.Summary = strings.TrimSpace(content.Title), strings.TrimSpace(content.Summary)
		if utf8.RuneCountInString(content.Title) > 160 || utf8.RuneCountInString(content.Summary) > 500 || len(content.Slides) > announcement.MaxSlides {
			return fmt.Errorf("title, summary or slide limit exceeded")
		}
		output := announcement.Localization{Title: content.Title, Summary: content.Summary, Slides: []announcement.Slide{}}
		usedUploads := map[int]bool{}
		for _, slide := range content.Slides {
			alt := strings.TrimSpace(slide.Alt)
			if utf8.RuneCountInString(alt) > 3000 {
				return fmt.Errorf("alt text exceeds 3000 characters")
			}
			filename := slide.Image
			var file any = filename
			if strings.HasPrefix(slide.Image, "upload:") {
				index, err := strconv.Atoi(strings.TrimPrefix(slide.Image, "upload:"))
				if err != nil || index < 0 || index >= len(input.Uploads[locale]) || usedUploads[index] {
					return fmt.Errorf("invalid or duplicate upload reference for %s", locale)
				}
				usedUploads[index] = true
				upload := input.Uploads[locale][index]
				filename, file = upload.Name, upload
			} else if !slices.Contains(row.GetStringSlice("images"), filename) {
				return fmt.Errorf("image does not belong to this announcement")
			}
			if seen[filename] {
				return fmt.Errorf("duplicate image reference")
			}
			seen[filename] = true
			files = append(files, file)
			output.Slides = append(output.Slides, announcement.Slide{FileName: filename, Alt: alt})
		}
		if len(usedUploads) != len(input.Uploads[locale]) {
			return fmt.Errorf("unreferenced uploads for %s", locale)
		}
		localized[locale] = output
	}
	for locale, uploads := range input.Uploads {
		if len(uploads) > 0 {
			if _, ok := localized[locale]; !ok {
				return fmt.Errorf("uploads require %s localization", locale)
			}
		}
	}
	row.Set("images", files)
	row.Set("localizations", localized)
	return nil
}
