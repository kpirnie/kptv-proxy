// work/logos/registry.go
package logos

import (
	"kptv-proxy/work/db"
	"kptv-proxy/work/logger"
	"sync"
)

var known sync.Map

// Register returns the cache hash for a remote logo URL and persists the
// hash -> URL pair the first time it is seen. Persisting is what lets a cold
// /logo request fetch the image without an export having run first.
func Register(url string) string {
	hash := HashURL(url)

	if _, seen := known.Load(hash); seen {
		return hash
	}

	if err := db.UpsertLogoURL(hash, url); err != nil {
		return hash
	}

	known.Store(hash, struct{}{})
	return hash
}

// URLFor returns the source URL recorded for a cache hash, consulting the
// in-memory set first and falling back to the database.
func URLFor(hash string) (string, bool) {
	url, ok := db.GetLogoURL(hash)
	if !ok {
		logger.Debug("{logos/registry - URLFor} no source url recorded for %s", hash)
		return "", false
	}

	known.Store(hash, struct{}{})
	return url, true
}
