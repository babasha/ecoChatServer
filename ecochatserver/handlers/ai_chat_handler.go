package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"github.com/egor/ecochatserver/llm"
	"github.com/gin-gonic/gin"
)

// textToolCallRe matches "[tool_call: name({...})]" patterns
// that models sometimes output as text instead of using the function calling API.
// Uses (?s) flag so . matches newlines (model sometimes formats JSON with line breaks).
var textToolCallRe = regexp.MustCompile(`(?s)\[tool_call:\s*(\w+)\((\{.*?\})\)\]`)

// parseTextToolCall attempts to extract a function call from plain text output.
// Returns nil if no tool call pattern is found.
func parseTextToolCall(text string) *llm.FunctionCall {
	matches := textToolCallRe.FindStringSubmatch(text)
	if len(matches) < 3 {
		return nil
	}

	name := matches[1]
	argsStr := matches[2]

	var args map[string]interface{}
	if err := json.Unmarshal([]byte(argsStr), &args); err != nil {
		log.Printf("[AI_CHAT] parseTextToolCall: failed to parse args for %s: %v (raw: %s)", name, err, argsStr)
		args = make(map[string]interface{})
	}

	return &llm.FunctionCall{
		Name:      name,
		Arguments: args,
	}
}

// aiChatHistory stores per-admin, per-role conversation history (in-memory, resets on restart)
var (
	aiChatHistory   = map[string][]llm.Message{} // "adminID:ROLE" → messages
	aiChatHistoryMu sync.RWMutex
)

const maxAIChatHistory = 30 // keep last 30 messages per admin per role

// validChatRoles defines allowed chat roles
var validChatRoles = map[string]bool{
	"RESPONDER":  true,
	"TRANSLATOR": true,
	"GLOBAL":     true,
}

// AIChatMessage handles a chat message to a role-specific AI
func AIChatMessage(c *gin.Context) {
	var request struct {
		Role    string `json:"role"`
		Message string `json:"message"`
	}
	if err := c.ShouldBindJSON(&request); err != nil || request.Message == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "message is required"})
		return
	}

	if request.Role == "" {
		request.Role = "GLOBAL"
	}
	request.Role = strings.ToUpper(request.Role)

	if !validChatRoles[request.Role] {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid role: %s", request.Role)})
		return
	}

	adminID := "unknown"
	if id, exists := c.Get("adminId"); exists {
		adminID = id.(string)
	}

	// Get provider for role
	var provider llm.Provider
	var err error

	if request.Role == "GLOBAL" {
		provider = llm.GetGlobalProvider()
		if provider == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "global LLM provider not configured"})
			return
		}
	} else {
		provider, err = llm.NewProviderForRole(llm.ProviderRole(request.Role))
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": fmt.Sprintf("provider for %s not available: %v", request.Role, err)})
			return
		}
	}

	// Get chat history for this admin + role
	historyKey := fmt.Sprintf("%s:%s", adminID, request.Role)

	aiChatHistoryMu.Lock()
	history := make([]llm.Message, len(aiChatHistory[historyKey]))
	copy(history, aiChatHistory[historyKey])
	aiChatHistoryMu.Unlock()

	// Build system prompt based on role
	systemPrompt := buildAIChatSystemPrompt(request.Role)
	opts := &llm.GenerateOptions{
		Temperature:  0.4,
		MaxTokens:    2000,
		SystemPrompt: systemPrompt,
	}

	resp, err := provider.GenerateResponse(c.Request.Context(), request.Message, history, opts)
	if err != nil {
		log.Printf("[AI_CHAT] %s LLM error: %v", request.Role, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "LLM error: " + err.Error()})
		return
	}

	// Save both messages to history (in-memory)
	aiChatHistoryMu.Lock()
	h := aiChatHistory[historyKey]
	h = append(h, llm.Message{Role: "user", Content: request.Message})
	h = append(h, llm.Message{Role: "assistant", Content: resp.Text})
	if len(h) > maxAIChatHistory {
		h = h[len(h)-maxAIChatHistory:]
	}
	aiChatHistory[historyKey] = h
	aiChatHistoryMu.Unlock()

	idPreview := adminID
	if len(idPreview) > 8 {
		idPreview = idPreview[:8]
	}
	log.Printf("[AI_CHAT] %s admin=%s: %d chars -> %d chars (provider=%s)",
		request.Role, idPreview, len(request.Message), len(resp.Text), provider.GetName())

	c.JSON(http.StatusOK, gin.H{
		"message":  resp.Text,
		"usage":    resp.Usage,
		"provider": provider.GetName(),
	})
}

// AIChatHistory returns conversation history for a specific role
func AIChatHistory(c *gin.Context) {
	adminID := "unknown"
	if id, exists := c.Get("adminId"); exists {
		adminID = id.(string)
	}

	role := strings.ToUpper(c.Query("role"))
	if role == "" {
		role = "GLOBAL"
	}

	historyKey := fmt.Sprintf("%s:%s", adminID, role)

	aiChatHistoryMu.Lock()
	history := aiChatHistory[historyKey]
	aiChatHistoryMu.Unlock()

	c.JSON(http.StatusOK, gin.H{
		"messages": history,
		"role":     role,
	})
}

// AIChatClear clears conversation history for a specific role (or all roles)
func AIChatClear(c *gin.Context) {
	adminID := "unknown"
	if id, exists := c.Get("adminId"); exists {
		adminID = id.(string)
	}

	role := strings.ToUpper(c.Query("role"))

	if role == "" {
		// Clear all roles for this admin
		prefix := adminID + ":"
		aiChatHistoryMu.Lock()
		for key := range aiChatHistory {
			if strings.HasPrefix(key, prefix) {
				delete(aiChatHistory, key)
			}
		}
		aiChatHistoryMu.Unlock()
	} else {
		historyKey := fmt.Sprintf("%s:%s", adminID, role)
		aiChatHistoryMu.Lock()
		delete(aiChatHistory, historyKey)
		aiChatHistoryMu.Unlock()
	}

	c.JSON(http.StatusOK, gin.H{"status": "cleared"})
}

// AIChatRoles returns available roles and their status
func AIChatRoles(c *gin.Context) {
	roles := []gin.H{}

	for roleName := range validChatRoles {
		roleInfo := gin.H{
			"role":      roleName,
			"available": false,
			"provider":  "",
		}

		if roleName == "GLOBAL" {
			p := llm.GetGlobalProvider()
			if p != nil {
				roleInfo["available"] = true
				roleInfo["provider"] = p.GetName()
			}
		} else {
			p, err := llm.NewProviderForRole(llm.ProviderRole(roleName))
			if err == nil && p != nil {
				roleInfo["available"] = true
				roleInfo["provider"] = p.GetName()
			}
		}

		roles = append(roles, roleInfo)
	}

	c.JSON(http.StatusOK, gin.H{"roles": roles})
}

func buildAIChatSystemPrompt(role string) string {
	switch role {
	case "RESPONDER":
		return `You are the Auto-Responder (РОП/Level 1) AI agent for Zefir IoT customer support.
You are having a conversation with a human admin who manages the support system.

Your role:
- You handle first-line customer support interactions
- You can answer questions about products (Zefir devices, plants, EcoSystem)
- You escalate complex issues to human operators
- You use tools like knowledge base search, device diagnostics, etc.

The admin is testing your behavior or asking about how you handle specific scenarios.
Respond as if you were handling a real customer inquiry when given example messages,
or explain your approach when asked about your behavior.

Be helpful, specific, and demonstrate your capabilities. Use the same language as the admin.`

	case "TRANSLATOR":
		return `You are the Translation AI agent for Zefir IoT customer support system.
You are having a conversation with a human admin who manages the support system.

Your role:
- You translate customer messages between languages (Russian, English, Hebrew, Arabic, etc.)
- You detect source language automatically
- You maintain meaning, tone, and context in translations

When the admin sends you text, translate it to the other common language.
If they ask about your capabilities, explain what you can do.
Use the same language as the admin for explanations.`

	default: // GLOBAL
		return `You are an AI assistant in the EcoChat admin panel for Zefir IoT.
You can help with general questions, analysis, coding, and tasks.
Use the same language as the admin.`
	}
}
