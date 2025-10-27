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

// TranslateUserMessage переводит сообщение от пользователя на языки всех админов с доступом
func (ts *TranslationService) TranslateUserMessage(ctx context.Context, content string, chatID uuid.UUID) (*TranslationResult, error) {
	// Получаем уникальные языки всех админов с доступом к чату
	adminLanguages, err := queries.GetAdminLanguagesForChat(ts.db, chatID)
	if err != nil {
		log.Printf("TranslateUserMessage: ошибка получения языков админов: %v", err)
		// Fallback: используем один язык по умолчанию
		adminLanguages = []queries.AdminLanguageInfo{
			{PreferredLanguage: "es"},
		}
	}

	// Получаем уникальные языки (один админ может быть под несколькими ролями)
	uniqueLangs := make(map[string]bool)
	for _, al := range adminLanguages {
		uniqueLangs[al.PreferredLanguage] = true
	}

	log.Printf("TranslateUserMessage: нужно перевести на %d уникальных языков: %v", len(uniqueLangs), uniqueLangs)

	// Определяем язык оригинала
	detectedLang, err := ts.provider.DetectLanguage(ctx, content)
	if err != nil {
		log.Printf("⚠️ TranslateUserMessage: ошибка определения языка: %v", err)
		detectedLang = "unknown"
	}

	log.Printf("TranslateUserMessage: определен язык: %s", detectedLang)

	// Создаем карту переводов
	translations := make(map[string]interface{})

	// Переводим на каждый уникальный язык
	for targetLang := range uniqueLangs {
		// Если язык совпадает с оригиналом - перевод не нужен
		if detectedLang == targetLang {
			log.Printf("TranslateUserMessage: язык %s совпадает с оригиналом, пропускаем", targetLang)
			translations[targetLang] = content
			continue
		}

		// Переводим
		log.Printf("TranslateUserMessage: перевод на %s", targetLang)
		result, err := ts.provider.Translate(ctx, content, detectedLang, targetLang)
		if err != nil {
			log.Printf("⚠️ TranslateUserMessage: ошибка перевода на %s: %v", targetLang, err)
			// В случае ошибки используем оригинал
			translations[targetLang] = content
			continue
		}

		translations[targetLang] = result.Translation
		log.Printf("TranslateUserMessage: перевод на %s успешен", targetLang)
	}

	log.Printf("TranslateUserMessage: создано %d переводов", len(translations))

	return &TranslationResult{
		Metadata: map[string]interface{}{
			"detectedLanguage": detectedLang,
			"translations":     translations,
		},
		DetectedLanguage: detectedLang,
		WasTranslated:    len(translations) > 0,
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
