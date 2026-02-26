package queries

import (
	"context"
	"database/sql"
	"fmt"
	"log"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// AdminWithAccess представляет админа с доступом к чату
type AdminWithAccess struct {
	AdminID uuid.UUID
	Role    string
}

// AdminLanguageInfo содержит ID админа и его язык
type AdminLanguageInfo struct {
	AdminID           uuid.UUID
	PreferredLanguage string
}

// GetAdminsWithChatAccess возвращает всех админов с доступом к чату
// УПРОЩЕННАЯ ВЕРСИЯ: пока у вас один super_admin, он всегда имеет доступ
func GetAdminsWithChatAccess(db *sql.DB, chatID uuid.UUID) ([]AdminWithAccess, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	// Super admin всегда имеет доступ ко всем чатам
	admins := []AdminWithAccess{
		{AdminID: superAdminID, Role: "super_admin"},
	}

	// Получаем админов из access control таблиц (supervisors и managers)
	query := `SELECT admin_id FROM get_admins_with_chat_access_simple($1)`

	rows, err := db.QueryContext(ctx, query, chatID)
	if err != nil {
		// Если функция не существует или ошибка - возвращаем только super admin
		log.Printf("GetAdminsWithChatAccess: ошибка получения доп. админов: %v, возвращаем только super_admin", err)
		return admins, nil
	}
	defer rows.Close()

	for rows.Next() {
		var adminID uuid.UUID
		if err := rows.Scan(&adminID); err != nil {
			log.Printf("GetAdminsWithChatAccess: ошибка сканирования: %v", err)
			continue
		}
		// Пропускаем дубликаты
		if adminID == superAdminID {
			continue
		}
		// Роль определяем примерно (в будущем можно улучшить)
		admins = append(admins, AdminWithAccess{
			AdminID: adminID,
			Role:    "supervisor", // По умолчанию считаем supervisor
		})
	}

	log.Printf("GetAdminsWithChatAccess: найдено %d админов с доступом к чату %s", len(admins), chatID)
	return admins, nil
}

// superAdminID — hardcoded super admin who always has access to all chats
var superAdminID = uuid.MustParse("05605c9d-c50f-4515-8949-9b61ae73b3aa")

// GetAdminLanguagesForChat возвращает языки всех админов с доступом к чату.
// ОПТИМИЗАЦИЯ: один SQL-запрос вместо двух (access + languages).
func GetAdminLanguagesForChat(db *sql.DB, chatID uuid.UUID) ([]AdminLanguageInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	query := `
		WITH admins AS (
			SELECT $2::uuid AS admin_id
			UNION
			SELECT admin_id FROM get_admins_with_chat_access_simple($1)
		)
		SELECT DISTINCT a.admin_id, COALESCE(s.preferred_language, 'ru') as preferred_language
		FROM admins a
		LEFT JOIN admin_settings s ON s.admin_id = a.admin_id
	`

	result, err := scanAdminLanguages(db.QueryContext(ctx, query, chatID, superAdminID))
	if err != nil {
		// Fallback: если get_admins_with_chat_access_simple не существует,
		// возвращаем только super admin с дефолтным языком
		log.Printf("GetAdminLanguagesForChat: ошибка запроса: %v, fallback на super_admin", err)
		return []AdminLanguageInfo{{AdminID: superAdminID, PreferredLanguage: "ru"}}, nil
	}

	log.Printf("GetAdminLanguagesForChat: получено %d языков для чата %s", len(result), chatID)
	return result, nil
}

// GetAdminLanguagesForChatOnlineOnly возвращает языки ТОЛЬКО онлайн админов с доступом к чату.
// ОПТИМИЗАЦИЯ: один SQL-запрос с фильтром по online IDs.
func GetAdminLanguagesForChatOnlineOnly(db *sql.DB, chatID uuid.UUID, onlineAdminIDs []uuid.UUID) ([]AdminLanguageInfo, error) {
	if len(onlineAdminIDs) == 0 {
		log.Printf("GetAdminLanguagesForChatOnlineOnly: нет онлайн админов")
		return []AdminLanguageInfo{}, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	query := `
		WITH admins AS (
			SELECT $2::uuid AS admin_id
			UNION
			SELECT admin_id FROM get_admins_with_chat_access_simple($1)
		)
		SELECT DISTINCT a.admin_id, COALESCE(s.preferred_language, 'ru') as preferred_language
		FROM admins a
		LEFT JOIN admin_settings s ON s.admin_id = a.admin_id
		WHERE a.admin_id = ANY($3)
	`

	result, err := scanAdminLanguages(db.QueryContext(ctx, query, chatID, superAdminID, pq.Array(onlineAdminIDs)))
	if err != nil {
		log.Printf("GetAdminLanguagesForChatOnlineOnly: ошибка запроса: %v", err)
		return []AdminLanguageInfo{}, nil
	}

	log.Printf("GetAdminLanguagesForChatOnlineOnly: получено %d ОНЛАЙН админов с языками для чата %s",
		len(result), chatID)
	return result, nil
}

// scanAdminLanguages scans rows of (admin_id, preferred_language) into AdminLanguageInfo slice.
func scanAdminLanguages(rows *sql.Rows, err error) ([]AdminLanguageInfo, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []AdminLanguageInfo
	for rows.Next() {
		var info AdminLanguageInfo
		if err := rows.Scan(&info.AdminID, &info.PreferredLanguage); err != nil {
			return nil, fmt.Errorf("ошибка сканирования языка: %w", err)
		}
		result = append(result, info)
	}
	return result, rows.Err()
}

// CheckAdminAccessToChat проверяет имеет ли админ доступ к чату
func CheckAdminAccessToChat(db *sql.DB, adminID uuid.UUID, adminRole string, chatID uuid.UUID) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	var hasAccess bool
	query := `SELECT check_admin_access_to_chat($1, $2, $3)`

	err := db.QueryRowContext(ctx, query, adminID, adminRole, chatID).Scan(&hasAccess)
	if err != nil {
		return false, fmt.Errorf("ошибка проверки доступа: %w", err)
	}

	return hasAccess, nil
}

// AddSupervisorSourceAccess добавляет supervisor'у доступ к источнику чатов
func AddSupervisorSourceAccess(db *sql.DB, supervisorID, clientID uuid.UUID, sourceType string, sourceID *string) error {
	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	query := `
		INSERT INTO admin_source_access (admin_id, client_id, source_type, source_id)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (admin_id, client_id, source_type, source_id) DO NOTHING
	`

	_, err := db.ExecContext(ctx, query, supervisorID, clientID, sourceType, sourceID)
	if err != nil {
		return fmt.Errorf("ошибка добавления доступа к источнику: %w", err)
	}

	log.Printf("AddSupervisorSourceAccess: supervisor %s получил доступ к %s источнику клиента %s", supervisorID, sourceType, clientID)
	return nil
}

// AddManagerToSupervisor добавляет менеджера под супервайзера
func AddManagerToSupervisor(db *sql.DB, supervisorID, managerID, clientID uuid.UUID) error {
	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	query := `
		INSERT INTO admin_hierarchy (supervisor_id, manager_id, client_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (manager_id, client_id) DO UPDATE
		SET supervisor_id = $1, created_at = NOW()
	`

	_, err := db.ExecContext(ctx, query, supervisorID, managerID, clientID)
	if err != nil {
		return fmt.Errorf("ошибка добавления менеджера: %w", err)
	}

	log.Printf("AddManagerToSupervisor: менеджер %s добавлен под supervisor %s для клиента %s", managerID, supervisorID, clientID)
	return nil
}

// RemoveSupervisorSourceAccess удаляет доступ supervisor'а к источнику
func RemoveSupervisorSourceAccess(db *sql.DB, supervisorID, clientID uuid.UUID, sourceType string, sourceID *string) error {
	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	query := `
		DELETE FROM admin_source_access
		WHERE admin_id = $1 AND client_id = $2 AND source_type = $3
		AND ($4::text IS NULL OR source_id = $4)
	`

	result, err := db.ExecContext(ctx, query, supervisorID, clientID, sourceType, sourceID)
	if err != nil {
		return fmt.Errorf("ошибка удаления доступа: %w", err)
	}

	rows, _ := result.RowsAffected()
	log.Printf("RemoveSupervisorSourceAccess: удалено %d записей доступа для supervisor %s", rows, supervisorID)
	return nil
}

// GetSupervisorSources возвращает список источников, к которым имеет доступ supervisor
func GetSupervisorSources(db *sql.DB, supervisorID uuid.UUID) ([]struct {
	ClientID   uuid.UUID
	SourceType string
	SourceID   *string
}, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	query := `
		SELECT client_id, source_type, source_id
		FROM admin_source_access
		WHERE admin_id = $1
		ORDER BY created_at DESC
	`

	rows, err := db.QueryContext(ctx, query, supervisorID)
	if err != nil {
		return nil, fmt.Errorf("ошибка получения источников: %w", err)
	}
	defer rows.Close()

	var sources []struct {
		ClientID   uuid.UUID
		SourceType string
		SourceID   *string
	}

	for rows.Next() {
		var s struct {
			ClientID   uuid.UUID
			SourceType string
			SourceID   *string
		}
		if err := rows.Scan(&s.ClientID, &s.SourceType, &s.SourceID); err != nil {
			return nil, fmt.Errorf("ошибка сканирования источника: %w", err)
		}
		sources = append(sources, s)
	}

	return sources, rows.Err()
}
