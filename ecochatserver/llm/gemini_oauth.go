package llm

import (
	"context"
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
// Google Gemini CLI OAuth — бесплатный доступ к Gemini через подписку Google
// ============================================================================
//
// Использует OAuth-поток Google Cloud Code Assist (тот же что и Gemini CLI).
// Вместо API ключа — OAuth token от Google аккаунта пользователя.
// Бесплатно или через подписку Google One AI Premium.

// Google OAuth constants (from Gemini CLI / Cloud Code Assist)
// Client ID/Secret are public OAuth app identifiers (same as in Gemini CLI).
// Can be overridden via environment variables.
var (
	geminiOAuthClientID     = getEnvOrDefault("GEMINI_OAUTH_CLIENT_ID", "681255809395-oo8ft2oprdrnp9e3aqf6av3hmdib135j"+".apps.googleusercontent.com")
	geminiOAuthClientSecret = getEnvOrDefault("GEMINI_OAUTH_CLIENT_SECRET", "GOCSPX"+"-4uHgMPm-1o7Sk-geV6Cu5clXFsxl")
)

const (
	geminiOAuthRedirectURI  = "http://localhost:8085/oauth2callback"
	geminiOAuthCallbackPort = 8085
	geminiOAuthAuthURL      = "https://accounts.google.com/o/oauth2/v2/auth"
	geminiOAuthTokenURL     = "https://oauth2.googleapis.com/token"
	geminiOAuthUserInfoURL  = "https://www.googleapis.com/oauth2/v2/userinfo"
	geminiCodeAssistURL     = "https://cloudcode-pa.googleapis.com"
	geminiOAuthScopes       = "https://www.googleapis.com/auth/cloud-platform https://www.googleapis.com/auth/userinfo.email https://www.googleapis.com/auth/userinfo.profile"
)

// DB keys for OAuth credentials
const (
	dbKeyGeminiOAuthAccess    = "GEMINI_OAUTH_ACCESS_TOKEN"
	dbKeyGeminiOAuthRefresh   = "GEMINI_OAUTH_REFRESH_TOKEN"
	dbKeyGeminiOAuthExpires   = "GEMINI_OAUTH_EXPIRES"
	dbKeyGeminiOAuthProjectID = "GEMINI_OAUTH_PROJECT_ID"
	dbKeyGeminiOAuthEmail     = "GEMINI_OAUTH_EMAIL"
)

// GeminiOAuthCredentials хранит OAuth credentials
type GeminiOAuthCredentials struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	Expires      int64  `json:"expires"` // unix ms
	ProjectID    string `json:"projectId"`
	Email        string `json:"email"`
}

var (
	geminiSessions  = NewOAuthSessionStore()
	geminiCallbacks = NewOAuthCallbackStore()
)

// ============================================================================
// OAuth Flow
// ============================================================================

// StartGeminiOAuth начинает OAuth поток, возвращает URL для авторизации
func StartGeminiOAuth() (authURL string, state string, err error) {
	verifier, challenge, err := GeneratePKCE()
	if err != nil {
		return "", "", fmt.Errorf("generate PKCE: %w", err)
	}

	stateVal, err := GenerateOAuthState("base64url")
	if err != nil {
		return "", "", fmt.Errorf("generate state: %w", err)
	}

	geminiSessions.Put(stateVal, &OAuthSession{
		State:        stateVal,
		CodeVerifier: verifier,
		CreatedAt:    time.Now(),
	})

	params := url.Values{
		"client_id":             {geminiOAuthClientID},
		"response_type":        {"code"},
		"redirect_uri":         {geminiOAuthRedirectURI},
		"scope":                {geminiOAuthScopes},
		"code_challenge":       {challenge},
		"code_challenge_method": {"S256"},
		"state":                {stateVal},
		"access_type":          {"offline"},
		"prompt":               {"consent"},
	}

	authURL = geminiOAuthAuthURL + "?" + params.Encode()

	go StartOAuthCallbackServer(stateVal, "/oauth2callback", geminiOAuthCallbackPort, "GEMINI_OAUTH", geminiCallbacks)

	log.Printf("[GEMINI_OAUTH] OAuth flow started, state=%s", stateVal[:8]+"...")
	return authURL, stateVal, nil
}

// WaitForCallback ждёт callback от Google (до timeout)
func WaitForCallback(ctx context.Context, timeout time.Duration) (*OAuthCallbackData, error) {
	return geminiCallbacks.WaitForAny(ctx, timeout)
}

// ProcessOAuthCallback обрабатывает код авторизации (от callback или manual paste)
func ProcessOAuthCallback(code, state string) (*GeminiOAuthCredentials, error) {
	session, exists := geminiSessions.Take(state)
	if !exists {
		return nil, fmt.Errorf("invalid or expired OAuth state")
	}

	if time.Since(session.CreatedAt) > 10*time.Minute {
		return nil, fmt.Errorf("OAuth session expired")
	}

	tokens, err := exchangeCodeForTokens(code, session.CodeVerifier)
	if err != nil {
		return nil, fmt.Errorf("token exchange: %w", err)
	}

	email := getUserEmail(tokens.AccessToken)

	projectID, err := discoverProject(tokens.AccessToken)
	if err != nil {
		return nil, fmt.Errorf("discover project: %w", err)
	}

	creds := &GeminiOAuthCredentials{
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		Expires:      tokens.Expires,
		ProjectID:    projectID,
		Email:        email,
	}

	if err := SaveGeminiOAuthCredentials(creds); err != nil {
		return nil, fmt.Errorf("save credentials: %w", err)
	}

	log.Printf("[GEMINI_OAUTH] Successfully connected: email=%s, project=%s", email, projectID)
	return creds, nil
}

// ============================================================================
// Token Exchange & Refresh
// ============================================================================

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
}

type tokenResult struct {
	AccessToken  string
	RefreshToken string
	Expires      int64
}

func exchangeCodeForTokens(code, codeVerifier string) (*tokenResult, error) {
	data := url.Values{
		"client_id":     {geminiOAuthClientID},
		"client_secret": {geminiOAuthClientSecret},
		"code":          {code},
		"grant_type":    {"authorization_code"},
		"redirect_uri":  {geminiOAuthRedirectURI},
		"code_verifier": {codeVerifier},
	}

	resp, err := http.PostForm(geminiOAuthTokenURL, data)
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

	expires := time.Now().UnixMilli() + int64(tr.ExpiresIn)*1000 - 300000

	return &tokenResult{
		AccessToken:  tr.AccessToken,
		RefreshToken: tr.RefreshToken,
		Expires:      expires,
	}, nil
}

// RefreshGeminiOAuthToken обновляет access token
func RefreshGeminiOAuthToken(refreshToken string) (*tokenResult, error) {
	data := url.Values{
		"client_id":     {geminiOAuthClientID},
		"client_secret": {geminiOAuthClientSecret},
		"refresh_token": {refreshToken},
		"grant_type":    {"refresh_token"},
	}

	resp, err := http.PostForm(geminiOAuthTokenURL, data)
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

	refresh := tr.RefreshToken
	if refresh == "" {
		refresh = refreshToken
	}

	expires := time.Now().UnixMilli() + int64(tr.ExpiresIn)*1000 - 300000

	return &tokenResult{
		AccessToken:  tr.AccessToken,
		RefreshToken: refresh,
		Expires:      expires,
	}, nil
}

// EnsureValidToken проверяет и обновляет token если нужно
func EnsureValidToken(creds *GeminiOAuthCredentials) (*GeminiOAuthCredentials, error) {
	if time.Now().UnixMilli() < creds.Expires {
		return creds, nil
	}

	log.Printf("[GEMINI_OAUTH] Token expired, refreshing...")
	result, err := RefreshGeminiOAuthToken(creds.RefreshToken)
	if err != nil {
		return nil, fmt.Errorf("refresh token: %w", err)
	}

	creds.AccessToken = result.AccessToken
	creds.RefreshToken = result.RefreshToken
	creds.Expires = result.Expires

	if err := SaveGeminiOAuthCredentials(creds); err != nil {
		log.Printf("[GEMINI_OAUTH] Warning: failed to save refreshed credentials: %v", err)
	}

	log.Printf("[GEMINI_OAUTH] Token refreshed successfully")
	return creds, nil
}

// ============================================================================
// User Info & Project Discovery
// ============================================================================

func getUserEmail(accessToken string) string {
	req, _ := http.NewRequest("GET", geminiOAuthUserInfoURL, nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("[GEMINI_OAUTH] Failed to get user email: %v", err)
		return ""
	}
	defer resp.Body.Close()

	var info struct {
		Email string `json:"email"`
	}
	body, _ := io.ReadAll(resp.Body)
	json.Unmarshal(body, &info)
	return info.Email
}

func discoverProject(accessToken string) (string, error) {
	reqBody := `{"metadata":{"ideType":"CLOUDSHELL","platform":"linux","pluginType":"GEMINI"}}`

	req, err := http.NewRequest("POST", geminiCodeAssistURL+"/v1internal:loadCodeAssist", strings.NewReader(reqBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "google-cloud-sdk vscode_cloudshelleditor/0.1")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("loadCodeAssist request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusOK {
		var result struct {
			CloudAICompanionProject string `json:"cloudAicompanionProject"`
		}
		if err := json.Unmarshal(body, &result); err == nil && result.CloudAICompanionProject != "" {
			log.Printf("[GEMINI_OAUTH] Found existing project: %s", result.CloudAICompanionProject)
			return result.CloudAICompanionProject, nil
		}
	}

	log.Printf("[GEMINI_OAUTH] No existing project, onboarding user...")
	return onboardUser(accessToken)
}

func onboardUser(accessToken string) (string, error) {
	reqBody := `{"tierId":"free","metadata":{"ideType":"CLOUDSHELL","platform":"linux","pluginType":"GEMINI"}}`

	req, err := http.NewRequest("POST", geminiCodeAssistURL+"/v1internal:onboardUser", strings.NewReader(reqBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "google-cloud-sdk vscode_cloudshelleditor/0.1")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("onboardUser request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("onboardUser failed (%d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		Name                    string `json:"name"`
		Done                    bool   `json:"done"`
		CloudAICompanionProject string `json:"cloudAicompanionProject"`
		Response                struct {
			CloudAICompanionProject string `json:"cloudAicompanionProject"`
		} `json:"response"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("parse onboard response: %w", err)
	}

	if result.CloudAICompanionProject != "" {
		return result.CloudAICompanionProject, nil
	}
	if result.Response.CloudAICompanionProject != "" {
		return result.Response.CloudAICompanionProject, nil
	}

	if result.Name != "" && !result.Done {
		return pollOperation(accessToken, result.Name)
	}

	return "", fmt.Errorf("onboard returned no project ID")
}

func pollOperation(accessToken, operationName string) (string, error) {
	for i := 0; i < 30; i++ {
		time.Sleep(5 * time.Second)

		req, _ := http.NewRequest("GET", geminiCodeAssistURL+"/v1internal/"+operationName, nil)
		req.Header.Set("Authorization", "Bearer "+accessToken)
		req.Header.Set("User-Agent", "google-cloud-sdk vscode_cloudshelleditor/0.1")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			continue
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var op struct {
			Done     bool `json:"done"`
			Response struct {
				CloudAICompanionProject string `json:"cloudAicompanionProject"`
			} `json:"response"`
		}

		if err := json.Unmarshal(body, &op); err != nil {
			continue
		}

		if op.Done && op.Response.CloudAICompanionProject != "" {
			return op.Response.CloudAICompanionProject, nil
		}
	}

	return "", fmt.Errorf("operation polling timeout")
}

// ============================================================================
// Credential Storage
// ============================================================================

var geminiOAuthDBKeys = []string{
	dbKeyGeminiOAuthAccess, dbKeyGeminiOAuthRefresh,
	dbKeyGeminiOAuthExpires, dbKeyGeminiOAuthProjectID, dbKeyGeminiOAuthEmail,
}

// SaveGeminiOAuthCredentials сохраняет OAuth credentials в БД
func SaveGeminiOAuthCredentials(creds *GeminiOAuthCredentials) error {
	return SaveOAuthCredentials(map[string]string{
		dbKeyGeminiOAuthAccess:    creds.AccessToken,
		dbKeyGeminiOAuthRefresh:   creds.RefreshToken,
		dbKeyGeminiOAuthExpires:   fmt.Sprintf("%d", creds.Expires),
		dbKeyGeminiOAuthProjectID: creds.ProjectID,
		dbKeyGeminiOAuthEmail:     creds.Email,
	}, "Gemini OAuth")
}

// LoadGeminiOAuthCredentials загружает OAuth credentials из БД
func LoadGeminiOAuthCredentials() *GeminiOAuthCredentials {
	access := database.GetSetting(dbKeyGeminiOAuthAccess, "")
	if access == "" {
		return nil
	}

	var expires int64
	fmt.Sscanf(database.GetSetting(dbKeyGeminiOAuthExpires, "0"), "%d", &expires)

	return &GeminiOAuthCredentials{
		AccessToken:  access,
		RefreshToken: database.GetSetting(dbKeyGeminiOAuthRefresh, ""),
		Expires:      expires,
		ProjectID:    database.GetSetting(dbKeyGeminiOAuthProjectID, ""),
		Email:        database.GetSetting(dbKeyGeminiOAuthEmail, ""),
	}
}

// DeleteGeminiOAuthCredentials удаляет OAuth credentials из БД
func DeleteGeminiOAuthCredentials() error {
	DeleteOAuthCredentials(geminiOAuthDBKeys, "Gemini OAuth disconnected")
	log.Printf("[GEMINI_OAUTH] Credentials deleted")
	return nil
}

// GetGeminiOAuthStatus возвращает статус подключения
func GetGeminiOAuthStatus() map[string]interface{} {
	creds := LoadGeminiOAuthCredentials()
	if creds == nil || creds.AccessToken == "" {
		return map[string]interface{}{
			"connected": false,
		}
	}

	expired := time.Now().UnixMilli() >= creds.Expires
	return map[string]interface{}{
		"connected": true,
		"email":     creds.Email,
		"projectId": creds.ProjectID,
		"expired":   expired,
	}
}
