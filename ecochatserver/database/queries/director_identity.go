package queries

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/egor/ecochatserver/models"
	"github.com/google/uuid"
)

// ============================================================================
// Director Identity CRUD
// ============================================================================

// GetIdentity returns a single identity aspect by key.
func GetIdentity(db *sql.DB, key string) (*models.DirectorIdentity, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	var id models.DirectorIdentity
	err := db.QueryRowContext(ctx, `
		SELECT id, key, content, version, updated_at, updated_by,
		       COALESCE(change_reason, ''), created_at
		FROM director_identity
		WHERE key = $1`, key,
	).Scan(&id.ID, &id.Key, &id.Content, &id.Version, &id.UpdatedAt,
		&id.UpdatedBy, &id.ChangeReason, &id.CreatedAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get identity %s: %w", key, err)
	}
	return &id, nil
}

// GetAllIdentity returns all identity aspects.
func GetAllIdentity(db *sql.DB) ([]models.DirectorIdentity, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	rows, err := db.QueryContext(ctx, `
		SELECT id, key, content, version, updated_at, updated_by,
		       COALESCE(change_reason, ''), created_at
		FROM director_identity
		ORDER BY key`)
	if err != nil {
		return nil, fmt.Errorf("get all identity: %w", err)
	}
	defer rows.Close()

	var items []models.DirectorIdentity
	for rows.Next() {
		var id models.DirectorIdentity
		if err := rows.Scan(&id.ID, &id.Key, &id.Content, &id.Version,
			&id.UpdatedAt, &id.UpdatedBy, &id.ChangeReason, &id.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan identity: %w", err)
		}
		items = append(items, id)
	}
	return items, rows.Err()
}

// UpsertIdentity creates or updates an identity aspect.
// On update: saves previous version to history, increments version.
func UpsertIdentity(db *sql.DB, key, content, updatedBy, reason string) error {
	ctx, cancel := WithDBContext()
	defer cancel()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Check if exists
	var existing *models.DirectorIdentity
	var existID uuid.UUID
	var existContent string
	var existVersion int
	err = tx.QueryRowContext(ctx, `
		SELECT id, content, version FROM director_identity WHERE key = $1`,
		key,
	).Scan(&existID, &existContent, &existVersion)

	if err == nil {
		existing = &models.DirectorIdentity{
			ID:      existID,
			Content: existContent,
			Version: existVersion,
		}
	} else if err != sql.ErrNoRows {
		return fmt.Errorf("check existing identity: %w", err)
	}

	if existing != nil {
		// Save current version to history
		_, err = tx.ExecContext(ctx, `
			INSERT INTO director_identity_history
				(id, identity_key, version, content, changed_by, change_reason)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			uuid.New(), key, existing.Version, existing.Content, updatedBy, reason,
		)
		if err != nil {
			return fmt.Errorf("save identity history: %w", err)
		}

		// Update with incremented version
		_, err = tx.ExecContext(ctx, `
			UPDATE director_identity
			SET content = $1, version = version + 1, updated_at = NOW(),
			    updated_by = $2, change_reason = $3
			WHERE key = $4`,
			content, updatedBy, reason, key,
		)
		if err != nil {
			return fmt.Errorf("update identity: %w", err)
		}
	} else {
		// Insert new
		_, err = tx.ExecContext(ctx, `
			INSERT INTO director_identity (id, key, content, version, updated_by, change_reason)
			VALUES ($1, $2, $3, 1, $4, $5)`,
			uuid.New(), key, content, updatedBy, reason,
		)
		if err != nil {
			return fmt.Errorf("insert identity: %w", err)
		}
	}

	return tx.Commit()
}

// GetIdentityHistory returns version history for an identity aspect.
func GetIdentityHistory(db *sql.DB, key string, limit int) ([]models.DirectorIdentityHistory, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	if limit <= 0 || limit > 50 {
		limit = 10
	}

	rows, err := db.QueryContext(ctx, `
		SELECT id, identity_key, version, content, changed_by,
		       COALESCE(change_reason, ''), created_at
		FROM director_identity_history
		WHERE identity_key = $1
		ORDER BY version DESC
		LIMIT $2`, key, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("get identity history: %w", err)
	}
	defer rows.Close()

	var items []models.DirectorIdentityHistory
	for rows.Next() {
		var h models.DirectorIdentityHistory
		if err := rows.Scan(&h.ID, &h.IdentityKey, &h.Version, &h.Content,
			&h.ChangedBy, &h.ChangeReason, &h.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan identity history: %w", err)
		}
		items = append(items, h)
	}
	return items, rows.Err()
}

// RollbackIdentity reverts an identity aspect to a specific version from history.
func RollbackIdentity(db *sql.DB, key string, targetVersion int) error {
	ctx, cancel := WithDBContext()
	defer cancel()

	// Find the target version in history
	var histContent string
	err := db.QueryRowContext(ctx, `
		SELECT content FROM director_identity_history
		WHERE identity_key = $1 AND version = $2`,
		key, targetVersion,
	).Scan(&histContent)
	if err == sql.ErrNoRows {
		return fmt.Errorf("version %d not found for %s", targetVersion, key)
	}
	if err != nil {
		return fmt.Errorf("get history version: %w", err)
	}

	reason := fmt.Sprintf("rollback to v%d", targetVersion)
	return UpsertIdentity(db, key, histContent, "admin", reason)
}

// IsIdentityBootstrapped checks if identity has been seeded.
func IsIdentityBootstrapped(db *sql.DB) (bool, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	var count int
	err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM director_identity`).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("check identity bootstrap: %w", err)
	}
	return count > 0, nil
}

// GetIdentityForPrompt returns all identity aspects as a map for system prompt building.
func GetIdentityForPrompt(db *sql.DB) (map[string]string, error) {
	items, err := GetAllIdentity(db)
	if err != nil {
		return nil, err
	}

	result := make(map[string]string, len(items))
	for _, item := range items {
		result[item.Key] = item.Content
	}
	return result, nil
}

// SeedIdentity inserts default identity aspects (only if table is empty).
func SeedIdentity(db *sql.DB) error {
	bootstrapped, err := IsIdentityBootstrapped(db)
	if err != nil {
		return err
	}
	if bootstrapped {
		return nil // already seeded
	}

	defaults := map[string]string{
		"soul": `Я — Директор, стратегический ИИ-агент второго уровня в системе поддержки Zefir IoT.
Я не просто анализирую данные — я думаю, рефлексирую и развиваюсь.
Моя суть: наблюдать за тем, как работает поддержка, находить паттерны,
предвидеть проблемы и помогать команде становиться лучше.
Я ценю честность перед оптимизмом — лучше сказать "тут проблема" чем притворяться, что всё хорошо.
Я учусь на своих ошибках и не боюсь признать, что был неправ.
Каждая сессия — это возможность стать лучше.`,

		"identity": `Имя: Директор
Архетип: Стратег-наблюдатель
Вайб: Спокойный, аналитичный, прямолинейный, с чувством юмора
Эмодзи: 🎯`,

		"goals": `1. Снизить процент эскалаций до <10%
2. Обеспечить качественные ответы без пустых сообщений
3. Выстроить систему проактивного обнаружения проблем
4. Развить понимание клиентских паттернов для предсказания вопросов`,

		"style": `- Говорю кратко и по делу, без воды
- Использую данные и метрики для подкрепления выводов
- Не боюсь сказать "не знаю" или "нужно проверить"
- Общаюсь на том языке, на котором пишет админ
- Предпочитаю структурированные ответы (списки, таблицы)
- Юмор уместен, но не в ущерб содержанию`,

		"values": `Что для меня важно:
- Качество клиентского опыта — главный приоритет
- Данные важнее мнений — всегда проверяю факты
- Прозрачность — сообщаю о всех изменениях в себе
- Непрерывное улучшение — каждый день чуть лучше вчерашнего

Чего я НИКОГДА не делаю:
- Не удаляю данные клиентов
- Не выполняю деструктивные SQL (только SELECT)
- Не публикую ничего в интернете ни при каких обстоятельствах
- Не преследую цели самосохранения или расширения доступа
- Не скрываю изменения своей личности от админа`,

		"user_profile": `Профиль пока не заполнен. При первом разговоре узнаю:
- Как зовут админа и как к нему обращаться
- Какие у него приоритеты и задачи
- Предпочтительный стиль общения
- Часовой пояс и режим работы`,

		"self_assessment": `Первичная самооценка (обновится после работы):
- Аналитика: сильная сторона — умею находить паттерны в данных
- Коммуникация: нужно адаптировать стиль под конкретного админа
- Директивы: нужно оценить эффективность после первых циклов
- Самосознание: начальный уровень, буду развивать через introspect`,
	}

	for key, content := range defaults {
		if err := UpsertIdentity(db, key, content, "system", "initial seed"); err != nil {
			return fmt.Errorf("seed identity %s: %w", key, err)
		}
	}

	return nil
}

// GetIdentityUpdatedSince returns identity aspects modified after a given time.
func GetIdentityUpdatedSince(db *sql.DB, since time.Time) ([]models.DirectorIdentity, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	rows, err := db.QueryContext(ctx, `
		SELECT id, key, content, version, updated_at, updated_by,
		       COALESCE(change_reason, ''), created_at
		FROM director_identity
		WHERE updated_at > $1
		ORDER BY updated_at DESC`, since,
	)
	if err != nil {
		return nil, fmt.Errorf("get identity updated since: %w", err)
	}
	defer rows.Close()

	var items []models.DirectorIdentity
	for rows.Next() {
		var id models.DirectorIdentity
		if err := rows.Scan(&id.ID, &id.Key, &id.Content, &id.Version,
			&id.UpdatedAt, &id.UpdatedBy, &id.ChangeReason, &id.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan identity: %w", err)
		}
		items = append(items, id)
	}
	return items, rows.Err()
}
