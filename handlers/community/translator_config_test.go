package community

import "testing"

func TestConfiguredTranslatorDefaultsWithoutEnvironment(t *testing.T) {
	t.Setenv("MUSECAT_TRANSLATION_MODEL", "ambient-model")
	translator, err := NewConfiguredTranslator(TranslationConfig{APIKey: "test-key"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	config := translator.(*OpenAITranslator).config
	if config.BaseURL != defaultOpenAIBaseURL || config.Model != defaultOpenAIModel || config.ReasoningEffort != "none" {
		t.Fatal("adapter defaults must not read ambient environment")
	}
	if _, err := NewConfiguredTranslator(TranslationConfig{APIKey: "test-key", ReasoningEffort: "invalid"}, nil); err == nil {
		t.Fatal("invalid reasoning effort accepted")
	}
}

func TestTranslationPublicationRequiresRuntimeEnablement(t *testing.T) {
	t.Setenv("MUSECAT_COMMUNITY_TRANSLATIONS_PUBLIC", "true")
	for _, enabled := range []string{"", "false", "true", "1", "yes"} {
		t.Run(enabled, func(t *testing.T) {
			t.Setenv("MUSECAT_COMMUNITY_TRANSLATIONS_ENABLED", enabled)
			if translationsPublic() != (enabled == "true" || enabled == "1" || enabled == "yes") {
				t.Fatal("publication must require explicit runtime enablement")
			}
		})
	}
}
