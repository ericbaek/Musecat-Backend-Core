package messaging

import (
	"github.com/pocketbase/pocketbase/tools/filesystem"
	"testing"
)

func TestInputHashChecksActualPhotoBytes(t *testing.T) {
	bytes := make([]byte, 15_000_001)
	copy(bytes, []byte("GIF89a\x01\x00\x01\x00"))
	file, err := filesystem.NewFileFromBytes(bytes, "photo.gif")
	if err != nil {
		t.Fatal(err)
	}
	file.Size = 1
	if _, err = inputHash(&SendInput{ClientMessageID: "photo", Photos: []*filesystem.File{file}}); err == nil {
		t.Fatal("declared size bypassed actual byte limit")
	}
	for _, bad := range []*filesystem.File{nil, {}} {
		if _, err = inputHash(&SendInput{ClientMessageID: "photo", Photos: []*filesystem.File{bad}}); err == nil {
			t.Fatal("invalid photo accepted")
		}
	}
}
