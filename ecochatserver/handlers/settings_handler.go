package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/egor/ecochatserver/database"
	"github.com/egor/ecochatserver/llm"
	"github.com/gin-gonic/gin"
)

// ServerSettings представляет настройки сервера
type ServerSettings struct {
	RateLimit struct {
		WindowMs    int `json:"windowMs"`
		MaxRequests int `json:"maxRequests"`
	} `json:"rateLimit"`
	AutoResponder struct {
		Enabled bool   `json:"enabled"`
		Message string `json:"message"`
	} `json:"autoResponder"`
	CORS struct {
		Origins []string `json:"origins"`
	} `json:"cors"`
	Logging struct {
		Level   string `json:"level"`
		Enabled bool   `json:"enabled"`
	} `json:"logging"`
}

// Глобальные настройки сервера
var (
	currentSettings ServerSettings
	settingsMutex   sync.RWMutex
	settingsFile    = "server_settings.json"
)

// InitServerSettings инициализирует настройки сервера
func InitServerSettings() {
	// Устанавливаем значения по умолчанию
	currentSettings.RateLimit.WindowMs = 60000 // 1 минута
	currentSettings.RateLimit.MaxRequests = 100

	currentSettings.AutoResponder.Enabled = false
	currentSettings.AutoResponder.Message = "Спасибо за ваше сообщение! Наш оператор скоро ответит вам."

	currentSettings.CORS.Origins = []string{"http://localhost:3000", "http://localhost:5173"}

	currentSettings.Logging.Level = "info"
	currentSettings.Logging.Enabled = true

	// Пытаемся загрузить настройки из файла
	if err := loadSettingsFromFile(); err != nil {
		log.Printf("InitServerSettings: не удалось загрузить настройки из файла: %v, используем значения по умолчанию", err)
		// Сохраняем дефолтные настройки в файл
		if err := saveSettingsToFile(); err != nil {
			log.Printf("InitServerSettings: не удалось сохранить дефолтные настройки: %v", err)
		}
	} else {
		log.Println("InitServerSettings: настройки успешно загружены из файла")
	}
}

// loadSettingsFromFile загружает настройки из файла
func loadSettingsFromFile() error {
	settingsMutex.Lock()
	defer settingsMutex.Unlock()

	data, err := os.ReadFile(settingsFile)
	if err != nil {
		return err
	}

	return json.Unmarshal(data, &currentSettings)
}

// saveSettingsToFile сохраняет настройки в файл
func saveSettingsToFile() error {
	settingsMutex.RLock()
	data, err := json.MarshalIndent(currentSettings, "", "  ")
	settingsMutex.RUnlock()

	if err != nil {
		return err
	}

	return os.WriteFile(settingsFile, data, 0644)
}

// GetServerSettings возвращает текущие настройки сервера
func GetServerSettings(c *gin.Context) {
	settingsMutex.RLock()
	settings := currentSettings
	settingsMutex.RUnlock()

	c.JSON(http.StatusOK, gin.H{
		"settings": settings,
		"fallback": false,
	})
}

// UpdateServerSettings обновляет настройки сервера
func UpdateServerSettings(c *gin.Context) {
	var newSettings ServerSettings

	if err := c.ShouldBindJSON(&newSettings); err != nil {
		log.Printf("UpdateServerSettings: ошибка парсинга JSON: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Некорректный формат данных",
			"details": err.Error(),
		})
		return
	}

	// Валидация настроек
	if newSettings.RateLimit.WindowMs < 1000 || newSettings.RateLimit.WindowMs > 3600000 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "windowMs должен быть в диапазоне от 1000 до 3600000 мс",
		})
		return
	}

	if newSettings.RateLimit.MaxRequests < 1 || newSettings.RateLimit.MaxRequests > 10000 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "maxRequests должен быть в диапазоне от 1 до 10000",
		})
		return
	}

	validLogLevels := map[string]bool{
		"error": true,
		"warn":  true,
		"info":  true,
		"debug": true,
	}

	if !validLogLevels[newSettings.Logging.Level] {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Недопустимый уровень логирования (допустимые: error, warn, info, debug)",
		})
		return
	}

	// Обновляем настройки
	settingsMutex.Lock()
	currentSettings = newSettings
	settingsMutex.Unlock()

	// Сохраняем в файл
	if err := saveSettingsToFile(); err != nil {
		log.Printf("UpdateServerSettings: не удалось сохранить настройки в файл: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Не удалось сохранить настройки",
		})
		return
	}

	log.Println("UpdateServerSettings: настройки успешно обновлены и сохранены")

	c.JSON(http.StatusOK, gin.H{
		"success":  true,
		"message":  "Настройки успешно обновлены",
		"settings": currentSettings,
	})
}

// GetCurrentSettings возвращает текущие настройки (для использования в других частях кода)
func GetCurrentSettings() ServerSettings {
	settingsMutex.RLock()
	defer settingsMutex.RUnlock()
	return currentSettings
}

// ============================================================================
// LLM SETTINGS HANDLERS (Simple version for app_settings table)
// ============================================================================

// GetSettings получает все настройки из БД
func GetSettings(c *gin.Context) {
	// Получаем все настройки (category не используется, так как таблица не содержит это поле)
	settings, err := database.GetAllSettings()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Failed to fetch settings: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, settings)
}

// UpdateSettingsBatch массово обновляет настройки
func UpdateSettingsBatch(c *gin.Context) {
	var request struct {
		Settings map[string]string `json:"settings"`
		Category string            `json:"category"`
	}

	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": fmt.Sprintf("Invalid request: %v", err),
		})
		return
	}

	if len(request.Settings) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "No settings provided",
		})
		return
	}

	// Сохраняем каждую настройку
	for key, value := range request.Settings {
		if err := database.SetSetting(key, value, ""); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": fmt.Sprintf("Failed to save setting %s: %v", key, err),
			})
			return
		}
	}

	// Инвалидируем кеш настроек
	database.InvalidateSettingsCache()

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": fmt.Sprintf("Updated %d settings", len(request.Settings)),
	})
}

// ReloadLLMProvider перезагружает LLM провайдера (HOT-SWAP)
func ReloadLLMProvider(c *gin.Context) {
	// Перезагружаем провайдера из БД
	err := llm.ReloadProviderFromDB(database.DB)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Failed to reload provider: %v", err),
		})
		return
	}

	provider := llm.GetGlobalProvider()

	c.JSON(http.StatusOK, gin.H{
		"success":  true,
		"message":  "Provider reloaded successfully",
		"provider": provider.GetName(),
	})
}

// TestLLMProviderConnection тестирует подключение к LLM провайдеру (renamed to avoid conflict)
func TestLLMProviderConnection(c *gin.Context) {
	var request struct {
		Provider string `json:"provider"`
	}

	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": fmt.Sprintf("Invalid request: %v", err),
		})
		return
	}

	if request.Provider == "" {
		request.Provider = database.GetSetting("LLM_PROVIDER", "gemini")
	}

	// Создаем тестовый провайдер на основе текущих настроек
	config := llm.LoadConfigFromEnv()

	// Если запрошен другой провайдер, переключаем
	if request.Provider != string(config.Type) {
		config.Type = llm.ProviderType(request.Provider)
	}

	provider, err := llm.NewProvider(config)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Failed to create provider: %v", err),
		})
		return
	}

	// Тестовый запрос
	ctx := c.Request.Context()
	startTime := time.Now()

	response, err := provider.GenerateResponse(
		ctx,
		"Hello! Please respond with a simple greeting.",
		[]llm.Message{},
		&llm.GenerateOptions{
			Temperature: 0.7,
			MaxTokens:   50,
		},
	)

	latency := time.Since(startTime).Milliseconds()

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Test failed: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":  true,
		"message":  fmt.Sprintf("%s connection successful", config.Type),
		"provider": provider.GetName(),
		"response": response.Text,
		"latency":  latency,
	})
}

// ============================================================================
// LLM SETTINGS HANDLERS (Frontend-compatible structure)
// ============================================================================

// LLMSettingsResponse структура ответа для фронтенда
type LLMSettingsResponse struct {
	ActiveProvider string `json:"activeProvider"`
	Gemini         struct {
		APIKey string `json:"apiKey"`
		Model  string `json:"model"`
	} `json:"gemini"`
	OpenAI struct {
		APIKey  string `json:"apiKey"`
		Model   string `json:"model"`
		BaseURL string `json:"baseUrl"`
	} `json:"openai"`
	LMStudio struct {
		BaseURL string `json:"baseUrl"`
		Model   string `json:"model"`
		APIKey  string `json:"apiKey,omitempty"`
	} `json:"lmstudio"`
	Claude struct {
		APIKey string `json:"apiKey"`
		Model  string `json:"model"`
	} `json:"claude"`
	Ollama struct {
		BaseURL string `json:"baseUrl"`
		Model   string `json:"model"`
	} `json:"ollama"`
	Timeout int `json:"timeout"`
}

// GetLLMSettingsSimple получает LLM настройки в формате для фронтенда
func GetLLMSettingsSimple(c *gin.Context) {
	response := LLMSettingsResponse{}

	// Читаем активный провайдер
	response.ActiveProvider = database.GetSetting("LLM_PROVIDER", "gemini")

	// Gemini
	response.Gemini.APIKey = maskAPIKey(database.GetSetting("GEMINI_API_KEY", ""))
	response.Gemini.Model = database.GetSetting("GEMINI_MODEL", "gemini-2.0-flash-exp")

	// OpenAI
	response.OpenAI.APIKey = maskAPIKey(database.GetSetting("OPENAI_API_KEY", ""))
	response.OpenAI.Model = database.GetSetting("OPENAI_MODEL", "gpt-4o-mini")
	response.OpenAI.BaseURL = database.GetSetting("OPENAI_BASE_URL", "https://api.openai.com/v1")

	// LM Studio
	response.LMStudio.BaseURL = database.GetSetting("LMSTUDIO_BASE_URL", "http://localhost:1234/v1")
	response.LMStudio.Model = database.GetSetting("LMSTUDIO_MODEL", "local-model")
	response.LMStudio.APIKey = maskAPIKey(database.GetSetting("LMSTUDIO_API_KEY", ""))

	// Claude
	response.Claude.APIKey = maskAPIKey(database.GetSetting("ANTHROPIC_API_KEY", ""))
	response.Claude.Model = database.GetSetting("CLAUDE_MODEL", "claude-3-5-sonnet-20241022")

	// Ollama
	response.Ollama.BaseURL = database.GetSetting("OLLAMA_BASE_URL", "http://localhost:11434")
	response.Ollama.Model = database.GetSetting("OLLAMA_MODEL", "llama3.1")

	// Timeout
	response.Timeout = database.GetSettingInt("LLM_API_TIMEOUT", 30)

	c.JSON(http.StatusOK, response)
}

// UpdateLLMSettingsSimple обновляет LLM настройки из формата фронтенда
func UpdateLLMSettingsSimple(c *gin.Context) {
	var request struct {
		ActiveProvider string `json:"activeProvider"`
		Gemini         struct {
			APIKey string `json:"apiKey"`
			Model  string `json:"model"`
		} `json:"gemini"`
		OpenAI struct {
			APIKey  string `json:"apiKey"`
			Model   string `json:"model"`
			BaseURL string `json:"baseUrl"`
		} `json:"openai"`
		LMStudio struct {
			BaseURL string `json:"baseUrl"`
			Model   string `json:"model"`
			APIKey  string `json:"apiKey"`
		} `json:"lmstudio"`
		Claude struct {
			APIKey string `json:"apiKey"`
			Model  string `json:"model"`
		} `json:"claude"`
		Ollama struct {
			BaseURL string `json:"baseUrl"`
			Model   string `json:"model"`
		} `json:"ollama"`
		Timeout int  `json:"timeout"`
		HotSwap bool `json:"hotSwap"`
	}

	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	log.Printf("[LLM_SETTINGS] Обновление настроек, провайдер: %s, hotSwap: %v", request.ActiveProvider, request.HotSwap)

	// Сохраняем активный провайдер
	if err := database.SetSetting("LLM_PROVIDER", request.ActiveProvider, "Активный LLM провайдер"); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to save LLM_PROVIDER: %v", err)})
		return
	}

	// Сохраняем Gemini настройки
	if request.Gemini.APIKey != "" && !isMasked(request.Gemini.APIKey) {
		if err := database.SetSetting("GEMINI_API_KEY", request.Gemini.APIKey, "Gemini API Key"); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to save GEMINI_API_KEY: %v", err)})
			return
		}
	}
	if request.Gemini.Model != "" {
		if err := database.SetSetting("GEMINI_MODEL", request.Gemini.Model, "Gemini Model"); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to save GEMINI_MODEL: %v", err)})
			return
		}
	}

	// Сохраняем OpenAI настройки
	if request.OpenAI.APIKey != "" && !isMasked(request.OpenAI.APIKey) {
		if err := database.SetSetting("OPENAI_API_KEY", request.OpenAI.APIKey, "OpenAI API Key"); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to save OPENAI_API_KEY: %v", err)})
			return
		}
	}
	if request.OpenAI.Model != "" {
		if err := database.SetSetting("OPENAI_MODEL", request.OpenAI.Model, "OpenAI Model"); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to save OPENAI_MODEL: %v", err)})
			return
		}
	}
	if request.OpenAI.BaseURL != "" {
		if err := database.SetSetting("OPENAI_BASE_URL", request.OpenAI.BaseURL, "OpenAI Base URL"); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to save OPENAI_BASE_URL: %v", err)})
			return
		}
	}

	// Сохраняем LM Studio настройки
	if request.LMStudio.BaseURL != "" {
		if err := database.SetSetting("LMSTUDIO_BASE_URL", request.LMStudio.BaseURL, "LM Studio Base URL"); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to save LMSTUDIO_BASE_URL: %v", err)})
			return
		}
	}
	if request.LMStudio.Model != "" {
		if err := database.SetSetting("LMSTUDIO_MODEL", request.LMStudio.Model, "LM Studio Model"); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to save LMSTUDIO_MODEL: %v", err)})
			return
		}
	}
	if request.LMStudio.APIKey != "" && !isMasked(request.LMStudio.APIKey) {
		if err := database.SetSetting("LMSTUDIO_API_KEY", request.LMStudio.APIKey, "LM Studio API Key (optional)"); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to save LMSTUDIO_API_KEY: %v", err)})
			return
		}
	}

	// Сохраняем Claude настройки
	if request.Claude.APIKey != "" && !isMasked(request.Claude.APIKey) {
		if err := database.SetSetting("ANTHROPIC_API_KEY", request.Claude.APIKey, "Anthropic API Key"); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to save ANTHROPIC_API_KEY: %v", err)})
			return
		}
	}
	if request.Claude.Model != "" {
		if err := database.SetSetting("CLAUDE_MODEL", request.Claude.Model, "Claude Model"); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to save CLAUDE_MODEL: %v", err)})
			return
		}
	}

	// Сохраняем Ollama настройки
	if request.Ollama.BaseURL != "" {
		if err := database.SetSetting("OLLAMA_BASE_URL", request.Ollama.BaseURL, "Ollama Base URL"); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to save OLLAMA_BASE_URL: %v", err)})
			return
		}
	}
	if request.Ollama.Model != "" {
		if err := database.SetSetting("OLLAMA_MODEL", request.Ollama.Model, "Ollama Model"); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to save OLLAMA_MODEL: %v", err)})
			return
		}
	}

	// Сохраняем Timeout
	if request.Timeout > 0 {
		if err := database.SetSetting("LLM_API_TIMEOUT", fmt.Sprintf("%d", request.Timeout), "LLM API Timeout (seconds)"); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to save LLM_API_TIMEOUT: %v", err)})
			return
		}
	}

	// Инвалидируем кеш
	database.InvalidateSettingsCache()

	// Если hotSwap=true, перезагружаем провайдера
	if request.HotSwap {
		log.Printf("[LLM_SETTINGS] Применяем hot-swap для провайдера: %s", request.ActiveProvider)
		if err := llm.ReloadProviderFromDB(database.DB); err != nil {
			log.Printf("[LLM_SETTINGS] ⚠️ Ошибка hot-swap: %v", err)
			c.JSON(http.StatusOK, gin.H{
				"success": true,
				"message": "Settings saved but hot-swap failed",
				"error":   err.Error(),
			})
			return
		}
		log.Printf("[LLM_SETTINGS] ✅ Hot-swap успешно применён")
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "LLM settings updated successfully",
	})
}

// maskAPIKey маскирует API ключ для безопасности
func maskAPIKey(key string) string {
	if key == "" {
		return ""
	}
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "****" + key[len(key)-4:]
}

// isMasked проверяет замаскирован ли API ключ
func isMasked(key string) bool {
	return key == "" || key == "****" || (len(key) > 4 && key[4:8] == "****")
}
