package handlers

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// bindJSON parses the JSON body into obj. On failure, responds with 400 and returns false.
func bindJSON(c *gin.Context, obj interface{}) bool {
	if err := c.ShouldBindJSON(obj); err != nil {
		log.Printf("%s: ошибка парсинга JSON: %v", c.FullPath(), err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Некорректный формат данных"})
		return false
	}
	return true
}

// parseUUIDParam parses a UUID from a URL parameter. On failure, responds with 400 and returns false.
func parseUUIDParam(c *gin.Context, param string) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param(param))
	if err != nil {
		log.Printf("%s: неверный UUID параметр %s: %v", c.FullPath(), param, err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Неверный ID"})
		return uuid.Nil, false
	}
	return id, true
}

// parseUUIDString parses a UUID from an arbitrary string. On failure, responds with 400 and returns false.
func parseUUIDString(c *gin.Context, s string) (uuid.UUID, bool) {
	id, err := uuid.Parse(s)
	if err != nil {
		log.Printf("%s: неверный UUID: %v", c.FullPath(), err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Неверный ID"})
		return uuid.Nil, false
	}
	return id, true
}
