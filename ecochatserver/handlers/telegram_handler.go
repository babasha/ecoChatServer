package handlers

import (
	"ecochatserver/database"
	"ecochatserver/llm"
	"ecochatserver/middleware"
	"ecochatserver/models"
	"ecochatserver/websocket"
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/gin-gonic/gin"
)

// Глобальная переменная для автоответчика
var AutoResponder *llm.AutoResponder

// InitAutoResponder инициализирует автоответчик
func InitAutoResponder() {
	// Проверяем переменную окружения для включения/отключения автоответчика
	enableAutoResponder := os.Getenv("ENABLE_AUTO_RESPONDER")
	if enableAutoResponder == "" {
		enableAutoResponder = "true" // По умолчанию включен
	}

	enabled, _ := strconv.ParseBool(enableAutoResponder)
	if !enabled {
		log.Println("Автоответчик отключен в настройках")
		return
	}

	AutoResponder = llm.NewAutoResponder()
	log.Println("Автоответчик успешно инициализирован")
}

// TelegramWebhook обрабатывает входящие запросы от Telegram API
func TelegramWebhook(c *gin.Context) {
	var incomingMessage models.IncomingMessage
	
	if err := c.ShouldBindJSON(&incomingMessage); err != nil {
		log.Printf("Ошибка парсинга JSON из Telegram webhook: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	
	log.Printf("Получено входящее сообщение от пользователя %s (source: %s)", incomingMessage.UserName, incomingMessage.Source)
	
	// Проверяем наличие обязательных полей
	if incomingMessage.UserID == "" || incomingMessage.ClientID == "" {
		log.Printf("Ошибка в запросе: отсутствуют обязательные поля UserID или ClientID")
		c.JSON(http.StatusBadRequest, gin.H{"error": "UserID и ClientID обязательны"})
		return
	}
	
	// Создаем или получаем существующий чат
	chat, _, err := database.CreateOrGetChat(
		incomingMessage.UserID,
		incomingMessage.UserName,
		incomingMessage.UserEmail,
		incomingMessage.Source,
		incomingMessage.UserID,
		incomingMessage.BotID,
		incomingMessage.ClientID,
	)
	if err != nil {
		log.Printf("Ошибка создания/получения чата: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка создания чата: " + err.Error()})
		return
	}
	
	log.Printf("Чат получен/создан с ID: %s", chat.ID)
	
	// Добавляем новое сообщение в чат
	messageType := "text"
	if incomingMessage.MessageType != "" {
		messageType = incomingMessage.MessageType
	}
	
	message, err := database.AddMessage(
		chat.ID,
		incomingMessage.Content,
		"user",
		incomingMessage.UserID,
		messageType,
		incomingMessage.Metadata,
	)
	if err != nil {
		log.Printf("Ошибка добавления сообщения: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка добавления сообщения: " + err.Error()})
		return
	}
	
	log.Printf("Добавлено сообщение с ID: %s в чат %s", message.ID, chat.ID)
	
	// Обновляем чат (получаем первую страницу сообщений)
	updatedChat, _, err := database.GetChatByID(chat.ID, 1, database.DefaultPageSize)
	if err != nil {
		log.Printf("Предупреждение: не удалось получить обновленный чат: %v", err)
		c.Error(err)
	} else {
		// Отправляем уведомление по WebSocket
		messageData, err := websocket.NewChatMessage(updatedChat, message)
		if err == nil {
			WebSocketHub.Broadcast(messageData)
			log.Printf("Отправлено уведомление по WebSocket")
		} else {
			log.Printf("Ошибка при создании WebSocket сообщения: %v", err)
		}
	}

	// Если автоответчик включен, генерируем автоматический ответ
	if AutoResponder != nil && updatedChat != nil {
		log.Printf("Запуск автоответчика для сообщения %s", message.ID)
		
		botResponse, err := AutoResponder.ProcessMessage(updatedChat, message)
		if err != nil {
			log.Printf("Ошибка при генерации автоответа: %v", err)
		} else if botResponse != nil {
			// Добавляем сообщение от бота в базу данных
			savedBotMessage, err := database.AddMessage(
				chat.ID,
				botResponse.Content,
				botResponse.Sender,
				botResponse.SenderID,
				botResponse.Type,
				botResponse.Metadata,
			)
			
			if err != nil {
				log.Printf("Ошибка при сохранении автоответа: %v", err)
			} else {
				log.Printf("Автоответчик успешно создал ответ: %s", savedBotMessage.Content)
				
				// Получаем обновленный чат еще раз после добавления ответа бота
				updatedChat, _, _ = database.GetChatByID(chat.ID, 1, database.DefaultPageSize)
				if updatedChat != nil {
					// Отправляем уведомление с ответом бота по WebSocket
					botMessageData, err := websocket.NewChatMessage(updatedChat, savedBotMessage)
					if err == nil {
						WebSocketHub.Broadcast(botMessageData)
						log.Printf("Отправлено уведомление с автоответом по WebSocket")
					}
				}
			}
		}
	}
	
	c.JSON(http.StatusOK, gin.H{"status": "message processed", "message_id": message.ID, "chat_id": chat.ID})
}

// Остальные функции без изменений...