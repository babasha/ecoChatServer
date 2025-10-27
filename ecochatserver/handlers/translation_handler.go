package handlers

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"

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
// ОПТИМИЗАЦИЯ: Убран неиспользуемый Content, упрощена структура
type TranslationResult struct {
	Metadata         map[string]interface{} // Минимальные метаданные: detectedLanguage + translations
	DetectedLanguage string                 // Определенный язык оригинала
	WasTranslated    bool                   // Был ли текст переведен
}

// TranslateUserMessage переводит сообщение от пользователя на язык админа
func (ts *TranslationService) TranslateUserMessage(ctx context.Context, content string, chatID uuid.UUID) (*TranslationResult, error) {
	// Получаем настройки админа (предпочитаемый язык)
	// TODO: В будущем нужно определить конкретного админа, работающего с чатом
	// Пока используем реального админа из БД
	defaultAdminID := uuid.MustParse("05605c9d-c50f-4515-8949-9b61ae73b3aa")
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

		// ВАЖНО: Возвращаем метаданные с ошибкой
		// Это критично для работы системы - если язык unknown, админ не сможет отправлять переводы
		return &TranslationResult{
			Metadata: map[string]interface{}{
				"detectedLanguage": "unknown",
			},
			DetectedLanguage: "unknown",
			WasTranslated:    false,
		}, nil
	}

	log.Printf("TranslateUserMessage: определен язык: %s", result.DetectedLang)

	// Если языки совпадают (перевод не требуется)
	if result.DetectedLang == targetLang {
		log.Printf("TranslateUserMessage: языки совпадают, перевод не требуется")
		return &TranslationResult{
			Metadata: map[string]interface{}{
				"detectedLanguage": result.DetectedLang,
			},
			DetectedLanguage: result.DetectedLang,
			WasTranslated:    false,
		}, nil
	}

	log.Printf("TranslateUserMessage: перевод выполнен успешно")

	// ОПТИМИЗАЦИЯ: Минимальные метаданные - только detectedLanguage + translations
	// Убраны избыточные поля: originalText, translatedText, targetLanguage, isTranslated
	return &TranslationResult{
		Metadata: map[string]interface{}{
			"detectedLanguage": result.DetectedLang,
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
		// Если не удалось получить язык, возвращаем пустые метаданные
		return &TranslationResult{
			Metadata:         map[string]interface{}{},
			DetectedLanguage: "unknown",
			WasTranslated:    false,
		}, nil
	}
	clientLang = strings.TrimSpace(clientLang)

	// Получаем язык админа
	adminSettings, err := queries.GetAdminSettings(ts.db, adminID)
	if err != nil {
		log.Printf("TranslateAdminMessage: ошибка получения настроек админа: %v", err)
		adminSettings = &queries.AdminSettings{
			PreferredLanguage: "ru",
		}
	}

	sourceLang := strings.TrimSpace(adminSettings.PreferredLanguage)
	log.Printf("TranslateAdminMessage: язык админа: %s, исходный язык клиента: %s", sourceLang, clientLang)

	// Повторное получение языка клиента из последнего сообщения в БД, если он не найден или неизвестен
	if clientLang == "" || strings.EqualFold(clientLang, "unknown") {
		log.Printf("TranslateAdminMessage: язык клиента неизвестен, пробуем получить из последнего сообщения в БД")
		if detected, detectErr := ts.getClientLanguageFromLastMessage(chatID); detectErr != nil {
			log.Printf("TranslateAdminMessage: получение языка из последнего сообщения не удалось: %v", detectErr)
		} else if detected != "" && !strings.EqualFold(detected, "unknown") {
			clientLang = detected
			log.Printf("TranslateAdminMessage: язык клиента обновлен из БД: %s", clientLang)
		}
	}

	// Если язык по-прежнему неизвестен, пропускаем перевод
	if clientLang == "" || strings.EqualFold(clientLang, "unknown") {
		log.Printf("TranslateAdminMessage: язык клиента по-прежнему неизвестен, пропускаем перевод")
		return &TranslationResult{
			Metadata: map[string]interface{}{
				"detectedLanguage": sourceLang,
			},
			DetectedLanguage: sourceLang,
			WasTranslated:    false,
		}, nil
	}

	log.Printf("TranslateAdminMessage: финальный язык клиента: %s", clientLang)

	// Если языки совпадают, перевод не требуется
	if sourceLang == clientLang {
		log.Printf("TranslateAdminMessage: языки совпадают, перевод не требуется")
		return &TranslationResult{
			Metadata: map[string]interface{}{
				"detectedLanguage": sourceLang,
			},
			DetectedLanguage: sourceLang,
			WasTranslated:    false,
		}, nil
	}

	// Переводим текст
	// ОПТИМИЗАЦИЯ: Retry логика уже реализована в адаптерах (OpenAIAdapter, GeminiClient)
	// Не дублируем retry на уровне сервиса
	log.Printf("TranslateAdminMessage: перевод с %s на %s", sourceLang, clientLang)

	translated, translateErr := ts.provider.TranslateText(ctx, content, sourceLang, clientLang)

	if translateErr != nil {
		log.Printf("🔴 TranslateAdminMessage: КРИТИЧЕСКАЯ ОШИБКА перевода: %v", translateErr)
		log.Printf("🔴 Сообщение админа НЕ ПЕРЕВЕДЕНО - клиент получит текст на языке админа!")

		return &TranslationResult{
			Metadata: map[string]interface{}{
				"detectedLanguage": sourceLang,
			},
			DetectedLanguage: sourceLang,
			WasTranslated:    false,
		}, nil
	}

	log.Printf("TranslateAdminMessage: перевод выполнен успешно")

	// ОПТИМИЗАЦИЯ: Минимальные метаданные - только detectedLanguage + translations
	return &TranslationResult{
		Metadata: map[string]interface{}{
			"detectedLanguage": sourceLang,
			"translations": map[string]interface{}{
				clientLang: translated,
			},
		},
		DetectedLanguage: sourceLang,
		WasTranslated:    true,
	}, nil
}

// getClientLanguageFromLastMessage получает язык клиента из последнего сообщения в БД
// ОПТИМИЗАЦИЯ: Не делает повторный API вызов, только читает из БД
// Язык уже должен быть определен и сохранен при получении сообщения от клиента
func (ts *TranslationService) getClientLanguageFromLastMessage(chatID uuid.UUID) (string, error) {
	lastMessage, err := database.GetLastUserMessage(chatID)
	if err != nil {
		return "", fmt.Errorf("GetLastUserMessage: %w", err)
	}
	if lastMessage == nil {
		return "", fmt.Errorf("no user messages found for chat %s", chatID)
	}

	// Язык должен быть уже сохранен в metadata при получении сообщения
	if lastMessage.Metadata != nil {
		if lang, ok := lastMessage.Metadata["detectedLanguage"].(string); ok && lang != "" && !strings.EqualFold(lang, "unknown") {
			log.Printf("getClientLanguageFromLastMessage: найден язык в metadata последнего сообщения: %s", lang)
			return strings.ToLower(strings.TrimSpace(lang)), nil
		}
	}

	return "", fmt.Errorf("no valid detected language found in last message metadata")
}
