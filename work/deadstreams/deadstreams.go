// work/deadstreams/deadstreams.go
package deadstreams

import (
	"fmt"
	"kptv-proxy/work/db"
	"kptv-proxy/work/logger"
)

const (
	StatusAlive      = 0
	StatusDeadManual = 2
	StatusDeadAuto   = 3
)

// IsStreamDead returns true if the stream hash for the given channel is
// marked dead in the persistent overrides table.
func IsStreamDead(channelName, hash string) bool {
	o, found := db.GetStreamOverride(channelName, hash)
	if !found {
		return false
	}
	return o.SStatus == StatusDeadManual || o.SStatus == StatusDeadAuto
}

// GetDeadStreamReason returns the dead_reason for a stream or empty string.
func GetDeadStreamReason(channelName, hash string) string {
	o, _ := db.GetStreamOverride(channelName, hash)
	return o.DeadReason
}

// MarkStreamDeadByHash marks a stream dead using its URL hash — preferred
// call path from the admin interface where the hash is available.
func MarkStreamDeadByHash(channelName, hash, reason string) error {
	status := StatusDeadAuto
	if reason == "manual" {
		status = StatusDeadManual
	}
	return db.SetStreamDead(channelName, hash, reason, status)
}

// ReviveStream clears dead status for a stream by channel name and index.
func ReviveStream(channelName, hash string) error {
	if err := db.DeleteStreamOverride(channelName, hash); err != nil {
		logger.Error("{deadstreams - ReviveStream} channel=%s hash=%s: %v", channelName, hash, err)
		return err
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
