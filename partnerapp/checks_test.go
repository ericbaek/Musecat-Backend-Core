package partnerapp_test

import (
	"context"
	"encoding/json"
	"github.com/ericbaek/musecat-backend-core/delegation"
	"golang.org/x/oauth2"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ericbaek/musecat-backend-core/connections"
	"github.com/ericbaek/musecat-backend-core/partnerapp"
)

type checkTransport func(*http.Request) (*http.Response, error)

func (f checkTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func checkJSON(status int, v any) *http.Response {
	data, _ := json.Marshal(v)
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(data)))}
}

func TestConformanceQueueIsolationSnapshotAndCleanup(t *testing.T) {
	f := newFixture(t)
	conns, err := connections.NewManager(nil, []byte(strings.Repeat("k", 32)), nil)
	if err != nil {
		t.Fatal(err)
	}
	s := partnerapp.NewService(f.manager, conns, nil, "")
	form := validForm("checked_app")
	form.DirectionB = fullB()
	application, err := s.Create(f.app, f.owner.Id, form)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.QueueChecks(f.app, f.operator.Id, application.ID, false, partnerapp.CheckFixtures{}); err != partnerapp.ErrNotFound {
		t.Fatal("foreign run", err)
	}
	if _, err = s.CheckReports(f.app, f.operator.Id, application.ID, false); err != partnerapp.ErrNotFound {
		t.Fatal("foreign read", err)
	}
	fixtures := partnerapp.CheckFixtures{AccessToken: "secret-access", ExpectedSubject: "staging-subject"}
	run, err := s.QueueChecks(f.app, f.owner.Id, application.ID, false, fixtures)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.QueueChecks(f.app, f.owner.Id, application.ID, false, fixtures); err != partnerapp.ErrChecksBusy {
		t.Fatal("duplicate run", err)
	}
	stored, _ := f.app.FindRecordById(partnerapp.CheckRuns, run.ID)
	if stored.GetString("fixtures") == "" || strings.Contains(stored.GetString("fixtures"), "secret-access") {
		t.Fatal("fixtures not sealed")
	}
	runner := partnerapp.NewCheckRunner(&http.Client{Transport: checkTransport(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") == "Bearer secret-access" {
			return checkJSON(200, map[string]string{"sub": "staging-subject"}), nil
		}
		return checkJSON(401, map[string]string{"error": "invalid_client"}), nil
	})})
	if err = s.ProcessChecks(context.Background(), f.app, 1, runner); err != nil {
		t.Fatal(err)
	}
	reports, err := s.CheckReports(f.app, f.owner.Id, application.ID, false)
	if err != nil || len(reports) != 1 || reports[0].Status != "failed" || !reports[0].Current {
		t.Fatal(reports, err)
	}
	data, _ := json.Marshal(reports)
	if strings.Contains(string(data), "secret-access") || strings.Contains(string(data), "staging-subject") {
		t.Fatal("report leaks fixtures")
	}
	stored, _ = f.app.FindRecordById(partnerapp.CheckRuns, run.ID)
	if stored.GetString("fixtures") != "" {
		t.Fatal("fixtures retained after completion")
	}
	form.Description.En = "Updated description"
	if _, err = s.Update(f.app, f.owner.Id, application.ID, form); err != nil {
		t.Fatal(err)
	}
	reports, err = s.CheckReports(f.app, f.owner.Id, application.ID, false)
	if err != nil || reports[0].Current {
		t.Fatal("stale result called current", err)
	}
}

func TestConformancePositiveRecordsRefreshRevocationAndMissingFixtures(t *testing.T) {
	form := validForm("checked_app")
	form.DirectionB = fullB()
	revoked := false
	runner := partnerapp.NewCheckRunner(&http.Client{Transport: checkTransport(func(r *http.Request) (*http.Response, error) {
		switch {
		case strings.Contains(r.URL.Path, ".well-known"):
			return checkJSON(404, map[string]string{}), nil
		case strings.Contains(r.URL.Path, "authorize"):
			return checkJSON(400, map[string]string{"error": "invalid_request"}), nil
		case strings.Contains(r.URL.Path, "token"):
			if _, _, ok := r.BasicAuth(); !ok {
				return checkJSON(401, map[string]string{"error": "invalid_client"}), nil
			}
			if revoked {
				return checkJSON(400, map[string]string{"error": "invalid_grant"}), nil
			}
			return checkJSON(200, map[string]any{"access_token": "new-access", "refresh_token": "new-refresh", "token_type": "Bearer", "expires_in": 3600}), nil
		case strings.Contains(r.URL.Path, "revoke"):
			if _, _, ok := r.BasicAuth(); !ok {
				return checkJSON(401, map[string]string{"error": "invalid_client"}), nil
			}
			revoked = true
			return checkJSON(200, map[string]string{}), nil
		case strings.Contains(r.URL.Path, "userinfo"):
			token := r.Header.Get("Authorization")
			if token == "" || (revoked && token == "Bearer new-access") {
				return checkJSON(401, map[string]string{}), nil
			}
			return checkJSON(200, map[string]string{"sub": "staging-subject"}), nil
		case strings.Contains(r.URL.Path, "records"):
			token := r.Header.Get("Authorization")
			if token == "" {
				return checkJSON(401, map[string]string{}), nil
			}
			if token == "Bearer private-token" {
				return checkJSON(403, map[string]string{"error": "records_private"}), nil
			}
			return checkJSON(200, map[string]any{"spec": connections.RecordSpec, "service": "chunithm", "sub": "staging-subject", "visibility": "public", "updated_at": time.Now().UTC().Format(time.RFC3339), "source": map[string]string{"name": "Partner", "url": "https://p.example/profile"}, "fields": []any{}, "sections": []any{}}), nil
		default:
			return checkJSON(200, map[string]string{}), nil
		}
	})})
	fixtures := partnerapp.CheckFixtures{AccessToken: "public-token", PrivateToken: "private-token", RefreshToken: "refresh", ExpectedSubject: "staging-subject", Destructive: true}
	cases := runner.Run(context.Background(), form, "https://musecat.example/callback", partnerapp.Credentials{ClientID: "musecat", ClientSecret: "secret"}, "", fixtures)
	for _, c := range cases {
		if c.Status != "pass" && c.Status != "not_applicable" {
			t.Fatal("positive case did not pass", c)
		}
	}
	if !revoked {
		t.Fatal("disposable token not revoked")
	}
	cases = runner.Run(context.Background(), form, "https://musecat.example/callback", partnerapp.Credentials{}, "", partnerapp.CheckFixtures{})
	missing := 0
	for _, c := range cases {
		if c.Status == "action_required" {
			missing++
		}
	}
	if missing < 3 {
		t.Fatal("missing fixtures falsely counted as passes", cases)
	}
}

func TestConformanceStaleQueueAndInterruptedRunAreTerminal(t *testing.T) {
	f := newFixture(t)
	application, err := f.service.Create(f.app, f.owner.Id, validForm("checked_app"))
	if err != nil {
		t.Fatal(err)
	}
	run, err := f.service.QueueChecks(f.app, f.owner.Id, application.ID, false, partnerapp.CheckFixtures{})
	if err != nil {
		t.Fatal(err)
	}
	row, _ := f.app.FindRecordById(partnerapp.CheckRuns, run.ID)
	row.Set("status", "running")
	row.Set("started", time.Now().Add(-4*time.Minute))
	row.Set("fixtures", "unreadable-secret")
	if err = f.app.Save(row); err != nil {
		t.Fatal(err)
	}
	if err = f.service.ProcessChecks(context.Background(), f.app, 1, nil); err != nil {
		t.Fatal(err)
	}
	row, _ = f.app.FindRecordById(partnerapp.CheckRuns, run.ID)
	if row.GetString("status") != "failed" || row.GetString("fixtures") != "" {
		t.Fatal("interrupted fixture replay possible")
	}
	if err = partnerapp.EnsureCheckSchema(f.app); err != nil {
		t.Fatal(err)
	}
	c, _ := f.app.FindCollectionByNameOrId(partnerapp.CheckRuns)
	allow := ""
	c.ListRule = &allow
	if err = f.app.Save(c); err != nil {
		t.Fatal(err)
	}
	if err = partnerapp.EnsureCheckSchema(f.app); err == nil {
		t.Fatal("public check reports accepted")
	}
}

func TestConformanceJointNegativeCasesAndIntentValidation(t *testing.T) {
	form := validForm("checked_app")
	form.DirectionB = fullB()
	form.Joint = &partnerapp.Joint{Enabled: true, Manifests: []string{connections.JointManifest}, JointURL: "https://p.example/joint", ConnectStartURL: "https://p.example/start", KeyRotationWindowHours: 24}
	runner := partnerapp.NewCheckRunner(&http.Client{Transport: checkTransport(func(r *http.Request) (*http.Response, error) {
		if !strings.Contains(r.URL.Path, "/joint/") {
			return checkJSON(404, map[string]string{}), nil
		}
		if r.Header.Get("Authorization") != "Bearer server-key" {
			return checkJSON(401, map[string]string{"error": "unauthorized_partner"}), nil
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/lookup"):
			switch body["intent"] {
			case "expired":
				return checkJSON(410, map[string]string{"error": "intent_expired"}), nil
			case "consumed":
				return checkJSON(409, map[string]string{"error": "intent_consumed"}), nil
			case "fresh":
				return checkJSON(200, map[string]any{"correlation_id": "a5b0a471-1cdd-46a3-a4a0-86e549e46496", "sub": "subject", "name": "PLAYER", "services": []string{"chunithm"}, "manifest": connections.JointManifest, "redirect_uri": form.DirectionA.RedirectURIs[0], "code_challenge": strings.Repeat("A", 43), "expires_at": time.Now().Add(4 * time.Minute).UTC().Format(time.RFC3339)}), nil
			default:
				return checkJSON(400, map[string]string{"error": "invalid_intent"}), nil
			}
		case strings.HasSuffix(r.URL.Path, "/receipts"):
			if body["manifest"] == "musecat-joint/unsupported" {
				return checkJSON(400, map[string]string{"error": "manifest_unsupported"}), nil
			}
			return checkJSON(400, map[string]string{"error": "invalid_intent"}), nil
		case strings.HasSuffix(r.URL.Path, "/revocations"):
			return checkJSON(200, map[string]string{"status": "unknown_generation"}), nil
		}
		return checkJSON(404, map[string]string{}), nil
	})})
	cases := runner.Run(context.Background(), form, "https://musecat.example/callback", partnerapp.Credentials{}, "server-key", partnerapp.CheckFixtures{FreshIntent: "fresh", ExpiredIntent: "expired", ConsumedIntent: "consumed", ExpectedSubject: "subject"})
	checked := 0
	for _, c := range cases {
		if strings.HasPrefix(c.ID, "joint.") && c.ID != "joint.start" {
			checked++
			if c.Status != "pass" {
				t.Fatal("joint case failed", c)
			}
		}
	}
	if checked < 9 {
		t.Fatal("joint coverage missing", checked)
	}
}

func TestConformanceCancelsChangedQueueAndWipesUnreadableFixtures(t *testing.T) {
	f := newFixture(t)
	old, err := connections.NewManager(nil, []byte(strings.Repeat("a", 32)), nil)
	if err != nil {
		t.Fatal(err)
	}
	s := partnerapp.NewService(f.manager, old, nil, "")
	form := validForm("stale_app")
	application, err := s.Create(f.app, f.owner.Id, form)
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.QueueChecks(f.app, f.owner.Id, application.ID, false, partnerapp.CheckFixtures{AccessToken: "private-token"})
	if err != nil {
		t.Fatal(err)
	}
	form.Description.En = "changed"
	if _, err = s.Update(f.app, f.owner.Id, application.ID, form); err != nil {
		t.Fatal(err)
	}
	if err = s.ProcessChecks(context.Background(), f.app, 1, nil); err != nil {
		t.Fatal(err)
	}
	row, _ := f.app.FindRecordById(partnerapp.CheckRuns, run.ID)
	if row.GetString("status") != "cancelled" || row.GetString("fixtures") != "" {
		t.Fatal("changed queue executed or retained fixtures")
	}
	application, err = s.Create(f.app, f.owner.Id, validForm("rotated_app"))
	if err != nil {
		t.Fatal(err)
	}
	run, err = s.QueueChecks(f.app, f.owner.Id, application.ID, false, partnerapp.CheckFixtures{AccessToken: "private-token"})
	if err != nil {
		t.Fatal(err)
	}
	next, err := connections.NewManager(nil, []byte(strings.Repeat("b", 32)), nil)
	if err != nil {
		t.Fatal(err)
	}
	changed := partnerapp.NewService(f.manager, next, nil, "")
	if err = changed.ProcessChecks(context.Background(), f.app, 1, nil); err != nil {
		t.Fatal(err)
	}
	row, _ = f.app.FindRecordById(partnerapp.CheckRuns, run.ID)
	if row.GetString("status") != "failed" || row.GetString("fixtures") != "" {
		t.Fatal("unreadable fixture stuck in queue")
	}
}

func TestConformanceDirectionARequiresARealRedeemedGrant(t *testing.T) {
	f := newFixture(t)
	form := validForm("oauth_checked")
	application, err := f.service.Create(f.app, f.owner.Id, form)
	if err != nil {
		t.Fatal(err)
	}
	runner := partnerapp.NewCheckRunner(&http.Client{Transport: checkTransport(func(*http.Request) (*http.Response, error) { return checkJSON(200, map[string]string{}), nil })})
	if _, err = f.service.QueueChecks(f.app, f.owner.Id, application.ID, false, partnerapp.CheckFixtures{}); err != nil {
		t.Fatal(err)
	}
	if err = f.service.ProcessChecks(context.Background(), f.app, 1, runner); err != nil {
		t.Fatal(err)
	}
	reports, err := f.service.CheckReports(f.app, f.owner.Id, application.ID, false)
	if err != nil || reports[0].Status != "incomplete" {
		t.Fatal("TLS alone claimed full conformance", reports, err)
	}
	form = validForm("oauth_verified")
	application, err = f.service.Create(f.app, f.owner.Id, form)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.Submit(f.app, f.owner.Id, application.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.Approve(f.app, f.operator.Id, application.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err = f.service.GenerateSecret(f.app, f.owner.Id, application.ID); err != nil {
		t.Fatal(err)
	}
	token, err := f.owner.NewAuthToken()
	if err != nil {
		t.Fatal(err)
	}
	actor := delegation.Actor{UserID: f.owner.Id, Session: token}
	verifier := oauth2.GenerateVerifier()
	target, err := f.manager.Begin(f.app, delegation.AuthorizationInput{ClientID: form.PartnerID, RedirectURI: form.DirectionA.RedirectURIs[0], ResponseType: "code", Scope: []string{delegation.AccountAccess}, State: "conformance-state", Challenge: oauth2.S256ChallengeFromVerifier(verifier), ChallengeMethod: "S256"})
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(target)
	handle := u.Query().Get("request")
	if _, err = f.manager.ReadConsent(f.app, actor, handle); err != nil {
		t.Fatal(err)
	}
	target, err = f.manager.Decide(f.app, actor, delegation.Decision{Request: handle, Approve: true, Scopes: []string{delegation.AccountAccess}})
	if err != nil {
		t.Fatal(err)
	}
	u, _ = url.Parse(target)
	if _, err = f.manager.Exchange(f.app, form.PartnerID, delegation.TokenInput{GrantType: "authorization_code", Code: u.Query().Get("code"), RedirectURI: form.DirectionA.RedirectURIs[0], Verifier: verifier}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.QueueChecks(f.app, f.owner.Id, application.ID, false, partnerapp.CheckFixtures{}); err != nil {
		t.Fatal(err)
	}
	if err = f.service.ProcessChecks(context.Background(), f.app, 1, runner); err != nil {
		t.Fatal(err)
	}
	reports, err = f.service.CheckReports(f.app, f.owner.Id, application.ID, false)
	if err != nil || reports[0].Status != "passed" {
		t.Fatal("redeemed grant not recognized", reports, err)
	}
}
