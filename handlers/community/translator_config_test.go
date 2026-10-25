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
