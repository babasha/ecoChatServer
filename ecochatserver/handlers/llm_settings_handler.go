package handlers

import (
	"log"
	"net/http"

	"github.com/egor/ecochatserver/database"
	"github.com/egor/ecochatserver/database/queries"
	"github.com/egor/ecochatserver/llm"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// GetLLMSettings получает текущие настройки LLM
func GetLLMSettings(c *gin.Context) {
	// Проверяем что это admin
	adminID, exists := c.Get("adminId")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	log.Printf("[LLM_SETTINGS_API] Получение настроек LLM, admin: %v", adminID)

	settings, err := queries.GetLLMSettings(database.DB)
	if err != nil {
		log.Printf("[LLM_SETTINGS_API] Ошибка получения настроек: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get settings"})
		return
	}

	// Возвращаем публичную версию (без API keys)
	c.JSON(http.StatusOK, settings.ToPublic())
}

// UpdateLLMSettingsRequest запрос на обновление настроек
type UpdateLLMSettingsRequest struct {
	ActiveProvider string `json:"activeProvider" binding:"required"`

	// Gemini
	GeminiAPIKey  *string `json:"geminiApiKey"` // pointer чтобы отличать пустую строку от отсутствия
	GeminiModel   string  `json:"geminiModel"`
	GeminiEnabled bool    `json:"geminiEnabled"`

	// OpenAI
	OpenAIAPIKey  *string `json:"openaiApiKey"`
	OpenAIModel   string  `json:"openaiModel"`
	OpenAIBaseURL string  `json:"openaiBaseUrl"`
	OpenAIEnabled bool    `json:"openaiEnabled"`

	// LM Studio
	LMStudioBaseURL string  `json:"lmstudioBaseUrl"`
	LMStudioModel   string  `json:"lmstudioModel"`
	LMStudioAPIKey  *string `json:"lmstudioApiKey"`
	LMStudioEnabled bool    `json:"lmstudioEnabled"`

	// Claude
	ClaudeAPIKey  *string `json:"claudeApiKey"`
	ClaudeModel   string  `json:"claudeModel"`
	ClaudeEnabled bool    `json:"claudeEnabled"`

	// Общие настройки
	APITimeoutSeconds int `json:"apiTimeoutSeconds"`
	MaxRetries        int `json:"maxRetries"`
}

// UpdateLLMSettings обновляет настройки LLM
func UpdateLLMSettings(c *gin.Context) {
	// Проверяем что это admin
	adminIDInterface, exists := c.Get("adminId")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	adminIDStr, ok := adminIDInterface.(string)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid admin ID"})
		return
	}

	adminID, err := uuid.Parse(adminIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid admin ID format"})
		return
	}

	var req UpdateLLMSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("[LLM_SETTINGS_API] Ошибка парсинга запроса: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	log.Printf("[LLM_SETTINGS_API] Обновление настроек, admin: %s, провайдер: %s", adminID, req.ActiveProvider)

	// Получаем текущие настройки
	currentSettings, err := queries.GetLLMSettings(database.DB)
	if err != nil {
		log.Printf("[LLM_SETTINGS_API] Ошибка получения текущих настроек: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get current settings"})
		return
	}

	// Обновляем только указанные поля
	newSettings := currentSettings

	newSettings.ActiveProvider = req.ActiveProvider

	// Gemini
	if req.GeminiAPIKey != nil && *req.GeminiAPIKey != "" {
		newSettings.GeminiAPIKey = *req.GeminiAPIKey
	}
	newSettings.GeminiModel = req.GeminiModel
	newSettings.GeminiEnabled = req.GeminiEnabled

	// OpenAI
	if req.OpenAIAPIKey != nil && *req.OpenAIAPIKey != "" {
		newSettings.OpenAIAPIKey = *req.OpenAIAPIKey
	}
	newSettings.OpenAIModel = req.OpenAIModel
	newSettings.OpenAIBaseURL = req.OpenAIBaseURL
	newSettings.OpenAIEnabled = req.OpenAIEnabled

	// LM Studio
	newSettings.LMStudioBaseURL = req.LMStudioBaseURL
	newSettings.LMStudioModel = req.LMStudioModel
	if req.LMStudioAPIKey != nil && *req.LMStudioAPIKey != "" {
		newSettings.LMStudioAPIKey = *req.LMStudioAPIKey
	}
	newSettings.LMStudioEnabled = req.LMStudioEnabled

	// Claude
	if req.ClaudeAPIKey != nil && *req.ClaudeAPIKey != "" {
		newSettings.ClaudeAPIKey = *req.ClaudeAPIKey
	}
	newSettings.ClaudeModel = req.ClaudeModel
	newSettings.ClaudeEnabled = req.ClaudeEnabled

	// Общие настройки
	if req.APITimeoutSeconds > 0 {
		newSettings.APITimeoutSeconds = req.APITimeoutSeconds
	}
	if req.MaxRetries > 0 {
		newSettings.MaxRetries = req.MaxRetries
	}

	// Сохраняем в БД
	err = queries.UpdateLLMSettings(database.DB, newSettings, adminID)
	if err != nil {
		log.Printf("[LLM_SETTINGS_API] Ошибка сохранения настроек: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save settings"})
		return
	}

	// 🔥 HOT SWAP: Пересоздаем провайдера с новыми настройками
	log.Printf("[LLM_SETTINGS_API] Применяем изменения: hot-swap провайдера на %s", req.ActiveProvider)
	err = llm.ReloadProviderFromDB(database.DB)
	if err != nil {
		log.Printf("[LLM_SETTINGS_API] ⚠️ Ошибка hot-swap провайдера: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Settings saved but failed to reload provider",
			"details": err.Error(),
		})
		return
	}

	log.Printf("[LLM_SETTINGS_API] ✅ Настройки успешно применены, провайдер: %s", req.ActiveProvider)

	// Возвращаем обновленные настройки
	updatedSettings, _ := queries.GetLLMSettings(database.DB)
	c.JSON(http.StatusOK, gin.H{
		"success":  true,
		"message":  "LLM settings updated successfully",
		"settings": updatedSettings.ToPublic(),
	})
}

// TestLLMConnection тестирует подключение к выбранному провайдеру
func TestLLMConnection(c *gin.Context) {
	// Проверяем что это admin
	_, exists := c.Get("adminId")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	var req struct {
		Provider string `json:"provider" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	log.Printf("[LLM_SETTINGS_API] Тестирование подключения к провайдеру: %s", req.Provider)

	// Получаем текущие настройки
	settings, err := queries.GetLLMSettings(database.DB)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get settings"})
		return
	}

	// Создаем временный конфиг для теста
	config := createProviderConfig(settings, req.Provider)
	if config == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid provider"})
		return
	}

	provider, err := llm.NewProvider(config)
	if err != nil {
		log.Printf("[LLM_SETTINGS_API] Ошибка создания провайдера для теста: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	// Тестируем простым запросом
	ctx := c.Request.Context()
	response, err := provider.GenerateResponse(ctx, "Test connection - respond with OK", nil, &llm.GenerateOptions{
		Temperature: 0.5,
		MaxTokens:   10,
	})

	if err != nil {
		log.Printf("[LLM_SETTINGS_API] Ошибка тестирования провайдера: %v", err)
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	log.Printf("[LLM_SETTINGS_API] ✅ Тест провайдера %s успешен, ответ: %s", req.Provider, response.Text)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Connection successful",
		"details": gin.H{
			"provider": req.Provider,
			"response": response.Text,
			"tokens":   response.Usage,
		},
	})
}

// GetLLMSettingsHistory получает историю изменений настроек
func GetLLMSettingsHistory(c *gin.Context) {
	// Проверяем что это admin
	_, exists := c.Get("adminId")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	history, err := queries.GetLLMSettingsHistory(database.DB, 50)
	if err != nil {
		log.Printf("[LLM_SETTINGS_API] Ошибка получения истории: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get history"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"history": history,
	})
}

// createProviderConfig создает конфигурацию провайдера из настроек БД
func createProviderConfig(settings *queries.LLMSettings, providerType string) *llm.ProviderConfig {
	config := &llm.ProviderConfig{
		Type:    llm.ProviderType(providerType),
		Timeout: settings.APITimeoutSeconds,
	}

	switch config.Type {
	case llm.ProviderGemini:
		config.APIKey = settings.GeminiAPIKey
		config.Model = settings.GeminiModel
		if !settings.GeminiEnabled {
			return nil
		}

	case llm.ProviderOpenAI:
		config.APIKey = settings.OpenAIAPIKey
		config.Model = settings.OpenAIModel
		config.BaseURL = settings.OpenAIBaseURL
		if !settings.OpenAIEnabled {
			return nil
		}

	case llm.ProviderLMStudio:
		config.BaseURL = settings.LMStudioBaseURL
		config.Model = settings.LMStudioModel
		config.APIKey = settings.LMStudioAPIKey
		if !settings.LMStudioEnabled {
			return nil
		}

	case llm.ProviderClaude:
		config.APIKey = settings.ClaudeAPIKey
		config.Model = settings.ClaudeModel
		if !settings.ClaudeEnabled {
			return nil
		}

	default:
		return nil
	}

	return config
}
