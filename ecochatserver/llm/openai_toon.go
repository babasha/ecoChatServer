package llm

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/egor/ecochatserver/llm/toon"
)

// DetectAndTranslateTOON - TOON версия определения языка и перевода для OpenAI
// Экономит ~45-50% токенов по сравнению с JSON версией
func (a *OpenAIAdapter) DetectAndTranslateTOON(ctx context.Context, text, targetLang string) (*TranslationResult, error) {
	prompt := toon.BuildDetectAndTranslatePrompt(text, targetLang)

	var result *TranslationResult
	err := withRetry(ctx, 2, "DetectAndTranslateTOON", func(attempt int) error {
		messages := []openAIMessage{
			{
				Role:    "system",
				Content: toon.SystemInstructionDetectAndTranslate,
			},
			{
				Role:    "user",
				Content: prompt,
			},
		}

		req := openAIChatRequest{
			Model:       a.model,
			Messages:    messages,
			Temperature: 0.3,
			MaxTokens:   2000,
			ChatTemplateKwargs: map[string]interface{}{
				"enable_thinking": false,
			},
		}

		chatResp, err := a.sendRequest(ctx, req)
		if err != nil {
			return err
		}

		resp := a.parseResponse(chatResp)

		if resp != nil && resp.Usage != nil {
			_ = LogUsage(ctx, UsageLogEntry{
				Provider:         "openai",
				Model:            a.model,
				PromptTokens:     resp.Usage.PromptTokens,
				CompletionTokens: resp.Usage.CompletionTokens,
				TotalTokens:      resp.Usage.TotalTokens,
				RequestType:      "translation_toon",
			})
		}

		cleanedText := toon.CleanLLMResponse(resp.Text)
		parsed, err := toon.ParseSimpleObject(cleanedText)
		if err != nil {
			log.Printf("[OPENAI_TOON] Parse failed: %v, Raw: %s, Cleaned: %s", err, resp.Text, cleanedText)
			return fmt.Errorf("failed to parse TOON response: %w", err)
		}

		detectedLang := parsed["lang"]
		translation := parsed["text"]

		if detectedLang == "" {
			log.Printf("[OPENAI_TOON] WARNING: empty lang in response")
			detectedLang = "unknown"
		}

		result = &TranslationResult{
			DetectedLang: strings.ToLower(strings.TrimSpace(detectedLang)),
			Translation:  translation,
		}
		return nil
	})

	return result, err
}

// TranslateBatchTOON - TOON версия batch перевода для OpenAI
// Экономит ~40% токенов используя простой list формат
func (a *OpenAIAdapter) TranslateBatchTOON(ctx context.Context, texts []string, fromLang, toLang string) ([]string, error) {
	if len(texts) == 0 {
		return []string{}, nil
	}

	if fromLang == toLang {
		return texts, nil
	}

	prompt := toon.BuildBatchTranslatePrompt(texts, fromLang, toLang)

	messages := []openAIMessage{
		{
			Role:    "system",
			Content: toon.SystemInstructionBatch,
		},
		{
			Role:    "user",
			Content: prompt,
		},
	}

	req := openAIChatRequest{
		Model:       a.model,
		Messages:    messages,
		Temperature: 0.3,
		MaxTokens:   2000,
		ChatTemplateKwargs: map[string]interface{}{
			"enable_thinking": false,
		},
	}

	chatResp, err := a.sendRequest(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	resp := a.parseResponse(chatResp)

	if resp != nil && resp.Usage != nil {
		_ = LogUsage(ctx, UsageLogEntry{
			Provider:         "openai",
			Model:            a.model,
			PromptTokens:     resp.Usage.PromptTokens,
			CompletionTokens: resp.Usage.CompletionTokens,
			TotalTokens:      resp.Usage.TotalTokens,
			RequestType:      "translation_batch_toon",
		})
	}

	cleanedText := toon.CleanLLMResponse(resp.Text)
	translations, err := toon.ParseSimpleList(cleanedText)
	if err != nil {
		log.Printf("[OPENAI_TOON] Failed to parse batch: %v", err)
		log.Printf("[OPENAI_TOON] Raw: %s, Cleaned: %s", resp.Text, cleanedText)
		return nil, fmt.Errorf("failed to parse translations: %w", err)
	}

	return NormalizeBatchTranslations(translations, texts), nil
}

// TranslateTextTOON - TOON версия простого перевода для OpenAI
// Максимальная экономия ~50-60% токенов
func (a *OpenAIAdapter) TranslateTextTOON(ctx context.Context, text, fromLang, toLang string) (string, error) {
	prompt := toon.BuildSimpleTranslatePrompt(text, fromLang, toLang)

	messages := []openAIMessage{
		{
			Role:    "system",
			Content: toon.SystemInstructionSimple,
		},
		{
			Role:    "user",
			Content: prompt,
		},
	}

	req := openAIChatRequest{
		Model:       a.model,
		Messages:    messages,
		Temperature: 0.3,
		MaxTokens:   2000,
		ChatTemplateKwargs: map[string]interface{}{
			"enable_thinking": false,
		},
	}

	chatResp, err := a.sendRequest(ctx, req)
	if err != nil {
		return "", err
	}

	resp := a.parseResponse(chatResp)

	if resp != nil && resp.Usage != nil {
		_ = LogUsage(ctx, UsageLogEntry{
			Provider:         "openai",
			Model:            a.model,
			PromptTokens:     resp.Usage.PromptTokens,
			CompletionTokens: resp.Usage.CompletionTokens,
			TotalTokens:      resp.Usage.TotalTokens,
			RequestType:      "translation_toon",
		})
	}

	result := toon.CleanLLMResponse(resp.Text)
	return strings.Trim(strings.TrimSpace(result), "\"'"), nil
}
