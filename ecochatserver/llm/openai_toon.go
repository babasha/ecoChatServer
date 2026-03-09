package llm

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/egor/ecochatserver/llm/toon"
)

// DetectAndTranslateTOON - TOON версия определения языка и перевода для OpenAI
// Экономит ~45-50% токенов по сравнению с JSON версией
func (a *OpenAIAdapter) DetectAndTranslateTOON(ctx context.Context, text, targetLang string) (*TranslationResult, error) {
	// 🎯 КОМПАКТНЫЙ ПРОМПТ (используем общий промпт из toon/prompts.go)
	prompt := toon.BuildDetectAndTranslatePrompt(text, targetLang)

	// Retry logic: до 2 попыток
	maxRetries := 2
	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
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
			MaxTokens:   2000, // thinking-модели (Qwen3.5) тратят ~500 токенов на размышления
			ChatTemplateKwargs: map[string]interface{}{
				"enable_thinking": false,
			},
		}

		chatResp, err := a.sendRequest(ctx, req)
		if err != nil {
			lastErr = err
			if attempt < maxRetries {
				log.Printf("[OPENAI_TOON] DetectAndTranslate attempt %d failed: %v, retrying...", attempt, err)
				time.Sleep(time.Second * time.Duration(attempt))
				continue
			}
			return nil, fmt.Errorf("translation service unavailable after %d attempts: %w", maxRetries, lastErr)
		}

		// Парсим ответ
		resp := a.parseResponse(chatResp)

		// Логируем использование токенов
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

		// 🎯 ПАРСИМ TOON ОТВЕТ
		cleanedText := toon.CleanLLMResponse(resp.Text)
		parsed, err := toon.ParseSimpleObject(cleanedText)
		if err != nil {
			lastErr = fmt.Errorf("failed to parse TOON response: %w", err)
			if attempt < maxRetries {
				log.Printf("[OPENAI_TOON] Parse failed on attempt %d: %v, retrying...", attempt, err)
				log.Printf("[OPENAI_TOON] Raw: %s, Cleaned: %s", resp.Text, cleanedText)
				time.Sleep(time.Second * time.Duration(attempt))
				continue
			}
			return nil, lastErr
		}

		// Извлекаем данные
		detectedLang := parsed["lang"]
		translation := parsed["text"]

		if detectedLang == "" {
			log.Printf("[OPENAI_TOON] WARNING: empty lang in response")
			detectedLang = "unknown"
		}

		return &TranslationResult{
			DetectedLang: strings.ToLower(strings.TrimSpace(detectedLang)),
			Translation:  translation,
		}, nil
	}

	return nil, fmt.Errorf("DetectAndTranslateTOON failed after %d attempts: %w", maxRetries, lastErr)
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

	// 🎯 УПРОЩЕННЫЙ ПРОМПТ (используем общий промпт из toon/prompts.go)
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

	// Логируем использование токенов
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

	// 🎯 ПАРСИМ TOON СПИСОК
	cleanedText := toon.CleanLLMResponse(resp.Text)
	translations, err := toon.ParseSimpleList(cleanedText)
	if err != nil {
		log.Printf("[OPENAI_TOON] Failed to parse batch: %v", err)
		log.Printf("[OPENAI_TOON] Raw: %s, Cleaned: %s", resp.Text, cleanedText)
		return nil, fmt.Errorf("failed to parse translations: %w", err)
	}

	// Проверка количества
	if len(translations) != len(texts) {
		log.Printf("[OPENAI_TOON] WARNING - expected %d, got %d translations", len(texts), len(translations))
		// Дополняем или обрезаем
		for len(translations) < len(texts) {
			translations = append(translations, texts[len(translations)])
		}
		if len(translations) > len(texts) {
			translations = translations[:len(texts)]
		}
	}

	return translations, nil
}

// TranslateTextTOON - TOON версия простого перевода для OpenAI
// Максимальная экономия ~50-60% токенов
func (a *OpenAIAdapter) TranslateTextTOON(ctx context.Context, text, fromLang, toLang string) (string, error) {
	// 🎯 МАКСИМАЛЬНО ПРОСТОЙ ПРОМПТ (используем общий промпт из toon/prompts.go)
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
		MaxTokens:   2000, // thinking-модели (Qwen3.5) тратят ~500 токенов на размышления
		ChatTemplateKwargs: map[string]interface{}{
			"enable_thinking": false,
		},
	}

	chatResp, err := a.sendRequest(ctx, req)
	if err != nil {
		return "", err
	}

	resp := a.parseResponse(chatResp)

	// Логируем использование токенов
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

	// Очищаем от возможных кавычек и markdown
	result := toon.CleanLLMResponse(resp.Text)
	return strings.Trim(strings.TrimSpace(result), "\"'"), nil
}
