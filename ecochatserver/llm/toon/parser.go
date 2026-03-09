package toon

import (
	"fmt"
	"regexp"
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

		// Убираем номера вида "1. ", "2) ", "10. ", "12) " etc
		if len(line) > 1 {
			numEnd := 0
			for numEnd < len(line) && line[numEnd] >= '0' && line[numEnd] <= '9' {
				numEnd++
			}
			if numEnd > 0 && numEnd < len(line) && (line[numEnd] == '.' || line[numEnd] == ')') {
				line = strings.TrimSpace(line[numEnd+1:])
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

// thinkTagRe strips <think>...</think> blocks from Qwen3.5 reasoning output
var (
	toonThinkTagRe         = regexp.MustCompile(`(?s)<think>.*?</think>\s*`)
	toonUnclosedThinkTagRe = regexp.MustCompile(`(?s)<think>.*$`)
)

// CleanLLMResponse очищает ответ LLM от markdown, thinking-блоков и лишних символов
// Используется перед парсингом
func CleanLLMResponse(response string) string {
	response = strings.TrimSpace(response)

	// Убираем <think>...</think> блоки (Qwen3.5 reasoning)
	if strings.Contains(response, "<think>") {
		response = toonThinkTagRe.ReplaceAllString(response, "")
		response = toonUnclosedThinkTagRe.ReplaceAllString(response, "")
		response = strings.TrimSpace(response)
	}

	// Убираем untagged thinking (Qwen3.5 с enable_thinking=false)
	// Формат: "Thinking Process:\n...\n\nlang: ru\ntext: ..."
	if strings.HasPrefix(response, "Thinking Process:") || strings.HasPrefix(response, "Thinking:") {
		// Ищем последний блок после пустой строки — это реальный ответ
		lines := strings.Split(response, "\n")
		for i := len(lines) - 1; i >= 0; i-- {
			if strings.TrimSpace(lines[i]) == "" && i < len(lines)-1 {
				candidate := strings.TrimSpace(strings.Join(lines[i+1:], "\n"))
				if candidate != "" && !strings.HasPrefix(candidate, "Thinking") && !strings.HasPrefix(candidate, "*") {
					response = candidate
					break
				}
			}
		}
	}

	// Убираем markdown код блоки
	response = strings.TrimPrefix(response, "```toon")
	response = strings.TrimPrefix(response, "```json")
	response = strings.TrimPrefix(response, "```")
	response = strings.TrimSuffix(response, "```")

	return strings.TrimSpace(response)
}
