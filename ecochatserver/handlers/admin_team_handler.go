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

// AddSupervisorSourceAccessRequest запрос для добавления supervisor доступа к источнику
type AddSupervisorSourceAccessRequest struct {
	SupervisorID uuid.UUID `json:"supervisorId" binding:"required"`
	ClientID     uuid.UUID `json:"clientId" binding:"required"`
	SourceType   string    `json:"sourceType" binding:"required"` // web_widget, instagram, telegram
	SourceID     *string   `json:"sourceId"`                      // optional, null = все источники этого типа
}

// RemoveSupervisorSourceAccessRequest запрос для удаления supervisor доступа
type RemoveSupervisorSourceAccessRequest struct {
	SupervisorID uuid.UUID `json:"supervisorId" binding:"required"`
	ClientID     uuid.UUID `json:"clientId" binding:"required"`
	SourceType   string    `json:"sourceType" binding:"required"`
	SourceID     *string   `json:"sourceId"`
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

// AddSupervisorSourceAccess добавляет supervisor доступ к источнику
// POST /api/admin/supervisors/sources
// Только для super_admin
func AddSupervisorSourceAccess(c *gin.Context) {
	// Проверка роли - только super_admin
	adminRole, exists := c.Get("admin_role")
	if !exists || adminRole != "super_admin" {
		log.Printf("AddSupervisorSourceAccess: доступ запрещен, роль: %v", adminRole)
		c.JSON(http.StatusForbidden, gin.H{"error": "Доступ запрещен"})
		return
	}

	var req AddSupervisorSourceAccessRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("AddSupervisorSourceAccess: ошибка валидации: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Валидация sourceType
	validSources := map[string]bool{
		"web_widget": true,
		"instagram":  true,
		"telegram":   true,
		"whatsapp":   true,
	}
	if !validSources[req.SourceType] {
		log.Printf("AddSupervisorSourceAccess: неверный sourceType: %s", req.SourceType)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Неверный тип источника"})
		return
	}

	err := database.AddSupervisorSourceAccess(req.SupervisorID, req.ClientID, req.SourceType, req.SourceID)
	if err != nil {
		log.Printf("AddSupervisorSourceAccess: ошибка добавления доступа: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка добавления доступа"})
		return
	}

	log.Printf("AddSupervisorSourceAccess: успешно добавлен доступ для supervisor %s к %s источнику клиента %s",
		req.SupervisorID, req.SourceType, req.ClientID)
	c.JSON(http.StatusOK, gin.H{"message": "Доступ успешно добавлен"})
}

// RemoveSupervisorSourceAccess удаляет доступ supervisor к источнику
// DELETE /api/admin/supervisors/sources
// Только для super_admin
func RemoveSupervisorSourceAccess(c *gin.Context) {
	// Проверка роли - только super_admin
	adminRole, exists := c.Get("admin_role")
	if !exists || adminRole != "super_admin" {
		log.Printf("RemoveSupervisorSourceAccess: доступ запрещен, роль: %v", adminRole)
		c.JSON(http.StatusForbidden, gin.H{"error": "Доступ запрещен"})
		return
	}

	var req RemoveSupervisorSourceAccessRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("RemoveSupervisorSourceAccess: ошибка валидации: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	err := queries.RemoveSupervisorSourceAccess(database.DB, req.SupervisorID, req.ClientID, req.SourceType, req.SourceID)
	if err != nil {
		log.Printf("RemoveSupervisorSourceAccess: ошибка удаления доступа: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка удаления доступа"})
		return
	}

	log.Printf("RemoveSupervisorSourceAccess: успешно удален доступ для supervisor %s", req.SupervisorID)
	c.JSON(http.StatusOK, gin.H{"message": "Доступ успешно удален"})
}

// GetSupervisorSources возвращает список источников, к которым имеет доступ supervisor
// GET /api/admin/supervisors/:id/sources
// Для super_admin и самого supervisor
func GetSupervisorSources(c *gin.Context) {
	supervisorIDParam := c.Param("id")
	supervisorID, err := uuid.Parse(supervisorIDParam)
	if err != nil {
		log.Printf("GetSupervisorSources: неверный ID supervisor: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Неверный ID"})
		return
	}

	// Проверка прав: super_admin или сам supervisor
	adminID, _ := c.Get("admin_id")
	adminRole, _ := c.Get("admin_role")

	if adminRole != "super_admin" && adminID != supervisorID.String() {
		log.Printf("GetSupervisorSources: доступ запрещен")
		c.JSON(http.StatusForbidden, gin.H{"error": "Доступ запрещен"})
		return
	}

	sources, err := queries.GetSupervisorSources(database.DB, supervisorID)
	if err != nil {
		log.Printf("GetSupervisorSources: ошибка получения источников: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка получения данных"})
		return
	}

	// Преобразуем в response формат
	response := make([]SupervisorSourceResponse, len(sources))
	for i, s := range sources {
		response[i] = SupervisorSourceResponse{
			ClientID:   s.ClientID,
			SourceType: s.SourceType,
			SourceID:   s.SourceID,
		}
	}

	log.Printf("GetSupervisorSources: найдено %d источников для supervisor %s", len(sources), supervisorID)
	c.JSON(http.StatusOK, gin.H{"sources": response})
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
