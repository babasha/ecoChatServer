package adkagent

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/egor/ecochatserver/llm"
	"github.com/egor/ecochatserver/models"
	"github.com/google/uuid"
)

// TestLLMQuality10Questions sends 10 real customer questions to the agent
// via LM Studio (Qwen) and prints responses for manual quality analysis.
//
// Run: LLM_PROVIDER=lmstudio go test -v ./adkagent -run TestLLMQuality10Questions -timeout 300s
func TestLLMQuality10Questions(t *testing.T) {
	// Force LM Studio provider
	os.Setenv("LLM_PROVIDER", "lmstudio")
	os.Setenv("LMSTUDIO_BASE_URL", "http://127.0.0.1:1234/v1")
	os.Setenv("LMSTUDIO_MODEL", "qwen/qwen3-vl-8b")

	ctx := context.Background()
	cfg := llm.GetDefaultConfig()
	cfg.DelaySeconds = 0

	ar, err := NewADKAutoResponderV2(ctx, cfg)
	if err != nil {
		t.Fatalf("Failed to create AutoResponder: %v", err)
	}

	questions := []struct {
		id    int
		lang  string
		query string
	}{
		{1, "RU", "Какой чип используется в датчике Zefir?"},
		{2, "RU", "Сколько держит батарея датчика?"},
		{3, "EN", "How do I set up my first Zefir sensor?"},
		{4, "RU", "Как сбросить датчик до заводских настроек?"},
		{5, "EN", "What does the blue LED on the sensor mean?"},
		{6, "RU", "Как работает mesh сеть между датчиками?"},
		{7, "EN", "What humidity range does a monstera need?"},
		{8, "RU", "Мой датчик показывает 0% влажности, что делать?"},
		{9, "EN", "Is my data encrypted? How is it stored?"},
		{10, "RU", "Можно ли использовать датчик на улице?"},
	}

	t.Logf("\n%s", "====================================================================================================")
	t.Logf("  LLM QUALITY TEST — 10 real customer questions via LM Studio (Qwen)")
	t.Logf("%s\n", "====================================================================================================")

	for _, q := range questions {
		t.Run(fmt.Sprintf("Q%d_%s", q.id, q.lang), func(t *testing.T) {
			chat := &models.Chat{
				ID:                   uuid.New(),
				Source:               "telegram",
				AutoResponderEnabled: true,
				AssignedTo:           nil,
				Metadata:             make(map[string]interface{}),
			}

			userMsg := &models.Message{
				ID:        uuid.New(),
				ChatID:    chat.ID,
				Content:   q.query,
				Sender:    "user",
				SenderID:  uuid.New(),
				Timestamp: time.Now(),
				Type:      "text",
			}

			start := time.Now()
			response, err := ar.ProcessMessage(ctx, chat, userMsg)
			elapsed := time.Since(start)

			t.Logf("\n  ── Q%d [%s] ──────────────────────────────────────────", q.id, q.lang)
			t.Logf("  Question: %s", q.query)

			if err != nil {
				t.Logf("  ERROR: %v", err)
				return
			}
			if response == nil {
				t.Logf("  ERROR: nil response")
				return
			}

			t.Logf("  Answer:   %s", response.Content)
			t.Logf("  Time:     %v", elapsed.Round(time.Millisecond))
			t.Logf("  Length:   %d chars", len(response.Content))
		})
	}
}
