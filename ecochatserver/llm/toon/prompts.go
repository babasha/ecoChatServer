package toon

import "fmt"

// Общие промпты для TOON формата
// Это избегает дублирования между провайдерами (Gemini, OpenAI, etc.)

// SystemInstructionDetectAndTranslate - system instruction для DetectAndTranslate
// /no_think отключает reasoning у Qwen3/3.5 на уровне модели (экономит ~500 токенов и ~40 секунд)
// Few-shot пример помогает модели сразу выдать правильный формат
const SystemInstructionDetectAndTranslate = `/no_think
Translator. Detect language, translate. Reply ONLY 2 lines:
lang: <code>
text: <translation>

Example — input: "Bonjour" target: en → output:
lang: fr
text: Hello`

// SystemInstructionBatch - system instruction для batch перевода
const SystemInstructionBatch = "/no_think\nTranslator. Reply with simple list, one item per line. No markdown, no numbering, no code blocks."

// SystemInstructionSimple - system instruction для простого перевода
const SystemInstructionSimple = "/no_think\nTranslator. Reply with translation only, no explanations."

// BuildDetectAndTranslatePrompt создает промпт для DetectAndTranslate
func BuildDetectAndTranslatePrompt(text, targetLang string) string {
	return fmt.Sprintf(`"%s" → %s`, text, targetLang)
}

// BuildBatchTranslatePrompt создает промпт для batch перевода
func BuildBatchTranslatePrompt(texts []string, fromLang, toLang string) string {
	prompt := fmt.Sprintf("Translate from %s to %s. Reply with ONLY translations, one per line, no numbering:\n\n", fromLang, toLang)
	for _, text := range texts {
		prompt += text + "\n"
	}
	return prompt
}

// BuildSimpleTranslatePrompt создает промпт для простого перевода
func BuildSimpleTranslatePrompt(text, fromLang, toLang string) string {
	return fmt.Sprintf(`Translate from %s to %s: "%s"`, fromLang, toLang, text)
}
