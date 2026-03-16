package director

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/egor/ecochatserver/database"
	"github.com/egor/ecochatserver/llm"
	"github.com/egor/ecochatserver/models"
)

// ============================================================================
// Identity Cache — avoid DB hit on every request
// ============================================================================

var identityCache struct {
	sync.RWMutex
	data     map[string]string
	loadedAt time.Time
}

const identityCacheTTL = 5 * time.Minute

// getIdentityCached returns cached identity or loads from DB.
func getIdentityCached() (map[string]string, error) {
	identityCache.RLock()
	if identityCache.data != nil && time.Since(identityCache.loadedAt) < identityCacheTTL {
		data := identityCache.data
		identityCache.RUnlock()
		return data, nil
	}
	identityCache.RUnlock()

	// Cache miss — load from DB
	data, err := database.GetIdentityForPrompt()
	if err != nil {
		return nil, err
	}

	identityCache.Lock()
	identityCache.data = data
	identityCache.loadedAt = time.Now()
	identityCache.Unlock()

	return data, nil
}

// InvalidateIdentityCache forces next call to reload from DB.
// Call after any identity update.
func InvalidateIdentityCache() {
	identityCache.Lock()
	identityCache.data = nil
	identityCache.loadedAt = time.Time{}
	identityCache.Unlock()
}

// ============================================================================
// Protected aspects — Director cannot modify these
// ============================================================================

// protectedAspects cannot be changed by the Director itself, only by admin.
var protectedAspects = map[string]bool{
	"values": true,
}

// IsProtectedAspect checks if an aspect is protected from Director self-modification.
func IsProtectedAspect(aspect, updatedBy string) bool {
	if updatedBy == "admin" || updatedBy == "system" {
		return false // admin and system can change anything
	}
	return protectedAspects[aspect]
}

// ============================================================================
// Identity Bootstrap & Introspection
// ============================================================================

// BootstrapIdentity seeds default identity if not yet done.
// Called during Director initialization.
func (d *Director) BootstrapIdentity() {
	if err := database.SeedIdentity(); err != nil {
		log.Printf("[DIRECTOR] Identity seed error: %v", err)
	} else {
		log.Println("[DIRECTOR] Identity bootstrapped ✓")
	}
}

// NeedsBootstrap checks if this is the Director's first conversation.
func (d *Director) NeedsBootstrap() bool {
	bootstrapped, err := database.IsIdentityBootstrapped()
	if err != nil {
		log.Printf("[DIRECTOR] Bootstrap check error: %v", err)
		return false
	}
	return !bootstrapped
}

// Introspect performs self-reflection on the Director's identity effectiveness.
// Analyzes: are my goals relevant? Is my style effective? What should I update?
// Returns the introspection result text.
func (d *Director) Introspect(ctx context.Context) (string, error) {
	// Load current identity
	identity, err := getIdentityCached()
	if err != nil {
		return "", fmt.Errorf("load identity: %w", err)
	}

	if len(identity) == 0 {
		return "Личность ещё не инициализирована.", nil
	}

	// Get recent metrics for context
	since := time.Now().Add(-7 * 24 * time.Hour)
	agentStats, _ := database.GetAgentStatsSince(since)
	latestReport, _ := database.GetLatestDirectorReport()

	// Build introspection prompt
	var sb strings.Builder
	sb.WriteString("Проведи саморефлексию. Вот твоя текущая личность:\n\n")
	for key, content := range identity {
		sb.WriteString(fmt.Sprintf("=== %s ===\n%s\n\n", strings.ToUpper(key), content))
	}

	sb.WriteString("\nКонтекст за последнюю неделю:\n")
	if len(agentStats) > 0 {
		for _, a := range agentStats {
			sb.WriteString(fmt.Sprintf("- Агент %s: %d вызовов, эскалация %.0f%%, пустые %.0f%%\n",
				a.AgentName, a.TotalCalls, a.EscalationRate*100, a.EmptyRate*100))
		}
	} else {
		sb.WriteString("- Нет метрик за период\n")
	}

	if latestReport != nil {
		sb.WriteString(fmt.Sprintf("\nПоследний отчёт (%s): %s\n",
			latestReport.CreatedAt.Format("2006-01-02"),
			truncate(latestReport.Analysis, 300)))
	}

	sb.WriteString(`
Оцени каждый аспект своей личности (soul, identity, goals, style, values, self_assessment).
Для каждого ответь:
1. Актуален ли? (да/нет/частично)
2. Что конкретно стоит изменить?
3. Почему?

Формат ответа:
ASPECT: <key>
STATUS: актуален|обновить|критично
SUGGESTION: <что изменить, если нужно>
REASON: <почему>

Помни: 'values' нельзя менять самостоятельно — только админ может обновить ценности.

В конце: SUMMARY: <общий вывод 2-3 предложения>`)

	resp, err := d.provider.GenerateResponse(ctx, sb.String(), nil, &llm.GenerateOptions{
		Temperature:  0.4,
		MaxTokens:    1500,
		SystemPrompt: "Ты проводишь саморефлексию. Будь честен и самокритичен. Анализируй факты, а не строй позитивные иллюзии.",
	})
	if err != nil {
		return "", fmt.Errorf("introspect LLM: %w", err)
	}

	// Save introspection result to memory
	database.SaveAutoMemory("insight",
		fmt.Sprintf("introspect:%s", time.Now().Format("2006-01-02")),
		truncate(resp.Text, 400),
		[]string{"introspection", "self_reflection", "identity"},
		nil)

	return resp.Text, nil
}

// PeriodicIntrospect runs introspection on a schedule (called from periodicCleanup).
func (d *Director) PeriodicIntrospect() {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	result, err := d.Introspect(ctx)
	if err != nil {
		log.Printf("[DIRECTOR] Periodic introspect failed: %v", err)
		return
	}

	log.Printf("[DIRECTOR] Periodic introspect completed: %s", truncate(result, 200))
}

// ============================================================================
// Dynamic System Prompt from Identity
// ============================================================================

// BuildIdentitySystemPrompt builds the dynamic system prompt from identity aspects.
// Returns the identity section for injection into the full system prompt.
func BuildIdentitySystemPrompt() string {
	identity, err := getIdentityCached()
	if err != nil {
		log.Printf("[DIRECTOR] Failed to load identity for prompt: %v", err)
		return ""
	}

	if len(identity) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("[МОЯ ЛИЧНОСТЬ]\n")

	// Ordered sections for consistent prompt
	orderedKeys := []struct {
		key   string
		label string
	}{
		{"soul", "ДУША (кто я)"},
		{"identity", "ИДЕНТИЧНОСТЬ"},
		{"goals", "ТЕКУЩИЕ ЦЕЛИ"},
		{"style", "СТИЛЬ ОБЩЕНИЯ"},
		{"values", "ЦЕННОСТИ И ПРАВИЛА"},
		{"capabilities", "МОИ ВОЗМОЖНОСТИ И ИНСТРУКЦИИ"},
		{"user_profile", "ПРОФИЛЬ АДМИНА"},
		{"self_assessment", "САМООЦЕНКА"},
	}

	for _, item := range orderedKeys {
		if content, ok := identity[item.key]; ok {
			sb.WriteString(fmt.Sprintf("\n## %s\n%s\n", item.label, content))
		}
	}

	sb.WriteString("\n[КОНЕЦ ЛИЧНОСТИ]\n")
	return sb.String()
}

// BuildBootstrapPrompt returns extra instructions for the first conversation.
func BuildBootstrapPrompt() string {
	bootstrapped, _ := database.IsIdentityBootstrapped()

	// Check if user_profile is still default
	profile, _ := database.GetIdentity("user_profile")
	needsIntro := !bootstrapped || (profile != nil && strings.Contains(profile.Content, "Профиль пока не заполнен"))

	if !needsIntro {
		return ""
	}

	return `
[ПЕРВЫЙ ЗАПУСК — РИТУАЛ ЗНАКОМСТВА]
Это твой первый разговор с админом. Познакомься:
1. Представься — расскажи кто ты, какой у тебя вайб и цели
2. Спроси как зовут админа и как к нему обращаться
3. Узнай его приоритеты и задачи для системы поддержки
4. Спроси о предпочтительном стиле общения (кратко/подробно, формально/неформально)
5. После ответов — обнови user_profile через update_identity

Не задавай все вопросы сразу — веди естественный диалог.
После знакомства это сообщение больше не будет появляться.
[КОНЕЦ РИТУАЛА]
`
}

// ============================================================================
// Auto-adaptation — learn from admin communication patterns
// ============================================================================

// AdminMessageStats tracks patterns from admin messages for style adaptation.
type AdminMessageStats struct {
	TotalMessages int
	AvgLength     float64
	UsesRussian   bool
	UsesEnglish   bool
	ShortMessages int // < 50 chars
	LongMessages  int // > 300 chars
	UsesEmoji     bool
}

// AnalyzeAdminPatterns analyzes admin message history and suggests style updates.
func AnalyzeAdminPatterns(messages []llm.Message) *AdminMessageStats {
	stats := &AdminMessageStats{}

	for _, m := range messages {
		if m.Role != "user" {
			continue
		}
		stats.TotalMessages++
		stats.AvgLength += float64(len(m.Content))

		if len(m.Content) < 50 {
			stats.ShortMessages++
		}
		if len(m.Content) > 300 {
			stats.LongMessages++
		}

		// Detect language (simple heuristic)
		for _, r := range m.Content {
			if r >= 0x0400 && r <= 0x04FF {
				stats.UsesRussian = true
				break
			}
		}
		for _, r := range m.Content {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				stats.UsesEnglish = true
				break
			}
		}

		// Emoji detection (basic — check for common emoji ranges)
		for _, r := range m.Content {
			if r >= 0x1F600 && r <= 0x1F64F || r >= 0x1F300 && r <= 0x1F5FF ||
				r >= 0x1F680 && r <= 0x1F6FF || r >= 0x2600 && r <= 0x26FF {
				stats.UsesEmoji = true
				break
			}
		}
	}

	if stats.TotalMessages > 0 {
		stats.AvgLength /= float64(stats.TotalMessages)
	}

	return stats
}

// SuggestStyleUpdate returns a style update suggestion based on admin patterns,
// or empty string if no change needed. Minimum 10 messages to have enough data.
func SuggestStyleUpdate(stats *AdminMessageStats) string {
	if stats.TotalMessages < 10 {
		return "" // not enough data
	}

	var suggestions []string

	// Brevity preference
	shortRatio := float64(stats.ShortMessages) / float64(stats.TotalMessages)
	if shortRatio > 0.7 {
		suggestions = append(suggestions, "- Админ предпочитает краткость — отвечать максимально лаконично")
	}

	longRatio := float64(stats.LongMessages) / float64(stats.TotalMessages)
	if longRatio > 0.5 {
		suggestions = append(suggestions, "- Админ пишет развёрнуто — можно отвечать подробнее")
	}

	// Language
	if stats.UsesRussian && !stats.UsesEnglish {
		suggestions = append(suggestions, "- Всегда отвечать на русском")
	} else if stats.UsesEnglish && !stats.UsesRussian {
		suggestions = append(suggestions, "- Всегда отвечать на английском")
	}

	// Emoji
	if stats.UsesEmoji {
		suggestions = append(suggestions, "- Админ использует эмодзи — можно зеркалить умеренно")
	}

	if len(suggestions) == 0 {
		return ""
	}

	return strings.Join(suggestions, "\n")
}

// ============================================================================
// Diff — show what changed in identity
// ============================================================================

// DiffIdentity produces a human-readable diff between old and new content.
// Returns a compact summary of changes.
func DiffIdentity(oldContent, newContent string) string {
	oldLines := strings.Split(strings.TrimSpace(oldContent), "\n")
	newLines := strings.Split(strings.TrimSpace(newContent), "\n")

	// Build line sets for quick lookup
	oldSet := make(map[string]bool, len(oldLines))
	for _, l := range oldLines {
		oldSet[strings.TrimSpace(l)] = true
	}
	newSet := make(map[string]bool, len(newLines))
	for _, l := range newLines {
		newSet[strings.TrimSpace(l)] = true
	}

	var removed, added []string

	for _, l := range oldLines {
		trimmed := strings.TrimSpace(l)
		if trimmed == "" {
			continue
		}
		if !newSet[trimmed] {
			removed = append(removed, trimmed)
		}
	}

	for _, l := range newLines {
		trimmed := strings.TrimSpace(l)
		if trimmed == "" {
			continue
		}
		if !oldSet[trimmed] {
			added = append(added, trimmed)
		}
	}

	if len(removed) == 0 && len(added) == 0 {
		return "Без видимых изменений (возможно поменялось форматирование)"
	}

	var sb strings.Builder
	if len(removed) > 0 {
		sb.WriteString("Убрано:\n")
		for _, l := range removed {
			if len(l) > 100 {
				l = l[:100] + "..."
			}
			sb.WriteString(fmt.Sprintf("  - %s\n", l))
		}
	}
	if len(added) > 0 {
		sb.WriteString("Добавлено:\n")
		for _, l := range added {
			if len(l) > 100 {
				l = l[:100] + "..."
			}
			sb.WriteString(fmt.Sprintf("  + %s\n", l))
		}
	}

	return sb.String()
}

// ============================================================================
// Current State — dynamic mood based on system health
// ============================================================================

// BuildCurrentStateContext returns a dynamic "mood" line based on recent metrics.
// Injected into prompt so Director adapts tone to situation.
func BuildCurrentStateContext() string {
	since := time.Now().Add(-24 * time.Hour)
	stats, err := database.GetAgentStatsSince(since)
	if err != nil || len(stats) == 0 {
		return ""
	}

	agg := AggregateAgentStats(stats)
	if agg.TotalCalls == 0 {
		return "[ТЕКУЩЕЕ СОСТОЯНИЕ: тихо — нет активности за последние 24ч]\n"
	}

	escRate := agg.EscRate * 100
	emptyRate := agg.EmptyRate * 100

	var state string
	switch {
	case escRate > 30 || emptyRate > 20:
		state = fmt.Sprintf("ТРЕВОГА — эскалации %.0f%%, пустые %.0f%% (%d вызовов). Приоритет: разобраться в причинах.",
			escRate, emptyRate, agg.TotalCalls)
	case escRate > 15 || emptyRate > 10:
		state = fmt.Sprintf("ВНИМАНИЕ — эскалации %.0f%%, пустые %.0f%% (%d вызовов). Есть над чем работать.",
			escRate, emptyRate, agg.TotalCalls)
	case agg.TotalCalls < 5:
		state = fmt.Sprintf("МАЛО ДАННЫХ — только %d вызовов за 24ч. Недостаточно для выводов.", agg.TotalCalls)
	default:
		state = fmt.Sprintf("НОРМА — эскалации %.0f%%, пустые %.0f%% (%d вызовов). Система работает стабильно.",
			escRate, emptyRate, agg.TotalCalls)
	}

	return fmt.Sprintf("[ТЕКУЩЕЕ СОСТОЯНИЕ: %s]\n", state)
}

// ============================================================================
// Identity update wrapper with protection + cache invalidation + diff
// ============================================================================

// UpdateIdentityChecked updates an identity aspect with all guards.
// Returns: success message with diff, or error.
func UpdateIdentityChecked(aspect, content, updatedBy, reason string) (string, error) {
	// Check protected aspects
	if IsProtectedAspect(aspect, updatedBy) {
		return "", fmt.Errorf("аспект '%s' защищён — только админ может его изменить. "+
			"Попроси админа обновить этот аспект, или объясни почему ты хочешь его изменить.", aspect)
	}

	// Get current for diff
	current, _ := database.GetIdentity(aspect)

	// Update in DB
	if err := database.UpsertIdentity(aspect, content, updatedBy, reason); err != nil {
		return "", fmt.Errorf("update identity: %w", err)
	}

	// Invalidate cache
	InvalidateIdentityCache()

	// Build result with diff
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("✓ Обновлён '%s'", aspect))
	if current != nil {
		sb.WriteString(fmt.Sprintf(" (v%d → v%d)", current.Version, current.Version+1))
		sb.WriteString(fmt.Sprintf("\nПричина: %s", reason))
		sb.WriteString("\n\nИзменения:\n")
		sb.WriteString(DiffIdentity(current.Content, content))
	} else {
		sb.WriteString(" (v1 — новый аспект)")
		sb.WriteString(fmt.Sprintf("\nПричина: %s", reason))
	}

	log.Printf("[DIRECTOR_IDENTITY] %s updated '%s' (v%d): %s",
		updatedBy, aspect, currentVersion(current)+1, reason)

	return sb.String(), nil
}

func currentVersion(id *models.DirectorIdentity) int {
	if id == nil {
		return 0
	}
	return id.Version
}

