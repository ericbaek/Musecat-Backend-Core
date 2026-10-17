package connections_test

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ericbaek/musecat-backend-core/connections"
	"github.com/ericbaek/musecat-backend-core/delegation"
	"github.com/ericbaek/musecat-backend-core/handlers/user"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"golang.org/x/oauth2"
)

func jointFixture(t *testing.T) (*fixture, *connections.JointManager, *delegation.Manager, connections.Actor) {
	t.Helper()
	f := newFixture(t)
	d, err := delegation.NewManager("https://musecat.example", "https://musecat.example/consent", []delegation.Client{{ID: "chuni", Name: "CHUNITHM Info", Secret: strings.Repeat("s", 32), RedirectURIs: []string{"https://rhythm.example/callback"}, Scopes: []string{delegation.AccountAccess}}}, user.CheckActiveUser)
	if err != nil {
		t.Fatal(err)
	}
	if err = delegation.SynchronizeClients(f.app, d); err != nil {
		t.Fatal(err)
	}
	m, err := connections.NewJointManager(f.manager, d, []connections.JointPartner{{ID: "rhythm", ClientID: "chuni", Provider: "rhythm", Services: []string{"chunithm"}}})
	if err != nil {
		t.Fatal(err)
	}
	a := connections.Actor{UserID: f.user.Id, Session: f.token, Check: func(app core.App) error { return user.CheckActiveUser(app, f.user) }}
	return f, m, d, a
}

// Obtain real consent/code/token evidence rather than manufacturing a grant row.
func jointGrant(t *testing.T, f *fixture, d *delegation.Manager, redeem bool) (string, string) {
	t.Helper()
	verifier := oauth2.GenerateVerifier()
	target, err := d.Begin(f.app, delegation.AuthorizationInput{ClientID: "chuni", RedirectURI: "https://rhythm.example/callback", ResponseType: "code", State: "session-bound-state", Challenge: oauth2.S256ChallengeFromVerifier(verifier), ChallengeMethod: "S256"})
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(target)
	handle := u.Query().Get("request")
	actor := delegation.Actor{UserID: f.user.Id, Session: f.token}
	if _, err = d.ReadConsent(f.app, actor, handle); err != nil {
		t.Fatal(err)
	}
	callback, err := d.Decide(f.app, actor, delegation.Decision{Request: handle, Approve: true, Scopes: []string{delegation.AccountAccess}})
	if err != nil {
		t.Fatal(err)
	}
	if redeem {
		u, _ = url.Parse(callback)
		_, err = d.Exchange(f.app, "chuni", delegation.TokenInput{GrantType: "authorization_code", Code: u.Query().Get("code"), RedirectURI: "https://rhythm.example/callback", Verifier: verifier})
		if err != nil {
			t.Fatal(err)
		}
	}
	g, err := f.app.FindFirstRecordByFilter(delegation.Grants, "user={:user} && client='chuni'", dbx.Params{"user": f.user.Id})
	if err != nil {
		t.Fatal(err)
	}
	return g.Id, g.GetString("generation")
}

func TestJointConnectionNeedsBothVerifiedLegsAndRechecksRevocation(t *testing.T) {
	f, m, d, a := jointFixture(t)
	start, err := m.Start(f.app, a, "rhythm", f.subject)
	if err != nil {
		t.Fatal(err)
	}
	if start.Status != "pending" {
		t.Fatal(start)
	}
	grant, generation := jointGrant(t, f, d, false)
	if _, err = m.RecordVerified(f.app, a, start.ID, grant, generation, ""); err == nil {
		t.Fatal("unredeemed code counted as connected")
	}
	grant, generation = jointGrant(t, f, d, true)
	partial, err := m.RecordVerified(f.app, a, start.ID, grant, generation, "")
	if err != nil || partial.Status != "partial" || !partial.MusecatAccess || partial.PartnerAccess {
		t.Fatalf("%+v %v", partial, err)
	}
	account := f.complete(f.start(f.token, "rhythm", "chunithm"))
	complete, err := m.RecordVerified(f.app, a, start.ID, "", "", account)
	if err != nil || complete.Status != "connected" || !complete.MusecatAccess || !complete.PartnerAccess {
		t.Fatalf("%+v %v", complete, err)
	}
	if _, err = m.RecordVerified(f.app, a, start.ID, grant, generation, account); err != nil {
		t.Fatal("confirmation not idempotent", err)
	}
	payload, _ := json.Marshal(complete)
	if strings.Contains(string(payload), f.subject) || strings.Contains(string(payload), grant) || strings.Contains(string(payload), account) {
		t.Fatal("private proof exposed", string(payload))
	}
	// Grant remains valid after access expiry while a live refresh family exists.
	token, err := f.app.FindFirstRecordByFilter(delegation.Tokens, "grant={:grant}", dbx.Params{"grant": grant})
	if err != nil {
		t.Fatal(err)
	}
	token.Set("access_expires", time.Now().Add(-time.Minute))
	if err = f.app.Save(token); err != nil {
		t.Fatal(err)
	}
	got, err := m.Status(f.app, a, start.ID)
	if err != nil || got.Status != "connected" {
		t.Fatalf("%+v %v", got, err)
	}
	if err = delegation.RevokeGrant(f.app, a.UserID, grant); err != nil {
		t.Fatal(err)
	}
	got, err = m.Status(f.app, a, start.ID)
	if err != nil || got.Status != "degraded" || got.MusecatAccess {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestJointRejectsForeignSubjectSessionAndReauthorization(t *testing.T) {
	f, m, _, a := jointFixture(t)
	start, err := m.Start(f.app, a, "rhythm", "wrong-subject")
	if err != nil {
		t.Fatal(err)
	}
	account := f.complete(f.start(f.token, "rhythm", "chunithm"))
	if _, err = m.RecordVerified(f.app, a, start.ID, "", "", account); err == nil {
		t.Fatal("different partner account accepted")
	}
	start, err = m.Start(f.app, a, "rhythm", f.subject)
	if err != nil {
		t.Fatal(err)
	}
	foreignToken, foreign := f.newUser()
	other := connections.Actor{UserID: foreign.Id, Session: foreignToken, Check: a.Check}
	if _, err = m.Status(f.app, other, start.ID); err == nil {
		t.Fatal("foreign attempt readable")
	}
	other = connections.Actor{UserID: a.UserID, Session: foreignToken, Check: a.Check}
	if _, err = m.Status(f.app, other, start.ID); err == nil {
		t.Fatal("forged actor session accepted")
	}
	alternate := f.user.TokenKey()
	f.user.SetTokenKey(alternate + "changed")
	if err = f.app.Save(f.user); err != nil {
		t.Fatal(err)
	}
	if _, err = m.RecordVerified(f.app, a, start.ID, "", "", account); err == nil {
		t.Fatal("invalidated session accepted")
	}
}

func TestJointExpirySupersessionCancellationAndRefreshFamilyExpiry(t *testing.T) {
	f, m, d, a := jointFixture(t)
	start, err := m.Start(f.app, a, "rhythm", f.subject)
	if err != nil {
		t.Fatal(err)
	}
	next, err := m.Start(f.app, a, "rhythm", f.subject)
	if err != nil {
		t.Fatal(err)
	}
	old, _ := m.Status(f.app, a, start.ID)
	if old.Status != "cancelled" {
		t.Fatal(old)
	}
	grant, generation := jointGrant(t, f, d, true)
	if _, err = m.RecordVerified(f.app, a, start.ID, grant, generation, ""); err == nil {
		t.Fatal("superseded attempt revived")
	}
	record, err := f.app.FindRecordById(connections.JointAttempts, next.ID)
	if err != nil {
		t.Fatal(err)
	}
	record.Set("expires", time.Now().Add(-time.Minute))
	if err = f.app.Save(record); err != nil {
		t.Fatal(err)
	}
	expired, err := m.Status(f.app, a, next.ID)
	if err != nil || expired.Status != "expired" {
		t.Fatalf("%+v %v", expired, err)
	}
	if _, err = m.RecordVerified(f.app, a, next.ID, grant, generation, ""); err == nil {
		t.Fatal("expired attempt revived")
	}
	if err = m.Cancel(f.app, a, next.ID); err != nil {
		t.Fatal(err)
	}
	if err = d.VerifyAccountGrant(f.app, a.UserID, "chuni", grant, generation); err != nil {
		t.Fatal("cancel revoked independent grant", err)
	}
	token, err := f.app.FindFirstRecordByFilter(delegation.Tokens, "grant={:g}", dbx.Params{"g": grant})
	if err != nil {
		t.Fatal(err)
	}
	token.Set("refresh_expires", time.Now().Add(-time.Minute))
	if err = f.app.Save(token); err != nil {
		t.Fatal(err)
	}
	if err = d.VerifyAccountGrant(f.app, a.UserID, "chuni", grant, generation); err == nil {
		t.Fatal("expired refresh family accepted")
	}
	if err = connections.PurgeJointAttempts(f.app, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err = f.app.FindRecordById(connections.JointAttempts, next.ID); err == nil {
		t.Fatal("expired attempt not purged")
	}
}

func TestJointSchemaFailsClosed(t *testing.T) {
	f, _, _, _ := jointFixture(t)
	if err := connections.EnsureJointSchema(f.app); err != nil {
		t.Fatal(err)
	}
	c, err := f.app.FindCollectionByNameOrId(connections.JointAttempts)
	if err != nil {
		t.Fatal(err)
	}
	if c.ListRule != nil || c.ViewRule != nil || c.CreateRule != nil || c.UpdateRule != nil || c.DeleteRule != nil {
		t.Fatal("raw joint ledger exposed")
	}
	c.Fields.GetByName("partner_subject").(*core.TextField).Hidden = false
	if err = f.app.Save(c); err != nil {
		t.Fatal(err)
	}
	if err = connections.EnsureJointSchema(f.app); err == nil {
		t.Fatal("incompatible ledger accepted")
	}
}

func TestJointReapprovalDisconnectAndWithdrawalFenceCompletion(t *testing.T) {
	f, m, d, a := jointFixture(t)
	start, err := m.Start(f.app, a, "rhythm", f.subject)
	if err != nil {
		t.Fatal(err)
	}
	grant, generation := jointGrant(t, f, d, true)
	account := f.complete(f.start(f.token, "rhythm", "chunithm"))
	if _, err = m.RecordVerified(f.app, a, start.ID, grant, generation, account); err != nil {
		t.Fatal(err)
	}
	// A newer grant cannot inherit completion from an old generation.
	_, newGeneration := jointGrant(t, f, d, true)
	if generation == newGeneration {
		t.Fatal("fixture did not reapprove")
	}
	got, err := m.Status(f.app, a, start.ID)
	if err != nil || got.Status != "degraded" || got.MusecatAccess {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err = m.RecordVerified(f.app, a, start.ID, grant, newGeneration, ""); err == nil {
		t.Fatal("new grant substituted into old attempt")
	}
	next, err := m.Start(f.app, a, "rhythm", f.subject)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.RecordVerified(f.app, a, next.ID, grant, newGeneration, account); err != nil {
		t.Fatal(err)
	}
	if err = connections.Disconnect(f.app, a, account); err != nil {
		t.Fatal(err)
	}
	got, err = m.Status(f.app, a, next.ID)
	if err != nil || got.Status != "degraded" || got.PartnerAccess {
		t.Fatalf("%+v %v", got, err)
	}
	if err = connections.DeleteUserData(f.app, a.UserID); err != nil {
		t.Fatal(err)
	}
	if n := f.count(connections.JointAttempts); n != 0 {
		t.Fatal("joint data survived withdrawal", n)
	}
}

func TestJointRegistryMismatchAndProofsCannotComplete(t *testing.T) {
	f, m, d, a := jointFixture(t)
	for _, p := range []connections.JointPartner{
		{ID: "bad", ClientID: "missing", Provider: "rhythm", Services: []string{"chunithm"}},
		{ID: "bad", ClientID: "chuni", Provider: "missing", Services: []string{"chunithm"}},
		{ID: "bad", ClientID: "chuni", Provider: "rhythm", Services: []string{"missing"}},
	} {
		if _, err := connections.NewJointManager(f.manager, d, []connections.JointPartner{p}); err == nil {
			t.Fatal("unregistered binding accepted", p)
		}
	}
	start, err := m.Start(f.app, a, "rhythm", f.subject)
	if err != nil {
		t.Fatal(err)
	}
	grant, generation := jointGrant(t, f, d, true)
	record, err := f.app.FindRecordById(delegation.Grants, grant)
	if err != nil {
		t.Fatal(err)
	}
	_, foreign := f.newUser()
	record.Set("user", foreign.Id)
	if err = f.app.Save(record); err != nil {
		t.Fatal(err)
	}
	if _, err = m.RecordVerified(f.app, a, start.ID, grant, generation, ""); err == nil {
		t.Fatal("other user's grant accepted")
	}
	record.Set("user", a.UserID)
	if err = f.app.Save(record); err != nil {
		t.Fatal(err)
	}
	account := f.complete(f.start(f.token, "noslog", "chunithm"))
	if _, err = m.RecordVerified(f.app, a, start.ID, grant, generation, account); err == nil {
		t.Fatal("unrelated provider accepted")
	}
	// Config change invalidates an earlier attempt, even with matching subject text.
	changed := append([]connections.Provider{}, f.providers...)
	changed[0].ClientSecret = "new-provider-secret"
	cm, err := connections.NewManager(changed, []byte(strings.Repeat("k", 32)), nil)
	if err != nil {
		t.Fatal(err)
	}
	jm, err := connections.NewJointManager(cm, d, []connections.JointPartner{{ID: "rhythm", ClientID: "chuni", Provider: "rhythm", Services: []string{"chunithm"}}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := jm.Status(f.app, a, start.ID)
	if err != nil || got.Status != "invalidated" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestJointConnectionRevisionAndEncryptionKeyAreRechecked(t *testing.T) {
	f, m, d, a := jointFixture(t)
	start, err := m.Start(f.app, a, "rhythm", f.subject)
	if err != nil {
		t.Fatal(err)
	}
	grant, generation := jointGrant(t, f, d, true)
	account := f.complete(f.start(f.token, "rhythm", "chunithm"))
	if _, err = m.RecordVerified(f.app, a, start.ID, grant, generation, account); err != nil {
		t.Fatal(err)
	}
	changedKey, err := connections.NewManager(f.providers, []byte(strings.Repeat("x", 32)), nil)
	if err != nil {
		t.Fatal(err)
	}
	other, err := connections.NewJointManager(changedKey, d, []connections.JointPartner{{ID: "rhythm", ClientID: "chuni", Provider: "rhythm", Services: []string{"chunithm"}}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := other.Status(f.app, a, start.ID)
	if err != nil || got.Status != "degraded" || got.PartnerAccess {
		t.Fatalf("%+v %v", got, err)
	}
	// Reauthorization must change the proof even if the timestamp is identical.
	before, err := f.app.FindRecordById(connections.Accounts, account)
	if err != nil {
		t.Fatal(err)
	}
	oldUpdated := before.GetString("updated")
	f.complete(f.start(f.token, "rhythm", "chunithm"))
	_, err = f.app.DB().NewQuery("UPDATE external_account_connection SET updated={:updated} WHERE id={:id}").Bind(dbx.Params{"updated": oldUpdated, "id": account}).Execute()
	if err != nil {
		t.Fatal(err)
	}
	got, err = m.Status(f.app, a, start.ID)
	if err != nil || got.Status != "degraded" || got.PartnerAccess {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestParseJointConfig(t *testing.T) {
	f, _, d, _ := jointFixture(t)
	for _, raw := range []string{"", "   "} {
		if m, err := connections.ParseJointConfig(raw, f.manager, d); err != nil || m != nil {
			t.Fatal("empty registry must disable joint coordination", raw, err)
		}
	}
	if m, err := connections.ParseJointConfig("[]", f.manager, d); err != nil || m != nil {
		t.Fatal("empty array must disable joint coordination", err)
	}
	valid := `[{"id":"rhythm","client_id":"chuni","provider":"rhythm","services":["chunithm"]}]`
	if m, err := connections.ParseJointConfig(valid, f.manager, d); err != nil || m == nil {
		t.Fatal("valid registry rejected", err)
	}
	for name, raw := range map[string]string{
		"object":         `{"id":"rhythm"}`,
		"trailing":       valid + `[]`,
		"unknown field":  `[{"id":"rhythm","client_id":"chuni","provider":"rhythm","services":["chunithm"],"url":"https://x"}]`,
		"missing client": `[{"id":"rhythm","client_id":"nope","provider":"rhythm","services":["chunithm"]}]`,
		"secret echo":    `[{"id":"rhythm","client_id":"chuni","provider":"rhythm","services":["chunithm"],"client_secret":"super-secret"}]`,
	} {
		_, err := connections.ParseJointConfig(raw, f.manager, d)
		if err == nil {
			t.Fatal("invalid registry accepted:", name)
		}
		if strings.Contains(err.Error(), "super-secret") || strings.Contains(err.Error(), "chuni") {
			t.Fatal("error echoes configuration:", err)
		}
	}
	if _, err := connections.ParseJointConfig(valid, nil, d); err == nil {
		t.Fatal("registry without providers accepted")
	}
	if _, err := connections.ParseJointConfig(valid, f.manager, nil); err == nil {
		t.Fatal("registry without OAuth clients accepted")
	}
}
