package adkagent

import (
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Default debounce timeout: wait this long after the last message before responding.
const defaultDebounceTimeout = 30 * time.Second

// debounceEntry tracks a pending debounce for a single chat.
type debounceEntry struct {
	cancel    chan struct{} // closed by newer message to cancel this waiter
	chatID    uuid.UUID
	createdAt time.Time
}

// messageDebouncer coordinates message batching per chat.
// When multiple messages arrive in quick succession, only the last waiter
// actually triggers processing — all earlier ones return nil.
type messageDebouncer struct {
	mu      sync.Mutex
	pending map[string]*debounceEntry // chatKey → entry
	timeout time.Duration
}

func newMessageDebouncer(timeout time.Duration) *messageDebouncer {
	if timeout <= 0 {
		timeout = defaultDebounceTimeout
	}
	return &messageDebouncer{
		pending: make(map[string]*debounceEntry),
		timeout: timeout,
	}
}

// Wait registers a new message for the chat and waits for the debounce period.
// Returns true if this waiter should proceed with processing (it's the last one).
// Returns false if a newer message arrived and this waiter should return nil.
func (d *messageDebouncer) Wait(chatID uuid.UUID) bool {
	chatKey := chatID.String()
	cancel := make(chan struct{})

	d.mu.Lock()
	// Cancel any existing waiter for this chat
	if existing, ok := d.pending[chatKey]; ok {
		close(existing.cancel)
		log.Printf("[DEBOUNCE] Chat %s: new message arrived, resetting timer", chatKey[:8])
	}
	d.pending[chatKey] = &debounceEntry{
		cancel:    cancel,
		chatID:    chatID,
		createdAt: time.Now(),
	}
	d.mu.Unlock()

	// Wait for timeout or cancellation
	select {
	case <-cancel:
		// Cancelled by a newer message — this waiter should NOT process
		return false
	case <-time.After(d.timeout):
		// Debounce period elapsed — check we're still the active waiter
		d.mu.Lock()
		current, ok := d.pending[chatKey]
		isActive := ok && current.cancel == cancel
		if isActive {
			delete(d.pending, chatKey)
		}
		d.mu.Unlock()

		if !isActive {
			// Another waiter took over (race condition edge case)
			return false
		}

		log.Printf("[DEBOUNCE] Chat %s: debounce complete (%v), proceeding", chatKey[:8], d.timeout)
		return true
	}
}

// Cancel removes any pending debounce for a chat (e.g., when chat is assigned to admin).
func (d *messageDebouncer) Cancel(chatID uuid.UUID) {
	chatKey := chatID.String()
	d.mu.Lock()
	if existing, ok := d.pending[chatKey]; ok {
		close(existing.cancel)
		delete(d.pending, chatKey)
	}
	d.mu.Unlock()
}

// PendingCount returns the number of chats currently being debounced.
func (d *messageDebouncer) PendingCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.pending)
}
