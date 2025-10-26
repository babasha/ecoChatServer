package handlers

import (
	"testing"

	"github.com/egor/ecochatserver/models"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

// TestTranslationCache_HitFromMetadata тестирует использование кеша из metadata
func TestTranslationCache_HitFromMetadata(t *testing.T) {
	// Arrange
	messageID := uuid.New()
	messages := []models.Message{
		{
			ID:      messageID,
			Content: "Hello world", // оригинал на английском
			Sender:  "user",
			Metadata: map[string]interface{}{
				"detectedLanguage": "en",
				"translations": map[string]interface{}{
					"ru": "Привет мир",
					"pl": "Witaj świecie",
				},
			},
		},
	}

	// Act - получаем русский перевод из кеша
	msg := messages[0]
	translations, ok := msg.Metadata["translations"].(map[string]interface{})
	assert.True(t, ok)

	ruTranslation, exists := translations["ru"]
	assert.True(t, exists)
	assert.Equal(t, "Привет мир", ruTranslation)

	// Act - получаем польский перевод из кеша
	plTranslation, exists := translations["pl"]
	assert.True(t, exists)
	assert.Equal(t, "Witaj świecie", plTranslation)
}

// TestTranslationCache_Miss тестирует случай когда перевода нет в кеше
func TestTranslationCache_Miss(t *testing.T) {
	// Arrange
	messageID := uuid.New()
	messages := []models.Message{
		{
			ID:      messageID,
			Content: "Hello world",
			Sender:  "user",
			Metadata: map[string]interface{}{
				"detectedLanguage": "en",
				"translations": map[string]interface{}{
					"ru": "Привет мир",
				},
			},
		},
	}

	// Act - пытаемся получить перевод на немецкий (которого нет)
	msg := messages[0]
	translations, ok := msg.Metadata["translations"].(map[string]interface{})
	assert.True(t, ok)

	deTranslation, exists := translations["de"]
	assert.False(t, exists)
	assert.Nil(t, deTranslation)
}

// TestTranslationCache_EmptyMetadata тестирует сообщение без metadata
func TestTranslationCache_EmptyMetadata(t *testing.T) {
	// Arrange - старое сообщение без metadata
	messageID := uuid.New()
	messages := []models.Message{
		{
			ID:       messageID,
			Content:  "Hello world",
			Sender:   "user",
			Metadata: nil, // нет metadata!
		},
	}

	// Act
	msg := messages[0]
	assert.Nil(t, msg.Metadata)

	// Проверяем безопасный доступ
	if msg.Metadata != nil {
		translations, ok := msg.Metadata["translations"].(map[string]interface{})
		assert.False(t, ok)
		assert.Nil(t, translations)
	}
}

// TestTranslationCache_NoDetectedLanguage тестирует metadata без detectedLanguage
func TestTranslationCache_NoDetectedLanguage(t *testing.T) {
	// Arrange
	messageID := uuid.New()
	messages := []models.Message{
		{
			ID:      messageID,
			Content: "Hello world",
			Sender:  "user",
			Metadata: map[string]interface{}{
				// НЕТ detectedLanguage
				"translations": map[string]interface{}{
					"ru": "Привет мир",
				},
			},
		},
	}

	// Act
	msg := messages[0]
	detectedLang, exists := msg.Metadata["detectedLanguage"]
	assert.False(t, exists)
	assert.Nil(t, detectedLang)

	// Переводы всё равно доступны
	translations, ok := msg.Metadata["translations"].(map[string]interface{})
	assert.True(t, ok)
	assert.NotNil(t, translations)
}

// TestTranslationCache_MultipleMessages тестирует кеш для нескольких сообщений
func TestTranslationCache_MultipleMessages(t *testing.T) {
	// Arrange - история с разными сообщениями
	messages := []models.Message{
		{
			ID:      uuid.New(),
			Content: "Hello",
			Sender:  "user",
			Metadata: map[string]interface{}{
				"detectedLanguage": "en",
				"translations": map[string]interface{}{
					"ru": "Привет",
				},
			},
		},
		{
			ID:      uuid.New(),
			Content: "How are you?",
			Sender:  "user",
			Metadata: map[string]interface{}{
				"detectedLanguage": "en",
				"translations": map[string]interface{}{
					"ru": "Как дела?",
				},
			},
		},
		{
			ID:      uuid.New(),
			Content: "Спасибо",
			Sender:  "admin",
			Metadata: map[string]interface{}{
				"detectedLanguage": "ru",
				"translations": map[string]interface{}{
					"en": "Thank you",
				},
			},
		},
	}

	// Act & Assert - проверяем кеш для каждого сообщения
	cacheHits := 0
	for _, msg := range messages {
		if msg.Metadata != nil {
			if translations, ok := msg.Metadata["translations"].(map[string]interface{}); ok {
				if _, exists := translations["ru"]; exists {
					cacheHits++
				}
			}
		}
	}

	// Первые 2 сообщения должны иметь перевод на русский
	assert.Equal(t, 2, cacheHits)
}

// TestSaveTranslation_UpdateMetadata тестирует обновление metadata с новым переводом
func TestSaveTranslation_UpdateMetadata(t *testing.T) {
	// Arrange
	message := models.Message{
		ID:      uuid.New(),
		Content: "Hello",
		Sender:  "user",
		Metadata: map[string]interface{}{
			"detectedLanguage": "en",
			"translations": map[string]interface{}{
				"ru": "Привет",
			},
		},
	}

	// Act - добавляем новый перевод на польский
	translations := message.Metadata["translations"].(map[string]interface{})
	translations["pl"] = "Witaj"

	// Assert
	assert.Len(t, translations, 2)
	assert.Equal(t, "Привет", translations["ru"])
	assert.Equal(t, "Witaj", translations["pl"])
}

// TestTranslationCache_ConcurrentAccess тестирует конкурентный доступ к кешу
func TestTranslationCache_ConcurrentAccess(t *testing.T) {
	// Arrange
	message := models.Message{
		ID:      uuid.New(),
		Content: "Hello",
		Sender:  "user",
		Metadata: map[string]interface{}{
			"detectedLanguage": "en",
			"translations": map[string]interface{}{
				"ru": "Привет",
			},
		},
	}

	// Act - симулируем конкурентный доступ от разных горутин
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func() {
			defer func() { done <- true }()

			// Читаем из кеша
			if message.Metadata != nil {
				if translations, ok := message.Metadata["translations"].(map[string]interface{}); ok {
					translation, exists := translations["ru"]
					assert.True(t, exists)
					assert.Equal(t, "Привет", translation)
				}
			}
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}
}
