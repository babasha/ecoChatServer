package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/egor/ecochatserver/database"
	"github.com/egor/ecochatserver/llm"
	"github.com/egor/ecochatserver/models"
	"github.com/google/uuid"
)

// ============================================================================
// Dynamic tool loading — merges built-in tools with custom skills from DB
// ============================================================================

// skillsCache caches the merged tools list to avoid DB queries on every chat request.
var (
	skillsCache      []llm.Tool
	skillsCacheMu    sync.RWMutex
	skillsCacheAt    time.Time
	skillsCacheTTL   = 30 * time.Second
)

// InvalidateSkillsCache forces the next getDirectorToolsWithSkills call to reload from DB.
func InvalidateSkillsCache() {
	skillsCacheMu.Lock()
	skillsCacheAt = time.Time{}
	skillsCacheMu.Unlock()
}

// getDirectorToolsWithSkills returns all available tools: built-in + custom skills.
func getDirectorToolsWithSkills() []llm.Tool {
	skillsCacheMu.RLock()
	if skillsCache != nil && time.Since(skillsCacheAt) < skillsCacheTTL {
		cached := skillsCache
		skillsCacheMu.RUnlock()
		return cached
	}
	skillsCacheMu.RUnlock()

	skills, err := database.GetEnabledSkills()
	if err != nil {
		log.Printf("[DIRECTOR_SKILLS] Failed to load custom skills: %v", err)
		return directorTools
	}

	var allTools []llm.Tool

	if len(skills) == 0 {
		allTools = directorTools
	} else {
		// Merge: built-in first, then custom
		allTools = make([]llm.Tool, len(directorTools), len(directorTools)+len(skills))
		copy(allTools, directorTools)

		for _, s := range skills {
			var params map[string]interface{}
			if s.Parameters != "" && s.Parameters != "{}" {
				if err := json.Unmarshal([]byte(s.Parameters), &params); err != nil {
					log.Printf("[DIRECTOR_SKILLS] Bad params for skill %s: %v", s.Name, err)
					params = map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
				}
			} else {
				params = map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
			}

			allTools = append(allTools, llm.Tool{
				Name:        s.Name,
				Description: fmt.Sprintf("[CUSTOM] %s", s.Description),
				Parameters:  params,
			})
		}

		log.Printf("[DIRECTOR_SKILLS] Loaded %d custom skills", len(skills))
	}

	// Update cache (including empty-skills case to avoid repeated DB queries)
	skillsCacheMu.Lock()
	skillsCache = allTools
	skillsCacheAt = time.Now()
	skillsCacheMu.Unlock()

	return allTools
}

// ============================================================================
// Custom skill executor — runs sql_query, prompt_chain, http_api skills
// ============================================================================

func executeCustomSkill(ctx context.Context, name string, args map[string]interface{}) string {
	skill, err := database.GetSkillByName(name)
	if err != nil {
		return fmt.Sprintf("Unknown tool: %s", name)
	}

	if !skill.Enabled {
		return fmt.Sprintf("Skill '%s' is disabled.", name)
	}

	var result string
	var execErr error

	switch skill.SkillType {
	case "sql_query":
		result, execErr = executeSQLSkill(skill, args)
	case "prompt_chain":
		result, execErr = executePromptSkill(ctx, skill, args)
	case "http_api":
		result, execErr = executeHTTPSkill(ctx, skill, args)
	case "composite":
		result, execErr = executeCompositeSkill(ctx, skill, args)
	default:
		result = fmt.Sprintf("Unknown skill type: %s", skill.SkillType)
	}

	// Record usage
	errStr := ""
	if execErr != nil {
		errStr = execErr.Error()
		result = fmt.Sprintf("Skill '%s' error: %v", name, execErr)
	}
	go database.RecordSkillUsage(name, errStr)

	return result
}

// executeSQLSkill runs a parameterized SELECT query.
// Only SELECT statements are allowed for safety.
func executeSQLSkill(skill *models.DirectorSkill, args map[string]interface{}) (string, error) {
	query := strings.TrimSpace(skill.Code)

	// Security: proper SQL tokenization and validation
	if err := validateSQLSafety(query); err != nil {
		return "", fmt.Errorf("SQL safety check failed: %w", err)
	}

	// Build positional args from the JSON parameters
	sqlArgs := buildSQLArgs(query, args)

	db := database.GetDB()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	rows, err := db.QueryContext(ctx, query, sqlArgs...)
	if err != nil {
		return "", fmt.Errorf("SQL exec: %w", err)
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return "", fmt.Errorf("get columns: %w", err)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Query result (%s):\n", strings.Join(columns, " | ")))

	rowCount := 0
	maxRows := 50
	for rows.Next() && rowCount < maxRows {
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

	if rowCount == 0 {
		sb.WriteString("(no rows)\n")
	} else if rowCount >= maxRows {
		sb.WriteString(fmt.Sprintf("... truncated at %d rows\n", maxRows))
	}

	sb.WriteString(fmt.Sprintf("Total: %d rows\n", rowCount))
	return sb.String(), nil
}

// buildSQLArgs extracts positional parameters ($1, $2, ...) from args map.
// Maps JSON parameter names to SQL positions based on alphabetical key ordering.
func buildSQLArgs(query string, args map[string]interface{}) []interface{} {
	var sqlArgs []interface{}

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

	sortedKeys := make([]string, 0, len(args))
	for k := range args {
		sortedKeys = append(sortedKeys, k)
	}
	sort.Strings(sortedKeys)

	for i := 0; i < maxN; i++ {
		if i < len(sortedKeys) {
			sqlArgs = append(sqlArgs, args[sortedKeys[i]])
		} else {
			sqlArgs = append(sqlArgs, nil)
		}
	}

	return sqlArgs
}

// executePromptSkill runs an LLM prompt template with parameter substitution.
func executePromptSkill(ctx context.Context, skill *models.DirectorSkill, args map[string]interface{}) (string, error) {
	prompt := skill.Code

	// Replace {{param}} placeholders with actual values
	for key, val := range args {
		placeholder := "{{" + key + "}}"
		prompt = strings.ReplaceAll(prompt, placeholder, fmt.Sprintf("%v", val))
	}

	dir := getDirectorInstance()
	if dir == nil {
		return "", fmt.Errorf("director not available")
	}

	resp, err := dir.Provider().GenerateResponse(ctx, prompt, nil, &llm.GenerateOptions{
		Temperature: 0.3,
		MaxTokens:   1500,
	})
	if err != nil {
		return "", fmt.Errorf("LLM call: %w", err)
	}

	return resp.Text, nil
}

// validateHTTPURL checks that the URL is safe (no SSRF to internal services).
func validateHTTPURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	// Must be http or https
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("only http/https schemes allowed, got: %s", parsed.Scheme)
	}

	hostname := parsed.Hostname()
	if hostname == "" {
		return fmt.Errorf("empty hostname")
	}

	// Block localhost and loopback
	blockedHosts := []string{"localhost", "127.0.0.1", "0.0.0.0", "::1", "[::1]"}
	lowerHost := strings.ToLower(hostname)
	for _, blocked := range blockedHosts {
		if lowerHost == blocked {
			return fmt.Errorf("requests to %s are not allowed", hostname)
		}
	}

	// Block private/internal IP ranges
	if ip := net.ParseIP(hostname); ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			return fmt.Errorf("requests to private/internal IPs are not allowed: %s", hostname)
		}
	}

	// Block common internal hostnames
	internalSuffixes := []string{".local", ".internal", ".localhost", ".svc.cluster.local"}
	for _, suffix := range internalSuffixes {
		if strings.HasSuffix(lowerHost, suffix) {
			return fmt.Errorf("requests to internal hosts are not allowed: %s", hostname)
		}
	}

	// Block metadata endpoints (cloud providers)
	metadataIPs := []string{"169.254.169.254", "100.100.100.200", "fd00:ec2::254"}
	for _, mip := range metadataIPs {
		if hostname == mip {
			return fmt.Errorf("requests to cloud metadata endpoints are not allowed")
		}
	}

	return nil
}

// executeHTTPSkill makes an HTTP request based on the skill's JSON config.
func executeHTTPSkill(ctx context.Context, skill *models.DirectorSkill, args map[string]interface{}) (string, error) {
	var config struct {
		URL          string            `json:"url"`
		Method       string            `json:"method"`
		Headers      map[string]string `json:"headers"`
		BodyTemplate string            `json:"body_template"`
	}

	if err := json.Unmarshal([]byte(skill.Code), &config); err != nil {
		return "", fmt.Errorf("invalid HTTP config: %w", err)
	}

	if config.URL == "" {
		return "", fmt.Errorf("URL is required in HTTP config")
	}
	if config.Method == "" {
		config.Method = "GET"
	}

	// Only allow safe HTTP methods
	allowedMethods := map[string]bool{"GET": true, "POST": true, "PUT": true, "PATCH": true}
	if !allowedMethods[strings.ToUpper(config.Method)] {
		return "", fmt.Errorf("HTTP method '%s' not allowed (only GET, POST, PUT, PATCH)", config.Method)
	}

	// Substitute {{param}} in URL and body
	targetURL := config.URL
	body := config.BodyTemplate
	for key, val := range args {
		placeholder := "{{" + key + "}}"
		targetURL = strings.ReplaceAll(targetURL, placeholder, fmt.Sprintf("%v", val))
		body = strings.ReplaceAll(body, placeholder, fmt.Sprintf("%v", val))
	}

	// SSRF protection: validate URL after parameter substitution
	if err := validateHTTPURL(targetURL); err != nil {
		return "", fmt.Errorf("URL security check failed: %w", err)
	}

	var reqBody *strings.Reader
	if body != "" {
		reqBody = strings.NewReader(body)
	}

	httpCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	var req *http.Request
	var err error
	if reqBody != nil {
		req, err = http.NewRequestWithContext(httpCtx, config.Method, targetURL, reqBody)
	} else {
		req, err = http.NewRequestWithContext(httpCtx, config.Method, targetURL, nil)
	}
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}

	for k, v := range config.Headers {
		req.Header.Set(k, v)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("HTTP request: %w", err)
	}
	defer resp.Body.Close()

	var respBody strings.Builder
	buf := make([]byte, 4096)
	totalRead := 0
	for totalRead < 10000 { // limit response to 10KB
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			respBody.Write(buf[:n])
			totalRead += n
		}
		if readErr != nil {
			break
		}
	}

	return fmt.Sprintf("HTTP %d %s\n%s", resp.StatusCode, resp.Status, respBody.String()), nil
}

// executeCompositeSkill runs a pipeline of skills, feeding outputs between steps.
func executeCompositeSkill(ctx context.Context, skill *models.DirectorSkill, args map[string]interface{}) (string, error) {
	var config models.CompositeConfig
	if err := json.Unmarshal([]byte(skill.Code), &config); err != nil {
		return "", fmt.Errorf("invalid composite config: %w", err)
	}

	if len(config.Steps) == 0 {
		return "", fmt.Errorf("composite skill has no steps")
	}
	if len(config.Steps) > 5 {
		return "", fmt.Errorf("composite skill exceeds max 5 steps")
	}

	// Overall timeout for entire pipeline
	pipeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Variables store: initial params + step outputs
	vars := make(map[string]string)
	for k, v := range args {
		vars[k] = fmt.Sprintf("%v", v)
	}

	// Execute steps in order
	for i, step := range config.Steps {
		if pipeCtx.Err() != nil {
			return "", fmt.Errorf("composite pipeline timeout at step %d", i+1)
		}

		// Resolve args_map: substitute {{var}} with current variables
		stepArgs := make(map[string]interface{})
		for paramName, paramVal := range step.ArgsMap {
			resolved := paramVal
			for varName, varVal := range vars {
				resolved = strings.ReplaceAll(resolved, "{{"+varName+"}}", varVal)
			}
			stepArgs[paramName] = resolved
		}

		// Execute the sub-skill
		result := executeCustomSkill(pipeCtx, step.Skill, stepArgs)

		// Check if it was an error
		if strings.HasPrefix(result, "Skill '") && strings.Contains(result, "error:") {
			return "", fmt.Errorf("step %d (%s) failed: %s", i+1, step.Skill, result)
		}
		if strings.HasPrefix(result, "Unknown tool:") {
			return "", fmt.Errorf("step %d: skill '%s' not found", i+1, step.Skill)
		}

		// Store output
		vars[step.OutputVar] = result
	}

	// Resolve output template
	output := config.Output
	for varName, varVal := range vars {
		output = strings.ReplaceAll(output, "{{"+varName+"}}", varVal)
	}

	return output, nil
}

// validateCompositeConfig validates a composite skill config, including indirect recursion detection.
func validateCompositeConfig(selfName string, code string) error {
	var config models.CompositeConfig
	if err := json.Unmarshal([]byte(code), &config); err != nil {
		return fmt.Errorf("invalid composite config JSON: %w", err)
	}
	if len(config.Steps) == 0 {
		return fmt.Errorf("composite skill must have at least one step")
	}
	if len(config.Steps) > 5 {
		return fmt.Errorf("composite skill max 5 steps")
	}
	if config.Output == "" {
		return fmt.Errorf("composite skill must have an output variable")
	}

	seen := map[string]bool{}
	for _, step := range config.Steps {
		if step.Skill == selfName {
			return fmt.Errorf("composite skill cannot reference itself (prevents infinite loops)")
		}
		if step.OutputVar == "" {
			return fmt.Errorf("each step must have an output_var")
		}
		if seen[step.OutputVar] {
			return fmt.Errorf("duplicate output_var '%s'", step.OutputVar)
		}
		seen[step.OutputVar] = true

		// Detect indirect recursion: check if referenced skill is composite and references us
		refSkill, err := database.GetSkillByName(step.Skill)
		if err == nil && refSkill != nil && refSkill.SkillType == "composite" {
			var refConfig models.CompositeConfig
			if json.Unmarshal([]byte(refSkill.Code), &refConfig) == nil {
				for _, refStep := range refConfig.Steps {
					if refStep.Skill == selfName {
						return fmt.Errorf("indirect recursion detected: '%s' → '%s' → '%s'", selfName, step.Skill, selfName)
					}
				}
			}
		}
	}
	return nil
}

// ============================================================================
// Skill management tool implementations
// ============================================================================

func toolCreateSkill(args map[string]interface{}) string {
	name := argStr(args, "name")
	description := argStr(args, "description")
	skillType := argStr(args, "skill_type")
	code := argStr(args, "code")

	if name == "" || description == "" || skillType == "" || code == "" {
		return "Error: name, description, skill_type, and code are required."
	}

	// Validate name: lowercase, underscores, no spaces
	for _, c := range name {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_') {
			return "Error: name must be lowercase letters, digits, and underscores only."
		}
	}

	// Don't allow overriding built-in tools
	for _, t := range directorTools {
		if t.Name == name {
			return fmt.Sprintf("Error: '%s' is a built-in tool and cannot be overridden.", name)
		}
	}

	validTypes := map[string]bool{"sql_query": true, "prompt_chain": true, "http_api": true, "composite": true}
	if !validTypes[skillType] {
		return "Error: skill_type must be 'sql_query', 'prompt_chain', 'http_api', or 'composite'."
	}

	if skillType == "sql_query" {
		if err := validateSQLSafety(code); err != nil {
			return fmt.Sprintf("Error: SQL safety check failed: %v", err)
		}
	}

	if skillType == "http_api" {
		var config struct {
			URL string `json:"url"`
		}
		if err := json.Unmarshal([]byte(code), &config); err != nil {
			return fmt.Sprintf("Error: invalid HTTP config JSON: %v", err)
		}
		if config.URL != "" {
			if err := validateHTTPURL(config.URL); err != nil {
				return fmt.Sprintf("Error: URL security check failed: %v", err)
			}
		}
	}

	if skillType == "composite" {
		if err := validateCompositeConfig(name, code); err != nil {
			return fmt.Sprintf("Error: %v", err)
		}
	}

	params := `{"type":"object","properties":{}}`
	if p := argStr(args, "parameters"); p != "" {
		var test map[string]interface{}
		if err := json.Unmarshal([]byte(p), &test); err != nil {
			return fmt.Sprintf("Error: invalid parameters JSON: %v", err)
		}
		params = p
	}

	tags := argTags(args, "tags")

	skill := &models.DirectorSkill{
		ID:          uuid.New(),
		Name:        name,
		Description: description,
		Parameters:  params,
		SkillType:   skillType,
		Code:        code,
		Enabled:     true,
		CreatedBy:   "director",
		Tags:        tags,
	}

	if err := database.CreateSkill(skill); err != nil {
		return fmt.Sprintf("Error creating skill: %v", err)
	}

	InvalidateSkillsCache()
	return fmt.Sprintf("Skill '%s' created (v%d, type=%s). It's now available as a tool. Use test_skill to verify it works.", name, skill.Version, skillType)
}

func toolEditSkill(args map[string]interface{}) string {
	name := argStr(args, "name")
	if name == "" {
		return "Error: name is required."
	}

	var desc, params, code *string
	var enabled *bool

	if d := argStr(args, "description"); d != "" {
		desc = &d
	}
	if p := argStr(args, "parameters"); p != "" {
		var test map[string]interface{}
		if err := json.Unmarshal([]byte(p), &test); err != nil {
			return fmt.Sprintf("Error: invalid parameters JSON: %v", err)
		}
		params = &p
	}
	if c := argStr(args, "code"); c != "" {
		code = &c
	}
	if _, ok := args["enabled"].(bool); ok {
		e := argBool(args, "enabled", true)
		enabled = &e
	}

	if desc == nil && params == nil && code == nil && enabled == nil {
		return "Error: provide at least one field to update (description, parameters, code, or enabled)."
	}

	// If code is being updated, validate safety based on skill type
	if code != nil {
		existing, err := database.GetSkillByName(name)
		if err != nil {
			return fmt.Sprintf("Error: skill not found: %s", name)
		}
		switch existing.SkillType {
		case "sql_query":
			if err := validateSQLSafety(*code); err != nil {
				return fmt.Sprintf("Error: SQL safety check failed: %v", err)
			}
		case "composite":
			if err := validateCompositeConfig(name, *code); err != nil {
				return fmt.Sprintf("Error: %v", err)
			}
		case "http_api":
			// Validate URL in config
			var config struct {
				URL string `json:"url"`
			}
			if json.Unmarshal([]byte(*code), &config) == nil && config.URL != "" {
				if err := validateHTTPURL(config.URL); err != nil {
					return fmt.Sprintf("Error: URL security check failed: %v", err)
				}
			}
		}
	}

	if err := database.UpdateSkill(name, desc, params, code, enabled, nil); err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	InvalidateSkillsCache()
	return fmt.Sprintf("Skill '%s' updated successfully.", name)
}

func toolListSkills() string {
	skills, err := database.GetAllSkills()
	if err != nil {
		return fmt.Sprintf("Error listing skills: %v", err)
	}

	if len(skills) == 0 {
		return "No custom skills created yet. Use create_skill to make one."
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Custom skills (%d total):\n\n", len(skills)))

	for i, s := range skills {
		status := "ENABLED"
		if !s.Enabled {
			status = "DISABLED"
		}
		sb.WriteString(fmt.Sprintf("%d. %s [%s] v%d (%s)\n", i+1, s.Name, s.SkillType, s.Version, status))
		sb.WriteString(fmt.Sprintf("   Description: %s\n", s.Description))
		sb.WriteString(fmt.Sprintf("   Usage: %d calls", s.UsageCount))
		if s.LastUsedAt != nil {
			sb.WriteString(fmt.Sprintf(", last used %s", s.LastUsedAt.Format("2006-01-02 15:04")))
		}
		sb.WriteString("\n")
		if s.LastError != "" {
			sb.WriteString(fmt.Sprintf("   Last error: %s\n", s.LastError))
		}
		if len(s.Tags) > 0 {
			sb.WriteString(fmt.Sprintf("   Tags: %s\n", strings.Join(s.Tags, ", ")))
		}

		codePreview := s.Code
		if len(codePreview) > 150 {
			codePreview = codePreview[:150] + "..."
		}
		sb.WriteString(fmt.Sprintf("   Code: %s\n", codePreview))
		sb.WriteString("\n")
	}

	return sb.String()
}

func toolDeleteSkill(args map[string]interface{}) string {
	name := argStr(args, "name")
	if name == "" {
		return "Error: name is required."
	}

	if err := database.DeleteSkill(name); err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	InvalidateSkillsCache()
	return fmt.Sprintf("Skill '%s' deleted.", name)
}

func toolTestSkill(ctx context.Context, args map[string]interface{}) string {
	name := argStr(args, "name")
	if name == "" {
		return "Error: name is required."
	}

	testArgs := make(map[string]interface{})
	if ta, ok := args["test_args"].(map[string]interface{}); ok {
		testArgs = ta
	}

	start := time.Now()
	result := executeCustomSkill(ctx, name, testArgs)
	elapsed := time.Since(start)

	return fmt.Sprintf("Test result for '%s' (took %dms):\n%s", name, elapsed.Milliseconds(), result)
}
