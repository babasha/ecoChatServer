package llm

import (
	"context"
	"testing"
	"time"
)

// TestOpenAIAdapter_DetectAndTranslate_LocalLLM тестирует DetectAndTranslate с локальной LM Studio
func TestOpenAIAdapter_DetectAndTranslate_LocalLLM(t *testing.T) {
	// Настройка клиента для локальной LM Studio
	adapter := NewOpenAIAdapter(
		"http://127.0.0.1:1234/v1", // LM Studio endpoint
		"",                          // API key не нужен для локальной LLM
		"local-model",               // Название модели (можно любое, LM Studio использует загруженную модель)
		30*time.Second,
	)

	if err := adapter.Initialize(); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	tests := []struct {
		name       string
		text       string
		targetLang string
		wantErr    bool
	}{
		{
			name:       "Ukrainian to Russian",
			text:       "Привіт, як справи?",
			targetLang: "Russian",
			wantErr:    false,
		},
		{
			name:       "English to Russian",
			text:       "Hello, how are you?",
			targetLang: "Russian",
			wantErr:    false,
		},
		{
			name:       "Russian to English",
			text:       "Привет, как дела?",
			targetLang: "English",
			wantErr:    false,
		},
		{
			name:       "Polish to Russian",
			text:       "Dzień dobry, jak się masz?",
			targetLang: "Russian",
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			result, err := adapter.DetectAndTranslate(ctx, tt.text, tt.targetLang)

			if (err != nil) != tt.wantErr {
				t.Errorf("DetectAndTranslate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if err != nil {
				return
			}

			if result == nil {
				t.Fatal("DetectAndTranslate() returned nil result")
			}

			if result.DetectedLang == "" {
				t.Error("DetectAndTranslate() detected language is empty")
			}

			if result.Translation == "" {
				t.Error("DetectAndTranslate() translation is empty")
			}

			t.Logf("✓ Original: %s", tt.text)
			t.Logf("✓ Detected language: %s", result.DetectedLang)
			t.Logf("✓ Translation: %s", result.Translation)
		})
	}
}

// TestOpenAIAdapter_TranslateText_LocalLLM тестирует простой перевод с локальной LLM
func TestOpenAIAdapter_TranslateText_LocalLLM(t *testing.T) {
	adapter := NewOpenAIAdapter(
		"http://127.0.0.1:1234/v1",
		"",
		"local-model",
		30*time.Second,
	)

	if err := adapter.Initialize(); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	tests := []struct {
		name     string
		text     string
		fromLang string
		toLang   string
	}{
		{
			name:     "Russian to English",
			text:     "Привет, мир!",
			fromLang: "Russian",
			toLang:   "English",
		},
		{
			name:     "English to Russian",
			text:     "Hello, world!",
			fromLang: "English",
			toLang:   "Russian",
		},
		{
			name:     "Ukrainian to Russian",
			text:     "Доброго ранку!",
			fromLang: "Ukrainian",
			toLang:   "Russian",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			translation, err := adapter.TranslateText(ctx, tt.text, tt.fromLang, tt.toLang)
			if err != nil {
				t.Fatalf("TranslateText() error = %v", err)
			}

			if translation == "" {
				t.Error("TranslateText() returned empty translation")
			}

			t.Logf("✓ %s → %s: %s → %s", tt.fromLang, tt.toLang, tt.text, translation)
		})
	}
}

// TestOpenAIAdapter_TranslateBatch_LocalLLM тестирует batch перевод с локальной LLM
func TestOpenAIAdapter_TranslateBatch_LocalLLM(t *testing.T) {
	adapter := NewOpenAIAdapter(
		"http://127.0.0.1:1234/v1",
		"",
		"local-model",
		60*time.Second, // Больше timeout для batch
	)

	if err := adapter.Initialize(); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	tests := []struct {
		name     string
		texts    []string
		fromLang string
		toLang   string
	}{
		{
			name: "Batch Ukrainian to Russian",
			texts: []string{
				"Доброго ранку",
				"Як твої справи?",
				"Дякую за допомогу",
			},
			fromLang: "Ukrainian",
			toLang:   "Russian",
		},
		{
			name: "Batch English to Russian",
			texts: []string{
				"Good morning",
				"How are you?",
				"Thank you",
			},
			fromLang: "English",
			toLang:   "Russian",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			translations, err := adapter.TranslateBatch(ctx, tt.texts, tt.fromLang, tt.toLang)
			if err != nil {
				t.Fatalf("TranslateBatch() error = %v", err)
			}

			if len(translations) != len(tt.texts) {
				t.Errorf("TranslateBatch() returned %d translations, want %d", len(translations), len(tt.texts))
			}

			for i, translation := range translations {
				if translation == "" {
					t.Errorf("TranslateBatch() translation[%d] is empty", i)
				}
				t.Logf("  [%d] %s → %s", i+1, tt.texts[i], translation)
			}
		})
	}
}
