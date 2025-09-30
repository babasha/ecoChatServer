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
)

// DetectLanguage определяет язык текста с помощью LLM
func (c *LLMClient) DetectLanguage(ctx context.Context, text string) (string, error) {
	if text == "" {
		return "", fmt.Errorf("текст для определения языка пуст")
	}

	prompt := fmt.Sprintf(`Определи язык следующего текста. Ответь ТОЛЬКО двухбуквенным кодом языка (например: ru, en, pl, de, fr, es, it, uk, be).

Текст: "%s"

Ответ (только код языка):`, text)

	messages := []Message{
		{
			Role:    "system",
			Content: "Ты эксперт по определению языков. Отвечай ТОЛЬКО двухбуквенным ISO кодом языка без дополнительных объяснений.",
		},
		{
			Role:    "user",
			Content: prompt,
		},
	}

	reqBody := ChatCompletionRequest{
		Model:       "gemma-3-4b-it-qat",
		Messages:    messages,
		Temperature: 0.1, // Низкая температура для более точного определения
		MaxTokens:   10,
	}

	response, err := c.sendRequest(ctx, reqBody)
	if err != nil {
		return "", fmt.Errorf("ошибка запроса к LLM: %w", err)
	}

	// Очищаем ответ от лишних символов
	langCode := strings.TrimSpace(strings.ToLower(response))
	langCode = strings.Trim(langCode, ".,!?\"'")

	log.Printf("DetectLanguage: text='%s' -> language='%s'", truncateText(text, 50), langCode)

	return langCode, nil
}

// TranslateText переводит текст с одного языка на другой
func (c *LLMClient) TranslateText(ctx context.Context, text, fromLang, toLang string) (string, error) {
	if text == "" {
		return "", fmt.Errorf("текст для перевода пуст")
	}

	if fromLang == toLang {
		log.Printf("TranslateText: исходный и целевой языки совпадают (%s), возвращаем оригинал", fromLang)
		return text, nil
	}

	// Маппинг кодов языков на читаемые названия
	langNames := map[string]string{
		"ru": "русский",
		"en": "английский",
		"pl": "польский",
		"de": "немецкий",
		"fr": "французский",
		"es": "испанский",
		"it": "итальянский",
		"uk": "украинский",
		"be": "белорусский",
		"cs": "чешский",
		"sk": "словацкий",
		"lt": "литовский",
		"lv": "латышский",
		"et": "эстонский",
	}

	fromName := langNames[fromLang]
	if fromName == "" {
		fromName = fromLang
	}
	toName := langNames[toLang]
	if toName == "" {
		toName = toLang
	}

	prompt := fmt.Sprintf(`Переведи следующий текст с %s языка на %s язык. Отвечай ТОЛЬКО переводом, без дополнительных комментариев и объяснений.

Текст для перевода: "%s"

Перевод:`, fromName, toName, text)

	messages := []Message{
		{
			Role:    "system",
			Content: "Ты профессиональный переводчик. Переводи точно и естественно. Отвечай ТОЛЬКО переводом без дополнительных комментариев.",
		},
		{
			Role:    "user",
			Content: prompt,
		},
	}

	reqBody := ChatCompletionRequest{
		Model:       "gemma-3-4b-it-qat",
		Messages:    messages,
		Temperature: 0.3, // Средняя температура для естественного перевода
		MaxTokens:   2000,
	}

	translation, err := c.sendRequest(ctx, reqBody)
	if err != nil {
		return "", fmt.Errorf("ошибка запроса к LLM: %w", err)
	}

	// Очищаем перевод от лишних кавычек
	translation = strings.TrimSpace(translation)
	translation = strings.Trim(translation, "\"'")

	log.Printf("TranslateText: from=%s to=%s, original='%s' -> translated='%s'",
		fromLang, toLang, truncateText(text, 50), truncateText(translation, 50))

	return translation, nil
}

// sendRequest - вспомогательная функция для отправки запроса к LLM
func (c *LLMClient) sendRequest(ctx context.Context, reqBody ChatCompletionRequest) (string, error) {
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request body: %w", err)
	}

	endpoint := fmt.Sprintf("%s/chat/completions", c.apiURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("create HTTP request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("LLM API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("LLM API error: status %d, body: %s", resp.StatusCode, string(body))
	}

	var completion ChatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&completion); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	if len(completion.Choices) == 0 {
		return "", fmt.Errorf("LLM API returned no choices")
	}

	return completion.Choices[0].Message.Content, nil
}

// truncateText обрезает текст для логирования
func truncateText(text string, maxLen int) string {
	if len(text) <= maxLen {
		return text
	}
	return text[:maxLen] + "..."
}
