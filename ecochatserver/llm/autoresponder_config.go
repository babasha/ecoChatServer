package llm

import "github.com/egor/ecochatserver/database"

// AutoResponderConfig содержит настройки автоответчика
type AutoResponderConfig struct {
	Enabled           bool   `json:"enabled"`
	BotName           string `json:"botName"`
	DelaySeconds      int    `json:"delaySeconds"`
	IdleTimeMinutes   int    `json:"idleTimeMinutes"`
	UseCompactPrompts bool   `json:"useCompactPrompts"` // TOON-inspired: экономия токенов
}

const (
	// Значения конфигурации по умолчанию
	DefaultBotName         = "Автоответчик"
	DefaultDelaySeconds    = 1
	DefaultIdleTimeMinutes = 5
)

// GetDefaultConfig возвращает конфигурацию по умолчанию
// Загружает настройки из БД если доступна
func GetDefaultConfig() AutoResponderConfig {
	// Загружаем UseCompactPrompts из БД (аналог USE_TOON_FORMAT)
	useCompact := database.GetSettingBool("USE_COMPACT_PROMPTS", false)

	return AutoResponderConfig{
		Enabled:           true,
		BotName:           DefaultBotName,
		DelaySeconds:      DefaultDelaySeconds,
		IdleTimeMinutes:   DefaultIdleTimeMinutes,
		UseCompactPrompts: useCompact,
	}
}
