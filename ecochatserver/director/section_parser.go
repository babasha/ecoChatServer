package director

import "strings"

// SectionParser extracts named sections from structured LLM responses.
// Sections are identified by "SECTION_NAME:" markers.
type SectionParser struct {
	text      string
	sections  []string
	positions map[string]int
}

// NewSectionParser creates a parser for the given text with known section markers.
// Section names should include the colon, e.g. "ANALYSIS:", "DIRECTIVES:".
func NewSectionParser(text string, sections []string) *SectionParser {
	positions := make(map[string]int, len(sections))
	for _, s := range sections {
		positions[s] = strings.Index(text, s)
	}
	return &SectionParser{text: text, sections: sections, positions: positions}
}

// Get extracts the text of a section (trimmed, between this marker and the next).
func (p *SectionParser) Get(section string) string {
	start, ok := p.positions[section]
	if !ok || start < 0 {
		return ""
	}
	start += len(section)

	end := len(p.text)
	for _, other := range p.sections {
		if other == section {
			continue
		}
		otherPos := p.positions[other]
		if otherPos > p.positions[section] && otherPos < end {
			end = otherPos
		}
	}
	return strings.TrimSpace(p.text[start:end])
}

// GetBulletList extracts a section and parses it as a bulleted list (- or * prefix).
func (p *SectionParser) GetBulletList(section string) []string {
	return ParseBulletList(p.Get(section))
}

// Has returns true if the section marker was found in the text.
func (p *SectionParser) Has(section string) bool {
	pos, ok := p.positions[section]
	return ok && pos >= 0
}

// ParseBulletList extracts items from lines starting with "- " or "* ".
func ParseBulletList(text string) []string {
	var items []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "- ") {
			items = append(items, strings.TrimSpace(line[2:]))
		} else if strings.HasPrefix(line, "* ") {
			items = append(items, strings.TrimSpace(line[2:]))
		}
	}
	return items
}
