package database

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"strconv"
	"sync"
	"time"
)

// SettingsCache кеш настроек в памяти
var (
	settingsCache      = make(map[string]string)
	settingsCacheMutex sync.RWMutex
	settingsCacheTime  time.Time
	cacheTTL           = 5 * time.Minute // Кеш живёт 5 минут
)

// GetSetting получает значение настройки из БД (с кешированием)
// Приоритет: 1) ENV переменная, 2) БД, 3) defaultValue
func GetSetting(key string, defaultValue string) string {
	// Сначала проверяем ENV переменную (высший приоритет)
	if envValue := os.Getenv(key); envValue != "" {
		return envValue
	}

	// Проверяем кеш
	settingsCacheMutex.RLock()
	if time.Since(settingsCacheTime) < cacheTTL {
		if cachedValue, exists := settingsCache[key]; exists {
			settingsCacheMutex.RUnlock()
			return cachedValue
		}
	}
	settingsCacheMutex.RUnlock()

	// Загружаем из БД
	value, err := GetSettingFromDB(key)
	if err != nil {
		log.Printf("[SETTINGS] Ошибка получения настройки %s из БД: %v, используем дефолт: %s", key, err, defaultValue)
		return defaultValue
	}

	// Если в БД пусто - возвращаем дефолт
	if value == "" {
		return defaultValue
	}

	// Сохраняем в кеш
	settingsCacheMutex.Lock()
	settingsCache[key] = value
	settingsCacheTime = time.Now()
	settingsCacheMutex.Unlock()

	return value
}

// GetDynamicSetting получает динамическую настройку из БД (с кешированием, БЕЗ ENV).
// Используется для значений, управляемых в runtime (OAuth токены, business ID и т.д.),
// которые не должны перекрываться устаревшими ENV переменными.
func GetDynamicSetting(key string, defaultValue string) string {
	// Проверяем кеш
	settingsCacheMutex.RLock()
	if time.Since(settingsCacheTime) < cacheTTL {
		if cachedValue, exists := settingsCache[key]; exists {
			settingsCacheMutex.RUnlock()
			return cachedValue
		}
	}
	settingsCacheMutex.RUnlock()

	// Загружаем из БД
	value, err := GetSettingFromDB(key)
	if err != nil {
		log.Printf("[SETTINGS] Ошибка получения настройки %s из БД: %v, используем дефолт: %s", key, err, defaultValue)
		return defaultValue
	}

	if value == "" {
		return defaultValue
	}

	// Сохраняем в кеш
	settingsCacheMutex.Lock()
	settingsCache[key] = value
	settingsCacheTime = time.Now()
	settingsCacheMutex.Unlock()

	return value
}

// GetSettingInt получает целочисленное значение настройки
func GetSettingInt(key string, defaultValue int) int {
	strValue := GetSetting(key, strconv.Itoa(defaultValue))
	intValue, err := strconv.Atoi(strValue)
	if err != nil {
		log.Printf("[SETTINGS] Ошибка преобразования настройки %s='%s' в int: %v, используем дефолт: %d", key, strValue, err, defaultValue)
		return defaultValue
	}
	return intValue
}

// GetSettingBool получает булево значение настройки
func GetSettingBool(key string, defaultValue bool) bool {
	strValue := GetSetting(key, strconv.FormatBool(defaultValue))
	boolValue, err := strconv.ParseBool(strValue)
	if err != nil {
		log.Printf("[SETTINGS] Ошибка преобразования настройки %s='%s' в bool: %v, используем дефолт: %v", key, strValue, err, defaultValue)
		return defaultValue
	}
	return boolValue
}

// GetSettingFromDB получает значение настройки напрямую из БД (без кеша)
func GetSettingFromDB(key string) (string, error) {
	if DB == nil {
		return "", fmt.Errorf("database connection is nil")
	}

	var value sql.NullString
	// JSONB хранит значения в JSON формате, используем #>> '{}' чтобы извлечь как текст
	query := `SELECT value #>> '{}' FROM app_settings WHERE key = $1`
	err := DB.QueryRow(query, key).Scan(&value)

	if err == sql.ErrNoRows {
		return "", nil // Настройка не найдена - вернём пустую строку
	}

	if err != nil {
		return "", fmt.Errorf("query error: %w", err)
	}

	if !value.Valid {
		return "", nil
	}

	return value.String, nil
}

// SetSetting сохраняет значение настройки в БД
// description параметр необязательный (для обратной совместимости можно передавать 2 аргумента).
func SetSetting(key string, value string, description ...string) error {
	if DB == nil {
		return fmt.Errorf("database connection is nil")
	}

	desc := ""
	if len(description) > 0 {
		desc = description[0]
	}

	// Оборачиваем значение в JSONB (добавляем кавычки для строки)
	query := `
		INSERT INTO app_settings (key, value, description)
		VALUES ($1, to_jsonb($2::text), $3)
		ON CONFLICT (key) DO UPDATE SET
			value = to_jsonb($2::text),
			description = EXCLUDED.description,
			updated_at = CURRENT_TIMESTAMP
	`

	_, err := DB.Exec(query, key, value, desc)
	if err != nil {
		return fmt.Errorf("failed to set setting: %w", err)
	}

	// Инвалидируем кеш
	settingsCacheMutex.Lock()
	delete(settingsCache, key)
	settingsCacheMutex.Unlock()

	log.Printf("[SETTINGS] Настройка %s обновлена: %s", key, value)
	return nil
}

// GetAllSettings получает все настройки из БД
func GetAllSettings() (map[string]string, error) {
	if DB == nil {
		return nil, fmt.Errorf("database connection is nil")
	}

	query := `SELECT key, value #>> '{}' FROM app_settings WHERE value IS NOT NULL ORDER BY key`
	rows, err := DB.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to query settings: %w", err)
	}
	defer rows.Close()

	settings := make(map[string]string)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			log.Printf("[SETTINGS] Ошибка чтения настройки: %v", err)
			continue
		}
		settings[key] = value
	}

	return settings, nil
}

// InvalidateSettingsCache очищает кеш настроек
func InvalidateSettingsCache() {
	settingsCacheMutex.Lock()
	defer settingsCacheMutex.Unlock()

	settingsCache = make(map[string]string)
	settingsCacheTime = time.Time{}
	log.Printf("[SETTINGS] Кеш настроек очищен")
}

// RefreshSettingsCache предзагружает все настройки в кеш
func RefreshSettingsCache() error {
	settings, err := GetAllSettings()
	if err != nil {
		return err
	}

	settingsCacheMutex.Lock()
	defer settingsCacheMutex.Unlock()

	settingsCache = settings
	settingsCacheTime = time.Now()
	log.Printf("[SETTINGS] Кеш настроек обновлён, загружено %d настроек", len(settings))

	return nil
}
