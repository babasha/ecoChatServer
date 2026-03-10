package handlers

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/egor/ecochatserver/database"
	"github.com/egor/ecochatserver/llm"
	"github.com/gin-gonic/gin"
)

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

// TestLLMProviderConnection тестирует подключение к LLM провайдеру
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

// RoleSettingsJSON — настройки роли для фронтенда
type RoleSettingsJSON struct {
	Provider string `json:"provider"` // "" = использовать глобальный
	Model    string `json:"model"`
	BaseURL  string `json:"baseUrl,omitempty"`
	APIKey   string `json:"apiKey,omitempty"`
}

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
	OpenAIOAuth struct {
		Model            string `json:"model"`
		ReasoningEffort  string `json:"reasoningEffort"`  // "minimal", "low", "medium", "high"
		ReasoningSummary string `json:"reasoningSummary"` // "auto", "concise", "detailed"
		TextVerbosity    string `json:"textVerbosity"`    // "low", "medium", "high"
	} `json:"openaiOauth"`
	LMStudio struct {
		BaseURL string `json:"baseUrl"`
		Model   string `json:"model"`
		APIKey  string `json:"apiKey,omitempty"`
	} `json:"lmstudio"`
	Claude struct {
		APIKey string `json:"apiKey"`
		Model  string `json:"model"`
	} `json:"claude"`
	ClaudeOAuth struct {
		Model           string `json:"model"`
		ThinkingEnabled bool   `json:"thinkingEnabled"`
		ThinkingEffort  string `json:"thinkingEffort"` // "low", "medium", "high", "max"
		ThinkingBudget  int    `json:"thinkingBudget"` // budget_tokens для non-adaptive
	} `json:"claudeOauth"`
	Ollama struct {
		BaseURL string `json:"baseUrl"`
		Model   string `json:"model"`
	} `json:"ollama"`
	Timeout             int  `json:"timeout"`
	EnableAutoResponder bool `json:"enableAutoResponder"`

	// Role-based provider assignments
	Roles struct {
		Translator RoleSettingsJSON `json:"translator"`
		Responder  RoleSettingsJSON `json:"responder"`
		Director   RoleSettingsJSON `json:"director"`
	} `json:"roles"`
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

	// OpenAI OAuth
	response.OpenAIOAuth.Model = database.GetSetting("OPENAI_OAUTH_MODEL", "gpt-5.2-codex")
	response.OpenAIOAuth.ReasoningEffort = database.GetSetting("OPENAI_OAUTH_REASONING_EFFORT", "")
	response.OpenAIOAuth.ReasoningSummary = database.GetSetting("OPENAI_OAUTH_REASONING_SUMMARY", "auto")
	response.OpenAIOAuth.TextVerbosity = database.GetSetting("OPENAI_OAUTH_TEXT_VERBOSITY", "medium")

	// LM Studio
	response.LMStudio.BaseURL = database.GetSetting("LMSTUDIO_BASE_URL", "http://localhost:1234/v1")
	response.LMStudio.Model = database.GetSetting("LMSTUDIO_MODEL", "local-model")
	response.LMStudio.APIKey = maskAPIKey(database.GetSetting("LMSTUDIO_API_KEY", ""))

	// Claude
	response.Claude.APIKey = maskAPIKey(database.GetSetting("ANTHROPIC_API_KEY", ""))
	response.Claude.Model = database.GetSetting("CLAUDE_MODEL", "claude-3-5-sonnet-20241022")

	// Claude OAuth
	response.ClaudeOAuth.Model = database.GetSetting("CLAUDE_OAUTH_MODEL", "claude-sonnet-4-20250514")
	response.ClaudeOAuth.ThinkingEnabled = database.GetSettingBool("CLAUDE_OAUTH_THINKING_ENABLED", false)
	response.ClaudeOAuth.ThinkingEffort = database.GetSetting("CLAUDE_OAUTH_THINKING_EFFORT", "")
	response.ClaudeOAuth.ThinkingBudget = database.GetSettingInt("CLAUDE_OAUTH_THINKING_BUDGET", 8192)

	// Ollama
	response.Ollama.BaseURL = database.GetSetting("OLLAMA_BASE_URL", "http://localhost:11434")
	response.Ollama.Model = database.GetSetting("OLLAMA_MODEL", "llama3.1")

	// Timeout
	response.Timeout = database.GetSettingInt("LLM_API_TIMEOUT", 30)

	// Auto Responder
	response.EnableAutoResponder = database.GetSettingBool("ENABLE_AUTO_RESPONDER", true)

	// Role assignments
	response.Roles.Translator = RoleSettingsJSON{
		Provider: database.GetSetting("TRANSLATOR_PROVIDER", ""),
		Model:    database.GetSetting("TRANSLATOR_MODEL", ""),
		BaseURL:  database.GetSetting("TRANSLATOR_BASE_URL", ""),
		APIKey:   maskAPIKey(database.GetSetting("TRANSLATOR_API_KEY", "")),
	}
	response.Roles.Responder = RoleSettingsJSON{
		Provider: database.GetSetting("RESPONDER_PROVIDER", ""),
		Model:    database.GetSetting("RESPONDER_MODEL", ""),
		BaseURL:  database.GetSetting("RESPONDER_BASE_URL", ""),
		APIKey:   maskAPIKey(database.GetSetting("RESPONDER_API_KEY", "")),
	}
	response.Roles.Director = RoleSettingsJSON{
		Provider: database.GetSetting("DIRECTOR_PROVIDER", ""),
		Model:    database.GetSetting("DIRECTOR_MODEL", ""),
		BaseURL:  database.GetSetting("DIRECTOR_BASE_URL", ""),
		APIKey:   maskAPIKey(database.GetSetting("DIRECTOR_API_KEY", "")),
	}

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
		OpenAIOAuth struct {
			Model            string `json:"model"`
			ReasoningEffort  string `json:"reasoningEffort"`
			ReasoningSummary string `json:"reasoningSummary"`
			TextVerbosity    string `json:"textVerbosity"`
		} `json:"openaiOauth"`
		LMStudio struct {
			BaseURL string `json:"baseUrl"`
			Model   string `json:"model"`
			APIKey  string `json:"apiKey"`
		} `json:"lmstudio"`
		Claude struct {
			APIKey string `json:"apiKey"`
			Model  string `json:"model"`
		} `json:"claude"`
		ClaudeOAuth struct {
			Model           string `json:"model"`
			ThinkingEnabled bool   `json:"thinkingEnabled"`
			ThinkingEffort  string `json:"thinkingEffort"`
			ThinkingBudget  int    `json:"thinkingBudget"`
		} `json:"claudeOauth"`
		Ollama struct {
			BaseURL string `json:"baseUrl"`
			Model   string `json:"model"`
		} `json:"ollama"`
		Timeout             int  `json:"timeout"`
		EnableAutoResponder bool `json:"enableAutoResponder"`
		HotSwap             bool `json:"hotSwap"`
		Roles               struct {
			Translator RoleSettingsJSON `json:"translator"`
			Responder  RoleSettingsJSON `json:"responder"`
			Director   RoleSettingsJSON `json:"director"`
		} `json:"roles"`
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

	// Сохраняем OpenAI OAuth настройки
	if request.OpenAIOAuth.Model != "" {
		if err := database.SetSetting("OPENAI_OAUTH_MODEL", request.OpenAIOAuth.Model, "OpenAI OAuth Model"); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to save OPENAI_OAUTH_MODEL: %v", err)})
			return
		}
	}
	if err := database.SetSetting("OPENAI_OAUTH_REASONING_EFFORT", request.OpenAIOAuth.ReasoningEffort, "OpenAI OAuth Reasoning Effort"); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to save OPENAI_OAUTH_REASONING_EFFORT: %v", err)})
		return
	}
	if err := database.SetSetting("OPENAI_OAUTH_REASONING_SUMMARY", request.OpenAIOAuth.ReasoningSummary, "OpenAI OAuth Reasoning Summary"); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to save OPENAI_OAUTH_REASONING_SUMMARY: %v", err)})
		return
	}
	if err := database.SetSetting("OPENAI_OAUTH_TEXT_VERBOSITY", request.OpenAIOAuth.TextVerbosity, "OpenAI OAuth Text Verbosity"); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to save OPENAI_OAUTH_TEXT_VERBOSITY: %v", err)})
		return
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

	// Сохраняем Claude OAuth настройки
	if request.ClaudeOAuth.Model != "" {
		if err := database.SetSetting("CLAUDE_OAUTH_MODEL", request.ClaudeOAuth.Model, "Claude OAuth Model"); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to save CLAUDE_OAUTH_MODEL: %v", err)})
			return
		}
	}
	thinkingEnabledVal := "false"
	if request.ClaudeOAuth.ThinkingEnabled {
		thinkingEnabledVal = "true"
	}
	if err := database.SetSetting("CLAUDE_OAUTH_THINKING_ENABLED", thinkingEnabledVal, "Claude OAuth Thinking Enabled"); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to save CLAUDE_OAUTH_THINKING_ENABLED: %v", err)})
		return
	}
	if err := database.SetSetting("CLAUDE_OAUTH_THINKING_EFFORT", request.ClaudeOAuth.ThinkingEffort, "Claude OAuth Thinking Effort"); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to save CLAUDE_OAUTH_THINKING_EFFORT: %v", err)})
		return
	}
	if request.ClaudeOAuth.ThinkingBudget > 0 {
		if err := database.SetSetting("CLAUDE_OAUTH_THINKING_BUDGET", fmt.Sprintf("%d", request.ClaudeOAuth.ThinkingBudget), "Claude OAuth Thinking Budget"); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to save CLAUDE_OAUTH_THINKING_BUDGET: %v", err)})
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

	// Сохраняем настройку автоответчика
	enableAutoResponderValue := "false"
	if request.EnableAutoResponder {
		enableAutoResponderValue = "true"
	}
	if err := database.SetSetting("ENABLE_AUTO_RESPONDER", enableAutoResponderValue, "Enable Auto Responder"); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to save ENABLE_AUTO_RESPONDER: %v", err)})
		return
	}

	// Сохраняем Role assignments
	for _, roleData := range []struct {
		role llm.ProviderRole
		cfg  RoleSettingsJSON
	}{
		{llm.RoleTranslator, request.Roles.Translator},
		{llm.RoleResponder, request.Roles.Responder},
		{llm.RoleDirector, request.Roles.Director},
	} {
		if err := llm.SaveRoleConfig(roleData.role, llm.RoleConfig{
			Provider: roleData.cfg.Provider,
			Model:    roleData.cfg.Model,
			BaseURL:  roleData.cfg.BaseURL,
			APIKey:   roleData.cfg.APIKey,
		}); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to save %s role: %v", roleData.role, err)})
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
		log.Printf("[LLM_SETTINGS] ✅ Hot-swap провайдера успешно применён")

		// ВАЖНО: Переинициализируем AutoResponder с новым провайдером
		log.Printf("[LLM_SETTINGS] Переинициализация AutoResponder с новым провайдером...")
		InitAutoResponder()
		log.Printf("[LLM_SETTINGS] ✅ AutoResponder переинициализирован")
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "LLM settings updated successfully",
	})
}
