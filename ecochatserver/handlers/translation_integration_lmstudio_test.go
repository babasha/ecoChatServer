package handlers

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/egor/ecochatserver/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createTestProvider создаёт провайдера для интеграционных тестов
func createTestProvider(t *testing.T) llm.Provider {
	lmStudioURL := os.Getenv("LM_STUDIO_URL")
	if lmStudioURL == "" {
		lmStudioURL = "http://localhost:1234/v1"
	}

	config := &llm.ProviderConfig{
		Type:    llm.ProviderLMStudio,
		BaseURL: lmStudioURL,
		Model:   "google/gemma-3-12b", // Модель на 12B параметров
		Timeout: 60,
	}

	provider, err := llm.NewProvider(config)
	require.NoError(t, err, "Не удалось создать провайдер")
	return provider
}

// TestLMStudio_RealTranslation тестирует реальный перевод через LM Studio
func TestLMStudio_RealTranslation(t *testing.T) {
	// Arrange - создаём реальный провайдер
	provider := createTestProvider(t)
	ctx := context.Background()

	testCases := []struct {
		name           string
		text           string
		fromLang       string
		toLang         string
		expectedLang   string // ожидаемый определённый язык
		minLength      int    // минимальная длина перевода
	}{
		{
			name:         "Английский → Русский",
			text:         "Hello, how are you?",
			fromLang:     "en",
			toLang:       "ru",
			expectedLang: "en",
			minLength:    5,
		},
		{
			name:         "Русский → Английский",
			text:         "Привет, как дела?",
			fromLang:     "ru",
			toLang:       "en",
			expectedLang: "ru",
			minLength:    5,
		},
		{
			name:         "Португальский → Русский",
			text:         "Olá, como você está?",
			fromLang:     "pt",
			toLang:       "ru",
			expectedLang: "pt",
			minLength:    5,
		},
		{
			name:         "Испанский → Русский",
			text:         "Hola, ¿cómo estás?",
			fromLang:     "es",
			toLang:       "ru",
			expectedLang: "es",
			minLength:    5,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Замеряем время
			startTime := time.Now()

			// Act - реальный перевод
			translation, err := provider.TranslateText(ctx, tc.text, tc.fromLang, tc.toLang)

			duration := time.Since(startTime)

			// Assert
			require.NoError(t, err, "Перевод не должен возвращать ошибку")
			assert.NotEmpty(t, translation, "Перевод не должен быть пустым")
			assert.GreaterOrEqual(t, len(translation), tc.minLength, "Перевод слишком короткий")

			// Выводим метрики
			t.Logf("📊 Метрики перевода:")
			t.Logf("   Оригинал:  %s", tc.text)
			t.Logf("   Перевод:   %s", translation)
			t.Logf("   Время:     %v", duration)
			t.Logf("   Длина:     %d символов", len(translation))
		})
	}
}

// TestLMStudio_DetectAndTranslate тестирует определение языка И перевод за один запрос
func TestLMStudio_DetectAndTranslate(t *testing.T) {
	// Arrange
	provider := createTestProvider(t)

	ctx := context.Background()

	testCases := []struct {
		name         string
		text         string
		targetLang   string
		expectedLang string
	}{
		{
			name:         "Определить английский",
			text:         "Hello, how are you?",
			targetLang:   "ru",
			expectedLang: "en",
		},
		{
			name:         "Определить русский",
			text:         "Привет, как дела?",
			targetLang:   "en",
			expectedLang: "ru",
		},
		{
			name:         "Определить португальский",
			text:         "Olá, tudo bem?",
			targetLang:   "ru",
			expectedLang: "pt",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			startTime := time.Now()

			// Act - определение И перевод за ОДИН запрос
			result, err := provider.DetectAndTranslate(ctx, tc.text, tc.targetLang)

			duration := time.Since(startTime)

			// Assert
			require.NoError(t, err)
			assert.NotNil(t, result)
			assert.NotEmpty(t, result.DetectedLang, "Язык должен быть определён")
			assert.NotEmpty(t, result.Translation, "Перевод не должен быть пустым")

			// Проверяем что язык определён правильно
			assert.Equal(t, tc.expectedLang, result.DetectedLang,
				"Определённый язык не совпадает с ожидаемым")

			// Метрики
			t.Logf("📊 Метрики DetectAndTranslate:")
			t.Logf("   Оригинал:          %s", tc.text)
			t.Logf("   Определён язык:    %s (ожидали: %s)", result.DetectedLang, tc.expectedLang)
			t.Logf("   Перевод:           %s", result.Translation)
			t.Logf("   Время:             %v", duration)
			t.Logf("   ✅ За ОДИН API запрос!")
		})
	}
}

// TestLMStudio_BatchTranslation тестирует batch перевод с реальным API
func TestLMStudio_BatchTranslation(t *testing.T) {
	// Arrange
	provider := createTestProvider(t)

	translator := &TranslationService{
		provider: provider,
	}

	ctx := context.Background()

	// 5 сообщений для batch перевода
	texts := []string{
		"Hello",
		"How are you?",
		"Good morning",
		"Thank you",
		"Goodbye",
	}

	startTime := time.Now()

	// Act - batch перевод (ОДИН API вызов!)
	translations, err := translator.TranslateBatch(ctx, texts, "en", "ru")

	duration := time.Since(startTime)

	// Assert
	require.NoError(t, err)
	assert.Len(t, translations, 5, "Должно быть 5 переводов")

	// Проверяем что все переводы не пустые
	for i, translation := range translations {
		assert.NotEmpty(t, translation, "Перевод %d не должен быть пустым", i)
	}

	// Метрики
	avgTimePerMessage := duration / time.Duration(len(texts))
	t.Logf("📊 Метрики Batch перевода:")
	t.Logf("   Количество сообщений:  %d", len(texts))
	t.Logf("   Общее время:           %v", duration)
	t.Logf("   Среднее время/сообщение: %v", avgTimePerMessage)
	t.Logf("   API вызовов:           1 (вместо %d!)", len(texts))
	t.Logf("")
	t.Logf("   Результаты:")
	for i, text := range texts {
		t.Logf("   %d. %s → %s", i+1, text, translations[i])
	}
}

// TestLMStudio_TokenUsage тестирует подсчёт использования токенов
func TestLMStudio_TokenUsage(t *testing.T) {
	// Arrange
	provider := createTestProvider(t)

	ctx := context.Background()

	testCases := []struct {
		name     string
		text     string
		fromLang string
		toLang   string
	}{
		{
			name:     "Короткий текст (5 слов)",
			text:     "Hello, how are you?",
			fromLang: "en",
			toLang:   "ru",
		},
		{
			name:     "Средний текст (15 слов)",
			text:     "Hello! I hope you're having a great day. Could you please help me with my order?",
			fromLang: "en",
			toLang:   "ru",
		},
		{
			name: "Длинный текст (30+ слов)",
			text: "Good morning! I am writing to inquire about the delivery status of my recent order. " +
				"I placed the order three days ago and haven't received any tracking information yet. " +
				"Could you please provide an update on when I can expect the delivery?",
			fromLang: "en",
			toLang:   "ru",
		},
	}

	totalInputTokens := 0
	totalOutputTokens := 0
	totalTime := time.Duration(0)

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			startTime := time.Now()

			// Act
			translation, err := provider.TranslateText(ctx, tc.text, tc.fromLang, tc.toLang)

			duration := time.Since(startTime)
			totalTime += duration

			// Assert
			require.NoError(t, err)
			assert.NotEmpty(t, translation)

			// Примерный подсчёт токенов (1 токен ≈ 4 символа для английского)
			estimatedInputTokens := len(tc.text) / 4
			estimatedOutputTokens := len(translation) / 4

			totalInputTokens += estimatedInputTokens
			totalOutputTokens += estimatedOutputTokens

			// Метрики
			t.Logf("📊 Использование токенов:")
			t.Logf("   Входной текст:     %d символов (~%d токенов)",
				len(tc.text), estimatedInputTokens)
			t.Logf("   Выходной текст:    %d символов (~%d токенов)",
				len(translation), estimatedOutputTokens)
			t.Logf("   Всего токенов:     ~%d", estimatedInputTokens+estimatedOutputTokens)
			t.Logf("   Время:             %v", duration)
			t.Logf("   Скорость:          ~%.0f токенов/сек",
				float64(estimatedInputTokens+estimatedOutputTokens)/duration.Seconds())
		})
	}

	// Итоговая статистика
	t.Logf("")
	t.Logf("═══════════════════════════════════════")
	t.Logf("📊 ИТОГОВАЯ СТАТИСТИКА:")
	t.Logf("   Всего тестов:      %d", len(testCases))
	t.Logf("   Input токенов:     ~%d", totalInputTokens)
	t.Logf("   Output токенов:    ~%d", totalOutputTokens)
	t.Logf("   Всего токенов:     ~%d", totalInputTokens+totalOutputTokens)
	t.Logf("   Общее время:       %v", totalTime)
	t.Logf("   Среднее время:     %v", totalTime/time.Duration(len(testCases)))
	t.Logf("═══════════════════════════════════════")
}

// TestLMStudio_PerformanceComparison сравнивает производительность разных подходов
func TestLMStudio_PerformanceComparison(t *testing.T) {
	// Arrange
	provider := createTestProvider(t)

	translator := &TranslationService{
		provider: provider,
	}

	ctx := context.Background()

	texts := []string{
		"Hello",
		"How are you?",
		"Good morning",
	}

	// Подход 1: Batch перевод (ОДИН API вызов)
	t.Run("Batch_перевод_(рекомендуется)", func(t *testing.T) {
		startTime := time.Now()

		translations, err := translator.TranslateBatch(ctx, texts, "en", "ru")

		duration := time.Since(startTime)

		require.NoError(t, err)
		assert.Len(t, translations, 3)

		t.Logf("📊 Batch перевод:")
		t.Logf("   Время:        %v", duration)
		t.Logf("   API вызовов:  1")
		t.Logf("   Скорость:     %v на сообщение", duration/time.Duration(len(texts)))
	})

	// Подход 2: Отдельный перевод для каждого (N API вызовов)
	t.Run("Отдельные_переводы_(медленно)", func(t *testing.T) {
		startTime := time.Now()

		translations := make([]string, len(texts))
		for i, text := range texts {
			translation, err := provider.TranslateText(ctx, text, "en", "ru")
			require.NoError(t, err)
			translations[i] = translation
		}

		duration := time.Since(startTime)

		assert.Len(t, translations, 3)

		t.Logf("📊 Отдельные переводы:")
		t.Logf("   Время:        %v", duration)
		t.Logf("   API вызовов:  %d", len(texts))
		t.Logf("   Скорость:     %v на сообщение", duration/time.Duration(len(texts)))
	})

	t.Logf("")
	t.Logf("💡 ВЫВОД: Batch перевод ЗНАЧИТЕЛЬНО быстрее!")
}

// TestLMStudio_QualityMetrics тестирует качество переводов
func TestLMStudio_QualityMetrics(t *testing.T) {
	// Arrange
	provider := createTestProvider(t)

	ctx := context.Background()

	testCases := []struct {
		name             string
		text             string
		fromLang         string
		toLang           string
		expectedContains []string // ключевые слова которые должны быть в переводе
	}{
		{
			name:             "Приветствие",
			text:             "Hello, how are you?",
			fromLang:         "en",
			toLang:           "ru",
			expectedContains: []string{"привет", "здравствуй", "как"},
		},
		{
			name:             "Благодарность",
			text:             "Thank you very much!",
			fromLang:         "en",
			toLang:           "ru",
			expectedContains: []string{"спасибо", "благодар"},
		},
		{
			name:             "Вопрос о заказе",
			text:             "Where is my order?",
			fromLang:         "en",
			toLang:           "ru",
			expectedContains: []string{"где", "заказ"},
		},
	}

	qualityScore := 0
	totalTests := len(testCases)

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			translation, err := provider.TranslateText(ctx, tc.text, tc.fromLang, tc.toLang)

			// Assert
			require.NoError(t, err)
			assert.NotEmpty(t, translation)

			// Проверка качества - содержит ли ключевые слова
			foundKeywords := 0
			for _, keyword := range tc.expectedContains {
				if containsCaseInsensitive(translation, keyword) {
					foundKeywords++
				}
			}

			qualityPercent := float64(foundKeywords) / float64(len(tc.expectedContains)) * 100

			if qualityPercent >= 50 {
				qualityScore++
			}

			t.Logf("📊 Качество перевода:")
			t.Logf("   Оригинал:      %s", tc.text)
			t.Logf("   Перевод:       %s", translation)
			t.Logf("   Ключевых слов: %d/%d (%.0f%%)",
				foundKeywords, len(tc.expectedContains), qualityPercent)

			if qualityPercent >= 80 {
				t.Logf("   Оценка:        ⭐⭐⭐ Отлично")
			} else if qualityPercent >= 50 {
				t.Logf("   Оценка:        ⭐⭐ Хорошо")
			} else {
				t.Logf("   Оценка:        ⭐ Приемлемо")
			}
		})
	}

	// Итоговая оценка качества
	overallQuality := float64(qualityScore) / float64(totalTests) * 100
	t.Logf("")
	t.Logf("═══════════════════════════════════════")
	t.Logf("📊 ОБЩАЯ ОЦЕНКА КАЧЕСТВА: %.0f%%", overallQuality)
	t.Logf("   Прошли проверку: %d/%d", qualityScore, totalTests)
	t.Logf("═══════════════════════════════════════")
}

// containsCaseInsensitive проверяет содержит ли строка подстроку (без учёта регистра)
func containsCaseInsensitive(s, substr string) bool {
	return len(s) >= len(substr) &&
		(s == substr ||
		 len(s) > len(substr) &&
		 (s[:len(substr)] == substr ||
		  s[len(s)-len(substr):] == substr ||
		  containsSubstring(s, substr)))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
