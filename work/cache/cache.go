package cache

import (
	"sync"
	"time"
)

// Cache provides a thread-safe in-memory cache with time-based expiration.
// It maintains separate cache stores for M3U8 data and channel data, and
// supports automatic periodic clearing of expired entries.
type Cache struct {
	m3u8Cache    map[string]cacheEntry // Cache for M3U8 playlist data, keyed by identifier
	channelCache map[string]cacheEntry // Cache for channel metadata, keyed by identifier
	mu           sync.RWMutex          // Read-write mutex for concurrent safe access
	duration     time.Duration         // Expiration duration for each cache entry
	lastClear    time.Time             // Timestamp when the cache was last fully cleared
}

// cacheEntry represents a single cached item with its data and creation timestamp.
type cacheEntry struct {
	data      any       // The cached data payload (string for M3U8, struct for channels, etc.)
	timestamp time.Time // When this entry was inserted into the cache
}

// NewCache creates and returns a new Cache instance with the specified expiration duration.
// The cache is initialized with empty stores and is ready for immediate use.
//
// Parameters:
//   - duration: how long entries are considered valid before expiring
//
// Returns:
//   - *Cache: pointer to a new Cache object
func NewCache(duration time.Duration) *Cache {
	return &Cache{
		m3u8Cache:    make(map[string]cacheEntry), // initialize empty M3U8 cache
		channelCache: make(map[string]cacheEntry), // initialize empty channel cache
		duration:     duration,                    // set expiration duration
		lastClear:    time.Now(),                  // record initialization time
	}
}

// GetM3U8 retrieves an M3U8 playlist from the cache by key.
//
// Behavior:
//   - If the key exists and the entry has not expired → returns the cached string and true.
//   - If the key is missing or expired → returns empty string and false.
//
// Parameters:
//   - key: identifier for the cached M3U8 playlist
//
// Returns:
//   - string: cached M3U8 playlist contents (if valid)
//   - bool: true if entry exists and is not expired
func (c *Cache) GetM3U8(key string) (string, bool) {

	// acquire read lock for safe concurrent access and release it on return
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, exists := c.m3u8Cache[key]

	// Validate existence and expiration
	if !exists || time.Since(entry.timestamp) > c.duration {
		return "", false
	}

	// Safe to return as string since SetM3U8 enforces type
	return entry.data.(string), true
}

// SetM3U8 stores an M3U8 playlist in the cache with the specified key.
// The entry is stamped with the current time for expiration tracking.
//
// Parameters:
//   - key: identifier for the playlist
//   - value: the M3U8 playlist content to cache
func (c *Cache) SetM3U8(key, value string) {

	// acquire write lock for mutation and ensure its released
	c.mu.Lock()
	defer c.mu.Unlock()

	// Insert/overwrite entry with current timestamp
	c.m3u8Cache[key] = cacheEntry{
		data:      value,
		timestamp: time.Now(),
	}
}

// ClearIfNeeded performs periodic cache clearance if the configured duration has elapsed
// since the last clearance. This method completely clears both cache stores and
// resets the last clearance timestamp.
//
// It is thread-safe and ensures no race conditions with concurrent readers/writers.
func (c *Cache) ClearIfNeeded() {

	// acquire write lock since we are resetting state and release it
	c.mu.Lock()
	defer c.mu.Unlock()

	// If enough time has passed since last clear, reset the caches
	if time.Since(c.lastClear) > c.duration {

		// Replace caches with fresh empty maps
		c.m3u8Cache = make(map[string]cacheEntry)
		c.channelCache = make(map[string]cacheEntry)

		// Update lastClear timestamp
		c.lastClear = time.Now()
	}
}
