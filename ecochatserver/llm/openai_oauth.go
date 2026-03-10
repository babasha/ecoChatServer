package llm

import (
	"context"
	"encoding/base64"
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
// OpenAI Codex OAuth — бесплатный доступ к ChatGPT через подписку Plus/Pro
// ============================================================================
//
// Использует OAuth-поток OpenAI Codex (тот же что и Codex CLI).
// Вместо API ключа — OAuth token от ChatGPT аккаунта пользователя.
// Работает с подпиской ChatGPT Plus/Pro.

// OpenAI OAuth constants (from Codex CLI)
const (
	openaiOAuthClientID     = "app_EMoamEEZ73f0CkXaXp7hrann"
	openaiOAuthAuthorizeURL = "https://auth.openai.com/oauth/authorize"
	openaiOAuthTokenURL     = "https://auth.openai.com/oauth/token"
	openaiOAuthRedirectURI  = "http://localhost:1455/auth/callback"
	openaiOAuthCallbackPort = 1455
	openaiOAuthScopes       = "openid profile email offline_access"
	openaiCodexBaseURL      = "https://chatgpt.com/backend-api"
	openaiJWTClaimPath      = "https://api.openai.com/auth"
)

// DB keys for OpenAI OAuth credentials
const (
	dbKeyOpenAIOAuthAccess    = "OPENAI_OAUTH_ACCESS_TOKEN"
	dbKeyOpenAIOAuthRefresh   = "OPENAI_OAUTH_REFRESH_TOKEN"
	dbKeyOpenAIOAuthExpires   = "OPENAI_OAUTH_EXPIRES"
	dbKeyOpenAIOAuthAccountID = "OPENAI_OAUTH_ACCOUNT_ID"
	dbKeyOpenAIOAuthEmail     = "OPENAI_OAUTH_EMAIL"
)

// OpenAIOAuthCredentials хранит OAuth credentials для ChatGPT
type OpenAIOAuthCredentials struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	Expires      int64  `json:"expires"` // unix ms
	AccountID    string `json:"accountId"`
	Email        string `json:"email"`
}

var (
	openaiSessions  = NewOAuthSessionStore()
	openaiCallbacks = NewOAuthCallbackStore()
)

// ============================================================================
// OAuth Flow
// ============================================================================

// StartOpenAIOAuth начинает OAuth поток, возвращает URL для авторизации
func StartOpenAIOAuth() (authURL string, state string, err error) {
	verifier, challenge, err := GeneratePKCE()
	if err != nil {
		return "", "", fmt.Errorf("generate PKCE: %w", err)
	}

	stateVal, err := GenerateOAuthState("hex")
	if err != nil {
		return "", "", fmt.Errorf("generate state: %w", err)
	}

	openaiSessions.Put(stateVal, &OAuthSession{
		State:        stateVal,
		CodeVerifier: verifier,
		CreatedAt:    time.Now(),
	})

	params := url.Values{
		"response_type":              {"code"},
		"client_id":                  {openaiOAuthClientID},
		"redirect_uri":              {openaiOAuthRedirectURI},
		"scope":                     {openaiOAuthScopes},
		"code_challenge":            {challenge},
		"code_challenge_method":     {"S256"},
		"state":                     {stateVal},
		"id_token_add_organizations": {"true"},
		"codex_cli_simplified_flow": {"true"},
		"originator":                {"ecochat"},
	}

	authURL = openaiOAuthAuthorizeURL + "?" + params.Encode()

	go StartOAuthCallbackServer(stateVal, "/auth/callback", openaiOAuthCallbackPort, "OPENAI_OAUTH", openaiCallbacks)

	log.Printf("[OPENAI_OAUTH] OAuth flow started, state=%s", stateVal[:8]+"...")
	return authURL, stateVal, nil
}

// WaitForOpenAICallback ждёт callback от OpenAI
func WaitForOpenAICallback(ctx context.Context, timeout time.Duration) (*OAuthCallbackData, error) {
	return openaiCallbacks.WaitForAny(ctx, timeout)
}

// ProcessOpenAIOAuthCallback обрабатывает код авторизации
func ProcessOpenAIOAuthCallback(code, state string) (*OpenAIOAuthCredentials, error) {
	session, exists := openaiSessions.Take(state)
	if !exists {
		return nil, fmt.Errorf("invalid or expired OAuth state")
	}

	if time.Since(session.CreatedAt) > 10*time.Minute {
		return nil, fmt.Errorf("OAuth session expired")
	}

	tokens, err := exchangeOpenAICodeForTokens(code, session.CodeVerifier)
	if err != nil {
		return nil, fmt.Errorf("token exchange: %w", err)
	}

	accountID, err := extractAccountIDFromJWT(tokens.AccessToken)
	if err != nil {
		return nil, fmt.Errorf("extract account ID: %w", err)
	}

	email := extractEmailFromJWT(tokens.AccessToken)

	creds := &OpenAIOAuthCredentials{
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		Expires:      tokens.Expires,
		AccountID:    accountID,
		Email:        email,
	}

	if err := SaveOpenAIOAuthCredentials(creds); err != nil {
		return nil, fmt.Errorf("save credentials: %w", err)
	}

	log.Printf("[OPENAI_OAUTH] Successfully connected: email=%s, accountId=%s", email, accountID[:8]+"...")
	return creds, nil
}

// ============================================================================
// Token Exchange & Refresh
// ============================================================================

func exchangeOpenAICodeForTokens(code, codeVerifier string) (*tokenResult, error) {
	data := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {openaiOAuthClientID},
		"code":          {code},
		"code_verifier": {codeVerifier},
		"redirect_uri":  {openaiOAuthRedirectURI},
	}

	resp, err := http.PostForm(openaiOAuthTokenURL, data)
	if err != nil {
		return nil, fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange failed (%d): %s", resp.StatusCode, string(body))
	}

	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return nil, fmt.Errorf("parse token response: %w", err)
	}

	if tr.AccessToken == "" || tr.RefreshToken == "" {
		return nil, fmt.Errorf("token response missing fields")
	}

	expires := time.Now().UnixMilli() + int64(tr.ExpiresIn)*1000

	return &tokenResult{
		AccessToken:  tr.AccessToken,
		RefreshToken: tr.RefreshToken,
		Expires:      expires,
	}, nil
}

// RefreshOpenAIOAuthToken обновляет access token
func RefreshOpenAIOAuthToken(refreshToken string) (*tokenResult, error) {
	data := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {openaiOAuthClientID},
	}

	resp, err := http.PostForm(openaiOAuthTokenURL, data)
	if err != nil {
		return nil, fmt.Errorf("refresh request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token refresh failed (%d): %s", resp.StatusCode, string(body))
	}

	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return nil, fmt.Errorf("parse refresh response: %w", err)
	}

	if tr.AccessToken == "" || tr.RefreshToken == "" {
		return nil, fmt.Errorf("refresh response missing fields")
	}

	expires := time.Now().UnixMilli() + int64(tr.ExpiresIn)*1000

	return &tokenResult{
		AccessToken:  tr.AccessToken,
		RefreshToken: tr.RefreshToken,
		Expires:      expires,
	}, nil
}

// EnsureValidOpenAIToken проверяет и обновляет token если нужно
func EnsureValidOpenAIToken(creds *OpenAIOAuthCredentials) (*OpenAIOAuthCredentials, error) {
	if time.Now().UnixMilli() < creds.Expires-300000 {
		return creds, nil
	}

	log.Printf("[OPENAI_OAUTH] Token expired, refreshing...")
	result, err := RefreshOpenAIOAuthToken(creds.RefreshToken)
	if err != nil {
		return nil, fmt.Errorf("refresh token: %w", err)
	}

	creds.AccessToken = result.AccessToken
	creds.RefreshToken = result.RefreshToken
	creds.Expires = result.Expires

	if accountID, err := extractAccountIDFromJWT(result.AccessToken); err == nil {
		creds.AccountID = accountID
	}

	if err := SaveOpenAIOAuthCredentials(creds); err != nil {
		log.Printf("[OPENAI_OAUTH] Warning: failed to save refreshed credentials: %v", err)
	}

	log.Printf("[OPENAI_OAUTH] Token refreshed successfully")
	return creds, nil
}

// ============================================================================
// JWT Parsing — извлечение accountId и email из access token
// ============================================================================

func extractAccountIDFromJWT(token string) (string, error) {
	payload, err := decodeJWTPayload(token)
	if err != nil {
		return "", err
	}

	authClaim, ok := payload[openaiJWTClaimPath]
	if !ok {
		return "", fmt.Errorf("JWT missing %s claim", openaiJWTClaimPath)
	}

	authMap, ok := authClaim.(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("JWT %s claim is not an object", openaiJWTClaimPath)
	}

	accountID, ok := authMap["chatgpt_account_id"].(string)
	if !ok || accountID == "" {
		return "", fmt.Errorf("JWT missing chatgpt_account_id")
	}

	return accountID, nil
}

func extractEmailFromJWT(token string) string {
	payload, err := decodeJWTPayload(token)
	if err != nil {
		return ""
	}

	if email, ok := payload["email"].(string); ok {
		return email
	}
	return ""
}

func decodeJWTPayload(token string) (map[string]interface{}, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid JWT: expected 3 parts, got %d", len(parts))
	}

	payloadB64 := parts[1]
	switch len(payloadB64) % 4 {
	case 2:
		payloadB64 += "=="
	case 3:
		payloadB64 += "="
	}

	decoded, err := base64.URLEncoding.DecodeString(payloadB64)
	if err != nil {
		decoded, err = base64.RawURLEncoding.DecodeString(parts[1])
		if err != nil {
			return nil, fmt.Errorf("decode JWT payload: %w", err)
		}
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(decoded, &payload); err != nil {
		return nil, fmt.Errorf("parse JWT payload: %w", err)
	}

	return payload, nil
}

// ============================================================================
// Credential Storage
// ============================================================================

var openaiOAuthDBKeys = []string{
	dbKeyOpenAIOAuthAccess, dbKeyOpenAIOAuthRefresh,
	dbKeyOpenAIOAuthExpires, dbKeyOpenAIOAuthAccountID, dbKeyOpenAIOAuthEmail,
}

// SaveOpenAIOAuthCredentials сохраняет OAuth credentials в БД
func SaveOpenAIOAuthCredentials(creds *OpenAIOAuthCredentials) error {
	return SaveOAuthCredentials(map[string]string{
		dbKeyOpenAIOAuthAccess:    creds.AccessToken,
		dbKeyOpenAIOAuthRefresh:   creds.RefreshToken,
		dbKeyOpenAIOAuthExpires:   fmt.Sprintf("%d", creds.Expires),
		dbKeyOpenAIOAuthAccountID: creds.AccountID,
		dbKeyOpenAIOAuthEmail:     creds.Email,
	}, "OpenAI OAuth")
}

// LoadOpenAIOAuthCredentials загружает OAuth credentials из БД
func LoadOpenAIOAuthCredentials() *OpenAIOAuthCredentials {
	access := database.GetSetting(dbKeyOpenAIOAuthAccess, "")
	if access == "" {
		return nil
	}

	var expires int64
	fmt.Sscanf(database.GetSetting(dbKeyOpenAIOAuthExpires, "0"), "%d", &expires)

	return &OpenAIOAuthCredentials{
		AccessToken:  access,
		RefreshToken: database.GetSetting(dbKeyOpenAIOAuthRefresh, ""),
		Expires:      expires,
		AccountID:    database.GetSetting(dbKeyOpenAIOAuthAccountID, ""),
		Email:        database.GetSetting(dbKeyOpenAIOAuthEmail, ""),
	}
}

// DeleteOpenAIOAuthCredentials удаляет OAuth credentials из БД
func DeleteOpenAIOAuthCredentials() error {
	DeleteOAuthCredentials(openaiOAuthDBKeys, "OpenAI OAuth disconnected")
	log.Printf("[OPENAI_OAUTH] Credentials deleted")
	return nil
}

// GetOpenAIOAuthStatus возвращает статус подключения
func GetOpenAIOAuthStatus() map[string]interface{} {
	creds := LoadOpenAIOAuthCredentials()
	if creds == nil || creds.AccessToken == "" {
		return map[string]interface{}{
			"connected": false,
		}
	}

	expired := time.Now().UnixMilli() >= creds.Expires
	return map[string]interface{}{
		"connected": true,
		"email":     creds.Email,
		"accountId": creds.AccountID,
		"expired":   expired,
	}
}
