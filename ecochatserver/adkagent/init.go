package adkagent

import (
	"context"
	"log"
	"os"

	"github.com/egor/ecochatserver/llm"
)

// InitADKAutoResponder создаёт AutoResponder на базе ADK V2
// Можно использовать вместо llm.NewAutoResponderWithConfig()
func InitADKAutoResponder(ctx context.Context, cfg llm.AutoResponderConfig) (*ADKAutoResponderV2, error) {
	log.Printf("[AGENT] Инициализация ADK AutoResponder V2")
	log.Printf("[AGENT] Config: enabled=%v, botName=%s, delaySeconds=%d",
		cfg.Enabled, cfg.BotName, cfg.DelaySeconds)

	ar, err := NewADKAutoResponderV2(ctx, cfg)
	if err != nil {
		return nil, err
	}

	log.Printf("[AGENT] ADK AutoResponder V2 успешно инициализирован")
	return ar, nil
}

// ShouldUseADK проверяет нужно ли использовать ADK агента
// (можно управлять через переменную окружения)
func ShouldUseADK() bool {
	useADK := os.Getenv("USE_ADK_AGENT")
	return useADK == "true" || useADK == "1"
}
