// work/localscan/fscache.go
package localscan

import "sync"

// fsCacheStore memoises sidecar reads and artwork probes for the duration of a
// single scan. Directory-level files resolve identically for every file in that
// directory, so without this an album of 20 tracks reads album.nfo 20 times and
// stats each candidate cover image 20 times.
type fsCacheStore struct {
	mu    sync.RWMutex
	nfo   map[string]*nfoData
	files map[string]string
}

var fsCache = newFSCache()

// newFSCache constructs an empty scan-scoped filesystem cache.
func newFSCache() *fsCacheStore {
	return &fsCacheStore{
		nfo:   make(map[string]*nfoData),
		files: make(map[string]string),
	}
}

// ResetFSCache discards memoised sidecar and artwork lookups. Called at the
// start of every scan so edits made since the last pass are picked up.
func ResetFSCache() {
	fsCache = newFSCache()
}

func (c *fsCacheStore) getNFO(path string) (*nfoData, bool) {
	c.mu.RLock()
	n, ok := c.nfo[path]
	c.mu.RUnlock()
	return n, ok
}

func (c *fsCacheStore) putNFO(path string, n *nfoData) {
	c.mu.Lock()
	c.nfo[path] = n
	c.mu.Unlock()
}

func (c *fsCacheStore) getFile(path string) (string, bool) {
	c.mu.RLock()
	v, ok := c.files[path]
	c.mu.RUnlock()
	return v, ok
}

func (c *fsCacheStore) putFile(path, resolved string) {
	c.mu.Lock()
	c.files[path] = resolved
	c.mu.Unlock()
}
