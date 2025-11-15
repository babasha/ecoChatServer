package toon

import "fmt"

// Общие промпты для TOON формата
// Это избегает дублирования между провайдерами (Gemini, OpenAI, etc.)

// SystemInstructionDetectAndTranslate - system instruction для DetectAndTranslate
const SystemInstructionDetectAndTranslate = "Language translator. Use TOON format: key: value. No markdown, no code blocks."

// SystemInstructionBatch - system instruction для batch перевода
const SystemInstructionBatch = "Translator. Reply with simple list, one item per line. No markdown, no numbering, no code blocks."

// SystemInstructionSimple - system instruction для простого перевода
const SystemInstructionSimple = "Translator. Reply with translation only, no explanations."

// BuildDetectAndTranslatePrompt создает промпт для DetectAndTranslate
func BuildDetectAndTranslatePrompt(text, targetLang string) string {
	return fmt.Sprintf(`Detect language and translate to %s: "%s"

Reply in TOON format (NO JSON, NO MARKDOWN, NO CODE BLOCKS):
lang: <code>
text: <translation>`, targetLang, text)
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
