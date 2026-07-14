package photo

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
	userhandler "github.com/ericbaek/musecat-backend-core/handlers/user"
)

const maxUploadPhotosPerRequest = 10

var allowedPhotoMIMEs = map[string]struct{}{
	"image/png":              {},
	"image/vnd.mozilla.apng": {},
	"image/jpeg":             {},
}

type uploadSummary struct {
	Total   int `json:"total"`
	Success int `json:"success"`
	Failed  int `json:"failed"`
}

type uploadedPhotoItem struct {
	Index    int    `json:"index"`
	AtomID   string `json:"atomId"`
	Filename string `json:"filename"`
}

type failedPhotoItem struct {
	Index    int    `json:"index"`
	Filename string `json:"filename"`
	Reason   string `json:"reason"`
}

// UploadArcadePhotos uploads up to 10 photo files and creates arcade_photo_atoms records.
// It does not update arcade.photo molecule relation.
func UploadArcadePhotos(re *core.RequestEvent) error {
	if err := re.Request.ParseMultipartForm(32 << 20); err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "invalid multipart body",
			"details": err.Error(),
		})
	}

	arcadeID := strings.TrimSpace(re.Request.FormValue("arcade"))
	if arcadeID == "" {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "validation failed",
			"details": "arcade is required",
		})
	}

	files, err := re.FindUploadedFiles("photos")
	if err != nil {
		if errors.Is(err, http.ErrMissingFile) {
			return re.JSON(http.StatusBadRequest, map[string]any{
				"error":   "validation failed",
				"details": "photos must have at least one item",
			})
		}
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "invalid multipart body",
			"details": err.Error(),
		})
	}

	if len(files) > maxUploadPhotosPerRequest {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "validation failed",
			"details": fmt.Sprintf("photos must have at most %d items", maxUploadPhotosPerRequest),
		})
	}

	if _, err := re.App.FindRecordById(arcadeinternal.CollectionArcade, arcadeID); err != nil {
		return re.JSON(http.StatusNotFound, map[string]any{
			"error": "arcade not found",
		})
	}

	atomColl, err := re.App.FindCollectionByNameOrId(arcadeinternal.CollectionArcadePhotoAtoms)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "failed to load arcade photo atom collection",
			"details": err.Error(),
		})
	}

	uploaded := make([]uploadedPhotoItem, 0, len(files))
	failed := make([]failedPhotoItem, 0)

	for i, file := range files {
		filename := file.OriginalName
		if filename == "" {
			filename = file.Name
		}

		mimeType, err := detectFileMIME(file)
		if err != nil {
			failed = append(failed, failedPhotoItem{
				Index:    i,
				Filename: filename,
				Reason:   "failed to inspect file type",
			})
			continue
		}
		if !isAllowedPhotoMIME(mimeType) {
			failed = append(failed, failedPhotoItem{
				Index:    i,
				Filename: filename,
				Reason:   fmt.Sprintf("unsupported mime type: %s", mimeType),
			})
			continue
		}

		atom := core.NewRecord(atomColl)
		atom.Set("arcade", arcadeID)
		atom.Set("photo", file)
		atom.Set("public", false)
		if re.Auth != nil {
			atom.Set("createdBy", re.Auth.Id)
		}

		if err := re.App.Save(atom); err != nil {
			failed = append(failed, failedPhotoItem{
				Index:    i,
				Filename: filename,
				Reason:   err.Error(),
			})
			continue
		}

		uploaded = append(uploaded, uploadedPhotoItem{
			Index:    i,
			AtomID:   atom.Id,
			Filename: filename,
		})
	}

	summary := uploadSummary{
		Total:   len(files),
		Success: len(uploaded),
		Failed:  len(failed),
	}

	var xpFeedback userhandler.ExpFeedback
	if summary.Success > 0 {
		baseExp, err := userhandler.LoadCurrentExp(re.App, re.Auth.Id)
		if err != nil {
			return re.JSON(http.StatusBadGateway, map[string]any{
				"error":   "failed to load current exp",
				"details": err.Error(),
			})
		}
		currentExp, _, err := userhandler.AwardExpTx(re.App, re.Auth.Id, userhandler.ArcadePhotoSubmissionKind(arcadeID), 5, baseExp)
		if err != nil {
			return re.JSON(http.StatusBadGateway, map[string]any{
				"error":   "failed to award xp",
				"details": err.Error(),
			})
		}
		xpFeedback = userhandler.BuildExpFeedback(baseExp, currentExp)
	}

	status := http.StatusOK
	if summary.Success == 0 {
		status = http.StatusUnprocessableEntity
	} else if summary.Failed > 0 {
		status = http.StatusMultiStatus
	}

	return re.JSON(status, map[string]any{
		"arcade":      arcadeID,
		"summary":     summary,
		"uploaded":    uploaded,
		"failed":      failed,
		"xp_feedback": xpFeedback,
	})
}

func detectFileMIME(file *filesystem.File) (string, error) {
	reader, err := file.Reader.Open()
	if err != nil {
		return "", err
	}
	defer reader.Close()

	buf := make([]byte, 512)
	n, err := reader.Read(buf)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	if n == 0 {
		return "", fmt.Errorf("empty file")
	}

	return http.DetectContentType(buf[:n]), nil
}

func isAllowedPhotoMIME(mimeType string) bool {
	if _, ok := allowedPhotoMIMEs[mimeType]; ok {
		return true
	}
	// Many APNG files are detected as image/png by sniffing.
	if strings.HasPrefix(mimeType, "image/png") {
		return true
	}
	return false
}
