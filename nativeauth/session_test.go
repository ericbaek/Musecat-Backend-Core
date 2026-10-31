package nativeauth_test

import (
	"crypto/sha256"
	"encoding/base64"
	"github.com/ericbaek/musecat-backend-core/nativeauth"
	"github.com/ericbaek/musecat-backend-core/testutil"
	"github.com/pocketbase/pocketbase/core"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func setup(t *testing.T) (core.App, *core.Record, nativeauth.Service) {
	t.Helper()
	app := testutil.NewTestApp(t)
	c, e := app.FindCollectionByNameOrId("user")
	if e != nil {
		t.Fatal(e)
	}
	u := core.NewRecord(c)
	u.SetEmail("native@example.com")
	u.Set("username", "nativeqa")
	u.SetPassword("testpassword123")
	if e = app.Save(u); e != nil {
		t.Fatal(e)
	}
	return app, u, nativeauth.Service{Clients: []nativeauth.Client{{ID: "ios", Callback: "https://musecat.app/native/callback"}}}
}
func challenge(v string) string {
	h := sha256.Sum256([]byte(v))
	return base64.RawURLEncoding.EncodeToString(h[:])
}
func issue(t *testing.T, s nativeauth.Service, a core.App, u *core.Record) nativeauth.Credentials {
	t.Helper()
	v := strings.Repeat("a", 43)
	c, e := s.IssueCode(a, u, "ios", "https://musecat.app/native/callback", challenge(v))
	if e != nil {
		t.Fatal(e)
	}
	o, e := s.Exchange(a, c, v, "ios", "https://musecat.app/native/callback")
	if e != nil {
		t.Fatal(e)
	}
	return o
}
func TestExchangePKCEAndSingleUse(t *testing.T) {
	a, u, s := setup(t)
	v := strings.Repeat("a", 43)
	c, e := s.IssueCode(a, u, "ios", "https://musecat.app/native/callback", challenge(v))
	if e != nil {
		t.Fatal(e)
	}
	for _, in := range [][3]string{{strings.Repeat("b", 43), "ios", "https://musecat.app/native/callback"}, {v, "partner", "https://musecat.app/native/callback"}, {v, "ios", "https://evil.example/callback"}} {
		if _, e = s.Exchange(a, c, in[0], in[1], in[2]); e == nil {
			t.Fatal("invalid binding accepted")
		}
	}
	var wins atomic.Int32
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, e := s.Exchange(a, c, v, "ios", "https://musecat.app/native/callback"); e == nil {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("exchange successes=%d", wins.Load())
	}
}
func TestRefreshReplayRevokesOnlyDevice(t *testing.T) {
	a, u, s := setup(t)
	first := issue(t, s, a, u)
	other := issue(t, s, a, u)
	next, e := s.Refresh(a, first.RefreshToken)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Authenticate(a, first.AccessToken); e == nil {
		t.Fatal("old access accepted")
	}
	if _, e = s.Refresh(a, first.RefreshToken); e == nil {
		t.Fatal("refresh replay accepted")
	}
	if _, e = s.Authenticate(a, next.AccessToken); e == nil {
		t.Fatal("replayed family remains active")
	}
	if _, e = s.Authenticate(a, other.AccessToken); e != nil {
		t.Fatal("other device revoked")
	}
	if e = s.Revoke(a, other.RefreshToken); e != nil {
		t.Fatal(e)
	}
	if e = s.Revoke(a, other.RefreshToken); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Authenticate(a, other.AccessToken); e == nil {
		t.Fatal("revoked access accepted")
	}
}
func TestExpirationAndAccountKeyRevocation(t *testing.T) {
	a, u, s := setup(t)
	now := time.Now()
	s.Now = func() time.Time { return now }
	o := issue(t, s, a, u)
	now = now.Add(11 * time.Minute)
	if _, e := s.Authenticate(a, o.AccessToken); e == nil {
		t.Fatal("expired access")
	}
	o, e := s.Refresh(a, o.RefreshToken)
	if e != nil {
		t.Fatal(e)
	}
	u.RefreshTokenKey()
	if e = a.Save(u); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Authenticate(a, o.AccessToken); e == nil {
		t.Fatal("account reset accepted")
	}
	if _, e = s.Refresh(a, o.RefreshToken); e == nil {
		t.Fatal("account reset refresh accepted")
	}
}
func TestNativeRejectsWebTokenAndWrongCallback(t *testing.T) {
	a, u, s := setup(t)
	jwt, e := u.NewAuthToken()
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Authenticate(a, jwt); e == nil {
		t.Fatal("web token accepted")
	}
	if _, e = s.IssueCode(a, u, "ios", "https://evil.example", strings.Repeat("a", 43)); e == nil {
		t.Fatal("callback accepted")
	}
}
func TestCodeAndAbsoluteRefreshExpiry(t *testing.T) {
	a, u, s := setup(t)
	now := time.Now()
	s.Now = func() time.Time { return now }
	v := strings.Repeat("a", 43)
	c, e := s.IssueCode(a, u, "ios", "https://musecat.app/native/callback", challenge(v))
	if e != nil {
		t.Fatal(e)
	}
	o := issue(t, s, a, u)
	now = now.Add(121 * time.Second)
	if _, e = s.Exchange(a, c, v, "ios", "https://musecat.app/native/callback"); e == nil {
		t.Fatal("expired code accepted")
	}
	now = now.Add(31 * 24 * time.Hour)
	if _, e = s.Refresh(a, o.RefreshToken); e == nil {
		t.Fatal("expired family accepted")
	}
}

func TestInstallSchemaRequiresUserAuthCollection(t *testing.T) {
	for _, withBaseUser := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "not auth"}[withBaseUser], func(t *testing.T) {
			app := core.NewBaseApp(core.BaseAppConfig{DataDir: t.TempDir()})
			if err := app.Bootstrap(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = app.ResetBootstrapState() })
			if withBaseUser {
				if err := app.Save(core.NewBaseCollection("user")); err != nil {
					t.Fatal(err)
				}
			}
			if err := nativeauth.InstallSchema(app); err == nil {
				t.Fatal("invalid schema prerequisite accepted")
			}
			var tables []struct {
				Name string `db:"name"`
			}
			if err := app.DB().NewQuery(`SELECT name FROM sqlite_master WHERE name LIKE 'native_auth_%'`).All(&tables); err != nil {
				t.Fatal(err)
			}
			if len(tables) != 0 {
				t.Fatal("failed schema check created private tables")
			}
		})
	}
}

func TestRemovedDevelopmentClientCannotAuthenticateOrRefresh(t *testing.T) {
	app, user, _ := setup(t)
	dev := nativeauth.Client{ID: "app.musecat.native.dev", Callback: "musecat-dev://auth/callback"}
	production := nativeauth.Client{ID: "app.musecat.native", Callback: "https://example.test/native/callback"}
	all := nativeauth.Service{Clients: []nativeauth.Client{dev, production}}
	mint := func(client nativeauth.Client) nativeauth.Credentials {
		verifier := strings.Repeat("r", 43)
		code, err := all.IssueCode(app, user, client.ID, client.Callback, challenge(verifier))
		if err != nil {
			t.Fatal(err)
		}
		credentials, err := all.Exchange(app, code, verifier, client.ID, client.Callback)
		if err != nil {
			t.Fatal(err)
		}
		return credentials
	}
	old := mint(dev)
	current := mint(production)
	active := nativeauth.Service{Clients: []nativeauth.Client{production}}
	if _, err := active.Authenticate(app, old.AccessToken); err != nativeauth.ErrInvalid {
		t.Fatal("removed dev access accepted")
	}
	if _, err := active.Refresh(app, old.RefreshToken); err != nativeauth.ErrInvalid {
		t.Fatal("removed dev refresh accepted")
	}
	// Re-enabling a registration must not undo the revocation committed by refresh denial.
	if _, err := all.Authenticate(app, old.AccessToken); err != nativeauth.ErrInvalid {
		t.Fatal("denied family revocation was rolled back")
	}
	if _, err := all.Refresh(app, old.RefreshToken); err != nativeauth.ErrInvalid {
		t.Fatal("denied family resurrected")
	}
	if _, err := active.Authenticate(app, current.AccessToken); err != nil {
		t.Fatal("remaining client access rejected")
	}
	rotated, err := active.Refresh(app, current.RefreshToken)
	if err != nil {
		t.Fatal("remaining client refresh rejected")
	}
	if _, err := active.Authenticate(app, rotated.AccessToken); err != nil {
		t.Fatal("remaining client rotation rejected")
	}
}
