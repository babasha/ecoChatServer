package handlers

import (
	"encoding/json"
	"log"

	"github.com/gin-gonic/gin"

	websocketpkg "github.com/egor/ecochatserver/websocket"
)

// processSubscribeToLogs подписывает клиента на real-time логи
func processSubscribeToLogs(client *websocketpkg.Client, payload json.RawMessage, ginCtx *gin.Context) {
	// Только админы могут подписываться на логи
	if client.ClientType != "admin" {
		client.SendError("access_denied", "Доступ запрещен")
		return
	}

	var p struct {
		LogType string `json:"logType"` // "server" или "websocket"
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		client.SendError("invalid_payload", "Некорректный формат данных для subscribeToLogs")
		return
	}

	// Выбираем буфер логов
	var logBuffer *LogBuffer
	switch p.LogType {
	case "server":
		logBuffer = ServerLogsBuffer
	case "websocket":
		logBuffer = WebSocketLogsBuffer
	default:
		client.SendError("invalid_log_type", "Неизвестный тип логов: "+p.LogType)
		return
	}

	if logBuffer == nil {
		client.SendError("log_buffer_not_initialized", "Буфер логов не инициализирован")
		return
	}

	// Подписываем клиента
	logChan := logBuffer.Subscribe(client.ID)

	log.Printf("processSubscribeToLogs: клиент %s подписался на логи типа %s", client.ID, p.LogType)

	// Отправляем подтверждение
	client.SendJSON(map[string]interface{}{
		"type": "subscribeToLogsConfirmed",
		"payload": map[string]interface{}{
			"logType": p.LogType,
			"status":  "subscribed",
		},
	})

	// Отправляем существующие логи
	existingLogs := logBuffer.GetLastN(100) // Последние 100 логов
	if len(existingLogs) > 0 {
		client.SendJSON(map[string]interface{}{
			"type": "logsBatch",
			"payload": map[string]interface{}{
				"logType": p.LogType,
				"logs":    existingLogs,
			},
		})
	}

	// Запускаем горутину для отправки новых логов
	go func() {
		for logEntry := range logChan {
			err := client.SendJSON(map[string]interface{}{
				"type": "newLog",
				"payload": map[string]interface{}{
					"logType": p.LogType,
					"log":     logEntry,
				},
			})
			if err != nil {
				log.Printf("processSubscribeToLogs: ошибка отправки лога клиенту %s: %v", client.ID, err)
				// Отписываем клиента при ошибке отправки
				logBuffer.Unsubscribe(client.ID)
				return
			}
		}
	}()
}

// processUnsubscribeFromLogs отписывает клиента от логов
func processUnsubscribeFromLogs(client *websocketpkg.Client, payload json.RawMessage, ginCtx *gin.Context) {
	var p struct {
		LogType string `json:"logType"` // "server" или "websocket"
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		client.SendError("invalid_payload", "Некорректный формат данных для unsubscribeFromLogs")
		return
	}

	// Выбираем буфер логов
	var logBuffer *LogBuffer
	switch p.LogType {
	case "server":
		logBuffer = ServerLogsBuffer
	case "websocket":
		logBuffer = WebSocketLogsBuffer
	default:
		client.SendError("invalid_log_type", "Неизвестный тип логов: "+p.LogType)
		return
	}

	if logBuffer == nil {
		client.SendError("log_buffer_not_initialized", "Буфер логов не инициализирован")
		return
	}

	// Отписываем клиента
	logBuffer.Unsubscribe(client.ID)

	log.Printf("processUnsubscribeFromLogs: клиент %s отписался от логов типа %s", client.ID, p.LogType)

	// Отправляем подтверждение
	client.SendJSON(map[string]interface{}{
		"type": "unsubscribeFromLogsConfirmed",
		"payload": map[string]interface{}{
			"logType": p.LogType,
			"status":  "unsubscribed",
		},
	})
}
