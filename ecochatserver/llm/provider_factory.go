package llm

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/egor/ecochatserver/database"
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
		return newOpenAIProvider(config)
	case ProviderLMStudio:
		return newLMStudioProvider(config)
	case ProviderClaude:
		return nil, fmt.Errorf("Claude provider not implemented yet")
	case ProviderOllama:
		return nil, fmt.Errorf("Ollama provider not implemented yet")
	default:
		return nil, fmt.Errorf("unknown provider type: %s", config.Type)
	}
}

// LoadConfigFromEnv загружает конфигурацию из БД (приоритет) или переменных окружения
// Приоритет: 1) ENV переменная, 2) БД, 3) дефолтное значение
func LoadConfigFromEnv() *ProviderConfig {
	config := &ProviderConfig{
		Type:    ProviderDefault,
		Timeout: 30, // по умолчанию 30 секунд
	}

	// Читаем тип провайдера из БД (с fallback на ENV и дефолт)
	providerTypeStr := database.GetSetting("LLM_PROVIDER", string(ProviderDefault))
	config.Type = ProviderType(providerTypeStr)

	log.Printf("[PROVIDER_FACTORY] Loading config: provider=%s", config.Type)

	// Читаем API ключ в зависимости от провайдера (из БД с fallback на ENV)
	switch config.Type {
	case ProviderGemini:
		config.APIKey = database.GetSetting("GEMINI_API_KEY", "")
		config.Model = database.GetSetting("GEMINI_MODEL", "gemini-2.5-flash")
	case ProviderOpenAI:
		config.APIKey = database.GetSetting("OPENAI_API_KEY", "")
		config.Model = database.GetSetting("OPENAI_MODEL", "gpt-4o-mini")
		config.BaseURL = database.GetSetting("OPENAI_BASE_URL", "https://api.openai.com/v1")
	case ProviderLMStudio:
		config.APIKey = database.GetSetting("LMSTUDIO_API_KEY", "") // опционален для LM Studio
		config.BaseURL = database.GetSetting("LMSTUDIO_BASE_URL", "http://localhost:1234/v1")
		config.Model = database.GetSetting("LMSTUDIO_MODEL", "local-model")
	case ProviderClaude:
		config.APIKey = database.GetSetting("ANTHROPIC_API_KEY", "")
		config.Model = database.GetSetting("CLAUDE_MODEL", "claude-3-5-sonnet-20241022")
	case ProviderOllama:
		config.BaseURL = database.GetSetting("OLLAMA_BASE_URL", "http://localhost:11434")
		config.Model = database.GetSetting("OLLAMA_MODEL", "llama3.1")
	}

	// Читаем таймаут из БД
	config.Timeout = database.GetSettingInt("LLM_API_TIMEOUT", 30)

	log.Printf("[PROVIDER_FACTORY] Config loaded: type=%s, model=%s, timeout=%ds",
		config.Type, config.Model, config.Timeout)

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
	case ProviderOllama, ProviderLMStudio:
		if config.BaseURL == "" {
			return fmt.Errorf("base URL is required for %s provider", config.Type)
		}
		// API key опционален для локальных провайдеров
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

// newOpenAIProvider создаёт OpenAI провайдера
func newOpenAIProvider(config *ProviderConfig) (Provider, error) {
	if err := ValidateConfig(config); err != nil {
		return nil, fmt.Errorf("invalid OpenAI config: %w", err)
	}

	adapter := NewOpenAIAdapter(
		config.BaseURL,
		config.APIKey,
		config.Model,
		GetDefaultTimeout(config),
	)

	if err := adapter.Initialize(); err != nil {
		return nil, fmt.Errorf("failed to initialize OpenAI adapter: %w", err)
	}

	return adapter, nil
}

// newLMStudioProvider создаёт LM Studio провайдера
func newLMStudioProvider(config *ProviderConfig) (Provider, error) {
	if err := ValidateConfig(config); err != nil {
		return nil, fmt.Errorf("invalid LM Studio config: %w", err)
	}

	// LM Studio использует тот же адаптер что и OpenAI
	adapter := NewOpenAIAdapter(
		config.BaseURL,
		config.APIKey, // может быть пустым для LM Studio
		config.Model,
		GetDefaultTimeout(config),
	)

	if err := adapter.Initialize(); err != nil {
		return nil, fmt.Errorf("failed to initialize LM Studio adapter: %w", err)
	}

	log.Printf("[PROVIDER_FACTORY] LM Studio provider created with base URL: %s", config.BaseURL)
	return adapter, nil
}

// ============================================================================
// GLOBAL PROVIDER with HOT-SWAP support
// ============================================================================

var (
	globalProvider Provider
	providerMutex  sync.RWMutex
)

// GetGlobalProvider возвращает глобальный провайдер (thread-safe)
func GetGlobalProvider() Provider {
	providerMutex.RLock()
	defer providerMutex.RUnlock()
	return globalProvider
}

// SetGlobalProvider устанавливает глобальный провайдер (thread-safe)
func SetGlobalProvider(provider Provider) {
	providerMutex.Lock()
	defer providerMutex.Unlock()
	globalProvider = provider
	log.Printf("[PROVIDER_FACTORY] 🔄 Global provider updated: %s", provider.GetName())
}

// InitializeGlobalProvider инициализирует глобальный провайдер
// Сначала пытается загрузить из БД, если не удалось - из env
func InitializeGlobalProvider(db *sql.DB) error {
	log.Printf("[PROVIDER_FACTORY] Initializing global provider...")

	// Пытаемся загрузить из БД
	if db != nil {
		err := ReloadProviderFromDB(db)
		if err == nil {
			log.Printf("[PROVIDER_FACTORY] ✅ Provider loaded from database")
			return nil
		}
		log.Printf("[PROVIDER_FACTORY] ⚠️ Failed to load from DB: %v, falling back to env", err)
	}

	// Fallback на env переменные
	config := LoadConfigFromEnv()
	provider, err := NewProvider(config)
	if err != nil {
		return fmt.Errorf("failed to create provider from env: %w", err)
	}

	SetGlobalProvider(provider)
	log.Printf("[PROVIDER_FACTORY] ✅ Provider loaded from environment: %s", provider.GetName())
	return nil
}

// ReloadProviderFromDB перезагружает провайдера из БД (HOT-SWAP)
func ReloadProviderFromDB(db *sql.DB) error {
	log.Printf("[PROVIDER_FACTORY] 🔄 Reloading provider from database...")

	// Используем database.GetSetting для чтения настроек из app_settings
	// Это работает так же как LoadConfigFromEnv(), но явно указывает что читаем из БД
	database.InvalidateSettingsCache() // Сбрасываем кэш перед чтением

	config := LoadConfigFromEnv() // Это уже читает из БД через database.GetSetting()

	// Создаем нового провайдера
	provider, err := NewProvider(config)
	if err != nil {
		return fmt.Errorf("failed to create provider: %w", err)
	}

	// Устанавливаем как глобального
	SetGlobalProvider(provider)

	log.Printf("[PROVIDER_FACTORY] 🔥 HOT-SWAP completed: %s (provider=%s, model=%s)",
		provider.GetName(), config.Type, config.Model)
	return nil
}

// ProviderConfigFromDB создает конфигурацию провайдера из БД настроек (для тестирования)
func ProviderConfigFromDB(settings interface{}, providerType string) *ProviderConfig {
	// Эта функция используется в llm_settings_handler.go для тестирования
	// Принимает *queries.LLMSettings но мы не можем импортировать queries здесь (циклическая зависимость)
	// Поэтому используем interface{} и type assertion

	// Используем рефлексию для получения полей из settings
	// Или создаем простую структуру для передачи данных

	// Для простоты, пока что возвращаем nil и обработаем это в handler
	return nil
}
