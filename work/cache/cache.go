// work/cache/cache.go
package cache

import (
	"hash/fnv"
	"time"

	"github.com/dgraph-io/ristretto/v2"
)

// Cache provides a thread-safe in-memory cache with time-based expiration using Ristretto.
type Cache struct {
	cache    *ristretto.Cache[uint64, string]
	duration time.Duration
}

// hashKey converts string keys to uint64 hashes for more efficient cache lookups
func hashKey(key string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(key))
	return h.Sum64()
}

// NewCache creates and returns a new Cache instance with the specified expiration duration.
func NewCache(duration time.Duration) *Cache {
	cache, err := ristretto.NewCache(&ristretto.Config[uint64, string]{
		NumCounters: 1e7,     // number of keys to track frequency of (10M)
		MaxCost:     1 << 30, // maximum cost of cache (1GB)
		BufferItems: 64,      // number of keys per Get buffer
	})
	if err != nil {
		panic(err)
	}

	return &Cache{
		cache:    cache,
		duration: duration,
	}
}

// GetM3U8 retrieves an EPG from the cache by key.
func (c *Cache) GetEPG(key string) (string, bool) {
	value, found := c.cache.Get(hashKey(key))
	return value, found
}

// stores an EPG in the cache with the specified key.
func (c *Cache) SetEPG(key, value string) {
	c.cache.SetWithTTL(hashKey(key), value, int64(len(value)), c.duration)
}

// GetM3U8 retrieves an M3U8 playlist from the cache by key.
func (c *Cache) GetM3U8(key string) (string, bool) {
	value, found := c.cache.Get(hashKey(key))
	return value, found
}

// SetM3U8 stores an M3U8 playlist in the cache with the specified key.
func (c *Cache) SetM3U8(key, value string) {
	c.cache.SetWithTTL(hashKey(key), value, int64(len(value)), c.duration)
}

// GetXCData retrieves XC API response data from cache
func (c *Cache) GetXCData(key string) (string, bool) {
	value, found := c.cache.Get(hashKey(key))
	return value, found
}

// SetXCData stores XC API response data in cache
func (c *Cache) SetXCData(key, value string) {
	c.cache.SetWithTTL(hashKey(key), value, int64(len(value)), c.duration)
}

// ClearIfNeeded performs cache clearance - with Ristretto this is handled automatically by TTL
func (c *Cache) ClearIfNeeded() {
	// Ristretto handles expiration automatically via TTL
	// This method is kept for API compatibility
}

// Close closes the cache and frees resources
func (c *Cache) Close() {
	c.cache.Close()
}
