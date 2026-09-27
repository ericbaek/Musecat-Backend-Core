package arcadeinternal

import (
	"fmt"
	"strings"

	"github.com/gabriel-vasile/mimetype"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/tools/filesystem"
)

// ValidateImageUploads enforces the custom API limit before database writes.
func ValidateImageUploads(files []*filesystem.File, maxCount int, maxBytes int64) error {
	if len(files) > maxCount {
		return fmt.Errorf("photos must have at most %d items", maxCount)
	}
	for _, file := range files {
		if file.Size > maxBytes {
			return apis.ErrRequestEntityTooLarge
		}
		reader, err := file.Reader.Open()
		if err != nil {
			return fmt.Errorf("failed to read photo")
		}
		mime, err := mimetype.DetectReader(reader)
		reader.Close()
		if err != nil || !strings.HasPrefix(mime.String(), "image/") {
			return fmt.Errorf("photos must be images")
		}
	}
	return nil
}
