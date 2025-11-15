package toon

import (
	"fmt"
	"strings"
)

// ParseSimpleObject парсит простой TOON объект (key: value пары)
// Используется для DetectAndTranslate
//
// Пример входа:
//
//	lang: en
//	text: Hello world
//
// Результат: map[string]string{"lang": "en", "text": "Hello world"}
func ParseSimpleObject(toonStr string) (map[string]string, error) {
	result := make(map[string]string)
	lines := strings.Split(strings.TrimSpace(toonStr), "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Пропускаем markdown блоки если LLM их добавил
		if strings.HasPrefix(line, "```") {
			continue
		}

		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		// Убираем кавычки если есть
		value = strings.Trim(value, "\"'")

		result[key] = value
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("no key-value pairs found in TOON string: %s", toonStr)
	}

	return result, nil
}

// ParseSimpleList парсит простой TOON список (одна строка = один элемент)
// Используется для TranslateBatch
//
// Пример входа:
//
//	Hello
//	World
//	How are you
//
// Результат: []string{"Hello", "World", "How are you"}
func ParseSimpleList(toonStr string) ([]string, error) {
	lines := strings.Split(strings.TrimSpace(toonStr), "\n")
	results := make([]string, 0)

	inCodeBlock := false

	for _, line := range lines {
		line = strings.TrimSpace(line)

		// Пропускаем markdown блоки
		if strings.HasPrefix(line, "```") {
			inCodeBlock = !inCodeBlock
			continue
		}

		// Пропускаем пустые строки и заголовки
		if line == "" || strings.HasPrefix(line, "translations") {
			continue
		}

		// Убираем лидирующие дефисы, звездочки, номера
		line = strings.TrimPrefix(line, "-")
		line = strings.TrimPrefix(line, "*")
		line = strings.TrimPrefix(line, "•")

		// Убираем номера вида "1. ", "2) ", etc
		if len(line) > 2 && line[1] == '.' || line[1] == ')' {
			if line[0] >= '0' && line[0] <= '9' {
				line = strings.TrimSpace(line[2:])
			}
		}

		line = strings.TrimSpace(line)

		// Убираем кавычки
		line = strings.Trim(line, "\"'")

		if line != "" {
			results = append(results, line)
		}
	}

	if len(results) == 0 {
		return nil, fmt.Errorf("no items found in TOON list: %s", toonStr)
	}

	return results, nil
}

// CleanLLMResponse очищает ответ LLM от markdown и лишних символов
// Используется перед парсингом
func CleanLLMResponse(response string) string {
	response = strings.TrimSpace(response)

	// Убираем markdown код блоки
	response = strings.TrimPrefix(response, "```toon")
	response = strings.TrimPrefix(response, "```json")
	response = strings.TrimPrefix(response, "```")
	response = strings.TrimSuffix(response, "```")

	return strings.TrimSpace(response)
}
