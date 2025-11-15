package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TokenBenchmarkResult результаты замера токенов
type TokenBenchmarkResult struct {
	Format           string
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	LatencyMs        int64
	Success          bool
	Response         string
}

// testLMStudio проверяет подключение к LM Studio
func testLMStudio(t *testing.T) {
	resp, err := http.Get("http://127.0.0.1:1234/v1/models")
	if err != nil {
		t.Fatalf("LM Studio не запущен: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("LM Studio вернул ошибку: %d", resp.StatusCode)
	}

	t.Log("✅ LM Studio подключен и работает")
}

// callLMStudioJSON вызывает LM Studio с JSON форматом
func callLMStudioJSON(ctx context.Context, text, targetLang string) (*TokenBenchmarkResult, error) {
	start := time.Now()

	// JSON промпт (как в твоем коде gemini_translation.go:138)
	prompt := fmt.Sprintf(`Detect the language of the text and translate it to %s.

Text: "%s"

Reply in JSON format:
{
  "detectedLang": "two-letter language code (ru, en, pl, etc.)",
  "translation": "translated text"
}

If text is already in %s, return it as translation with detected language.`, targetLang, text, targetLang)

	reqBody := map[string]interface{}{
		"model": "google/gemma-3-12b",
		"messages": []map[string]string{
			{
				"role":    "system",
				"content": "You are a language detection and translation expert. Always reply in valid JSON format only.",
			},
			{
				"role":    "user",
				"content": prompt,
			},
		},
		"temperature": 0.3,
		"max_tokens":  2048,
	}

	payload, _ := json.Marshal(reqBody)
	req, _ := http.NewRequestWithContext(ctx, "POST", "http://127.0.0.1:1234/v1/chat/completions", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	latency := time.Since(start).Milliseconds()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error: %d - %s", resp.StatusCode, string(body))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode failed: %w", err)
	}

	responseText := ""
	if len(result.Choices) > 0 {
		responseText = result.Choices[0].Message.Content
	}

	return &TokenBenchmarkResult{
		Format:           "JSON",
		PromptTokens:     result.Usage.PromptTokens,
		CompletionTokens: result.Usage.CompletionTokens,
		TotalTokens:      result.Usage.TotalTokens,
		LatencyMs:        latency,
		Success:          true,
		Response:         responseText,
	}, nil
}

// callLMStudioTOON вызывает LM Studio с TOON форматом
func callLMStudioTOON(ctx context.Context, text, targetLang string) (*TokenBenchmarkResult, error) {
	start := time.Now()

	// TOON промпт (компактный)
	prompt := fmt.Sprintf(`Detect language and translate to %s: "%s"

Reply in TOON format (NO JSON, NO MARKDOWN):
lang: <code>
text: <translation>`, targetLang, text)

	reqBody := map[string]interface{}{
		"model": "google/gemma-3-12b",
		"messages": []map[string]string{
			{
				"role":    "system",
				"content": "Language translator. Use TOON format: key: value",
			},
			{
				"role":    "user",
				"content": prompt,
			},
		},
		"temperature": 0.3,
		"max_tokens":  2048,
	}

	payload, _ := json.Marshal(reqBody)
	req, _ := http.NewRequestWithContext(ctx, "POST", "http://127.0.0.1:1234/v1/chat/completions", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	latency := time.Since(start).Milliseconds()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error: %d - %s", resp.StatusCode, string(body))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode failed: %w", err)
	}

	responseText := ""
	if len(result.Choices) > 0 {
		responseText = result.Choices[0].Message.Content
	}

	return &TokenBenchmarkResult{
		Format:           "TOON",
		PromptTokens:     result.Usage.PromptTokens,
		CompletionTokens: result.Usage.CompletionTokens,
		TotalTokens:      result.Usage.TotalTokens,
		LatencyMs:        latency,
		Success:          true,
		Response:         responseText,
	}, nil
}

// TestTokenUsageComparison сравнивает расход токенов JSON vs TOON
func TestTokenUsageComparison(t *testing.T) {
	// Проверяем подключение
	testLMStudio(t)

	// Тестовые данные
	testCases := []struct {
		text       string
		targetLang string
		name       string
	}{
		{"Hello world", "Russian", "Short English"},
		{"Привет мир", "English", "Short Russian"},
		{"How are you doing today?", "Spanish", "Medium English"},
		{"The quick brown fox jumps over the lazy dog", "French", "Long English"},
		{"Я очень рад встрече с вами сегодня", "English", "Long Russian"},
	}

	ctx := context.Background()
	var jsonResults []TokenBenchmarkResult
	var toonResults []TokenBenchmarkResult

	t.Log("\n🔬 Начинаем тестирование JSON vs TOON\n")

	for _, tc := range testCases {
		t.Logf("📝 Тест: %s", tc.name)

		// JSON тест
		t.Log("  Testing JSON format...")
		jsonResult, err := callLMStudioJSON(ctx, tc.text, tc.targetLang)
		if err != nil {
			t.Errorf("  ❌ JSON failed: %v", err)
			continue
		}
		jsonResults = append(jsonResults, *jsonResult)
		t.Logf("  ✅ JSON: %d tokens (prompt: %d, completion: %d) - %dms",
			jsonResult.TotalTokens, jsonResult.PromptTokens, jsonResult.CompletionTokens, jsonResult.LatencyMs)

		// Пауза между запросами
		time.Sleep(500 * time.Millisecond)

		// TOON тест
		t.Log("  Testing TOON format...")
		toonResult, err := callLMStudioTOON(ctx, tc.text, tc.targetLang)
		if err != nil {
			t.Errorf("  ❌ TOON failed: %v", err)
			continue
		}
		toonResults = append(toonResults, *toonResult)
		t.Logf("  ✅ TOON: %d tokens (prompt: %d, completion: %d) - %dms",
			toonResult.TotalTokens, toonResult.PromptTokens, toonResult.CompletionTokens, toonResult.LatencyMs)

		// Сравнение
		savings := float64(jsonResult.TotalTokens-toonResult.TotalTokens) / float64(jsonResult.TotalTokens) * 100
		t.Logf("  💰 Экономия: %.1f%% (%d tokens)\n",
			savings, jsonResult.TotalTokens-toonResult.TotalTokens)

		time.Sleep(500 * time.Millisecond)
	}

	// Итоговая статистика
	t.Log("\n" + strings.Repeat("=", 80))
	t.Log("📊 ИТОГОВАЯ СТАТИСТИКА")
	t.Log(strings.Repeat("=", 80))

	if len(jsonResults) > 0 && len(toonResults) > 0 {
		var jsonTotal, toonTotal int
		var jsonPrompt, toonPrompt int
		var jsonCompletion, toonCompletion int

		for i := 0; i < len(jsonResults); i++ {
			jsonTotal += jsonResults[i].TotalTokens
			jsonPrompt += jsonResults[i].PromptTokens
			jsonCompletion += jsonResults[i].CompletionTokens

			toonTotal += toonResults[i].TotalTokens
			toonPrompt += toonResults[i].PromptTokens
			toonCompletion += toonResults[i].CompletionTokens
		}

		t.Logf("\n📈 JSON формат:")
		t.Logf("   Всего токенов: %d", jsonTotal)
		t.Logf("   Prompt токены: %d", jsonPrompt)
		t.Logf("   Response токены: %d", jsonCompletion)

		t.Logf("\n📉 TOON формат:")
		t.Logf("   Всего токенов: %d", toonTotal)
		t.Logf("   Prompt токены: %d", toonPrompt)
		t.Logf("   Response токены: %d", toonCompletion)

		totalSavings := float64(jsonTotal-toonTotal) / float64(jsonTotal) * 100
		promptSavings := float64(jsonPrompt-toonPrompt) / float64(jsonPrompt) * 100
		completionSavings := float64(jsonCompletion-toonCompletion) / float64(jsonCompletion) * 100

		t.Logf("\n💰 ЭКОНОМИЯ:")
		t.Logf("   Общая: %.1f%% (%d tokens)", totalSavings, jsonTotal-toonTotal)
		t.Logf("   На промптах: %.1f%% (%d tokens)", promptSavings, jsonPrompt-toonPrompt)
		t.Logf("   На ответах: %.1f%% (%d tokens)", completionSavings, jsonCompletion-toonCompletion)

		t.Logf("\n💵 СТОИМОСТЬ (пример для gpt-4o-mini: $0.15/1M input, $0.60/1M output):")
		jsonCost := float64(jsonPrompt)*0.15/1000000 + float64(jsonCompletion)*0.60/1000000
		toonCost := float64(toonPrompt)*0.15/1000000 + float64(toonCompletion)*0.60/1000000
		t.Logf("   JSON: $%.6f", jsonCost)
		t.Logf("   TOON: $%.6f", toonCost)
		t.Logf("   Экономия: $%.6f (%.1f%%)", jsonCost-toonCost, (jsonCost-toonCost)/jsonCost*100)

		t.Logf("\n📊 При 10,000 переводов/день:")
		jsonCostDaily := jsonCost * 10000
		toonCostDaily := toonCost * 10000
		t.Logf("   JSON: $%.2f/день, $%.2f/месяц", jsonCostDaily, jsonCostDaily*30)
		t.Logf("   TOON: $%.2f/день, $%.2f/месяц", toonCostDaily, toonCostDaily*30)
		t.Logf("   Экономия: $%.2f/день, $%.2f/месяц", jsonCostDaily-toonCostDaily, (jsonCostDaily-toonCostDaily)*30)

		t.Log(strings.Repeat("=", 80))
	}
}

// TestBatchTranslationComparison тест batch перевода
func TestBatchTranslationComparison(t *testing.T) {
	testLMStudio(t)

	texts := []string{
		"Hello",
		"How are you?",
		"Good morning",
		"Thank you",
		"See you later",
	}

	ctx := context.Background()

	t.Log("\n🔬 Тестирование BATCH перевода JSON vs TOON\n")

	// JSON Batch
	t.Log("📝 Testing JSON batch format...")
	jsonStart := time.Now()

	textsJSON, _ := json.Marshal(texts)
	jsonPrompt := fmt.Sprintf(`Translate the following texts from English to Russian.
Return a JSON array with translated texts in the same order.
Format: ["translation1", "translation2", ...]

Texts to translate:
%s`, string(textsJSON))

	jsonReqBody := map[string]interface{}{
		"model": "google/gemma-3-12b",
		"messages": []map[string]string{
			{
				"role":    "system",
				"content": "You are a translator. Reply with valid JSON only.",
			},
			{
				"role":    "user",
				"content": jsonPrompt,
			},
		},
		"temperature": 0.3,
		"max_tokens":  2048,
	}

	jsonPayload, _ := json.Marshal(jsonReqBody)
	jsonReq, _ := http.NewRequestWithContext(ctx, "POST", "http://127.0.0.1:1234/v1/chat/completions", bytes.NewReader(jsonPayload))
	jsonReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	jsonResp, err := client.Do(jsonReq)
	if err != nil {
		t.Fatalf("JSON request failed: %v", err)
	}

	var jsonResult struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}
	json.NewDecoder(jsonResp.Body).Decode(&jsonResult)
	jsonResp.Body.Close()
	jsonLatency := time.Since(jsonStart).Milliseconds()

	t.Logf("✅ JSON: %d tokens (prompt: %d, completion: %d) - %dms",
		jsonResult.Usage.TotalTokens, jsonResult.Usage.PromptTokens, jsonResult.Usage.CompletionTokens, jsonLatency)
	t.Logf("   Response: %s\n", jsonResult.Choices[0].Message.Content)

	time.Sleep(1 * time.Second)

	// TOON Batch
	t.Log("📝 Testing TOON batch format...")
	toonStart := time.Now()

	var sb strings.Builder
	sb.WriteString("Translate from English to Russian.\n\n")
	sb.WriteString("Input:\n")
	for i, text := range texts {
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, text))
	}
	sb.WriteString(fmt.Sprintf("\nReply in TOON format:\ntranslations[%d]:\n <translation1>\n <translation2>\n ...", len(texts)))

	toonReqBody := map[string]interface{}{
		"model": "google/gemma-3-12b",
		"messages": []map[string]string{
			{
				"role":    "system",
				"content": "Translator. Use TOON format for arrays.",
			},
			{
				"role":    "user",
				"content": sb.String(),
			},
		},
		"temperature": 0.3,
		"max_tokens":  2048,
	}

	toonPayload, _ := json.Marshal(toonReqBody)
	toonReq, _ := http.NewRequestWithContext(ctx, "POST", "http://127.0.0.1:1234/v1/chat/completions", bytes.NewReader(toonPayload))
	toonReq.Header.Set("Content-Type", "application/json")

	toonResp, err := client.Do(toonReq)
	if err != nil {
		t.Fatalf("TOON request failed: %v", err)
	}

	var toonResult struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}
	json.NewDecoder(toonResp.Body).Decode(&toonResult)
	toonResp.Body.Close()
	toonLatency := time.Since(toonStart).Milliseconds()

	t.Logf("✅ TOON: %d tokens (prompt: %d, completion: %d) - %dms",
		toonResult.Usage.TotalTokens, toonResult.Usage.PromptTokens, toonResult.Usage.CompletionTokens, toonLatency)
	t.Logf("   Response: %s\n", toonResult.Choices[0].Message.Content)

	// Сравнение
	savings := float64(jsonResult.Usage.TotalTokens-toonResult.Usage.TotalTokens) / float64(jsonResult.Usage.TotalTokens) * 100
	t.Logf("\n💰 BATCH ЭКОНОМИЯ: %.1f%% (%d tokens)",
		savings, jsonResult.Usage.TotalTokens-toonResult.Usage.TotalTokens)
}
