// Package security provides shared validation functions for SQL safety
// and HTTP URL SSRF protection, used by both handlers and director packages.
package security

import (
	"fmt"
	"net"
	"net/url"
	"strings"
	"unicode"
)

// ============================================================================
// HTTP URL validation (SSRF protection)
// ============================================================================

// ValidateHTTPURL checks that the URL is safe to request (no SSRF to internal services).
func ValidateHTTPURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("only http/https schemes allowed, got: %s", parsed.Scheme)
	}

	hostname := parsed.Hostname()
	if hostname == "" {
		return fmt.Errorf("empty hostname")
	}

	blockedHosts := []string{"localhost", "127.0.0.1", "0.0.0.0", "::1", "[::1]"}
	lowerHost := strings.ToLower(hostname)
	for _, blocked := range blockedHosts {
		if lowerHost == blocked {
			return fmt.Errorf("requests to %s are not allowed", hostname)
		}
	}

	if ip := net.ParseIP(hostname); ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			return fmt.Errorf("requests to private/internal IPs are not allowed: %s", hostname)
		}
	}

	internalSuffixes := []string{".local", ".internal", ".localhost", ".svc.cluster.local"}
	for _, suffix := range internalSuffixes {
		if strings.HasSuffix(lowerHost, suffix) {
			return fmt.Errorf("requests to internal hosts are not allowed: %s", hostname)
		}
	}

	metadataIPs := []string{"169.254.169.254", "100.100.100.200", "fd00:ec2::254"}
	for _, mip := range metadataIPs {
		if hostname == mip {
			return fmt.Errorf("requests to cloud metadata endpoints are not allowed")
		}
	}

	return nil
}

// ============================================================================
// SQL safety validation
// ============================================================================

// ValidateSQLSafety performs proper SQL tokenization and validates that the query
// is a safe SELECT/WITH statement.
func ValidateSQLSafety(query string) error {
	tokens := tokenizeSQL(query)

	if len(tokens) == 0 {
		return fmt.Errorf("empty query")
	}

	firstToken := strings.ToUpper(tokens[0])
	if firstToken != "SELECT" && firstToken != "WITH" {
		return fmt.Errorf("query must start with SELECT or WITH, got: %s", firstToken)
	}

	forbidden := map[string]bool{
		"INSERT": true, "UPDATE": true, "DELETE": true, "DROP": true,
		"ALTER": true, "TRUNCATE": true, "CREATE": true, "GRANT": true,
		"REVOKE": true, "EXEC": true, "EXECUTE": true, "COPY": true,
		"SET": true, "LOCK": true, "CALL": true, "DO": true,
		"EXPLAIN": true, "VACUUM": true, "ANALYZE": true, "REINDEX": true,
		"CLUSTER": true, "LISTEN": true, "NOTIFY": true, "PREPARE": true,
		"DEALLOCATE": true,
	}

	for i, tok := range tokens {
		upper := strings.ToUpper(tok)

		if tok == ";" {
			return fmt.Errorf("semicolons not allowed (prevents statement chaining)")
		}

		if forbidden[upper] {
			return fmt.Errorf("forbidden SQL keyword: %s", upper)
		}

		if upper == "INTO" && i > 0 {
			prevUpper := strings.ToUpper(tokens[i-1])
			if prevUpper != "MERGE" && prevUpper != "FETCH" {
				for j := i - 1; j >= 0; j-- {
					pToken := strings.ToUpper(tokens[j])
					if pToken == "SELECT" {
						return fmt.Errorf("SELECT INTO not allowed")
					}
					if pToken == "FROM" || pToken == "JOIN" || pToken == "WHERE" {
						break
					}
				}
			}
		}
	}

	return nil
}

// tokenizeSQL splits SQL into meaningful tokens, stripping comments and
// returning only actual SQL keywords/identifiers/operators.
func tokenizeSQL(query string) []string {
	var tokens []string
	runes := []rune(query)
	n := len(runes)
	i := 0

	for i < n {
		ch := runes[i]

		if unicode.IsSpace(ch) {
			i++
			continue
		}

		if ch == '-' && i+1 < n && runes[i+1] == '-' {
			for i < n && runes[i] != '\n' {
				i++
			}
			continue
		}

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

		if ch == '\'' {
			i++
			for i < n {
				if runes[i] == '\'' {
					if i+1 < n && runes[i+1] == '\'' {
						i += 2
					} else {
						i++
						break
					}
				} else {
					i++
				}
			}
			continue
		}

		if ch == '$' {
			tag := extractDollarTag(runes, i)
			if tag != "" {
				i += len([]rune(tag))
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
				continue
			}
		}

		if ch == '"' {
			i++
			for i < n {
				if runes[i] == '"' {
					if i+1 < n && runes[i+1] == '"' {
						i += 2
					} else {
						i++
						break
					}
				} else {
					i++
				}
			}
			continue
		}

		if ch == ';' {
			tokens = append(tokens, ";")
			i++
			continue
		}

		if ch == '(' || ch == ')' || ch == ',' || ch == '.' ||
			ch == '+' || ch == '*' || ch == '/' || ch == '%' ||
			ch == '<' || ch == '>' || ch == '=' || ch == '!' ||
			ch == '|' || ch == '&' || ch == '^' || ch == '~' ||
			ch == ':' || ch == '@' || ch == '#' || ch == '?' ||
			ch == '[' || ch == ']' || ch == '{' || ch == '}' {
			i++
			continue
		}

		if ch == '_' || unicode.IsLetter(ch) {
			start := i
			for i < n && (runes[i] == '_' || unicode.IsLetter(runes[i]) || unicode.IsDigit(runes[i])) {
				i++
			}
			tokens = append(tokens, string(runes[start:i]))
			continue
		}

		if unicode.IsDigit(ch) || ch == '$' {
			i++
			for i < n && (unicode.IsDigit(runes[i]) || unicode.IsLetter(runes[i]) || runes[i] == '_') {
				i++
			}
			continue
		}

		i++
	}

	return tokens
}

func extractDollarTag(runes []rune, i int) string {
	n := len(runes)
	if i >= n || runes[i] != '$' {
		return ""
	}

	if i+1 < n && runes[i+1] == '$' {
		return "$$"
	}

	j := i + 1
	if j >= n || (!unicode.IsLetter(runes[j]) && runes[j] != '_') {
		return ""
	}

	for j < n && (unicode.IsLetter(runes[j]) || unicode.IsDigit(runes[j]) || runes[j] == '_') {
		j++
	}

	if j < n && runes[j] == '$' {
		return string(runes[i : j+1])
	}

	return ""
}
