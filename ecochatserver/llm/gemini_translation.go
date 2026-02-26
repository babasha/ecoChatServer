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
		endpoint := "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-flash:generateContent"

		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-goog-api-key", c.apiKey)

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

		logGeminiUsage(ctx, &geminiResp, "gemini-2.5-flash", "translation")

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

// geminiDetectAndTranslateResult внутренний результат определения языка и перевода для Gemini
// (используется только внутри GeminiClient, для публичного API используйте TranslationResult)
type geminiDetectAndTranslateResult struct {
	DetectedLang string
	Translation  string
}

// DetectAndTranslate определяет язык и переводит текст за ОДИН запрос (быстрее)
// Включает retry логику для надежности
func (c *GeminiClient) DetectAndTranslate(ctx context.Context, text, targetLang string) (*geminiDetectAndTranslateResult, error) {
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

	// Retry логика: 3 попытки с экспоненциальной задержкой
	maxRetries := 3
	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
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

		payload, err := json.Marshal(reqBody)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}

		endpoint := "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-flash:generateContent"

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-goog-api-key", c.apiKey)

		resp, err := c.client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("API request failed: %w", err)
			if attempt < maxRetries {
				log.Printf("DetectAndTranslate: попытка %d/%d провалилась: %v, повтор через %d сек...", attempt, maxRetries, err, attempt)
				time.Sleep(time.Duration(attempt) * time.Second)
				continue
			}
			return nil, lastErr
		}

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			lastErr = fmt.Errorf("API error: status %d, body: %s", resp.StatusCode, string(body))
			if attempt < maxRetries {
				log.Printf("DetectAndTranslate: попытка %d/%d провалилась: статус %d, повтор через %d сек...", attempt, maxRetries, resp.StatusCode, attempt)
				time.Sleep(time.Duration(attempt) * time.Second)
				continue
			}
			return nil, lastErr
		}

		var geminiResp GeminiResponse
		if err := json.NewDecoder(resp.Body).Decode(&geminiResp); err != nil {
			resp.Body.Close()
			lastErr = fmt.Errorf("decode response: %w", err)
			if attempt < maxRetries {
				log.Printf("DetectAndTranslate: попытка %d/%d провалилась: ошибка декодирования, повтор через %d сек...", attempt, maxRetries, attempt)
				time.Sleep(time.Duration(attempt) * time.Second)
				continue
			}
			return nil, lastErr
		}
		resp.Body.Close()

		if len(geminiResp.Candidates) == 0 || len(geminiResp.Candidates[0].Content.Parts) == 0 {
			lastErr = fmt.Errorf("empty response from Gemini")
			if attempt < maxRetries {
				log.Printf("DetectAndTranslate: попытка %d/%d провалилась: пустой ответ, повтор через %d сек...", attempt, maxRetries, attempt)
				time.Sleep(time.Duration(attempt) * time.Second)
				continue
			}
			return nil, lastErr
		}

		logGeminiUsage(ctx, &geminiResp, "gemini-2.5-flash", "translation")

		responseText, ok := geminiResp.Candidates[0].Content.Parts[0]["text"].(string)
		if !ok {
			lastErr = fmt.Errorf("invalid response format")
			if attempt < maxRetries {
				log.Printf("DetectAndTranslate: попытка %d/%d провалилась: невалидный формат, повтор через %d сек...", attempt, maxRetries, attempt)
				time.Sleep(time.Duration(attempt) * time.Second)
				continue
			}
			return nil, lastErr
		}

		// Парсим JSON ответ
		responseText = strings.TrimSpace(responseText)
		// Убираем markdown код блоки если есть
		responseText = strings.TrimPrefix(responseText, "```json")
		responseText = strings.TrimPrefix(responseText, "```")
		responseText = strings.TrimSuffix(responseText, "```")
		responseText = strings.TrimSpace(responseText)

		var result geminiDetectAndTranslateResult
		if err := json.Unmarshal([]byte(responseText), &result); err != nil {
			log.Printf("DetectAndTranslate: failed to parse JSON on attempt %d/%d: %s", attempt, maxRetries, responseText)
			lastErr = fmt.Errorf("failed to parse response: %w", err)
			if attempt < maxRetries {
				log.Printf("DetectAndTranslate: повтор через %d сек...", attempt)
				time.Sleep(time.Duration(attempt) * time.Second)
				continue
			}
			return nil, lastErr
		}

		// Нормализуем код языка
		result.DetectedLang = strings.ToLower(strings.TrimSpace(result.DetectedLang))

		// Успех!
		if attempt > 1 {
			log.Printf("DetectAndTranslate: успех после %d попыток", attempt)
		}

		return &result, nil
	}

	return nil, fmt.Errorf("DetectAndTranslate failed after %d attempts: %w", maxRetries, lastErr)
}

// TranslateBatchResponse структура JSON ответа для batch перевода
type TranslateBatchResponse struct {
	Translations []string `json:"translations"`
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

	// Формируем JSON промпт для надежного парсинга
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Translate these %d messages from %s to %s.\n\n", len(texts), fromName, toName))
	sb.WriteString("Messages to translate:\n")
	for i, text := range texts {
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, text))
	}
	sb.WriteString("\nReply in this exact JSON format:\n")
	sb.WriteString("{\n  \"translations\": [\"translation 1\", \"translation 2\", ...]\n}")

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
				{"text": "You are a translation expert. Always reply with valid JSON only, no additional text or markdown."},
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

	endpoint := "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-flash:generateContent"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", c.apiKey)

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

	logGeminiUsage(ctx, &geminiResp, "gemini-2.5-flash", "translation")

	responseText, ok := geminiResp.Candidates[0].Content.Parts[0]["text"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid response format")
	}

	// Убираем markdown код блоки если есть
	responseText = strings.TrimSpace(responseText)
	responseText = strings.TrimPrefix(responseText, "```json")
	responseText = strings.TrimPrefix(responseText, "```")
	responseText = strings.TrimSuffix(responseText, "```")
	responseText = strings.TrimSpace(responseText)

	// Парсим JSON ответ
	var batchResult TranslateBatchResponse
	if err := json.Unmarshal([]byte(responseText), &batchResult); err != nil {
		log.Printf("TranslateBatch: failed to parse JSON response: %s", responseText)
		return nil, fmt.Errorf("failed to parse JSON response: %w", err)
	}

	translations := batchResult.Translations

	// Проверка количества
	if len(translations) != len(texts) {
		log.Printf("TranslateBatch: WARNING - expected %d, got %d translations", len(texts), len(translations))
		// Дополняем оригиналами если не хватает
		for len(translations) < len(texts) {
			translations = append(translations, texts[len(translations)])
		}
		// Обрезаем если больше
		if len(translations) > len(texts) {
			translations = translations[:len(texts)]
		}
	}

	return translations, nil
}
