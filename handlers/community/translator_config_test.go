package community

import "testing"

func TestConfiguredTranslatorDefaultsWithoutEnvironment(t *testing.T) {
	t.Setenv("MUSECAT_TRANSLATION_PROVIDER", "unsupported-ambient-provider")
	t.Setenv("MUSECAT_TRANSLATION_MODEL", "ambient-model")
	translator, err := NewConfiguredTranslator(TranslationConfig{Provider: "deepseek", APIKey: "test-key"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	deepseek := translator.(*DeepSeekTranslator)
	if deepseek.config.BaseURL != defaultDeepSeekBaseURL || deepseek.config.Model != defaultDeepSeekModel {
		t.Fatal("explicit provider must use adapter defaults, not environment")
	}
	translator, err = NewConfiguredTranslator(TranslationConfig{APIKey: "test-key"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	gemini := translator.(*GeminiTranslator)
	if gemini.config.BaseURL != defaultGeminiBaseURL || gemini.config.Model != defaultGeminiModel {
		t.Fatal("empty provider must use Gemini defaults")
	}
}
