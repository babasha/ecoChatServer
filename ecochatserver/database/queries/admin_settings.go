package queries

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
)

// AdminSettings представляет настройки админа
type AdminSettings struct {
	AdminID           uuid.UUID
	PreferredLanguage string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// GetAdminSettings получает настройки админа
func GetAdminSettings(db *sql.DB, adminID uuid.UUID) (*AdminSettings, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	query := `
		SELECT admin_id, preferred_language, created_at, updated_at
		FROM admin_settings
		WHERE admin_id = $1
	`

	var settings AdminSettings
	err := db.QueryRowContext(ctx, query, adminID).Scan(
		&settings.AdminID,
		&settings.PreferredLanguage,
		&settings.CreatedAt,
		&settings.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		// Если настроек нет, возвращаем настройки по умолчанию
		log.Printf("GetAdminSettings: настройки для админа %s не найдены, возвращаем дефолтные", adminID)
		return &AdminSettings{
			AdminID:           adminID,
			PreferredLanguage: "ru",
			CreatedAt:         time.Now(),
			UpdatedAt:         time.Now(),
		}, nil
	}

	if err != nil {
		log.Printf("GetAdminSettings: ошибка получения настроек: %v", err)
		return nil, fmt.Errorf("ошибка получения настроек: %w", err)
	}

	log.Printf("GetAdminSettings: настройки для админа %s получены, язык=%s", adminID, settings.PreferredLanguage)
	return &settings, nil
}

// UpsertAdminSettings создает или обновляет настройки админа
func UpsertAdminSettings(db *sql.DB, adminID uuid.UUID, preferredLanguage string) error {
	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	query := `
		INSERT INTO admin_settings (admin_id, preferred_language, created_at, updated_at)
		VALUES ($1, $2, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT (admin_id)
		DO UPDATE SET
			preferred_language = EXCLUDED.preferred_language,
			updated_at = CURRENT_TIMESTAMP
	`

	result, err := db.ExecContext(ctx, query, adminID, preferredLanguage)
	if err != nil {
		log.Printf("UpsertAdminSettings: ошибка сохранения настроек: %v", err)
		return fmt.Errorf("ошибка сохранения настроек: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		log.Printf("UpsertAdminSettings: ошибка проверки результата: %v", err)
		return fmt.Errorf("ошибка проверки результата: %w", err)
	}

	log.Printf("UpsertAdminSettings: настройки для админа %s сохранены, язык=%s, затронуто строк=%d",
		adminID, preferredLanguage, rowsAffected)

	return nil
}
