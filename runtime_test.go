package main

import (
	"testing"

	"github.com/pocketbase/pocketbase"

	arcadeflag "github.com/ericbaek/musecat-backend-core/handlers/arcade/flag"
	"github.com/ericbaek/musecat-backend-core/handlers/community"
)

func TestRuntimeTranslationSelection(t *testing.T) {
	t.Setenv("MUSECAT_TRANSLATION_PROVIDER", "deepseek")
	t.Setenv("MUSECAT_TRANSLATION_API_KEY", "")
	t.Setenv("DEEPSEEK_API_KEY", "deployment-key")
	config := translationConfig()
	if config.Provider != "deepseek" || config.APIKey != "deployment-key" {
		t.Fatal("provider-specific key was not selected")
	}
	t.Setenv("MUSECAT_TRANSLATION_API_KEY", "generic-key")
	if translationConfig().APIKey != "generic-key" {
		t.Fatal("generic key must take precedence")
	}
}

func TestRuntimeOwnsJobSelection(t *testing.T) {
	t.Setenv("MUSECAT_GEO_DATA_DIR", "")
	t.Setenv("MUSECAT_TRANSLATION_PROVIDER", "deepseek")
	t.Setenv("MUSECAT_TRANSLATION_BASE_URL", "")
	t.Setenv("MUSECAT_TRANSLATION_MODEL", "")
	t.Setenv("DEEPSEEK_API_KEY", "")
	for _, key := range []string{"", "configured-test-key"} {
		t.Run(map[bool]string{true: "enabled", false: "disabled"}[key != ""], func(t *testing.T) {
			t.Setenv("MUSECAT_TRANSLATION_API_KEY", key)
			app := pocketbase.NewWithConfig(pocketbase.Config{DefaultDataDir: t.TempDir()})
			if err := configureRuntime(app); err != nil {
				t.Fatal(err)
			}
			jobs := map[string]bool{}
			for _, job := range app.Cron().Jobs() {
				jobs[job.Id()] = true
			}
			if jobs[community.TranslationCronJobID] != (key != "") {
				t.Fatal("translation schedule does not match provider availability")
			}
			if !jobs[arcadeflag.AutoSolveCronJobID] || !jobs[arcadeflag.StaleResolutionCronJobID] {
				t.Fatal("flag domain jobs must remain enabled")
			}
		})
	}
}

func TestRuntimeRejectsInvalidGeoBundle(t *testing.T) {
	t.Setenv("MUSECAT_GEO_DATA_DIR", t.TempDir())
	app := pocketbase.NewWithConfig(pocketbase.Config{DefaultDataDir: t.TempDir()})
	if err := configureRuntime(app); err == nil {
		t.Fatal("invalid offline resource must fail startup")
	}
}
