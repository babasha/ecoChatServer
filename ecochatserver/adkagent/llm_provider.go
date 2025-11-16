package adkagent

import (
	"context"
	"iter"
	"log"
	"os"

	"google.golang.org/adk/model"
	"google.golang.org/adk/model/gemini"
	"google.golang.org/genai"

	"github.com/egor/ecochatserver/database"
)

// LLMProviderType определяет тип LLM провайдера
type LLMProviderType string

const (
	ProviderGemini   LLMProviderType = "gemini"
	ProviderOpenAI   LLMProviderType = "openai"
	ProviderLMStudio LLMProviderType = "lmstudio"
	ProviderMock     LLMProviderType = "mock"
)

// LLMConfig содержит конфигурацию для LLM провайдера
type LLMConfig struct {
	Provider LLMProviderType
	APIKey   string
	BaseURL  string
	Model    string
}

// LoadLLMConfig загружает конфигурацию LLM из переменных окружения и БД
// Приоритет: 1) ENV переменные, 2) БД, 3) дефолты
func LoadLLMConfig() *LLMConfig {
	// Определяем провайдера
	providerStr := database.GetSetting("LLM_PROVIDER", string(ProviderGemini))
	provider := LLMProviderType(providerStr)

	config := &LLMConfig{
		Provider: provider,
	}

	switch provider {
	case ProviderGemini:
		config.APIKey = database.GetSetting("GEMINI_API_KEY", "")
		config.Model = database.GetSetting("GEMINI_MODEL", "gemini-2.5-flash") // Updated to latest stable model
		log.Printf("[ADK_LLM] Загружена конфигурация Gemini: model=%s", config.Model)

	case ProviderOpenAI:
		config.APIKey = database.GetSetting("OPENAI_API_KEY", "")
		config.BaseURL = database.GetSetting("OPENAI_BASE_URL", "https://api.openai.com/v1")
		config.Model = database.GetSetting("OPENAI_MODEL", "gpt-4o-mini")
		log.Printf("[ADK_LLM] Загружена конфигурация OpenAI: model=%s, baseURL=%s", config.Model, config.BaseURL)

	case ProviderLMStudio:
		// LM Studio: локально через http://127.0.0.1:1234 или через ngrok
		isDev := os.Getenv("ENVIRONMENT") != "production"

		if isDev {
			// Разработка - локальный LM Studio
			config.BaseURL = database.GetSetting("LMSTUDIO_BASE_URL", "http://127.0.0.1:1234/v1")
			log.Printf("[ADK_LLM] 🏠 Режим разработки: использую локальный LM Studio")
		} else {
			// Продакшен - ngrok туннель
			config.BaseURL = database.GetSetting("LMSTUDIO_NGROK_URL", "https://bc3dc5beb47a.ngrok-free.app/v1")
			log.Printf("[ADK_LLM] ☁️ Режим продакшена: использую ngrok туннель")
		}

		config.APIKey = database.GetSetting("LMSTUDIO_API_KEY", "not-needed") // LM Studio не требует ключ
		config.Model = database.GetSetting("LMSTUDIO_MODEL", "local-model")
		log.Printf("[ADK_LLM] Загружена конфигурация LM Studio: model=%s, baseURL=%s", config.Model, config.BaseURL)

	default:
		log.Printf("[ADK_LLM] ⚠️ Неизвестный провайдер %s, использую Mock", provider)
		config.Provider = ProviderMock
	}

	return config
}

// NewLLMModel создаёт LLM модель для ADK на основе конфигурации
// Поддерживает hot-swap через переменные окружения и БД
func NewLLMModel(ctx context.Context) (model.LLM, error) {
	config := LoadLLMConfig()

	switch config.Provider {
	case ProviderGemini:
		return newGeminiModel(ctx, config)

	case ProviderOpenAI:
		return newOpenAIModel(ctx, config)

	case ProviderLMStudio:
		return newLMStudioModel(ctx, config)

	case ProviderMock:
		log.Println("[ADK_LLM] Используем MockLLM для тестирования")
		return &MockLLM{}, nil

	default:
		log.Printf("[ADK_LLM] ⚠️ Неподдерживаемый провайдер %s, fallback на Mock", config.Provider)
		return &MockLLM{}, nil
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

// newOpenAIModel создаёт OpenAI модель через наш адаптер
func newOpenAIModel(ctx context.Context, config *LLMConfig) (model.LLM, error) {
	if config.APIKey == "" {
		log.Println("[ADK_LLM] ⚠️ OPENAI_API_KEY не установлен, используем MockLLM")
		return &MockLLM{}, nil
	}

	adapter := NewOpenAIAdapter(config.BaseURL, config.APIKey, config.Model)
	log.Printf("[ADK_LLM] ✅ OpenAI адаптер инициализирован: %s (base: %s)", config.Model, config.BaseURL)
	return adapter, nil
}

// newLMStudioModel создаёт LM Studio модель (через OpenAI API)
func newLMStudioModel(ctx context.Context, config *LLMConfig) (model.LLM, error) {
	// LM Studio совместим с OpenAI API, используем наш адаптер
	adapter := NewOpenAIAdapter(config.BaseURL, config.APIKey, config.Model)
	log.Printf("[ADK_LLM] ✅ LM Studio адаптер инициализирован: %s (base: %s)", config.Model, config.BaseURL)
	log.Printf("[ADK_LLM] 💡 Убедитесь что LM Studio запущен на %s", config.BaseURL)
	return adapter, nil
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
