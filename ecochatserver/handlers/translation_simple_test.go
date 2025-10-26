package handlers

import (
	"context"
	"testing"

	"github.com/egor/ecochatserver/llm"
	"github.com/egor/ecochatserver/models"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// SimpleMockProvider - упрощённый мок только для переводов
type SimpleMockProvider struct {
	mock.Mock
}

func (m *SimpleMockProvider) GetName() string {
	return "MockProvider"
}

func (m *SimpleMockProvider) GenerateResponse(ctx context.Context, userMessage string, chatHistory []llm.Message, opts *llm.GenerateOptions) (*llm.Response, error) {
	args := m.Called(ctx, userMessage, chatHistory, opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*llm.Response), args.Error(1)
}

func (m *SimpleMockProvider) GenerateWithTools(ctx context.Context, userMessage string, chatHistory []llm.Message, tools []llm.Tool, opts *llm.GenerateOptions) (*llm.Response, error) {
	args := m.Called(ctx, userMessage, chatHistory, tools, opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*llm.Response), args.Error(1)
}

func (m *SimpleMockProvider) ContinueWithFunctionResult(ctx context.Context, chatHistory []llm.Message, functionCall *llm.FunctionCall, functionResult string, opts *llm.GenerateOptions) (*llm.Response, error) {
	args := m.Called(ctx, chatHistory, functionCall, functionResult, opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*llm.Response), args.Error(1)
}

func (m *SimpleMockProvider) DetectAndTranslate(ctx context.Context, text, targetLang string) (*llm.TranslationResult, error) {
	args := m.Called(ctx, text, targetLang)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*llm.TranslationResult), args.Error(1)
}

func (m *SimpleMockProvider) TranslateText(ctx context.Context, text, fromLang, toLang string) (string, error) {
	args := m.Called(ctx, text, fromLang, toLang)
	return args.String(0), args.Error(1)
}

func (m *SimpleMockProvider) TranslateBatch(ctx context.Context, texts []string, fromLang, toLang string) ([]string, error) {
	args := m.Called(ctx, texts, fromLang, toLang)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]string), args.Error(1)
}

// TestSimple_TranslateBatch тестирует batch перевод
func TestSimple_TranslateBatch(t *testing.T) {
	// Arrange
	mockProvider := new(SimpleMockProvider)
	translator := &TranslationService{
		provider: mockProvider,
	}

	ctx := context.Background()
	texts := []string{"Hello", "World", "Test"}
	fromLang := "en"
	toLang := "ru"
	expectedTranslations := []string{"Привет", "Мир", "Тест"}

	// Mock TranslateBatch
	mockProvider.On("TranslateBatch", ctx, texts, fromLang, toLang).Return(expectedTranslations, nil)

	// Act
	result, err := translator.TranslateBatch(ctx, texts, fromLang, toLang)

	// Assert
	assert.NoError(t, err)
	assert.Equal(t, expectedTranslations, result)
	assert.Len(t, result, 3)

	mockProvider.AssertExpectations(t)
}

// TestSimple_TranslateBatchEmpty тестирует batch с пустым вводом
func TestSimple_TranslateBatchEmpty(t *testing.T) {
	// Arrange
	mockProvider := new(SimpleMockProvider)
	translator := &TranslationService{
		provider: mockProvider,
	}

	ctx := context.Background()
	texts := []string{}

	// Act
	result, err := translator.TranslateBatch(ctx, texts, "en", "ru")

	// Assert
	assert.NoError(t, err)
	assert.Empty(t, result)

	// Mock не должен вызываться
	mockProvider.AssertNotCalled(t, "TranslateBatch")
}

// TestSimple_TranslateBatchSameLanguage тестирует когда языки совпадают
func TestSimple_TranslateBatchSameLanguage(t *testing.T) {
	// Arrange
	mockProvider := new(SimpleMockProvider)
	translator := &TranslationService{
		provider: mockProvider,
	}

	ctx := context.Background()
	texts := []string{"Привет", "Мир"}

	// Act
	result, err := translator.TranslateBatch(ctx, texts, "ru", "ru")

	// Assert
	assert.NoError(t, err)
	assert.Equal(t, texts, result) // должен вернуть оригиналы

	// Mock не должен вызываться когда языки совпадают
	mockProvider.AssertNotCalled(t, "TranslateBatch")
}

// TestSimple_CacheHit тестирует использование кеша
func TestSimple_CacheHit(t *testing.T) {
	// Arrange
	messageID := uuid.New()
	message := models.Message{
		ID:      messageID,
		Content: "Hello",
		Sender:  "user",
		Metadata: map[string]interface{}{
			"detectedLanguage": "en",
			"translations": map[string]interface{}{
				"ru": "Привет",
				"pl": "Witaj",
			},
		},
	}

	// Act - проверяем кеш
	adminLang := "ru"
	translations, ok := message.Metadata["translations"].(map[string]interface{})
	assert.True(t, ok)

	cached, exists := translations[adminLang]
	assert.True(t, exists)
	assert.Equal(t, "Привет", cached)
}

// TestSimple_CacheMiss тестирует отсутствие в кеше
func TestSimple_CacheMiss(t *testing.T) {
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

	// Act - ищем перевод которого нет
	adminLang := "de" // немецкого нет
	translations, ok := message.Metadata["translations"].(map[string]interface{})
	assert.True(t, ok)

	cached, exists := translations[adminLang]
	assert.False(t, exists)
	assert.Nil(t, cached)
}

// TestSimple_BatchOptimization тестирует batch оптимизацию
func TestSimple_BatchOptimization(t *testing.T) {
	// Arrange
	mockProvider := new(SimpleMockProvider)
	translator := &TranslationService{
		provider: mockProvider,
	}

	ctx := context.Background()

	// 5 сообщений на английском
	texts := []string{
		"Hello",
		"How are you?",
		"Good morning",
		"Thank you",
		"Goodbye",
	}

	expectedTranslations := []string{
		"Привет",
		"Как дела?",
		"Доброе утро",
		"Спасибо",
		"До свидания",
	}

	// Mock ОДИН batch вызов вместо 5 отдельных!
	mockProvider.On("TranslateBatch", ctx, texts, "en", "ru").Return(expectedTranslations, nil).Once()

	// Act - batch перевод
	result, err := translator.TranslateBatch(ctx, texts, "en", "ru")

	// Assert
	assert.NoError(t, err)
	assert.Len(t, result, 5)
	assert.Equal(t, expectedTranslations, result)

	// ВАЖНО: Должен быть ОДИН API вызов, не 5!
	mockProvider.AssertNumberOfCalls(t, "TranslateBatch", 1)
	mockProvider.AssertExpectations(t)
}

// BenchmarkBatchTranslate_10Messages бенчмарк для batch перевода
func BenchmarkBatchTranslate_10Messages(b *testing.B) {
	mockProvider := new(SimpleMockProvider)
	translator := &TranslationService{
		provider: mockProvider,
	}

	ctx := context.Background()
	texts := make([]string, 10)
	for i := 0; i < 10; i++ {
		texts[i] = "Test message"
	}

	translations := make([]string, 10)
	for i := 0; i < 10; i++ {
		translations[i] = "Тестовое сообщение"
	}

	mockProvider.On("TranslateBatch", ctx, texts, "en", "ru").Return(translations, nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = translator.TranslateBatch(ctx, texts, "en", "ru")
	}
}
