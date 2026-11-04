package partnerapp_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/ericbaek/musecat-backend-core/partnerapp"
)

func TestDevelopmentAudienceProductionReviewAndSnapshots(t *testing.T) {
	f := newFixture(t)
	tester := f.user()
	tester.Set("username", "designated_tester")
	if err := f.app.Save(tester); err != nil {
		t.Fatal(err)
	}
	form := validForm("audience_app")
	a, err := f.service.Create(f.app, f.owner.Id, form)
	if err != nil {
		t.Fatal(err)
	}
	if a.Publication != "development" || a.ReviewStatus != "none" || !f.service.CanUse(f.app, form.PartnerID, f.owner.Id) || f.service.CanUse(f.app, form.PartnerID, tester.Id) {
		t.Fatal("development audience", a)
	}
	a, err = f.service.SetTesters(f.app, f.owner.Id, a.ID, []string{"designated_tester"})
	if err != nil || len(a.Testers) != 1 || a.Testers[0] != tester.Id || a.TesterUsernames[0] != "designated_tester" || !f.service.CanUse(f.app, form.PartnerID, tester.Id) {
		t.Fatal("tester addition", a, err)
	}
	if _, err = f.service.SetTesters(f.app, f.owner.Id, a.ID, make([]string, 11)); err == nil {
		t.Fatal("more than10testers")
	}
	if _, err = f.service.SetTesters(f.app, f.owner.Id, a.ID, []string{"designated_tester", "designated_tester"}); err == nil {
		t.Fatal("duplicate testers")
	}
	if _, err = f.service.SetTesters(f.app, f.operator.Id, a.ID, nil); !errors.Is(err, partnerapp.ErrNotFound) {
		t.Fatal("foreign testers mutation", err)
	}
	a, err = f.service.Submit(f.app, f.owner.Id, a.ID)
	if err != nil || a.ReviewStatus != "submitted" || a.Publication != "development" {
		t.Fatal("production request", a, err)
	}
	a, err = f.service.Reject(f.app, f.operator.Id, a.ID, "fix docs")
	if err != nil || a.ReviewStatus != "rejected" || a.Publication != "development" {
		t.Fatal("rejection widened audience", a, err)
	}
	if _, err = f.service.Submit(f.app, f.owner.Id, a.ID); err != nil {
		t.Fatal(err)
	}
	a, err = f.service.Approve(f.app, f.operator.Id, a.ID, "")
	if err != nil || a.Publication != "production" || a.ReviewStatus != "none" || !f.service.CanUse(f.app, form.PartnerID, f.operator.Id) {
		t.Fatal("production approval", a, err)
	}
	_, secret, err := f.service.GenerateSecret(f.app, f.owner.Id, a.ID)
	if err != nil || secret == "" {
		t.Fatal(err)
	}
	form.DirectionA.RedirectURIs = []string{"https://partner.example/proposed"}
	a, err = f.service.Update(f.app, f.owner.Id, a.ID, form)
	if err != nil || a.Approved.DirectionA.RedirectURIs[0] == form.DirectionA.RedirectURIs[0] {
		t.Fatal("unreviewed production update live", a, err)
	}
	if _, err = f.service.Submit(f.app, f.owner.Id, a.ID); err != nil {
		t.Fatal(err)
	}
	a, err = f.service.Approve(f.app, f.operator.Id, a.ID, "")
	if err != nil || a.Approved.DirectionA.RedirectURIs[0] != form.DirectionA.RedirectURIs[0] {
		t.Fatal("reviewed snapshot not installed", a, err)
	}
	if _, err = f.service.Suspend(f.app, f.operator.Id, a.ID, ""); err != nil {
		t.Fatal(err)
	}
	if f.service.CanUse(f.app, form.PartnerID, f.owner.Id) {
		t.Fatal("suspended app allowed")
	}
}

func TestDevelopmentLoopbackCallbacksCannotGoProduction(t *testing.T) {
	for i, callback := range []string{"http://localhost:8088/callback", "http://127.0.0.1:8088/callback", "http://[::1]:8088/callback"} {
		f := newFixture(t)
		form := validForm("loopback_" + strings.Repeat("x", i+1))
		form.DirectionA.RedirectURIs = []string{callback}
		a, err := f.service.Create(f.app, f.owner.Id, form)
		if err != nil {
			t.Fatal(callback, err)
		}
		if _, _, err = f.service.GenerateSecret(f.app, f.owner.Id, a.ID); err != nil {
			t.Fatal("local callback not executable", callback, err)
		}
		if _, err = f.service.Submit(f.app, f.owner.Id, a.ID); err == nil {
			t.Fatal("local callback production request accepted")
		}
	}
	for _, callback := range []string{"https://127.0.0.1/cb", "https://[::1]/cb", "https://127.0.0.2/cb"} {
		form := validForm("local_tls")
		form.DirectionA.RedirectURIs = []string{callback}
		if form.ValidateProduction() == nil {
			t.Fatal("TLS loopback approved for production", callback)
		}
	}
	for _, callback := range []string{"http://localhost.evil/cb", "http://192.168.1.1/cb", "http://127.0.0.2/cb", "http://localhost/cb?query=1"} {
		form := validForm("bad_loopback")
		form.DirectionA.RedirectURIs = []string{callback}
		if form.Validate() == nil {
			t.Fatal("nonexact callback accepted", callback)
		}
	}
}
