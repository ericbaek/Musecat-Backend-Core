package connections_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ericbaek/musecat-backend-core/connections"
	"github.com/ericbaek/musecat-backend-core/delegation"
	jointhandler "github.com/ericbaek/musecat-backend-core/handlers/joint"
	"github.com/pocketbase/pocketbase/core"
	"golang.org/x/oauth2"
)

func flowFixture(t *testing.T) (*fixture, *connections.JointManager, *delegation.Manager, connections.Actor, string, *int) {
	f, _, d, a := jointFixture(t)
	sum := sha256.Sum256([]byte(strings.Repeat("i", 43)))
	m, err := connections.NewJointManager(f.manager, d, []connections.JointPartner{{ID: "rhythm", ClientID: "chuni", Provider: "rhythm", Services: []string{"chunithm"}, JointURL: "https://rhythm.example/joint", ConnectStartURL: "https://rhythm.example/start", Manifests: []string{connections.JointManifest}, OutboundKey: strings.Repeat("o", 43), InboundKeyHash: hex.EncodeToString(sum[:])}})
	if err != nil {
		t.Fatal(err)
	}
	f.app.OnServe().BindFunc(func(se *core.ServeEvent) error { jointhandler.RegisterRoutes(se, m); return se.Next() })
	verifier := oauth2.GenerateVerifier()
	correlation := "dcbf6421-b888-46f5-8718-cba5ed9c1176"
	receipts := 0
	f.joint = func(r *http.Request) *http.Response {
		if r.URL.Path == "/joint/intents/lookup" || r.URL.Path == "/joint/receipts" || r.URL.Path == "/joint/revocations" {
			if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("o", 43) || r.Header.Get("X-Request-Id") == "" {
				t.Error("missing partner authentication or request ID")
			}
		}
		switch r.URL.Path {
		case "/joint/intents/lookup":
			return jsonResponse(map[string]any{"correlation_id": correlation, "sub": "verified-subject", "name": "PLAYER", "services": []string{"chunithm"}, "manifest": connections.JointManifest, "code_challenge": oauth2.S256ChallengeFromVerifier(verifier), "redirect_uri": "https://rhythm.example/callback", "expires_at": time.Now().Add(4 * time.Minute).UTC().Format(time.RFC3339)})
		case "/joint/receipts":
			receipts++
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			f.challenge = body["code_challenge"]
			return jsonResponse(map[string]string{"partner_code": "partner-code", "partner_code_expires_at": time.Now().Add(80 * time.Second).UTC().Format(time.RFC3339), "scope": "profile chuni:read"})
		case "/joint/revocations":
			return jsonResponse(map[string]string{"status": "revoked"})
		}
		return nil
	}
	return f, m, d, a, verifier, &receipts
}

func TestJointFlowApprovalReplayAndBilateralRevocation(t *testing.T) {
	f, m, d, a, verifier, receipts := flowFixture(t)
	lookup, err := m.Lookup(context.Background(), f.app, a, "rhythm", strings.Repeat("h", 43))
	if err != nil {
		t.Fatal(err)
	}
	again, err := m.Lookup(context.Background(), f.app, a, "rhythm", strings.Repeat("h", 43))
	if err != nil || again.ID != lookup.ID {
		t.Fatalf("lookup not idempotent: %+v %v", again, err)
	}
	target, err := m.Approve(context.Background(), f.app, a, lookup.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Approve(context.Background(), f.app, a, lookup.ID); err == nil || *receipts != 1 {
		t.Fatal("approval replayed")
	}
	before, err := m.Status(f.app, a, lookup.ID)
	if err != nil || before.Status != "partial" {
		t.Fatalf("unredeemed code: %+v %v", before, err)
	}
	u, _ := url.Parse(target)
	if _, err = d.Exchange(f.app, "chuni", delegation.TokenInput{GrantType: "authorization_code", Code: u.Query().Get("code"), RedirectURI: "https://rhythm.example/callback", Verifier: verifier}); err != nil {
		t.Fatal(err)
	}
	status, err := m.Status(f.app, a, lookup.ID)
	if err != nil || status.Status != "connected" {
		t.Fatalf("%+v %v", status, err)
	}
	if err = m.DisconnectPair(f.app, a, lookup.ID); err != nil {
		t.Fatal(err)
	}
	status, err = m.Status(f.app, a, lookup.ID)
	if err != nil || status.Status != "revoked" {
		t.Fatalf("%+v %v", status, err)
	}
	if err = m.DeliverRevocations(context.Background(), f.app, 20); err != nil {
		t.Fatal(err)
	}
	rows, err := f.app.FindRecordsByFilter(connections.JointOutbox, "delivered != ''", "", 10, 0)
	if err != nil || len(rows) != 1 {
		t.Fatalf("outbox not delivered: %v %v", rows, err)
	}
}

func TestJointFlowSubjectMismatchDoesNotStoreWrongAccount(t *testing.T) {
	f, m, _, a, _, _ := flowFixture(t)
	lookup, err := m.Lookup(context.Background(), f.app, a, "rhythm", strings.Repeat("h", 43))
	if err != nil {
		t.Fatal(err)
	}
	f.subject = "different-account"
	if _, err = m.Approve(context.Background(), f.app, a, lookup.ID); err == nil {
		t.Fatal("wrong subject accepted")
	}
	rows, err := f.app.FindRecordsByFilter(connections.Accounts, "", "", 10, 0)
	if err != nil || len(rows) != 0 {
		t.Fatal("wrong account persisted", err)
	}
	grants, err := f.app.FindRecordsByFilter(delegation.Grants, "revoked=false", "", 10, 0)
	if err != nil || len(grants) != 0 {
		t.Fatal("grant survived failed exchange", err)
	}
}

func TestJointOldGenerationCannotRevokeIndependentReauthorization(t *testing.T) {
	f, m, d, a, _, _ := flowFixture(t)
	lookup, err := m.Lookup(context.Background(), f.app, a, "rhythm", strings.Repeat("h", 43))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Approve(context.Background(), f.app, a, lookup.ID); err != nil {
		t.Fatal(err)
	}
	row, _ := f.app.FindRecordById(connections.JointAttempts, lookup.ID)
	grant, generation := jointGrant(t, f, d, true)
	result, err := m.ReceiveRevocation(f.app, "rhythm", row.GetString("correlation"), row.GetString("generation"))
	if err != nil || result != "unknown_generation" {
		t.Fatal(result, err)
	}
	if err = m.DisconnectPair(f.app, a, lookup.ID); err != nil {
		t.Fatal(err)
	}
	if err = d.VerifyAccountGrant(f.app, a.UserID, "chuni", grant, generation); err != nil {
		t.Fatal("new independent grant revoked", err)
	}
}

func TestJointPartnerRequestReplaysOriginalResultAndRejectsChangedBody(t *testing.T) {
	f, m, _, _, _, _ := flowFixture(t)
	calls := 0
	id := "36bc2d92-98ae-4275-a1aa-312b00b44e91"
	run := func(core.App) (any, error) { calls++; return map[string]string{"status": "revoked"}, nil }
	for range 2 {
		if _, err := m.PartnerRequest(f.app, "rhythm", id, "body", run); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatal("idempotent retry executed twice")
	}
	if _, err := m.PartnerRequest(f.app, "rhythm", id, "different body", run); err == nil {
		t.Fatal("changed request reused ID")
	}
	if _, _, err := m.BeginURL("rhythm", "https://evil.example/ko/connect/rhythm"); err == nil {
		t.Fatal("unregistered return URL accepted")
	}
	f.request("", "GET", "/partner/v1/joint/36bc2d92-98ae-4275-a1aa-312b00b44e91", nil, 401)
}

func TestWithdrawalPreservesRevocationAndRetryIdentity(t *testing.T) {
	f, m, _, a, _, _ := flowFixture(t)
	lookup, err := m.Lookup(t.Context(), f.app, a, "rhythm", strings.Repeat("h", 43))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Approve(t.Context(), f.app, a, lookup.ID); err != nil {
		t.Fatal(err)
	}
	f.request(f.token, "POST", "/user/withdraw", map[string]string{"password": "secret123"}, 200)
	attempts, err := f.app.FindRecordsByFilter(connections.JointAttempts, "", "", 0, 0)
	if err != nil || len(attempts) != 0 {
		t.Fatal("personal attempt retained", err)
	}
	outbox, err := f.app.FindRecordsByFilter(connections.JointOutbox, "", "", 0, 0)
	if err != nil || len(outbox) != 1 {
		t.Fatal("withdrawal lost remote revocation", err)
	}
	requestID := ""
	deliveries := 0
	f.joint = func(r *http.Request) *http.Response {
		deliveries++
		if requestID != "" && requestID != r.Header.Get("X-Request-Id") {
			t.Error("retry changed request ID")
		}
		requestID = r.Header.Get("X-Request-Id")
		if deliveries == 1 {
			return status(503, map[string]string{"error": "temporarily_unavailable"})
		}
		return status(200, map[string]string{"status": "revoked"})
	}
	if err := m.DeliverRevocations(t.Context(), f.app, 20); err != nil {
		t.Fatal(err)
	}
	row, _ := f.app.FindRecordById(connections.JointOutbox, outbox[0].Id)
	if row.GetString("delivered") != "" || row.GetFloat("attempts") != 1 {
		t.Fatal("failed delivery marked complete")
	}
	row.Set("next_at", time.Now().Add(-time.Minute))
	if err := f.app.Save(row); err != nil {
		t.Fatal(err)
	}
	if err := m.DeliverRevocations(t.Context(), f.app, 20); err != nil {
		t.Fatal(err)
	}
	row, _ = f.app.FindRecordById(connections.JointOutbox, row.Id)
	if row.GetString("delivered") == "" || deliveries != 2 || requestID == "" {
		t.Fatal("revocation not delivered", deliveries)
	}
}

func TestMusecatEntryHasFixedCompletionDestination(t *testing.T) {
	_, m, _, _, _, _ := flowFixture(t)
	target, _, err := m.BeginURL("rhythm", "https://musecat.example/kr/connect/rhythm")
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	if u.Query().Get("completion_uri") != "https://musecat.example/kr/settings/connections" {
		t.Fatal("unexpected completion destination", u.Query().Get("completion_uri"))
	}
	for _, bad := range []string{"https://evil.example/kr/connect/rhythm", "https://musecat.example/kr/connect/rhythm?completion_uri=https://evil.example"} {
		if _, _, err := m.BeginURL("rhythm", bad); err == nil {
			t.Fatal("accepted untrusted destination")
		}
	}
}
