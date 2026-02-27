package adkagent

import (
	"context"
	"log"
	"os"

	"github.com/egor/ecochatserver/llm"
)

// InitADKAutoResponder creates an ADK V2 AutoResponder for Zefir
func InitADKAutoResponder(ctx context.Context, cfg llm.AutoResponderConfig) (*ADKAutoResponderV2, error) {
	log.Printf("[AGENT] Initializing Zefir ADK AutoResponder V2")
	log.Printf("[AGENT] Config: enabled=%v, botName=%s, delaySeconds=%d",
		cfg.Enabled, cfg.BotName, cfg.DelaySeconds)

	ar, err := NewADKAutoResponderV2(ctx, cfg)
	if err != nil {
		return nil, err
	}

	log.Printf("[AGENT] Zefir ADK AutoResponder V2 initialized successfully")
	return ar, nil
}

// ShouldUseADK checks whether to use ADK agent
func ShouldUseADK() bool {
	useADK := os.Getenv("USE_ADK_AGENT")
	return useADK == "true" || useADK == "1"
}
