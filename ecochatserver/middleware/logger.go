package middleware

import (
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
)

// Logger создаёт middleware для логирования HTTP запросов
func Logger() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Время начала запроса
		startTime := time.Now()

		// Обрабатываем запрос
		c.Next()

		// Время после обработки запроса
		endTime := time.Now()
		// Время выполнения запроса
		latencyTime := endTime.Sub(startTime)

		// Получаем информацию о запросе
		method := c.Request.Method
		uri := c.Request.RequestURI
		status := c.Writer.Status()
		clientIP := c.ClientIP()

		// Логируем запрос
		fmt.Printf("[GIN] %v | %3d | %13v | %15s | %-7s %s\n",
			endTime.Format("2006/01/02 - 15:04:05"),
			status,
			latencyTime,
			clientIP,
			method,
			uri,
		)
	}
}