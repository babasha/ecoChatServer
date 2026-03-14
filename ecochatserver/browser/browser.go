package browser

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/chromedp"
)

// Service provides headless Chrome automation for the Director.
// Capabilities: navigate, screenshot, extract text, evaluate JS.
type Service struct {
	mu        sync.Mutex
	allocCtx  context.Context
	cancel    context.CancelFunc
	running   bool
	allowList []string // allowed URL patterns (SSRF protection)
}

// ScreenshotResult holds screenshot data.
type ScreenshotResult struct {
	URL       string `json:"url"`
	Title     string `json:"title"`
	ImageB64  string `json:"image_base64"` // PNG base64-encoded
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	Timestamp string `json:"timestamp"`
}

// PageTextResult holds extracted page text.
type PageTextResult struct {
	URL   string `json:"url"`
	Title string `json:"title"`
	Text  string `json:"text"`
}

// New creates a new browser service.
func New() *Service {
	return &Service{}
}

// Start initializes the headless Chrome allocator.
func (s *Service) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return nil
	}

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-extensions", true),
		chromedp.Flag("disable-background-networking", true),
		chromedp.Flag("disable-default-apps", true),
		chromedp.Flag("disable-sync", true),
		chromedp.Flag("disable-translate", true),
		chromedp.WindowSize(1280, 720),
	)

	s.allocCtx, s.cancel = chromedp.NewExecAllocator(context.Background(), opts...)
	s.running = true
	log.Println("[BROWSER] Service started (headless Chrome)")
	return nil
}

// Stop shuts down the browser allocator.
func (s *Service) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return
	}

	s.cancel()
	s.running = false
	log.Println("[BROWSER] Service stopped")
}

// SetAllowList sets the list of allowed URL patterns for SSRF protection.
// Patterns can be: exact domain, wildcard (*.example.com), or full URL prefix.
func (s *Service) SetAllowList(patterns []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.allowList = patterns
}

// Screenshot navigates to a URL and takes a PNG screenshot.
func (s *Service) Screenshot(ctx context.Context, targetURL string) (*ScreenshotResult, error) {
	if err := s.validateURL(targetURL); err != nil {
		return nil, err
	}

	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return nil, fmt.Errorf("browser service not started")
	}
	allocCtx := s.allocCtx
	s.mu.Unlock()

	// Create a new browser context with timeout
	timeoutCtx, timeoutCancel := context.WithTimeout(allocCtx, 30*time.Second)
	defer timeoutCancel()

	browserCtx, browserCancel := chromedp.NewContext(timeoutCtx)
	defer browserCancel()

	var buf []byte
	var title string

	err := chromedp.Run(browserCtx,
		chromedp.EmulateViewport(1280, 720),
		chromedp.Navigate(targetURL),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.Sleep(2*time.Second), // let JS render
		chromedp.Title(&title),
		chromedp.FullScreenshot(&buf, 80),
	)
	if err != nil {
		return nil, fmt.Errorf("screenshot failed: %w", err)
	}

	return &ScreenshotResult{
		URL:       targetURL,
		Title:     title,
		ImageB64:  base64.StdEncoding.EncodeToString(buf),
		Width:     1280,
		Height:    720,
		Timestamp: time.Now().Format(time.RFC3339),
	}, nil
}

// GetText navigates to a URL and extracts visible text content.
func (s *Service) GetText(ctx context.Context, targetURL string, maxLen int) (*PageTextResult, error) {
	if err := s.validateURL(targetURL); err != nil {
		return nil, err
	}

	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return nil, fmt.Errorf("browser service not started")
	}
	allocCtx := s.allocCtx
	s.mu.Unlock()

	timeoutCtx, timeoutCancel := context.WithTimeout(allocCtx, 30*time.Second)
	defer timeoutCancel()

	browserCtx, browserCancel := chromedp.NewContext(timeoutCtx)
	defer browserCancel()

	var title string
	var text string

	err := chromedp.Run(browserCtx,
		chromedp.Navigate(targetURL),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.Sleep(2*time.Second),
		chromedp.Title(&title),
		chromedp.ActionFunc(func(cdpCtx context.Context) error {
			node, err := dom.GetDocument().Do(cdpCtx)
			if err != nil {
				return err
			}
			text, err = dom.GetOuterHTML().WithNodeID(node.NodeID).Do(cdpCtx)
			return err
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("text extraction failed: %w", err)
	}

	// Strip HTML tags for clean text
	text = stripHTML(text)

	if maxLen > 0 && len([]rune(text)) > maxLen {
		text = string([]rune(text)[:maxLen]) + "…"
	}

	return &PageTextResult{
		URL:   targetURL,
		Title: title,
		Text:  text,
	}, nil
}

// EvalJS navigates to a URL and evaluates JavaScript, returning the result.
func (s *Service) EvalJS(ctx context.Context, targetURL, script string) (string, error) {
	if err := s.validateURL(targetURL); err != nil {
		return "", err
	}

	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return "", fmt.Errorf("browser service not started")
	}
	allocCtx := s.allocCtx
	s.mu.Unlock()

	timeoutCtx, timeoutCancel := context.WithTimeout(allocCtx, 30*time.Second)
	defer timeoutCancel()

	browserCtx, browserCancel := chromedp.NewContext(timeoutCtx)
	defer browserCancel()

	var result interface{}
	err := chromedp.Run(browserCtx,
		chromedp.Navigate(targetURL),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.Sleep(2*time.Second),
		chromedp.Evaluate(script, &result),
	)
	if err != nil {
		return "", fmt.Errorf("JS evaluation failed: %w", err)
	}

	return fmt.Sprintf("%v", result), nil
}

// ============================================================================
// SSRF Protection
// ============================================================================

// validateURL checks that the URL is allowed and not targeting internal networks.
func (s *Service) validateURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	// Only allow HTTP/HTTPS
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("only http/https URLs allowed, got: %s", parsed.Scheme)
	}

	// Block internal/private IPs
	host := parsed.Hostname()
	if isInternalHost(host) {
		return fmt.Errorf("access to internal networks is blocked: %s", host)
	}

	// Check allowlist if configured
	s.mu.Lock()
	allowList := s.allowList
	s.mu.Unlock()

	if len(allowList) > 0 {
		if !matchesAllowList(rawURL, host, allowList) {
			return fmt.Errorf("URL not in allowlist: %s", host)
		}
	}

	return nil
}

// isInternalHost checks if a hostname resolves to a private/internal address.
func isInternalHost(host string) bool {
	// Block common internal hostnames
	lower := strings.ToLower(host)
	if lower == "localhost" || lower == "127.0.0.1" || lower == "::1" ||
		lower == "0.0.0.0" || strings.HasSuffix(lower, ".local") ||
		strings.HasSuffix(lower, ".internal") {
		return true
	}

	// Resolve and check IP ranges
	ips, err := net.LookupIP(host)
	if err != nil {
		return false // DNS failure — let chromedp handle it
	}

	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
			ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return true
		}
		// Block metadata endpoints (cloud provider SSRF targets)
		if ip.Equal(net.ParseIP("169.254.169.254")) {
			return true
		}
	}

	return false
}

// matchesAllowList checks if a URL matches any pattern in the allowlist.
func matchesAllowList(rawURL, host string, patterns []string) bool {
	for _, pattern := range patterns {
		// Wildcard domain: *.example.com
		if strings.HasPrefix(pattern, "*.") {
			suffix := pattern[1:] // ".example.com"
			if strings.HasSuffix(host, suffix) || host == pattern[2:] {
				return true
			}
			continue
		}
		// Exact domain match
		if host == pattern {
			return true
		}
		// URL prefix match
		if strings.HasPrefix(rawURL, pattern) {
			return true
		}
	}
	return false
}

// ============================================================================
// Helpers
// ============================================================================

// stripHTML removes HTML tags from a string (simple approach).
func stripHTML(s string) string {
	var result strings.Builder
	inTag := false
	prevSpace := false

	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
			if !prevSpace {
				result.WriteRune(' ')
				prevSpace = true
			}
		case !inTag:
			if r == '\n' || r == '\r' || r == '\t' {
				if !prevSpace {
					result.WriteRune(' ')
					prevSpace = true
				}
			} else {
				result.WriteRune(r)
				prevSpace = r == ' '
			}
		}
	}

	return strings.TrimSpace(result.String())
}
