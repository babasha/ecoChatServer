package handlers

import (
	"github.com/egor/ecochatserver/adkagent"
	"github.com/egor/ecochatserver/browser"
	"github.com/egor/ecochatserver/cronservice"
	"github.com/egor/ecochatserver/database"
	"github.com/egor/ecochatserver/llm"
)

// CronService — глобальный экземпляр сервиса расписаний (устанавливается из main.go).
var CronService *cronservice.Service

// BrowserService — глобальный экземпляр браузерного сервиса (устанавливается из main.go).
var BrowserService *browser.Service

// ============================================================================
// Tool argument helpers — extract typed values from map[string]interface{}
// ============================================================================

// argFloat extracts a float64 arg, returning def if missing or invalid.
func argFloat(args map[string]interface{}, key string, def float64) float64 {
	if v, ok := args[key].(float64); ok && v > 0 {
		return v
	}
	return def
}

// argInt extracts an int arg (JSON numbers come as float64), returning def if missing.
func argInt(args map[string]interface{}, key string, def int) int {
	if v, ok := args[key].(float64); ok && v > 0 {
		return int(v)
	}
	return def
}

// argStr extracts a string arg, returning "" if missing.
func argStr(args map[string]interface{}, key string) string {
	s, _ := args[key].(string)
	return s
}

// argBool extracts a bool arg, returning def if missing.
func argBool(args map[string]interface{}, key string, def bool) bool {
	if v, ok := args[key].(bool); ok {
		return v
	}
	return def
}

// argTags extracts a []string from a JSON array arg.
func argTags(args map[string]interface{}, key string) []string {
	tagsRaw, ok := args[key].([]interface{})
	if !ok {
		return nil
	}
	var tags []string
	for _, t := range tagsRaw {
		if s, ok := t.(string); ok {
			tags = append(tags, s)
		}
	}
	return tags
}

// ============================================================================
// AutoResponder / Director accessors — single cast point
// ============================================================================

// getAutoResponder returns the ADKAutoResponderV2 instance or nil.
func getAutoResponder() *adkagent.ADKAutoResponderV2 {
	adkAR, ok := AutoResponder.(*adkagent.ADKAutoResponderV2)
	if !ok || adkAR == nil {
		return nil
	}
	return adkAR
}

// ============================================================================
// History DB → LLM conversion
// ============================================================================

// loadHistoryFromDB loads director chat history from DB and converts to []llm.Message.
func loadHistoryFromDB(adminID string, limit int) []llm.Message {
	dbMsgs, err := database.LoadDirectorChatHistory(adminID, limit)
	if err != nil || len(dbMsgs) == 0 {
		return nil
	}
	msgs := make([]llm.Message, 0, len(dbMsgs))
	for _, m := range dbMsgs {
		msgs = append(msgs, llm.Message{Role: m.Role, Content: m.Content})
	}
	return msgs
}
