package adkagent

import (
	"log"
	"strings"
	"unicode"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/tool"
)

// ============================================================================
// ToolRouter — dynamic per-request tool selection via tool.Toolset
//
// Scores 3 tool groups (plant, device, support) against user message tokens.
// Only tools from matching groups are sent to the LLM, saving ~500-700 tokens.
// Fallback: if no group matches, ALL 16 tools are returned (safe default).
// search_faq is always included as universal fallback tool.
// ============================================================================

// toolGroup represents a group of related tools with scoring keywords.
type toolGroup struct {
	name     string
	tools    []tool.Tool
	keywords map[string]float64 // keyword → weight
}

// ToolRouter implements tool.Toolset for dynamic per-request tool selection.
type ToolRouter struct {
	groups   []toolGroup
	allTools []tool.Tool // fallback: all tools combined

	// Scoring threshold: groups scoring above this are included
	threshold float64
}

// NewToolRouter creates a ToolRouter from the three tool groups.
// Keywords are auto-extracted from tool names, descriptions, and parameter enums.
func NewToolRouter(plantTools, deviceTools, supportTools []tool.Tool) *ToolRouter {
	// Collect all tools for fallback
	var allTools []tool.Tool
	allTools = append(allTools, plantTools...)
	allTools = append(allTools, deviceTools...)
	allTools = append(allTools, supportTools...)

	// Build keyword maps for each group (tool names + param enums only, no descriptions)
	plantKW := buildGroupKeywords("plant", plantTools)
	deviceKW := buildGroupKeywords("device", deviceTools)
	supportKW := buildGroupKeywords("support", supportTools)

	// Add group-level keywords (curated, non-overlapping domain terms)
	addGroupLevelKeywords(plantKW, []string{
		"plant", "flower", "species", "humidity", "moisture", "care",
		"watering", "soil", "tropical", "succulent", "herb", "vegetable",
		"flowering", "monstera", "cactus", "orchid", "basil", "fern",
		"compare", "recommend", "category", "edible", "light",
	}, 1.5)

	addGroupLevelKeywords(deviceKW, []string{
		"sensor", "device", "battery", "charge", "setup", "connect",
		"pair", "pairing", "bluetooth", "ble", "wifi", "mesh", "esp32",
		"firmware", "troubleshoot", "offline", "reading", "led",
		"calibrate", "probe", "usb", "topology", "esp_now", "root_node",
	}, 1.5)

	addGroupLevelKeywords(supportKW, []string{
		"app", "application", "contact", "support", "help", "faq",
		"security", "privacy", "encryption", "notification", "alert",
		"feature", "passport", "prediction", "history", "export",
		"chart", "api", "platform", "language", "free", "cost", "price",
		"home_assistant", "maps",
	}, 1.5)

	// Add FAQ tags to support group, but EXCLUDE tags that belong to
	// plant or device domains to prevent cross-group pollution.
	excludeFromSupport := mergeKeysets(plantKW, deviceKW)
	for _, tag := range GetFAQTags() {
		if _, inPlantOrDevice := excludeFromSupport[tag]; inPlantOrDevice {
			continue // skip device/plant keywords like "sensor", "battery", "wifi"
		}
		if _, exists := supportKW[tag]; !exists {
			supportKW[tag] = 2.0
		}
	}

	// Deconflict: group-level keywords are "owned" by their group.
	// Remove them from other groups where they leaked via param splitting
	// (e.g., "plant" from "plant_assign" in device, "plant_passport" in support).
	plantExclusive := []string{
		"plant", "flower", "species", "humidity", "moisture", "care",
		"watering", "soil", "tropical", "succulent", "herb", "vegetable",
		"flowering", "monstera", "cactus", "orchid", "basil", "fern",
		"compare", "recommend", "category", "edible", "light",
	}
	deviceExclusive := []string{
		"sensor", "sensors", "device", "battery", "charge", "setup", "connect",
		"pair", "pairing", "bluetooth", "ble", "wifi", "mesh", "esp32",
		"firmware", "troubleshoot", "offline", "reading", "led",
		"calibrate", "probe", "usb", "topology", "network",
	}
	supportExclusive := []string{
		"app", "application", "contact", "support", "help", "faq",
		"security", "privacy", "encryption", "notification", "alert",
		"feature", "passport", "prediction", "history", "export",
		"chart", "api", "platform", "language", "free", "cost", "price",
	}

	removeKeywords(deviceKW, plantExclusive)   // remove plant words leaked into device
	removeKeywords(supportKW, plantExclusive)   // remove plant words leaked into support
	removeKeywords(plantKW, deviceExclusive)    // remove device words leaked into plant
	removeKeywords(supportKW, deviceExclusive)  // remove device words leaked into support
	removeKeywords(plantKW, supportExclusive)   // remove support words leaked into plant
	removeKeywords(deviceKW, supportExclusive)  // remove support words leaked into device

	tr := &ToolRouter{
		groups: []toolGroup{
			{name: "plant", tools: plantTools, keywords: plantKW},
			{name: "device", tools: deviceTools, keywords: deviceKW},
			{name: "support", tools: supportTools, keywords: supportKW},
		},
		allTools:  allTools,
		threshold: 2.0,
	}

	log.Printf("[TOOL_ROUTER] Initialized: %d groups, %d total tools, threshold=%.1f",
		len(tr.groups), len(allTools), tr.threshold)
	for _, g := range tr.groups {
		log.Printf("[TOOL_ROUTER]   Group '%s': %d tools, %d keywords",
			g.name, len(g.tools), len(g.keywords))
	}

	return tr
}

// Name implements tool.Toolset.
func (tr *ToolRouter) Name() string {
	return "zefir_tool_router"
}

// Tools implements tool.Toolset. Called by ADK on every request.
// Extracts text from UserContent, scores groups, returns relevant tools.
func (tr *ToolRouter) Tools(ctx agent.ReadonlyContext) ([]tool.Tool, error) {
	// 1. Extract text from user message
	text := extractUserText(ctx)
	if text == "" {
		log.Printf("[TOOL_ROUTER] Empty user text, returning all %d tools (fallback)", len(tr.allTools))
		return tr.allTools, nil
	}

	// 2. Tokenize and expand via synonyms
	tokens := tokenize(text)

	// 3. Score each group
	type scored struct {
		idx   int
		score float64
	}
	var matches []scored

	for i, group := range tr.groups {
		score := scoreGroup(group, tokens)
		if score >= tr.threshold {
			matches = append(matches, scored{idx: i, score: score})
		}
	}

	// 4. If no group matches threshold, return all tools (safe fallback)
	if len(matches) == 0 {
		log.Printf("[TOOL_ROUTER] No group above threshold (%.1f) for: %q → returning all %d tools",
			tr.threshold, truncate(text, 80), len(tr.allTools))
		return tr.allTools, nil
	}

	// 5. Collect tools from matching groups + always include search_faq
	var selected []tool.Tool
	groupNames := make([]string, 0, len(matches))
	hasFAQ := false

	for _, m := range matches {
		group := tr.groups[m.idx]
		groupNames = append(groupNames, group.name)
		for _, t := range group.tools {
			selected = append(selected, t)
			if toolName(t) == "search_faq" {
				hasFAQ = true
			}
		}
	}

	// Always include search_faq as universal fallback
	if !hasFAQ {
		if faqTool := tr.findTool("search_faq"); faqTool != nil {
			selected = append(selected, faqTool)
		}
	}

	log.Printf("[TOOL_ROUTER] Selected %d tools (groups: %s) for: %q",
		len(selected), strings.Join(groupNames, "+"), truncate(text, 80))

	return selected, nil
}

// ============================================================================
// Keyword index building
// ============================================================================

// buildGroupKeywords extracts keywords from tool names and parameter enums.
// Description tokens are intentionally excluded — they contain generic words
// (e.g., "guide", "info", "list") that cause cross-group pollution.
func buildGroupKeywords(groupName string, tools []tool.Tool) map[string]float64 {
	kw := make(map[string]float64)

	for _, t := range tools {
		name := toolName(t)

		// Tool name tokens (high weight): "search_plant" → ["search", "plant"]
		for _, token := range splitIdentifier(name) {
			token = strings.ToLower(token)
			if !stopWords[token] && len(token) > 1 {
				kw[token] = max(kw[token], 3.0)
			}
		}

		// Parameter enum keywords (high weight)
		if paramKW, ok := toolParamKeywords[name]; ok {
			for _, pk := range paramKW {
				// Split compound param names: "ble_pairing" → "ble", "pairing"
				for _, part := range strings.Split(pk, "_") {
					part = strings.ToLower(part)
					if len(part) > 1 {
						kw[part] = max(kw[part], 2.5)
					}
				}
				// Also add full param name for exact matches
				kw[strings.ToLower(pk)] = max(kw[strings.ToLower(pk)], 2.5)
			}
		}
	}

	return kw
}

// addGroupLevelKeywords adds domain-level keywords with specified weight.
func addGroupLevelKeywords(kw map[string]float64, keywords []string, weight float64) {
	for _, k := range keywords {
		kw[strings.ToLower(k)] = max(kw[strings.ToLower(k)], weight)
	}
}

// ============================================================================
// Scoring
// ============================================================================

// scoreGroup scores a tool group against the tokenized user message.
func scoreGroup(group toolGroup, tokens []string) float64 {
	var score float64
	seen := make(map[string]bool) // avoid double-counting same keyword

	for _, token := range tokens {
		if seen[token] {
			continue
		}
		if weight, ok := group.keywords[token]; ok {
			score += weight
			seen[token] = true
		}
	}

	return score
}

// ============================================================================
// Tokenization
// ============================================================================

// tokenize splits user text into lowercase tokens and expands via synonym map.
// For CJK text (Chinese/Japanese/Korean), performs substring matching against
// the synonym dictionary since CJK languages don't use spaces between words.
func tokenize(text string) []string {
	raw := tokenizeSimple(text)

	var expanded []string
	for _, token := range raw {
		if stopWords[token] {
			continue
		}

		// Check if token contains CJK characters — if so, do substring matching
		if containsCJK(token) {
			cjkTokens := extractCJKSynonyms(token)
			expanded = append(expanded, cjkTokens...)
			continue
		}

		expanded = append(expanded, token)

		// Expand via synonym map
		if syn, ok := synonyms[token]; ok {
			expanded = append(expanded, strings.ToLower(syn))
		}
	}

	return expanded
}

// tokenizeSimple splits text into lowercase tokens on whitespace and punctuation.
func tokenizeSimple(text string) []string {
	lower := strings.ToLower(text)

	// Split on whitespace and common punctuation
	fields := strings.FieldsFunc(lower, func(r rune) bool {
		return unicode.IsSpace(r) || r == ',' || r == '.' || r == '!' ||
			r == '?' || r == ':' || r == ';' || r == '"' || r == '\'' ||
			r == '(' || r == ')' || r == '[' || r == ']' || r == '/' ||
			r == '-' || r == '—' || r == '–'
	})

	var tokens []string
	for _, f := range fields {
		if len(f) > 0 {
			tokens = append(tokens, f)
		}
	}
	return tokens
}

// splitIdentifier splits a snake_case identifier into parts.
func splitIdentifier(name string) []string {
	return strings.Split(name, "_")
}

// ============================================================================
// Helpers
// ============================================================================

// extractUserText extracts the text content from the user's message.
func extractUserText(ctx agent.ReadonlyContext) string {
	content := ctx.UserContent()
	if content == nil || len(content.Parts) == 0 {
		return ""
	}

	var sb strings.Builder
	for _, part := range content.Parts {
		if part.Text != "" {
			if sb.Len() > 0 {
				sb.WriteString(" ")
			}
			sb.WriteString(part.Text)
		}
	}
	return sb.String()
}

// toolName returns the name of a tool.
func toolName(t tool.Tool) string {
	return t.Name()
}

// toolDescription returns the description of a tool.
func toolDescription(t tool.Tool) string {
	return t.Description()
}

// findTool finds a tool by name across all groups.
func (tr *ToolRouter) findTool(name string) tool.Tool {
	for _, t := range tr.allTools {
		if toolName(t) == name {
			return t
		}
	}
	return nil
}

// containsCJK checks if a string contains CJK (Chinese/Japanese/Korean) characters.
func containsCJK(s string) bool {
	for _, r := range s {
		if isCJK(r) {
			return true
		}
	}
	return false
}

// isCJK returns true if the rune is a CJK unified ideograph.
func isCJK(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) || // CJK Unified Ideographs
		(r >= 0x3400 && r <= 0x4DBF) || // CJK Extension A
		(r >= 0xF900 && r <= 0xFAFF) // CJK Compatibility Ideographs
}

// extractCJKSynonyms scans a CJK string for known synonym substrings.
// Returns both the matched CJK tokens and their English translations.
// Uses longest-match-first to handle multi-char synonyms (e.g., "传感器" = 3 chars).
func extractCJKSynonyms(text string) []string {
	var result []string
	runes := []rune(text)

	// First, try matching known CJK synonyms (longest first: 3, 2, 1 chars)
	matched := make(map[string]bool)
	for i := 0; i < len(runes); i++ {
		// Try 3-char, then 2-char, then 1-char matches
		for length := 3; length >= 1; length-- {
			if i+length > len(runes) {
				continue
			}
			candidate := string(runes[i : i+length])
			if syn, ok := synonyms[candidate]; ok && !matched[candidate] {
				matched[candidate] = true
				result = append(result, candidate)
				result = append(result, strings.ToLower(syn))
				i += length - 1 // advance past matched chars
				break
			}
		}
	}

	// Also check CJK stop words at single-char level
	// (already filtered since they won't match synonyms)

	return result
}

// removeKeywords deletes specified keywords from a keyword map.
// Used to deconflict cross-group pollution from param splitting.
func removeKeywords(kw map[string]float64, toRemove []string) {
	for _, k := range toRemove {
		delete(kw, strings.ToLower(k))
	}
}

// mergeKeysets returns a set of all keys present in either map.
func mergeKeysets(a, b map[string]float64) map[string]bool {
	merged := make(map[string]bool, len(a)+len(b))
	for k := range a {
		merged[k] = true
	}
	for k := range b {
		merged[k] = true
	}
	return merged
}

// truncate is defined in types.go — reused here for log messages
