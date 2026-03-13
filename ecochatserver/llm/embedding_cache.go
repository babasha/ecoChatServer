package llm

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ============================================================================
// Embedding Cache — two-level cache for embedding vectors
// L1: in-memory (sync.Map, up to 500 entries, 1h TTL)
// L2: PostgreSQL (embedding_cache table, 30d TTL)
// ============================================================================

const (
	l1MaxEntries = 500
	l1TTL        = 1 * time.Hour
	l2TTL        = 30 * 24 * time.Hour // 30 days
)

// EmbeddingCache provides L1 (in-memory) + L2 (PostgreSQL) caching for embeddings.
type EmbeddingCache struct {
	db       *sql.DB
	l1       sync.Map       // hash → *cacheEntry
	l1Count  atomic.Int64   // approximate L1 size
	provider string
	model    string
	hits     atomic.Int64
	misses   atomic.Int64
	l1Hits   atomic.Int64
	l2Hits   atomic.Int64
}

type cacheEntry struct {
	embedding []float64
	expiresAt time.Time
}

// CacheStats holds cache performance metrics.
type CacheStats struct {
	Hits     int64 `json:"hits"`
	Misses   int64 `json:"misses"`
	L1Hits   int64 `json:"l1_hits"`
	L2Hits   int64 `json:"l2_hits"`
	L1Count  int64 `json:"l1_count"`
	HitRate  float64 `json:"hit_rate"`
}

var collapseSpaces = regexp.MustCompile(`\s+`)

// NewEmbeddingCache creates a new two-level embedding cache.
func NewEmbeddingCache(db *sql.DB, provider, model string) *EmbeddingCache {
	return &EmbeddingCache{
		db:       db,
		provider: provider,
		model:    model,
	}
}

// normalizeText trims, collapses whitespace, and lowercases text.
func normalizeText(text string) string {
	text = strings.TrimSpace(text)
	text = collapseSpaces.ReplaceAllString(text, " ")
	text = strings.ToLower(text)
	return text
}

// hashText returns SHA-256 hex of normalized text.
func hashText(normalized string) string {
	h := sha256.Sum256([]byte(normalized))
	return fmt.Sprintf("%x", h)
}

// Get looks up an embedding: L1 → L2 → miss.
func (c *EmbeddingCache) Get(ctx context.Context, text string) ([]float64, bool) {
	normalized := normalizeText(text)
	hash := hashText(normalized)

	// L1 lookup
	if val, ok := c.l1.Load(hash); ok {
		entry := val.(*cacheEntry)
		if time.Now().Before(entry.expiresAt) {
			c.hits.Add(1)
			c.l1Hits.Add(1)
			log.Printf("[EMBED_CACHE] HIT L1 hash=%s…", hash[:12])
			return entry.embedding, true
		}
		// expired — remove
		c.l1.Delete(hash)
		c.l1Count.Add(-1)
	}

	// L2 lookup
	emb, ok := c.l2Get(ctx, hash)
	if ok {
		c.hits.Add(1)
		c.l2Hits.Add(1)
		// promote to L1
		c.l1Store(hash, emb)
		log.Printf("[EMBED_CACHE] HIT L2 hash=%s…", hash[:12])
		return emb, true
	}

	c.misses.Add(1)
	log.Printf("[EMBED_CACHE] MISS hash=%s…", hash[:12])
	return nil, false
}

// Put stores an embedding in both L1 and L2 (write-through).
func (c *EmbeddingCache) Put(ctx context.Context, text string, embedding []float64) {
	normalized := normalizeText(text)
	hash := hashText(normalized)

	// L1 store
	c.l1Store(hash, embedding)

	// L2 store (async-ish, but within same context)
	preview := normalized
	if len(preview) > 100 {
		preview = preview[:100]
	}
	c.l2Put(ctx, hash, embedding, preview)
}

// GetBatch checks cache for multiple texts.
// Returns: cached embeddings (by index) and list of uncached indices.
func (c *EmbeddingCache) GetBatch(ctx context.Context, texts []string) (map[int][]float64, []int) {
	cached := make(map[int][]float64)
	var uncached []int

	// Collect hashes for batch L2 lookup
	type indexedHash struct {
		idx        int
		hash       string
		normalized string
	}
	var needL2 []indexedHash

	for i, text := range texts {
		normalized := normalizeText(text)
		hash := hashText(normalized)

		// L1 lookup
		if val, ok := c.l1.Load(hash); ok {
			entry := val.(*cacheEntry)
			if time.Now().Before(entry.expiresAt) {
				c.hits.Add(1)
				c.l1Hits.Add(1)
				cached[i] = entry.embedding
				continue
			}
			c.l1.Delete(hash)
			c.l1Count.Add(-1)
		}
		needL2 = append(needL2, indexedHash{idx: i, hash: hash, normalized: normalized})
	}

	if len(needL2) == 0 {
		return cached, uncached
	}

	// Batch L2 lookup
	hashes := make([]string, len(needL2))
	for i, ih := range needL2 {
		hashes[i] = ih.hash
	}
	l2Results := c.l2GetBatch(ctx, hashes)

	for _, ih := range needL2 {
		if emb, ok := l2Results[ih.hash]; ok {
			c.hits.Add(1)
			c.l2Hits.Add(1)
			cached[ih.idx] = emb
			c.l1Store(ih.hash, emb)
		} else {
			c.misses.Add(1)
			uncached = append(uncached, ih.idx)
		}
	}

	return cached, uncached
}

// PutBatch stores multiple embeddings in cache (write-through).
func (c *EmbeddingCache) PutBatch(ctx context.Context, texts []string, embeddings [][]float64) {
	for i, text := range texts {
		if i < len(embeddings) && embeddings[i] != nil {
			c.Put(ctx, text, embeddings[i])
		}
	}
}

// Stats returns current cache statistics.
func (c *EmbeddingCache) Stats() CacheStats {
	hits := c.hits.Load()
	misses := c.misses.Load()
	total := hits + misses
	var hitRate float64
	if total > 0 {
		hitRate = float64(hits) / float64(total)
	}
	return CacheStats{
		Hits:    hits,
		Misses:  misses,
		L1Hits:  c.l1Hits.Load(),
		L2Hits:  c.l2Hits.Load(),
		L1Count: c.l1Count.Load(),
		HitRate: hitRate,
	}
}

// Cleanup removes expired entries from L1 and old entries from L2.
func (c *EmbeddingCache) Cleanup(ctx context.Context) {
	// L1 cleanup: remove expired entries
	now := time.Now()
	var l1Removed int64
	c.l1.Range(func(key, value any) bool {
		entry := value.(*cacheEntry)
		if now.After(entry.expiresAt) {
			c.l1.Delete(key)
			l1Removed++
		}
		return true
	})
	if l1Removed > 0 {
		c.l1Count.Add(-l1Removed)
	}

	// L2 cleanup: remove entries older than 30 days
	_, err := c.db.ExecContext(ctx,
		`DELETE FROM embedding_cache WHERE last_used < NOW() - INTERVAL '30 days'`)
	if err != nil {
		log.Printf("[EMBED_CACHE] L2 cleanup error: %v", err)
		return
	}

	stats := c.Stats()
	log.Printf("[EMBED_CACHE] Cleanup done: L1 removed=%d, L1 count=%d, hits=%d, misses=%d, hit_rate=%.1f%%",
		l1Removed, stats.L1Count, stats.Hits, stats.Misses, stats.HitRate*100)
}

// ── Internal helpers ─────────────────────────────────────────────────────────

// l1Store adds an entry to L1 cache, evicting oldest if over limit.
func (c *EmbeddingCache) l1Store(hash string, embedding []float64) {
	// Check if already exists (update)
	if _, loaded := c.l1.Load(hash); loaded {
		c.l1.Store(hash, &cacheEntry{
			embedding: embedding,
			expiresAt: time.Now().Add(l1TTL),
		})
		return
	}

	// Evict if over limit
	if c.l1Count.Load() >= l1MaxEntries {
		c.l1EvictOne()
	}

	c.l1.Store(hash, &cacheEntry{
		embedding: embedding,
		expiresAt: time.Now().Add(l1TTL),
	})
	c.l1Count.Add(1)
}

// l1EvictOne removes one entry from L1 (earliest expiring).
func (c *EmbeddingCache) l1EvictOne() {
	var oldestKey any
	var oldestTime time.Time
	first := true

	c.l1.Range(func(key, value any) bool {
		entry := value.(*cacheEntry)
		if first || entry.expiresAt.Before(oldestTime) {
			oldestKey = key
			oldestTime = entry.expiresAt
			first = false
		}
		return true
	})

	if oldestKey != nil {
		c.l1.Delete(oldestKey)
		c.l1Count.Add(-1)
	}
}

// l2Get looks up an embedding in PostgreSQL and bumps hit_count/last_used.
func (c *EmbeddingCache) l2Get(ctx context.Context, hash string) ([]float64, bool) {
	var raw []byte
	err := c.db.QueryRowContext(ctx, `
		UPDATE embedding_cache
		SET hit_count = hit_count + 1, last_used = NOW()
		WHERE hash = $1 AND provider = $2 AND model = $3
		RETURNING embedding::text`, hash, c.provider, c.model).Scan(&raw)
	if err != nil {
		return nil, false
	}

	emb, err := parseFloat8Array(raw)
	if err != nil {
		log.Printf("[EMBED_CACHE] L2 parse error: %v", err)
		return nil, false
	}
	return emb, true
}

// l2Put upserts an embedding into PostgreSQL.
func (c *EmbeddingCache) l2Put(ctx context.Context, hash string, embedding []float64, textPreview string) {
	arrStr := float64SliceToSQL(embedding)
	_, err := c.db.ExecContext(ctx, `
		INSERT INTO embedding_cache (hash, provider, model, dim, embedding, text_preview, hit_count, created_at, last_used)
		VALUES ($1, $2, $3, $4, $5::float8[], $6, 0, NOW(), NOW())
		ON CONFLICT (hash, provider, model)
		DO UPDATE SET embedding = EXCLUDED.embedding, text_preview = EXCLUDED.text_preview, last_used = NOW()`,
		hash, c.provider, c.model, len(embedding), arrStr, textPreview)
	if err != nil {
		log.Printf("[EMBED_CACHE] L2 put error: %v", err)
	}
}

// l2GetBatch retrieves multiple embeddings from PostgreSQL by hashes.
func (c *EmbeddingCache) l2GetBatch(ctx context.Context, hashes []string) map[string][]float64 {
	result := make(map[string][]float64)
	if len(hashes) == 0 {
		return result
	}

	// Build placeholders: $3, $4, $5, ...
	placeholders := make([]string, len(hashes))
	args := make([]any, 0, len(hashes)+2)
	args = append(args, c.provider, c.model)
	for i, h := range hashes {
		placeholders[i] = fmt.Sprintf("$%d", i+3)
		args = append(args, h)
	}

	query := fmt.Sprintf(`
		UPDATE embedding_cache
		SET hit_count = hit_count + 1, last_used = NOW()
		WHERE provider = $1 AND model = $2 AND hash IN (%s)
		RETURNING hash, embedding::text`,
		strings.Join(placeholders, ","))

	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		log.Printf("[EMBED_CACHE] L2 batch get error: %v", err)
		return result
	}
	defer rows.Close()

	for rows.Next() {
		var hash string
		var raw []byte
		if err := rows.Scan(&hash, &raw); err != nil {
			continue
		}
		emb, err := parseFloat8Array(raw)
		if err != nil {
			continue
		}
		result[hash] = emb
	}

	return result
}

// ── SQL array helpers ────────────────────────────────────────────────────────

// float64SliceToSQL converts []float64 to PostgreSQL array literal: '{1.0,2.0,...}'.
func float64SliceToSQL(v []float64) string {
	if len(v) == 0 {
		return "'{}'"
	}
	var b strings.Builder
	b.WriteString("{")
	for i, f := range v {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, "%g", f)
	}
	b.WriteString("}")
	return b.String()
}

// parseFloat8Array parses PostgreSQL float8[] text representation: {1.0,2.0,...}.
func parseFloat8Array(raw []byte) ([]float64, error) {
	s := strings.TrimSpace(string(raw))
	if s == "{}" || s == "" {
		return nil, fmt.Errorf("empty array")
	}

	// Strip braces
	s = strings.TrimPrefix(s, "{")
	s = strings.TrimSuffix(s, "}")

	parts := strings.Split(s, ",")
	result := make([]float64, len(parts))
	for i, p := range parts {
		var f float64
		_, err := fmt.Sscanf(strings.TrimSpace(p), "%g", &f)
		if err != nil {
			return nil, fmt.Errorf("parse float at index %d: %w", i, err)
		}
		result[i] = f
	}
	return result, nil
}
