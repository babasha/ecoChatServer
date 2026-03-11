package director

import (
	"strings"
)

// ParsedDirectorResponse holds all sections from the Director's LLM response.
type ParsedDirectorResponse struct {
	Analysis           string
	Directives         []Directive
	CustomerComplaints []string
	KeyObservations    []string
	Expectations       string
}

// parseDirectorResponse splits LLM response into structured sections.
func parseDirectorResponse(text string) *ParsedDirectorResponse {
	result := &ParsedDirectorResponse{}

	// Section markers in expected order
	sections := []string{"ANALYSIS:", "CUSTOMER_COMPLAINTS:", "KEY_OBSERVATIONS:", "DIRECTIVES:", "EXPECTATIONS:"}

	// Find positions of all sections
	positions := map[string]int{}
	for _, s := range sections {
		positions[s] = strings.Index(text, s)
	}

	// Helper: extract text between two sections
	extractSection := func(sectionName string) string {
		start := positions[sectionName]
		if start < 0 {
			return ""
		}
		start += len(sectionName)

		// Find the next section that comes after this one
		end := len(text)
		for _, other := range sections {
			if other == sectionName {
				continue
			}
			otherPos := positions[other]
			if otherPos > positions[sectionName] && otherPos < end {
				end = otherPos
			}
		}
		return strings.TrimSpace(text[start:end])
	}

	// Helper: parse bulleted list from section text
	parseBulletList := func(sectionText string) []string {
		var items []string
		for _, line := range strings.Split(sectionText, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "- ") {
				items = append(items, strings.TrimSpace(line[2:]))
			} else if strings.HasPrefix(line, "* ") {
				items = append(items, strings.TrimSpace(line[2:]))
			}
		}
		return items
	}

	// 1. Analysis
	result.Analysis = extractSection("ANALYSIS:")
	if result.Analysis == "" {
		result.Analysis = text // fallback: entire text
	}

	// 2. Customer complaints
	result.CustomerComplaints = parseBulletList(extractSection("CUSTOMER_COMPLAINTS:"))

	// 3. Key observations
	result.KeyObservations = parseBulletList(extractSection("KEY_OBSERVATIONS:"))

	// 4. Directives
	directivesText := extractSection("DIRECTIVES:")
	for _, line := range strings.Split(directivesText, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "- ") && !strings.HasPrefix(line, "* ") {
			continue
		}
		line = strings.TrimPrefix(line, "- ")
		line = strings.TrimPrefix(line, "* ")

		dir := parseDirectiveLine(line)
		if dir.Instruction != "" {
			result.Directives = append(result.Directives, dir)
		}
	}

	// 5. Expectations
	result.Expectations = extractSection("EXPECTATIONS:")

	return result
}

func parseDirectiveLine(line string) Directive {
	dir := Directive{
		Type:     "prompt_update",
		Priority: "medium",
	}

	// Extract [type:...] tag
	if idx := strings.Index(line, "[type:"); idx >= 0 {
		end := strings.Index(line[idx:], "]")
		if end > 0 {
			dir.Type = line[idx+6 : idx+end]
			line = line[:idx] + line[idx+end+1:]
		}
	}

	// Extract [priority:...] tag
	if idx := strings.Index(line, "[priority:"); idx >= 0 {
		end := strings.Index(line[idx:], "]")
		if end > 0 {
			dir.Priority = line[idx+10 : idx+end]
			line = line[:idx] + line[idx+end+1:]
		}
	}

	dir.Instruction = strings.TrimSpace(line)
	dir.Description = dir.Instruction

	return dir
}

// getDirectorSystemPrompt returns the system prompt for LLM analysis calls.
func getDirectorSystemPrompt() string {
	// Load identity for analysis context
	identityPrompt := BuildIdentitySystemPrompt()

	var sb strings.Builder
	if identityPrompt != "" {
		sb.WriteString(identityPrompt)
		sb.WriteString("\n")
	}

	sb.WriteString(`You analyze conversation summaries and support metrics to improve the AI assistant's performance.

Your output MUST follow this exact format (all sections required):

ANALYSIS:
<3-7 sentences analyzing the overall situation, patterns, quality of responses, and areas for improvement>

CUSTOMER_COMPLAINTS:
- <specific complaint or pain point extracted from conversations>
- <another complaint...>
(list every distinct complaint/frustration you found in the summaries, even minor ones)

KEY_OBSERVATIONS:
- <pattern, trend, or notable observation across conversations>
- <another observation...>
(what's working well, what's failing, recurring topics, behavioral patterns)

DIRECTIVES:
- [type:prompt_update] [priority:high/medium/low] <instruction for the support agent>
- [type:faq_gap] [priority:high/medium/low] <missing FAQ topic to address>
- [type:pattern] [priority:high/medium/low] <pattern observation and recommendation>
- [type:alert] [priority:high/medium/low] <urgent issue requiring attention>

EXPECTATIONS:
<2-4 sentences: what specific improvements you expect from these directives. What metrics should improve? What customer experience changes should occur? How will you measure success?>

Rules:
- Be specific and actionable — no vague advice
- Quote actual customer phrases when describing complaints
- Identify root causes, not just symptoms
- Focus on improving customer experience
- Flag recurring issues that need FAQ updates
- Max 5 directives per report
- Write in the same language as the summaries`)

	return sb.String()
}
