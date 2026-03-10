package llm

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"strings"
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
	case ProviderGeminiOAuth:
		return newGeminiOAuthProvider(config)
	case ProviderOpenAI:
		return newOpenAIProvider(config)
	case ProviderOpenAIOAuth:
		return newOpenAIOAuthProvider(config)
	case ProviderLMStudio:
		return newLMStudioProvider(config)
	case ProviderClaude:
		return nil, fmt.Errorf("Claude API key provider not implemented yet — use claude-oauth instead")
	case ProviderClaudeOAuth:
		return newClaudeOAuthProvider(config)
	case ProviderOllama:
		return newOllamaProvider(config)
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
	case ProviderGeminiOAuth:
		config.Model = database.GetSetting("GEMINI_OAUTH_MODEL", "gemini-2.5-flash")
	case ProviderOpenAIOAuth:
		config.Model = database.GetSetting("OPENAI_OAUTH_MODEL", "gpt-4o")
	case ProviderClaudeOAuth:
		config.Model = database.GetSetting("CLAUDE_OAUTH_MODEL", "claude-sonnet-4-20250514")
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
	case ProviderGeminiOAuth, ProviderOpenAIOAuth, ProviderClaudeOAuth:
		// OAuth credentials проверяются при создании адаптера, не здесь
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

// newGeminiOAuthProvider создаёт Gemini провайдера через Cloud Code Assist OAuth
func newGeminiOAuthProvider(config *ProviderConfig) (Provider, error) {
	model := config.Model
	if model == "" {
		model = "gemini-2.5-flash"
	}

	adapter := NewCloudCodeAdapter(model, GetDefaultTimeout(config))
	log.Printf("[PROVIDER_FACTORY] Gemini OAuth provider created: model=%s", model)
	return adapter, nil
}

// newOpenAIOAuthProvider создаёт ChatGPT провайдера через Codex OAuth
func newOpenAIOAuthProvider(config *ProviderConfig) (Provider, error) {
	model := config.Model
	if model == "" {
		model = "gpt-4o"
	}

	adapter := NewOpenAICodexAdapter(model, GetDefaultTimeout(config))
	log.Printf("[PROVIDER_FACTORY] OpenAI OAuth provider created: model=%s", model)
	return adapter, nil
}

// newClaudeOAuthProvider создаёт Claude провайдера через Anthropic OAuth
func newClaudeOAuthProvider(config *ProviderConfig) (Provider, error) {
	model := config.Model
	if model == "" {
		model = "claude-sonnet-4-20250514"
	}

	adapter := NewClaudeOAuthAdapter(model, GetDefaultTimeout(config))
	log.Printf("[PROVIDER_FACTORY] Claude OAuth provider created: model=%s", model)
	return adapter, nil
}

// newOllamaProvider создаёт Ollama провайдера (через OpenAI-совместимый API)
func newOllamaProvider(config *ProviderConfig) (Provider, error) {
	if err := ValidateConfig(config); err != nil {
		return nil, fmt.Errorf("invalid Ollama config: %w", err)
	}

	// Ollama OpenAI-compatible endpoint: {baseURL}/v1/chat/completions
	// Пользователь указывает http://localhost:11434, мы добавляем /v1
	baseURL := config.BaseURL
	if !strings.HasSuffix(baseURL, "/v1") {
		baseURL = strings.TrimRight(baseURL, "/") + "/v1"
	}

	adapter := NewOpenAIAdapter(
		baseURL,
		"", // Ollama не требует API key
		config.Model,
		GetDefaultTimeout(config),
	)

	if err := adapter.Initialize(); err != nil {
		return nil, fmt.Errorf("failed to initialize Ollama adapter: %w", err)
	}

	log.Printf("[PROVIDER_FACTORY] Ollama provider created: model=%s, baseURL=%s", config.Model, baseURL)
	return adapter, nil
}

// ============================================================================
// ROLE-BASED PROVIDER SYSTEM
// ============================================================================
//
// Каждая роль (translator, responder, director) может иметь свой LLM провайдер.
// Настройки хранятся в БД: {ROLE}_PROVIDER, {ROLE}_MODEL, {ROLE}_BASE_URL, {ROLE}_API_KEY
// Если роль не настроена — используется глобальный провайдер.

// ProviderRole определяет роль провайдера в системе
type ProviderRole string

const (
	RoleTranslator ProviderRole = "TRANSLATOR" // Перевод сообщений (маленькая быстрая модель)
	RoleResponder  ProviderRole = "RESPONDER"  // Авто-ответчик (средняя модель)
	RoleDirector   ProviderRole = "DIRECTOR"   // Директор/аналитик (большая облачная модель)
)

// RoleConfig — конфигурация провайдера для конкретной роли
type RoleConfig struct {
	Provider string `json:"provider"` // Тип провайдера ("gemini", "ollama", etc.) или "" = global
	Model    string `json:"model"`    // Модель или "" = default модель провайдера
	BaseURL  string `json:"baseUrl"`  // Кастомный URL или "" = default URL провайдера
	APIKey   string `json:"apiKey"`   // API key или "" = из глобальных настроек провайдера
}

// NewProviderForRole создаёт провайдера для конкретной роли.
// Приоритет: настройки роли в БД → глобальный провайдер.
func NewProviderForRole(role ProviderRole) (Provider, error) {
	prefix := string(role)

	// Читаем провайдер роли из БД
	providerType := database.GetSetting(prefix+"_PROVIDER", "")
	if providerType == "" {
		// Роль не настроена — используем глобальный провайдер
		global := GetGlobalProvider()
		if global == nil {
			return nil, fmt.Errorf("no provider configured for role %s and no global provider", role)
		}
		log.Printf("[PROVIDER_ROLES] %s: using global provider", role)
		return global, nil
	}

	// Читаем настройки роли
	model := database.GetSetting(prefix+"_MODEL", "")
	baseURL := database.GetSetting(prefix+"_BASE_URL", "")
	apiKey := database.GetSetting(prefix+"_API_KEY", "")

	// Если модель/URL/ключ не указаны — берём из глобальных настроек этого провайдера
	config := &ProviderConfig{
		Type:    ProviderType(providerType),
		Timeout: database.GetSettingInt("LLM_API_TIMEOUT", 30),
	}

	switch config.Type {
	case ProviderGemini:
		config.APIKey = firstNonEmpty(apiKey, database.GetSetting("GEMINI_API_KEY", ""))
		config.Model = firstNonEmpty(model, database.GetSetting("GEMINI_MODEL", "gemini-2.5-flash"))
	case ProviderGeminiOAuth:
		config.Model = firstNonEmpty(model, database.GetSetting("GEMINI_OAUTH_MODEL", "gemini-2.5-flash"))
	case ProviderOpenAIOAuth:
		config.Model = firstNonEmpty(model, database.GetSetting("OPENAI_OAUTH_MODEL", "gpt-4o"))
	case ProviderClaudeOAuth:
		config.Model = firstNonEmpty(model, database.GetSetting("CLAUDE_OAUTH_MODEL", "claude-sonnet-4-20250514"))
	case ProviderOpenAI:
		config.APIKey = firstNonEmpty(apiKey, database.GetSetting("OPENAI_API_KEY", ""))
		config.Model = firstNonEmpty(model, database.GetSetting("OPENAI_MODEL", "gpt-4o-mini"))
		config.BaseURL = firstNonEmpty(baseURL, database.GetSetting("OPENAI_BASE_URL", "https://api.openai.com/v1"))
	case ProviderLMStudio:
		config.APIKey = firstNonEmpty(apiKey, database.GetSetting("LMSTUDIO_API_KEY", ""))
		config.Model = firstNonEmpty(model, database.GetSetting("LMSTUDIO_MODEL", "local-model"))
		config.BaseURL = firstNonEmpty(baseURL, database.GetSetting("LMSTUDIO_BASE_URL", "http://localhost:1234/v1"))
	case ProviderClaude:
		config.APIKey = firstNonEmpty(apiKey, database.GetSetting("ANTHROPIC_API_KEY", ""))
		config.Model = firstNonEmpty(model, database.GetSetting("CLAUDE_MODEL", "claude-3-5-sonnet-20241022"))
	case ProviderOllama:
		config.Model = firstNonEmpty(model, database.GetSetting("OLLAMA_MODEL", "llama3.1"))
		config.BaseURL = firstNonEmpty(baseURL, database.GetSetting("OLLAMA_BASE_URL", "http://localhost:11434"))
	default:
		return nil, fmt.Errorf("unknown provider type for role %s: %s", role, providerType)
	}

	provider, err := NewProvider(config)
	if err != nil {
		return nil, fmt.Errorf("create provider for role %s: %w", role, err)
	}

	log.Printf("[PROVIDER_ROLES] %s: dedicated provider created — %s (model=%s)", role, providerType, config.Model)
	return provider, nil
}

// GetRoleConfig возвращает текущую конфигурацию роли из БД
func GetRoleConfig(role ProviderRole) RoleConfig {
	prefix := string(role)
	return RoleConfig{
		Provider: database.GetSetting(prefix+"_PROVIDER", ""),
		Model:    database.GetSetting(prefix+"_MODEL", ""),
		BaseURL:  database.GetSetting(prefix+"_BASE_URL", ""),
		APIKey:   database.GetSetting(prefix+"_API_KEY", ""),
	}
}

// SaveRoleConfig сохраняет конфигурацию роли в БД
func SaveRoleConfig(role ProviderRole, cfg RoleConfig) error {
	prefix := string(role)
	desc := fmt.Sprintf("%s role config", role)

	if err := database.SetSetting(prefix+"_PROVIDER", cfg.Provider, desc+" provider"); err != nil {
		return err
	}
	if err := database.SetSetting(prefix+"_MODEL", cfg.Model, desc+" model"); err != nil {
		return err
	}
	if err := database.SetSetting(prefix+"_BASE_URL", cfg.BaseURL, desc+" base URL"); err != nil {
		return err
	}
	// API key — только если не замаскирован
	if cfg.APIKey != "" && !isMaskedKey(cfg.APIKey) {
		if err := database.SetSetting(prefix+"_API_KEY", cfg.APIKey, desc+" API key"); err != nil {
			return err
		}
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func isMaskedKey(key string) bool {
	return strings.Contains(key, "***")
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

