package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// Маппинг кодов языков на читаемые названия (для промптов)
var langNames = map[string]string{
	"ru": "Russian",
	"en": "English",
	"pl": "Polish",
	"de": "German",
	"fr": "French",
	"es": "Spanish",
	"it": "Italian",
	"uk": "Ukrainian",
	"be": "Belarusian",
	"cs": "Czech",
	"sk": "Slovak",
	"lt": "Lithuanian",
	"lv": "Latvian",
	"et": "Estonian",
	"ka": "Georgian",
	"uz": "Uzbek",
}

// TranslateText переводит текст с одного языка на другой
func (c *GeminiClient) TranslateText(ctx context.Context, text, fromLang, toLang string) (string, error) {
	if text == "" {
		return "", fmt.Errorf("текст для перевода пуст")
	}

	if fromLang == toLang {
		return text, nil
	}

	fromName := langNames[fromLang]
	if fromName == "" {
		fromName = fromLang
	}
	toName := langNames[toLang]
	if toName == "" {
		toName = toLang
	}

	prompt := fmt.Sprintf(`Translate from %s to %s: "%s"`, fromName, toName, text)

	// Простой вызов с retry (3 попытки)
	for attempt := 1; attempt <= 3; attempt++ {
		reqBody := GeminiRequest{
			Contents: []GeminiMessage{{
				Role:  "user",
				Parts: []map[string]interface{}{{"text": prompt}},
			}},
			SystemInstruction: &GeminiSystemInstruction{
				Parts: []map[string]interface{}{{"text": "Translate. Reply with translation only."}},
			},
			GenerationConfig: map[string]interface{}{
				"temperature":     0.3,
				"maxOutputTokens": 2048,
			},
		}

		payload, _ := json.Marshal(reqBody)
		endpoint := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-flash:generateContent?key=%s", c.apiKey)

		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")

		resp, err := c.client.Do(req)
		if err != nil || resp.StatusCode != http.StatusOK {
			if attempt < 3 {
				time.Sleep(time.Duration(attempt) * time.Second)
				continue
			}
			if resp != nil {
				resp.Body.Close()
			}
			return "", fmt.Errorf("API request failed after 3 retries")
		}

		var geminiResp GeminiResponse
		json.NewDecoder(resp.Body).Decode(&geminiResp)
		resp.Body.Close()

		// Логируем использование токенов
		if geminiResp.UsageMetadata != nil {
			_ = LogUsage(ctx, UsageLogEntry{
				Provider:         "gemini",
				Model:            "gemini-2.5-flash",
				PromptTokens:     geminiResp.UsageMetadata.PromptTokenCount,
				CompletionTokens: geminiResp.UsageMetadata.CandidatesTokenCount,
				TotalTokens:      geminiResp.UsageMetadata.TotalTokenCount,
				RequestType:      "translation",
			})
		}

		if len(geminiResp.Candidates) > 0 && len(geminiResp.Candidates[0].Content.Parts) > 0 {
			if text, ok := geminiResp.Candidates[0].Content.Parts[0]["text"].(string); ok && text != "" {
				return strings.Trim(strings.TrimSpace(text), "\"'"), nil
			}
		}

		if attempt < 3 {
			time.Sleep(time.Duration(attempt) * time.Second)
		}
	}

	return "", fmt.Errorf("translation failed after 3 attempts")
}

// DetectAndTranslateResult результат определения языка и перевода
type DetectAndTranslateResult struct {
	DetectedLang string
	Translation  string
}

// DetectAndTranslate определяет язык и переводит текст за ОДИН запрос (быстрее)
func (c *GeminiClient) DetectAndTranslate(ctx context.Context, text, targetLang string) (*DetectAndTranslateResult, error) {
	if text == "" {
		return nil, fmt.Errorf("текст для перевода пуст")
	}

	targetName := langNames[targetLang]
	if targetName == "" {
		targetName = targetLang
	}

	prompt := fmt.Sprintf(`Determine the language of the text and translate it to %s.

Text: "%s"

Reply in JSON format:
{
  "detectedLang": "two-letter language code (ru, en, pl, etc.)",
  "translation": "translated text"
}

If text is already in %s, return it as translation with detected language.`, targetName, text, targetName)

	reqBody := GeminiRequest{
		Contents: []GeminiMessage{{
			Role:  "user",
			Parts: []map[string]interface{}{{"text": prompt}},
		}},
		SystemInstruction: &GeminiSystemInstruction{
			Parts: []map[string]interface{}{{"text": "You are a language detection and translation expert. Always reply in valid JSON format only."}},
		},
		GenerationConfig: map[string]interface{}{
			"temperature":     0.3,
			"maxOutputTokens": 2048,
		},
	}

	payload, _ := json.Marshal(reqBody)
	endpoint := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-flash:generateContent?key=%s", c.apiKey)

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error: status %d, body: %s", resp.StatusCode, string(body))
	}

	var geminiResp GeminiResponse
	if err := json.NewDecoder(resp.Body).Decode(&geminiResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if len(geminiResp.Candidates) == 0 || len(geminiResp.Candidates[0].Content.Parts) == 0 {
		return nil, fmt.Errorf("empty response from Gemini")
	}

	// Логируем использование токенов
	if geminiResp.UsageMetadata != nil {
		_ = LogUsage(ctx, UsageLogEntry{
			Provider:         "gemini",
			Model:            "gemini-2.5-flash",
			PromptTokens:     geminiResp.UsageMetadata.PromptTokenCount,
			CompletionTokens: geminiResp.UsageMetadata.CandidatesTokenCount,
			TotalTokens:      geminiResp.UsageMetadata.TotalTokenCount,
			RequestType:      "translation",
		})
	}

	responseText, ok := geminiResp.Candidates[0].Content.Parts[0]["text"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid response format")
	}

	// Парсим JSON ответ
	responseText = strings.TrimSpace(responseText)
	// Убираем markdown код блоки если есть
	responseText = strings.TrimPrefix(responseText, "```json")
	responseText = strings.TrimPrefix(responseText, "```")
	responseText = strings.TrimSuffix(responseText, "```")
	responseText = strings.TrimSpace(responseText)

	var result DetectAndTranslateResult
	if err := json.Unmarshal([]byte(responseText), &result); err != nil {
		log.Printf("DetectAndTranslate: failed to parse JSON: %s", responseText)
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Нормализуем код языка
	result.DetectedLang = strings.ToLower(strings.TrimSpace(result.DetectedLang))

	return &result, nil
}

// TranslateBatch переводит несколько текстов за один запрос (более эффективно)
func (c *GeminiClient) TranslateBatch(ctx context.Context, texts []string, fromLang, toLang string) ([]string, error) {
	if len(texts) == 0 {
		return []string{}, nil
	}

	if fromLang == toLang {
		return texts, nil
	}

	fromName := langNames[fromLang]
	if fromName == "" {
		fromName = fromLang
	}
	toName := langNames[toLang]
	if toName == "" {
		toName = toLang
	}

	// Компактный промпт для batch перевода
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Translate %d messages from %s to %s. Reply with translations only, one per line:\n\n", len(texts), fromName, toName))
	for i, text := range texts {
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, text))
	}

	reqBody := GeminiRequest{
		Contents: []GeminiMessage{
			{
				Role: "user",
				Parts: []map[string]interface{}{
					{"text": sb.String()},
				},
			},
		},
		SystemInstruction: &GeminiSystemInstruction{
			Parts: []map[string]interface{}{
				{"text": "Translate text. Reply with translations only, no numbering or formatting."},
			},
		},
		GenerationConfig: map[string]interface{}{
			"temperature":     0.3,
			"maxOutputTokens": 4096, // Больше для batch
		},
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	endpoint := fmt.Sprintf(
		"https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-flash:generateContent?key=%s",
		c.apiKey,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error: status %d, body: %s", resp.StatusCode, string(body))
	}

	var geminiResp GeminiResponse
	if err := json.NewDecoder(resp.Body).Decode(&geminiResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if len(geminiResp.Candidates) == 0 || len(geminiResp.Candidates[0].Content.Parts) == 0 {
		return nil, fmt.Errorf("empty response from Gemini")
	}

	// Логируем использование токенов
	if geminiResp.UsageMetadata != nil {
		_ = LogUsage(ctx, UsageLogEntry{
			Provider:         "gemini",
			Model:            "gemini-2.5-flash",
			PromptTokens:     geminiResp.UsageMetadata.PromptTokenCount,
			CompletionTokens: geminiResp.UsageMetadata.CandidatesTokenCount,
			TotalTokens:      geminiResp.UsageMetadata.TotalTokenCount,
			RequestType:      "translation",
		})
	}

	responseText, ok := geminiResp.Candidates[0].Content.Parts[0]["text"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid response format")
	}

	// Парсим результат
	lines := strings.Split(strings.TrimSpace(responseText), "\n")
	translations := make([]string, 0, len(texts))

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Убираем нумерацию если есть
		if len(line) > 3 && line[1] == '.' && line[0] >= '0' && line[0] <= '9' {
			line = strings.TrimSpace(line[2:])
		}
		translations = append(translations, line)
	}

	// Проверка количества
	if len(translations) != len(texts) {
		log.Printf("TranslateBatch: WARNING - expected %d, got %d translations", len(texts), len(translations))
		// Дополняем оригиналами если не хватает
		for len(translations) < len(texts) {
			translations = append(translations, texts[len(translations)])
		}
	}

	return translations, nil
}
