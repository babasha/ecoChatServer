package handlers

import (
	"context"
	"fmt"
	"log"

	"github.com/egor/ecochatserver/database"
	"github.com/egor/ecochatserver/llm"
	"github.com/egor/ecochatserver/models"
	"github.com/google/uuid"
)

// TranslateBatchRequest содержит информацию для batch перевода
type TranslateBatchRequest struct {
	Text  string
	From  string
	To    string
	Index int // Индекс для сопоставления с результатом
}

// TranslateBatch переводит несколько текстов за один API вызов
// Использует TOON формат если включен (экономия ~40% токенов)
func (ts *TranslationService) TranslateBatch(ctx context.Context, texts []string, fromLang, toLang string) ([]string, error) {
	if len(texts) == 0 {
		return []string{}, nil
	}

	if fromLang == toLang {
		return texts, nil
	}

	log.Printf("TranslateBatch: перевод %d текстов с %s на %s за ОДИН запрос", len(texts), fromLang, toLang)

	// 🎯 TOON или JSON в зависимости от флага
	var translations []string
	var err error

	if ts.useTOON {
		// Проверяем поддерживает ли провайдер TOON
		if providerWithTOON, ok := ts.provider.(llm.ProviderWithTOON); ok {
			translations, err = providerWithTOON.TranslateBatchTOON(ctx, texts, fromLang, toLang)
			if err == nil {
				log.Printf("✅ TOON batch формат использован")
			}
		} else {
			// Fallback на JSON если провайдер не поддерживает TOON
			log.Printf("⚠️ Провайдер не поддерживает TOON, fallback на JSON")
			translations, err = ts.provider.TranslateBatch(ctx, texts, fromLang, toLang)
		}
	} else {
		translations, err = ts.provider.TranslateBatch(ctx, texts, fromLang, toLang)
	}

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

	// ОПТИМИЗАЦИЯ: Группируем сообщения по языку для корректного batch перевода
	// Избегаем проблемы когда сообщения на разных языках переводятся как один язык
	byLang := make(map[string][]models.Message)
	for _, msg := range toTranslate {
		if msg.Metadata != nil {
			if detected, ok := msg.Metadata["detectedLanguage"].(string); ok && detected != "" && detected != adminLang {
				byLang[detected] = append(byLang[detected], msg)
			}
		}
	}

	if len(byLang) == 0 {
		log.Printf("TranslateMessagesForAdmin: нет сообщений для перевода")
		return nil
	}

	// Переводим каждую языковую группу отдельно
	allTranslationsMap := make(map[uuid.UUID]map[string]string)

	for fromLang, msgs := range byLang {
		log.Printf("TranslateMessagesForAdmin: перевод %d сообщений с %s на %s", len(msgs), fromLang, adminLang)

		// Собираем тексты для batch
		texts := make([]string, len(msgs))
		for i, msg := range msgs {
			texts[i] = msg.Content
		}

		// Batch перевод для этой группы
		translations, err := ts.TranslateBatch(ctx, texts, fromLang, adminLang)
		if err != nil {
			log.Printf("TranslateMessagesForAdmin: ошибка перевода с %s: %v", fromLang, err)
			continue
		}

		// Собираем переводы в общий map
		for i, msg := range msgs {
			if i >= len(translations) {
				break
			}
			allTranslationsMap[msg.ID] = map[string]string{
				adminLang: translations[i],
			}
		}
	}

	// Сохраняем ВСЕ переводы в 1 транзакции
	if len(allTranslationsMap) > 0 {
		err := database.SaveTranslationsBatch(allTranslationsMap)
		if err != nil {
			return fmt.Errorf("TranslateMessagesForAdmin: SaveTranslationsBatch: %w", err)
		}

		// Обновляем content в исходном массиве
		for msgID, translations := range allTranslationsMap {
			if translation, ok := translations[adminLang]; ok {
				for j := range messages {
					if messages[j].ID == msgID {
						messages[j].Content = translation
						break
					}
				}
			}
		}

		log.Printf("TranslateMessagesForAdmin: переведено и сохранено %d сообщений в 1 транзакции", len(allTranslationsMap))
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

	// ОПТИМИЗАЦИЯ: Batch save вместо N UPDATE запросов
	translationsMap := make(map[uuid.UUID]map[string]string)
	for i, msg := range toTranslate {
		if i >= len(translations) {
			break
		}
		translationsMap[msg.ID] = map[string]string{
			clientLang: translations[i],
		}
	}

	// Сохраняем ВСЕ переводы в 1 транзакции
	err = database.SaveTranslationsBatch(translationsMap)
	if err != nil {
		return fmt.Errorf("TranslateMessagesForWidget: SaveTranslationsBatch: %w", err)
	}

	// Обновляем content в исходном массиве
	for i, msg := range toTranslate {
		if i >= len(translations) {
			break
		}

		for j := range messages {
			if messages[j].ID == msg.ID {
				messages[j].Content = translations[i]
				break
			}
		}
	}

	log.Printf("TranslateMessagesForWidget: переведено и сохранено %d сообщений в 1 транзакции", len(translationsMap))
	return nil
}
