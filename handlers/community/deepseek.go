package community

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type DeepSeekTranslator struct {
	config TranslationConfig
	client *http.Client
}

func NewDeepSeekTranslator(config TranslationConfig, client *http.Client) (*DeepSeekTranslator, error) {
	config.APIKey = strings.TrimSpace(config.APIKey)
	config.BaseURL = strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	config.Model = strings.TrimSpace(config.Model)
	if config.APIKey == "" {
		return nil, fmt.Errorf("translation API key is not configured")
	}
	if config.BaseURL == "" {
		return nil, fmt.Errorf("translation base URL is not configured")
	}
	if config.Model == "" {
		return nil, fmt.Errorf("translation model is not configured")
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &DeepSeekTranslator{config: config, client: client}, nil
}

func (t *DeepSeekTranslator) Translate(ctx context.Context, source SourceContent) (TranslationResult, error) {
	source.Title = strings.TrimSpace(source.Title)
	source.Body = strings.TrimSpace(source.Body)
	if source.Body == "" {
		return nil, fmt.Errorf("source body is empty")
	}
	input, err := json.Marshal(map[string]any{
		"source_locale":  OriginalLocale,
		"target_locales": []string{"en-US", "ja-JP"},
		"content":        source,
	})
	if err != nil {
		return nil, fmt.Errorf("encode translation input: %w", err)
	}
	payload := map[string]any{
		"model": t.config.Model,
		"messages": []map[string]string{
			{"role": "system", "content": translationSystemPrompt(t.config.Glossary)},
			{"role": "user", "content": string(input)},
		},
		"thinking":        map[string]string{"type": "disabled"},
		"temperature":     0.1,
		"max_tokens":      maxTranslationTokens,
		"response_format": map[string]string{"type": "json_object"},
		"stream":          false,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode DeepSeek request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.config.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build DeepSeek request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+t.config.APIKey)
	req.Header.Set("Content-Type", "application/json")

	res, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call DeepSeek: %w", err)
	}
	defer res.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read DeepSeek response: %w", err)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("DeepSeek returned %d: %s", res.StatusCode, truncate(strings.TrimSpace(string(responseBody)), 1000))
	}
	var envelope struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(responseBody, &envelope); err != nil {
		return nil, fmt.Errorf("decode DeepSeek response: %w", err)
	}
	if len(envelope.Choices) == 0 || strings.TrimSpace(envelope.Choices[0].Message.Content) == "" {
		return nil, fmt.Errorf("DeepSeek returned no translation candidate")
	}
	var result TranslationResult
	raw := trimJSONFence(envelope.Choices[0].Message.Content)
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return nil, fmt.Errorf("decode DeepSeek translation JSON: %w", err)
	}
	if err := validateTranslationResult(result, source); err != nil {
		return nil, err
	}
	return result, nil
}

func trimJSONFence(value string) string {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "```") {
		return value
	}
	value = strings.TrimPrefix(value, "```json")
	value = strings.TrimPrefix(value, "```")
	value = strings.TrimSuffix(value, "```")
	return strings.TrimSpace(value)
}
