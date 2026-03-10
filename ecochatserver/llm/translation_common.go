package llm

import (
	"log"
	"strings"
)

// ============================================================================
// Translation common utilities — shared across Gemini and OpenAI translation
// ============================================================================

// LangNames maps two-letter language codes to readable names (for prompts).
var LangNames = map[string]string{
	"ru": "Russian",
	"en": "English",
	"pl": "Polish",
	"de": "German",
	"fr": "French",
	"es": "Spanish",
	"it": "Italian",
	"uk": "Ukrainian",
	"cs": "Czech",
	"sk": "Slovak",
	"lt": "Lithuanian",
	"lv": "Latvian",
	"et": "Estonian",
	"ka": "Georgian",
	"uz": "Uzbek",
}

// LangDisplayName returns the readable language name for a code, or the code itself.
func LangDisplayName(code string) string {
	if name, ok := LangNames[code]; ok {
		return name
	}
	return code
}

// CleanJSONResponse removes markdown code blocks and <think> tags from LLM responses.
func CleanJSONResponse(text string) string {
	text = stripThinkTags(text)
	text = strings.TrimSpace(text)

	if strings.HasPrefix(text, "```") {
		lines := strings.Split(text, "\n")
		if len(lines) > 0 {
			lines = lines[1:] // Remove opening ```json or ```
		}
		if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "```" {
			lines = lines[:len(lines)-1]
		}
		text = strings.Join(lines, "\n")
	}

	return strings.TrimSpace(text)
}

// NormalizeBatchTranslations adjusts the translations slice to match the originals length.
// Pads with originals if too few, truncates if too many.
func NormalizeBatchTranslations(translations, originals []string) []string {
	if len(translations) != len(originals) {
		log.Printf("NormalizeBatchTranslations: WARNING - expected %d, got %d translations", len(originals), len(translations))
		for len(translations) < len(originals) {
			translations = append(translations, originals[len(translations)])
		}
		if len(translations) > len(originals) {
			translations = translations[:len(originals)]
		}
	}
	return translations
}
