package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"
)

// LLMClient представляет клиент для взаимодействия с локальной ЛЛМ моделью
type LLMClient struct {
	apiURL     string
	apiTimeout time.Duration
	client     *http.Client
}

// Message представляет сообщение в формате API LM Studio
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatCompletionRequest представляет запрос к API чата
type ChatCompletionRequest struct {
	Model       string     `json:"model"`
	Messages    []Message  `json:"messages"`
	Temperature float64    `json:"temperature,omitempty"`
	MaxTokens   int        `json:"max_tokens,omitempty"`
	Stream      bool       `json:"stream,omitempty"`
}

// ChatCompletionChoice представляет вариант ответа от API
type ChatCompletionChoice struct {
	Index        int      `json:"index"`
	Message      Message  `json:"message"`
	FinishReason string   `json:"finish_reason"`
}

// ChatCompletionResponse представляет ответ от API чата
type ChatCompletionResponse struct {
	ID      string                `json:"id"`
	Object  string                `json:"object"`
	Created int64                 `json:"created"`
	Model   string                `json:"model"`
	Choices []ChatCompletionChoice `json:"choices"`
	Usage   map[string]int        `json:"usage"`
}

// NewLLMClient создает новый клиент для взаимодействия с LLM
func NewLLMClient() *LLMClient {
	apiURL := os.Getenv("LLM_API_URL")
	if apiURL == "" {
		apiURL = "http://localhost:1234/v1"
	}

	timeout := 30 * time.Second
	
	return &LLMClient{
		apiURL:     apiURL,
		apiTimeout: timeout,
		client: &http.Client{
			Timeout: timeout,
		},
	}
}

// GenerateResponse генерирует ответ на сообщение пользователя
func (c *LLMClient) GenerateResponse(userMessage string, chatHistory []Message) (string, error) {
	// Если история чата не передана, создаем ее только с текущим сообщением
	if len(chatHistory) == 0 {
		chatHistory = []Message{
			{
				Role:    "system",
				Content: "Ты вежливый и полезный ассистент, который отвечает на вопросы клиентов компании. Твои ответы должны быть краткими, информативными и дружелюбными.сначала нужно поздароваться сказать ЗДОРОВО КАРОВА",
			},
			{
				Role:    "user",
				Content: userMessage,
			},
		}
	} else {
		// Добавляем новое сообщение пользователя в историю
		chatHistory = append(chatHistory, Message{
			Role:    "user",
			Content: userMessage,
		})
	}

	// Формируем запрос
	reqBody := ChatCompletionRequest{
		Model:       "gemma", // Используем дефолтную модель или ту, что доступна в LM Studio
		Messages:    chatHistory,
		Temperature: 0.7,
		MaxTokens:   1000,
	}

	reqJSON, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("ошибка при сериализации запроса: %v", err)
	}

	log.Printf("Отправка запроса к LLM: %s", string(reqJSON))

	// Отправляем запрос к API
	req, err := http.NewRequest("POST", c.apiURL+"/chat/completions", bytes.NewBuffer(reqJSON))
	if err != nil {
		return "", fmt.Errorf("ошибка при создании запроса: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("ошибка при выполнении запроса: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("API вернул ошибку (код %d): %s", resp.StatusCode, string(body))
	}

	// Читаем ответ
	var completion ChatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&completion); err != nil {
		return "", fmt.Errorf("ошибка при декодировании ответа: %v", err)
	}

	// Проверяем, что есть хотя бы один вариант ответа
	if len(completion.Choices) == 0 {
		return "", fmt.Errorf("API не вернул ни одного варианта ответа")
	}

	// Возвращаем содержимое первого ответа
	return completion.Choices[0].Message.Content, nil
}