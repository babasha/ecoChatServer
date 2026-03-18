package director

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/egor/ecochatserver/database"
	"github.com/egor/ecochatserver/llm"
)

// Director — Level 2 agent that analyzes РОП's work and produces directives
// Uses a powerful cloud model (Claude/GPT-4) for strategic analysis
type Director struct {
	tracker   *EventTracker
	sentinel  *Sentinel         // pattern-based anomaly detection
	provider  llm.Provider      // separate provider — can be Claude while РОП uses Qwen
	optimizer *PromptOptimizer  // prompt versioning and optimization
}

// Config for Director initialization
type Config struct {
	// Provider config for the Director's own LLM (e.g., Claude)
	// If nil, falls back to global provider
	ProviderConfig *llm.ProviderConfig
}

// New creates a Director with its own LLM provider.
// Priority: explicit config → DB settings (DIRECTOR_*) → global provider fallback
func New(cfg Config) (*Director, error) {
	var provider llm.Provider

	if cfg.ProviderConfig != nil {
		// Explicit config passed
		var err error
		provider, err = llm.NewProvider(cfg.ProviderConfig)
		if err != nil {
			return nil, fmt.Errorf("create director provider: %w", err)
		}
		log.Printf("[DIRECTOR] Using dedicated provider: %s", provider.GetName())
	} else {
		// Try loading from DB settings: DIRECTOR_PROVIDER, DIRECTOR_API_KEY, DIRECTOR_MODEL
		dbProvider := loadDirectorProviderFromDB()
		if dbProvider != nil {
			provider = dbProvider
			log.Printf("[DIRECTOR] Using DB-configured provider: %s", provider.GetName())
		} else {
			provider = llm.GetGlobalProvider()
			log.Printf("[DIRECTOR] Using global provider (fallback)")
		}
	}

	tracker := NewEventTracker(DefaultTrackerConfig())

	d := &Director{
		tracker:   tracker,
		sentinel:  NewSentinel(),
		provider:  provider,
		optimizer: NewPromptOptimizer(provider),
	}

	// Make the provider available for Critic in package-level functions (identity.go)
	SetCriticProvider(provider)

	// Bootstrap identity (seed defaults if first run)
	d.BootstrapIdentity()

	// Start background cleanup goroutine
	go d.periodicCleanup()

	log.Printf("[DIRECTOR] Initialized — Level 2 agent ready")
	return d, nil
}

// Tracker returns the event tracker for external event recording.
func (d *Director) Tracker() *EventTracker {
	return d.tracker
}

// Provider returns the Director's LLM provider (for chat API).
func (d *Director) Provider() llm.Provider {
	return d.provider
}

// Sentinel returns the pattern detector for external event recording.
func (d *Director) Sentinel() *Sentinel {
	return d.sentinel
}

// Optimizer returns the prompt optimizer for external use (API handlers).
func (d *Director) Optimizer() *PromptOptimizer {
	return d.optimizer
}

// CheckAndAnalyze checks if analysis should be triggered, runs it if needed.
// Called asynchronously after each message processing.
func (d *Director) CheckAndAnalyze(ctx context.Context) {
	shouldTrigger, reason := d.tracker.ShouldTriggerDirector()
	if !shouldTrigger {
		return
	}

	log.Printf("[DIRECTOR] Triggered: %s (count=%d, window=%v)",
		reason.Description, reason.Count, reason.Window)

	if err := d.AnalyzeEvent(ctx, reason); err != nil {
		log.Printf("[DIRECTOR] Event analysis failed: %v", err)
	}
}

// FeedSentinel sends a rich event to the Sentinel for pattern detection.
// If a pattern is detected, returns the alert (caller can trigger targeted analysis).
func (d *Director) FeedSentinel(event SentinelEvent) *SentinelAlert {
	return d.sentinel.RecordEvent(event)
}

// AnalyzeEvent runs analysis triggered by a specific event.
func (d *Director) AnalyzeEvent(ctx context.Context, reason *TriggerReason) error {
	return d.analyze(ctx, "event_triggered", reason.Description)
}

// AnalyzeDaily runs the daily analysis (called by cron or manually).
func (d *Director) AnalyzeDaily(ctx context.Context) error {
	return d.analyze(ctx, "daily", "scheduled daily analysis")
}

// GetActiveDirectives returns current directives that the РОП should follow.
func (d *Director) GetActiveDirectives(ctx context.Context) ([]Directive, error) {
	report, err := database.GetLatestDirectorReport()
	if err != nil {
		return nil, err
	}
	if report == nil {
		return nil, nil
	}

	// Filter out expired directives
	var active []Directive
	now := time.Now()
	for _, dir := range report.Directives {
		if dir.ExpiresAt != nil && now.After(*dir.ExpiresAt) {
			continue
		}
		active = append(active, dir)
	}

	return active, nil
}

// BuildDirectivesContext formats active directives for injection into agent context.
func (d *Director) BuildDirectivesContext(ctx context.Context) string {
	directives, err := d.GetActiveDirectives(ctx)
	if err != nil {
		log.Printf("[DIRECTOR] Failed to get directives: %v", err)
		return ""
	}

	if len(directives) == 0 {
		return ""
	}

	var parts []string
	for _, dir := range directives {
		prefix := ""
		if dir.Priority == "high" {
			prefix = "IMPORTANT: "
		}
		parts = append(parts, fmt.Sprintf("- %s%s", prefix, dir.Instruction))
	}

	return "[DIRECTOR INSTRUCTIONS]\n" + strings.Join(parts, "\n") + "\n[END INSTRUCTIONS]"
}

// periodicCleanup runs background maintenance tasks.
func (d *Director) periodicCleanup() {
	trackerTicker := time.NewTicker(1 * time.Hour)
	decayTicker := time.NewTicker(24 * time.Hour)
	digestTicker := time.NewTicker(6 * time.Hour)           // check for digest generation
	introspectTicker := time.NewTicker(7 * 24 * time.Hour)  // weekly introspection
	skillBuilderTicker := time.NewTicker(24 * time.Hour)    // daily skill auto-creation check
	defer trackerTicker.Stop()
	defer decayTicker.Stop()
	defer digestTicker.Stop()
	defer introspectTicker.Stop()
	defer skillBuilderTicker.Stop()

	for {
		select {
		case <-trackerTicker.C:
			d.tracker.Cleanup()
			d.sentinel.Cleanup()
			d.sentinel.UpdateBaseline()
		case <-decayTicker.C:
			decayed, purged, err := database.DecayMemories()
			if err != nil {
				log.Printf("[DIRECTOR] Memory decay error: %v", err)
			} else if decayed > 0 || purged > 0 {
				log.Printf("[DIRECTOR] Memory maintenance: decayed=%d, purged=%d", decayed, purged)
			}
			// Webhook events retention (default 30 days)
			webhookRetention := database.GetSettingInt("DIRECTOR_WEBHOOK_RETENTION_DAYS", 30)
			if deleted, err := database.CleanupOldWebhookEvents(webhookRetention); err != nil {
				log.Printf("[DIRECTOR] Webhook cleanup error: %v", err)
			} else if deleted > 0 {
				log.Printf("[DIRECTOR] Webhook cleanup: deleted %d old events (retention=%d days)", deleted, webhookRetention)
			}
			// Embedding cache cleanup
			if client := llm.GetEmbeddingClient(); client != nil && client.Cache() != nil {
				cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
				client.Cache().Cleanup(cleanupCtx)
				cleanupCancel()
			}
			// Compaction retention cleanup
			retentionDays := database.GetSettingInt("director_compaction_retention_days", 0)
			if retentionDays > 0 {
				if deleted, err := database.DeleteOldCompactions(retentionDays); err != nil {
					log.Printf("[DIRECTOR] Compaction cleanup error: %v", err)
				} else if deleted > 0 {
					log.Printf("[DIRECTOR] Compaction cleanup: deleted %d old compactions (retention=%d days)", deleted, retentionDays)
				}
			}
		case <-digestTicker.C:
			d.generateDigestsIfNeeded()
		case <-introspectTicker.C:
			d.PeriodicIntrospect()
		case <-skillBuilderTicker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			d.analyzeToolPatterns(ctx)
			cancel()
		}
	}
}

// loadDirectorProviderFromDB tries to create a dedicated LLM provider from DB settings.
// Settings: DIRECTOR_PROVIDER (claude/openai/gemini), DIRECTOR_API_KEY, DIRECTOR_MODEL
func loadDirectorProviderFromDB() llm.Provider {
	providerType := database.GetSetting("DIRECTOR_PROVIDER", "")
	if providerType == "" {
		return nil
	}

	apiKey := database.GetSetting("DIRECTOR_API_KEY", "")
	if apiKey == "" {
		log.Printf("[DIRECTOR] DIRECTOR_PROVIDER=%s but no DIRECTOR_API_KEY set", providerType)
		return nil
	}

	model := database.GetSetting("DIRECTOR_MODEL", "")

	provider, err := llm.NewProvider(&llm.ProviderConfig{
		Type:   llm.ProviderType(providerType),
		APIKey: apiKey,
		Model:  model,
	})
	if err != nil {
		log.Printf("[DIRECTOR] Failed to create DB-configured provider: %v", err)
		return nil
	}

	return provider
}
