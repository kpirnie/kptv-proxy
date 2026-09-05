// work/deadstreams/deadstreams.go
package deadstreams

import (
	"fmt"
	"kptv-proxy/work/db"
	"kptv-proxy/work/logger"
	"sync"
	"sync/atomic"
)

const (
	StatusAlive      = 0
	StatusDeadManual = 2
	StatusDeadAuto   = 3
)

var (
	overrideCache atomic.Pointer[map[string]db.StreamOverride]
	overrideMu    sync.Mutex
)

// overrides returns the in-memory override snapshot, loading it from the
// database on first use. Every write in this package updates the snapshot, so
// the table is read once per process instead of once per stream check.
func overrides() map[string]db.StreamOverride {
	if m := overrideCache.Load(); m != nil {
		return *m
	}

	overrideMu.Lock()
	defer overrideMu.Unlock()

	if m := overrideCache.Load(); m != nil {
		return *m
	}

	loaded, err := db.GetAllStreamOverrides()
	if err != nil {
		logger.Error("{deadstreams - overrides} Failed to load overrides: %v", err)
		return map[string]db.StreamOverride{}
	}
	overrideCache.Store(&loaded)
	return loaded
}

// replaceOverride publishes a new snapshot with one entry set or removed,
// building a complete map so readers never observe a partial mutation.
func replaceOverride(key string, o db.StreamOverride, remove bool) {
	cur := overrides()

	overrideMu.Lock()
	defer overrideMu.Unlock()

	if m := overrideCache.Load(); m != nil {
		cur = *m
	}

	next := make(map[string]db.StreamOverride, len(cur)+1)
	for k, v := range cur {
		next[k] = v
	}
	if remove {
		delete(next, key)
	} else {
		next[key] = o
	}
	overrideCache.Store(&next)
}

// IsStreamDead returns true if the stream hash for the given channel is
// marked dead in the persistent overrides table.
func IsStreamDead(channelName, hash string) bool {
	o, found := overrides()[db.OverrideKey(channelName, hash)]
	if !found {
		return false
	}
	return o.SStatus == StatusDeadManual || o.SStatus == StatusDeadAuto
}

// GetDeadStreamReason returns the dead_reason for a stream or empty string.
func GetDeadStreamReason(channelName, hash string) string {
	return overrides()[db.OverrideKey(channelName, hash)].DeadReason
}

// MarkStreamDeadByHash marks a stream dead using its URL hash — preferred
// call path from the admin interface where the hash is available.
func MarkStreamDeadByHash(channelName, hash, reason string) error {
	status := StatusDeadAuto
	if reason == "manual" {
		status = StatusDeadManual
	}
	if err := db.SetStreamDead(channelName, hash, reason, status); err != nil {
		return err
	}

	key := db.OverrideKey(channelName, hash)
	o, exists := overrides()[key]
	if !exists {
		o = db.StreamOverride{Hash: hash, SOrder: -1}
	}
	o.SStatus = status
	o.DeadReason = reason
	replaceOverride(key, o, false)
	return nil
}

// ReviveStream clears dead status for a stream by channel name and index.
func ReviveStream(channelName, hash string) error {
	if err := db.SetStreamAlive(channelName, hash); err != nil {
		logger.Error("{deadstreams - ReviveStream} channel=%s hash=%s: %v", channelName, hash, err)
		return err
	}

	// SetStreamAlive drops the row when no custom order is held, and only
	// clears the dead status otherwise — mirror both cases in the snapshot.
	key := db.OverrideKey(channelName, hash)
	if o, exists := overrides()[key]; exists && o.SOrder != -1 {
		o.SStatus = StatusAlive
		o.DeadReason = ""
		replaceOverride(key, o, false)
	} else {
		replaceOverride(key, db.StreamOverride{}, true)
	}

	logger.Debug("{deadstreams - ReviveStream} channel=%s hash=%s revived", channelName, hash)
	return nil
}

// hashURL produces the same FNV64a hash used during import.
func hashURL(url string) string {
	// Import utils would create a cycle — inline the same logic.
	import_hash := func(s string) string {
		var h uint64 = 14695981039346656037
		for i := 0; i < len(s); i++ {
			h ^= uint64(s[i])
			h *= 1099511628211
		}
		return fmt.Sprintf("%x", h)
	}
	return import_hash(url)
}
