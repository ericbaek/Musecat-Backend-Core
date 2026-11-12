package main

import (
	"testing"

	"github.com/pocketbase/pocketbase"

	arcadeflag "github.com/ericbaek/musecat-backend-core/handlers/arcade/flag"
	"github.com/ericbaek/musecat-backend-core/handlers/community"
)

func TestRuntimeTranslationSelection(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "deployment-key")
	t.Setenv("MUSECAT_TRANSLATION_REASONING_EFFORT", "none")
	config := translationConfig()
	if config.APIKey != "deployment-key" || config.ReasoningEffort != "none" {
		t.Fatal("OpenAI translation config was not selected")
	}
}

func TestRuntimeOwnsJobSelection(t *testing.T) {
	t.Setenv("MUSECAT_GEO_DATA_DIR", "")
	t.Setenv("MUSECAT_TRANSLATION_REASONING_EFFORT", "none")
	t.Setenv("MUSECAT_TRANSLATION_BASE_URL", "")
	t.Setenv("MUSECAT_TRANSLATION_MODEL", "")
	t.Setenv("OPENAI_API_KEY", "")
	for _, enabled := range []string{"", "false", "true"} {
		for _, key := range []string{"", "configured-test-key"} {
			t.Run(enabled+"/"+map[bool]string{true: "key", false: "no-key"}[key != ""], func(t *testing.T) {
				t.Setenv("MUSECAT_COMMUNITY_TRANSLATIONS_ENABLED", enabled)
				t.Setenv("OPENAI_API_KEY", key)
				app := pocketbase.NewWithConfig(pocketbase.Config{DefaultDataDir: t.TempDir()})
				if err := configureRuntime(app); err != nil {
					t.Fatal(err)
				}
				jobs := map[string]bool{}
				for _, job := range app.Cron().Jobs() {
					jobs[job.Id()] = true
				}
				if jobs[community.TranslationCronJobID] != (enabled == "true" && key != "") {
					t.Fatal("translation requires explicit enablement and a provider key")
				}
				if !jobs[arcadeflag.AutoSolveCronJobID] || !jobs[arcadeflag.StaleResolutionCronJobID] {
					t.Fatal("flag domain jobs must remain enabled")
				}
			})
		}
	}
}

func TestRuntimeRejectsInvalidGeoBundle(t *testing.T) {
	t.Setenv("MUSECAT_GEO_DATA_DIR", t.TempDir())
	app := pocketbase.NewWithConfig(pocketbase.Config{DefaultDataDir: t.TempDir()})
	if err := configureRuntime(app); err == nil {
		t.Fatal("invalid offline resource must fail startup")
	}
}
