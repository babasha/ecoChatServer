package adkagent

import (
	"context"
	"testing"

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

