package community

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/png"
	"testing"

	"github.com/pocketbase/pocketbase/tools/filesystem"
)

func TestPhotoDimensionsBoundDecodedAllocation(t *testing.T) {
	var source bytes.Buffer
	if err := png.Encode(&source, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name          string
		width, height uint32
		allowed       bool
	}{
		{"ordinary", 1, 1, true},
		{"48MP", 8000, 6000, true},
		{"boundary", 8000, 8000, true},
		{"over boundary", 8000, 8001, false},
		{"tiny compressed oversized header", 1000000, 1000000, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := append([]byte(nil), source.Bytes()...)
			binary.BigEndian.PutUint32(data[16:20], tc.width)
			binary.BigEndian.PutUint32(data[20:24], tc.height)
			binary.BigEndian.PutUint32(data[29:33], crc32.ChecksumIEEE(data[12:29]))
			file, err := filesystem.NewFileFromBytes(data, "photo.png")
			if err != nil {
				t.Fatal(err)
			}
			err = validatePhotos([]*filesystem.File{file}, MaxPostPhotos)
			if (err == nil) != tc.allowed {
				t.Fatalf("allowed=%v error=%v", tc.allowed, err)
			}
		})
	}
	file, err := filesystem.NewFileFromBytes(source.Bytes()[:25], "truncated.png")
	if err != nil {
		t.Fatal(err)
	}
	if validatePhotos([]*filesystem.File{file}, MaxPostPhotos) == nil {
		t.Fatal("truncated image header accepted")
	}
}
