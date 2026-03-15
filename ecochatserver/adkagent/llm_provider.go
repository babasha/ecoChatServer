package adkagent

import (
	"context"
	"iter"
	"log"
	"os"
	"time"

	"google.golang.org/adk/model"
	"google.golang.org/adk/model/gemini"
	"google.golang.org/adk/model/openai"
	"google.golang.org/genai"

	"github.com/egor/ecochatserver/database"
	"github.com/egor/ecochatserver/llm"
)

// LLMProviderType определяет тип LLM провайдера
type LLMProviderType string

const (
	ProviderGemini      LLMProviderType = "gemini"
	ProviderOpenAI      LLMProviderType = "openai"
	ProviderLMStudio    LLMProviderType = "lmstudio"
	ProviderMock        LLMProviderType = "mock"

	// Providers available via ProviderAdapter (llm.Provider → ADK model.LLM)
	ProviderClaude      LLMProviderType = "claude"
	ProviderClaudeOAuth LLMProviderType = "claude-oauth"
	ProviderGeminiOAuth LLMProviderType = "gemini-oauth"
	ProviderOpenAIOAuth LLMProviderType = "openai-oauth"
	ProviderOllamaADK   LLMProviderType = "ollama"
)

// LLMConfig содержит конфигурацию для LLM провайдера
type LLMConfig struct {
	Provider LLMProviderType
	APIKey   string
	BaseURL  string
	Model    string
}

// LoadLLMConfig загружает конфигурацию LLM из переменных окружения и БД
// Приоритет: 1) RESPONDER_* настройки, 2) глобальные настройки LLM_PROVIDER, 3) дефолты
func LoadLLMConfig() *LLMConfig {
	// Сначала проверяем отдельную конфигурацию для авто-ответчика (RESPONDER_*)
	responderProvider := database.GetSetting("RESPONDER_PROVIDER", "")
	if responderProvider != "" {
		config := loadResponderConfig(responderProvider)
		if config != nil {
			return config
		}
	}

	// Fallback: используем глобальный провайдер
	providerStr := database.GetSetting("LLM_PROVIDER", string(ProviderGemini))
	provider := LLMProviderType(providerStr)

	config := &LLMConfig{
		Provider: provider,
	}

	switch provider {
	case ProviderGemini:
		config.APIKey = database.GetSetting("GEMINI_API_KEY", "")
		config.Model = database.GetSetting("GEMINI_MODEL", "gemini-2.5-flash")
		log.Printf("[ADK_LLM] Загружена конфигурация Gemini: model=%s", config.Model)

	case ProviderOpenAI:
		config.APIKey = database.GetSetting("OPENAI_API_KEY", "")
		config.BaseURL = database.GetSetting("OPENAI_BASE_URL", "https://api.openai.com/v1")
		config.Model = database.GetSetting("OPENAI_MODEL", "gpt-4o-mini")
		log.Printf("[ADK_LLM] Загружена конфигурация OpenAI: model=%s, baseURL=%s", config.Model, config.BaseURL)

	case ProviderLMStudio:
		config.BaseURL = database.GetSetting("LMSTUDIO_BASE_URL", "http://127.0.0.1:1234/v1")
		config.APIKey = database.GetSetting("LMSTUDIO_API_KEY", "not-needed")
		config.Model = database.GetSetting("LMSTUDIO_MODEL", "local-model")

		isDev := os.Getenv("ENVIRONMENT") != "production"
		if isDev {
			log.Printf("[ADK_LLM] 🏠 Режим разработки: LM Studio")
		} else {
			log.Printf("[ADK_LLM] ☁️ Режим продакшена: LM Studio через %s", config.BaseURL)
		}
		log.Printf("[ADK_LLM] Загружена конфигурация LM Studio: model=%s, baseURL=%s", config.Model, config.BaseURL)

	default:
		log.Printf("[ADK_LLM] ⚠️ Неизвестный провайдер %s, использую Mock", provider)
		config.Provider = ProviderMock
	}

	return config
}

// loadResponderConfig загружает конфигурацию из RESPONDER_* настроек
func loadResponderConfig(providerType string) *LLMConfig {
	provider := LLMProviderType(providerType)
	config := &LLMConfig{
		Provider: provider,
	}

	// Модель из RESPONDER_MODEL, fallback на дефолт провайдера
	responderModel := database.GetSetting("RESPONDER_MODEL", "")
	responderBaseURL := database.GetSetting("RESPONDER_BASE_URL", "")
	responderAPIKey := database.GetSetting("RESPONDER_API_KEY", "")

	switch provider {
	case ProviderGemini:
		config.APIKey = firstNonEmpty(responderAPIKey, database.GetSetting("GEMINI_API_KEY", ""))
		config.Model = firstNonEmpty(responderModel, database.GetSetting("GEMINI_MODEL", "gemini-2.5-flash"))
		log.Printf("[ADK_LLM] RESPONDER: Gemini model=%s", config.Model)

	case ProviderOpenAI:
		config.APIKey = firstNonEmpty(responderAPIKey, database.GetSetting("OPENAI_API_KEY", ""))
		config.BaseURL = firstNonEmpty(responderBaseURL, database.GetSetting("OPENAI_BASE_URL", "https://api.openai.com/v1"))
		config.Model = firstNonEmpty(responderModel, database.GetSetting("OPENAI_MODEL", "gpt-4o-mini"))
		log.Printf("[ADK_LLM] RESPONDER: OpenAI model=%s, baseURL=%s", config.Model, config.BaseURL)

	case ProviderLMStudio:
		config.APIKey = firstNonEmpty(responderAPIKey, database.GetSetting("LMSTUDIO_API_KEY", "not-needed"))
		config.BaseURL = firstNonEmpty(responderBaseURL, database.GetSetting("LMSTUDIO_BASE_URL", "http://127.0.0.1:1234/v1"))
		config.Model = firstNonEmpty(responderModel, database.GetSetting("LMSTUDIO_MODEL", "local-model"))
		log.Printf("[ADK_LLM] RESPONDER: LM Studio model=%s, baseURL=%s", config.Model, config.BaseURL)

	case ProviderOllamaADK:
		// Ollama через ProviderAdapter (llm.Provider → ADK model.LLM)
		config.Provider = ProviderOllamaADK
		config.BaseURL = firstNonEmpty(responderBaseURL, database.GetSetting("OLLAMA_BASE_URL", "http://localhost:11434"))
		config.Model = firstNonEmpty(responderModel, database.GetSetting("OLLAMA_MODEL", "llama3.1"))
		log.Printf("[ADK_LLM] RESPONDER: Ollama (via adapter) model=%s, baseURL=%s", config.Model, config.BaseURL)

	case ProviderClaude:
		config.Provider = ProviderClaude
		config.APIKey = firstNonEmpty(responderAPIKey, database.GetSetting("ANTHROPIC_API_KEY", ""))
		config.Model = firstNonEmpty(responderModel, database.GetSetting("CLAUDE_MODEL", "claude-sonnet-4-20250514"))
		log.Printf("[ADK_LLM] RESPONDER: Claude (via adapter) model=%s", config.Model)

	case ProviderClaudeOAuth:
		config.Provider = ProviderClaudeOAuth
		config.Model = firstNonEmpty(responderModel, database.GetSetting("CLAUDE_OAUTH_MODEL", "claude-sonnet-4-20250514"))
		log.Printf("[ADK_LLM] RESPONDER: Claude OAuth (via adapter) model=%s", config.Model)

	case ProviderGeminiOAuth:
		config.Provider = ProviderGeminiOAuth
		config.Model = firstNonEmpty(responderModel, database.GetSetting("GEMINI_OAUTH_MODEL", "gemini-2.5-flash"))
		log.Printf("[ADK_LLM] RESPONDER: Gemini OAuth (via adapter) model=%s", config.Model)

	case ProviderOpenAIOAuth:
		config.Provider = ProviderOpenAIOAuth
		config.Model = firstNonEmpty(responderModel, database.GetSetting("OPENAI_OAUTH_MODEL", "gpt-5.2-codex"))
		log.Printf("[ADK_LLM] RESPONDER: OpenAI OAuth (via adapter) model=%s", config.Model)

	default:
		log.Printf("[ADK_LLM] ⚠️ RESPONDER: Неизвестный провайдер %s, fallback на глобальный", providerType)
		return nil
	}

	return config
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// NewLLMModel создаёт LLM модель для ADK на основе конфигурации.
// Поддерживает hot-swap через переменные окружения и БД.
//
// Native ADK models: Gemini, OpenAI, LM Studio (через ADK model.LLM напрямую)
// Adapted models: Claude, Ollama, OAuth providers (через ProviderAdapter: llm.Provider → ADK model.LLM)
func NewLLMModel(ctx context.Context) (model.LLM, error) {
	config := LoadLLMConfig()

	switch config.Provider {
	// --- Native ADK models ---
	case ProviderGemini:
		return newGeminiModel(ctx, config)

	case ProviderOpenAI:
		return newOpenAIModel(ctx, config)

	case ProviderLMStudio:
		return newLMStudioModel(ctx, config)

	// --- Adapted models (via ProviderAdapter: llm.Provider → ADK model.LLM) ---
	case ProviderClaude, ProviderClaudeOAuth,
		ProviderGeminiOAuth, ProviderOpenAIOAuth,
		ProviderOllamaADK:
		return newAdaptedModel(config)

	case ProviderMock:
		log.Println("[ADK_LLM] Используем MockLLM для тестирования")
		return &MockLLM{}, nil

	default:
		log.Printf("[ADK_LLM] ⚠️ Неподдерживаемый провайдер %s, fallback на Mock", config.Provider)
		return &MockLLM{}, nil
	}
}

// newAdaptedModel creates an ADK model.LLM by wrapping an llm.Provider via ProviderAdapter.
// This enables the РОП to use Claude, Ollama, and OAuth-based providers.
func newAdaptedModel(config *LLMConfig) (model.LLM, error) {
	// Map adkagent LLMProviderType → llm.ProviderType
	providerType := mapToLLMProviderType(config.Provider)

	providerConfig := &llm.ProviderConfig{
		Type:   providerType,
		APIKey: config.APIKey,
		Model:  config.Model,
	}
	if config.BaseURL != "" {
		providerConfig.BaseURL = config.BaseURL
	}

	provider, err := llm.NewProvider(providerConfig)
	if err != nil {
		log.Printf("[ADK_LLM] ❌ Ошибка создания %s провайдера: %v, fallback на Mock", config.Provider, err)
		return &MockLLM{}, nil
	}

	adapter := NewProviderAdapter(provider, config.Model)
	log.Printf("[ADK_LLM] ✅ %s модель инициализирована через ProviderAdapter: %s",
		config.Provider, config.Model)
	return adapter, nil
}

// mapToLLMProviderType converts adkagent.LLMProviderType → llm.ProviderType.
func mapToLLMProviderType(adkType LLMProviderType) llm.ProviderType {
	switch adkType {
	case ProviderClaude:
		return llm.ProviderClaude
	case ProviderClaudeOAuth:
		return llm.ProviderClaudeOAuth
	case ProviderGeminiOAuth:
		return llm.ProviderGeminiOAuth
	case ProviderOpenAIOAuth:
		return llm.ProviderOpenAIOAuth
	case ProviderOllamaADK:
		return llm.ProviderOllama
	default:
		return llm.ProviderType(adkType)
	}
}

// newGeminiModel создаёт Gemini модель
func newGeminiModel(ctx context.Context, config *LLMConfig) (model.LLM, error) {
	if config.APIKey == "" {
		log.Println("[ADK_LLM] ⚠️ GEMINI_API_KEY не установлен, используем MockLLM")
		return &MockLLM{}, nil
	}

	geminiModel, err := gemini.NewModel(ctx, config.Model, &genai.ClientConfig{
		APIKey: config.APIKey,
	})
	if err != nil {
		log.Printf("[ADK_LLM] ❌ Ошибка создания Gemini модели: %v, fallback на Mock", err)
		return &MockLLM{}, nil
	}

	log.Printf("[ADK_LLM] ✅ Gemini модель инициализирована: %s", config.Model)
	return geminiModel, nil
}

// newOpenAIModel создаёт OpenAI модель через форкнутый ADK
func newOpenAIModel(ctx context.Context, config *LLMConfig) (model.LLM, error) {
	if config.APIKey == "" {
		log.Println("[ADK_LLM] ⚠️ OPENAI_API_KEY не установлен, используем MockLLM")
		return &MockLLM{}, nil
	}

	// Используем новый openai.NewModel() из форка
	openaiModel, err := openai.NewModel(config.Model, &openai.Config{
		BaseURL: config.BaseURL,
		APIKey:  config.APIKey,
	})
	if err != nil {
		log.Printf("[ADK_LLM] ❌ Ошибка создания OpenAI модели: %v, fallback на Mock", err)
		return &MockLLM{}, nil
	}

	log.Printf("[ADK_LLM] ✅ OpenAI модель инициализирована: %s (base: %s)", config.Model, config.BaseURL)
	return openaiModel, nil
}

// newLMStudioModel создаёт LM Studio модель (через OpenAI API)
func newLMStudioModel(ctx context.Context, config *LLMConfig) (model.LLM, error) {
	// Включаем debug logging для разработки
	debugMode := os.Getenv("LLM_DEBUG") == "true" || os.Getenv("ENVIRONMENT") != "production"

	// LM Studio совместим с OpenAI API, используем openai.NewModel()
	// Timeout увеличен: reasoning модели (Qwen 3.5) генерируют <think> блок перед ответом
	lmStudioModel, err := openai.NewModel(config.Model, &openai.Config{
		BaseURL:      config.BaseURL,
		APIKey:       config.APIKey,
		DebugLogging: debugMode,
		Logger:       log.Default(),
		Timeout:      3 * time.Minute,
	})
	if err != nil {
		log.Printf("[ADK_LLM] ❌ Ошибка создания LM Studio модели: %v, fallback на Mock", err)
		return &MockLLM{}, nil
	}

	log.Printf("[ADK_LLM] ✅ LM Studio модель инициализирована: %s (base: %s, debug=%v)", config.Model, config.BaseURL, debugMode)
	log.Printf("[ADK_LLM] 💡 Убедитесь что LM Studio запущен на %s", config.BaseURL)
	return lmStudioModel, nil
}

// MockLLM - заглушка для тестирования и fallback
type MockLLM struct{}

func (m *MockLLM) Name() string {
	return "mock-llm"
}

func (m *MockLLM) GenerateContent(
	ctx context.Context,
	req *model.LLMRequest,
	stream bool,
) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		log.Printf("[MockLLM] 🎭 Получен запрос, возвращаю тестовый ответ")

		textContent := genai.Text(`Привет! Это тестовый ответ от MockLLM.

📋 Информация:
- Для продакшена настройте LLM провайдер в переменных окружения или БД
- Доступные провайдеры: gemini, openai, lmstudio
- Для LM Studio: запустите сервер на http://127.0.0.1:1234

🔧 Пример конфигурации:
export LLM_PROVIDER=lmstudio
export LMSTUDIO_BASE_URL=http://127.0.0.1:1234/v1
export LMSTUDIO_MODEL=your-model-name`)

		response := &model.LLMResponse{
			Content:      textContent[0],
			TurnComplete: true,
		}
		yield(response, nil)
	}
}
