package handlers

import (
	"fmt"
	"strings"
	"unicode"
)

// validateSQLSafety performs proper SQL tokenization and validates that the query
// is a safe SELECT/WITH statement. Unlike naive string.Contains checks, this handles:
// - SQL comments (-- and /* */)
// - String literals ('...' with '' escaping)
// - Dollar-quoted strings ($$...$$)
// - Identifier quoting ("...")
// - Statement chaining via semicolons
func validateSQLSafety(query string) error {
	tokens := tokenizeSQL(query)

	if len(tokens) == 0 {
		return fmt.Errorf("empty query")
	}

	// First meaningful token must be SELECT or WITH
	firstToken := strings.ToUpper(tokens[0])
	if firstToken != "SELECT" && firstToken != "WITH" {
		return fmt.Errorf("query must start with SELECT or WITH, got: %s", firstToken)
	}

	// Forbidden keywords (uppercase for comparison)
	forbidden := map[string]bool{
		"INSERT":   true,
		"UPDATE":   true,
		"DELETE":   true,
		"DROP":     true,
		"ALTER":    true,
		"TRUNCATE": true,
		"CREATE":   true,
		"GRANT":    true,
		"REVOKE":   true,
		"EXEC":     true,
		"EXECUTE":  true,
		"COPY":     true,
		"SET":      true,
		"LOCK":     true,
		"CALL":     true,
		"DO":       true,
	}

	for i, tok := range tokens {
		upper := strings.ToUpper(tok)

		// Check for semicolons (statement chaining)
		if tok == ";" {
			return fmt.Errorf("semicolons not allowed (prevents statement chaining)")
		}

		// Check forbidden keywords
		if forbidden[upper] {
			// Allow "SET" only inside window functions: ROWS/RANGE ... SET
			// Actually SET in SQL appears only as a standalone statement, so block it
			return fmt.Errorf("forbidden SQL keyword: %s", upper)
		}

		// Block SELECT ... INTO (creates tables/variables)
		if upper == "INTO" && i > 0 {
			// Check if this is SELECT ... INTO (not INSERT INTO which is already blocked)
			// INTO is allowed after: FETCH INTO, but that's rare and we're being safe
			prevUpper := strings.ToUpper(tokens[i-1])
			if prevUpper != "MERGE" && prevUpper != "FETCH" {
				// Check if there's a SELECT before this INTO (not a subquery context)
				for j := i - 1; j >= 0; j-- {
					pToken := strings.ToUpper(tokens[j])
					if pToken == "SELECT" {
						return fmt.Errorf("SELECT INTO not allowed")
					}
					if pToken == "FROM" || pToken == "JOIN" || pToken == "WHERE" {
						break // INTO after FROM/WHERE is fine (e.g. aggregate INTO)
					}
				}
			}
		}
	}

	return nil
}

// tokenizeSQL splits SQL into meaningful tokens, stripping comments and
// returning only actual SQL keywords/identifiers/operators.
// String literals and quoted identifiers are skipped (not returned as tokens).
func tokenizeSQL(query string) []string {
	var tokens []string
	runes := []rune(query)
	n := len(runes)
	i := 0

	for i < n {
		ch := runes[i]

		// Skip whitespace
		if unicode.IsSpace(ch) {
			i++
			continue
		}

		// Single-line comment: -- ...
		if ch == '-' && i+1 < n && runes[i+1] == '-' {
			for i < n && runes[i] != '\n' {
				i++
			}
			continue
		}

		// Block comment: /* ... */ (supports nesting)
		if ch == '/' && i+1 < n && runes[i+1] == '*' {
			i += 2
			depth := 1
			for i < n && depth > 0 {
				if runes[i] == '/' && i+1 < n && runes[i+1] == '*' {
					depth++
					i += 2
				} else if runes[i] == '*' && i+1 < n && runes[i+1] == '/' {
					depth--
					i += 2
				} else {
					i++
				}
			}
			continue
		}

		// String literal: '...' with '' escaping
		if ch == '\'' {
			i++ // skip opening quote
			for i < n {
				if runes[i] == '\'' {
					if i+1 < n && runes[i+1] == '\'' {
						i += 2 // escaped quote
					} else {
						i++ // closing quote
						break
					}
				} else {
					i++
				}
			}
			continue // string content is ignored for safety checks
		}

		// Dollar-quoted string: $$...$$ or $tag$...$tag$
		if ch == '$' {
			tag := extractDollarTag(runes, i)
			if tag != "" {
				i += len([]rune(tag)) // skip opening tag
				// Find closing tag
				for i < n {
					if runes[i] == '$' {
						rest := string(runes[i:])
						if strings.HasPrefix(rest, tag) {
							i += len([]rune(tag))
							break
						}
					}
					i++
				}
				continue // dollar-quoted content is ignored
			}
			// Not a dollar-quote, treat $ as regular char (parameter marker)
			// Fall through to identifier/token handling
		}

		// Quoted identifier: "..."
		if ch == '"' {
			i++ // skip opening quote
			for i < n {
				if runes[i] == '"' {
					if i+1 < n && runes[i+1] == '"' {
						i += 2 // escaped quote
					} else {
						i++ // closing quote
						break
					}
				} else {
					i++
				}
			}
			continue // quoted identifiers are ignored
		}

		// Semicolon — important, add as token
		if ch == ';' {
			tokens = append(tokens, ";")
			i++
			continue
		}

		// Operators and punctuation (skip)
		if ch == '(' || ch == ')' || ch == ',' || ch == '.' ||
			ch == '+' || ch == '*' || ch == '/' || ch == '%' ||
			ch == '<' || ch == '>' || ch == '=' || ch == '!' ||
			ch == '|' || ch == '&' || ch == '^' || ch == '~' ||
			ch == ':' || ch == '@' || ch == '#' || ch == '?' ||
			ch == '[' || ch == ']' || ch == '{' || ch == '}' {
			i++
			continue
		}

		// Word (keyword or identifier): [a-zA-Z_][a-zA-Z0-9_]*
		if ch == '_' || unicode.IsLetter(ch) {
			start := i
			for i < n && (runes[i] == '_' || unicode.IsLetter(runes[i]) || unicode.IsDigit(runes[i])) {
				i++
			}
			tokens = append(tokens, string(runes[start:i]))
			continue
		}

		// Numbers and parameter markers ($1, $2)
		if unicode.IsDigit(ch) || ch == '$' {
			start := i
			i++
			for i < n && (unicode.IsDigit(runes[i]) || unicode.IsLetter(runes[i]) || runes[i] == '_') {
				i++
			}
			// Don't add numbers/params as tokens (not relevant for safety)
			_ = start
			continue
		}

		// Unknown char, skip
		i++
	}

	return tokens
}

// extractDollarTag extracts a dollar-quote tag starting at position i.
// Returns the full tag (e.g., "$$" or "$tag$") or "" if not a valid dollar-quote.
func extractDollarTag(runes []rune, i int) string {
	n := len(runes)
	if i >= n || runes[i] != '$' {
		return ""
	}

	// Simple $$
	if i+1 < n && runes[i+1] == '$' {
		return "$$"
	}

	// $tag$ — tag is [a-zA-Z_][a-zA-Z0-9_]*
	j := i + 1
	if j >= n || (!unicode.IsLetter(runes[j]) && runes[j] != '_') {
		return "" // not a dollar-quote tag
	}

	for j < n && (unicode.IsLetter(runes[j]) || unicode.IsDigit(runes[j]) || runes[j] == '_') {
		j++
	}

	if j < n && runes[j] == '$' {
		return string(runes[i : j+1])
	}

	return ""
}
