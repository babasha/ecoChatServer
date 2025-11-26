package adkagent

import (
	"context"
	"testing"

	"github.com/egor/ecochatserver/llm"
)

// TestAgentCreation проверяет создание агента
func TestAgentCreation(t *testing.T) {
	ctx := context.Background()
	storeClient := llm.NewStoreClient()

	// Тестируем создание неавторизованного агента
	agent, err := NewSupportAgent(ctx, storeClient, false)
	if err != nil {
		t.Skipf("Skipping test: %v (возможно не установлен GEMINI_API_KEY)", err)
		return
	}

	if agent == nil {
		t.Fatal("Agent should not be nil")
	}

	t.Log("✅ Агент успешно создан")
}

// TestADKAutoResponderCreation проверяет создание AutoResponder на базе ADK
func TestADKAutoResponderCreation(t *testing.T) {
	ctx := context.Background()
	cfg := llm.GetDefaultConfig()

	ar, err := NewADKAutoResponderV2(ctx, cfg)
	if err != nil {
		t.Fatalf("Failed to create ADK AutoResponder: %v", err)
	}

	if ar == nil {
		t.Fatal("ADK AutoResponder should not be nil")
	}

	if ar.storeClient == nil {
		t.Fatal("StoreClient should not be nil")
	}

	t.Log("✅ ADK AutoResponder успешно создан")
	t.Logf("   Config: enabled=%v, botName=%s", ar.config.Enabled, ar.config.BotName)
}

// TestToolsCreation temporarily disabled (will add tools later)
// func TestToolsCreation(t *testing.T) {
// 	// Will implement after adding tools
// }

// Example_simpleAgent демонстрирует простое использование
func Example_simpleAgent() {
	ctx := context.Background()
	cfg := llm.GetDefaultConfig()

	// Создаём ADK AutoResponder
	ar, err := NewADKAutoResponderV2(ctx, cfg)
	if err != nil {
		panic(err)
	}

	// Используем как обычный AutoResponder
	_ = ar

	// Output:
}
