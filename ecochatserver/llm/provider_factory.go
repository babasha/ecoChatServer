package llm

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"time"
)

// ============================================================================
// PROVIDER FACTORY - Creates providers based on configuration
// ============================================================================

// NewProvider создаёт провайдера на основе конфигурации
func NewProvider(config *ProviderConfig) (Provider, error) {
	if config == nil {
		// Используем конфигурацию по умолчанию из переменных окружения
		config = LoadConfigFromEnv()
	}

	log.Printf("[PROVIDER_FACTORY] Creating provider: type=%s, model=%s", config.Type, config.Model)

	switch config.Type {
	case ProviderGemini:
		return newGeminiProvider(config)
	case ProviderOpenAI:
		return nil, fmt.Errorf("OpenAI provider not implemented yet")
	case ProviderClaude:
		return nil, fmt.Errorf("Claude provider not implemented yet")
	case ProviderOllama:
		return nil, fmt.Errorf("Ollama provider not implemented yet")
	default:
		return nil, fmt.Errorf("unknown provider type: %s", config.Type)
	}
}

// LoadConfigFromEnv загружает конфигурацию из переменных окружения
func LoadConfigFromEnv() *ProviderConfig {
	config := &ProviderConfig{
		Type:    ProviderDefault,
		Timeout: 30, // по умолчанию 30 секунд
	}

	// Читаем тип провайдера
	if providerType := os.Getenv("LLM_PROVIDER"); providerType != "" {
		config.Type = ProviderType(providerType)
	}

	// Читаем API ключ в зависимости от провайдера
	switch config.Type {
	case ProviderGemini:
		config.APIKey = os.Getenv("GEMINI_API_KEY")
		config.Model = getEnvOrDefault("GEMINI_MODEL", "gemini-2.5-flash")
	case ProviderOpenAI:
		config.APIKey = os.Getenv("OPENAI_API_KEY")
		config.Model = getEnvOrDefault("OPENAI_MODEL", "gpt-4o-mini")
	case ProviderClaude:
		config.APIKey = os.Getenv("ANTHROPIC_API_KEY")
		config.Model = getEnvOrDefault("CLAUDE_MODEL", "claude-3-5-sonnet-20241022")
	case ProviderOllama:
		config.BaseURL = getEnvOrDefault("OLLAMA_BASE_URL", "http://localhost:11434")
		config.Model = getEnvOrDefault("OLLAMA_MODEL", "llama3.1")
	}

	// Читаем таймаут
	if timeoutStr := os.Getenv("LLM_API_TIMEOUT"); timeoutStr != "" {
		if timeout, err := strconv.Atoi(timeoutStr); err == nil {
			config.Timeout = timeout
		}
	}

	// Читаем кастомный base URL (если указан)
	if baseURL := os.Getenv("LLM_BASE_URL"); baseURL != "" {
		config.BaseURL = baseURL
	}

	return config
}

// getEnvOrDefault возвращает значение переменной окружения или значение по умолчанию
func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// GetDefaultTimeout возвращает таймаут по умолчанию
func GetDefaultTimeout(config *ProviderConfig) time.Duration {
	if config != nil && config.Timeout > 0 {
		return time.Duration(config.Timeout) * time.Second
	}
	return 30 * time.Second
}

// ValidateConfig проверяет корректность конфигурации
func ValidateConfig(config *ProviderConfig) error {
	if config == nil {
		return fmt.Errorf("config is nil")
	}

	switch config.Type {
	case ProviderGemini, ProviderOpenAI, ProviderClaude:
		if config.APIKey == "" {
			return fmt.Errorf("API key is required for %s provider", config.Type)
		}
	case ProviderOllama:
		if config.BaseURL == "" {
			return fmt.Errorf("base URL is required for Ollama provider")
		}
	default:
		return fmt.Errorf("unknown provider type: %s", config.Type)
	}

	return nil
}

// ============================================================================
// HELPER FUNCTIONS
// ============================================================================

// newGeminiProvider создаёт Gemini провайдера
func newGeminiProvider(config *ProviderConfig) (Provider, error) {
	if err := ValidateConfig(config); err != nil {
		return nil, fmt.Errorf("invalid Gemini config: %w", err)
	}

	// Создаём адаптер для Gemini (будет реализован в следующем файле)
	adapter := &GeminiAdapter{
		apiKey:  config.APIKey,
		model:   config.Model,
		timeout: GetDefaultTimeout(config),
	}

	if err := adapter.Initialize(); err != nil {
		return nil, fmt.Errorf("failed to initialize Gemini adapter: %w", err)
	}

	return adapter, nil
}
