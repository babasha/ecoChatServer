package handlers

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"sync"

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
