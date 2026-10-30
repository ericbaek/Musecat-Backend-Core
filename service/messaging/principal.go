package messaging

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"

	"github.com/ericbaek/musecat-backend-core/service/supportfeedback"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
)

// Principal is supplied by the authenticated transport, never by a message body.
// Guest credentials authorize inquiries only and are never account identities.
type Principal struct {
	UserID   string
	GuestKey string
}

type participant struct {
	ID        string
	Kind      string
	User      *core.Record
	guestHash string
}

func (p Principal) resolve(app core.App) (*participant, error) {
	if p.UserID != "" && p.GuestKey == "" {
		u, err := ActiveUser(app, p.UserID)
		if err != nil {
			return nil, err
		}
		return &participant{ID: u.Id, Kind: "user", User: u}, nil
	}
	hash := supportfeedback.GuestKeyHash(p.GuestKey)
	if p.UserID != "" || hash == "" {
		return nil, apis.NewUnauthorizedError("Inquiry credentials required.", nil)
	}
	return &participant{ID: GuestIdentity(hash), Kind: "guest", guestHash: hash}, nil
}

// GuestIdentity is an opaque, domain-separated display/read identity. It does
// not expose the stored credential digest and cannot itself authorize access.
func GuestIdentity(hash string) string {
	sum := sha256.Sum256([]byte("messaging-guest:" + hash))
	return "g_" + hex.EncodeToString(sum[:])[:30]
}

func requesterType(q *core.Record) string {
	if q.GetString("requester_type") == "guest" {
		return "guest"
	}
	return "user"
}

func (p *participant) requests(q *core.Record) bool {
	if requesterType(q) != p.Kind || q.GetString("requester") != p.ID {
		return false
	}
	return p.Kind == "user" || (p.guestHash != "" && subtle.ConstantTimeCompare([]byte(q.GetString("guest_key_hash")), []byte(p.guestHash)) == 1)
}

func ViewerIdentity(app core.App, viewer Principal) (Identity, error) {
	p, err := viewer.resolve(app)
	if err != nil {
		return Identity{}, err
	}
	return Identity{Type: p.Kind, ID: p.ID}, nil
}
