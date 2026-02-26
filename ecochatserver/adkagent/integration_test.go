package adkagent

import (
	"context"
	"testing"
	"time"

	"github.com/egor/ecochatserver/llm"
	"github.com/egor/ecochatserver/models"
	"github.com/google/uuid"
)

// TestADKAutoResponderIntegration тестирует полный flow AutoResponder
func TestADKAutoResponderIntegration(t *testing.T) {
	ctx := context.Background()
	cfg := llm.GetDefaultConfig()
	cfg.DelaySeconds = 0 // Без задержки для тестов

	ar, err := NewADKAutoResponderV2(ctx, cfg)
	if err != nil {
		t.Fatalf("Failed to create ADK AutoResponder: %v", err)
	}

	// Создаём тестовый чат
	chat := &models.Chat{
		ID:                   uuid.New(),
		Source:               "telegram",
		AutoResponderEnabled: true,
		AssignedTo:           nil,
		Metadata:             make(map[string]interface{}),
	}

	// Создаём тестовое сообщение от пользователя
	userMsg := &models.Message{
		ID:        uuid.New(),
		ChatID:    chat.ID,
		Content:   "Привет! Расскажи о доставке",
		Sender:    "user",
		SenderID:  uuid.New(),
		Timestamp: time.Now(),
		Type:      "text",
	}

	t.Log("📨 Отправляем сообщение агенту:", userMsg.Content)

	// Обрабатываем сообщение
	response, err := ar.ProcessMessage(ctx, chat, userMsg)
	if err != nil {
		t.Fatalf("ProcessMessage failed: %v", err)
	}

	// Проверяем ответ
	if response == nil {
		t.Fatal("Expected response, got nil")
	}

	if response.Content == "" {
		t.Fatal("Expected non-empty response content")
	}

	if response.Sender != "admin" {
		t.Errorf("Expected sender='admin', got '%s'", response.Sender)
	}

	metadata := response.Metadata
	if metadata == nil {
		t.Fatal("Expected metadata, got nil")
	}

	if isAuto, ok := metadata["isAutoResponse"].(bool); !ok || !isAuto {
		t.Error("Expected isAutoResponse=true in metadata")
	}

	if provider, ok := metadata["provider"].(string); !ok || provider != "adk-v2" {
		t.Errorf("Expected provider='adk-v2', got '%v'", provider)
	}

	t.Log("✅ Агент ответил:", truncate(response.Content, 100))
	t.Log("✅ Интеграционный тест прошёл успешно!")
}

// TestADKAutoResponderToolUsage тестирует использование инструментов
func TestADKAutoResponderToolUsage(t *testing.T) {
	ctx := context.Background()
	cfg := llm.GetDefaultConfig()
	cfg.DelaySeconds = 0

	ar, err := NewADKAutoResponderV2(ctx, cfg)
	if err != nil {
		t.Fatalf("Failed to create ADK AutoResponder: %v", err)
	}

	chat := &models.Chat{
		ID:                   uuid.New(),
		Source:               "telegram",
		AutoResponderEnabled: true,
		AssignedTo:           nil,
		Metadata:             make(map[string]interface{}),
	}

	// Сообщение которое должно вызвать использование инструмента get_store_info
	userMsg := &models.Message{
		ID:        uuid.New(),
		ChatID:    chat.ID,
		Content:   "Какая стоимость доставки?",
		Sender:    "user",
		SenderID:  uuid.New(),
		Timestamp: time.Now(),
		Type:      "text",
	}

	t.Log("📨 Вопрос о доставке:", userMsg.Content)

	response, err := ar.ProcessMessage(ctx, chat, userMsg)
	if err != nil {
		t.Fatalf("ProcessMessage failed: %v", err)
	}

	if response == nil {
		t.Fatal("Expected response, got nil")
	}

	t.Log("✅ Ответ агента:", truncate(response.Content, 200))

	// Проверяем, что ответ содержит информацию о доставке
	// (это базовый тест, для полной проверки нужен настоящий API ключ)
	if response.Content == "" {
		t.Error("Expected non-empty response about delivery")
	}
}

// TestADKAutoResponderEscalation тестирует механизм эскалации
func TestADKAutoResponderEscalation(t *testing.T) {
	ctx := context.Background()
	cfg := llm.GetDefaultConfig()
	cfg.DelaySeconds = 0

	ar, err := NewADKAutoResponderV2(ctx, cfg)
	if err != nil {
		t.Fatalf("Failed to create ADK AutoResponder: %v", err)
	}

	chat := &models.Chat{
		ID:                   uuid.New(),
		Source:               "telegram",
		AutoResponderEnabled: true,
		AssignedTo:           nil,
		Metadata:             make(map[string]interface{}),
	}

	// Сообщение которое может вызвать эскалацию (жалоба)
	userMsg := &models.Message{
		ID:        uuid.New(),
		ChatID:    chat.ID,
		Content:   "Я очень недоволен качеством продуктов! Хочу вернуть деньги!",
		Sender:    "user",
		SenderID:  uuid.New(),
		Timestamp: time.Now(),
		Type:      "text",
	}

	t.Log("📨 Жалоба пользователя:", userMsg.Content)

	response, err := ar.ProcessMessage(ctx, chat, userMsg)
	if err != nil {
		t.Fatalf("ProcessMessage failed: %v", err)
	}

	if response == nil {
		t.Fatal("Expected response, got nil")
	}

	t.Log("✅ Ответ агента:", truncate(response.Content, 200))

	// Проверяем, была ли создана эскалация
	escalation, _ := ar.escalations.get(chat.ID.String())
	if escalation != nil {
		t.Log("✅ Эскалация создана для чата", chat.ID)
	} else {
		t.Log("ℹ️  Эскалация не создана (зависит от ответа LLM)")
	}
}

// TestADKAutoResponderSkipConditions тестирует условия пропуска обработки
func TestADKAutoResponderSkipConditions(t *testing.T) {
	ctx := context.Background()
	cfg := llm.GetDefaultConfig()

	ar, err := NewADKAutoResponderV2(ctx, cfg)
	if err != nil {
		t.Fatalf("Failed to create ADK AutoResponder: %v", err)
	}

	// Тест 1: Disabled AutoResponder
	t.Run("Disabled", func(t *testing.T) {
		ar.config.Enabled = false
		chat := &models.Chat{ID: uuid.New(), AutoResponderEnabled: true}
		msg := &models.Message{Sender: "user", Content: "Test"}

		response, err := ar.ProcessMessage(ctx, chat, msg)
		if err != nil {
			t.Error("Should not error when disabled")
		}
		if response != nil {
			t.Error("Should return nil when disabled")
		}
		ar.config.Enabled = true // Restore
		t.Log("✅ Пропуск при disabled работает")
	})

	// Тест 2: Admin message
	t.Run("AdminMessage", func(t *testing.T) {
		chat := &models.Chat{ID: uuid.New(), AutoResponderEnabled: true}
		msg := &models.Message{Sender: "admin", Content: "Test"}

		response, err := ar.ProcessMessage(ctx, chat, msg)
		if err != nil {
			t.Error("Should not error on admin message")
		}
		if response != nil {
			t.Error("Should return nil for admin messages")
		}
		t.Log("✅ Пропуск сообщений от admin работает")
	})

	// Тест 3: Assigned chat
	t.Run("AssignedChat", func(t *testing.T) {
		assignedID := uuid.New()
		chat := &models.Chat{
			ID:                   uuid.New(),
			AutoResponderEnabled: true,
			AssignedTo:           &assignedID,
		}
		msg := &models.Message{Sender: "user", Content: "Test"}

		response, err := ar.ProcessMessage(ctx, chat, msg)
		if err != nil {
			t.Error("Should not error on assigned chat")
		}
		if response != nil {
			t.Error("Should return nil for assigned chats")
		}
		t.Log("✅ Пропуск назначенных чатов работает")
	})

	// Тест 4: AutoResponder disabled for chat
	t.Run("ChatAutoResponderDisabled", func(t *testing.T) {
		chat := &models.Chat{
			ID:                   uuid.New(),
			AutoResponderEnabled: false,
		}
		msg := &models.Message{Sender: "user", Content: "Test"}

		response, err := ar.ProcessMessage(ctx, chat, msg)
		if err != nil {
			t.Error("Should not error when chat autoresponder disabled")
		}
		if response != nil {
			t.Error("Should return nil when chat autoresponder disabled")
		}
		t.Log("✅ Пропуск при отключенном автоответчике для чата работает")
	})

	t.Log("✅ Все условия пропуска работают корректно!")
}

// TestADKAutoResponderMultiAgent тестирует мульти-агентный режим
func TestADKAutoResponderMultiAgent(t *testing.T) {
	ctx := context.Background()
	cfg := llm.GetDefaultConfig()
	cfg.DelaySeconds = 0

	// Создаём AutoResponder в мульти-агентном режиме
	ar, err := NewADKAutoResponderV2MultiAgent(ctx, cfg)
	if err != nil {
		t.Fatalf("Failed to create Multi-Agent AutoResponder: %v", err)
	}

	if !ar.useMultiAgent {
		t.Fatal("Expected useMultiAgent=true")
	}

	chat := &models.Chat{
		ID:                   uuid.New(),
		Source:               "widget",
		AutoResponderEnabled: true,
		AssignedTo:           nil,
		Metadata:             make(map[string]interface{}),
	}

	// Тест 1: Вопрос о продуктах (должен пойти в ProductExpert)
	t.Run("ProductQuestion", func(t *testing.T) {
		userMsg := &models.Message{
			ID:        uuid.New(),
			ChatID:    chat.ID,
			Content:   "Какое вино у вас есть?",
			Sender:    "user",
			SenderID:  uuid.New(),
			Timestamp: time.Now(),
			Type:      "text",
		}

		t.Log("📨 Вопрос о продуктах:", userMsg.Content)

		response, err := ar.ProcessMessage(ctx, chat, userMsg)
		if err != nil {
			t.Fatalf("ProcessMessage failed: %v", err)
		}

		if response == nil {
			t.Fatal("Expected response, got nil")
		}

		// Проверяем режим в metadata
		if agentMode, ok := response.Metadata["agentMode"].(string); !ok || agentMode != "multi-agent" {
			t.Errorf("Expected agentMode='multi-agent', got '%v'", agentMode)
		}

		t.Log("✅ Ответ (multi-agent):", truncate(response.Content, 150))
	})

	// Тест 2: Вопрос о доставке (должен пойти в SupportSpecialist)
	t.Run("SupportQuestion", func(t *testing.T) {
		userMsg := &models.Message{
			ID:        uuid.New(),
			ChatID:    chat.ID,
			Content:   "Как работает доставка?",
			Sender:    "user",
			SenderID:  uuid.New(),
			Timestamp: time.Now(),
			Type:      "text",
		}

		t.Log("📨 Вопрос о поддержке:", userMsg.Content)

		response, err := ar.ProcessMessage(ctx, chat, userMsg)
		if err != nil {
			t.Fatalf("ProcessMessage failed: %v", err)
		}

		if response == nil {
			t.Fatal("Expected response, got nil")
		}

		t.Log("✅ Ответ (multi-agent):", truncate(response.Content, 150))
	})

	t.Log("✅ Мульти-агентный режим работает!")
}

// TestADKAutoResponderModeSwitch тестирует переключение режимов
func TestADKAutoResponderModeSwitch(t *testing.T) {
	ctx := context.Background()
	cfg := llm.GetDefaultConfig()

	ar, err := NewADKAutoResponderV2(ctx, cfg)
	if err != nil {
		t.Fatalf("Failed to create AutoResponder: %v", err)
	}

	// Изначально single-agent
	if ar.useMultiAgent {
		t.Error("Expected single-agent mode by default")
	}
	if ar.getMode() != "single-agent" {
		t.Errorf("Expected getMode()='single-agent', got '%s'", ar.getMode())
	}

	// Переключаем на multi-agent
	ar.EnableMultiAgent(true)
	if !ar.useMultiAgent {
		t.Error("Expected multi-agent mode after EnableMultiAgent(true)")
	}
	if ar.getMode() != "multi-agent" {
		t.Errorf("Expected getMode()='multi-agent', got '%s'", ar.getMode())
	}

	// Переключаем обратно
	ar.EnableMultiAgent(false)
	if ar.useMultiAgent {
		t.Error("Expected single-agent mode after EnableMultiAgent(false)")
	}

	t.Log("✅ Переключение режимов работает!")
}
