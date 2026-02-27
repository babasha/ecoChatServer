package adkagent

import (
	"context"
	"testing"
)

// TestMultiAgentCreation tests creation of the multi-agent system
func TestMultiAgentCreation(t *testing.T) {
	ctx := context.Background()
	zefirClient := NewZefirClient()

	cfg := MultiAgentConfig{
		ZefirClient: zefirClient,
		ZefirUserID: "",
	}

	oa, err := NewOrchestratorAgent(ctx, cfg)
	if err != nil {
		t.Skipf("Skipping test: %v (LLM provider may not be configured)", err)
		return
	}

	if oa == nil {
		t.Fatal("OrchestratorAgent should not be nil")
	}

	if oa.orchestrator == nil {
		t.Fatal("Orchestrator agent should not be nil")
	}

	if oa.plantAgent == nil {
		t.Fatal("Plant agent should not be nil")
	}

	if oa.deviceAgent == nil {
		t.Fatal("Device agent should not be nil")
	}

	if oa.supportAgent == nil {
		t.Fatal("Support agent should not be nil")
	}

	t.Log("Multi-agent system created successfully")
	t.Logf("   Orchestrator: %s", oa.orchestrator.Name())
	t.Logf("   PlantAgent: %s", oa.plantAgent.Name())
	t.Logf("   DeviceAgent: %s", oa.deviceAgent.Name())
	t.Logf("   SupportAgent: %s", oa.supportAgent.Name())
}

// TestMultiAgentWithUser tests creation for a user with Zefir account
func TestMultiAgentWithUser(t *testing.T) {
	ctx := context.Background()
	zefirClient := NewZefirClient()

	cfg := MultiAgentConfig{
		ZefirClient: zefirClient,
		ZefirUserID: "user-abc-123",
	}

	oa, err := NewOrchestratorAgent(ctx, cfg)
	if err != nil {
		t.Skipf("Skipping test: %v (LLM provider may not be configured)", err)
		return
	}

	if oa.GetZefirUserID() != "user-abc-123" {
		t.Errorf("Expected zefirUserID 'user-abc-123', got '%s'", oa.GetZefirUserID())
	}

	t.Log("Multi-agent system for Zefir user created successfully")
}
