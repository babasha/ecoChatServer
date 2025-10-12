package middleware

import (
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
)

// LogFunc - тип функции для логирования
type LogFunc func(level string, message, source string)

// logFunc - глобальная функция для логирования (устанавливается из main)
var logFunc LogFunc

// SetLogFunc устанавливает функцию логирования
func SetLogFunc(f LogFunc) {
	logFunc = f
}

// Logger создаёт middleware для логирования HTTP запросов
func Logger() gin.HandlerFunc {
	return func(c *gin.Context) {
		startTime := time.Now()

		c.Next()

		latencyTime := time.Since(startTime)
		status := c.Writer.Status()

		// Логируем запрос
		logMsg := fmt.Sprintf("[GIN] %v | %3d | %13v | %15s | %-7s %s",
			time.Now().Format("2006/01/02 - 15:04:05"),
			status,
			latencyTime,
			c.ClientIP(),
			c.Request.Method,
			c.Request.RequestURI,
		)
		fmt.Println(logMsg)

		// Добавляем лог в буфер для отображения в админке
		if logFunc != nil {
			logLevel := "info"
			if status >= 500 {
				logLevel = "error"
			} else if status >= 400 {
				logLevel = "warning"
			}
			logFunc(logLevel, logMsg, "HTTP")
		}
	}
}
