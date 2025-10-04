package handlers

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/egor/ecochatserver/database"
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
// Использует Gemini API для перевода пакета сообщений
func (ts *TranslationService) TranslateBatch(ctx context.Context, texts []string, fromLang, toLang string) ([]string, error) {
	if len(texts) == 0 {
		return []string{}, nil
	}

	// Если язык совпадает - возвращаем оригиналы
	if fromLang == toLang {
		return texts, nil
	}

	log.Printf("TranslateBatch: перевод %d текстов с %s на %s", len(texts), fromLang, toLang)

	// Формируем промпт для batch перевода
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Translate the following %d messages from %s to %s.\n", len(texts), fromLang, toLang))
	sb.WriteString("Return ONLY the translations, one per line, in the EXACT same order.\n")
	sb.WriteString("Do NOT add numbering, quotes, or any other formatting.\n\n")

	for i, text := range texts {
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, text))
	}

	prompt := sb.String()

	// Вызываем LLM
	response, err := ts.llmClient.GenerateResponse(ctx, prompt, nil)
	if err != nil {
		return nil, fmt.Errorf("TranslateBatch: LLM error: %w", err)
	}

	// Парсим результат - каждый перевод на новой строке
	lines := strings.Split(strings.TrimSpace(response), "\n")
	translations := make([]string, 0, len(texts))

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Убираем возможную нумерацию (1., 2., и т.д.)
		if len(line) > 3 && line[1] == '.' && line[0] >= '0' && line[0] <= '9' {
			line = strings.TrimSpace(line[2:])
		}

		translations = append(translations, line)
	}

	// Проверяем что количество переводов совпадает
	if len(translations) != len(texts) {
		log.Printf("TranslateBatch: WARNING - ожидалось %d переводов, получено %d", len(texts), len(translations))
		log.Printf("TranslateBatch: Response was: %s", response)

		// Если переводов меньше - дополняем оригиналами
		for len(translations) < len(texts) {
			translations = append(translations, texts[len(translations)])
		}
	}

	log.Printf("TranslateBatch: успешно переведено %d текстов", len(translations))
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
	// Аналогично TranslateMessagesForAdmin, но для сообщений от админа
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

		// Проверяем кеш
		if msg.Metadata != nil {
			if translations, ok := msg.Metadata["translations"].(map[string]interface{}); ok {
				if cached, exists := translations[clientLang]; exists && cached != "" {
					if cachedStr, ok := cached.(string); ok {
						msg.Content = cachedStr
						log.Printf("TranslateMessagesForWidget: использован кеш для msg %s", msg.ID)
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
