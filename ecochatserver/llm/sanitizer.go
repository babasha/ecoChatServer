// Package llm — фильтр для скрытия природы ассистента.
package llm

import (
	"strings"
)

// forbiddenTerms — слова/фразы, при которых LLM раскрывает свою природу
// ВАЖНО: Эти термины вызывают ЭСКАЛАЦИЮ, а не просто фильтрацию
// Основные языки: RU, EN (остальные обрабатываются через LLM промпт)
var forbiddenTerms = []string{
	// Русский - основные фразы
	"вы бот", "вы робот", "ты бот", "ты робот", "это бот",

	// English - основные фразы
	"are you a bot", "are you a robot", "you are a bot", "this is a bot",
}

// escalationTriggers — фразы, которые требуют вмешательства человека
// ВАЖНО: Сделаны более специфичными, чтобы не блокировать нормальные фразы
// Основные языки: RU, EN (остальные обрабатываются через LLM промпт)
var escalationTriggers = []string{
	// ── Русский - явные признаки неспособности помочь ──
	"я не понимаю", "я не знаю", "я не могу помочь",
	"не могу вам помочь", "не могу ответить",
	"извините, но я не", "к сожалению я не",
	"обратитесь к специалисту", "свяжитесь со специалистом",
	"нужен специалист", "требуется менеджер",

	// ── English - явные признаки неспособности помочь ──
	"i don't understand", "i don't know", "i can't help",
	"i cannot help you", "i cannot answer",
	"sorry, but i can't", "unfortunately i cannot",
	"sorry but i don't", "unfortunately i don't",
	"contact specialist", "need specialist",
	"requires manager", "need to escalate",

	// ── Универсальные маркеры эскалации ──
	"#эскалация", "#escalate",
}

// sanitize проверяет текст LLM. escalate=true => нужен живой оператор.
// Strips <think> tags first to prevent false triggers from reasoning content.
func sanitize(resp string) (clean string, escalate bool) {
	// Strip <think> tags from reasoning models (Qwen 3.5, QwQ) before checking.
	// Otherwise, reasoning like "I don't know if the user means..." triggers escalation.
	resp = stripThinkTags(resp)
	lower := strings.ToLower(resp)

	// Проверяем запрещенные термины (раскрытие природы AI)
	for _, term := range forbiddenTerms {
		if strings.Contains(lower, term) {
			// LLM пытается раскрыть свою природу - нужен человек
			return "Сейчас подключу нашего специалиста, который сможет вам помочь. Одну минутку! 🙏", true
		}
	}

	// Проверяем триггеры эскалации (LLM не может помочь)
	for _, trigger := range escalationTriggers {
		if strings.Contains(lower, trigger) {
			// LLM не справился - эскалируем
			// Если в ответе уже есть вежливое объяснение, оставляем его
			if strings.Contains(lower, "специалист") || strings.Contains(lower, "менеджер") {
				return resp, true
			}
			return "Позвольте подключить нашего специалиста для решения этого вопроса. 🙏", true
		}
	}

	// Текст чистый, дополнительная очистка не нужна
	return strings.TrimSpace(resp), false
}
