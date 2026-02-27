package adkagent

import (
	"context"
	"testing"

	"github.com/egor/ecochatserver/llm"
)

// TestAgentCreation tests creating a Zefir support agent
func TestAgentCreation(t *testing.T) {
	ctx := context.Background()
	zefirClient := NewZefirClient()

	agent, err := NewSupportAgent(ctx, zefirClient)
	if err != nil {
		t.Skipf("Skipping test: %v (LLM provider may not be configured)", err)
		return
	}

	if agent == nil {
		t.Fatal("Agent should not be nil")
	}

	t.Log("Zefir agent created successfully")
}

// TestADKAutoResponderCreation tests creating the AutoResponder
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

	if ar.zefirClient == nil {
		t.Fatal("ZefirClient should not be nil")
	}

	t.Log("Zefir ADK AutoResponder created successfully")
	t.Logf("   Config: enabled=%v, botName=%s", ar.config.Enabled, ar.config.BotName)
}

// Example_simpleAgent demonstrates basic usage
func Example_simpleAgent() {
	ctx := context.Background()
	cfg := llm.GetDefaultConfig()

	// Create Zefir ADK AutoResponder
	ar, err := NewADKAutoResponderV2(ctx, cfg)
	if err != nil {
		panic(err)
	}

	_ = ar

	// Output:
}
