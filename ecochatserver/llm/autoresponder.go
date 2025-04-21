package llm

import (
    "database/sql"
    "encoding/json"
    "log"
    "time"

    // Импорт локального пакета через module path из go.mod
    "github.com/egor/ecochatserver/models"
)
// AutoResponderConfig содержит настройки автоответчика
type AutoResponderConfig struct {
	Enabled         bool   `json:"enabled"`           // Включен ли автоответчик
	BotName         string `json:"botName"`           // Имя бота в сообщениях
	DelaySeconds    int    `json:"delaySeconds"`      // Задержка перед ответом (симуляция набора)
	IdleTimeMinutes int    `json:"idleTimeMinutes"`   // Время ожидания ответа от оператора
}

// GetDefaultConfig возвращает настройки автоответчика по умолчанию
func GetDefaultConfig() AutoResponderConfig {
	return AutoResponderConfig{
		Enabled:         true,
		BotName:         "Автоответчик",
		DelaySeconds:    1,
		IdleTimeMinutes: 5,
	}
}

// AutoResponder представляет собой автоответчик на базе ЛЛМ
type AutoResponder struct {
	client  *LLMClient
	config  AutoResponderConfig
	history map[string][]Message // chatID -> история сообщений
}

// NewAutoResponder создает новый экземпляр автоответчика
func NewAutoResponder() *AutoResponder {
	return &AutoResponder{
		client:  NewLLMClient(),
		config:  GetDefaultConfig(),
		history: make(map[string][]Message),
	}
}

// ProcessMessage обрабатывает входящее сообщение и возвращает ответ, если нужно
func (ar *AutoResponder) ProcessMessage(chat *models.Chat, message *models.Message) (*models.Message, error) {
	// Проверяем, включен ли автоответчик
	if !ar.config.Enabled {
		return nil, nil
	}

	// Отвечаем только на сообщения от пользователя
	if message.Sender != "user" {
		return nil, nil
	}

	// Если чат уже назначен оператору, не отвечаем автоматически
	if chat.AssignedTo != nil && *chat.AssignedTo != "" {
		log.Printf("Чат %s назначен оператору, автоответчик не используется", chat.ID)
		return nil, nil
	}

	// Получаем или инициализируем историю сообщений для этого чата
	chatHistory, exists := ar.history[chat.ID]
	if !exists {
		// Инициализируем историю с системным сообщением
		chatHistory = []Message{
			{
				Role:    "system",
				Content: "Ты вежливый и полезный ассистент, который отвечает на вопросы клиентов. Твои ответы должны быть краткими, информативными и дружелюбными, в нашей компании мы начинаем диалог со слов ку епта .",
			},
		}
		ar.history[chat.ID] = chatHistory
	}

	// Добавляем текущее сообщение пользователя в историю
	chatHistory = append(chatHistory, Message{
		Role:    "user",
		Content: message.Content,
	})
	ar.history[chat.ID] = chatHistory

	// Имитируем задержку ответа
	if ar.config.DelaySeconds > 0 {
		time.Sleep(time.Duration(ar.config.DelaySeconds) * time.Second)
	}

	// Генерируем ответ с помощью ЛЛМ
	response, err := ar.client.GenerateResponse(message.Content, chatHistory)
	if err != nil {
		log.Printf("Ошибка при генерации ответа: %v", err)
		return nil, err
	}

	// Создаем сообщение с ответом
	now := time.Now()
	botMessage := &models.Message{
		ID:        "", // ID будет присвоен при сохранении в БД
		ChatID:    chat.ID,
		Content:   response,
		Sender:    "admin", // Используем тип "admin", чтобы отображалось как ответ оператора
		SenderID:  "bot",   // Специальный ID для обозначения, что это бот
		Timestamp: now,
		Read:      true,
		Type:      "text",
		Metadata: map[string]interface{}{
			"isAutoResponse": true,
			"botName":        ar.config.BotName,
		},
	}

	// Добавляем ответ бота в историю
	ar.history[chat.ID] = append(ar.history[chat.ID], Message{
		Role:    "assistant",
		Content: response,
	})

	return botMessage, nil
}

// SaveChatHistory сохраняет историю чата с ЛЛМ в метаданные чата
func (ar *AutoResponder) SaveChatHistory(chatID string, tx *sql.Tx) error {
	history, exists := ar.history[chatID]
	if !exists {
		return nil // Нет истории для сохранения
	}

	// Сериализуем историю в JSON
	historyJSON, err := json.Marshal(history)
	if err != nil {
		return err
	}

	// Обновляем метаданные чата в БД
	if tx != nil {
		_, err = tx.Exec(
			"UPDATE chats SET metadata = json_set(COALESCE(metadata, '{}'), '$.llmHistory', ?) WHERE id = ?",
			string(historyJSON), chatID,
		)
	} else {
		// TODO: Добавить сохранение без транзакции, если необходимо
	}

	return err
}

// LoadChatHistory загружает историю чата с ЛЛМ из метаданных чата
func (ar *AutoResponder) LoadChatHistory(chatID string, metadata string) error {
	if metadata == "" {
		return nil // Нет метаданных для загрузки
	}

	var metadataMap map[string]interface{}
	if err := json.Unmarshal([]byte(metadata), &metadataMap); err != nil {
		return err
	}

	historyJSON, exists := metadataMap["llmHistory"]
	if !exists {
		return nil // Нет истории для загрузки
	}

	// Преобразуем интерфейс обратно в JSON
	historyBytes, err := json.Marshal(historyJSON)
	if err != nil {
		return err
	}

	// Десериализуем JSON в историю
	var history []Message
	if err := json.Unmarshal(historyBytes, &history); err != nil {
		return err
	}

	ar.history[chatID] = history
	return nil
}

// ClearChatHistory очищает историю чата с ЛЛМ
func (ar *AutoResponder) ClearChatHistory(chatID string) {
	delete(ar.history, chatID)
}