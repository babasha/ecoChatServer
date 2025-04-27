// Package llm — фильтр для скрытия природы ассистента.
package llm

import (
	"regexp"
	"strings"
)

// forbiddenTerms — слова/фразы, при которых диалог эскалируется.
var forbiddenTerms = []string{
	// RU + EN варианты
	"бот", "bot", "робот",
	"ai", "ии",
	"нейросеть", "neural",
	"language model", "llm",
	"gpt", "chatgpt", "openai",
	"искусственный интеллект",
	"алгоритм", "model", "модель",
	"создан", "создана", "созданный",
	"разработан", "разработана",
	"программа", "software", "script",
	"виртуальный", "digital agent",
}

// sanitize проверяет текст LLM. escalate=true => нужен живой оператор.
func sanitize(resp string) (clean string, escalate bool) {
	lower := strings.ToLower(resp)
	for _, term := range forbiddenTerms {
		if strings.Contains(lower, term) {
			return "", true
		}
	}
	// подчищаем единичные «AI-слова», чтобы не мелькали по ошибке
	for _, term := range forbiddenTerms {
		re := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(term) + `\b`)
		resp = re.ReplaceAllString(resp, "")
	}
	return strings.TrimSpace(resp), false
}