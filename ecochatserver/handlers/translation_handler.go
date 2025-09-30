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
	llmClient *llm.LLMClient
	db        *sql.DB
}

// NewTranslationService создает новый TranslationService
func NewTranslationService(llmClient *llm.LLMClient) *TranslationService {
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
	// Определяем язык сообщения пользователя
	log.Printf("TranslateUserMessage: определение языка сообщения")
	detectedLang, err := ts.llmClient.DetectLanguage(ctx, content)
	if err != nil {
		log.Printf("TranslateUserMessage: ошибка определения языка: %v", err)
		// Если не удалось определить язык, возвращаем оригинал
		return &TranslationResult{
			Content:          content,
			Metadata:         map[string]interface{}{},
			DetectedLanguage: "unknown",
			WasTranslated:    false,
		}, nil
	}

	log.Printf("TranslateUserMessage: определен язык: %s", detectedLang)

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

	// Если языки совпадают, возвращаем оригинал
	if detectedLang == targetLang {
		log.Printf("TranslateUserMessage: языки совпадают, перевод не требуется")
		return &TranslationResult{
			Content: content,
			Metadata: map[string]interface{}{
				"detectedLanguage": detectedLang,
			},
			DetectedLanguage: detectedLang,
			WasTranslated:    false,
		}, nil
	}

	// Переводим текст
	log.Printf("TranslateUserMessage: перевод с %s на %s", detectedLang, targetLang)
	translated, err := ts.llmClient.TranslateText(ctx, content, detectedLang, targetLang)
	if err != nil {
		log.Printf("TranslateUserMessage: ошибка перевода: %v", err)
		// Если перевод не удался, возвращаем оригинал
		return &TranslationResult{
			Content: content,
			Metadata: map[string]interface{}{
				"detectedLanguage":  detectedLang,
				"translationFailed": true,
			},
			DetectedLanguage: detectedLang,
			WasTranslated:    false,
		}, nil
	}

	log.Printf("TranslateUserMessage: перевод выполнен успешно")

	// Возвращаем результат с метаданными
	return &TranslationResult{
		Content: translated,
		Metadata: map[string]interface{}{
			"originalText":     content,
			"translatedText":   translated,
			"detectedLanguage": detectedLang,
			"targetLanguage":   targetLang,
			"isTranslated":     true,
		},
		DetectedLanguage: detectedLang,
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
		},
		DetectedLanguage: sourceLang,
		WasTranslated:    true,
	}, nil
}
