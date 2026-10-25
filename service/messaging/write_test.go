package messaging

import (
	"github.com/pocketbase/pocketbase/core"
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

func TestInputHashMentionsPreserveLegacyPayload(t *testing.T) {
	in := SendInput{Text: " hello ", ClientMessageID: "legacy"}
	plain, err := inputHash(&in)
	if err != nil || plain != "eccd87ce39b5aa1a8d8f5a7ae8c7f740a0b1d378ae40ab0fd03c28fd2e8b54af" {
		t.Fatal(plain, err)
	}
	in.Mentions = []string{"musecat", "musecat"}
	mentioned, err := inputHash(&in)
	if err != nil || mentioned == plain || len(in.Mentions) != 1 {
		t.Fatal(mentioned, in.Mentions, err)
	}
	canonical, err := inputHash(&in)
	if err != nil || canonical != mentioned {
		t.Fatal(canonical, err)
	}
	in.Mentions = []string{"unknown"}
	if _, err := inputHash(&in); err == nil {
		t.Fatal("unknown mention accepted")
	}
}

func TestReviewOldReportNormalizesMentions(t *testing.T) {
	col := core.NewBaseCollection(Reports)
	col.Fields.Add(&core.JSONField{Name: "evidence"})
	r := core.NewRecord(col)
	r.Set("evidence", map[string]any{"message": map[string]any{"id": "old"}})
	report, err := reviewDTO(r)
	if err != nil || report.Message.Mentions == nil || len(report.Message.Mentions) != 0 {
		t.Fatal(report, err)
	}
}
