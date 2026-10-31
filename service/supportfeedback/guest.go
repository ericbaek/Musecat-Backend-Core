package supportfeedback

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"regexp"

	"github.com/pocketbase/pocketbase/core"
)

const GuestHeader = "X-Feedback-Guest-Key"

var guestKeyPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

// Guest keys are browser-held credentials; only their digest is stored.
func GuestKeyHash(key string) string {
	if !guestKeyPattern.MatchString(key) {
		return ""
	}
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}
func CanAccessGuest(feedback *core.Record, key string) bool {
	hash := GuestKeyHash(key)
	stored := feedback.GetString("guest_key_hash")
	return feedback.GetString("createdBy") == "" && hash != "" && stored != "" && subtle.ConstantTimeCompare([]byte(hash), []byte(stored)) == 1
}
func EnsureGuestSchema(app core.App) error {
	return app.RunInTransaction(func(tx core.App) error {
		feedback, err := tx.FindCollectionByNameOrId("support_feedback")
		if err != nil {
			return err
		}
		if !feedback.IsBase() || feedback.System || feedback.ListRule != nil || feedback.ViewRule != nil || feedback.CreateRule != nil || feedback.UpdateRule != nil || feedback.DeleteRule != nil {
			return fmt.Errorf("incompatible support_feedback access policy")
		}
		if old := feedback.Fields.GetByName("guest_key_hash"); old == nil {
			feedback.Fields.Add(&core.TextField{Name: "guest_key_hash", Max: 64, Hidden: true})
		} else if field, ok := old.(*core.TextField); !ok || field.Max != 64 || !field.Hidden {
			return fmt.Errorf("incompatible guest key hash")
		}
		feedback.AddIndex("idx_feedback_guest", false, "guest_key_hash,updated", "guest_key_hash != ''")
		if err := tx.Save(feedback); err != nil {
			return err
		}
		messages, err := tx.FindCollectionByNameOrId(Messages)
		if err != nil {
			return err
		}
		if !messages.IsBase() || messages.System || messages.ListRule != nil || messages.ViewRule != nil || messages.CreateRule != nil || messages.UpdateRule != nil || messages.DeleteRule != nil {
			return fmt.Errorf("incompatible guest message access policy")
		}
		author, ok := messages.Fields.GetByName("author").(*core.RelationField)
		if !ok || author.MaxSelect != 1 {
			return fmt.Errorf("incompatible message author")
		}
		author.Required = false
		return tx.Save(messages)
	})
}
