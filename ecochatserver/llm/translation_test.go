package llm

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestDetectAndTranslate проверяет функцию DetectAndTranslate с retry логикой
func TestDetectAndTranslate(t *testing.T) {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		t.Skip("GEMINI_API_KEY not set, skipping test")
	}

	client := NewGeminiClient()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	tests := []struct {
		name       string
		text       string
		targetLang string
		wantLang   string
	}{
		{
			name:       "Ukrainian to Russian",
			text:       "Привіт, як справи?",
			targetLang: "ru",
			wantLang:   "uk",
		},
		{
			name:       "English to Russian",
			text:       "Hello, how are you?",
			targetLang: "ru",
			wantLang:   "en",
		},
		{
			name:       "Russian to English",
			text:       "Привет, как дела?",
			targetLang: "en",
			wantLang:   "ru",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := client.DetectAndTranslate(ctx, tt.text, tt.targetLang)
			if err != nil {
				t.Fatalf("DetectAndTranslate() error = %v", err)
			}

			if result == nil {
				t.Fatal("DetectAndTranslate() returned nil result")
			}

			if result.DetectedLang != tt.wantLang {
				t.Errorf("DetectAndTranslate() detected language = %v, want %v", result.DetectedLang, tt.wantLang)
			}

			if result.Translation == "" {
				t.Error("DetectAndTranslate() translation is empty")
			}

			t.Logf("✓ Detected: %s, Translation: %s", result.DetectedLang, result.Translation)
		})
	}
}

// TestTranslateBatch проверяет batch перевод с JSON парсингом
func TestTranslateBatch(t *testing.T) {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		t.Skip("GEMINI_API_KEY not set, skipping test")
	}

	client := NewGeminiClient()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	tests := []struct {
		name     string
		texts    []string
		fromLang string
		toLang   string
		wantLen  int
	}{
		{
			name: "Batch 3 messages - Ukrainian to Russian",
			texts: []string{
				"Доброго ранку",
				"Як твої справи?",
				"Дякую за допомогу",
			},
			fromLang: "uk",
			toLang:   "ru",
			wantLen:  3,
		},
		{
			name: "Batch 5 messages - English to Russian",
			texts: []string{
				"Good morning",
				"How are you?",
				"Thank you",
				"See you later",
				"Have a nice day",
			},
			fromLang: "en",
			toLang:   "ru",
			wantLen:  5,
		},
		{
			name: "Batch 12 messages - Russian to English (tests 10+ parsing fix)",
			texts: []string{
				"Привет",
				"Как дела?",
				"Спасибо",
				"Пожалуйста",
				"До свидания",
				"Доброе утро",
				"Добрый день",
				"Добрый вечер",
				"Спокойной ночи",
				"Извините",
				"Не за что",
				"Пока",
			},
			fromLang: "ru",
			toLang:   "en",
			wantLen:  12,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			translations, err := client.TranslateBatch(ctx, tt.texts, tt.fromLang, tt.toLang)
			if err != nil {
				t.Fatalf("TranslateBatch() error = %v", err)
			}

			if len(translations) != tt.wantLen {
				t.Errorf("TranslateBatch() returned %d translations, want %d", len(translations), tt.wantLen)
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

// TestTranslateText проверяет базовый перевод
func TestTranslateText(t *testing.T) {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		t.Skip("GEMINI_API_KEY not set, skipping test")
	}

	client := NewGeminiClient()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	translation, err := client.TranslateText(ctx, "Привет, мир!", "ru", "en")
	if err != nil {
		t.Fatalf("TranslateText() error = %v", err)
	}

	if translation == "" {
		t.Error("TranslateText() returned empty translation")
	}

	t.Logf("✓ Translation: %s", translation)
}
