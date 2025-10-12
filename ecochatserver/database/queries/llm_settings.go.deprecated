package queries

import (
	"database/sql"
	"encoding/json"
	"log"
	"time"

	"github.com/google/uuid"
)

// LLMSettings настройки LLM провайдеров
type LLMSettings struct {
	ID             uuid.UUID `json:"id"`
	ActiveProvider string    `json:"activeProvider"`

	// Gemini
	GeminiAPIKey  string `json:"geminiApiKey,omitempty"` // omitempty для безопасности
	GeminiModel   string `json:"geminiModel"`
	GeminiEnabled bool   `json:"geminiEnabled"`

	// OpenAI
	OpenAIAPIKey  string `json:"openaiApiKey,omitempty"`
	OpenAIModel   string `json:"openaiModel"`
	OpenAIBaseURL string `json:"openaiBaseUrl"`
	OpenAIEnabled bool   `json:"openaiEnabled"`

	// LM Studio
	LMStudioBaseURL string `json:"lmstudioBaseUrl"`
	LMStudioModel   string `json:"lmstudioModel"`
	LMStudioAPIKey  string `json:"lmstudioApiKey,omitempty"`
	LMStudioEnabled bool   `json:"lmstudioEnabled"`

	// Claude
	ClaudeAPIKey  string `json:"claudeApiKey,omitempty"`
	ClaudeModel   string `json:"claudeModel"`
	ClaudeEnabled bool   `json:"claudeEnabled"`

	// Общие настройки
	APITimeoutSeconds int `json:"apiTimeoutSeconds"`
	MaxRetries        int `json:"maxRetries"`

	// Метаданные
	CreatedAt time.Time  `json:"createdAt"`
	UpdatedAt time.Time  `json:"updatedAt"`
	UpdatedBy *uuid.UUID `json:"updatedBy,omitempty"`
}

// LLMSettingsPublic публичная версия без API keys
type LLMSettingsPublic struct {
	ID             uuid.UUID `json:"id"`
	ActiveProvider string    `json:"activeProvider"`

	// Gemini
	GeminiModel   string `json:"geminiModel"`
	GeminiEnabled bool   `json:"geminiEnabled"`
	GeminiHasKey  bool   `json:"geminiHasKey"` // только флаг наличия ключа

	// OpenAI
	OpenAIModel   string `json:"openaiModel"`
	OpenAIBaseURL string `json:"openaiBaseUrl"`
	OpenAIEnabled bool   `json:"openaiEnabled"`
	OpenAIHasKey  bool   `json:"openaiHasKey"`

	// LM Studio
	LMStudioBaseURL string `json:"lmstudioBaseUrl"`
	LMStudioModel   string `json:"lmstudioModel"`
	LMStudioEnabled bool   `json:"lmstudioEnabled"`
	LMStudioHasKey  bool   `json:"lmstudioHasKey"`

	// Claude
	ClaudeModel   string `json:"claudeModel"`
	ClaudeEnabled bool   `json:"claudeEnabled"`
	ClaudeHasKey  bool   `json:"claudeHasKey"`

	// Общие настройки
	APITimeoutSeconds int `json:"apiTimeoutSeconds"`
	MaxRetries        int `json:"maxRetries"`

	// Метаданные
	UpdatedAt time.Time  `json:"updatedAt"`
	UpdatedBy *uuid.UUID `json:"updatedBy,omitempty"`
}

// ToPublic конвертирует в публичную версию (без API keys)
func (s *LLMSettings) ToPublic() *LLMSettingsPublic {
	return &LLMSettingsPublic{
		ID:             s.ID,
		ActiveProvider: s.ActiveProvider,

		GeminiModel:   s.GeminiModel,
		GeminiEnabled: s.GeminiEnabled,
		GeminiHasKey:  s.GeminiAPIKey != "",

		OpenAIModel:   s.OpenAIModel,
		OpenAIBaseURL: s.OpenAIBaseURL,
		OpenAIEnabled: s.OpenAIEnabled,
		OpenAIHasKey:  s.OpenAIAPIKey != "",

		LMStudioBaseURL: s.LMStudioBaseURL,
		LMStudioModel:   s.LMStudioModel,
		LMStudioEnabled: s.LMStudioEnabled,
		LMStudioHasKey:  s.LMStudioAPIKey != "",

		ClaudeModel:   s.ClaudeModel,
		ClaudeEnabled: s.ClaudeEnabled,
		ClaudeHasKey:  s.ClaudeAPIKey != "",

		APITimeoutSeconds: s.APITimeoutSeconds,
		MaxRetries:        s.MaxRetries,

		UpdatedAt: s.UpdatedAt,
		UpdatedBy: s.UpdatedBy,
	}
}

// GetLLMSettings получает настройки LLM
func GetLLMSettings(db *sql.DB) (*LLMSettings, error) {
	query := `
		SELECT
			id, active_provider,
			gemini_api_key, gemini_model, gemini_enabled,
			openai_api_key, openai_model, openai_base_url, openai_enabled,
			lmstudio_base_url, lmstudio_model, lmstudio_api_key, lmstudio_enabled,
			claude_api_key, claude_model, claude_enabled,
			api_timeout_seconds, max_retries,
			created_at, updated_at, updated_by
		FROM llm_settings
		WHERE id = '00000000-0000-0000-0000-000000000001'::UUID
		LIMIT 1
	`

	settings := &LLMSettings{}
	var updatedBy sql.NullString

	err := db.QueryRow(query).Scan(
		&settings.ID, &settings.ActiveProvider,
		&settings.GeminiAPIKey, &settings.GeminiModel, &settings.GeminiEnabled,
		&settings.OpenAIAPIKey, &settings.OpenAIModel, &settings.OpenAIBaseURL, &settings.OpenAIEnabled,
		&settings.LMStudioBaseURL, &settings.LMStudioModel, &settings.LMStudioAPIKey, &settings.LMStudioEnabled,
		&settings.ClaudeAPIKey, &settings.ClaudeModel, &settings.ClaudeEnabled,
		&settings.APITimeoutSeconds, &settings.MaxRetries,
		&settings.CreatedAt, &settings.UpdatedAt, &updatedBy,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			log.Printf("[LLM_SETTINGS] Настройки не найдены, используем дефолтные")
			return getDefaultSettings(), nil
		}
		return nil, err
	}

	if updatedBy.Valid {
		uid, _ := uuid.Parse(updatedBy.String)
		settings.UpdatedBy = &uid
	}

	return settings, nil
}

// UpdateLLMSettings обновляет настройки LLM
func UpdateLLMSettings(db *sql.DB, settings *LLMSettings, adminID uuid.UUID) error {
	// Сохраняем предыдущие настройки для истории
	previous, err := GetLLMSettings(db)
	if err != nil {
		log.Printf("[LLM_SETTINGS] Ошибка получения предыдущих настроек: %v", err)
	}

	query := `
		UPDATE llm_settings SET
			active_provider = $1,
			gemini_api_key = $2,
			gemini_model = $3,
			gemini_enabled = $4,
			openai_api_key = $5,
			openai_model = $6,
			openai_base_url = $7,
			openai_enabled = $8,
			lmstudio_base_url = $9,
			lmstudio_model = $10,
			lmstudio_api_key = $11,
			lmstudio_enabled = $12,
			claude_api_key = $13,
			claude_model = $14,
			claude_enabled = $15,
			api_timeout_seconds = $16,
			max_retries = $17,
			updated_at = NOW(),
			updated_by = $18
		WHERE id = '00000000-0000-0000-0000-000000000001'::UUID
	`

	_, err = db.Exec(query,
		settings.ActiveProvider,
		settings.GeminiAPIKey, settings.GeminiModel, settings.GeminiEnabled,
		settings.OpenAIAPIKey, settings.OpenAIModel, settings.OpenAIBaseURL, settings.OpenAIEnabled,
		settings.LMStudioBaseURL, settings.LMStudioModel, settings.LMStudioAPIKey, settings.LMStudioEnabled,
		settings.ClaudeAPIKey, settings.ClaudeModel, settings.ClaudeEnabled,
		settings.APITimeoutSeconds, settings.MaxRetries,
		adminID,
	)

	if err != nil {
		return err
	}

	// Сохраняем историю изменений
	if previous != nil {
		_ = SaveLLMSettingsHistory(db, previous, settings, adminID)
	}

	log.Printf("[LLM_SETTINGS] Настройки обновлены админом %s, активный провайдер: %s", adminID, settings.ActiveProvider)
	return nil
}

// SaveLLMSettingsHistory сохраняет историю изменений
func SaveLLMSettingsHistory(db *sql.DB, previous, new *LLMSettings, adminID uuid.UUID) error {
	// Создаем diff изменений
	changes := map[string]interface{}{
		"previous": previous,
		"new":      new,
	}

	changesJSON, err := json.Marshal(changes)
	if err != nil {
		return err
	}

	query := `
		INSERT INTO llm_settings_history (
			previous_provider, new_provider, changes, changed_by
		) VALUES ($1, $2, $3, $4)
	`

	_, err = db.Exec(query,
		previous.ActiveProvider,
		new.ActiveProvider,
		changesJSON,
		adminID,
	)

	return err
}

// GetLLMSettingsHistory получает историю изменений
func GetLLMSettingsHistory(db *sql.DB, limit int) ([]map[string]interface{}, error) {
	if limit <= 0 {
		limit = 50
	}

	query := `
		SELECT
			id, previous_provider, new_provider,
			changes, changed_by, changed_at
		FROM llm_settings_history
		ORDER BY changed_at DESC
		LIMIT $1
	`

	rows, err := db.Query(query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var history []map[string]interface{}

	for rows.Next() {
		var (
			id               uuid.UUID
			previousProvider string
			newProvider      string
			changesJSON      []byte
			changedBy        uuid.UUID
			changedAt        time.Time
		)

		err := rows.Scan(&id, &previousProvider, &newProvider, &changesJSON, &changedBy, &changedAt)
		if err != nil {
			continue
		}

		var changes map[string]interface{}
		json.Unmarshal(changesJSON, &changes)

		history = append(history, map[string]interface{}{
			"id":               id,
			"previousProvider": previousProvider,
			"newProvider":      newProvider,
			"changes":          changes,
			"changedBy":        changedBy,
			"changedAt":        changedAt,
		})
	}

	return history, nil
}

// getDefaultSettings возвращает дефолтные настройки
func getDefaultSettings() *LLMSettings {
	id := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	return &LLMSettings{
		ID:                id,
		ActiveProvider:    "gemini",
		GeminiModel:       "gemini-2.5-flash",
		GeminiEnabled:     true,
		OpenAIModel:       "gpt-4o-mini",
		OpenAIBaseURL:     "https://api.openai.com/v1",
		OpenAIEnabled:     false,
		LMStudioBaseURL:   "http://localhost:1234/v1",
		LMStudioModel:     "local-model",
		LMStudioEnabled:   false,
		ClaudeModel:       "claude-3-5-sonnet-20241022",
		ClaudeEnabled:     false,
		APITimeoutSeconds: 30,
		MaxRetries:        3,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}
}
