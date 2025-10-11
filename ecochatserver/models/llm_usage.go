package models

import (
	"time"

	"github.com/google/uuid"
)

// LLMUsageLog представляет запись об использовании LLM токенов
type LLMUsageLog struct {
	ID               uuid.UUID              `json:"id" db:"id"`
	Timestamp        time.Time              `json:"timestamp" db:"timestamp"`
	ClientID         *uuid.UUID             `json:"client_id,omitempty" db:"client_id"`
	ChatID           *uuid.UUID             `json:"chat_id,omitempty" db:"chat_id"`
	AdminID          *uuid.UUID             `json:"admin_id,omitempty" db:"admin_id"`
	Provider         string                 `json:"provider" db:"provider"`
	Model            string                 `json:"model" db:"model"`
	PromptTokens     int                    `json:"prompt_tokens" db:"prompt_tokens"`
	CompletionTokens int                    `json:"completion_tokens" db:"completion_tokens"`
	TotalTokens      int                    `json:"total_tokens" db:"total_tokens"`
	RequestType      string                 `json:"request_type,omitempty" db:"request_type"`
	Metadata         map[string]interface{} `json:"metadata,omitempty" db:"metadata"`
}

// LLMUsageStats представляет агрегированную статистику использования
type LLMUsageStats struct {
	Date              string  `json:"date" db:"date"`
	Provider          string  `json:"provider" db:"provider"`
	Model             string  `json:"model" db:"model"`
	RequestType       string  `json:"request_type" db:"request_type"`
	ClientID          *string `json:"client_id,omitempty" db:"client_id"`
	RequestCount      int64   `json:"request_count" db:"request_count"`
	TotalPromptTokens int64   `json:"total_prompt_tokens" db:"total_prompt_tokens"`
	TotalCompTokens   int64   `json:"total_completion_tokens" db:"total_completion_tokens"`
	TotalTokens       int64   `json:"total_tokens" db:"total_tokens_sum"`
	AvgTokens         float64 `json:"avg_tokens_per_request" db:"avg_tokens_per_request"`
	MaxTokens         int     `json:"max_tokens" db:"max_tokens_in_request"`
	MinTokens         int     `json:"min_tokens" db:"min_tokens_in_request"`
}

// DailyCostEstimate представляет оценку стоимости по дням
type DailyCostEstimate struct {
	Date          string  `json:"date" db:"date"`
	Provider      string  `json:"provider" db:"provider"`
	TotalTokens   int64   `json:"total_tokens" db:"total_tokens"`
	RequestCount  int64   `json:"request_count" db:"total_requests"`
	EstimatedCost float64 `json:"estimated_cost_usd" db:"total_cost"`
}
