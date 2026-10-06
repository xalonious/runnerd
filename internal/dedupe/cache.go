package dedupe

import (
	"errors"
	"sync"
	"time"
)

var ErrFull = errors.New("deduplication cache full")

type Cache struct {
	mu       sync.Mutex
	entries  map[string]time.Time
	ttl      time.Duration
	capacity int
	now      func() time.Time
}

func New(ttl time.Duration, capacity int) *Cache {
	return &Cache{entries: make(map[string]time.Time), ttl: ttl, capacity: capacity, now: time.Now}
}

func (c *Cache) Commit(id string, enqueue func() error, relatedIDs ...string) (duplicate bool, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	keys := make(map[string]struct{}, 1+len(relatedIDs))
	keys[id] = struct{}{}
	for _, related := range relatedIDs {
		keys[related] = struct{}{}
	}
	for key := range keys {
		if expires, ok := c.entries[key]; ok && now.Before(expires) {
			return true, nil
		}
	}
	if len(c.entries)+len(keys) > c.capacity {
		c.cleanup(now)
	}
	additional := 0
	for key := range keys {
		if _, exists := c.entries[key]; !exists {
			additional++
		}
	}
	if len(c.entries)+additional > c.capacity {
		return false, ErrFull
	}
	if err := enqueue(); err != nil {
		return false, err
	}
	for key := range keys {
		c.entries[key] = now.Add(c.ttl)
	}
	return false, nil
}

func (c *Cache) Cleanup() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cleanup(c.now())
}

func (c *Cache) cleanup(now time.Time) {
	for id, expires := range c.entries {
		if !now.Before(expires) {
			delete(c.entries, id)
		}
	}
}
