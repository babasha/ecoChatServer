package handlers

import (
	"context"
	"fmt"
	"log"

	"github.com/egor/ecochatserver/database"
	"github.com/egor/ecochatserver/llm"
	"github.com/egor/ecochatserver/models"
)

// TranslateBatchRequest содержит информацию для batch перевода
type TranslateBatchRequest struct {
	Text     string
	From     string
	To       string
	Index    int // Индекс для сопоставления с результатом
}

// TranslateBatch переводит несколько текстов за один API вызов
// Использует оптимизированный метод GeminiClient.TranslateBatch
func (ts *TranslationService) TranslateBatch(ctx context.Context, texts []string, fromLang, toLang string) ([]string, error) {
	if len(texts) == 0 {
		return []string{}, nil
	}

	if fromLang == toLang {
		return texts, nil
	}

	log.Printf("TranslateBatch: перевод %d текстов с %s на %s за ОДИН запрос", len(texts), fromLang, toLang)

	// 🔧 ОПТИМИЗАЦИЯ: Используем универсальный Provider.TranslateBatch
	// Все провайдеры (Gemini, OpenAI, Claude) реализуют этот метод
	translations, err := ts.provider.TranslateBatch(ctx, texts, fromLang, toLang)
	if err != nil {
		return nil, fmt.Errorf("TranslateBatch: %w", err)
	}

	log.Printf("TranslateBatch: успешно переведено %d текстов за один API вызов", len(translations))
	return translations, nil
}

// TranslateMessagesForAdmin переводит сообщения для отображения админу
// Использует lazy caching - переводит только если нет в кеше
func (ts *TranslationService) TranslateMessagesForAdmin(ctx context.Context, messages []models.Message, adminLang string) error {
	// Собираем сообщения которые нужно перевести
	toTranslate := []models.Message{}

	for i := range messages {
		msg := &messages[i]

		// Переводим только сообщения от пользователя
		if msg.Sender != "user" {
			continue
		}

		// Проверяем есть ли уже перевод в кеше
		if msg.Metadata != nil {
			if translations, ok := msg.Metadata["translations"].(map[string]interface{}); ok {
				if cached, exists := translations[adminLang]; exists && cached != "" {
					// Есть кеш - используем
					if cachedStr, ok := cached.(string); ok {
						msg.Content = cachedStr
						log.Printf("TranslateMessagesForAdmin: использован кеш для msg %s", msg.ID)
						continue
					}
				}
			}
		}

		// Нет кеша - добавляем в очередь на перевод
		toTranslate = append(toTranslate, *msg)
	}

	if len(toTranslate) == 0 {
		log.Printf("TranslateMessagesForAdmin: все сообщения уже переведены (кеш)")
		return nil
	}

	log.Printf("TranslateMessagesForAdmin: нужно перевести %d сообщений", len(toTranslate))

	// Собираем тексты для batch перевода
	texts := make([]string, len(toTranslate))
	fromLang := ""

	for i, msg := range toTranslate {
		texts[i] = msg.Content

		// Определяем язык из metadata
		if fromLang == "" && msg.Metadata != nil {
			if detected, ok := msg.Metadata["detectedLanguage"].(string); ok {
				fromLang = detected
			}
		}
	}

	// Если не удалось определить язык - пропускаем перевод
	if fromLang == "" || fromLang == adminLang {
		log.Printf("TranslateMessagesForAdmin: язык не определен или совпадает с целевым")
		return nil
	}

	// Batch перевод
	translations, err := ts.TranslateBatch(ctx, texts, fromLang, adminLang)
	if err != nil {
		return fmt.Errorf("TranslateMessagesForAdmin: %w", err)
	}

	// Сохраняем переводы в БД и обновляем в памяти
	for i, msg := range toTranslate {
		if i >= len(translations) {
			break
		}

		translation := translations[i]

		// Сохраняем в БД
		err := database.SaveTranslation(msg.ID, adminLang, translation)
		if err != nil {
			log.Printf("TranslateMessagesForAdmin: ошибка сохранения перевода для %s: %v", msg.ID, err)
			continue
		}

		// Обновляем content в исходном массиве
		for j := range messages {
			if messages[j].ID == msg.ID {
				messages[j].Content = translation
				break
			}
		}

		log.Printf("TranslateMessagesForAdmin: переведено и сохранено msg %s", msg.ID)
	}

	return nil
}

// TranslateMessagesForWidget переводит сообщения для отображения в виджете
// Переводит только сообщения от админа на язык клиента
func (ts *TranslationService) TranslateMessagesForWidget(ctx context.Context, messages []models.Message, clientLang string) error {
	toTranslate := []models.Message{}

	for i := range messages {
		msg := &messages[i]

		// Переводим только сообщения от админа
		if msg.Sender != "admin" {
			continue
		}

		// Автоответы уже на языке клиента - не переводим
		if msg.Metadata != nil {
			if isAuto, ok := msg.Metadata["isAutoResponse"].(bool); ok && isAuto {
				continue
			}
		}

		// Проверяем кеш в metadata.translations
		if msg.Metadata != nil {
			if translations, ok := msg.Metadata["translations"].(map[string]interface{}); ok {
				if cached, exists := translations[clientLang]; exists && cached != "" {
					if cachedStr, ok := cached.(string); ok {
						msg.Content = cachedStr
						log.Printf("TranslateMessagesForWidget: использован кеш translations для msg %s", msg.ID)
						continue
					}
				}
			}
		}

		toTranslate = append(toTranslate, *msg)
	}

	if len(toTranslate) == 0 {
		return nil
	}

	log.Printf("TranslateMessagesForWidget: нужно перевести %d сообщений", len(toTranslate))

	texts := make([]string, len(toTranslate))
	fromLang := ""

	for i, msg := range toTranslate {
		texts[i] = msg.Content

		if fromLang == "" && msg.Metadata != nil {
			if detected, ok := msg.Metadata["detectedLanguage"].(string); ok {
				fromLang = detected
			}
		}
	}

	if fromLang == "" || fromLang == clientLang {
		return nil
	}

	translations, err := ts.TranslateBatch(ctx, texts, fromLang, clientLang)
	if err != nil {
		return fmt.Errorf("TranslateMessagesForWidget: %w", err)
	}

	for i, msg := range toTranslate {
		if i >= len(translations) {
			break
		}

		translation := translations[i]

		err := database.SaveTranslation(msg.ID, clientLang, translation)
		if err != nil {
			log.Printf("TranslateMessagesForWidget: ошибка сохранения: %v", err)
			continue
		}

		for j := range messages {
			if messages[j].ID == msg.ID {
				messages[j].Content = translation
				break
			}
		}
	}

	return nil
}
