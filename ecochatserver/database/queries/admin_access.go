package queries

import (
	"context"
	"database/sql"
	"fmt"
	"log"

	"github.com/google/uuid"
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
	superAdminID := uuid.MustParse("05605c9d-c50f-4515-8949-9b61ae73b3aa")
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

// GetAdminLanguagesForChat возвращает языки всех админов с доступом к чату
// Это ключевая функция для системы перевода - получаем все языки сразу
func GetAdminLanguagesForChat(db *sql.DB, chatID uuid.UUID) ([]AdminLanguageInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	// Получаем админов с доступом к чату
	admins, err := GetAdminsWithChatAccess(db, chatID)
	if err != nil {
		return nil, fmt.Errorf("ошибка получения админов: %w", err)
	}

	if len(admins) == 0 {
		log.Printf("GetAdminLanguagesForChat: нет админов с доступом к чату %s", chatID)
		return []AdminLanguageInfo{}, nil
	}

	// Формируем список ID для IN запроса
	adminIDs := make([]interface{}, len(admins))
	placeholders := ""
	for i, admin := range admins {
		adminIDs[i] = admin.AdminID
		if i > 0 {
			placeholders += ","
		}
		placeholders += fmt.Sprintf("$%d", i+1)
	}

	// Получаем языки всех админов за один запрос
	query := fmt.Sprintf(`
		SELECT admin_id, preferred_language
		FROM admin_settings
		WHERE admin_id IN (%s)
	`, placeholders)

	rows, err := db.QueryContext(ctx, query, adminIDs...)
	if err != nil {
		return nil, fmt.Errorf("ошибка получения языков админов: %w", err)
	}
	defer rows.Close()

	languageMap := make(map[uuid.UUID]string)
	for rows.Next() {
		var adminID uuid.UUID
		var lang string
		if err := rows.Scan(&adminID, &lang); err != nil {
			return nil, fmt.Errorf("ошибка сканирования языка: %w", err)
		}
		languageMap[adminID] = lang
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ошибка итерации языков: %w", err)
	}

	// Формируем результат с дефолтными языками для админов без настроек
	result := make([]AdminLanguageInfo, 0, len(admins))
	for _, admin := range admins {
		lang, exists := languageMap[admin.AdminID]
		if !exists || lang == "" {
			lang = "en" // Дефолтный язык
			log.Printf("GetAdminLanguagesForChat: для админа %s используется дефолтный язык: %s", admin.AdminID, lang)
		}
		result = append(result, AdminLanguageInfo{
			AdminID:           admin.AdminID,
			PreferredLanguage: lang,
		})
	}

	log.Printf("GetAdminLanguagesForChat: получено %d языков для чата %s", len(result), chatID)
	return result, nil
}

// GetAdminLanguagesForChatOnlineOnly возвращает языки ТОЛЬКО онлайн админов с доступом к чату
// Это оптимизация для системы перевода - переводим только для тех кто реально смотрит
func GetAdminLanguagesForChatOnlineOnly(db *sql.DB, chatID uuid.UUID, onlineAdminIDs []uuid.UUID) ([]AdminLanguageInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	// Если никто не онлайн - возвращаем пустой список
	if len(onlineAdminIDs) == 0 {
		log.Printf("GetAdminLanguagesForChatOnlineOnly: нет онлайн админов")
		return []AdminLanguageInfo{}, nil
	}

	// Получаем админов с доступом к чату
	adminsWithAccess, err := GetAdminsWithChatAccess(db, chatID)
	if err != nil {
		return nil, fmt.Errorf("ошибка получения админов: %w", err)
	}

	if len(adminsWithAccess) == 0 {
		log.Printf("GetAdminLanguagesForChatOnlineOnly: нет админов с доступом к чату %s", chatID)
		return []AdminLanguageInfo{}, nil
	}

	// Создаем map онлайн админов для быстрого поиска
	onlineMap := make(map[uuid.UUID]bool)
	for _, adminID := range onlineAdminIDs {
		onlineMap[adminID] = true
	}

	// Фильтруем только онлайн админов с доступом
	onlineAdminsWithAccess := make([]AdminWithAccess, 0)
	for _, admin := range adminsWithAccess {
		if onlineMap[admin.AdminID] {
			onlineAdminsWithAccess = append(onlineAdminsWithAccess, admin)
		}
	}

	if len(onlineAdminsWithAccess) == 0 {
		log.Printf("GetAdminLanguagesForChatOnlineOnly: нет ОНЛАЙН админов с доступом к чату %s (всего админов: %d, онлайн: %d)",
			chatID, len(adminsWithAccess), len(onlineAdminIDs))
		return []AdminLanguageInfo{}, nil
	}

	// Формируем список ID для IN запроса
	adminIDs := make([]interface{}, len(onlineAdminsWithAccess))
	placeholders := ""
	for i, admin := range onlineAdminsWithAccess {
		adminIDs[i] = admin.AdminID
		if i > 0 {
			placeholders += ","
		}
		placeholders += fmt.Sprintf("$%d", i+1)
	}

	// Получаем языки онлайн админов за один запрос
	query := fmt.Sprintf(`
		SELECT admin_id, preferred_language
		FROM admin_settings
		WHERE admin_id IN (%s)
	`, placeholders)

	rows, err := db.QueryContext(ctx, query, adminIDs...)
	if err != nil {
		return nil, fmt.Errorf("ошибка получения языков админов: %w", err)
	}
	defer rows.Close()

	languageMap := make(map[uuid.UUID]string)
	for rows.Next() {
		var adminID uuid.UUID
		var lang string
		if err := rows.Scan(&adminID, &lang); err != nil {
			return nil, fmt.Errorf("ошибка сканирования языка: %w", err)
		}
		languageMap[adminID] = lang
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ошибка итерации языков: %w", err)
	}

	// Формируем результат с дефолтными языками для админов без настроек
	result := make([]AdminLanguageInfo, 0, len(onlineAdminsWithAccess))
	for _, admin := range onlineAdminsWithAccess {
		lang, exists := languageMap[admin.AdminID]
		if !exists || lang == "" {
			lang = "ru" // Дефолтный язык теперь русский (можно сделать конфигурируемым)
			log.Printf("GetAdminLanguagesForChatOnlineOnly: для ОНЛАЙН админа %s используется дефолтный язык: %s", admin.AdminID, lang)
		}
		result = append(result, AdminLanguageInfo{
			AdminID:           admin.AdminID,
			PreferredLanguage: lang,
		})
	}

	log.Printf("GetAdminLanguagesForChatOnlineOnly: получено %d ОНЛАЙН админов с языками для чата %s (всего админов с доступом: %d)",
		len(result), chatID, len(adminsWithAccess))
	return result, nil
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
