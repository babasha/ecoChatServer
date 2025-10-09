package handlers

import (
	"context"
	"database/sql"
	"log"

	"github.com/egor/ecochatserver/database"
	"github.com/egor/ecochatserver/database/queries"
	"github.com/egor/ecochatserver/llm"
	"github.com/google/uuid"
)

// TranslationService предоставляет функции для перевода сообщений
type TranslationService struct {
	llmClient llm.LLM // Используем интерфейс вместо конкретного типа
	db        *sql.DB
}

// NewTranslationService создает новый TranslationService
func NewTranslationService(llmClient llm.LLM) *TranslationService {
	return &TranslationService{
		llmClient: llmClient,
		db:        database.DB,
	}
}

// TranslationResult содержит результат перевода
type TranslationResult struct {
	Content          string                 // Текст для отображения (переведенный или оригинальный)
	Metadata         map[string]interface{} // Метаданные с информацией о переводе
	DetectedLanguage string                 // Определенный язык оригинала
	WasTranslated    bool                   // Был ли текст переведен
}

// TranslateUserMessage переводит сообщение от пользователя на язык админа
func (ts *TranslationService) TranslateUserMessage(ctx context.Context, content string, chatID uuid.UUID) (*TranslationResult, error) {
	// Получаем настройки админа (предпочитаемый язык)
	// TODO: В будущем нужно определить конкретного админа, работающего с чатом
	// Пока используем дефолтного админа
	defaultAdminID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	adminSettings, err := queries.GetAdminSettings(ts.db, defaultAdminID)
	if err != nil {
		log.Printf("TranslateUserMessage: ошибка получения настроек админа: %v", err)
		// Используем русский по умолчанию
		adminSettings = &queries.AdminSettings{
			PreferredLanguage: "ru",
		}
	}

	targetLang := adminSettings.PreferredLanguage
	log.Printf("TranslateUserMessage: целевой язык админа: %s", targetLang)

	// 🚀 ОПТИМИЗАЦИЯ: Используем DetectAndTranslate - один запрос вместо двух!
	log.Printf("TranslateUserMessage: определение языка И перевод за один запрос")
	result, err := ts.llmClient.DetectAndTranslate(ctx, content, targetLang)
	if err != nil {
		log.Printf("TranslateUserMessage: ошибка DetectAndTranslate: %v", err)
		// Если не удалось, возвращаем оригинал
		return &TranslationResult{
			Content:          content,
			Metadata:         map[string]interface{}{},
			DetectedLanguage: "unknown",
			WasTranslated:    false,
		}, nil
	}

	log.Printf("TranslateUserMessage: определен язык: %s", result.DetectedLang)

	// Если языки совпадают (Gemini вернёт оригинал в translation)
	if result.DetectedLang == targetLang {
		log.Printf("TranslateUserMessage: языки совпадают, перевод не требуется")
		return &TranslationResult{
			Content: content,
			Metadata: map[string]interface{}{
				"detectedLanguage": result.DetectedLang,
			},
			DetectedLanguage: result.DetectedLang,
			WasTranslated:    false,
		}, nil
	}

	log.Printf("TranslateUserMessage: перевод выполнен успешно")

	// Возвращаем результат с метаданными
	return &TranslationResult{
		Content: result.Translation,
		Metadata: map[string]interface{}{
			"originalText":     content,
			"translatedText":   result.Translation,
			"detectedLanguage": result.DetectedLang,
			"targetLanguage":   targetLang,
			"isTranslated":     true,
			"translations": map[string]interface{}{
				targetLang: result.Translation,
			},
		},
		DetectedLanguage: result.DetectedLang,
		WasTranslated:    true,
	}, nil
}

// TranslateAdminMessage переводит сообщение от админа на язык клиента
func (ts *TranslationService) TranslateAdminMessage(ctx context.Context, content string, chatID uuid.UUID, adminID uuid.UUID) (*TranslationResult, error) {
	// Получаем язык клиента из последних сообщений чата
	log.Printf("TranslateAdminMessage: получение языка клиента из истории чата")

	// Используем оптимизированную функцию для получения языка клиента
	clientLang, err := database.GetClientLanguageFromChat(chatID)
	if err != nil {
		log.Printf("TranslateAdminMessage: ошибка получения языка клиента: %v", err)
		// Если не удалось получить язык, возвращаем оригинал
		return &TranslationResult{
			Content:          content,
			Metadata:         map[string]interface{}{},
			DetectedLanguage: "unknown",
			WasTranslated:    false,
		}, nil
	}

	// Если язык клиента не найден, возвращаем оригинал
	if clientLang == "" {
		log.Printf("TranslateAdminMessage: язык клиента не определен, перевод не выполняется")
		return &TranslationResult{
			Content:          content,
			Metadata:         map[string]interface{}{},
			DetectedLanguage: "unknown",
			WasTranslated:    false,
		}, nil
	}

	log.Printf("TranslateAdminMessage: найден язык клиента: %s", clientLang)

	// Получаем язык админа
	adminSettings, err := queries.GetAdminSettings(ts.db, adminID)
	if err != nil {
		log.Printf("TranslateAdminMessage: ошибка получения настроек админа: %v", err)
		adminSettings = &queries.AdminSettings{
			PreferredLanguage: "ru",
		}
	}

	sourceLang := adminSettings.PreferredLanguage
	log.Printf("TranslateAdminMessage: язык админа: %s, язык клиента: %s", sourceLang, clientLang)

	// Если языки совпадают, возвращаем оригинал
	if sourceLang == clientLang {
		log.Printf("TranslateAdminMessage: языки совпадают, перевод не требуется")
		return &TranslationResult{
			Content: content,
			Metadata: map[string]interface{}{
				"detectedLanguage": sourceLang,
			},
			DetectedLanguage: sourceLang,
			WasTranslated:    false,
		}, nil
	}

	// Переводим текст
	log.Printf("TranslateAdminMessage: перевод с %s на %s", sourceLang, clientLang)
	translated, err := ts.llmClient.TranslateText(ctx, content, sourceLang, clientLang)
	if err != nil {
		log.Printf("TranslateAdminMessage: ошибка перевода: %v", err)
		return &TranslationResult{
			Content: content,
			Metadata: map[string]interface{}{
				"detectedLanguage":  sourceLang,
				"targetLanguage":    clientLang, // Добавляем целевой язык для fallback
				"translationFailed": true,
			},
			DetectedLanguage: sourceLang,
			WasTranslated:    false,
		}, nil
	}

	log.Printf("TranslateAdminMessage: перевод выполнен успешно")

	return &TranslationResult{
		Content: translated,
		Metadata: map[string]interface{}{
			"originalText":     content,
			"translatedText":   translated,
			"detectedLanguage": sourceLang,
			"targetLanguage":   clientLang,
			"isTranslated":     true,
			"translations": map[string]interface{}{
				clientLang: translated,
			},
		},
		DetectedLanguage: sourceLang,
		WasTranslated:    true,
	}, nil
}
