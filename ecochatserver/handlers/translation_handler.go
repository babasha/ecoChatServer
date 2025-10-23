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
	provider llm.Provider // Используем универсальный провайдер вместо старого LLM интерфейса
	db       *sql.DB
}

// NewTranslationService создает новый TranslationService
func NewTranslationService(provider llm.Provider) *TranslationService {
	return &TranslationService{
		provider: provider,
		db:       database.DB,
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
	result, err := ts.provider.DetectAndTranslate(ctx, content, targetLang)
	if err != nil {
		log.Printf("⚠️ TranslateUserMessage: КРИТИЧЕСКАЯ ОШИБКА DetectAndTranslate: %v", err)
		log.Printf("⚠️ Язык клиента НЕ ОПРЕДЕЛЁН - последующие ответы админа не будут переводиться!")

		// ВАЖНО: Возвращаем оригинал, НО отмечаем что перевод провалился
		// Это критично для работы системы - если язык unknown, админ не сможет отправлять переводы
		return &TranslationResult{
			Content: content,
			Metadata: map[string]interface{}{
				"translationError": err.Error(),
				"detectedLanguage": "unknown",
			},
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

	// Переводим текст с retry логикой
	log.Printf("TranslateAdminMessage: перевод с %s на %s", sourceLang, clientLang)

	var translated string
	var translateErr error
	maxRetries := 2

	for attempt := 1; attempt <= maxRetries; attempt++ {
		translated, translateErr = ts.provider.TranslateText(ctx, content, sourceLang, clientLang)
		if translateErr == nil {
			break // Успешно
		}

		if attempt < maxRetries {
			log.Printf("⚠️ TranslateAdminMessage: попытка %d провалилась: %v, повтор...", attempt, translateErr)
		}
	}

	if translateErr != nil {
		log.Printf("🔴 TranslateAdminMessage: КРИТИЧЕСКАЯ ОШИБКА перевода после %d попыток: %v", maxRetries, translateErr)
		log.Printf("🔴 Сообщение админа НЕ ПЕРЕВЕДЕНО - клиент получит текст на языке админа!")

		return &TranslationResult{
			Content: content,
			Metadata: map[string]interface{}{
				"detectedLanguage":  sourceLang,
				"targetLanguage":    clientLang,
				"translationFailed": true,
				"translationError":  translateErr.Error(),
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
