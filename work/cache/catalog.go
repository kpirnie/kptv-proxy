package cache

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"kptv-proxy/work/logger"
	"os"
	"path/filepath"
	"time"
)

// --------------------- CATALOG CACHING ---------------------

// hold the catalog cache structure
type catalogStore struct {
	dir string
	ttl time.Duration
}

// setup the catalog storage
func newCatalogStore(dir string, ttl time.Duration) (*catalogStore, error) {

	// try to create the catalog cache directory
	if err := os.MkdirAll(dir, 0755); err != nil {
		logger.Error("{cache(catalog) - newCatalogStore} failed to create catalog store directory: %v", err)
		return nil, err
	}
	logger.Debug("{cache(catalog) - newCatalogStore} create catalog cache store")

	// return it
	return &catalogStore{dir: dir, ttl: ttl}, nil
}

// create the full hashed file path for a given catalog key
func (s *catalogStore) path(key string) string {
	return filepath.Join(s.dir, fmt.Sprintf("%s.json.gz", hashKey(key)))
}

// set writes a gzipped catalog payload to disk using atomic temp+rename
func (s *catalogStore) set(key, value string) error {

	// build target path — path() handles the hashing
	target := s.path(key)

	// try to create the temp file path and set its name
	tmp, err := os.CreateTemp(s.dir, "catalog-*.tmp")
	if err != nil {
		logger.Error("{cache(catalog) - set} create temp: %v", err)
		return err
	}
	tmpName := tmp.Name()

	// compress the payload straight into the temp file
	zw := gzip.NewWriter(tmp)
	if _, err := io.WriteString(zw, value); err != nil {
		zw.Close()
		tmp.Close()
		os.Remove(tmpName)
		logger.Error("{cache(catalog) - set} write temp: %v", err)
		return err
	}

	// finish the gzip stream before closing the file
	if err := zw.Close(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		logger.Error("{cache(catalog) - set} close gzip: %v", err)
		return err
	}

	// close the temp file before renaming
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		logger.Error("{cache(catalog) - set} close temp: %v", err)
		return err
	}

	// atomic rename into place
	if err := os.Rename(tmpName, target); err != nil {
		os.Remove(tmpName)
		logger.Error("{cache(catalog) - set} rename: %v", err)
		return err
	}

	// debug logging
	logger.Debug("{cache(catalog) - set} set catalog to cache")

	// dont return anything
	return nil
}

// get checks the file's mod time against the TTL, returning the decompressed
// payload when it is still valid
func (s *catalogStore) get(key string) (string, bool) {

	// build target path — path() handles the hashing
	target := s.path(key)

	// stat the file to check existence and mod time
	info, err := os.Stat(target)
	if err != nil {
		// dont bother logging here, a cold cache is normal
		return "", false
	}

	// check if the cached file has expired
	if time.Since(info.ModTime()) > s.ttl {
		return "", false
	}

	// open the compressed payload
	f, err := os.Open(target)
	if err != nil {
		logger.Error("{cache(catalog) - get} cannot open: %v", err)
		return "", false
	}
	defer f.Close()

	// wrap it in a gzip reader
	zr, err := gzip.NewReader(f)
	if err != nil {
		logger.Error("{cache(catalog) - get} cannot read gzip: %v", err)
		return "", false
	}
	defer zr.Close()

	// decompress the whole payload
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, zr); err != nil {
		logger.Error("{cache(catalog) - get} cannot decompress: %v", err)
		return "", false
	}

	// debug logging
	logger.Debug("{cache(catalog) - get} got catalog from cache")

	// return the payload
	return buf.String(), true
}

// GetCatalog retrieves a source catalog payload, falling back to the disk store
// when the in-memory entry is gone. The disk copy is what lets a restart reuse
// the previous import instead of re-fetching every source.
func (c *Cache) GetCatalog(key string) (string, bool) {
	logger.Debug("{cache - GetCatalog} get the cached catalog")

	if value, ok := c.cache.GetIfPresent(hashKey(key)); ok {
		return value, true
	}

	value, ok := c.catalog.get(key)
	if !ok {
		return "", false
	}

	// warm the in-memory copy so subsequent imports skip the disk read
	c.cache.Set(hashKey(key), value)
	return value, true
}

// SetCatalog stores a source catalog payload in both the in-memory cache and the
// disk store.
func (c *Cache) SetCatalog(key, value string) {
	logger.Debug("{cache - SetCatalog} set the catalog to cache")

	c.cache.Set(hashKey(key), value)

	if err := c.catalog.set(key, value); err != nil {
		logger.Error("{cache - SetCatalog} disk write failed: %v", err)
	}
}
