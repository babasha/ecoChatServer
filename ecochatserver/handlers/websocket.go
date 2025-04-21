package handlers

import (
    "log"

    // Внутренний пакет через полный путь модуля
    "github.com/egor/ecochatserver/websocket"
)

// WebSocketHub - глобальная переменная для доступа к WebSocket хабу из всех обработчиков
var WebSocketHub *websocket.Hub

// SetWebSocketHub устанавливает WebSocket хаб для обработчиков
func SetWebSocketHub(hub *websocket.Hub) {
	WebSocketHub = hub
	log.Println("WebSocket hub установлен в обработчиках")
}