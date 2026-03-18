package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/egor/ecochatserver/database"
	"github.com/egor/ecochatserver/director"
	"github.com/egor/ecochatserver/llm"
	"github.com/egor/ecochatserver/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ============================================================================
// Director Webhook Trigger — external systems can trigger Director analysis
// ============================================================================
//
// Endpoints:
//   POST /webhook/director         — receive webhook event
//   GET  /webhook/director/verify  — verification handshake (Grafana, etc.)
//   GET  /webhook/director/events  — recent events (admin-only)
//   GET  /webhook/director/stats   — usage stats (admin-only)
//
// Auth: Bearer token OR HMAC signature (X-Hub-Signature-256)
// Token source: DB setting "DIRECTOR_WEBHOOK_TOKEN" or env var
//
// Payload:
//   {
//     "source":          "grafana",          // required
//     "event_type":      "alert_firing",     // required
//     "message":         "CPU > 90% ...",    // required
//     "metadata":        { ... },            // optional
//     "idempotency_key": "grafana-alert-42", // optional, dedup within 24h
//     "action":          "analyze",          // optional: analyze|notify|chat
//     "priority":        "high"              // optional: low|normal|high|critical
//   }
//
// Actions:
//   analyze — trigger full Director analysis (default)
//   notify  — record event + save to memory, no analysis
//   chat    — send message to Director as if admin typed it
// ============================================================================

// DirectorWebhookTrigger handles incoming webhook events from external systems.
func DirectorWebhookTrigger(c *gin.Context) {
	// --- Auth ---
	if !verifyWebhookAuth(c) {
		return
	}

	// --- Parse payload ---
	var req models.WebhookEventRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid payload",
			"details": err.Error(),
		})
		return
	}

	// Defaults
	if req.Action == "" {
		req.Action = "analyze"
	}
	if req.Priority == "" {
		req.Priority = "normal"
	}

	// Validate action
	switch req.Action {
	case "analyze", "notify", "chat":
		// ok
	default:
		c.JSON(http.StatusBadRequest, gin.H{
			"error": fmt.Sprintf("invalid action: %s (must be analyze|notify|chat)", req.Action),
		})
		return
	}

	// --- Idempotency check ---
	if req.IdempotencyKey != "" {
		exists, err := database.CheckWebhookIdempotencyKey(req.IdempotencyKey)
		if err != nil {
			log.Printf("[WEBHOOK] Idempotency check error: %v", err)
		} else if exists {
			c.JSON(http.StatusOK, gin.H{
				"status":  "duplicate",
				"message": "event already processed (idempotency_key)",
			})
			return
		}
	}

	// --- Create event record ---
	evt := &models.WebhookEvent{
		ID:             uuid.New(),
		Source:         req.Source,
		EventType:      req.EventType,
		Message:        req.Message,
		Metadata:       req.Metadata,
		IdempotencyKey: req.IdempotencyKey,
		Action:         req.Action,
		Priority:       req.Priority,
		Status:         "received",
		CreatedAt:      time.Now(),
	}

	// Log to DB (fire-and-forget)
	go func() {
		if err := database.InsertWebhookEvent(evt); err != nil {
			log.Printf("[WEBHOOK] Failed to log event: %v", err)
		}
	}()

	log.Printf("[WEBHOOK] Event received: source=%s type=%s action=%s priority=%s",
		req.Source, req.EventType, req.Action, req.Priority)

	// --- Dispatch based on action ---
	switch req.Action {
	case "analyze":
		go processWebhookAnalyze(evt)
	case "notify":
		go processWebhookNotify(evt)
	case "chat":
		go processWebhookChat(evt)
	}

	c.JSON(http.StatusAccepted, gin.H{
		"status":   "accepted",
		"event_id": evt.ID,
		"action":   evt.Action,
	})
}

// DirectorWebhookVerify handles verification handshakes from external systems.
// Supports: Grafana (no verify needed), generic token verification.
func DirectorWebhookVerify(c *gin.Context) {
	// Generic token verification (similar to Meta webhook verify)
	verifyToken := c.Query("verify_token")
	challenge := c.Query("challenge")

	expectedToken := database.GetSetting("DIRECTOR_WEBHOOK_TOKEN", "")
	if expectedToken == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "webhook not configured"})
		return
	}

	if subtle.ConstantTimeCompare([]byte(verifyToken), []byte(expectedToken)) == 1 {
		if challenge != "" {
			c.String(http.StatusOK, challenge)
		} else {
			c.JSON(http.StatusOK, gin.H{"status": "verified"})
		}
		return
	}

	c.JSON(http.StatusForbidden, gin.H{"error": "invalid verify_token"})
}

// DirectorWebhookEvents returns recent webhook events (admin-only).
func DirectorWebhookEvents(c *gin.Context) {
	limit := 20
	if l := c.Query("limit"); l != "" {
		fmt.Sscanf(l, "%d", &limit)
	}

	events, err := database.GetRecentWebhookEvents(limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"events": events,
		"count":  len(events),
	})
}

// DirectorWebhookStats returns webhook usage statistics (admin-only).
func DirectorWebhookStats(c *gin.Context) {
	stats, err := database.GetWebhookStats()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, stats)
}

// ============================================================================
// Auth helpers
// ============================================================================

// verifyWebhookAuth checks Bearer token or HMAC signature.
func verifyWebhookAuth(c *gin.Context) bool {
	token := database.GetSetting("DIRECTOR_WEBHOOK_TOKEN", "")
	if token == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": "webhook not configured — set DIRECTOR_WEBHOOK_TOKEN in settings",
		})
		return false
	}

	// Method 1: Bearer token
	authHeader := c.GetHeader("Authorization")
	if strings.HasPrefix(authHeader, "Bearer ") {
		provided := strings.TrimPrefix(authHeader, "Bearer ")
		if subtle.ConstantTimeCompare([]byte(provided), []byte(token)) == 1 {
			return true
		}
	}

	// Method 2: X-Webhook-Token header (simpler for tools like Grafana)
	if webhookToken := c.GetHeader("X-Webhook-Token"); webhookToken != "" {
		if subtle.ConstantTimeCompare([]byte(webhookToken), []byte(token)) == 1 {
			return true
		}
	}

	// Method 3: HMAC signature (X-Hub-Signature-256)
	signature := c.GetHeader("X-Hub-Signature-256")
	if signature != "" {
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read body"})
			return false
		}
		// Restore body for later binding
		c.Request.Body = io.NopCloser(strings.NewReader(string(body)))

		if verifyHMACSignature(body, signature, token) {
			return true
		}
	}

	c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid or missing authentication"})
	return false
}

// verifyHMACSignature verifies X-Hub-Signature-256 header.
func verifyHMACSignature(body []byte, signature, secret string) bool {
	// Format: sha256=<hex>
	parts := strings.SplitN(signature, "=", 2)
	if len(parts) != 2 || parts[0] != "sha256" {
		return false
	}

	expectedMAC, err := hex.DecodeString(parts[1])
	if err != nil {
		return false
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	actualMAC := mac.Sum(nil)

	return hmac.Equal(expectedMAC, actualMAC)
}

// ============================================================================
// Event processors — run async after webhook accepted
// ============================================================================

// processWebhookAnalyze triggers Director analysis with the webhook event as context.
func processWebhookAnalyze(evt *models.WebhookEvent) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	dir := getDirectorInstance()
	if dir == nil {
		log.Printf("[WEBHOOK] Director not available, skipping analysis for event %s", evt.ID)
		database.UpdateWebhookEventStatus(evt.ID, "failed", "director not initialized")
		return
	}

	database.UpdateWebhookEventStatus(evt.ID, "processing", "")

	// Record as event in tracker for threshold tracking + Sentinel for pattern detection
	tracker := dir.Tracker()
	switch evt.EventType {
	case "escalation", "escalation_alert":
		chatID := ""
		agentName := ""
		if evt.Metadata != nil {
			if cid, ok := evt.Metadata["chat_id"].(string); ok {
				chatID = cid
			}
			if an, ok := evt.Metadata["agent_name"].(string); ok {
				agentName = an
			}
		}
		tracker.RecordEscalation(chatID)
		dir.FeedSentinel(director.SentinelEvent{
			Type:      director.EventEscalation,
			ChatID:    chatID,
			AgentName: agentName,
		})
	case "empty_response":
		chatID := ""
		agentName := ""
		if evt.Metadata != nil {
			if cid, ok := evt.Metadata["chat_id"].(string); ok {
				chatID = cid
			}
			if an, ok := evt.Metadata["agent_name"].(string); ok {
				agentName = an
			}
		}
		tracker.RecordEmptyResponse(chatID)
		dir.FeedSentinel(director.SentinelEvent{
			Type:      director.EventEmptyResponse,
			ChatID:    chatID,
			AgentName: agentName,
		})
	case "tool_failure":
		chatID := ""
		toolName := "unknown"
		if evt.Metadata != nil {
			if cid, ok := evt.Metadata["chat_id"].(string); ok {
				chatID = cid
			}
			if tn, ok := evt.Metadata["tool_name"].(string); ok {
				toolName = tn
			}
		}
		tracker.RecordToolFailure(chatID, toolName)
		dir.FeedSentinel(director.SentinelEvent{
			Type:     director.EventToolFailure,
			ChatID:   chatID,
			ToolName: toolName,
		})
	}

	// Build a descriptive reason
	reason := &director.TriggerReason{
		Event:       director.EventType(evt.EventType),
		Description: fmt.Sprintf("webhook:%s/%s — %s", evt.Source, evt.EventType, truncateStr(evt.Message, 100)),
		Count:       1,
		Window:      0,
	}

	// For critical priority — force analysis regardless of cooldown
	if evt.Priority == "critical" || evt.Priority == "high" {
		log.Printf("[WEBHOOK] High-priority event, triggering immediate analysis")
		if err := dir.AnalyzeEvent(ctx, reason); err != nil {
			log.Printf("[WEBHOOK] Analysis failed for event %s: %v", evt.ID, err)
			database.UpdateWebhookEventStatus(evt.ID, "failed", err.Error())
			return
		}
	} else {
		// Normal priority — check thresholds first
		dir.CheckAndAnalyze(ctx)
	}

	// Save event to Director memory for future recall
	saveWebhookMemory(evt)

	database.UpdateWebhookEventStatus(evt.ID, "completed", "")
	log.Printf("[WEBHOOK] Event %s processed successfully (action=analyze)", evt.ID)
}

// processWebhookNotify records the event and saves to memory without triggering analysis.
func processWebhookNotify(evt *models.WebhookEvent) {
	database.UpdateWebhookEventStatus(evt.ID, "processing", "")

	saveWebhookMemory(evt)

	database.UpdateWebhookEventStatus(evt.ID, "completed", "")
	log.Printf("[WEBHOOK] Event %s processed (action=notify)", evt.ID)
}

// processWebhookChat sends the webhook message to Director as a chat message.
func processWebhookChat(evt *models.WebhookEvent) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	dir := getDirectorInstance()
	if dir == nil {
		database.UpdateWebhookEventStatus(evt.ID, "failed", "director not initialized")
		return
	}

	database.UpdateWebhookEventStatus(evt.ID, "processing", "")

	provider := dir.Provider()
	if provider == nil {
		database.UpdateWebhookEventStatus(evt.ID, "failed", "no LLM provider")
		return
	}

	// Build a chat message with context
	chatMessage := fmt.Sprintf("[WEBHOOK: %s/%s] %s", evt.Source, evt.EventType, evt.Message)
	if evt.Metadata != nil {
		metaJSON, _ := json.Marshal(evt.Metadata)
		if len(metaJSON) > 2 { // not just "{}"
			chatMessage += fmt.Sprintf("\n\nМетаданные: %s", string(metaJSON))
		}
	}

	// Use a special admin ID for webhook-originated chats
	webhookAdminID := "webhook:" + evt.Source

	// Save as a user message in director chat history
	database.SaveDirectorChatMessage(webhookAdminID, "user", chatMessage)

	// Build system prompt as first message in history
	systemPrompt := buildDirectorChatSystemPrompt()
	chatHistory := []llm.Message{
		{Role: "system", Content: systemPrompt},
	}

	resp, err := provider.GenerateResponse(ctx, chatMessage, chatHistory, &llm.GenerateOptions{
		Temperature: 0.4,
		MaxTokens:   1000,
	})
	if err != nil {
		log.Printf("[WEBHOOK] Chat LLM call failed: %v", err)
		database.UpdateWebhookEventStatus(evt.ID, "failed", err.Error())
		return
	}

	// Save response
	database.SaveDirectorChatMessage(webhookAdminID, "assistant", resp.Text)

	// Save to memory
	saveWebhookMemory(evt)

	database.UpdateWebhookEventStatus(evt.ID, "completed", "")
	log.Printf("[WEBHOOK] Event %s processed (action=chat), response length=%d", evt.ID, len(resp.Text))
}

// ============================================================================
// Helpers
// ============================================================================

// saveWebhookMemory saves a webhook event to Director's persistent memory.
func saveWebhookMemory(evt *models.WebhookEvent) {
	dateKey := evt.CreatedAt.Format("20060102-1504")
	memKey := fmt.Sprintf("webhook:%s:%s:%s", evt.Source, evt.EventType, dateKey)
	content := fmt.Sprintf("[%s] %s: %s", evt.Source, evt.EventType, truncateStr(evt.Message, 300))

	tags := []string{"webhook", "source:" + evt.Source, "type:" + evt.EventType}
	if evt.Priority == "critical" || evt.Priority == "high" {
		tags = append(tags, "priority:"+evt.Priority)
	}

	// Webhook memories expire in 14 days by default
	expires := time.Now().Add(14 * 24 * time.Hour)

	if err := database.SaveAutoMemory("fact", memKey, content, tags, &expires); err != nil {
		log.Printf("[WEBHOOK] Failed to save memory for event %s: %v", evt.ID, err)
	}
}

// truncateStr truncates a string to maxLen characters.
func truncateStr(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) > maxLen {
		return string(runes[:maxLen]) + "…"
	}
	return s
}
