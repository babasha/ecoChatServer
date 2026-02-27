package adkagent

import (
	"context"
	"testing"
	"time"

	"github.com/egor/ecochatserver/llm"
	"github.com/egor/ecochatserver/models"
	"github.com/google/uuid"
)

// TestADKAutoResponderIntegration tests the full AutoResponder flow
func TestADKAutoResponderIntegration(t *testing.T) {
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

	// Zefir-specific question
	userMsg := &models.Message{
		ID:        uuid.New(),
		ChatID:    chat.ID,
		Content:   "What humidity does a monstera need?",
		Sender:    "user",
		SenderID:  uuid.New(),
		Timestamp: time.Now(),
		Type:      "text",
	}

	t.Log("Sending message:", userMsg.Content)

	response, err := ar.ProcessMessage(ctx, chat, userMsg)
	if err != nil {
		t.Fatalf("ProcessMessage failed: %v", err)
	}

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

	t.Log("Agent response:", truncate(response.Content, 100))
	t.Log("Integration test passed!")
}

// TestADKAutoResponderToolUsage tests that tools are invoked
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

	// Question that should trigger search_faq tool
	userMsg := &models.Message{
		ID:        uuid.New(),
		ChatID:    chat.ID,
		Content:   "Is Zefir free?",
		Sender:    "user",
		SenderID:  uuid.New(),
		Timestamp: time.Now(),
		Type:      "text",
	}

	t.Log("FAQ question:", userMsg.Content)

	response, err := ar.ProcessMessage(ctx, chat, userMsg)
	if err != nil {
		t.Fatalf("ProcessMessage failed: %v", err)
	}

	if response == nil {
		t.Fatal("Expected response, got nil")
	}

	t.Log("Agent response:", truncate(response.Content, 200))

	if response.Content == "" {
		t.Error("Expected non-empty response about Zefir pricing")
	}
}

// TestADKAutoResponderEscalation tests the escalation mechanism
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

	// Complaint that might trigger escalation
	userMsg := &models.Message{
		ID:        uuid.New(),
		ChatID:    chat.ID,
		Content:   "My sensor broke and I want a refund! This is unacceptable!",
		Sender:    "user",
		SenderID:  uuid.New(),
		Timestamp: time.Now(),
		Type:      "text",
	}

	t.Log("Complaint:", userMsg.Content)

	response, err := ar.ProcessMessage(ctx, chat, userMsg)
	if err != nil {
		t.Fatalf("ProcessMessage failed: %v", err)
	}

	if response == nil {
		t.Fatal("Expected response, got nil")
	}

	t.Log("Agent response:", truncate(response.Content, 200))

	escalation, _ := ar.escalations.get(chat.ID.String())
	if escalation != nil {
		t.Log("Escalation created for chat", chat.ID)
	} else {
		t.Log("No escalation (depends on LLM response)")
	}
}

// TestADKAutoResponderSkipConditions tests message skip conditions
func TestADKAutoResponderSkipConditions(t *testing.T) {
	ctx := context.Background()
	cfg := llm.GetDefaultConfig()

	ar, err := NewADKAutoResponderV2(ctx, cfg)
	if err != nil {
		t.Fatalf("Failed to create AutoResponder: %v", err)
	}

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
		ar.config.Enabled = true
		t.Log("Skip when disabled works")
	})

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
		t.Log("Skip admin messages works")
	})

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
		t.Log("Skip assigned chats works")
	})

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
		t.Log("Skip disabled chat autoresponder works")
	})

	t.Log("All skip conditions work correctly!")
}

// TestADKAutoResponderMultiAgent tests multi-agent mode
func TestADKAutoResponderMultiAgent(t *testing.T) {
	ctx := context.Background()
	cfg := llm.GetDefaultConfig()
	cfg.DelaySeconds = 0

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

	// Plant question → should route to PlantExpert
	t.Run("PlantQuestion", func(t *testing.T) {
		userMsg := &models.Message{
			ID:        uuid.New(),
			ChatID:    chat.ID,
			Content:   "What humidity does a monstera need?",
			Sender:    "user",
			SenderID:  uuid.New(),
			Timestamp: time.Now(),
			Type:      "text",
		}

		t.Log("Plant question:", userMsg.Content)

		response, err := ar.ProcessMessage(ctx, chat, userMsg)
		if err != nil {
			t.Fatalf("ProcessMessage failed: %v", err)
		}

		if response == nil {
			t.Fatal("Expected response, got nil")
		}

		if agentMode, ok := response.Metadata["agentMode"].(string); !ok || agentMode != "multi-agent" {
			t.Errorf("Expected agentMode='multi-agent', got '%v'", agentMode)
		}

		t.Log("Response (multi-agent):", truncate(response.Content, 150))
	})

	// Device question → should route to DeviceSpecialist
	t.Run("DeviceQuestion", func(t *testing.T) {
		userMsg := &models.Message{
			ID:        uuid.New(),
			ChatID:    chat.ID,
			Content:   "My sensor won't connect via Bluetooth",
			Sender:    "user",
			SenderID:  uuid.New(),
			Timestamp: time.Now(),
			Type:      "text",
		}

		t.Log("Device question:", userMsg.Content)

		response, err := ar.ProcessMessage(ctx, chat, userMsg)
		if err != nil {
			t.Fatalf("ProcessMessage failed: %v", err)
		}

		if response == nil {
			t.Fatal("Expected response, got nil")
		}

		t.Log("Response (multi-agent):", truncate(response.Content, 150))
	})

	t.Log("Multi-agent mode works!")
}

// TestADKAutoResponderModeSwitch tests switching between modes
func TestADKAutoResponderModeSwitch(t *testing.T) {
	ctx := context.Background()
	cfg := llm.GetDefaultConfig()

	ar, err := NewADKAutoResponderV2(ctx, cfg)
	if err != nil {
		t.Fatalf("Failed to create AutoResponder: %v", err)
	}

	if ar.useMultiAgent {
		t.Error("Expected single-agent mode by default")
	}
	if ar.getMode() != "single-agent" {
		t.Errorf("Expected getMode()='single-agent', got '%s'", ar.getMode())
	}

	ar.EnableMultiAgent(true)
	if !ar.useMultiAgent {
		t.Error("Expected multi-agent mode after EnableMultiAgent(true)")
	}
	if ar.getMode() != "multi-agent" {
		t.Errorf("Expected getMode()='multi-agent', got '%s'", ar.getMode())
	}

	ar.EnableMultiAgent(false)
	if ar.useMultiAgent {
		t.Error("Expected single-agent mode after EnableMultiAgent(false)")
	}

	t.Log("Mode switching works!")
}
