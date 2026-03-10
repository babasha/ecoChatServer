package llm

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/egor/ecochatserver/database"
)

// ============================================================================
// Common OAuth utilities — shared across Gemini, OpenAI, Claude OAuth flows
// ============================================================================

// OAuthSession represents an active OAuth session with PKCE state.
type OAuthSession struct {
	State        string
	CodeVerifier string
	CreatedAt    time.Time
}

// OAuthCallbackData represents data received from an OAuth callback.
type OAuthCallbackData struct {
	Code  string
	State string
	Error string
}

// ============================================================================
// PKCE helpers
// ============================================================================

// GeneratePKCE generates a PKCE code verifier and S256 challenge.
func GeneratePKCE() (verifier, challenge string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("generate random: %w", err)
	}
	verifier = base64.RawURLEncoding.EncodeToString(buf)

	h := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(h[:])

	return verifier, challenge, nil
}

// GenerateOAuthState generates a random state string.
// encoding: "base64url" or "hex"
func GenerateOAuthState(encoding string) (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	switch encoding {
	case "hex":
		return hex.EncodeToString(buf), nil
	default:
		return base64.RawURLEncoding.EncodeToString(buf), nil
	}
}

// ============================================================================
// Thread-safe session store
// ============================================================================

// OAuthSessionStore is a thread-safe store for OAuth sessions.
type OAuthSessionStore struct {
	mu       sync.Mutex
	sessions map[string]*OAuthSession
}

// NewOAuthSessionStore creates a new OAuthSessionStore.
func NewOAuthSessionStore() *OAuthSessionStore {
	return &OAuthSessionStore{
		sessions: make(map[string]*OAuthSession),
	}
}

// Put stores a session by state key.
func (s *OAuthSessionStore) Put(state string, session *OAuthSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[state] = session
}

// Take retrieves and deletes a session by state key.
func (s *OAuthSessionStore) Take(state string) (*OAuthSession, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[state]
	if ok {
		delete(s.sessions, state)
	}
	return sess, ok
}

// ============================================================================
// Thread-safe callback store
// ============================================================================

// OAuthCallbackStore is a thread-safe store for OAuth callback channels.
type OAuthCallbackStore struct {
	mu       sync.Mutex
	channels map[string]chan *OAuthCallbackData
}

// NewOAuthCallbackStore creates a new OAuthCallbackStore.
func NewOAuthCallbackStore() *OAuthCallbackStore {
	return &OAuthCallbackStore{
		channels: make(map[string]chan *OAuthCallbackData),
	}
}

// Register creates a buffered channel for the given state and returns it.
func (s *OAuthCallbackStore) Register(state string) chan *OAuthCallbackData {
	ch := make(chan *OAuthCallbackData, 1)
	s.mu.Lock()
	s.channels[state] = ch
	s.mu.Unlock()
	return ch
}

// Send sends callback data to the channel matching the given state.
func (s *OAuthCallbackStore) Send(state string, data *OAuthCallbackData) bool {
	s.mu.Lock()
	ch, ok := s.channels[state]
	s.mu.Unlock()
	if ok {
		ch <- data
	}
	return ok
}

// Delete removes the channel for the given state.
func (s *OAuthCallbackStore) Delete(state string) {
	s.mu.Lock()
	delete(s.channels, state)
	s.mu.Unlock()
}

// WaitForAny waits for any callback result (picks the latest active flow).
func (s *OAuthCallbackStore) WaitForAny(ctx context.Context, timeout time.Duration) (*OAuthCallbackData, error) {
	s.mu.Lock()
	var ch chan *OAuthCallbackData
	var foundState string
	for state, c := range s.channels {
		ch = c
		foundState = state
	}
	s.mu.Unlock()

	if ch == nil {
		return nil, fmt.Errorf("no OAuth flow in progress")
	}

	select {
	case data := <-ch:
		s.Delete(foundState)
		return data, nil
	case <-time.After(timeout):
		return nil, fmt.Errorf("OAuth callback timeout")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// ============================================================================
// Common callback server
// ============================================================================

// StartOAuthCallbackServer starts a temporary HTTP server to receive an OAuth callback.
func StartOAuthCallbackServer(expectedState, callbackPath string, port int, logPrefix string, store *OAuthCallbackStore) {
	ch := store.Register(expectedState)
	_ = ch // channel is used via store.Send

	mux := http.NewServeMux()
	mux.HandleFunc(callbackPath, func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		state := r.URL.Query().Get("state")
		errParam := r.URL.Query().Get("error")

		if errParam != "" {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprintf(w, `<html><body><h2>Authorization Error</h2><p>%s</p><p>You can close this window.</p></body></html>`, errParam)
			store.Send(state, &OAuthCallbackData{Error: errParam})
			return
		}

		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body><h2>Authorization Successful!</h2><p>You can close this window and return to the admin panel.</p><script>window.close()</script></body></html>`)
		store.Send(state, &OAuthCallbackData{Code: code, State: state})
	})

	server := &http.Server{
		Addr:    fmt.Sprintf("127.0.0.1:%d", port),
		Handler: mux,
	}

	go func() {
		time.Sleep(5 * time.Minute)
		server.Close()
		store.Delete(expectedState)
	}()

	log.Printf("[%s] Callback server starting on :%d", logPrefix, port)
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		log.Printf("[%s] Callback server error: %v (port may be busy — use manual code paste)", logPrefix, err)
	}
}

// ============================================================================
// Common credential DB helpers
// ============================================================================

// SaveOAuthCredentials saves a map of key→value pairs to database settings.
func SaveOAuthCredentials(settings map[string]string, description string) error {
	for key, value := range settings {
		if err := database.SetSetting(key, value, description); err != nil {
			return fmt.Errorf("save %s: %w", key, err)
		}
	}
	return nil
}

// DeleteOAuthCredentials clears the given keys in database settings.
func DeleteOAuthCredentials(keys []string, description string) {
	for _, key := range keys {
		database.SetSetting(key, "", description)
	}
}
