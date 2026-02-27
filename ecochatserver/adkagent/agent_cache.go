package adkagent

import (
	"log"
	"sync"
	"time"
)

// syncCache — generic thread-safe cache with double-checked locking for getOrCreate.
// Supports optional TTL-based eviction to prevent unbounded memory growth.
type syncCache[T any] struct {
	mu         sync.RWMutex
	items      map[string]T
	lastAccess map[string]time.Time // трекинг последнего доступа для TTL eviction
	ttl        time.Duration        // 0 = без TTL (бесконечный кэш)
	name       string               // имя кэша для логирования
}

func newSyncCache[T any]() *syncCache[T] {
	return &syncCache[T]{
		items:      make(map[string]T),
		lastAccess: make(map[string]time.Time),
	}
}

// newSyncCacheWithTTL создаёт кэш с автоматической очисткой по TTL.
// evictInterval — как часто проверять на устаревшие записи.
func newSyncCacheWithTTL[T any](name string, ttl, evictInterval time.Duration) *syncCache[T] {
	c := &syncCache[T]{
		items:      make(map[string]T),
		lastAccess: make(map[string]time.Time),
		ttl:        ttl,
		name:       name,
	}

	// Фоновая горутина для периодической очистки
	go func() {
		ticker := time.NewTicker(evictInterval)
		defer ticker.Stop()
		for range ticker.C {
			evicted := c.evictStale()
			if evicted > 0 {
				log.Printf("[%s_CACHE] TTL eviction: удалено %d записей, осталось %d", c.name, evicted, c.len())
			}
		}
	}()

	return c
}

// get returns the value for key (if present) and updates last access time.
func (c *syncCache[T]) get(key string) (T, bool) {
	c.mu.RLock()
	v, ok := c.items[key]
	c.mu.RUnlock()

	if ok && c.ttl > 0 {
		c.mu.Lock()
		c.lastAccess[key] = time.Now()
		c.mu.Unlock()
	}

	return v, ok
}

// set stores a value under key.
func (c *syncCache[T]) set(key string, value T) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[key] = value
	c.lastAccess[key] = time.Now()
}

// getOrCreate returns existing value or creates one using create func.
// Uses double-checked locking to avoid redundant creation.
func (c *syncCache[T]) getOrCreate(key string, create func() (T, error)) (T, error) {
	// Fast path: read lock
	c.mu.RLock()
	if v, ok := c.items[key]; ok {
		c.mu.RUnlock()
		// Update access time outside read lock
		if c.ttl > 0 {
			c.mu.Lock()
			c.lastAccess[key] = time.Now()
			c.mu.Unlock()
		}
		return v, nil
	}
	c.mu.RUnlock()

	// Slow path: write lock
	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check after acquiring write lock
	if v, ok := c.items[key]; ok {
		c.lastAccess[key] = time.Now()
		return v, nil
	}

	v, err := create()
	if err != nil {
		var zero T
		return zero, err
	}
	c.items[key] = v
	c.lastAccess[key] = time.Now()
	return v, nil
}

// remove deletes the entry and returns true if it existed.
func (c *syncCache[T]) remove(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, existed := c.items[key]
	delete(c.items, key)
	delete(c.lastAccess, key)
	return existed
}

// clear removes all entries and returns how many were removed.
func (c *syncCache[T]) clear() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := len(c.items)
	c.items = make(map[string]T)
	c.lastAccess = make(map[string]time.Time)
	return n
}

// len returns the number of cached entries.
func (c *syncCache[T]) len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.items)
}

// keys returns all cached keys.
func (c *syncCache[T]) keys() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	keys := make([]string, 0, len(c.items))
	for k := range c.items {
		keys = append(keys, k)
	}
	return keys
}

// evictStale удаляет записи, к которым не обращались дольше TTL.
func (c *syncCache[T]) evictStale() int {
	if c.ttl == 0 {
		return 0
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	cutoff := time.Now().Add(-c.ttl)
	evicted := 0

	for key, lastAccess := range c.lastAccess {
		if lastAccess.Before(cutoff) {
			delete(c.items, key)
			delete(c.lastAccess, key)
			evicted++
		}
	}

	return evicted
}
