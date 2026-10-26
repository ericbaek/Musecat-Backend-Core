package partnerapp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/ericbaek/musecat-backend-core/connections"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

func jsonObject(data []byte) map[string]any {
	var v map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	if decoder.Decode(&v) != nil || decoder.Decode(new(any)) != io.EOF {
		return nil
	}
	return v
}
func stringValue(v map[string]any, key string) string { s, _ := v[key].(string); return s }

// Run probes only registered endpoints. It never creates OAuth consent or exchanges
// a real user's authorization code. Positive checks require explicit staging fixtures.
func (r *CheckRunner) Run(ctx context.Context, form Form, callback string, creds Credentials, key string, fixtures CheckFixtures) []CheckCase {
	cases := []CheckCase{}
	add := func(id, status, code string) { cases = append(cases, CheckCase{ID: id, Status: status, Code: code}) }
	expect := func(id, method, target, bearer string, values url.Values, body any, status int, code string, basic *Credentials) bool {
		actual, data, err := r.call(ctx, method, target, bearer, values, body, uuid.NewString(), basic)
		if err != nil {
			add(id, "fail", err.Error())
			return false
		}
		if actual != status || (code != "" && stringValue(jsonObject(data), "error") != code) {
			add(id, "fail", "unexpected_protocol_response")
			return false
		}
		add(id, "pass", "expected_response")
		return true
	}
	// Callback URLs may legitimately reject a request without a code, or redirect to login.
	for i, target := range form.DirectionA.RedirectURIs {
		status, _, err := r.call(ctx, http.MethodGet, target, "", nil, nil, "", nil)
		id := "callback.transport." + string(rune('1'+i))
		if err != nil {
			add(id, "fail", err.Error())
		} else if status >= 200 && status < 500 {
			add(id, "pass", "tls_reachable")
		} else {
			add(id, "fail", "endpoint_unavailable")
		}
	}
	b := form.DirectionB
	if b == nil {
		add("direction_b", "not_applicable", "not_declared")
		return cases
	}
	provider := connections.Provider{Issuer: b.Issuer, AuthorizationURL: b.AuthorizationURL, TokenURL: b.TokenURL, UserInfoURL: b.UserInfoURL, RecordsURL: b.RecordsURL}
	identityField := "sub"
	if provider.IsChuniInfo() {
		identityField = "id"
	}
	metadataStatus, metadataBody, metadataErr := r.call(ctx, http.MethodGet, strings.TrimRight(b.Issuer, "/")+"/.well-known/oauth-authorization-server", "", nil, nil, "", nil)
	if metadataErr != nil {
		add("oauth.metadata", "fail", metadataErr.Error())
	} else if metadataStatus == 404 {
		add("oauth.metadata", "not_applicable", "metadata_not_published")
	} else {
		v := jsonObject(metadataBody)
		if metadataStatus != 200 || stringValue(v, "issuer") != b.Issuer || stringValue(v, "authorization_endpoint") != b.AuthorizationURL || stringValue(v, "token_endpoint") != b.TokenURL || stringValue(v, "revocation_endpoint") != b.RevocationURL {
			add("oauth.metadata", "fail", "metadata_mismatch")
		} else {
			add("oauth.metadata", "pass", "metadata_matches")
		}
	}
	expect("oauth.token_auth", http.MethodPost, b.TokenURL, "", url.Values{"grant_type": {"authorization_code"}, "code": {"musecat-conformance-invalid-code"}, "redirect_uri": {callback}}, nil, 401, "invalid_client", nil)
	expect("oauth.revoke_auth", http.MethodPost, b.RevocationURL, "", url.Values{"token": {"musecat-conformance-invalid-token"}}, nil, 401, "", nil)
	expect("oauth.userinfo_auth", http.MethodGet, b.UserInfoURL, "", nil, nil, 401, "", nil)
	for _, service := range b.Services {
		expect("records.auth."+service.ID, http.MethodGet, provider.RecordEndpoint(service.ID), "", nil, nil, 401, "", nil)
	}
	if creds.ClientID == "" {
		add("oauth.pkce_plain", "action_required", "oauth_credentials_required")
	} else {
		target, _ := url.Parse(b.AuthorizationURL)
		q := target.Query()
		q.Set("client_id", creds.ClientID)
		q.Set("redirect_uri", callback)
		q.Set("response_type", "code")
		q.Set("scope", b.IdentityScope)
		q.Set("state", uuid.NewString())
		q.Set("code_challenge", oauth2.GenerateVerifier())
		q.Set("code_challenge_method", "plain")
		target.RawQuery = q.Encode()
		// The URL is generated here from the registered endpoint, never a browser URL.
		status, data, err := r.callQuery(ctx, target.String(), b.Issuer)
		code := stringValue(jsonObject(data), "error")
		if err != nil {
			add("oauth.pkce_plain", "fail", err.Error())
		} else if (status == 400 || (status >= 300 && status < 400 && stringValue(jsonObject(data), "verified_callback") == "true")) && slices.Contains([]string{"invalid_request", "unsupported_code_challenge_method"}, code) {
			add("oauth.pkce_plain", "pass", "plain_rejected")
		} else if status == 200 || (status >= 300 && status < 400) {
			add("oauth.pkce_plain", "action_required", "interactive_pkce_test_required")
		} else {
			add("oauth.pkce_plain", "fail", "unexpected_protocol_response")
		}
	}
	if fixtures.AccessToken == "" || fixtures.ExpectedSubject == "" {
		add("oauth.userinfo", "action_required", "access_token_and_subject_required")
		for _, service := range b.Services {
			add("records.envelope."+service.ID, "action_required", "access_token_and_subject_required")
		}
	} else {
		status, data, err := r.call(ctx, http.MethodGet, b.UserInfoURL, fixtures.AccessToken, nil, nil, "", nil)
		v := jsonObject(data)
		sub := stringValue(v, identityField)
		if err != nil {
			add("oauth.userinfo", "fail", err.Error())
		} else if status != 200 || sub != fixtures.ExpectedSubject || (stringValue(v, "iss") != "" && stringValue(v, "iss") != b.Issuer) {
			add("oauth.userinfo", "fail", "identity_mismatch")
		} else {
			add("oauth.userinfo", "pass", "verified_subject")
		}
		for _, service := range b.Services {
			status, data, err := r.call(ctx, http.MethodGet, provider.RecordEndpoint(service.ID), fixtures.AccessToken, nil, nil, "", nil)
			if err != nil {
				add("records.envelope."+service.ID, "fail", err.Error())
				continue
			}
			envelope, e := connections.ParseProviderRecords(provider, data, service.ID, fixtures.ExpectedSubject)
			if status != 200 || e != nil || envelope.Visibility != "public" {
				add("records.envelope."+service.ID, "fail", "invalid_public_records_envelope")
			} else {
				add("records.envelope."+service.ID, "pass", "valid_envelope")
			}
		}
	}
	for _, service := range b.Services {
		id := "records.private." + service.ID
		if provider.IsChuniInfo() {
			add(id, "not_applicable", "upstream_scope_visibility")
			continue
		}
		privateSubject := fixtures.PrivateSubject
		if privateSubject == "" {
			privateSubject = fixtures.ExpectedSubject
		}
		if fixtures.PrivateToken == "" || privateSubject == "" {
			add(id, "action_required", "private_records_token_required")
			continue
		}
		status, data, err := r.call(ctx, http.MethodGet, provider.RecordEndpoint(service.ID), fixtures.PrivateToken, nil, nil, "", nil)
		if err != nil {
			add(id, "fail", err.Error())
			continue
		}
		hidden := status == 403 && stringValue(jsonObject(data), "error") == "records_private"
		if status == 200 {
			envelope, e := connections.ParseProviderRecords(provider, data, service.ID, privateSubject)
			hidden = e == nil && envelope.Visibility == "private"
		}
		if hidden {
			add(id, "pass", "private_records_hidden")
		} else {
			add(id, "fail", "private_records_not_hidden")
		}
	}
	if fixtures.RefreshToken == "" || !fixtures.Destructive || creds.ClientID == "" || fixtures.ExpectedSubject == "" {
		add("oauth.refresh_revoke", "action_required", "disposable_refresh_token_and_consent_required")
	} else {
		status, data, err := r.call(ctx, http.MethodPost, b.TokenURL, "", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {fixtures.RefreshToken}}, nil, "", &creds)
		v := jsonObject(data)
		access := stringValue(v, "access_token")
		refresh := stringValue(v, "refresh_token")
		if refresh == "" {
			refresh = fixtures.RefreshToken
		}
		expires, _ := v["expires_in"].(float64)
		if err != nil {
			add("oauth.refresh_revoke", "fail", err.Error())
		} else if status != 200 || access == "" || !strings.EqualFold(stringValue(v, "token_type"), "Bearer") || expires <= 0 {
			add("oauth.refresh_revoke", "fail", "invalid_refresh_response")
		} else {
			identityStatus, identity, identityErr := r.call(ctx, http.MethodGet, b.UserInfoURL, access, nil, nil, "", nil)
			identityOK := identityErr == nil && identityStatus == 200 && stringValue(jsonObject(identity), identityField) == fixtures.ExpectedSubject
			// Always attempt to clean up a successfully refreshed disposable family.
			revokeStatus, _, revokeErr := r.call(ctx, http.MethodPost, b.RevocationURL, "", url.Values{"token": {refresh}}, nil, "", &creds)
			deniedStatus, _, deniedErr := r.call(ctx, http.MethodGet, b.UserInfoURL, access, nil, nil, "", nil)
			refreshStatus, refreshBody, refreshErr := r.call(ctx, http.MethodPost, b.TokenURL, "", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}}, nil, "", &creds)
			if identityOK && revokeErr == nil && revokeStatus == 200 && deniedErr == nil && deniedStatus == 401 && refreshErr == nil && refreshStatus == 400 && stringValue(jsonObject(refreshBody), "error") == "invalid_grant" {
				add("oauth.refresh_revoke", "pass", "refresh_and_revocation_verified")
			} else {
				add("oauth.refresh_revoke", "fail", "refresh_or_revocation_failed")
			}
		}
	}
	j := form.Joint
	if j == nil || !j.Enabled {
		add("joint", "not_applicable", "not_declared")
		return cases
	}
	// A successful connection does not prove these destructive, account-bound
	// failure scenarios. Keep the live staging obligation visible, never infer a pass.
	add("joint.failure_scenarios", "action_required", "staging_failure_scenarios_required")
	base := strings.TrimRight(j.JointURL, "/")
	invalidIntent := oauth2.GenerateVerifier()
	invalidBody := map[string]any{"intent": invalidIntent, "correlation_id": uuid.NewString(), "manifest": connections.JointManifest, "generation": uuid.NewString(), "musecat_code": "musecat-conformance-invalid-code", "musecat_code_expires_at": time.Now().Add(time.Minute).UTC().Format(time.RFC3339), "code_challenge": oauth2.S256ChallengeFromVerifier(oauth2.GenerateVerifier()), "redirect_uri": callback}
	for _, path := range []string{"intents/lookup", "receipts", "revocations"} {
		expect("joint.auth."+strings.ReplaceAll(path, "/", "_"), http.MethodPost, base+"/"+path, "", nil, invalidBody, 401, "unauthorized_partner", nil)
	}
	if key == "" {
		add("joint.key", "action_required", "partner_server_key_required")
		return cases
	}
	expect("joint.key", http.MethodPost, base+"/intents/lookup", key, nil, map[string]string{"intent": invalidIntent, "partner_id": form.PartnerID}, 400, "invalid_intent", nil)
	expect("joint.invalid_receipt", http.MethodPost, base+"/receipts", key, nil, invalidBody, 400, "invalid_intent", nil)
	for _, fixture := range []struct {
		id, value, code string
		status          int
	}{{"joint.expired_intent", fixtures.ExpiredIntent, "intent_expired", 410}, {"joint.consumed_intent", fixtures.ConsumedIntent, "intent_consumed", 409}} {
		if fixture.value == "" {
			add(fixture.id, "action_required", "intent_fixture_required")
		} else {
			expect(fixture.id, http.MethodPost, base+"/intents/lookup", key, nil, map[string]string{"intent": fixture.value, "partner_id": form.PartnerID}, fixture.status, fixture.code, nil)
		}
	}
	if fixtures.FreshIntent == "" || fixtures.ExpectedSubject == "" {
		add("joint.lookup", "action_required", "fresh_intent_required")
		add("joint.manifest", "action_required", "fresh_intent_required")
	} else {
		status, data, err := r.call(ctx, http.MethodPost, base+"/intents/lookup", key, nil, map[string]string{"intent": fixtures.FreshIntent, "partner_id": form.PartnerID}, uuid.NewString(), nil)
		v := jsonObject(data)
		expires, e := time.Parse(time.RFC3339, stringValue(v, "expires_at"))
		correlation, ue := uuid.Parse(stringValue(v, "correlation_id"))
		valid := status == 200 && err == nil && e == nil && expires.After(time.Now()) && expires.Before(time.Now().Add(5*time.Minute+time.Second)) && ue == nil && correlation.Version() == 4 && stringValue(v, "sub") == fixtures.ExpectedSubject && fixtures.ExpectedSubject != "" && stringValue(v, "manifest") == connections.JointManifest && slices.Contains(form.DirectionA.RedirectURIs, stringValue(v, "redirect_uri")) && regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`).MatchString(stringValue(v, "code_challenge"))
		services, ok := v["services"].([]any)
		valid = valid && ok && len(services) > 0
		seen := map[string]bool{}
		for _, value := range services {
			id, ok := value.(string)
			if !ok || seen[id] || !slices.ContainsFunc(b.Services, func(s DirectionBService) bool { return s.ID == id }) {
				valid = false
			}
			seen[id] = true
		}
		if valid {
			add("joint.lookup", "pass", "valid_session_bound_intent")
		} else {
			add("joint.lookup", "fail", "invalid_intent_response")
		}
		// Unsupported manifest must reject before issuing codes or consuming the intent.
		invalidBody["intent"] = fixtures.FreshIntent
		invalidBody["manifest"] = "musecat-joint/unsupported"
		expect("joint.manifest", http.MethodPost, base+"/receipts", key, nil, invalidBody, 400, "manifest_unsupported", nil)
	}
	requestID := uuid.NewString()
	revocation := map[string]string{"pair_id": uuid.NewString(), "generation": uuid.NewString(), "reason": "superseded", "revoked_at": time.Now().UTC().Format(time.RFC3339)}
	firstStatus, first, firstErr := r.call(ctx, http.MethodPost, base+"/revocations", key, nil, revocation, requestID, nil)
	secondStatus, second, secondErr := r.call(ctx, http.MethodPost, base+"/revocations", key, nil, revocation, requestID, nil)
	if firstErr == nil && firstStatus == 200 && stringValue(jsonObject(first), "status") == "unknown_generation" {
		add("joint.unknown_generation", "pass", "unknown_generation_rejected")
	} else {
		add("joint.unknown_generation", "fail", "unexpected_protocol_response")
	}
	if firstErr == nil && secondErr == nil && firstStatus == 200 && secondStatus == 200 && stringValue(jsonObject(first), "status") == stringValue(jsonObject(second), "status") {
		add("joint.request_replay", "pass", "idempotent_response")
	} else {
		add("joint.request_replay", "fail", "replay_not_idempotent")
	}
	status, _, err := r.call(ctx, http.MethodGet, j.ConnectStartURL, "", nil, nil, "", nil)
	if err != nil {
		add("joint.start", "fail", err.Error())
	} else if status >= 200 && status < 400 {
		add("joint.start", "pass", "tls_reachable")
	} else {
		add("joint.start", "fail", "endpoint_unavailable")
	}
	return cases
}

// Only this internally generated authorization probe may carry a query string.
func (r *CheckRunner) callQuery(ctx context.Context, target string, issuer string) (int, []byte, error) {
	u, err := url.Parse(target)
	if err != nil {
		return 0, nil, errCheckNetwork
	}
	query := u.RawQuery
	u.RawQuery = ""
	if !httpsURL(u.String()) {
		return 0, nil, errCheckNetwork
	}
	// call's normal URL guard intentionally rejects browser-supplied query strings.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String()+"?"+query, nil)
	if err != nil {
		return 0, nil, err
	}
	response, err := r.client.Do(req)
	if err != nil {
		return 0, nil, errCheckNetwork
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		callback, e := url.Parse(response.Header.Get("Location"))
		if e == nil {
			values := callback.Query()
			callback.RawQuery = ""
			callback.Fragment = ""
			requested, _ := url.Parse(target)
			if callback.String() == requested.Query().Get("redirect_uri") && len(values["state"]) == 1 && values.Get("state") == requested.Query().Get("state") && len(values["error"]) == 1 && !values.Has("code") && (values.Get("iss") == "" || values.Get("iss") == issuer) {
				data, _ := json.Marshal(map[string]string{"error": values.Get("error"), "verified_callback": "true"})
				return response.StatusCode, data, nil
			}
		}
		return response.StatusCode, nil, nil
	}
	var v map[string]any
	decoder := json.NewDecoder(http.MaxBytesReader(nil, response.Body, 64<<10))
	if decoder.Decode(&v) != nil {
		return response.StatusCode, nil, nil
	}
	data, _ := json.Marshal(v)
	return response.StatusCode, data, nil
}
