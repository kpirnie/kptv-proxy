package cache

import (
	"fmt"
	"hash/fnv"
	"io"
	"kptv-proxy/work/logger"
	"os"
	"path/filepath"
	"time"

	"github.com/maypok86/otter/v2"
)

// hold the structure of the cache
type Cache struct {
	cache    *otter.Cache[string, string]
	duration time.Duration
	epg      *epgStore
}

// --------------------- EPG CACHING ---------------------

// hold the epg cache structure
type epgStore struct {
	dir string
	ttl time.Duration
}

// setup the EPG storage
func newEPGStore(dir string, ttl time.Duration) (*epgStore, error) {

	// try to create the EPG cache directory
	if err := os.MkdirAll(dir, 0755); err != nil {
		logger.Error("{cache(epg) - newEPGStore} failed to create EPG store directory: %v", err)
		return nil, err
	}
	logger.Debug("{cache(epg) - newEPGStore} create epg cache store")

	// return it
	return &epgStore{dir: dir, ttl: ttl}, nil
}

// create the full hashed file path for a given EPG key
func (e *epgStore) path(key string) string {
	return filepath.Join(e.dir, fmt.Sprintf("%d.xml", hashKey(key)))
}

// set writes raw EPG XML to disk using atomic temp+rename
func (e *epgStore) set(key, value string) error {

	// build target path — path() handles the hashing
	target := e.path(key)

	// try to create the temp file path and set its name
	tmp, err := os.CreateTemp(e.dir, "epg-*.tmp")
	if err != nil {
		logger.Error("{cache(epg) - set} create temp: %v", err)
		return err
	}
	tmpName := tmp.Name()

	// write the XML data to the temp file
	if _, err := io.WriteString(tmp, value); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		logger.Error("{cache(epg) - set} write temp: %v", err)
		return err
	}

	// close the temp file before renaming
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		logger.Error("{cache(epg) - set} close temp: %v", err)
		return err
	}

	// atomic rename into place
	if err := os.Rename(tmpName, target); err != nil {
		os.Remove(tmpName)
		logger.Error("{cache(epg) - set} rename: %v", err)
		return err
	}

	// debug logging
	logger.Debug("{cache(epg) - set} set epg to cache")

	// dont return anything
	return nil
}

// get checks the file's mod time against the TTL. If valid, returns an open
// file handle and its size. The caller must close the returned file.
func (e *epgStore) get(key string) (*os.File, int64, bool) {

	// build target path — path() handles the hashing
	target := e.path(key)

	// stat the file to check existence and mod time
	info, err := os.Stat(target)
	if err != nil {
		logger.Error("{cache(epg) - get} doesnt exist: %v", err)
		return nil, 0, false
	}

	// check if the cached file has expired
	if time.Since(info.ModTime()) > e.ttl {
		// dont bother logging here
		return nil, 0, false
	}

	// open and return the file handle
	f, err := os.Open(target)
	if err != nil {
		logger.Error("{cache(epg) - get} cannot open: %v", err)
		return nil, 0, false
	}

	// debug logging
	logger.Debug("{cache(epg) - get} got epg from cache")

	// return the file
	return f, info.Size(), true
}

// remainingTTL returns how many seconds remain before the cached EPG expires.
// Returns 0 if the file doesn't exist or is already expired.
func (e *epgStore) remainingTTL(key string) int {

	// stat the file to check mod time
	target := e.path(key)

	// check the file status
	info, err := os.Stat(target)
	if err != nil {
		return 0
	}

	// calculate remaining time
	remaining := e.ttl - time.Since(info.ModTime())
	if remaining <= 0 {
		return 0
	}

	// return the number of seconds remaining
	return int(remaining.Seconds())
}

// GetEPGFile returns a file handle and size for the cached EPG data.
// The caller MUST close the returned file. Returns false if no valid
// cached data exists.
func (c *Cache) GetEPGFile(key string) (*os.File, int64, bool) {
	return c.epg.get(key)
}

// SetEPG writes EPG XML data to disk.
func (c *Cache) SetEPG(key, value string) {
	if err := c.epg.set(key, value); err != nil {
		logger.Error("{cache(epg) - SetEPG} Failed to write EPG to disk: %v", err)
	}

}

// EPGRemainingTTL returns seconds remaining before the EPG cache expires.
func (c *Cache) EPGRemainingTTL(key string) int {
	return c.epg.remainingTTL(key)
}

// WarmUpEPG runs the fetch function in a background goroutine and writes
// the result to disk.
func (c *Cache) WarmUpEPG(fetchFunc func() string) {
	go func() {
		data := fetchFunc()
		if data != "" {
			c.SetEPG("merged", data)
		}
	}()
}

// GetEPG reads the full EPG from disk into a string
// for streaming to HTTP responses.
func (c *Cache) GetEPG(key string) (string, bool) {
	f, _, ok := c.epg.get(key)
	if !ok {
		return "", false
	}
	defer f.Close()

	// read in the epg
	data, err := io.ReadAll(f)
	if err != nil {
		logger.Error("{cache(epg) - GetEPG} Failed to read EPG from disk: %v", err)
		return "", false
	}

	// debug logging
	logger.Debug("{cache(epg) - GetEPG} Get the epg")

	// return the epg
	return string(data), true
}

// --------------------- M3U/XC CACHING ---------------------

// NewCache creates and returns a new Cache instance backed by otter for
// small entries (M3U8/XC) and disk for EPG data.
func NewCache(duration time.Duration) (*Cache, error) {

	// create the otter cache with write-based expiry
	c := otter.Must(&otter.Options[string, string]{
		MaximumSize:      10_000,
		ExpiryCalculator: otter.ExpiryWriting[string, string](duration),
	})

	// create the disk-backed EPG store
	epg, err := newEPGStore("/tmp/kptv-epg", 12*time.Hour)
	if err != nil {
		logger.Error("{cache - NewCache} failed to create EPG store: %v", err)
		return nil, err
	}
	logger.Debug("{cache - NewCache} creating the cache")

	// return the cache object
	return &Cache{
		cache:    c,
		duration: duration,
		epg:      epg,
	}, nil
}

// GetM3U8 retrieves an M3U8 playlist from the in-memory cache.
func (c *Cache) GetM3U8(key string) (string, bool) {
	logger.Debug("{cache - GetM3U8} get the cached m3u8")
	value, ok := c.cache.GetIfPresent(fmt.Sprintf("%d", hashKey(key)))
	return value, ok
}

// SetM3U8 stores an M3U8 playlist in the in-memory cache.
func (c *Cache) SetM3U8(key, value string) {
	logger.Debug("{cache - SetM3U8} set m3u8 to cache")
	c.cache.Set(fmt.Sprintf("%d", hashKey(key)), value)
}

// GetXCData retrieves XC API response data from the in-memory cache.
func (c *Cache) GetXCData(key string) (string, bool) {
	logger.Debug("{cache - GetXCData} get the cached xtream code data")
	value, ok := c.cache.GetIfPresent(fmt.Sprintf("%d", hashKey(key)))
	return value, ok
}

// SetXCData stores XC API response data in the in-memory cache.
func (c *Cache) SetXCData(key, value string) {
	logger.Debug("{cache - SetXCData} set the xtream code data to cache")
	c.cache.Set(fmt.Sprintf("%d", hashKey(key)), value)
}

// ClearIfNeeded is kept for API compatibility. Otter handles eviction automatically.
func (c *Cache) ClearIfNeeded() {
	logger.Debug("{cache - ClearIfNeeded} clear the cache")
	// clear the cache and finish pending operations...
	c.cache.InvalidateAll()
	c.cache.CleanUp()
}

// Close is kept for API compatibility.
func (c *Cache) Close() {
	logger.Debug("{cache - Close} close the cache")

	// finish up pending operations, and stop all cache GO routines
	c.cache.CleanUp()
	c.cache.StopAllGoroutines()
}

// hashKey converts string keys to uint64 hashes for more efficient cache lookups
func hashKey(key string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(key))
	return h.Sum64()
}
