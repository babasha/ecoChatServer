package llm

import (
	"context"
	"fmt"
	"log"
	"time"
)

// withRetry выполняет fn до maxRetries раз с экспоненциальной задержкой.
// Между попытками проверяет ctx.Done(). name используется для логирования.
func withRetry(ctx context.Context, maxRetries int, name string, fn func(attempt int) error) error {
	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		lastErr = fn(attempt)
		if lastErr == nil {
			if attempt > 1 {
				log.Printf("%s: успех после %d попыток", name, attempt)
			}
			return nil
		}

		if attempt < maxRetries {
			log.Printf("%s: попытка %d/%d провалилась: %v, повтор через %d сек...", name, attempt, maxRetries, lastErr, attempt)

			select {
			case <-ctx.Done():
				return fmt.Errorf("%s: context cancelled: %w", name, ctx.Err())
			case <-time.After(time.Duration(attempt) * time.Second):
			}
		}
	}

	return fmt.Errorf("%s failed after %d attempts: %w", name, maxRetries, lastErr)
}

// logGeminiUsage логирует использование токенов из GeminiResponse.
func logGeminiUsage(ctx context.Context, resp *GeminiResponse, model, requestType string) {
	if resp == nil || resp.UsageMetadata == nil {
		return
	}
	_ = LogUsage(ctx, UsageLogEntry{
		Provider:         "gemini",
		Model:            model,
		PromptTokens:     resp.UsageMetadata.PromptTokenCount,
		CompletionTokens: resp.UsageMetadata.CandidatesTokenCount,
		TotalTokens:      resp.UsageMetadata.TotalTokenCount,
		RequestType:      requestType,
	})
}
