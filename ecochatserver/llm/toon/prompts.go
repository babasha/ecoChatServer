package toon

import "fmt"

// Общие промпты для TOON формата
// Это избегает дублирования между провайдерами (Gemini, OpenAI, etc.)

// SystemInstructionDetectAndTranslate - system instruction для DetectAndTranslate
// Явная инструкция + few-shot пример для маленьких моделей (2B-4B)
const SystemInstructionDetectAndTranslate = `Detect source language and translate text to the target language. Reply ONLY 2 lines:
lang: <source_language_code>
text: <translated_text>

Example — input: "Bonjour" translate to: en → output:
lang: fr
text: Hello`

// SystemInstructionBatch - system instruction для batch перевода
const SystemInstructionBatch = "Translator. Reply with simple list, one item per line. No markdown, no numbering, no code blocks."

// SystemInstructionSimple - system instruction для простого перевода
const SystemInstructionSimple = "Translator. Reply with translation only, no explanations."

// BuildDetectAndTranslatePrompt создает промпт для DetectAndTranslate
func BuildDetectAndTranslatePrompt(text, targetLang string) string {
	return fmt.Sprintf(`"%s" translate to: %s`, text, targetLang)
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
