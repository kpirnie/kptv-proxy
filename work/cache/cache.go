package cache

import (
	"sync"
	"time"
)

// Cache manages cached data
type Cache struct {
	m3u8Cache    map[string]cacheEntry
	channelCache map[string]cacheEntry
	mu           sync.RWMutex
	duration     time.Duration
	lastClear    time.Time
}

type cacheEntry struct {
	data      any
	timestamp time.Time
}

// NewCache creates a new Cache instance with the specified duration
func NewCache(duration time.Duration) *Cache {
	return &Cache{
		m3u8Cache:    make(map[string]cacheEntry),
		channelCache: make(map[string]cacheEntry),
		duration:     duration,
		lastClear:    time.Now(),
	}
}

// Cache methods
func (c *Cache) GetM3U8(key string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, exists := c.m3u8Cache[key]
	if !exists || time.Since(entry.timestamp) > c.duration {
		return "", false
	}

	return entry.data.(string), true
}

func (c *Cache) SetM3U8(key, value string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.m3u8Cache[key] = cacheEntry{
		data:      value,
		timestamp: time.Now(),
	}
}

func (c *Cache) ClearIfNeeded() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if time.Since(c.lastClear) > c.duration {
		c.m3u8Cache = make(map[string]cacheEntry)
		c.channelCache = make(map[string]cacheEntry)
		c.lastClear = time.Now()
	}
}
