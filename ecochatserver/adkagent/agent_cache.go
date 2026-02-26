package adkagent

import "sync"

// syncCache — generic thread-safe cache with double-checked locking for getOrCreate.
// Used to deduplicate agent/orchestrator/escalation caching logic.
type syncCache[T any] struct {
	mu    sync.RWMutex
	items map[string]T
}

func newSyncCache[T any]() *syncCache[T] {
	return &syncCache[T]{items: make(map[string]T)}
}

// get returns the value for key (if present).
func (c *syncCache[T]) get(key string) (T, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	v, ok := c.items[key]
	return v, ok
}

// set stores a value under key.
func (c *syncCache[T]) set(key string, value T) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[key] = value
}

// getOrCreate returns existing value or creates one using create func.
// Uses double-checked locking to avoid redundant creation.
func (c *syncCache[T]) getOrCreate(key string, create func() (T, error)) (T, error) {
	// Fast path: read lock
	c.mu.RLock()
	if v, ok := c.items[key]; ok {
		c.mu.RUnlock()
		return v, nil
	}
	c.mu.RUnlock()

	// Slow path: write lock
	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check after acquiring write lock
	if v, ok := c.items[key]; ok {
		return v, nil
	}

	v, err := create()
	if err != nil {
		var zero T
		return zero, err
	}
	c.items[key] = v
	return v, nil
}

// remove deletes the entry and returns true if it existed.
func (c *syncCache[T]) remove(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, existed := c.items[key]
	delete(c.items, key)
	return existed
}

// clear removes all entries and returns how many were removed.
func (c *syncCache[T]) clear() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := len(c.items)
	c.items = make(map[string]T)
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
