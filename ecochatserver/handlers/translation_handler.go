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

// HubInterface определяет методы Hub которые нужны для TranslationService
type HubInterface interface {
	GetOnlineAdminIDs() []uuid.UUID
}

// TranslationService предоставляет функции для перевода сообщений
type TranslationService struct {
	provider llm.Provider // Используем универсальный провайдер вместо старого LLM интерфейса
	db       *sql.DB
	hub      HubInterface // Hub для получения списка онлайн админов
}

// NewTranslationService создает новый TranslationService
func NewTranslationService(provider llm.Provider, hub HubInterface) *TranslationService {
	return &TranslationService{
		provider: provider,
		db:       database.DB,
		hub:      hub,
	}
}

// TranslationResult содержит результат перевода
// ОПТИМИЗАЦИЯ: Убран неиспользуемый Content, упрощена структура
type TranslationResult struct {
	Metadata         map[string]interface{} // Минимальные метаданные: detectedLanguage + translations
	DetectedLanguage string                 // Определенный язык оригинала
	WasTranslated    bool                   // Был ли текст переведен
}

// TranslateUserMessage переводит сообщение от пользователя ТОЛЬКО для ОНЛАЙН админов с доступом
// ОПТИМИЗАЦИЯ: больше не переводим для оффлайн админов, экономим токены
func (ts *TranslationService) TranslateUserMessage(ctx context.Context, content string, chatID uuid.UUID) (*TranslationResult, error) {
	var adminLanguages []queries.AdminLanguageInfo
	var err error

	// Шаг 1: Пытаемся получить языки ОНЛАЙН админов (если hub доступен)
	if ts.hub != nil {
		onlineAdminIDs := ts.hub.GetOnlineAdminIDs()
		log.Printf("TranslateUserMessage: найдено %d онлайн админов", len(onlineAdminIDs))

		adminLanguages, err = queries.GetAdminLanguagesForChatOnlineOnly(ts.db, chatID, onlineAdminIDs)
		if err != nil {
			log.Printf("TranslateUserMessage: ошибка GetAdminLanguagesForChatOnlineOnly: %v", err)
		} else if len(adminLanguages) > 0 {
			log.Printf("TranslateUserMessage: используем языки %d онлайн админов", len(adminLanguages))
			// Успех! Есть онлайн админы, используем их языки
			goto processLanguages
		}
		// Если нет онлайн админов или ошибка - fallback на всех админов
		log.Printf("TranslateUserMessage: нет онлайн админов, fallback на язык первого админа с доступом")
	}

	// Шаг 2: Fallback - получаем ВСЕХ админов с доступом
	adminLanguages, err = queries.GetAdminLanguagesForChat(ts.db, chatID)
	if err != nil {
		log.Printf("TranslateUserMessage: ошибка GetAdminLanguagesForChat: %v, используем дефолт", err)
	} else if len(adminLanguages) > 0 {
		// Берем только ПЕРВОГО админа для экономии токенов
		adminLanguages = []queries.AdminLanguageInfo{adminLanguages[0]}
		log.Printf("TranslateUserMessage: используем язык первого админа: %s", adminLanguages[0].PreferredLanguage)
		goto processLanguages
	}

	// Шаг 3: Финальный fallback - дефолтный язык
	log.Printf("TranslateUserMessage: нет админов с доступом, используем дефолт ru")
	adminLanguages = []queries.AdminLanguageInfo{{PreferredLanguage: "ru"}}

processLanguages:

	// Получаем уникальные языки (один админ может быть под несколькими ролями)
	uniqueLangs := make(map[string]bool)
	for _, al := range adminLanguages {
		uniqueLangs[al.PreferredLanguage] = true
	}

	log.Printf("TranslateUserMessage: нужно перевести на %d уникальных языков для ОНЛАЙН админов: %v", len(uniqueLangs), uniqueLangs)

	// Определяем язык оригинала с помощью первого перевода
	var detectedLang string
	translations := make(map[string]interface{})
	firstLang := true

	for targetLang := range uniqueLangs {
		if firstLang {
			// Первый запрос: определяем язык И переводим
			log.Printf("TranslateUserMessage: определение языка И перевод на %s", targetLang)
			result, err := ts.provider.DetectAndTranslate(ctx, content, targetLang)
			if err != nil {
				log.Printf("⚠️ TranslateUserMessage: ошибка DetectAndTranslate: %v", err)
				detectedLang = "unknown"
				translations[targetLang] = content
				firstLang = false
				continue
			}

			detectedLang = result.DetectedLang
			log.Printf("TranslateUserMessage: определен язык: %s", detectedLang)

			// Если язык совпадает - используем оригинал
			if detectedLang == targetLang {
				translations[targetLang] = content
			} else {
				translations[targetLang] = result.Translation
			}
			firstLang = false
			continue
		}

		// Остальные языки: переводим напрямую
		if detectedLang == targetLang {
			log.Printf("TranslateUserMessage: язык %s совпадает с оригиналом, пропускаем", targetLang)
			translations[targetLang] = content
			continue
		}

		log.Printf("TranslateUserMessage: перевод на %s", targetLang)
		translatedText, err := ts.provider.TranslateText(ctx, content, detectedLang, targetLang)
		if err != nil {
			log.Printf("⚠️ TranslateUserMessage: ошибка перевода на %s: %v", targetLang, err)
			translations[targetLang] = content
			continue
		}

		translations[targetLang] = translatedText
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
