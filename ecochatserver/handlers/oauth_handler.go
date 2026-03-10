package handlers

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/egor/ecochatserver/llm"
	"github.com/gin-gonic/gin"
)

// ============================================================================
// Universal OAuth Handler Factory
// ============================================================================
//
// Replaces gemini_oauth_handler.go, openai_oauth_handler.go, claude_oauth_handler.go
// with a single generic implementation parameterized by OAuthProviderConfig.

// OAuthProviderConfig configures the behaviour of generated OAuth handlers.
type OAuthProviderConfig struct {
	Name            string
	StartOAuth      func() (authURL, state string, err error)
	GetStatus       func() map[string]interface{}
	ProcessCallback func(code, state string) (map[string]interface{}, error)
	Disconnect      func() error
	WaitForCallback func(ctx context.Context, timeout time.Duration) (*llm.OAuthCallbackData, error) // nil = no poll
	Messages        struct {
		Start      string
		Success    string
		Disconnect string
	}
}

// MakeStartOAuthHandler creates a GET handler to start the OAuth flow.
func MakeStartOAuthHandler(cfg OAuthProviderConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		authURL, state, err := cfg.StartOAuth()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": fmt.Sprintf("Failed to start OAuth: %v", err),
			})
			return
		}

		resp := gin.H{
			"authUrl": authURL,
			"message": cfg.Messages.Start,
		}
		if state != "" {
			resp["state"] = state
		}
		c.JSON(http.StatusOK, resp)
	}
}

// MakeOAuthStatusHandler creates a GET handler to check connection status.
func MakeOAuthStatusHandler(cfg OAuthProviderConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		status := cfg.GetStatus()
		c.JSON(http.StatusOK, status)
	}
}

// MakeOAuthManualCallbackHandler creates a POST handler for manual redirect URL / code input.
func MakeOAuthManualCallbackHandler(cfg OAuthProviderConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request struct {
			RedirectURL string `json:"redirectUrl"`
			Code        string `json:"code"`
			State       string `json:"state"`
		}

		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
			return
		}

		code := request.Code
		state := request.State

		if request.RedirectURL != "" {
			parsed, err := url.Parse(request.RedirectURL)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid redirect URL"})
				return
			}
			code = parsed.Query().Get("code")
			state = parsed.Query().Get("state")

			if errParam := parsed.Query().Get("error"); errParam != "" {
				c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("%s OAuth error: %s", cfg.Name, errParam)})
				return
			}
		}

		if code == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Missing code parameter"})
			return
		}

		// For providers with callback servers (Gemini, OpenAI), state is required.
		// For Claude, state can be embedded in code as "code#state" — ProcessCallback handles parsing.
		if state == "" && cfg.WaitForCallback != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Missing state parameter"})
			return
		}

		result, err := cfg.ProcessCallback(code, state)
		if err != nil {
			log.Printf("[%s_OAUTH] Manual callback error: %v", strings.ToUpper(cfg.Name), err)
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": fmt.Sprintf("OAuth processing failed: %v", err),
			})
			return
		}

		resp := gin.H{
			"success": true,
			"message": cfg.Messages.Success,
		}
		for k, v := range result {
			resp[k] = v
		}
		c.JSON(http.StatusOK, resp)
	}
}

// MakeOAuthPollHandler creates a POST handler that waits for an automatic callback.
func MakeOAuthPollHandler(cfg OAuthProviderConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cfg.WaitForCallback == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "Poll not supported for this provider"})
			return
		}

		ctx := c.Request.Context()
		data, err := cfg.WaitForCallback(ctx, 3*time.Minute)
		if err != nil {
			if strings.Contains(err.Error(), "timeout") {
				c.JSON(http.StatusOK, gin.H{"status": "waiting"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		if data.Error != "" {
			c.JSON(http.StatusOK, gin.H{"status": "error", "error": data.Error})
			return
		}

		result, err := cfg.ProcessCallback(data.Code, data.State)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"status": "error", "error": err.Error()})
			return
		}

		resp := gin.H{"status": "connected"}
		for k, v := range result {
			resp[k] = v
		}
		c.JSON(http.StatusOK, resp)
	}
}

// MakeOAuthDisconnectHandler creates a POST handler to disconnect the OAuth account.
func MakeOAuthDisconnectHandler(cfg OAuthProviderConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := cfg.Disconnect(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": cfg.Messages.Disconnect,
		})
	}
}

// ============================================================================
// Provider configs
// ============================================================================

// NewGeminiOAuthConfig returns the OAuthProviderConfig for Gemini.
func NewGeminiOAuthConfig() OAuthProviderConfig {
	return OAuthProviderConfig{
		Name:      "Google",
		StartOAuth: func() (string, string, error) { return llm.StartGeminiOAuth() },
		GetStatus:  llm.GetGeminiOAuthStatus,
		ProcessCallback: func(code, state string) (map[string]interface{}, error) {
			creds, err := llm.ProcessOAuthCallback(code, state)
			if err != nil {
				return nil, err
			}
			return map[string]interface{}{
				"email":     creds.Email,
				"projectId": creds.ProjectID,
			}, nil
		},
		Disconnect:      llm.DeleteGeminiOAuthCredentials,
		WaitForCallback: llm.WaitForCallback,
		Messages: struct {
			Start      string
			Success    string
			Disconnect string
		}{
			Start:      "Open authUrl in browser. After authorization, the callback will be handled automatically. If it fails, copy the redirect URL and paste it via /api/llm/gemini-oauth/manual endpoint.",
			Success:    "Google Gemini connected successfully!",
			Disconnect: "Google Gemini disconnected",
		},
	}
}

// NewOpenAIOAuthConfig returns the OAuthProviderConfig for OpenAI.
func NewOpenAIOAuthConfig() OAuthProviderConfig {
	return OAuthProviderConfig{
		Name:      "OpenAI",
		StartOAuth: func() (string, string, error) { return llm.StartOpenAIOAuth() },
		GetStatus:  llm.GetOpenAIOAuthStatus,
		ProcessCallback: func(code, state string) (map[string]interface{}, error) {
			creds, err := llm.ProcessOpenAIOAuthCallback(code, state)
			if err != nil {
				return nil, err
			}
			return map[string]interface{}{
				"email":     creds.Email,
				"accountId": creds.AccountID,
			}, nil
		},
		Disconnect:      llm.DeleteOpenAIOAuthCredentials,
		WaitForCallback: llm.WaitForOpenAICallback,
		Messages: struct {
			Start      string
			Success    string
			Disconnect string
		}{
			Start:      "Open authUrl in browser. After authorization, the callback will be handled automatically. If it fails, copy the redirect URL and paste it via /api/llm/openai-oauth/manual endpoint.",
			Success:    "ChatGPT connected successfully!",
			Disconnect: "ChatGPT disconnected",
		},
	}
}

// NewClaudeOAuthConfig returns the OAuthProviderConfig for Claude.
func NewClaudeOAuthConfig() OAuthProviderConfig {
	return OAuthProviderConfig{
		Name: "Claude",
		StartOAuth: func() (string, string, error) {
			authURL, _, err := llm.StartClaudeOAuth()
			return authURL, "", err // Claude does not expose state to frontend
		},
		GetStatus: llm.GetClaudeOAuthStatus,
		ProcessCallback: func(code, state string) (map[string]interface{}, error) {
			creds, err := llm.ProcessClaudeOAuthCode(code, state)
			if err != nil {
				return nil, err
			}
			return map[string]interface{}{
				"email": creds.Email,
			}, nil
		},
		Disconnect:      llm.DeleteClaudeOAuthCredentials,
		WaitForCallback: nil, // Claude has no poll endpoint
		Messages: struct {
			Start      string
			Success    string
			Disconnect string
		}{
			Start:      "Open authUrl in browser. After authorization, Anthropic will show you a code. Copy the code and paste it using the /api/llm/claude-oauth/callback endpoint.",
			Success:    "Claude connected successfully!",
			Disconnect: "Claude disconnected",
		},
	}
}
