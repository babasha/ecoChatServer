package handlers

import (
	"ecochatserver/websocket"
	"log"
)

// WebSocketHub - глобальная переменная для доступа к WebSocket хабу из всех обработчиков
var WebSocketHub *websocket.Hub

// SetWebSocketHub устанавливает WebSocket хаб для обработчиков
func SetWebSocketHub(hub *websocket.Hub) {
	WebSocketHub = hub
	log.Println("WebSocket hub установлен в обработчиках")
}