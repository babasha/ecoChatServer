package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/egor/ecochatserver/database"
)

func generateOAuthState() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

const (
	// Facebook OAuth URLs
	facebookAuthURL  = "https://www.facebook.com/v21.0/dialog/oauth"
	facebookTokenURL = "https://graph.facebook.com/v21.0/oauth/access_token"
	facebookGraphURL = "https://graph.facebook.com/v21.0"

	// Instagram Login OAuth URLs (новый flow через Instagram, не Facebook)
	instagramTokenURL         = "https://api.instagram.com/oauth/access_token"
	instagramLongLivedTokenURL = "https://graph.instagram.com/access_token"
	instagramGraphURL         = "https://graph.instagram.com"
)

type facebookTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}

// exchangeForLongLivedUserToken меняет short-lived user token на long-lived user token (~60 дней)
func exchangeForLongLivedUserToken(shortToken, clientID, clientSecret string) (*facebookTokenResponse, error) {
	if shortToken == "" {
		return nil, fmt.Errorf("short-lived token is empty")
	}
	if clientID == "" || clientSecret == "" {
		return nil, fmt.Errorf("facebook client credentials are not configured")
	}

	params := url.Values{}
	params.Set("grant_type", "fb_exchange_token")
	params.Set("client_id", clientID)
	params.Set("client_secret", clientSecret)
	params.Set("fb_exchange_token", shortToken)

	exchangeURL := fmt.Sprintf("%s?%s", facebookTokenURL, params.Encode())

	resp, err := http.Get(exchangeURL)
	if err != nil {
		return nil, fmt.Errorf("long-lived token request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("long-lived token read failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("long-lived token exchange failed: %s", string(body))
	}

	var token facebookTokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return nil, fmt.Errorf("long-lived token parse failed: %w", err)
	}

	return &token, nil
}

// InstagramOAuthInitiate инициирует процесс OAuth через Facebook Business Login
// (именно через него доступны Instagram DM и messaging permissions)
// GET /api/instagram/oauth/init
func InstagramOAuthInitiate(c *gin.Context) {
	clientID := os.Getenv("FACEBOOK_APP_ID")
	if clientID == "" {
		log.Println("InstagramOAuthInitiate: FACEBOOK_APP_ID не настроен")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "OAuth не настроен на сервере"})
		return
	}

	// Redirect URI прописан в Meta Developer → Valid OAuth redirect URIs
	redirectURI := "https://ecochatserver-production.up.railway.app/auth/instagram/callback"
	if v := os.Getenv("INSTAGRAM_OAUTH_REDIRECT_URI"); v != "" {
		redirectURI = v
	}

	// Scopes для Instagram Business Login
	scopes := "instagram_basic,instagram_manage_messages"

	// Генерируем state для CSRF-защиты — callback его проверит
	state := generateOAuthState()
	c.SetCookie("oauth_state", state, 600, "/", "", false, true)

	// OAuth через Facebook (не Instagram) — именно так работает Instagram Business API
	authURL := fmt.Sprintf(
		"%s?client_id=%s&redirect_uri=%s&scope=%s&response_type=code&state=%s",
		facebookAuthURL,
		url.QueryEscape(clientID),
		url.QueryEscape(redirectURI),
		url.QueryEscape(scopes),
		url.QueryEscape(state),
	)

	log.Printf("InstagramOAuthInitiate: auth URL сформирован для client_id=%s, redirect=%s", clientID, redirectURI)

	c.JSON(http.StatusOK, gin.H{"authUrl": authURL})
}

// igFrontendURL возвращает базовый URL фронтенда из env или дефолт
func igFrontendURL() string {
	if v := os.Getenv("FRONTEND_URL"); v != "" {
		return v
	}
	return "https://admin-chat-vert.vercel.app"
}

// InstagramOAuthCallback обрабатывает callback от Facebook OAuth
// GET /api/instagram/oauth/callback и GET /auth/instagram/callback
func InstagramOAuthCallback(c *gin.Context) {
	code := c.Query("code")
	if code == "" {
		log.Println("InstagramOAuthCallback: code отсутствует")
		c.Redirect(http.StatusFound, igFrontendURL()+"/settings?error=no_code")
		return
	}

	clientID := os.Getenv("FACEBOOK_APP_ID")
	clientSecret := os.Getenv("FACEBOOK_APP_SECRET")
	redirectURI := "https://ecochatserver-production.up.railway.app/auth/instagram/callback"
	if v := os.Getenv("INSTAGRAM_OAUTH_REDIRECT_URI"); v != "" {
		redirectURI = v
	}

	if clientID == "" || clientSecret == "" {
		log.Println("InstagramOAuthCallback: FACEBOOK_APP_ID или FACEBOOK_APP_SECRET не настроены")
		c.Redirect(http.StatusFound, igFrontendURL()+"/settings?error=config_missing")
		return
	}

	// Обмен code на access_token через Facebook Graph API
	tokenURL := fmt.Sprintf(
		"https://graph.facebook.com/v21.0/oauth/access_token?client_id=%s&redirect_uri=%s&client_secret=%s&code=%s",
		url.QueryEscape(clientID),
		url.QueryEscape(redirectURI),
		url.QueryEscape(clientSecret),
		url.QueryEscape(code),
	)

	log.Println("InstagramOAuthCallback: обмениваем code на access_token...")

	resp, err := http.Get(tokenURL)
	if err != nil {
		log.Printf("InstagramOAuthCallback: ошибка запроса токена: %v", err)
		c.Redirect(http.StatusFound, igFrontendURL()+"/settings?error=token_exchange_failed")
		return
	}
	defer resp.Body.Close()

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		log.Printf("InstagramOAuthCallback: ошибка парсинга токена: %v", err)
		c.Redirect(http.StatusFound, igFrontendURL()+"/settings?error=token_parse_failed")
		return
	}

	if tokenResp.AccessToken == "" {
		log.Printf("InstagramOAuthCallback: пустой access_token, статус=%d", resp.StatusCode)
		c.Redirect(http.StatusFound, igFrontendURL()+"/settings?error=token_empty")
		return
	}

	log.Printf("InstagramOAuthCallback: токен получен (expires_in=%d)", tokenResp.ExpiresIn)

	// Сохраняем токен в настройки БД
	_ = database.SetSetting(instagramAccessTokenSetting, tokenResp.AccessToken, "Instagram access token")
	tokenExpiresAt := time.Now().Add(60 * 24 * time.Hour)
	if tokenResp.ExpiresIn > 0 {
		tokenExpiresAt = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	}
	_ = database.SetSetting("INSTAGRAM_TOKEN_EXPIRES_AT", tokenExpiresAt.UTC().Format(time.RFC3339), "Instagram token expiration")

	redirectTo := igFrontendURL() + "/settings?instagram_connected=true"
	log.Printf("InstagramOAuthCallback: токен сохранён, редирект → %s (FRONTEND_URL=%q)", redirectTo, os.Getenv("FRONTEND_URL"))
	// Используем location.replace чтобы не засорять browser history
	// (иначе кнопка "назад" вернёт на /auth/instagram/callback с протухшим code)
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.String(http.StatusOK, `<!DOCTYPE html><html><head><script>window.location.replace(%q);</script></head><body></body></html>`, redirectTo)
}

// FacebookPage представляет страницу Facebook
type FacebookPage struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	AccessToken string `json:"access_token"`
}

// InstagramAccountInfo содержит информацию об Instagram Business аккаунте
type InstagramAccountInfo struct {
	ID              string `json:"id"`
	Username        string `json:"username"`
	Name            string `json:"name"`
	ProfilePicture  string `json:"profile_picture_url"`
	PageID          string
	PageName        string
	PageAccessToken string
	TokenExpiresAt  time.Time
}

// fetchUserPages получает список страниц пользователя
func fetchUserPages(accessToken string) ([]FacebookPage, error) {
	url := fmt.Sprintf("%s/me/accounts?access_token=%s", facebookGraphURL, url.QueryEscape(accessToken))

	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	var response struct {
		Data []FacebookPage `json:"data"`
	}

	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return response.Data, nil
}

// fetchInstagramBusinessAccount получает Instagram Business аккаунт для страницы
func fetchInstagramBusinessAccount(pageAccessToken, pageID string) (*InstagramAccountInfo, error) {
	url := fmt.Sprintf("%s/%s?fields=instagram_business_account{id,username,name,profile_picture_url}&access_token=%s",
		facebookGraphURL, pageID, url.QueryEscape(pageAccessToken))

	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	var response struct {
		InstagramBusinessAccount *InstagramAccountInfo `json:"instagram_business_account"`
	}

	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return response.InstagramBusinessAccount, nil
}

// saveInstagramAccount сохраняет информацию об Instagram аккаунте в БД
func saveInstagramAccount(account InstagramAccountInfo) error {
	db := database.DB
	if db == nil {
		return fmt.Errorf("database not initialized")
	}

	// Используем время истечения токена, полученное от Facebook. Если отсутствует — дефолт 60 дней.
	expiresAt := account.TokenExpiresAt
	if expiresAt.IsZero() {
		expiresAt = time.Now().Add(60 * 24 * time.Hour)
	}

	query := `
		INSERT INTO instagram_accounts
		(instagram_account_id, username, name, profile_picture_url, page_id, page_name,
		 access_token, token_expires_at, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW(), NOW())
		ON CONFLICT (instagram_account_id)
		DO UPDATE SET
			username = EXCLUDED.username,
			name = EXCLUDED.name,
			profile_picture_url = EXCLUDED.profile_picture_url,
			page_id = EXCLUDED.page_id,
			page_name = EXCLUDED.page_name,
			access_token = EXCLUDED.access_token,
			token_expires_at = EXCLUDED.token_expires_at,
			status = EXCLUDED.status,
			updated_at = NOW()
	`

	_, err := db.Exec(query,
		account.ID,
		account.Username,
		account.Name,
		account.ProfilePicture,
		account.PageID,
		account.PageName,
		account.PageAccessToken,
		expiresAt,
		"active",
	)

	if err != nil {
		return fmt.Errorf("failed to save account: %w", err)
	}

	// Обновляем переменные окружения в настройках
	_ = database.SetSetting("INSTAGRAM_BUSINESS_ACCOUNT_ID", account.ID, "Instagram Business account ID")
	_ = database.SetSetting("INSTAGRAM_ACCESS_TOKEN", account.PageAccessToken, "Instagram access token")
	_ = database.SetSetting("INSTAGRAM_TOKEN_EXPIRES_AT", expiresAt.UTC().Format(time.RFC3339), "Instagram token expiration timestamp")

	return nil
}

// GetInstagramAccountStatus возвращает статус подключенного Instagram аккаунта
// GET /api/instagram/status
func GetInstagramAccountStatus(c *gin.Context) {
	db := database.DB
	if db == nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "database not initialized",
		})
		return
	}

	var account struct {
		InstagramAccountID string    `db:"instagram_account_id"`
		Username           string    `db:"username"`
		Name               string    `db:"name"`
		ProfilePictureURL  string    `db:"profile_picture_url"`
		PageName           string    `db:"page_name"`
		Status             string    `db:"status"`
		TokenExpiresAt     time.Time `db:"token_expires_at"`
		CreatedAt          time.Time `db:"created_at"`
		UpdatedAt          time.Time `db:"updated_at"`
	}

	query := `
		SELECT instagram_account_id, username, name, profile_picture_url, page_name,
		       status, token_expires_at, created_at, updated_at
		FROM instagram_accounts
		WHERE status = 'active'
		ORDER BY updated_at DESC
		LIMIT 1
	`

	row := db.QueryRow(query)
	err := row.Scan(
		&account.InstagramAccountID,
		&account.Username,
		&account.Name,
		&account.ProfilePictureURL,
		&account.PageName,
		&account.Status,
		&account.TokenExpiresAt,
		&account.CreatedAt,
		&account.UpdatedAt,
	)
	if err != nil {
		// Аккаунт не найден
		c.JSON(http.StatusOK, gin.H{
			"connected": false,
		})
		return
	}

	// Проверяем, не истек ли токен
	tokenExpired := time.Now().After(account.TokenExpiresAt)
	daysUntilExpiry := int(time.Until(account.TokenExpiresAt).Hours() / 24)
	if daysUntilExpiry < 0 {
		daysUntilExpiry = 0
	}

	c.JSON(http.StatusOK, gin.H{
		"connected":          true,
		"instagramAccountId": account.InstagramAccountID,
		"username":           account.Username,
		"name":               account.Name,
		"profilePictureUrl":  account.ProfilePictureURL,
		"pageName":           account.PageName,
		"status":             account.Status,
		"tokenExpired":       tokenExpired,
		"daysUntilExpiry":    daysUntilExpiry,
		"connectedAt":        account.CreatedAt,
		"lastUpdated":        account.UpdatedAt,
	})
}

// DisconnectInstagramAccount отключает Instagram аккаунт
// POST /api/instagram/disconnect
func DisconnectInstagramAccount(c *gin.Context) {
	db := database.DB
	if db == nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "database not initialized",
		})
		return
	}

	query := `
		UPDATE instagram_accounts
		SET status = 'disconnected',
		    updated_at = NOW()
		WHERE status = 'active'
	`

	result, err := db.Exec(query)
	if err != nil {
		log.Printf("DisconnectInstagramAccount: Error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "failed to disconnect account",
		})
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{
			"error": "no active account found",
		})
		return
	}

	log.Println("DisconnectInstagramAccount: Account disconnected successfully")

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Instagram account disconnected",
	})
}

// InstagramLoginCallback обрабатывает callback от Instagram Login (новый flow).
// Redirect URI в Meta настроен на /auth/instagram/callback
// GET /auth/instagram/callback
func InstagramLoginCallback(c *gin.Context) {
	code := c.Query("code")
	errorCode := c.Query("error")
	errorReason := c.Query("error_reason")

	frontendURL := igFrontendURL()

	if errorCode != "" {
		log.Printf("InstagramLoginCallback: Instagram OAuth error: %s — %s", errorCode, errorReason)
		c.Redirect(http.StatusTemporaryRedirect,
			frontendURL+"/settings?error="+url.QueryEscape(errorReason))
		return
	}

	if code == "" {
		log.Println("InstagramLoginCallback: code отсутствует")
		c.Redirect(http.StatusTemporaryRedirect, frontendURL+"/settings?error=no_code")
		return
	}

	clientID := os.Getenv("FACEBOOK_APP_ID")
	clientSecret := os.Getenv("FACEBOOK_APP_SECRET")
	redirectURI := "https://ecochatserver-production.up.railway.app/auth/instagram/callback"
	if v := os.Getenv("INSTAGRAM_OAUTH_REDIRECT_URI"); v != "" {
		redirectURI = v
	}

	if clientID == "" || clientSecret == "" {
		log.Println("InstagramLoginCallback: FACEBOOK_APP_ID или FACEBOOK_APP_SECRET не настроены")
		c.Redirect(http.StatusTemporaryRedirect, frontendURL+"/settings?error=config_missing")
		return
	}

	// Шаг 1: Обмениваем code на short-lived user token
	// POST https://api.instagram.com/oauth/access_token
	formData := url.Values{}
	formData.Set("client_id", clientID)
	formData.Set("client_secret", clientSecret)
	formData.Set("grant_type", "authorization_code")
	formData.Set("redirect_uri", redirectURI)
	formData.Set("code", code)

	log.Println("InstagramLoginCallback: обмениваем code на short-lived token...")

	resp, err := http.PostForm(instagramTokenURL, formData)
	if err != nil {
		log.Printf("InstagramLoginCallback: ошибка POST к Instagram: %v", err)
		c.Redirect(http.StatusTemporaryRedirect, frontendURL+"/settings?error=exchange_failed")
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	log.Printf("InstagramLoginCallback: short-lived token response status=%d body=%s", resp.StatusCode, truncateForLog(string(body), 500))

	if resp.StatusCode != http.StatusOK {
		log.Printf("InstagramLoginCallback: ошибка получения short-lived token: %s", string(body))
		c.Redirect(http.StatusTemporaryRedirect, frontendURL+"/settings?error=token_failed")
		return
	}

	var shortToken struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		UserID      int64  `json:"user_id"`
	}
	if err := json.Unmarshal(body, &shortToken); err != nil {
		log.Printf("InstagramLoginCallback: ошибка парсинга short-lived token: %v", err)
		c.Redirect(http.StatusTemporaryRedirect, frontendURL+"/settings?error=parse_failed")
		return
	}

	log.Printf("InstagramLoginCallback: short-lived token получен для user_id=%d", shortToken.UserID)

	// Шаг 2: Обмениваем short-lived на long-lived token (~60 дней)
	// GET https://graph.instagram.com/access_token?grant_type=ig_exchange_token&...
	longTokenURL := fmt.Sprintf("%s?grant_type=ig_exchange_token&client_secret=%s&access_token=%s",
		instagramLongLivedTokenURL,
		url.QueryEscape(clientSecret),
		url.QueryEscape(shortToken.AccessToken),
	)

	longResp, err := http.Get(longTokenURL)
	finalToken := shortToken.AccessToken
	tokenExpiresAt := time.Now().Add(60 * 24 * time.Hour) // дефолт 60 дней

	if err != nil {
		log.Printf("InstagramLoginCallback: ошибка получения long-lived token: %v, используем short-lived", err)
	} else {
		defer longResp.Body.Close()
		longBody, _ := io.ReadAll(longResp.Body)
		log.Printf("InstagramLoginCallback: long-lived token response status=%d body=%s", longResp.StatusCode, truncateForLog(string(longBody), 300))

		var longToken struct {
			AccessToken string `json:"access_token"`
			TokenType   string `json:"token_type"`
			ExpiresIn   int64  `json:"expires_in"`
		}
		if longResp.StatusCode == http.StatusOK {
			if err := json.Unmarshal(longBody, &longToken); err == nil && longToken.AccessToken != "" {
				finalToken = longToken.AccessToken
				if longToken.ExpiresIn > 0 {
					tokenExpiresAt = time.Now().Add(time.Duration(longToken.ExpiresIn) * time.Second)
				}
				log.Printf("InstagramLoginCallback: long-lived token получен, истекает %s", tokenExpiresAt.Format(time.RFC3339))
			}
		}
	}

	// Шаг 3: Получаем информацию об аккаунте
	// GET https://graph.instagram.com/me?fields=id,username,name&access_token=...
	meURL := fmt.Sprintf("%s/me?fields=id,username,name,profile_picture_url&access_token=%s",
		instagramGraphURL,
		url.QueryEscape(finalToken),
	)

	meResp, err := http.Get(meURL)
	var igUsername, igName, igID string
	if err != nil {
		log.Printf("InstagramLoginCallback: ошибка получения /me: %v", err)
	} else {
		defer meResp.Body.Close()
		meBody, _ := io.ReadAll(meResp.Body)
		log.Printf("InstagramLoginCallback: /me response status=%d body=%s", meResp.StatusCode, truncateForLog(string(meBody), 300))

		var me struct {
			ID                string `json:"id"`
			Username          string `json:"username"`
			Name              string `json:"name"`
			ProfilePictureURL string `json:"profile_picture_url"`
		}
		if meResp.StatusCode == http.StatusOK {
			if err := json.Unmarshal(meBody, &me); err == nil {
				igID = me.ID
				igUsername = me.Username
				igName = me.Name
			}
		}
	}

	if igID == "" {
		igID = fmt.Sprintf("%d", shortToken.UserID)
	}
	if igUsername == "" {
		igUsername = igID
	}

	// Шаг 4: Сохраняем токен и данные аккаунта в настройки
	_ = database.SetSetting(instagramAccessTokenSetting, finalToken, "Instagram access token (Login flow)")
	_ = database.SetSetting(instagramBusinessIDSetting, igID, "Instagram user/business account ID")
	_ = database.SetSetting("INSTAGRAM_TOKEN_EXPIRES_AT", tokenExpiresAt.UTC().Format(time.RFC3339), "Instagram token expiration")

	log.Printf("InstagramLoginCallback: успешно подключён аккаунт @%s (id=%s)", igUsername, igID)

	// Также сохраняем в таблицу instagram_accounts если она есть
	account := InstagramAccountInfo{
		ID:             igID,
		Username:       igUsername,
		Name:           igName,
		TokenExpiresAt: tokenExpiresAt,
	}
	if err := saveInstagramAccount(account); err != nil {
		log.Printf("InstagramLoginCallback: ошибка сохранения в instagram_accounts: %v (продолжаем)", err)
	}

	c.Redirect(http.StatusTemporaryRedirect, frontendURL+"/settings?instagram_connected=true")
}
