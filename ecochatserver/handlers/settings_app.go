package handlers

import (
	"fmt"
	"net/http"

	"github.com/egor/ecochatserver/database"
	"github.com/gin-gonic/gin"
)

// GetSettings получает все настройки из БД
func GetSettings(c *gin.Context) {
	// Получаем все настройки (category не используется, так как таблица не содержит это поле)
	settings, err := database.GetAllSettings()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Failed to fetch settings: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, settings)
}

// UpdateSettingsBatch массово обновляет настройки
func UpdateSettingsBatch(c *gin.Context) {
	var request struct {
		Settings map[string]string `json:"settings"`
		Category string            `json:"category"`
	}

	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": fmt.Sprintf("Invalid request: %v", err),
		})
		return
	}

	if len(request.Settings) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "No settings provided",
		})
		return
	}

	// Сохраняем каждую настройку
	for key, value := range request.Settings {
		if err := database.SetSetting(key, value, ""); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": fmt.Sprintf("Failed to save setting %s: %v", key, err),
			})
			return
		}
	}

	// Инвалидируем кеш настроек
	database.InvalidateSettingsCache()

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": fmt.Sprintf("Updated %d settings", len(request.Settings)),
	})
}
