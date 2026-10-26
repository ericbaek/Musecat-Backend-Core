package partnerapp_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"

	"github.com/ericbaek/musecat-backend-core/connections"
	"github.com/ericbaek/musecat-backend-core/delegation"
	"github.com/ericbaek/musecat-backend-core/handlers/user"
	"github.com/ericbaek/musecat-backend-core/partnerapp"
	"github.com/ericbaek/musecat-backend-core/testutil"
)

type fixture struct {
	t        *testing.T
	app      *tests.TestApp
	manager  *delegation.Manager
	service  *partnerapp.Service
	owner    *core.Record
	operator *core.Record
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{t: t, app: testutil.NewTestApp(t)}
	var err error
	f.manager, err = delegation.NewManager("https://api.musecat.example", "https://musecat.example/oauth/authorize",
		[]delegation.Client{{ID: "static_partner", Name: "Static", Secret: strings.Repeat("s", 40), RedirectURIs: []string{"https://static.example/cb"}, Scopes: []string{delegation.AccountAccess}}}, user.CheckActiveUser)
	if err != nil {
		t.Fatal(err)
	}
	f.service = partnerapp.NewService(f.manager, nil, nil, "")
	if err := f.service.Sync(f.app); err != nil {
		t.Fatal(err)
	}
	f.owner = f.user()
	f.operator = f.user()
	return f
}

func (f *fixture) user() *core.Record {
	c, err := f.app.FindCollectionByNameOrId("user")
	if err != nil {
		f.t.Fatal(err)
	}
	r := core.NewRecord(c)
	r.SetEmail(fmt.Sprintf("partnerapp-%d@example.com", time.Now().UnixNano()))
	r.SetPassword("secret123")
	if err := f.app.Save(r); err != nil {
		f.t.Fatal(err)
	}
	return r
}

func validForm(id string) partnerapp.Form {
	return partnerapp.Form{
		Spec: partnerapp.FormSpec, PartnerID: id,
		DisplayName:  partnerapp.Localized{Ko: "파트너", En: "Partner", Ja: "パートナー"},
		ContactEmail: "dev@partner.example",
		DirectionA:   partnerapp.DirectionA{RedirectURIs: []string{"https://partner.example/oauth/musecat/callback"}},
	}
}

func TestFormValidation(t *testing.T) {
	cases := map[string]func(*partnerapp.Form){
		"spec":               func(f *partnerapp.Form) { f.Spec = "v0" },
		"partner_id":         func(f *partnerapp.Form) { f.PartnerID = "Bad ID" },
		"reserved id":        func(f *partnerapp.Form) { f.PartnerID = "musecat_app" },
		"display_name.en":    func(f *partnerapp.Form) { f.DisplayName.En = "" },
		"contact_email":      func(f *partnerapp.Form) { f.ContactEmail = "not-an-email" },
		"no redirect":        func(f *partnerapp.Form) { f.DirectionA.RedirectURIs = nil },
		"http redirect":      func(f *partnerapp.Form) { f.DirectionA.RedirectURIs = []string{"http://partner.example/cb"} },
		"localhost redirect": func(f *partnerapp.Form) { f.DirectionA.RedirectURIs = []string{"https://localhost/cb"} },
		"query redirect":     func(f *partnerapp.Form) { f.DirectionA.RedirectURIs = []string{"https://partner.example/cb?x=1"} },
		"duplicate redirect": func(f *partnerapp.Form) {
			f.DirectionA.RedirectURIs = []string{"https://p.example/a", "https://p.example/a"}
		},
		"direction_b incomplete": func(f *partnerapp.Form) {
			f.DirectionB = &partnerapp.DirectionB{Issuer: "https://p.example", ClientAuthMethod: "client_secret_basic", IdentityScope: "profile", Services: []partnerapp.DirectionBService{{ID: "chunithm", Scope: "chunithm:read"}}}
		},
		"direction_b unknown service": func(f *partnerapp.Form) {
			f.DirectionB = fullB()
			f.DirectionB.Services = []partnerapp.DirectionBService{{ID: "pump", Scope: "pump:read"}}
		},
		"direction_b post auth": func(f *partnerapp.Form) { f.DirectionB = fullB(); f.DirectionB.ClientAuthMethod = "client_secret_post" },
		"joint without b": func(f *partnerapp.Form) {
			f.Joint = &partnerapp.Joint{Enabled: true, Manifests: []string{partnerapp.JointManifest}, JointURL: "https://p.example/j", ConnectStartURL: "https://p.example/s"}
		},
		"joint manifest": func(f *partnerapp.Form) {
			f.DirectionB = fullB()
			f.Joint = &partnerapp.Joint{Enabled: true, Manifests: []string{"other/v9"}, JointURL: "https://p.example/j", ConnectStartURL: "https://p.example/s"}
		},
	}
	for name, mutate := range cases {
		f := validForm("partner")
		mutate(&f)
		var fields partnerapp.FieldErrors
		if err := f.Validate(); !errors.As(err, &fields) || len(fields) == 0 {
			t.Errorf("%s: expected field errors, got %v", name, err)
		}
	}
	full := validForm("partner")
	full.DirectionB = fullB()
	full.Joint = &partnerapp.Joint{Enabled: true, Manifests: []string{partnerapp.JointManifest}, JointURL: "https://p.example/j", ConnectStartURL: "https://p.example/s"}
	if err := full.Validate(); err != nil {
		t.Fatal("full form rejected", err)
	}
	if full.Joint.KeyRotationWindowHours != 24 {
		t.Fatal("rotation window default not applied")
	}
}

func fullB() *partnerapp.DirectionB {
	return &partnerapp.DirectionB{Issuer: "https://p.example", AuthorizationURL: "https://p.example/authorize", TokenURL: "https://p.example/token", UserInfoURL: "https://p.example/userinfo", RevocationURL: "https://p.example/revoke", RecordsURL: "https://p.example/records", ClientAuthMethod: "client_secret_basic", IdentityScope: "profile", Services: []partnerapp.DirectionBService{{ID: "chunithm", Scope: "chunithm:read"}}}
}

func TestLifecycleActivatesClientWithoutRestart(t *testing.T) {
	f := newFixture(t)
	app, err := f.service.Create(f.app, f.owner.Id, validForm("rhythm_info"))
	if err != nil {
		t.Fatal(err)
	}
	if app.Status != partnerapp.StatusApproved || app.Live || app.HasSecret {
		t.Fatal("unexpected draft projection", app)
	}
	// Registration is accepted immediately; old submit/approve calls remain compatible.
	if _, err := f.service.Submit(f.app, f.owner.Id, app.ID); err != nil {
		t.Fatal(err)
	}
	queue, err := f.service.Queue(f.app, partnerapp.StatusSubmitted)
	if err != nil || len(queue) != 1 {
		t.Fatal("queue", queue, err)
	}
	// Another user cannot see or act on it.
	if _, err := f.service.Get(f.app, f.operator.Id, app.ID); !errors.Is(err, partnerapp.ErrNotFound) {
		t.Fatal("foreign read", err)
	}
	approved, err := f.service.Approve(f.app, f.operator.Id, app.ID, "ok")
	if err != nil {
		t.Fatal(err)
	}
	if approved.Status != partnerapp.StatusApproved || approved.Live || approved.Approved == nil {
		t.Fatal("approved without secret must not be live", approved)
	}
	if _, ok := f.manager.Client("rhythm_info"); ok {
		t.Fatal("client registered before secret exists")
	}
	clientID, secret, err := f.service.GenerateSecret(f.app, f.owner.Id, app.ID)
	if err != nil || clientID != "rhythm_info" || len(secret) < 32 {
		t.Fatal("secret", clientID, err)
	}
	info, ok := f.manager.Client("rhythm_info")
	if !ok || info.RedirectURIs[0] != "https://partner.example/oauth/musecat/callback" || info.Scopes[0] != delegation.AccountAccess {
		t.Fatal("client not live after secret", info, ok)
	}
	if err := f.manager.AuthenticateClient("rhythm_info", secret); err != nil {
		t.Fatal("secret does not authenticate", err)
	}
	if err := f.manager.AuthenticateClient("rhythm_info", secret+"x"); err == nil {
		t.Fatal("wrong secret accepted")
	}
	if _, ok := f.manager.Client("static_partner"); !ok {
		t.Fatal("static client lost on reload")
	}
	events, err := f.service.OwnerEvents(f.app, f.owner.Id, app.ID)
	if err != nil || len(events) != 4 || events[0].Kind != "secret_generated" || events[3].Kind != "created" {
		t.Fatal("events", events, err)
	}
	// Rotation keeps the client live but changes the secret.
	_, rotated, err := f.service.GenerateSecret(f.app, f.owner.Id, app.ID)
	if err != nil || rotated == secret {
		t.Fatal("rotate", err)
	}
	if err := f.manager.AuthenticateClient("rhythm_info", secret); err == nil {
		t.Fatal("old secret still valid")
	}
	// Suspension removes the client immediately; reinstatement restores it with the same secret.
	if _, err := f.service.Suspend(f.app, f.operator.Id, app.ID, "abuse"); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.manager.Client("rhythm_info"); ok {
		t.Fatal("suspended client still registered")
	}
	if _, err := f.service.Update(f.app, f.owner.Id, app.ID, validForm("rhythm_info")); !errors.Is(err, partnerapp.ErrState) {
		t.Fatal("edit while suspended", err)
	}
	back, err := f.service.Reinstate(f.app, f.operator.Id, app.ID, "")
	if err != nil || back.Status != partnerapp.StatusApproved || !back.Live {
		t.Fatal("reinstate", back, err)
	}
	if err := f.manager.AuthenticateClient("rhythm_info", rotated); err != nil {
		t.Fatal("secret lost across suspension", err)
	}
	// Editing an approved application keeps the old form live until re-approval.
	changed := validForm("rhythm_info")
	changed.DirectionA.RedirectURIs = []string{"https://partner.example/new/callback"}
	edited, err := f.service.Update(f.app, f.owner.Id, app.ID, changed)
	if err != nil || edited.Status != partnerapp.StatusApproved || !edited.Live {
		t.Fatal("edit approved", edited, err)
	}
	if info, _ := f.manager.Client("rhythm_info"); info.RedirectURIs[0] != "https://partner.example/oauth/musecat/callback" {
		t.Fatal("production edit activated before review")
	}
	renamed := changed
	renamed.PartnerID = "other_id"
	if _, err := f.service.Update(f.app, f.owner.Id, app.ID, renamed); !errors.Is(err, partnerapp.ErrState) {
		t.Fatal("partner_id must be immutable after approval", err)
	}
	if _, err := f.service.Submit(f.app, f.owner.Id, app.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Approve(f.app, f.operator.Id, app.ID, ""); err != nil {
		t.Fatal(err)
	}
	if info, _ := f.manager.Client("rhythm_info"); info.RedirectURIs[0] != "https://partner.example/new/callback" {
		t.Fatal("approved edit not live")
	}
	// A fresh service instance (another process) converges from the database alone.
	other, err := delegation.NewManager("https://api.musecat.example", "https://musecat.example/oauth/authorize", nil, user.CheckActiveUser)
	if err != nil {
		t.Fatal(err)
	}
	if err := partnerapp.NewService(other, nil, nil, "").Sync(f.app); err != nil {
		t.Fatal(err)
	}
	if err := other.AuthenticateClient("rhythm_info", rotated); err != nil {
		t.Fatal("second instance did not load the live client", err)
	}
}

func TestConflictsAndLimits(t *testing.T) {
	f := newFixture(t)
	if _, err := f.service.Create(f.app, f.owner.Id, validForm("static_partner")); !errors.Is(err, partnerapp.ErrConflict) {
		t.Fatal("static client ID must be reserved", err)
	}
	first, err := f.service.Create(f.app, f.owner.Id, validForm("dup"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Create(f.app, f.operator.Id, validForm("dup")); !errors.Is(err, partnerapp.ErrConflict) {
		t.Fatal("duplicate partner_id across owners", err)
	}
	if _, err := f.service.Update(f.app, f.owner.Id, first.ID, validForm("static_partner")); !errors.Is(err, partnerapp.ErrState) {
		t.Fatal("registered ID must be immutable", err)
	}
	for i := 0; i < 7; i++ {
		if _, err := f.service.Create(f.app, f.owner.Id, validForm(fmt.Sprintf("app%d", i))); err != nil {
			t.Fatal(i, err)
		}
	}
	if _, err := f.service.Create(f.app, f.owner.Id, validForm("app_overflow")); !errors.Is(err, partnerapp.ErrState) {
		t.Fatal("owner limit", err)
	}
	// Review actions cannot reject an already registered application.
	if _, err := f.service.Reject(f.app, f.operator.Id, first.ID, ""); !errors.Is(err, partnerapp.ErrState) {
		t.Fatal("reject active app", err)
	}

}

func TestDisabledOAuthStillAllowsRegistration(t *testing.T) {
	f := newFixture(t)
	service := partnerapp.NewService(nil, nil, nil, "")
	app, err := service.Create(f.app, f.owner.Id, validForm("later"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Submit(f.app, f.owner.Id, app.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Approve(f.app, f.operator.Id, app.ID, ""); err != nil {
		t.Fatal("register without OAuth", err)
	}
}

func TestDirectionBActivatesProviderWithCredentials(t *testing.T) {
	f := newFixture(t)
	key := []byte(strings.Repeat("k", 32))
	conns, err := connections.NewManager(nil, key, nil)
	if err != nil {
		t.Fatal(err)
	}
	service := partnerapp.NewService(f.manager, conns, nil, "")
	if err := service.Sync(f.app); err != nil {
		t.Fatal(err)
	}
	form := validForm("rhythm_info")
	form.DirectionB = fullB()
	app, err := service.Create(f.app, f.owner.Id, form)
	if err != nil {
		t.Fatal(err)
	}
	// Redirect URI is derived from the consent application origin so partners can
	// register it before approval.
	if app.MusecatRedirectURI != "https://musecat.example"+partnerapp.CallbackPath+"/rhythm_info" || !app.DirectionBDeclared {
		t.Fatal("redirect uri", app.MusecatRedirectURI)
	}
	creds := partnerapp.Credentials{ClientID: "musecat-at-partner", ClientSecret: strings.Repeat("p", 40)}
	// Credentials may be entered before approval; activation waits for the approved form.
	withCreds, err := service.SetCredentials(f.app, f.owner.Id, app.ID, creds)
	if err != nil || !withCreds.HasDirectionBCredentials || !withCreds.DirectionBLive {
		t.Fatal("pre-approval credentials", withCreds, err)
	}
	if _, err := service.SetCredentials(f.app, f.owner.Id, app.ID, partnerapp.Credentials{ClientID: "x", ClientSecret: "short"}); err == nil {
		t.Fatal("weak credentials accepted")
	}
	if len(conns.ProvidersFor(f.owner.Id)) != 1 || len(conns.Providers()) != 0 {
		t.Fatal("development provider audience incorrect")
	}
	if _, err := service.Submit(f.app, f.owner.Id, app.ID); err != nil {
		t.Fatal(err)
	}
	approved, err := service.Approve(f.app, f.operator.Id, app.ID, "")
	if err != nil || !approved.DirectionBLive {
		t.Fatal("approve", approved, err)
	}
	providers := conns.Providers()
	if len(providers) != 1 || providers[0].ID != "rhythm_info" || providers[0].Issuer != "https://p.example" || providers[0].Services[0].ID != "chunithm" || providers[0].Services[0].Name != "CHUNITHM" {
		t.Fatal("provider registration", providers)
	}
	// Stored ciphertext never contains the secret; projection never includes it.
	r, err := f.app.FindRecordById(partnerapp.Applications, app.ID)
	if err != nil || strings.Contains(r.GetString("direction_b_credentials"), "musecat-at-partner") {
		t.Fatal("credentials stored in plaintext")
	}
	if detail, _ := service.Get(f.app, f.owner.Id, app.ID); strings.Contains(fmt.Sprint(detail), "musecat-at-partner") {
		t.Fatal("projection leaks credentials")
	}
	// A second partner with the same issuer cannot activate.
	other := validForm("clone_info")
	other.DirectionB = fullB()
	clone, err := service.Create(f.app, f.operator.Id, other)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetCredentials(f.app, f.operator.Id, clone.ID, creds); !errors.Is(err, partnerapp.ErrConflict) {
		t.Fatal("duplicate issuer activated", err)
	}
	// Suspension and credential removal both deactivate the provider immediately.
	if _, err := service.Suspend(f.app, f.operator.Id, app.ID, ""); err != nil {
		t.Fatal(err)
	}
	if len(conns.Providers()) != 0 {
		t.Fatal("suspended provider still live")
	}
	if _, err := service.Reinstate(f.app, f.operator.Id, app.ID, ""); err != nil || len(conns.Providers()) != 1 {
		t.Fatal("reinstate", err)
	}
	if cleared, err := service.ClearCredentials(f.app, f.owner.Id, app.ID); err != nil || cleared.DirectionBLive || len(conns.Providers()) != 0 {
		t.Fatal("clear", cleared, err)
	}
	// Without a connection key, credentials cannot be stored at all.
	if _, err := partnerapp.NewService(f.manager, nil, nil, "").SetCredentials(f.app, f.owner.Id, app.ID, creds); !errors.Is(err, partnerapp.ErrNoConnectionKey) {
		t.Fatal("missing key", err)
	}
}

func TestJointActivationAndInboundKeyRotation(t *testing.T) {
	f := newFixture(t)
	conns, err := connections.NewManager(nil, []byte(strings.Repeat("k", 32)), nil)
	if err != nil {
		t.Fatal(err)
	}
	joint, err := connections.NewJointManager(conns, f.manager, nil)
	if err != nil {
		t.Fatal(err)
	}
	service := partnerapp.NewService(f.manager, conns, joint, "")
	form := validForm("joint_info")
	form.DirectionB = fullB()
	form.Joint = &partnerapp.Joint{Enabled: true, Manifests: []string{connections.JointManifest}, JointURL: "https://p.example/joint", ConnectStartURL: "https://p.example/start", KeyRotationWindowHours: 24}
	app, err := service.Create(f.app, f.owner.Id, form)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.SetCredentials(f.app, f.owner.Id, app.ID, partnerapp.Credentials{ClientID: "musecat", ClientSecret: strings.Repeat("s", 40)}); err != nil {
		t.Fatal(err)
	}
	if _, err = service.SetJointKey(f.app, f.owner.Id, app.ID, strings.Repeat("o", 43)); err != nil {
		t.Fatal(err)
	}
	_, key, err := service.GeneratePartnerKey(f.app, f.owner.Id, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(joint.Partners()) != 0 {
		t.Fatal("joint activated before approval")
	}
	if _, err = service.Submit(f.app, f.owner.Id, app.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = service.GenerateSecret(f.app, f.owner.Id, app.ID); err != nil {
		t.Fatal("registered OAuth secret failed")
	}
	if _, err = service.Approve(f.app, f.operator.Id, app.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err = service.GenerateSecret(f.app, f.owner.Id, app.ID); err != nil {
		t.Fatal(err)
	}
	if id, ok := joint.AuthenticatePartner(key); !ok || id != "joint_info" {
		t.Fatal("joint key did not activate", id)
	}
	_, newKey, err := service.GeneratePartnerKey(f.app, f.owner.Id, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := joint.AuthenticatePartner(key); !ok {
		t.Fatal("rotation dropped previous key")
	}
	if _, ok := joint.AuthenticatePartner(newKey); !ok {
		t.Fatal("new key inactive")
	}
	row, _ := f.app.FindRecordById(partnerapp.Applications, app.ID)
	row.Set("previous_partner_key_expires", time.Now().Add(-time.Second))
	if err = f.app.Save(row); err != nil {
		t.Fatal(err)
	}
	if err = service.Sync(f.app); err != nil {
		t.Fatal(err)
	}
	if _, ok := joint.AuthenticatePartner(key); ok {
		t.Fatal("expired previous key accepted")
	}
	if strings.Contains(row.GetString("joint_credentials"), strings.Repeat("o", 43)) || row.GetString("partner_key_hash") == newKey {
		t.Fatal("keys persisted in plaintext")
	}
	if _, err = service.Suspend(f.app, f.operator.Id, app.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := joint.AuthenticatePartner(newKey); ok {
		t.Fatal("suspended partner accepted")
	}
}

func TestWithdrawnOwnerCannotKeepOrReactivateApplication(t *testing.T) {
	f := newFixture(t)
	application, err := f.service.Create(f.app, f.owner.Id, validForm("withdrawn_partner"))
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
	run, err := f.service.QueueChecks(f.app, f.operator.Id, application.ID, true, partnerapp.CheckFixtures{})
	if err != nil {
		t.Fatal(err)
	}
	queued, _ := f.app.FindRecordById(partnerapp.CheckRuns, run.ID)
	queued.Set("status", "running")
	queued.Set("fixtures", "encrypted-operator-fixtures")
	if err := f.app.Save(queued); err != nil {
		t.Fatal(err)
	}
	before, err := f.service.Version(f.app)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.app.RunInTransaction(func(tx core.App) error {
		owner, err := tx.FindRecordById("user", f.owner.Id)
		if err != nil {
			return err
		}
		owner.Set("withdrawn", true)
		if err = tx.Save(owner); err != nil {
			return err
		}
		return partnerapp.SuspendOwnedApplications(tx, owner.Id)
	}); err != nil {
		t.Fatal(err)
	}
	cancelled, _ := f.app.FindRecordById(partnerapp.CheckRuns, run.ID)
	if cancelled.GetString("status") != "cancelled" || cancelled.GetString("fixtures") != "" {
		t.Fatal("operator-owned run survived app owner withdrawal")
	}
	registration, err := f.app.FindFirstRecordByFilter(delegation.Registrations, "client='withdrawn_partner'")
	if err != nil || registration.GetBool("enabled") {
		t.Fatal("registration survived withdrawal", err)
	}
	after, err := f.service.Version(f.app)
	if err != nil || after == before {
		t.Fatal("withdrawal invisible to other instances", err)
	}
	if err := f.service.Sync(f.app); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.manager.Client("withdrawn_partner"); ok {
		t.Fatal("withdrawn owner's client still active")
	}
	if _, err = f.service.Reinstate(f.app, f.operator.Id, application.ID, ""); !errors.Is(err, partnerapp.ErrState) {
		t.Fatal("withdrawn owner reinstated", err)
	}
}

func TestLoginCapabilityChangesOnlyWithExplicitOwnerUpdate(t *testing.T) {
	f := newFixture(t)
	form := validForm("login_partner")
	app, err := f.service.Create(f.app, f.owner.Id, form)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.Submit(f.app, f.owner.Id, app.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.Approve(f.app, f.operator.Id, app.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err = f.service.GenerateSecret(f.app, f.owner.Id, app.ID); err != nil {
		t.Fatal(err)
	}
	info, _ := f.manager.Client("login_partner")
	if len(info.Scopes) != 1 || info.Scopes[0] != delegation.AccountAccess {
		t.Fatal("existing client upgraded", info)
	}
	form.DirectionA.LoginEnabled = true
	if _, err = f.service.Update(f.app, f.owner.Id, app.ID, form); err != nil {
		t.Fatal(err)
	}
	info, _ = f.manager.Client("login_partner")
	if len(info.Scopes) != 1 {
		t.Fatal("production edit activated scopes before review")
	}
	if _, err = f.service.Submit(f.app, f.owner.Id, app.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.Approve(f.app, f.operator.Id, app.ID, ""); err != nil {
		t.Fatal(err)
	}
	info, _ = f.manager.Client("login_partner")
	seen := map[string]bool{}
	for _, scope := range info.Scopes {
		seen[scope] = true
	}
	for _, scope := range []string{delegation.OpenID, delegation.Profile, "arcade:read"} {
		if !seen[scope] {
			t.Fatal("login scope missing", scope)
		}
	}
}

func TestDirectionBOnlyLifecycle(t *testing.T) {
	f := newFixture(t)
	conns, err := connections.NewManager(nil, []byte(strings.Repeat("k", 32)), nil)
	if err != nil {
		t.Fatal(err)
	}
	service := partnerapp.NewService(f.manager, conns, nil, "")
	form := validForm("records_only")
	form.DirectionA.RedirectURIs = nil
	form.DirectionB = fullB()
	if err := form.Validate(); err != nil {
		t.Fatal("B-only rejected", err)
	}
	for _, kind := range []string{"login", "joint", "invalid_b"} {
		bad := form
		switch kind {
		case "login":
			bad.DirectionA.LoginEnabled = true
		case "joint":
			bad.Joint = &partnerapp.Joint{Enabled: true, Manifests: []string{partnerapp.JointManifest}, JointURL: "https://p.example/joint", ConnectStartURL: "https://p.example/connect"}
		case "invalid_b":
			bad.DirectionB = &partnerapp.DirectionB{}
		}
		err := bad.Validate()
		if err == nil {
			t.Fatalf("invalid %s B-only accepted", kind)
		}
		if kind != "invalid_b" {
			var fields partnerapp.FieldErrors
			if !errors.As(err, &fields) || fields["direction_a.redirect_uris"] != "requires_direction_a" {
				t.Fatalf("%s missing explicit Direction A requirement: %v", kind, err)
			}
		}
	}
	a, err := service.Create(f.app, f.owner.Id, form)
	if err != nil {
		t.Fatal(err)
	}
	if a.DirectionADeclared || a.DirectionALive || a.Live || !a.DirectionBDeclared {
		t.Fatal("draft capabilities", a)
	}
	creds := partnerapp.Credentials{ClientID: "musecat-records", ClientSecret: strings.Repeat("b", 40)}
	if _, err = service.SetCredentials(f.app, f.owner.Id, a.ID, creds); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Submit(f.app, f.owner.Id, a.ID); err != nil {
		t.Fatal(err)
	}
	a, err = service.Approve(f.app, f.operator.Id, a.ID, "B only")
	if err != nil || !a.Live || !a.DirectionBLive || a.DirectionALive || a.HasSecret {
		t.Fatal("approval", a, err)
	}
	if len(conns.Providers()) != 1 {
		t.Fatal("B provider missing")
	}
	if _, ok := f.manager.Client(form.PartnerID); ok {
		t.Fatal("B-only became OAuth client")
	}
	if _, _, err = service.GenerateSecret(f.app, f.owner.Id, a.ID); !errors.Is(err, partnerapp.ErrState) {
		t.Fatal("B-only issued A secret", err)
	}
	// Persisted state reloads without an A secret or client registration.
	if err = service.Sync(f.app); err != nil {
		t.Fatal(err)
	}
	console, err := service.Console(f.app, f.owner.Id, a.ID, 1)
	if err != nil || len(console.Scopes) != 0 || len(console.RedirectURIs) != 0 || console.ConnectedCount != 0 {
		t.Fatal("B-only console", console, err)
	}
	if a, err = service.Suspend(f.app, f.operator.Id, a.ID, "pause"); err != nil || a.Live || len(conns.Providers()) != 0 {
		t.Fatal("suspend", a, err)
	}
	if _, err = service.SetCredentials(f.app, f.owner.Id, a.ID, creds); !errors.Is(err, partnerapp.ErrState) {
		t.Fatal("suspended credential write", err)
	}
	if a, err = service.Reinstate(f.app, f.operator.Id, a.ID, "resume"); err != nil || !a.Live || len(conns.Providers()) != 1 {
		t.Fatal("reinstate", a, err)
	}
	if a, err = service.ClearCredentials(f.app, f.owner.Id, a.ID); err != nil || a.Live || a.DirectionBLive || len(conns.Providers()) != 0 {
		t.Fatal("clear credentials", a, err)
	}
	if a, err = service.SetCredentials(f.app, f.owner.Id, a.ID, creds); err != nil || !a.Live {
		t.Fatal("restore credentials", a, err)
	}
}

func TestRemovingDirectionADisablesExistingClient(t *testing.T) {
	f := newFixture(t)
	conns, err := connections.NewManager(nil, []byte(strings.Repeat("k", 32)), nil)
	if err != nil {
		t.Fatal(err)
	}
	service := partnerapp.NewService(f.manager, conns, nil, "")
	form := validForm("switch_to_b")
	form.DirectionB = fullB()
	a, err := service.Create(f.app, f.owner.Id, form)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Submit(f.app, f.owner.Id, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Approve(f.app, f.operator.Id, a.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err = service.GenerateSecret(f.app, f.owner.Id, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.manager.Client(form.PartnerID); !ok {
		t.Fatal("A client absent")
	}
	form.DirectionA.RedirectURIs = nil
	if _, err = service.Update(f.app, f.owner.Id, a.ID, form); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.manager.Client(form.PartnerID); !ok {
		t.Fatal("production edit disabled A before review")
	}
	if _, err = service.Submit(f.app, f.owner.Id, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Approve(f.app, f.operator.Id, a.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.manager.Client(form.PartnerID); ok {
		t.Fatal("stored old secret reactivated A for B-only")
	}
	if err = service.Sync(f.app); err != nil {
		t.Fatal(err)
	}
	if _, _, err = service.GenerateSecret(f.app, f.owner.Id, a.ID); !errors.Is(err, partnerapp.ErrState) {
		t.Fatal("B-only rotated old secret", err)
	}
}

func TestRecordsKeyForBOnlyDoesNotEnableJoint(t *testing.T) {
	f := newFixture(t)
	conns, err := connections.NewManager(nil, []byte(strings.Repeat("k", 32)), nil)
	if err != nil {
		t.Fatal(err)
	}
	service := partnerapp.NewService(f.manager, conns, nil, "")
	form := validForm("b_records_key")
	form.DirectionA.RedirectURIs = nil
	form.DirectionB = fullB()
	a, err := service.Create(f.app, f.owner.Id, form)
	if err != nil {
		t.Fatal(err)
	}
	_, key, err := service.GenerateRecordsKey(f.app, f.owner.Id, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.AuthenticateRecordsKey(f.app, key); err == nil {
		t.Fatal("inactive B authenticated")
	}
	a, err = service.SetCredentials(f.app, f.owner.Id, a.ID, partnerapp.Credentials{ClientID: "musecat", ClientSecret: strings.Repeat("p", 40)})
	if err != nil {
		t.Fatal(err)
	}
	if id, err := service.AuthenticateRecordsKey(f.app, key); err != nil || id != form.PartnerID {
		t.Fatal(id, err)
	}
	if a.JointLive || a.JointDeclared {
		t.Fatal("records key enabled Joint")
	}
	if _, _, err = service.GeneratePartnerKey(f.app, f.owner.Id, a.ID); !errors.Is(err, partnerapp.ErrState) {
		t.Fatal("Joint key issued for B only", err)
	}
	if _, _, err = service.GenerateRecordsKey(f.app, f.operator.Id, a.ID); !errors.Is(err, partnerapp.ErrNotFound) {
		t.Fatal("foreign key issued", err)
	}
	_, rotated, err := service.GenerateRecordsKey(f.app, f.owner.Id, a.ID)
	if err != nil || rotated == key {
		t.Fatal(err)
	}
	for _, k := range []string{key, rotated} {
		if _, err := service.AuthenticateRecordsKey(f.app, k); err != nil {
			t.Fatal("rotation window", err)
		}
	}
	r, _ := f.app.FindRecordById(partnerapp.Applications, a.ID)
	r.Set("previous_partner_key_expires", time.Now().Add(-time.Minute))
	if err = f.app.Save(r); err != nil {
		t.Fatal(err)
	}
	if _, err = service.AuthenticateRecordsKey(f.app, key); err == nil {
		t.Fatal("expired previous key accepted")
	}
	if _, err = service.Suspend(f.app, f.operator.Id, a.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, err = service.AuthenticateRecordsKey(f.app, rotated); err == nil {
		t.Fatal("suspended key accepted")
	}
}

func TestRegistrationAndOwnerUpdateNeedNoOperatorAction(t *testing.T) {
	f := newFixture(t)
	form := validForm("immediate_registration")
	a, err := f.service.Create(f.app, f.owner.Id, form)
	if err != nil {
		t.Fatal(err)
	}
	if a.Status != partnerapp.StatusApproved || a.Approved == nil {
		t.Fatal("registration snapshot missing", a)
	}
	_, secret, err := f.service.GenerateSecret(f.app, f.owner.Id, a.ID)
	if err != nil {
		t.Fatal("secret requires review", err)
	}
	if err = f.manager.AuthenticateClient(form.PartnerID, secret); err != nil {
		t.Fatal("not immediately usable", err)
	}
	registration, err := f.app.FindFirstRecordByFilter(delegation.Registrations, "client='immediate_registration'")
	if err != nil {
		t.Fatal(err)
	}
	epoch := registration.GetString("epoch")
	form.DirectionA.RedirectURIs = []string{"https://partner.example/changed"}
	a, err = f.service.Update(f.app, f.owner.Id, a.ID, form)
	if err != nil {
		t.Fatal(err)
	}
	client, ok := f.manager.Client(form.PartnerID)
	if !ok || client.RedirectURIs[0] != form.DirectionA.RedirectURIs[0] || !a.Live {
		t.Fatal("update requires review", client, a)
	}
	registration, err = f.app.FindRecordById(delegation.Registrations, registration.Id)
	if err != nil || registration.GetString("epoch") == epoch {
		t.Fatal("changed registration did not invalidate old epoch", err)
	}
	invalid := form
	invalid.DirectionA.RedirectURIs = []string{"http://untrusted.example/cb"}
	if _, err = f.service.Update(f.app, f.owner.Id, a.ID, invalid); err == nil {
		t.Fatal("unvalidated update activated")
	}
	client, _ = f.manager.Client(form.PartnerID)
	if client.RedirectURIs[0] != form.DirectionA.RedirectURIs[0] {
		t.Fatal("invalid update changed active configuration")
	}
}
