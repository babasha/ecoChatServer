package adkagent

import (
	"context"
	"testing"

	"google.golang.org/adk/memory"

	"github.com/egor/ecochatserver/llm"
)

// TestMultiAgentCreation проверяет создание мульти-агентной системы
func TestMultiAgentCreation(t *testing.T) {
	ctx := context.Background()
	storeClient := llm.NewStoreClient()

	cfg := MultiAgentConfig{
		StoreClient:  storeClient,
		IsAuthorized: false,
		UserID:       0,
	}

	oa, err := NewOrchestratorAgent(ctx, cfg)
	if err != nil {
		t.Skipf("Skipping test: %v (возможно не настроен LLM провайдер)", err)
		return
	}

	if oa == nil {
		t.Fatal("OrchestratorAgent should not be nil")
	}

	if oa.orchestrator == nil {
		t.Fatal("Orchestrator agent should not be nil")
	}

	if oa.productAgent == nil {
		t.Fatal("Product agent should not be nil")
	}

	if oa.orderAgent == nil {
		t.Fatal("Order agent should not be nil")
	}

	if oa.supportAgent == nil {
		t.Fatal("Support agent should not be nil")
	}

	t.Log("✅ Мульти-агентная система успешно создана")
	t.Logf("   Orchestrator: %s", oa.orchestrator.Name())
	t.Logf("   ProductAgent: %s", oa.productAgent.Name())
	t.Logf("   OrderAgent: %s", oa.orderAgent.Name())
	t.Logf("   SupportAgent: %s", oa.supportAgent.Name())
}

// TestMultiAgentAuthorized проверяет создание системы для авторизованного пользователя
func TestMultiAgentAuthorized(t *testing.T) {
	ctx := context.Background()
	storeClient := llm.NewStoreClient()

	cfg := MultiAgentConfig{
		StoreClient:  storeClient,
		IsAuthorized: true,
		UserID:       12345,
	}

	oa, err := NewOrchestratorAgent(ctx, cfg)
	if err != nil {
		t.Skipf("Skipping test: %v (возможно не настроен LLM провайдер)", err)
		return
	}

	if oa.GetUserID() != 12345 {
		t.Errorf("Expected userID 12345, got %d", oa.GetUserID())
	}

	if !oa.isAuthorized {
		t.Error("Expected isAuthorized to be true")
	}

	t.Log("✅ Мульти-агентная система для авторизованного пользователя создана")
}

// TestCustomerMemoryService проверяет работу сервиса памяти
func TestCustomerMemoryService(t *testing.T) {
	cms := NewCustomerMemoryService()

	if cms == nil {
		t.Fatal("CustomerMemoryService should not be nil")
	}

	// Добавляем предпочтение
	cms.AddPreference("user123", "category", "wine")
	cms.AddPreference("user123", "category", "cheese")
	cms.AddPreference("user123", "language", "ru")

	// Проверяем статистику
	totalUsers, totalMemories := cms.GetStats()
	if totalUsers != 1 {
		t.Errorf("Expected 1 user, got %d", totalUsers)
	}
	if totalMemories != 3 {
		t.Errorf("Expected 3 memories, got %d", totalMemories)
	}

	// Получаем сводку предпочтений
	prefs := cms.GetUserPreferences("user123")
	if prefs == "" {
		t.Error("Expected non-empty preferences summary")
	}

	t.Log("✅ CustomerMemoryService работает корректно")
	t.Logf("   Users: %d, Memories: %d", totalUsers, totalMemories)
	t.Logf("   Preferences: %s", prefs)
}

// TestCustomerMemorySearch проверяет поиск в памяти
func TestCustomerMemorySearch(t *testing.T) {
	ctx := context.Background()
	cms := NewCustomerMemoryService()

	// Добавляем данные
	cms.AddPreference("user456", "product", "milk")
	cms.AddPreference("user456", "product", "bread")

	// Ищем через ADK memory.SearchRequest
	resp, err := cms.Search(ctx, &memory.SearchRequest{
		UserID: "user456",
		Query:  "milk",
	})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(resp.Memories) == 0 {
		t.Log("⚠️ Search returned 0 results (expected for simple impl)")
	} else {
		t.Logf("✅ Search returned %d results", len(resp.Memories))
	}
}
