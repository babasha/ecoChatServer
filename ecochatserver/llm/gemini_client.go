package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// GeminiClient представляет клиента для взаимодействия с Google Gemini API
type GeminiClient struct {
	apiKey string
	client *http.Client
}

// GeminiMessage представляет сообщение в формате Gemini
type GeminiMessage struct {
	Role  string               `json:"role"`
	Parts []map[string]string  `json:"parts"`
}

// GeminiRequest описывает тело POST‑запроса к Gemini API
type GeminiRequest struct {
	Contents         []GeminiMessage `json:"contents"`
	GenerationConfig map[string]interface{} `json:"generationConfig,omitempty"`
}

// GeminiCandidate представляет один из вариантов ответа
type GeminiCandidate struct {
	Content      GeminiMessage `json:"content"`
	FinishReason string        `json:"finishReason"`
}

// GeminiResponse описывает ответ Gemini API
type GeminiResponse struct {
	Candidates []GeminiCandidate `json:"candidates"`
}

// NewGeminiClient создаёт новый GeminiClient
func NewGeminiClient() *GeminiClient {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		panic("GEMINI_API_KEY not set")
	}

	timeout := 30 * time.Second
	if t := os.Getenv("LLM_API_TIMEOUT"); t != "" {
		if d, err := time.ParseDuration(t); err == nil {
			timeout = d
		}
	}

	return &GeminiClient{
		apiKey: apiKey,
		client: &http.Client{Timeout: timeout},
	}
}

// convertMessagesToGemini конвертирует историю чата в формат Gemini
func convertMessagesToGemini(messages []Message) []GeminiMessage {
	var geminiMessages []GeminiMessage

	for _, msg := range messages {
		role := msg.Role
		// Gemini использует "user" и "model" вместо "assistant"
		if role == "assistant" {
			role = "model"
		}
		// Системные сообщения добавляем как user message с префиксом
		if role == "system" {
			role = "user"
		}

		geminiMessages = append(geminiMessages, GeminiMessage{
			Role: role,
			Parts: []map[string]string{
				{"text": msg.Content},
			},
		})
	}

	return geminiMessages
}

// GenerateResponse отправляет историю диалога в Gemini API
func (c *GeminiClient) GenerateResponse(
	ctx context.Context,
	userMessage string,
	chatHistory []Message,
) (string, error) {
	// Если истории нет — инициализируем с системным сообщением
	if len(chatHistory) == 0 {
		chatHistory = []Message{
			{
				Role: "system",
				Content: "Ты вежливый и полезный ассистент, отвечающий на вопросы клиентов. " +
					"Твои ответы должны быть краткими, информативными и дружелюбными.",
			},
		}
	}

	// Добавляем текущее сообщение пользователя
	chatHistory = append(chatHistory, Message{
		Role:    "user",
		Content: userMessage,
	})

	// Конвертируем в формат Gemini
	geminiMessages := convertMessagesToGemini(chatHistory)

	// Формируем тело запроса
	reqBody := GeminiRequest{
		Contents: geminiMessages,
		GenerationConfig: map[string]interface{}{
			"temperature":     0.7,
			"maxOutputTokens": 1000,
		},
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request body: %w", err)
	}

	// URL Gemini API
	endpoint := fmt.Sprintf(
		"https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-flash-lite:generateContent?key=%s",
		c.apiKey,
	)

	// Создаём HTTP‑запрос
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("create HTTP request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// Выполняем запрос
	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("Gemini API request failed: %w", err)
	}
	defer resp.Body.Close()

	// Обрабатываем код ответа
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("Gemini API error: status %d, body: %s", resp.StatusCode, string(body))
	}

	// Декодируем JSON-ответ
	var geminiResp GeminiResponse
	if err := json.NewDecoder(resp.Body).Decode(&geminiResp); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	if len(geminiResp.Candidates) == 0 {
		return "", fmt.Errorf("Gemini API returned no candidates")
	}

	if len(geminiResp.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("Gemini API returned empty content")
	}

	return geminiResp.Candidates[0].Content.Parts[0]["text"], nil
}

// DetectLanguage определяет язык текста с помощью Gemini
func (c *GeminiClient) DetectLanguage(ctx context.Context, text string) (string, error) {
	if text == "" {
		return "", fmt.Errorf("текст для определения языка пуст")
	}

	prompt := fmt.Sprintf(`Определи язык следующего текста. Ответь ТОЛЬКО двухбуквенным кодом языка (например: ru, en, pl, de, fr, es, it, uk, be).

Текст: "%s"

Ответ (только код языка):`, text)

	response, err := c.GenerateResponse(ctx, prompt, []Message{
		{
			Role:    "system",
			Content: "Ты эксперт по определению языков. Отвечай ТОЛЬКО двухбуквенным ISO кодом языка без дополнительных объяснений.",
		},
	})
	if err != nil {
		return "", fmt.Errorf("ошибка запроса к Gemini: %w", err)
	}

	// Очищаем ответ от лишних символов
	langCode := strings.TrimSpace(strings.ToLower(response))
	langCode = strings.Trim(langCode, ".,!?\"'")

	return langCode, nil
}

// TranslateText переводит текст с одного языка на другой
func (c *GeminiClient) TranslateText(ctx context.Context, text, fromLang, toLang string) (string, error) {
	if text == "" {
		return "", fmt.Errorf("текст для перевода пуст")
	}

	if fromLang == toLang {
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

	translation, err := c.GenerateResponse(ctx, prompt, []Message{
		{
			Role:    "system",
			Content: "Ты профессиональный переводчик. Переводи точно и естественно. Отвечай ТОЛЬКО переводом без дополнительных комментариев.",
		},
	})
	if err != nil {
		return "", fmt.Errorf("ошибка запроса к Gemini: %w", err)
	}

	// Очищаем перевод от лишних кавычек
	translation = strings.TrimSpace(translation)
	translation = strings.Trim(translation, "\"'")

	return translation, nil
}
