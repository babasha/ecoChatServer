package llm

import (
	"context"

	"github.com/google/uuid"
)

// ============================================================================
// UNIVERSAL TYPES - Provider-agnostic data structures
// ============================================================================
//
// Эти типы используются во всем приложении и не зависят от конкретного провайдера.
// Каждый адаптер провайдера конвертирует свои специфичные типы в эти универсальные.

// Message представляет универсальное сообщение в диалоге
type Message struct {
	Role    string `json:"role"`    // "system", "user", "assistant", "function"
	Content string `json:"content"` // Текст сообщения
}

// Tool представляет универсальное описание инструмента (функции) для LLM
type Tool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"` // JSON Schema
}

// FunctionCall представляет вызов функции от LLM
type FunctionCall struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"args"`
}

// GenerateOptions опции для генерации ответа
type GenerateOptions struct {
	Temperature    float64 `json:"temperature,omitempty"`
	MaxTokens      int     `json:"max_tokens,omitempty"`
	SystemPrompt   string  `json:"system_prompt,omitempty"`
	ResponseFormat string  `json:"response_format,omitempty"` // "text" или "json"

	// Для логирования использования токенов
	ClientID       *uuid.UUID `json:"-"` // UUID клиента (для статистики)
	ChatID         *uuid.UUID `json:"-"` // UUID чата (для статистики)
	AdminID        *uuid.UUID `json:"-"` // UUID админа (для статистики)
}

// GetClientID возвращает ClientID для логирования
func (o *GenerateOptions) GetClientID() *uuid.UUID {
	if o == nil {
		return nil
	}
	return o.ClientID
}

// GetChatID возвращает ChatID для логирования
func (o *GenerateOptions) GetChatID() *uuid.UUID {
	if o == nil {
		return nil
	}
	return o.ChatID
}

// GetAdminID возвращает AdminID для логирования
func (o *GenerateOptions) GetAdminID() *uuid.UUID {
	if o == nil {
		return nil
	}
	return o.AdminID
}

// Response универсальный ответ от провайдера
type Response struct {
	Text         string        `json:"text"`                    // Текстовый ответ (если есть)
	FunctionCall *FunctionCall `json:"function_call,omitempty"` // Вызов функции (если есть)
	FinishReason string        `json:"finish_reason"`           // "stop", "length", "function_call", etc.
	Usage        *Usage        `json:"usage,omitempty"`         // Статистика использования токенов
}

// Usage статистика использования токенов
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// TranslationResult результат перевода текста
type TranslationResult struct {
	DetectedLang string `json:"detected_lang"` // Определённый язык (код вида "ru", "en")
	Translation  string `json:"translation"`   // Переведённый текст
}

// ============================================================================
// PROVIDER INTERFACE - Main abstraction for all LLM providers
// ============================================================================

// Provider универсальный интерфейс для работы с любым LLM провайдером
type Provider interface {
	// GetName возвращает название провайдера (для логирования)
	GetName() string

	// GenerateResponse простая генерация ответа без инструментов
	GenerateResponse(ctx context.Context, userMessage string, chatHistory []Message, opts *GenerateOptions) (*Response, error)

	// GenerateWithTools генерация ответа с поддержкой вызова функций
	GenerateWithTools(ctx context.Context, userMessage string, chatHistory []Message, tools []Tool, opts *GenerateOptions) (*Response, error)

	// ContinueWithFunctionResult продолжает диалог после выполнения функции
	ContinueWithFunctionResult(ctx context.Context, chatHistory []Message, functionCall *FunctionCall, functionResult string, opts *GenerateOptions) (*Response, error)

	// TranslateText переводит текст с одного языка на другой
	TranslateText(ctx context.Context, text, fromLang, toLang string) (string, error)

	// DetectAndTranslate определяет язык и переводит за один запрос
	DetectAndTranslate(ctx context.Context, text, targetLang string) (*TranslationResult, error)

	// TranslateBatch переводит несколько текстов за один запрос (для эффективности)
	TranslateBatch(ctx context.Context, texts []string, fromLang, toLang string) ([]string, error)
}

// ============================================================================
// PROVIDER CONFIG - Configuration for creating providers
// ============================================================================

// ProviderType тип провайдера
type ProviderType string

const (
	ProviderGemini   ProviderType = "gemini"
	ProviderOpenAI   ProviderType = "openai"
	ProviderLMStudio ProviderType = "lmstudio" // LM Studio (локальная LLM через OpenAI-совместимый API)
	ProviderClaude   ProviderType = "claude"
	ProviderOllama   ProviderType = "ollama"   // Локальные модели Ollama
	ProviderDefault  ProviderType = "gemini"   // По умолчанию
)

// ProviderConfig конфигурация провайдера
type ProviderConfig struct {
	Type    ProviderType `json:"type"`              // Тип провайдера
	APIKey  string       `json:"api_key,omitempty"` // API ключ (если требуется)
	BaseURL string       `json:"base_url,omitempty"` // Базовый URL (для кастомных эндпоинтов)
	Model   string       `json:"model,omitempty"`   // Название модели
	Timeout int          `json:"timeout,omitempty"` // Таймаут в секундах
}
