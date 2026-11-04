package partnerapp

import (
	"net"
	"net/mail"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

// FormSpec is the onboarding document version partners submit.
const FormSpec = "musecat-partner-onboarding/v1"

// JointManifest is the only consent manifest version Musecat currently defines.
const JointManifest = "musecat-joint/v1"

var identifier = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,127}$`)

// ServiceRegistry lists the canonical game service IDs partners may declare.
// Musecat owns this list; partners request additions rather than inventing IDs.
var ServiceRegistry = []string{
	"chunithm", "maimai", "ongeki", "sdvx", "iidx", "taiko", "wacca", "arcaea",
	"polaris_chord", "ddr", "jubeat", "popn", "nostalgia", "gitadora", "museca",
}

var serviceNames = map[string]string{
	"chunithm": "CHUNITHM", "maimai": "maimai でらっくす", "ongeki": "オンゲキ", "sdvx": "SOUND VOLTEX",
	"iidx": "beatmania IIDX", "taiko": "太鼓の達人", "wacca": "WACCA", "arcaea": "Arcaea",
	"polaris_chord": "ポラリスコード", "ddr": "DanceDanceRevolution", "jubeat": "jubeat", "popn": "pop'n music",
	"nostalgia": "ノスタルジア", "gitadora": "GITADORA", "museca": "MÚSECA",
}

// ServiceName returns the canonical display title for a registry service ID.
func ServiceName(id string) string {
	if n, ok := serviceNames[id]; ok {
		return n
	}
	return id
}

type Localized struct {
	Ko string `json:"ko"`
	En string `json:"en"`
	Ja string `json:"ja"`
}

type DirectionA struct {
	LoginEnabled bool     `json:"login_enabled,omitempty"`
	RedirectURIs []string `json:"redirect_uris"`
}

type DirectionBService struct {
	ID    string `json:"id"`
	Scope string `json:"scope"`
}

// DirectionB declares the partner's authorization server. It is validated and
// stored now; activation ships with the records adapter (spec section 3).
type DirectionB struct {
	Issuer           string              `json:"issuer"`
	AuthorizationURL string              `json:"authorization_url"`
	TokenURL         string              `json:"token_url"`
	UserInfoURL      string              `json:"userinfo_url"`
	RevocationURL    string              `json:"revocation_url"`
	RecordsURL       string              `json:"records_url"`
	ClientAuthMethod string              `json:"client_auth_method"`
	IdentityScope    string              `json:"identity_scope"`
	Services         []DirectionBService `json:"services"`
}

// Joint declares one-step reciprocal connection support (spec sections 4–6).
type Joint struct {
	Enabled                bool     `json:"enabled"`
	Manifests              []string `json:"manifests"`
	JointURL               string   `json:"joint_url"`
	ConnectStartURL        string   `json:"connect_start_url"`
	KeyRotationWindowHours int      `json:"key_rotation_window_hours"`
}

// Form is the partner-authored part of the onboarding document. Credentials are
// never part of it: Musecat-issued secrets are generated after development registration, and
// partner-issued credentials are collected when their direction activates.
type Form struct {
	Spec         string      `json:"spec"`
	PartnerID    string      `json:"partner_id"`
	DisplayName  Localized   `json:"display_name"`
	Description  Localized   `json:"description"`
	ContactEmail string      `json:"contact_email"`
	Website      string      `json:"website,omitempty"`
	PrivacyURL   string      `json:"privacy_url,omitempty"`
	TermsURL     string      `json:"terms_url,omitempty"`
	SupportURL   string      `json:"support_url,omitempty"`
	DirectionA   DirectionA  `json:"direction_a"`
	DirectionB   *DirectionB `json:"direction_b,omitempty"`
	Joint        *Joint      `json:"joint,omitempty"`
}

// FieldErrors maps a JSON pointer-like field path to a stable error code.
type FieldErrors map[string]string

func (e FieldErrors) Error() string { return "invalid_form" }

func httpsURL(raw string) bool {
	if len(raw) > 2048 {
		return false
	}
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.Fragment == "" && u.RawQuery == "" && !u.ForceQuery && strings.ToLower(u.Hostname()) != "localhost"
}

func origin(raw string) bool {
	if !httpsURL(raw) {
		return false
	}
	u, _ := url.Parse(raw)
	return u.Path == "" || u.Path == "/"
}

func localized(l Localized, path string, required bool, max int, errs FieldErrors) {
	for suffix, v := range map[string]string{".ko": l.Ko, ".en": l.En, ".ja": l.Ja} {
		if len(v) > max || strings.TrimSpace(v) != v {
			errs[path+suffix] = "invalid"
		}
	}
	if required && l.En == "" {
		errs[path+".en"] = "required"
	}
}

// Validate normalizes the form in place and reports every field problem at once.
// Validation is structural; reachability and protocol checks are a later phase.
func (f *Form) Validate() error {
	errs := FieldErrors{}
	if f.Spec != FormSpec {
		errs["spec"] = "unsupported"
	}
	f.PartnerID = strings.TrimSpace(f.PartnerID)
	if !identifier.MatchString(f.PartnerID) || strings.HasPrefix(f.PartnerID, "musecat") {
		errs["partner_id"] = "invalid"
	}
	localized(f.DisplayName, "display_name", true, 128, errs)
	localized(f.Description, "description", false, 1024, errs)
	f.ContactEmail = strings.TrimSpace(f.ContactEmail)
	if a, err := mail.ParseAddress(f.ContactEmail); f.ContactEmail == "" || err != nil || a.Address != f.ContactEmail || len(f.ContactEmail) > 254 {
		errs["contact_email"] = "invalid"
	}
	for key, value := range map[string]string{"website": f.Website, "privacy_url": f.PrivacyURL, "terms_url": f.TermsURL, "support_url": f.SupportURL} {
		if value != "" && !httpsURL(value) {
			errs[key] = "invalid"
		}
	}
	if f.DirectionA.RedirectURIs == nil && f.DirectionB != nil {
		f.DirectionA.RedirectURIs = []string{}
	}
	if n := len(f.DirectionA.RedirectURIs); n > 8 || (n == 0 && f.DirectionB == nil) {
		errs["direction_a.redirect_uris"] = "count"
	}
	if f.DirectionA.LoginEnabled && len(f.DirectionA.RedirectURIs) == 0 {
		errs["direction_a.redirect_uris"] = "requires_direction_a"
	}
	seen := map[string]bool{}
	for i, u := range f.DirectionA.RedirectURIs {
		u = strings.TrimSpace(u)
		f.DirectionA.RedirectURIs[i] = u
		if (!httpsURL(u) && !loopbackRedirect(u)) || seen[u] {
			errs["direction_a.redirect_uris."+itoa(i)] = "invalid"
		}
		seen[u] = true
	}
	if f.DirectionB != nil {
		b := f.DirectionB
		for path, v := range map[string]string{"issuer": b.Issuer, "authorization_url": b.AuthorizationURL, "token_url": b.TokenURL, "userinfo_url": b.UserInfoURL, "revocation_url": b.RevocationURL, "records_url": b.RecordsURL} {
			if !httpsURL(v) {
				errs["direction_b."+path] = "invalid"
			}
		}
		if b.Issuer != "" && !origin(b.Issuer) {
			errs["direction_b.issuer"] = "origin"
		}
		if b.ClientAuthMethod != "client_secret_basic" {
			errs["direction_b.client_auth_method"] = "unsupported"
		}
		if b.IdentityScope == "" || len(b.IdentityScope) > 128 || strings.ContainsAny(b.IdentityScope, " \t\n") {
			errs["direction_b.identity_scope"] = "invalid"
		}
		if n := len(b.Services); n == 0 || n > 16 {
			errs["direction_b.services"] = "count"
		}
		ids := map[string]bool{}
		for i, s := range b.Services {
			if !slices.Contains(ServiceRegistry, s.ID) || ids[s.ID] {
				errs["direction_b.services."+itoa(i)+".id"] = "unknown"
			}
			ids[s.ID] = true
			if s.Scope == "" || len(s.Scope) > 128 || strings.ContainsAny(s.Scope, " \t\n") || s.Scope == b.IdentityScope {
				errs["direction_b.services."+itoa(i)+".scope"] = "invalid"
			}
		}
	}
	if f.Joint != nil && f.Joint.Enabled {
		j := f.Joint
		if len(f.DirectionA.RedirectURIs) == 0 {
			errs["direction_a.redirect_uris"] = "requires_direction_a"
		}
		if f.DirectionB == nil {
			errs["joint.enabled"] = "requires_direction_b"
		}
		if len(j.Manifests) == 0 || !slices.Contains(j.Manifests, JointManifest) || len(j.Manifests) > 8 {
			errs["joint.manifests"] = "unsupported"
		}
		if !httpsURL(j.JointURL) {
			errs["joint.joint_url"] = "invalid"
		}
		if !httpsURL(j.ConnectStartURL) {
			errs["joint.connect_start_url"] = "invalid"
		}
		if j.KeyRotationWindowHours == 0 {
			j.KeyRotationWindowHours = 24
		}
		if j.KeyRotationWindowHours < 1 || j.KeyRotationWindowHours > 168 {
			errs["joint.key_rotation_window_hours"] = "range"
		}
	}
	if len(errs) > 0 {
		return errs
	}
	return nil
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

// loopbackRedirect permits exact local HTTP callbacks for development clients only.
func loopbackRedirect(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return false
	}
	switch strings.ToLower(u.Hostname()) {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

// ValidateProduction rejects local callback exceptions before production review.
func (f *Form) ValidateProduction() error {
	if err := f.Validate(); err != nil {
		return err
	}
	for _, u := range f.DirectionA.RedirectURIs {
		if !httpsURL(u) || isLoopbackHost(u) {
			return FieldErrors{"direction_a.redirect_uris": "production_requires_https"}
		}
	}
	return nil
}

func isLoopbackHost(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
