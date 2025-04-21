package llm

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "os"
    "time"
)

// LLMClient представляет клиента для взаимодействия с локальной ЛЛМ-моделью.
type LLMClient struct {
    apiURL string
    client *http.Client
}

// ChatCompletionRequest описывает тело POST‑запроса к LLM API.
type ChatCompletionRequest struct {
    Model       string     `json:"model"`
    Messages    []Message  `json:"messages"`
    Temperature float64    `json:"temperature,omitempty"`
    MaxTokens   int        `json:"max_tokens,omitempty"`
    Stream      bool       `json:"stream,omitempty"`
}

// ChatCompletionChoice — один из вариантов ответа от LLM API.
type ChatCompletionChoice struct {
    Index        int     `json:"index"`
    Message      Message `json:"message"`
    FinishReason string  `json:"finish_reason"`
}

// ChatCompletionResponse описывает ответ LLM API.
type ChatCompletionResponse struct {
    ID      string                 `json:"id"`
    Object  string                 `json:"object"`
    Created int64                  `json:"created"`
    Model   string                 `json:"model"`
    Choices []ChatCompletionChoice `json:"choices"`
    Usage   map[string]int         `json:"usage"`
}

// NewLLMClient создаёт новый LLMClient.
// Настраивается URL из LLM_API_URL и таймаут из LLM_API_TIMEOUT или по умолчанию 30s.
func NewLLMClient() *LLMClient {
    apiURL := os.Getenv("LLM_API_URL")
    if apiURL == "" {
        apiURL = "http://localhost:1234/v1"
    }

    timeout := 30 * time.Second
    if t := os.Getenv("LLM_API_TIMEOUT"); t != "" {
        if d, err := time.ParseDuration(t); err == nil {
            timeout = d
        }
    }

    return &LLMClient{
        apiURL: apiURL,
        client: &http.Client{Timeout: timeout},
    }
}

// GenerateResponse отправляет историю диалога и текущее сообщение в LLM API,
// возвращает текст первого варианта ответа.
func (c *LLMClient) GenerateResponse(
    ctx context.Context,
    userMessage string,
    chatHistory []Message,
) (string, error) {
    // Если истории нет — инициализируем системным сообщением + первым user
    if len(chatHistory) == 0 {
        chatHistory = []Message{
            {
                Role:    "system",
                Content: "Ты вежливый и полезный ассистент, отвечающий на вопросы клиентов. " +
                    "Твои ответы должны быть краткими, информативными и дружелюбными.",
            },
            {
                Role:    "user",
                Content: userMessage,
            },
        }
    } else {
        chatHistory = append(chatHistory, Message{
            Role:    "user",
            Content: userMessage,
        })
    }

    // Формируем тело запроса
    reqBody := ChatCompletionRequest{
        Model:       "gemma",
        Messages:    chatHistory,
        Temperature: 0.7,
        MaxTokens:   1000,
    }
    payload, err := json.Marshal(reqBody)
    if err != nil {
        return "", fmt.Errorf("marshal request body: %w", err)
    }

    // Собираем HTTP‑запрос с контекстом
    endpoint := fmt.Sprintf("%s/chat/completions", c.apiURL)
    req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
    if err != nil {
        return "", fmt.Errorf("create HTTP request: %w", err)
    }
    req.Header.Set("Content-Type", "application/json")

    // Выполняем запрос
    resp, err := c.client.Do(req)
    if err != nil {
        return "", fmt.Errorf("LLM API request failed: %w", err)
    }
    defer resp.Body.Close()

    // Обрабатываем код ответа
    if resp.StatusCode != http.StatusOK {
        body, _ := io.ReadAll(resp.Body)
        return "", fmt.Errorf("LLM API error: status %d, body: %s", resp.StatusCode, string(body))
    }

    // Декодируем JSON-ответ
    var completion ChatCompletionResponse
    if err := json.NewDecoder(resp.Body).Decode(&completion); err != nil {
        return "", fmt.Errorf("decode response: %w", err)
    }

    if len(completion.Choices) == 0 {
        return "", fmt.Errorf("LLM API returned no choices")
    }

    return completion.Choices[0].Message.Content, nil
}