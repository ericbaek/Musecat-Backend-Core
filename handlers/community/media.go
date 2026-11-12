package community

import (
	"fmt"
	"github.com/gabriel-vasile/mimetype"
	"github.com/pocketbase/pocketbase/tools/filesystem"
	_ "golang.org/x/image/webp"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
)

const MaxPhotoPixels = 64_000_000

const MaxCommentPhotos = 3
const MaxCommentBodyBytes = MaxCommentPhotos*MaxPostPhotoBytes + (1 << 20)

func validatePhotos(photos []*filesystem.File, maximum int) error {
	if len(photos) > maximum {
		return fmt.Errorf("photos must have at most %d items", maximum)
	}
	for _, file := range photos {
		if file.Size > MaxPostPhotoBytes {
			return fmt.Errorf("each photo must be at most 10 MB")
		}
		reader, err := file.Reader.Open()
		if err != nil {
			return fmt.Errorf("failed to read photo")
		}
		mime, err := mimetype.DetectReader(reader)
		reader.Close()
		if err != nil || (mime.String() != "image/jpeg" && mime.String() != "image/png" && mime.String() != "image/webp" && mime.String() != "image/gif") {
			return fmt.Errorf("photos must be JPEG, PNG, WebP or GIF images")
		}
		reader, err = file.Reader.Open()
		if err != nil {
			return fmt.Errorf("failed to read photo")
		}
		config, _, decodeErr := image.DecodeConfig(reader)
		reader.Close()
		if decodeErr != nil || config.Width <= 0 || config.Height <= 0 {
			return fmt.Errorf("invalid photo dimensions")
		}
		if config.Width > MaxPhotoPixels/config.Height {
			return fmt.Errorf("photos must be at most 64 megapixels")
		}
	}
	return nil
}
