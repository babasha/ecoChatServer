package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/egor/ecochatserver/database"
)

// ============================================================================
// Claude OAuth — доступ к Claude через подписку Pro/Max
// ============================================================================
//
// Использует OAuth-поток Anthropic (тот же что и Claude Code CLI).
// Вместо API ключа — OAuth token от Claude аккаунта пользователя.
// Работает с подпиской Claude Pro/Max.
//
// Отличие от OpenAI/Gemini: нет локального callback-сервера.
// Redirect URI ведёт на console.anthropic.com, который показывает код пользователю.
// Пользователь вручную копирует код (формат: code#state) и вставляет в админку.

// Claude OAuth constants (from Claude Code CLI / pi-mono)
const (
	claudeOAuthClientID     = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	claudeOAuthAuthorizeURL = "https://claude.ai/oauth/authorize"
	claudeOAuthTokenURL     = "https://console.anthropic.com/v1/oauth/token"
	claudeOAuthRedirectURI  = "https://console.anthropic.com/oauth/code/callback"
	claudeOAuthScopes       = "org:create_api_key user:profile user:inference"
)

// DB keys for Claude OAuth credentials
const (
	dbKeyClaudeOAuthAccess  = "CLAUDE_OAUTH_ACCESS_TOKEN"
	dbKeyClaudeOAuthRefresh = "CLAUDE_OAUTH_REFRESH_TOKEN"
	dbKeyClaudeOAuthExpires = "CLAUDE_OAUTH_EXPIRES"
	dbKeyClaudeOAuthEmail   = "CLAUDE_OAUTH_EMAIL"
)

// ClaudeOAuthCredentials хранит OAuth credentials для Claude
type ClaudeOAuthCredentials struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	Expires      int64  `json:"expires"` // unix ms
	Email        string `json:"email"`
}

var claudeSessions = NewOAuthSessionStore()

// ============================================================================
// OAuth Flow
// ============================================================================

// StartClaudeOAuth начинает OAuth поток, возвращает URL для авторизации
// Anthropic OAuth использует redirect на console.anthropic.com — нет callback-сервера.
// Пользователь должен скопировать код с callback-страницы и вставить его вручную.
func StartClaudeOAuth() (authURL string, state string, err error) {
	verifier, challenge, err := GeneratePKCE()
	if err != nil {
		return "", "", fmt.Errorf("generate PKCE: %w", err)
	}

	// В Anthropic OAuth state == verifier (как в pi-mono)
	stateVal := verifier

	claudeSessions.Put(stateVal, &OAuthSession{
		CodeVerifier: verifier,
		CreatedAt:    time.Now(),
	})

	params := url.Values{
		"code":                  {"true"},
		"client_id":             {claudeOAuthClientID},
		"response_type":         {"code"},
		"redirect_uri":          {claudeOAuthRedirectURI},
		"scope":                 {claudeOAuthScopes},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"state":                 {stateVal},
	}

	authURL = claudeOAuthAuthorizeURL + "?" + params.Encode()

	log.Printf("[CLAUDE_OAUTH] OAuth flow started, state=%s...", stateVal[:8])
	return authURL, stateVal, nil
}

// ProcessClaudeOAuthCode обрабатывает код авторизации от пользователя
// authCode может быть:
//   - "code#state" (формат от Anthropic callback page)
//   - просто код + state отдельно
func ProcessClaudeOAuthCode(authCode string, explicitState string) (*ClaudeOAuthCredentials, error) {
	var code, state string

	if explicitState != "" {
		code = authCode
		state = explicitState
	} else {
		parts := strings.SplitN(authCode, "#", 2)
		code = parts[0]
		if len(parts) > 1 {
			state = parts[1]
		}
	}

	if code == "" {
		return nil, fmt.Errorf("authorization code is empty")
	}
	if state == "" {
		return nil, fmt.Errorf("state is missing (expected format: code#state)")
	}

	session, exists := claudeSessions.Take(state)
	if !exists {
		return nil, fmt.Errorf("invalid or expired OAuth state")
	}

	if time.Since(session.CreatedAt) > 10*time.Minute {
		return nil, fmt.Errorf("OAuth session expired (>10 min)")
	}

	tokens, err := exchangeClaudeCodeForTokens(code, state, session.CodeVerifier)
	if err != nil {
		return nil, fmt.Errorf("token exchange: %w", err)
	}

	creds := &ClaudeOAuthCredentials{
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		Expires:      tokens.Expires,
	}

	if err := SaveClaudeOAuthCredentials(creds); err != nil {
		return nil, fmt.Errorf("save credentials: %w", err)
	}

	tokenPrefix := creds.AccessToken
	if len(tokenPrefix) > 20 {
		tokenPrefix = tokenPrefix[:20]
	}
	log.Printf("[CLAUDE_OAUTH] Successfully connected (token prefix: %s...)", tokenPrefix)
	return creds, nil
}

// ============================================================================
// Token Exchange & Refresh
// ============================================================================

type claudeTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

func exchangeClaudeCodeForTokens(code, state, codeVerifier string) (*tokenResult, error) {
	reqBody := map[string]string{
		"grant_type":    "authorization_code",
		"client_id":     claudeOAuthClientID,
		"code":          code,
		"state":         state,
		"redirect_uri":  claudeOAuthRedirectURI,
		"code_verifier": codeVerifier,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	resp, err := http.Post(claudeOAuthTokenURL, "application/json", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange failed (%d): %s", resp.StatusCode, string(body))
	}

	var tr claudeTokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return nil, fmt.Errorf("parse token response: %w", err)
	}

	if tr.AccessToken == "" || tr.RefreshToken == "" {
		return nil, fmt.Errorf("token response missing fields (access=%v, refresh=%v)",
			tr.AccessToken != "", tr.RefreshToken != "")
	}

	expires := time.Now().UnixMilli() + int64(tr.ExpiresIn)*1000 - 5*60*1000

	return &tokenResult{
		AccessToken:  tr.AccessToken,
		RefreshToken: tr.RefreshToken,
		Expires:      expires,
	}, nil
}

// RefreshClaudeOAuthToken обновляет access token
func RefreshClaudeOAuthToken(refreshToken string) (*tokenResult, error) {
	reqBody := map[string]string{
		"grant_type":    "refresh_token",
		"client_id":     claudeOAuthClientID,
		"refresh_token": refreshToken,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	resp, err := http.Post(claudeOAuthTokenURL, "application/json", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("refresh request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token refresh failed (%d): %s", resp.StatusCode, string(body))
	}

	var tr claudeTokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return nil, fmt.Errorf("parse refresh response: %w", err)
	}

	if tr.AccessToken == "" || tr.RefreshToken == "" {
		return nil, fmt.Errorf("refresh response missing fields")
	}

	expires := time.Now().UnixMilli() + int64(tr.ExpiresIn)*1000 - 5*60*1000

	return &tokenResult{
		AccessToken:  tr.AccessToken,
		RefreshToken: tr.RefreshToken,
		Expires:      expires,
	}, nil
}

// EnsureValidClaudeToken проверяет и обновляет token если нужно
func EnsureValidClaudeToken(creds *ClaudeOAuthCredentials) (*ClaudeOAuthCredentials, error) {
	if time.Now().UnixMilli() < creds.Expires {
		return creds, nil
	}

	log.Printf("[CLAUDE_OAUTH] Token expired, refreshing...")
	result, err := RefreshClaudeOAuthToken(creds.RefreshToken)
	if err != nil {
		return nil, fmt.Errorf("refresh token: %w", err)
	}

	creds.AccessToken = result.AccessToken
	creds.RefreshToken = result.RefreshToken
	creds.Expires = result.Expires

	if err := SaveClaudeOAuthCredentials(creds); err != nil {
		log.Printf("[CLAUDE_OAUTH] Warning: failed to save refreshed credentials: %v", err)
	}

	log.Printf("[CLAUDE_OAUTH] Token refreshed successfully")
	return creds, nil
}

// ============================================================================
// Credential Storage
// ============================================================================

var claudeOAuthDBKeys = []string{
	dbKeyClaudeOAuthAccess, dbKeyClaudeOAuthRefresh,
	dbKeyClaudeOAuthExpires, dbKeyClaudeOAuthEmail,
}

// SaveClaudeOAuthCredentials сохраняет OAuth credentials в БД
func SaveClaudeOAuthCredentials(creds *ClaudeOAuthCredentials) error {
	return SaveOAuthCredentials(map[string]string{
		dbKeyClaudeOAuthAccess:  creds.AccessToken,
		dbKeyClaudeOAuthRefresh: creds.RefreshToken,
		dbKeyClaudeOAuthExpires: fmt.Sprintf("%d", creds.Expires),
		dbKeyClaudeOAuthEmail:   creds.Email,
	}, "Claude OAuth")
}

// LoadClaudeOAuthCredentials загружает OAuth credentials из БД
func LoadClaudeOAuthCredentials() *ClaudeOAuthCredentials {
	access := database.GetSetting(dbKeyClaudeOAuthAccess, "")
	if access == "" {
		return nil
	}

	var expires int64
	fmt.Sscanf(database.GetSetting(dbKeyClaudeOAuthExpires, "0"), "%d", &expires)

	return &ClaudeOAuthCredentials{
		AccessToken:  access,
		RefreshToken: database.GetSetting(dbKeyClaudeOAuthRefresh, ""),
		Expires:      expires,
		Email:        database.GetSetting(dbKeyClaudeOAuthEmail, ""),
	}
}

// DeleteClaudeOAuthCredentials удаляет OAuth credentials из БД
func DeleteClaudeOAuthCredentials() error {
	DeleteOAuthCredentials(claudeOAuthDBKeys, "Claude OAuth disconnected")
	log.Printf("[CLAUDE_OAUTH] Credentials deleted")
	return nil
}

// GetClaudeOAuthStatus возвращает статус подключения
func GetClaudeOAuthStatus() map[string]interface{} {
	creds := LoadClaudeOAuthCredentials()
	if creds == nil || creds.AccessToken == "" {
		return map[string]interface{}{
			"connected": false,
		}
	}

	expired := time.Now().UnixMilli() >= creds.Expires
	return map[string]interface{}{
		"connected": true,
		"email":     creds.Email,
		"expired":   expired,
	}
}
