package readpolicy

import (
	"errors"
	"github.com/pocketbase/pocketbase/core"
	_ "github.com/pocketbase/pocketbase/migrations"
	"github.com/pocketbase/pocketbase/tests"
	"testing"
)

func TestDefaultAndDeploymentIsolation(t *testing.T) {
	app, err := tests.NewTestApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer app.Cleanup()
	re := &core.RequestEvent{App: app}
	if err := Check(re, "a"); err != nil {
		t.Fatal(err)
	}
	expected := errors.New("denied")
	Register(app, func(_ *core.RequestEvent, ids []string) error {
		if len(ids) != 2 {
			t.Fatal(ids)
		}
		return expected
	})
	if !errors.Is(Check(re, "a", "b"), expected) {
		t.Fatal("missing rejection")
	}
	if err := Check(re); err != nil {
		t.Fatal(err)
	}
}
