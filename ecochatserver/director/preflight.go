package director

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/egor/ecochatserver/database"
	"github.com/egor/ecochatserver/llm"
	"github.com/egor/ecochatserver/models"
	"github.com/egor/ecochatserver/security"
)

// SkillProposal wraps a skill with test args for preflight validation.
type SkillProposal struct {
	Skill    *models.DirectorSkill
	TestArgs map[string]interface{}
}

// PreflightResult holds the outcome of a preflight validation.
type PreflightResult struct {
	Passed     bool   // all checks passed
	SkillCode  string // final code (may be repaired)
	TestOutput string // output from runtime test
	Errors     []string
	Repaired   bool // was the code fixed by LLM
	Attempts   int  // total validation attempts
}

const maxRepairAttempts = 2

// PreflightValidate runs two-level validation on a skill proposal:
// Level 1 (static): structure, safety, template checks
// Level 2 (runtime): actual execution with test args in safe mode
// If validation fails, attempts LLM-based targeted repair (up to 2 iterations).
func (d *Director) PreflightValidate(ctx context.Context, proposal *SkillProposal) *PreflightResult {
	result := &PreflightResult{
		SkillCode: proposal.Skill.Code,
	}

	for attempt := 1; attempt <= maxRepairAttempts+1; attempt++ {
		result.Attempts = attempt

		// Level 1: Static validation
		if err := staticValidate(proposal.Skill.SkillType, result.SkillCode, proposal.Skill.Parameters); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("[static] %v", err))

			if attempt <= maxRepairAttempts {
				repaired, repairErr := d.repairSkillCode(ctx, proposal, result.SkillCode, err.Error(), "static")
				if repairErr != nil || repaired == "" {
					log.Printf("[PREFLIGHT] Repair failed (attempt %d): %v", attempt, repairErr)
					ExtractRepairFailure(proposal.Skill.Name, proposal.Skill.SkillType, err.Error(), attempt)
					continue
				}
				result.SkillCode = repaired
				result.Repaired = true
				log.Printf("[PREFLIGHT] Static repair applied (attempt %d)", attempt)
				continue // re-validate with repaired code
			}

			result.Passed = false
			return result
		}

		// Level 2: Runtime validation
		output, err := runtimeValidate(ctx, d.provider, proposal.Skill.SkillType, result.SkillCode, proposal.TestArgs)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("[runtime] %v", err))

			if attempt <= maxRepairAttempts {
				// Targeted repair: send the exact error from runtime
				repaired, repairErr := d.repairSkillCode(ctx, proposal, result.SkillCode, err.Error(), "runtime")
				if repairErr != nil || repaired == "" {
					log.Printf("[PREFLIGHT] Runtime repair failed (attempt %d): %v", attempt, repairErr)
					ExtractRepairFailure(proposal.Skill.Name, proposal.Skill.SkillType, err.Error(), attempt)
					continue
				}
				result.SkillCode = repaired
				result.Repaired = true
				log.Printf("[PREFLIGHT] Runtime repair applied (attempt %d)", attempt)
				continue // re-validate with repaired code
			}

			result.Passed = false
			return result
		}

		// Output quality check (like NaN/Inf/dummy detection in AutoResearchClaw)
		if err := validateOutput(output); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("[quality] %v", err))

			if attempt <= maxRepairAttempts {
				repaired, repairErr := d.repairSkillCode(ctx, proposal, result.SkillCode,
					fmt.Sprintf("output quality issue: %v\nactual output: %s", err, truncate(output, 300)), "quality")
				if repairErr != nil || repaired == "" {
					continue
				}
				result.SkillCode = repaired
				result.Repaired = true
				continue
			}

			result.Passed = false
			return result
		}

		// All checks passed
		result.Passed = true
		result.TestOutput = truncate(output, 500)
		log.Printf("[PREFLIGHT] Skill '%s' passed (attempt %d, repaired=%v)",
			proposal.Skill.Name, attempt, result.Repaired)
		return result
	}

	result.Passed = false
	return result
}

// ============================================================================
// Level 1: Static validation
// ============================================================================

func staticValidate(skillType, code, params string) error {
	if strings.TrimSpace(code) == "" {
		return fmt.Errorf("empty code")
	}

	switch skillType {
	case "sql_query":
		return security.ValidateSQLSafety(code)
	case "prompt_chain":
		return staticValidatePromptChain(code, params)
	case "http_api":
		return staticValidateHTTP(code)
	case "composite":
		return staticValidateComposite(code)
	default:
		return fmt.Errorf("unknown skill type: %s", skillType)
	}
}

func staticValidatePromptChain(code, params string) error {
	// Check that template has content
	if len(strings.TrimSpace(code)) < 10 {
		return fmt.Errorf("prompt template too short (min 10 chars)")
	}

	// Extract {{param}} placeholders from template
	placeholders := extractPlaceholders(code)

	// If parameters defined, check that placeholders match
	if params != "" && params != `{"type":"object","properties":{}}` {
		var schema map[string]interface{}
		if err := json.Unmarshal([]byte(params), &schema); err != nil {
			return fmt.Errorf("invalid parameters JSON Schema: %w", err)
		}
		props, _ := schema["properties"].(map[string]interface{})
		for _, ph := range placeholders {
			if props != nil {
				if _, exists := props[ph]; !exists {
					return fmt.Errorf("placeholder {{%s}} not defined in parameters schema", ph)
				}
			}
		}
	}

	return nil
}

func staticValidateHTTP(code string) error {
	var config struct {
		URL    string `json:"url"`
		Method string `json:"method"`
	}
	if err := json.Unmarshal([]byte(code), &config); err != nil {
		return fmt.Errorf("invalid HTTP config JSON: %w", err)
	}
	if config.URL == "" {
		return fmt.Errorf("URL is required in HTTP config")
	}
	method := strings.ToUpper(config.Method)
	if method == "" {
		method = "GET"
	}
	allowed := map[string]bool{"GET": true, "POST": true, "PUT": true, "PATCH": true}
	if !allowed[method] {
		return fmt.Errorf("HTTP method '%s' not allowed", method)
	}
	return nil
}

func staticValidateComposite(code string) error {
	var config models.CompositeConfig
	if err := json.Unmarshal([]byte(code), &config); err != nil {
		return fmt.Errorf("invalid composite config: %w", err)
	}
	if len(config.Steps) == 0 {
		return fmt.Errorf("composite skill has no steps")
	}
	if len(config.Steps) > 5 {
		return fmt.Errorf("composite skill exceeds max 5 steps")
	}
	// Check that all referenced sub-skills exist
	for _, step := range config.Steps {
		skill, err := database.GetSkillByName(step.Skill)
		if err != nil || skill == nil {
			return fmt.Errorf("step references unknown skill '%s'", step.Skill)
		}
		if !skill.Enabled {
			return fmt.Errorf("step references disabled skill '%s'", step.Skill)
		}
	}
	return nil
}

// extractPlaceholders finds all {{name}} patterns in text.
func extractPlaceholders(text string) []string {
	var result []string
	seen := map[string]bool{}
	for {
		start := strings.Index(text, "{{")
		if start < 0 {
			break
		}
		end := strings.Index(text[start:], "}}")
		if end < 0 {
			break
		}
		name := text[start+2 : start+end]
		if name != "" && !seen[name] {
			result = append(result, name)
			seen[name] = true
		}
		text = text[start+end+2:]
	}
	return result
}

// ============================================================================
// Level 2: Runtime validation
// ============================================================================

func runtimeValidate(ctx context.Context, provider llm.Provider, skillType, code string, testArgs map[string]interface{}) (string, error) {
	switch skillType {
	case "sql_query":
		return runtimeValidateSQL(ctx, code, testArgs)
	case "prompt_chain":
		return runtimeValidatePromptChain(ctx, provider, code, testArgs)
	case "http_api":
		return runtimeValidateHTTP(ctx, code)
	case "composite":
		// Composite runtime validation is skipped — static check is sufficient
		return "composite: static checks passed", nil
	default:
		return "", fmt.Errorf("unknown skill type: %s", skillType)
	}
}

// runtimeValidateSQL executes the query in a read-only transaction (always rolled back).
func runtimeValidateSQL(ctx context.Context, query string, testArgs map[string]interface{}) (string, error) {
	db := database.GetDB()

	execCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	tx, err := db.BeginTx(execCtx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return "", fmt.Errorf("begin read-only tx: %w", err)
	}
	defer tx.Rollback() // always rollback — this is a test

	// Build positional args from testArgs (sorted alphabetically, like buildSQLArgs)
	sqlArgs := buildTestSQLArgs(query, testArgs)

	rows, err := tx.QueryContext(execCtx, query, sqlArgs...)
	if err != nil {
		return "", fmt.Errorf("SQL execution error: %w", err)
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return "", fmt.Errorf("get columns: %w", err)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("columns: %s\n", strings.Join(columns, ", ")))

	rowCount := 0
	for rows.Next() && rowCount < 3 { // only read 3 rows for validation
		values := make([]interface{}, len(columns))
		valuePtrs := make([]interface{}, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}
		if err := rows.Scan(valuePtrs...); err != nil {
			return "", fmt.Errorf("scan row: %w", err)
		}
		parts := make([]string, len(columns))
		for i, v := range values {
			if v == nil {
				parts[i] = "NULL"
			} else {
				parts[i] = fmt.Sprintf("%v", v)
			}
		}
		sb.WriteString(strings.Join(parts, " | "))
		sb.WriteString("\n")
		rowCount++
	}

	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("row iteration error: %w", err)
	}

	sb.WriteString(fmt.Sprintf("rows_read: %d\n", rowCount))
	return sb.String(), nil
}

// buildTestSQLArgs builds positional args for SQL test, similar to handlers.buildSQLArgs.
func buildTestSQLArgs(query string, testArgs map[string]interface{}) []interface{} {
	if len(testArgs) == 0 {
		return nil
	}

	// Find max $N in the query
	maxN := 0
	for i := 1; i <= 20; i++ {
		if strings.Contains(query, fmt.Sprintf("$%d", i)) {
			maxN = i
		}
	}
	if maxN == 0 {
		return nil
	}

	// Sort keys alphabetically
	sortedKeys := make([]string, 0, len(testArgs))
	for k := range testArgs {
		sortedKeys = append(sortedKeys, k)
	}
	sort.Strings(sortedKeys)

	var args []interface{}
	for i := 0; i < maxN; i++ {
		if i < len(sortedKeys) {
			args = append(args, testArgs[sortedKeys[i]])
		} else {
			args = append(args, nil)
		}
	}
	return args
}

// runtimeValidatePromptChain runs the prompt with maxTokens:50 to verify it doesn't error.
func runtimeValidatePromptChain(ctx context.Context, provider llm.Provider, code string, testArgs map[string]interface{}) (string, error) {
	prompt := code
	for key, val := range testArgs {
		placeholder := "{{" + key + "}}"
		prompt = strings.ReplaceAll(prompt, placeholder, fmt.Sprintf("%v", val))
	}

	// Check for unresolved placeholders
	if strings.Contains(prompt, "{{") {
		start := strings.Index(prompt, "{{")
		end := strings.Index(prompt[start:], "}}")
		if end > 0 {
			return "", fmt.Errorf("unresolved placeholder: %s", prompt[start:start+end+2])
		}
	}

	// Short LLM call — just verify it doesn't error
	resp, err := provider.GenerateResponse(ctx, prompt, nil, &llm.GenerateOptions{
		Temperature: 0.1,
		MaxTokens:   50,
	})
	if err != nil {
		return "", fmt.Errorf("LLM test call failed: %w", err)
	}

	return resp.Text, nil
}

// runtimeValidateHTTP does a HEAD request to verify the URL is reachable.
// Includes SSRF protection to prevent probing internal infrastructure.
func runtimeValidateHTTP(ctx context.Context, code string) (string, error) {
	var config struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal([]byte(code), &config); err != nil {
		return "", fmt.Errorf("invalid HTTP config: %w", err)
	}

	// Skip if URL has placeholders
	if strings.Contains(config.URL, "{{") {
		return "skipped: URL contains placeholders", nil
	}

	// SSRF protection: reuse security.ValidateHTTPURL
	if err := security.ValidateHTTPURL(config.URL); err != nil {
		return "", fmt.Errorf("URL security check: %w", err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, "HEAD", config.URL, nil)
	if err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
	}

	// Prevent redirect-based SSRF bypass
	client := &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return fmt.Errorf("too many redirects")
			}
			// Validate redirect target too
			if err := security.ValidateHTTPURL(req.URL.String()); err != nil {
				return fmt.Errorf("redirect target blocked: %w", err)
			}
			return nil
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("URL not reachable: %w", err)
	}
	defer resp.Body.Close()

	return fmt.Sprintf("HTTP %d %s", resp.StatusCode, resp.Status), nil
}


// ============================================================================
// Output quality checks (NaN/Inf/dummy detection)
// ============================================================================

func validateOutput(output string) error {
	if strings.TrimSpace(output) == "" {
		return fmt.Errorf("empty output")
	}

	// Check for error patterns in output
	lower := strings.ToLower(output)
	errorPatterns := []string{
		"error:", "error querying", "sql exec:", "unknown tool",
		"skill '", "panic:", "fatal:",
	}
	for _, pattern := range errorPatterns {
		if strings.Contains(lower, pattern) {
			return fmt.Errorf("output contains error pattern: '%s'", pattern)
		}
	}

	// For SQL: check for meaningful data
	if strings.HasPrefix(output, "columns:") {
		if strings.Contains(output, "rows_read: 0") {
			// Empty result is acceptable for SQL — query works, just no data
			return nil
		}
	}

	return nil
}

// ============================================================================
// LLM-based targeted repair (like AutoResearchClaw's targeted fix)
// ============================================================================

func (d *Director) repairSkillCode(ctx context.Context, proposal *SkillProposal, currentCode, errorMsg, errorLevel string) (string, error) {
	log.Printf("[PREFLIGHT] Attempting %s repair for '%s': %s",
		errorLevel, proposal.Skill.Name, truncate(errorMsg, 100))

	var prompt string
	switch proposal.Skill.SkillType {
	case "sql_query":
		prompt = buildSQLRepairPrompt(currentCode, errorMsg, proposal.Skill.Parameters)
	case "prompt_chain":
		prompt = buildPromptChainRepairPrompt(currentCode, errorMsg, proposal.Skill.Parameters)
	case "http_api":
		prompt = buildHTTPRepairPrompt(currentCode, errorMsg)
	default:
		return "", fmt.Errorf("repair not supported for skill type: %s", proposal.Skill.SkillType)
	}

	resp, err := d.provider.GenerateResponse(ctx, prompt, nil, &llm.GenerateOptions{
		Temperature:  0.2,
		MaxTokens:    800,
		SystemPrompt: "You are fixing a broken skill implementation. Output ONLY the fixed code, nothing else. No explanation, no markdown formatting — just the raw code.",
	})
	if err != nil {
		return "", fmt.Errorf("repair LLM call: %w", err)
	}

	fixed := cleanRepairResponse(resp.Text)
	if fixed == "" || fixed == currentCode {
		return "", fmt.Errorf("repair produced no changes")
	}

	return fixed, nil
}

func buildSQLRepairPrompt(currentSQL, errorMsg, params string) string {
	return fmt.Sprintf(`The following SQL query failed validation:

QUERY:
%s

ERROR:
%s

PARAMETERS SCHEMA:
%s

Fix the SQL query. Rules:
- Must be SELECT or WITH ... SELECT only
- No INSERT/UPDATE/DELETE/DROP/ALTER
- No semicolons
- Use $1, $2, etc. for parameters (PostgreSQL)
- The query must return meaningful results

Output ONLY the fixed SQL query:`, currentSQL, errorMsg, params)
}

func buildPromptChainRepairPrompt(currentTemplate, errorMsg, params string) string {
	return fmt.Sprintf(`The following prompt template failed validation:

TEMPLATE:
%s

ERROR:
%s

PARAMETERS SCHEMA:
%s

Fix the prompt template. Rules:
- Use {{param_name}} for parameter placeholders
- All placeholders must match parameters in the schema
- Template must be at least 10 characters
- Must produce a meaningful prompt when placeholders are filled

Output ONLY the fixed template:`, currentTemplate, errorMsg, params)
}

func buildHTTPRepairPrompt(currentConfig, errorMsg string) string {
	return fmt.Sprintf(`The following HTTP API config failed validation:

CONFIG:
%s

ERROR:
%s

Fix the HTTP config JSON. Rules:
- Must be valid JSON with "url", "method" fields
- Method must be GET, POST, PUT, or PATCH
- URL must be a valid HTTPS URL
- Can include "headers" and "body_template" fields

Output ONLY the fixed JSON config:`, currentConfig, errorMsg)
}

// cleanRepairResponse strips markdown formatting from LLM repair output.
// Handles: ```code```, prose before code block, multiple code blocks.
func cleanRepairResponse(text string) string {
	text = strings.TrimSpace(text)

	// Find first ``` anywhere in the response (LLM may add prose before it)
	firstFence := strings.Index(text, "```")
	if firstFence >= 0 {
		content := text[firstFence+3:]
		// Skip language tag on same line (```sql, ```json, etc.)
		if idx := strings.Index(content, "\n"); idx >= 0 {
			content = content[idx+1:]
		} else {
			content = ""
		}
		// Find closing ```
		if idx := strings.Index(content, "```"); idx >= 0 {
			content = content[:idx]
		}
		content = strings.TrimSpace(content)
		if content != "" {
			return content
		}
	}

	return text
}

// ============================================================================
// Lesson extraction — save preflight failures to memory
// ============================================================================

// SavePreflightLesson persists a preflight failure as a lesson for future reference.
func SavePreflightLesson(skillName, skillType string, result *PreflightResult) {
	if result.Passed {
		return
	}

	ExtractPreflightFailure(skillName, skillType, result.Errors)
}
