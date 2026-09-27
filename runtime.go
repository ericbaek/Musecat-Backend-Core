package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/pocketbase/pocketbase"

	"github.com/ericbaek/musecat-backend-core/coreapp"
	"github.com/ericbaek/musecat-backend-core/geo"
	arcadeflag "github.com/ericbaek/musecat-backend-core/handlers/arcade/flag"
	"github.com/ericbaek/musecat-backend-core/handlers/community"
)

// configureRuntime is the composition root: select resources and scheduled jobs
// here; shared API rules and worker state transitions remain in Core.
func configureRuntime(app *pocketbase.PocketBase) error {
	var resolver *geo.OfflineResolver
	var err error
	if dir := strings.TrimSpace(os.Getenv("MUSECAT_GEO_DATA_DIR")); dir != "" {
		resolver, err = geo.LoadOfflineResolver(dir)
	} else {
		resolver, err = geo.LoadEmbeddedResolver()
	}
	if err != nil {
		return fmt.Errorf("load offline geo data: %w", err)
	}
	var translator community.Translator
	translation := translationConfig()
	if translation.APIKey != "" {
		translator, err = community.NewConfiguredTranslator(translation, nil)
		if err != nil {
			return fmt.Errorf("configure community translation: %w", err)
		}
	}
	coreapp.Configure(app, coreapp.Config{
		ClientIPForwardSecret: strings.TrimSpace(os.Getenv("MUSECAT_IP_FORWARD_SECRET")),
		GeoResolver:           resolver,
		Documentation:         documentationConfig(),
	})
	arcadeflag.RegisterAutoSolveCron(app)
	community.RegisterTranslationCron(app, translator)
	return nil
}

func documentationConfig() coreapp.DocumentationConfig {
	return coreapp.DocumentationConfig{
		SiteDir:  "docs-site",
		Username: strings.TrimSpace(os.Getenv("DOCS_BASIC_AUTH_USER")),
		Password: strings.TrimSpace(os.Getenv("DOCS_BASIC_AUTH_PASS")),
		SpecPath: strings.TrimSpace(os.Getenv("MUSECAT_OPENAPI_SPEC_PATH")),
	}
}

func translationConfig() community.TranslationConfig {
	provider := strings.ToLower(strings.TrimSpace(os.Getenv("MUSECAT_TRANSLATION_PROVIDER")))
	if provider == "" {
		provider = "gemini"
	}
	apiKey := strings.TrimSpace(os.Getenv("MUSECAT_TRANSLATION_API_KEY"))
	if apiKey == "" {
		if provider == "deepseek" {
			apiKey = strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY"))
		} else {
			apiKey = strings.TrimSpace(os.Getenv("GEMINI_API_KEY"))
		}
	}
	return community.TranslationConfig{
		Provider: provider,
		APIKey:   apiKey,
		BaseURL:  strings.TrimSpace(os.Getenv("MUSECAT_TRANSLATION_BASE_URL")),
		Model:    strings.TrimSpace(os.Getenv("MUSECAT_TRANSLATION_MODEL")),
		Glossary: strings.TrimSpace(os.Getenv("MUSECAT_TRANSLATION_GLOSSARY")),
	}
}
