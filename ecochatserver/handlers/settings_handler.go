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

// GetSettings получает настройки по категории
func GetSettings(c *gin.Context) {
	category := c.Query("category")

	if category == "" {
		// Получаем все настройки
		settings, err := database.GetAllSettings()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": fmt.Sprintf("Failed to fetch settings: %v", err),
			})
			return
		}

		c.JSON(http.StatusOK, settings)
		return
	}

	// Получаем настройки по категории
	settings, err := database.GetSettingsByCategory(category)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Failed to fetch settings for category %s: %v", category, err),
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
		if err := database.SetSetting(key, value, "", request.Category); err != nil {
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
