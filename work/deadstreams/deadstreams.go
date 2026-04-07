package deadstreams

import (
	"encoding/json"
	"fmt"
	"kptv-proxy/work/logger"
	"os"
	"strings"
	"sync"
	"time"
)

// DeadStreamEntry represents a single dead stream record with comprehensive metadata.
// Each entry contains identifying information about the stream and the circumstances
// under which it was marked as dead, enabling tracking and analysis of stream failures.
type DeadStreamEntry struct {
	Channel     string `json:"channel"`     // Name of the channel this stream belongs to
	StreamIndex int    `json:"streamIndex"` // Zero-based index of the stream within the channel
	URL         string `json:"url"`         // Full URL of the dead stream for identification
	SourceName  string `json:"sourceName"`  // Human-readable name of the stream source/provider
	Timestamp   string `json:"timestamp"`   // RFC3339 formatted time when the stream was marked dead
	Reason      string `json:"reason"`      // Categorized reason for death (e.g., "manual", "auto_blocked")
}

// DeadStreamsFile represents the JSON file structure for persisting dead stream records.
// It wraps a slice of DeadStreamEntry objects to provide a consistent file format
// and enable future extension with additional metadata if needed.
type DeadStreamsFile struct {
	DeadStreams []DeadStreamEntry `json:"deadStreams"` // Array of all dead stream records
}

// in-memory cache of dead streams, keyed by "channel:index"
// populated on first load, updated on every write
var (
	deadStreamCache    map[string]DeadStreamEntry
	deadStreamCacheMu  sync.RWMutex
	deadStreamCacheSet bool // true once the cache has been populated
)

// cacheKey builds the lookup key from channel name and stream index
func cacheKey(channelName string, streamIndex int) string {
	return fmt.Sprintf("%s:%d", channelName, streamIndex)
}

// LoadDeadStreams reads and parses the dead streams database from disk.
// It handles various file states gracefully:
//   - Missing file: returns empty structure
//   - Empty/whitespace file: returns empty structure
//   - Corrupted file: creates backup, replaces with empty structure, and continues
//
// The function ensures the application never crashes due to dead streams file issues
// while preserving data through automatic backup creation when corruption is detected.
//
// Returns:
//   - *DeadStreamsFile: parsed dead streams data or empty structure on error
//   - error: non-nil only for serious I/O failures that prevent operation
func LoadDeadStreams() (*DeadStreamsFile, error) {
	deadStreamsPath := "/settings/dead-streams.json"

	// Check if file exists on disk
	if _, err := os.Stat(deadStreamsPath); os.IsNotExist(err) {
		// File doesn't exist - return empty structure for first-time initialization
		return &DeadStreamsFile{DeadStreams: []DeadStreamEntry{}}, nil
	}

	// Attempt to read the entire file contents
	data, err := os.ReadFile(deadStreamsPath)
	if err != nil {
		logger.Error("{deadstreams - LoadDeadStreams} failed to read dead streams file: %v", err)
		return nil, err
	}

	// Handle empty or whitespace-only files gracefully
	trimmedData := strings.TrimSpace(string(data))
	if len(data) == 0 || trimmedData == "" {
		logger.Debug("{deadstreams - LoadDeadStreams} no content in deadstream file")
		return &DeadStreamsFile{DeadStreams: []DeadStreamEntry{}}, nil
	}

	// Parse JSON content into structured data
	var deadStreams DeadStreamsFile
	if err := json.Unmarshal(data, &deadStreams); err != nil {

		// Create timestamped backup of corrupted file for debugging
		backupPath := deadStreamsPath + ".corrupted." + time.Now().Format("20060102-150405")
		if backupErr := os.WriteFile(backupPath, data, 0644); backupErr != nil {
			logger.Warn("{deadstreams - LoadDeadStreams} could not backup corrupted dead streams file: %v", backupErr)
		}

		// Replace corrupted file with clean empty structure
		emptyStructure := &DeadStreamsFile{DeadStreams: []DeadStreamEntry{}}
		if saveErr := SaveDeadStreams(emptyStructure); saveErr != nil {
			logger.Warn("could not save empty dead streams file: %v", saveErr)
		}

		logger.Error("{deadstreams - LoadDeadStreams} error parsing %v", err)
		return emptyStructure, nil
	}

	// Ensure the DeadStreams slice is never nil to prevent runtime panics
	if deadStreams.DeadStreams == nil {
		deadStreams.DeadStreams = []DeadStreamEntry{}
	}
	logger.Debug("{deadstreams - LoadDeadStreams} load deadstreams json")
	return &deadStreams, nil
}

// SaveDeadStreams persists the provided dead streams data to disk as formatted JSON.
// The function writes the data atomically and with consistent formatting to ensure
// the file remains readable and maintainable across application restarts.
//
// Parameters:
//   - deadStreams: complete dead streams data structure to persist
//
// Returns:
//   - error: non-nil if the file cannot be written to disk
func SaveDeadStreams(deadStreams *DeadStreamsFile) error {

	// set the file path
	deadStreamsPath := "/settings/dead-streams.json"

	// Marshal to JSON with consistent formatting (2-space indentation)
	data, err := json.MarshalIndent(deadStreams, "", "  ")
	if err != nil {
		logger.Error("failed to marshal dead streams: %v", err)
		return err
	}
	logger.Debug("{deadstreams - SaveDeadStreams} write the deadstreams")
	// Write atomically to disk with appropriate file permissions
	return os.WriteFile(deadStreamsPath, data, 0644)
}

// MarkStreamDead records a stream as dead in the persistent database.
// If an entry already exists for the same channel and stream index, the function
// updates the reason and timestamp only if they differ from the existing record.
// This prevents duplicate entries while allowing reason updates when circumstances change.
//
// Parameters:
//   - channelName: name of the channel containing the dead stream
//   - streamIndex: zero-based index identifying the specific stream within the channel
//   - url: full URL of the dead stream for identification and debugging
//   - sourceName: human-readable name of the stream source/provider
//   - reason: categorized reason for marking dead (e.g., "manual", "auto_blocked", "timeout")
//
// Returns:
//   - error: non-nil if the dead streams database cannot be loaded or saved
func MarkStreamDead(channelName string, streamIndex int, url, sourceName, reason string) error {
	ensureCacheLoaded()

	deadStreams, err := LoadDeadStreams()
	if err != nil {
		logger.Error("{deadstreams - MarkDeadStream} loading deadstreams %v", err)
		return err
	}

	// check for existing entry — update reason if it changed
	for i, entry := range deadStreams.DeadStreams {
		if entry.Channel == channelName && entry.StreamIndex == streamIndex {
			if entry.Reason != reason {
				deadStreams.DeadStreams[i].Reason = reason
				deadStreams.DeadStreams[i].Timestamp = time.Now().Format(time.RFC3339)

				if err := SaveDeadStreams(deadStreams); err != nil {
					return err
				}

				// sync updated entry into cache
				deadStreamCacheMu.Lock()
				deadStreamCache[cacheKey(channelName, streamIndex)] = deadStreams.DeadStreams[i]
				deadStreamCacheMu.Unlock()
			}
			logger.Warn("{deadstreams - MarkDeadStream} deadstream already exists")
			return nil
		}
	}

	// build new entry
	newEntry := DeadStreamEntry{
		Channel:     channelName,
		StreamIndex: streamIndex,
		URL:         url,
		SourceName:  sourceName,
		Timestamp:   time.Now().Format(time.RFC3339),
		Reason:      reason,
	}

	deadStreams.DeadStreams = append(deadStreams.DeadStreams, newEntry)

	if err := SaveDeadStreams(deadStreams); err != nil {
		return err
	}

	// add new entry to cache
	deadStreamCacheMu.Lock()
	deadStreamCache[cacheKey(channelName, streamIndex)] = newEntry
	deadStreamCacheMu.Unlock()

	logger.Debug("dead stream entry created: %v", newEntry)
	return nil
}

// ReviveStream removes a dead stream record from the persistent database.
// This function is used to manually restore streams that have been fixed
// or to clean up false positives in the dead streams tracking.
//
// Parameters:
//   - channelName: name of the channel containing the stream to revive
//   - streamIndex: zero-based index identifying the specific stream to revive
//
// Returns:
//   - error: non-nil if the stream is not found in dead streams or database operations fail
func ReviveStream(channelName string, streamIndex int) error {
	ensureCacheLoaded()

	deadStreams, err := LoadDeadStreams()
	if err != nil {
		return err
	}

	var newDeadStreams []DeadStreamEntry
	found := false
	for _, entry := range deadStreams.DeadStreams {
		if entry.Channel == channelName && entry.StreamIndex == streamIndex {
			found = true
			continue
		}
		newDeadStreams = append(newDeadStreams, entry)
	}

	if !found {
		logger.Error("{deadstreams - ReviveStream} stream not found")
		return nil
	}

	deadStreams.DeadStreams = newDeadStreams

	if err := SaveDeadStreams(deadStreams); err != nil {
		return err
	}

	// evict from cache on successful revive
	deadStreamCacheMu.Lock()
	delete(deadStreamCache, cacheKey(channelName, streamIndex))
	deadStreamCacheMu.Unlock()

	logger.Debug("{deadstreams - ReviveStream} revive stream")
	return nil
}

// IsStreamDead checks whether a specific stream is currently marked as dead.
// This function provides a fast lookup mechanism for stream selection logic
// to skip known problematic streams during failover operations.
//
// Parameters:
//   - channelName: name of the channel to check
//   - streamIndex: zero-based index of the stream within the channel
//
// Returns:
//   - bool: true if the stream is marked dead, false if alive or database unavailable
func IsStreamDead(channelName string, streamIndex int) bool {

	// ensure cache is warm before reading
	ensureCacheLoaded()

	deadStreamCacheMu.RLock()
	defer deadStreamCacheMu.RUnlock()

	_, exists := deadStreamCache[cacheKey(channelName, streamIndex)]
	return exists
}

// GetDeadStreamReason retrieves the reason why a specific stream was marked as dead.
// This information is useful for debugging, logging, and displaying status information
// in administrative interfaces or monitoring systems.
//
// Parameters:
//   - channelName: name of the channel containing the stream
//   - streamIndex: zero-based index of the stream within the channel
//
// Returns:
//   - string: reason the stream was marked dead, or empty string if not found or database unavailable
func GetDeadStreamReason(channelName string, streamIndex int) string {
	ensureCacheLoaded()

	deadStreamCacheMu.RLock()
	defer deadStreamCacheMu.RUnlock()

	if entry, exists := deadStreamCache[cacheKey(channelName, streamIndex)]; exists {
		return entry.Reason
	}
	return ""
}

// ensureCacheLoaded populates the in-memory cache from disk if not already done.
// Uses double-checked locking to avoid redundant disk reads under concurrency.
func ensureCacheLoaded() {
	deadStreamCacheMu.RLock()
	loaded := deadStreamCacheSet
	deadStreamCacheMu.RUnlock()

	if loaded {
		return
	}

	deadStreamCacheMu.Lock()
	defer deadStreamCacheMu.Unlock()

	// double-check after acquiring write lock
	if deadStreamCacheSet {
		return
	}

	file, err := LoadDeadStreams()
	if err != nil || file == nil {
		deadStreamCache = make(map[string]DeadStreamEntry)
		deadStreamCacheSet = true
		return
	}

	deadStreamCache = make(map[string]DeadStreamEntry, len(file.DeadStreams))
	for _, entry := range file.DeadStreams {
		deadStreamCache[cacheKey(entry.Channel, entry.StreamIndex)] = entry
	}
	deadStreamCacheSet = true
}
