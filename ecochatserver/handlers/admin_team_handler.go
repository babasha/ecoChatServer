package handlers

import (
	"log"
	"net/http"
	"time"

	"github.com/egor/ecochatserver/database"
	"github.com/egor/ecochatserver/database/queries"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// AddSupervisorAccessRequest запрос для добавления supervisor доступа к бизнесу (всем источникам)
type AddSupervisorAccessRequest struct {
	SupervisorID uuid.UUID `json:"supervisorId" binding:"required"`
	ClientID     uuid.UUID `json:"clientId" binding:"required"` // business_id
}

// RemoveSupervisorAccessRequest запрос для удаления supervisor доступа к бизнесу
type RemoveSupervisorAccessRequest struct {
	SupervisorID uuid.UUID `json:"supervisorId" binding:"required"`
	ClientID     uuid.UUID `json:"clientId" binding:"required"`
}

// ClientInfo информация о клиенте/компании
type ClientInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// AddManagerRequest запрос для добавления manager под supervisor
type AddManagerRequest struct {
	SupervisorID uuid.UUID `json:"supervisorId" binding:"required"`
	ManagerID    uuid.UUID `json:"managerId" binding:"required"`
	ClientID     uuid.UUID `json:"clientId" binding:"required"`
}

// SupervisorSourceResponse ответ с информацией о доступах supervisor
type SupervisorSourceResponse struct {
	ClientID   uuid.UUID `json:"clientId"`
	SourceType string    `json:"sourceType"`
	SourceID   *string   `json:"sourceId,omitempty"`
}

// AddSupervisorAccess добавляет supervisor доступ ко ВСЕМ источникам бизнеса
// POST /api/admin/supervisors/access
// Только для super_admin
func AddSupervisorAccess(c *gin.Context) {
	// Проверка роли - только super_admin
	adminRole, exists := c.Get("admin_role")
	if !exists || adminRole != "super_admin" {
		log.Printf("AddSupervisorAccess: доступ запрещен, роль: %v", adminRole)
		c.JSON(http.StatusForbidden, gin.H{"error": "Доступ запрещен"})
		return
	}

	var req AddSupervisorAccessRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("AddSupervisorAccess: ошибка валидации: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Добавляем доступ ко всем типам источников для данного бизнеса
	// Мы добавляем записи для всех типов источников с NULL в source_id
	sourceTypes := []string{"web_widget", "instagram", "telegram", "whatsapp"}

	for _, sourceType := range sourceTypes {
		err := database.AddSupervisorSourceAccess(req.SupervisorID, req.ClientID, sourceType, nil)
		if err != nil {
			log.Printf("AddSupervisorAccess: ошибка добавления доступа %s: %v", sourceType, err)
			// Продолжаем добавление остальных, не прерываем
		}
	}

	log.Printf("AddSupervisorAccess: успешно добавлен доступ для supervisor %s к бизнесу %s",
		req.SupervisorID, req.ClientID)
	c.JSON(http.StatusOK, gin.H{"message": "Доступ успешно добавлен"})
}

// RemoveSupervisorAccess удаляет доступ supervisor ко ВСЕМ источникам бизнеса
// DELETE /api/admin/supervisors/access
// Только для super_admin
func RemoveSupervisorAccess(c *gin.Context) {
	// Проверка роли - только super_admin
	adminRole, exists := c.Get("admin_role")
	if !exists || adminRole != "super_admin" {
		log.Printf("RemoveSupervisorAccess: доступ запрещен, роль: %v", adminRole)
		c.JSON(http.StatusForbidden, gin.H{"error": "Доступ запрещен"})
		return
	}

	var req RemoveSupervisorAccessRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("RemoveSupervisorAccess: ошибка валидации: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Удаляем доступ ко всем типам источников для данного бизнеса
	sourceTypes := []string{"web_widget", "instagram", "telegram", "whatsapp"}

	for _, sourceType := range sourceTypes {
		err := queries.RemoveSupervisorSourceAccess(database.DB, req.SupervisorID, req.ClientID, sourceType, nil)
		if err != nil {
			log.Printf("RemoveSupervisorAccess: ошибка удаления доступа %s: %v", sourceType, err)
			// Продолжаем удаление остальных
		}
	}

	log.Printf("RemoveSupervisorAccess: успешно удален доступ для supervisor %s к бизнесу %s", req.SupervisorID, req.ClientID)
	c.JSON(http.StatusOK, gin.H{"message": "Доступ успешно удален"})
}

// GetSupervisorBusinesses возвращает список бизнесов, к которым имеет доступ supervisor
// GET /api/admin/supervisors/:id/businesses
// Для super_admin и самого supervisor
func GetSupervisorBusinesses(c *gin.Context) {
	supervisorIDParam := c.Param("id")
	supervisorID, err := uuid.Parse(supervisorIDParam)
	if err != nil {
		log.Printf("GetSupervisorBusinesses: неверный ID supervisor: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Неверный ID"})
		return
	}

	// Проверка прав: super_admin или сам supervisor
	adminID, _ := c.Get("admin_id")
	adminRole, _ := c.Get("admin_role")

	if adminRole != "super_admin" && adminID != supervisorID.String() {
		log.Printf("GetSupervisorBusinesses: доступ запрещен")
		c.JSON(http.StatusForbidden, gin.H{"error": "Доступ запрещен"})
		return
	}

	// Получаем уникальные бизнесы для supervisor
	query := `
		SELECT DISTINCT ssa.client_id, c.business_name
		FROM supervisor_source_access ssa
		LEFT JOIN chats c ON c.business_id::text = ssa.client_id::text
		WHERE ssa.supervisor_id = $1
		ORDER BY c.business_name
	`

	rows, err := database.DB.Query(query, supervisorID)
	if err != nil {
		log.Printf("GetSupervisorBusinesses: ошибка запроса: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка получения данных"})
		return
	}
	defer rows.Close()

	var businesses []ClientInfo
	for rows.Next() {
		var business ClientInfo
		var businessName *string
		if err := rows.Scan(&business.ID, &businessName); err != nil {
			log.Printf("GetSupervisorBusinesses: ошибка сканирования: %v", err)
			continue
		}
		if businessName != nil {
			business.Name = *businessName
		} else {
			business.Name = "Без имени"
		}
		businesses = append(businesses, business)
	}

	log.Printf("GetSupervisorBusinesses: найдено %d бизнесов для supervisor %s", len(businesses), supervisorID)
	c.JSON(http.StatusOK, gin.H{"businesses": businesses})
}

// SupervisorInfo представляет информацию о supervisor
type SupervisorInfo struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	DisplayName string `json:"displayName"`
	Status      string `json:"status"`
	CreatedAt   string `json:"createdAt"`
}

// GetAllSupervisors возвращает список всех supervisors
// GET /api/admin/supervisors
// Для super_admin и supervisor
func GetAllSupervisors(c *gin.Context) {
	// Проверка прав: super_admin или supervisor
	adminRole, _ := c.Get("admin_role")
	if adminRole != "super_admin" && adminRole != "supervisor" {
		log.Printf("GetAllSupervisors: доступ запрещен, роль: %v", adminRole)
		c.JSON(http.StatusForbidden, gin.H{"error": "Доступ запрещен"})
		return
	}

	// Получаем всех пользователей с ролью supervisor
	query := `
		SELECT u.id, u.email, u.display_name, u.status, u.created_at
		FROM users u
		JOIN roles r ON r.id = u.role_id
		WHERE r.name = 'supervisor' AND u.deleted_at IS NULL
		ORDER BY u.created_at DESC
	`

	rows, err := database.DB.Query(query)
	if err != nil {
		log.Printf("GetAllSupervisors: ошибка запроса: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка получения списка supervisors"})
		return
	}
	defer rows.Close()

	var supervisors []SupervisorInfo
	for rows.Next() {
		var supervisor SupervisorInfo
		var email, displayName *string
		var createdAt time.Time

		if err := rows.Scan(&supervisor.ID, &email, &displayName, &supervisor.Status, &createdAt); err != nil {
			log.Printf("GetAllSupervisors: ошибка сканирования: %v", err)
			continue
		}

		if email != nil {
			supervisor.Email = *email
		}
		if displayName != nil {
			supervisor.DisplayName = *displayName
		}
		supervisor.CreatedAt = createdAt.Format(time.RFC3339)
		supervisors = append(supervisors, supervisor)
	}

	log.Printf("GetAllSupervisors: найдено %d supervisors", len(supervisors))
	c.JSON(http.StatusOK, gin.H{"supervisors": supervisors})
}

// AddManager добавляет manager под supervisor
// POST /api/admin/managers
// Для super_admin и supervisor
func AddManager(c *gin.Context) {
	var req AddManagerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("AddManager: ошибка валидации: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Проверка прав: super_admin или supervisor который добавляет под себя
	adminID, _ := c.Get("admin_id")
	adminRole, _ := c.Get("admin_role")

	if adminRole != "super_admin" && adminID != req.SupervisorID.String() {
		log.Printf("AddManager: доступ запрещен")
		c.JSON(http.StatusForbidden, gin.H{"error": "Доступ запрещен"})
		return
	}

	err := queries.AddManagerToSupervisor(database.DB, req.SupervisorID, req.ManagerID, req.ClientID)
	if err != nil {
		log.Printf("AddManager: ошибка добавления manager: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка добавления manager"})
		return
	}

	log.Printf("AddManager: успешно добавлен manager %s под supervisor %s для клиента %s",
		req.ManagerID, req.SupervisorID, req.ClientID)
	c.JSON(http.StatusOK, gin.H{"message": "Manager успешно добавлен"})
}

// RemoveManager удаляет manager
// DELETE /api/admin/managers/:id
// Для super_admin и supervisor
func RemoveManager(c *gin.Context) {
	managerIDParam := c.Param("id")
	managerID, err := uuid.Parse(managerIDParam)
	if err != nil {
		log.Printf("RemoveManager: неверный ID manager: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Неверный ID"})
		return
	}

	// Получаем clientID из query параметров
	clientIDParam := c.Query("clientId")
	if clientIDParam == "" {
		log.Printf("RemoveManager: clientId не указан")
		c.JSON(http.StatusBadRequest, gin.H{"error": "clientId обязателен"})
		return
	}

	clientID, err := uuid.Parse(clientIDParam)
	if err != nil {
		log.Printf("RemoveManager: неверный clientId: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Неверный clientId"})
		return
	}

	// Проверка прав - для простоты пока разрешаем только super_admin
	adminRole, exists := c.Get("admin_role")
	if !exists || adminRole != "super_admin" {
		log.Printf("RemoveManager: доступ запрещен, роль: %v", adminRole)
		c.JSON(http.StatusForbidden, gin.H{"error": "Доступ запрещен"})
		return
	}

	// Удаляем из admin_hierarchy
	_, err = database.DB.Exec(`
		DELETE FROM admin_hierarchy
		WHERE manager_id = $1 AND client_id = $2
	`, managerID, clientID)

	if err != nil {
		log.Printf("RemoveManager: ошибка удаления manager: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка удаления manager"})
		return
	}

	log.Printf("RemoveManager: успешно удален manager %s для клиента %s", managerID, clientID)
	c.JSON(http.StatusOK, gin.H{"message": "Manager успешно удален"})
}

// PendingUser представляет пользователя ожидающего подтверждения
type PendingUser struct {
	ID          string  `json:"id"`
	Email       *string `json:"email"`
	DisplayName *string `json:"displayName"`
	Status      string  `json:"status"`
	CreatedAt   string  `json:"createdAt"`
}

// GetPendingUsers возвращает список пользователей ожидающих подтверждения
// GET /api/admin/users/pending
// Только для super_admin
func GetPendingUsers(c *gin.Context) {
	// Проверка роли - только super_admin
	adminRole, exists := c.Get("admin_role")
	if !exists || adminRole != "super_admin" {
		log.Printf("GetPendingUsers: доступ запрещен, роль: %v", adminRole)
		c.JSON(http.StatusForbidden, gin.H{"error": "Доступ запрещен"})
		return
	}

	// Получаем всех пользователей со статусом pending или без роли
	query := `
		SELECT u.id, u.email, u.display_name, u.status, u.created_at
		FROM users u
		WHERE (u.status = 'pending' OR u.role_id IS NULL)
		AND u.deleted_at IS NULL
		ORDER BY u.created_at DESC
	`

	rows, err := database.DB.Query(query)
	if err != nil {
		log.Printf("GetPendingUsers: ошибка запроса: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка получения пользователей"})
		return
	}
	defer rows.Close()

	var users []PendingUser
	for rows.Next() {
		var user PendingUser
		var createdAt time.Time
		if err := rows.Scan(&user.ID, &user.Email, &user.DisplayName, &user.Status, &createdAt); err != nil {
			log.Printf("GetPendingUsers: ошибка сканирования: %v", err)
			continue
		}
		user.CreatedAt = createdAt.Format(time.RFC3339)
		users = append(users, user)
	}

	log.Printf("GetPendingUsers: найдено %d ожидающих пользователей", len(users))
	c.JSON(http.StatusOK, gin.H{"users": users})
}

// UpdateUserRoleRequest запрос для обновления роли пользователя
type UpdateUserRoleRequest struct {
	UserID uuid.UUID `json:"userId" binding:"required"`
	Role   string    `json:"role" binding:"required"` // supervisor, manager, admin
}

// UpdateUserRole назначает роль пользователю
// PUT /api/admin/users/role
// Только для super_admin
func UpdateUserRole(c *gin.Context) {
	// Проверка роли - только super_admin
	adminRole, exists := c.Get("admin_role")
	if !exists || adminRole != "super_admin" {
		log.Printf("UpdateUserRole: доступ запрещен, роль: %v", adminRole)
		c.JSON(http.StatusForbidden, gin.H{"error": "Доступ запрещен"})
		return
	}

	var req UpdateUserRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("UpdateUserRole: ошибка валидации: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Валидация роли
	validRoles := map[string]bool{
		"supervisor": true,
		"manager":    true,
		"admin":      true,
	}
	if !validRoles[req.Role] {
		log.Printf("UpdateUserRole: неверная роль: %s", req.Role)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Неверная роль"})
		return
	}

	// Обновляем роль и активируем пользователя
	query := `
		UPDATE users
		SET role_id = (SELECT id FROM roles WHERE name = $1),
		    status = 'active',
		    updated_at = NOW()
		WHERE id = $2
	`

	result, err := database.DB.Exec(query, req.Role, req.UserID)
	if err != nil {
		log.Printf("UpdateUserRole: ошибка обновления: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка обновления роли"})
		return
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		log.Printf("UpdateUserRole: пользователь не найден: %s", req.UserID)
		c.JSON(http.StatusNotFound, gin.H{"error": "Пользователь не найден"})
		return
	}

	log.Printf("UpdateUserRole: роль %s назначена пользователю %s", req.Role, req.UserID)
	c.JSON(http.StatusOK, gin.H{"message": "Роль успешно назначена"})
}

// DeleteUser удаляет пользователя (soft delete)
// DELETE /api/admin/users/:id
// Только для super_admin
func DeleteUser(c *gin.Context) {
	// Проверка роли - только super_admin
	adminRole, exists := c.Get("admin_role")
	if !exists || adminRole != "super_admin" {
		log.Printf("DeleteUser: доступ запрещен, роль: %v", adminRole)
		c.JSON(http.StatusForbidden, gin.H{"error": "Доступ запрещен"})
		return
	}

	userIDParam := c.Param("id")
	userID, err := uuid.Parse(userIDParam)
	if err != nil {
		log.Printf("DeleteUser: неверный ID пользователя: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Неверный ID пользователя"})
		return
	}

	// Soft delete - устанавливаем deleted_at
	query := `
		UPDATE users
		SET deleted_at = NOW(),
		    updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL
	`

	result, err := database.DB.Exec(query, userID)
	if err != nil {
		log.Printf("DeleteUser: ошибка удаления: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка удаления пользователя"})
		return
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		log.Printf("DeleteUser: пользователь не найден или уже удален: %s", userID)
		c.JSON(http.StatusNotFound, gin.H{"error": "Пользователь не найден"})
		return
	}

	log.Printf("DeleteUser: пользователь %s успешно удален", userID)
	c.JSON(http.StatusOK, gin.H{"message": "Пользователь успешно удален"})
}

// GetAllClients возвращает список всех клиентов/компаний
// GET /api/admin/clients
func GetAllClients(c *gin.Context) {
	// Доступ для super_admin и supervisor
	adminRole, _ := c.Get("admin_role")
	if adminRole != "super_admin" && adminRole != "supervisor" {
		c.JSON(http.StatusForbidden, gin.H{"error": "Доступ запрещен"})
		return
	}

	query := `
		SELECT DISTINCT business_id, business_name
		FROM chats
		WHERE business_id IS NOT NULL AND business_name IS NOT NULL
		ORDER BY business_name
	`

	rows, err := database.DB.Query(query)
	if err != nil {
		log.Printf("GetAllClients: ошибка запроса: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка получения списка клиентов"})
		return
	}
	defer rows.Close()

	var clients []ClientInfo
	for rows.Next() {
		var client ClientInfo
		if err := rows.Scan(&client.ID, &client.Name); err != nil {
			log.Printf("GetAllClients: ошибка сканирования: %v", err)
			continue
		}
		clients = append(clients, client)
	}

	log.Printf("GetAllClients: найдено %d клиентов", len(clients))
	c.JSON(http.StatusOK, gin.H{"clients": clients})
}
