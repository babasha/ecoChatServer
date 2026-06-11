package queries

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
)

// SaveTranslation сохраняет перевод сообщения в metadata.translations
// Использует JSONB merge для не перезаписи существующих переводов
func SaveTranslation(db *sql.DB, messageID uuid.UUID, language string, translation string) error {
	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	// Обновляем metadata используя jsonb_set для добавления перевода.
	// ВАЖНО: jsonb_set НЕ создаёт промежуточный объект 'translations', если его
	// ещё нет (возвращает JSON без изменений). Поэтому сначала гарантируем его
	// наличие через `|| jsonb_build_object('translations', <existing or {}>)`,
	// сохраняя уже записанные языки, и только потом ставим конкретный ключ.
	query := `
		UPDATE messages
		SET metadata = jsonb_set(
			COALESCE(metadata, '{}'::jsonb)
				|| jsonb_build_object('translations', COALESCE(metadata->'translations', '{}'::jsonb)),
			ARRAY['translations', $2],
			to_jsonb($3::text),
			true
		)
		WHERE id = $1
	`

	_, err := db.ExecContext(ctx, query, messageID, language, translation)
	if err != nil {
		return fmt.Errorf("SaveTranslation: %w", err)
	}

	return nil
}

// SaveDetectedLanguage сохраняет определённый язык сообщения в metadata.detectedLanguage
func SaveDetectedLanguage(db *sql.DB, messageID uuid.UUID, language string) error {
	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	query := `
		UPDATE messages
		SET metadata = jsonb_set(
			COALESCE(metadata, '{}'::jsonb),
			'{detectedLanguage}',
			to_jsonb($2::text),
			true
		)
		WHERE id = $1
	`

	_, err := db.ExecContext(ctx, query, messageID, language)
	if err != nil {
		return fmt.Errorf("SaveDetectedLanguage: %w", err)
	}

	return nil
}

// SaveTranslationsBatch сохраняет несколько переводов за один вызов
func SaveTranslationsBatch(db *sql.DB, translations map[uuid.UUID]map[string]string) error {
	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout*2) // Увеличенный таймаут для batch
	defer cancel()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("SaveTranslationsBatch: begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		UPDATE messages
		SET metadata = jsonb_set(
			COALESCE(metadata, '{}'::jsonb)
				|| jsonb_build_object('translations', COALESCE(metadata->'translations', '{}'::jsonb)),
			ARRAY['translations', $2],
			to_jsonb($3::text),
			true
		)
		WHERE id = $1
	`)
	if err != nil {
		return fmt.Errorf("SaveTranslationsBatch: prepare: %w", err)
	}
	defer stmt.Close()

	for messageID, langs := range translations {
		for lang, translation := range langs {
			_, err := stmt.ExecContext(ctx, messageID, lang, translation)
			if err != nil {
				return fmt.Errorf("SaveTranslationsBatch: exec for %s: %w", messageID, err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("SaveTranslationsBatch: commit: %w", err)
	}

	return nil
}

// GetTranslation получает перевод сообщения на указанный язык из кеша
// Возвращает пустую строку если перевода нет
func GetTranslation(db *sql.DB, messageID uuid.UUID, language string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	var translation sql.NullString
	query := `
		SELECT metadata->'translations'->$2
		FROM messages
		WHERE id = $1
	`

	err := db.QueryRowContext(ctx, query, messageID, language).Scan(&translation)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("GetTranslation: %w", err)
	}

	// Убираем кавычки из JSON строки
	if translation.Valid && len(translation.String) > 2 {
		// JSON строки хранятся как "текст", убираем первый и последний символ
		return translation.String[1 : len(translation.String)-1], nil
	}

	return "", nil
}
