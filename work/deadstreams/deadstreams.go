package deadstreams

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// DeadStreamEntry represents a single dead stream record.
// Each entry contains identifying information and metadata
// about why and when the stream was marked as dead.
type DeadStreamEntry struct {
	Channel     string `json:"channel"`     // Name of the channel
	StreamIndex int    `json:"streamIndex"` // Index of the stream in the channel
	URL         string `json:"url"`         // URL of the dead stream
	SourceName  string `json:"sourceName"`  // Source/provider name
	Timestamp   string `json:"timestamp"`   // Time when the stream was marked dead
	Reason      string `json:"reason"`      // Reason for being dead (e.g., "manual", "auto_blocked")
}

// DeadStreamsFile wraps a slice of DeadStreamEntry for JSON serialization.
type DeadStreamsFile struct {
	DeadStreams []DeadStreamEntry `json:"deadStreams"`
}

// LoadDeadStreams loads the dead streams list from disk.
// If the file does not exist or is empty, it returns an empty DeadStreamsFile.
// If the file is corrupted, it creates a backup and replaces it with an empty file.
func LoadDeadStreams() (*DeadStreamsFile, error) {
	deadStreamsPath := "/settings/dead-streams.json"

	// Check if file exists
	if _, err := os.Stat(deadStreamsPath); os.IsNotExist(err) {
		// File doesn't exist, return empty structure
		return &DeadStreamsFile{DeadStreams: []DeadStreamEntry{}}, nil
	}

	// Read the file
	data, err := os.ReadFile(deadStreamsPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read dead streams file: %w", err)
	}

	// Handle empty file or whitespace-only file
	trimmedData := strings.TrimSpace(string(data))
	if len(data) == 0 || trimmedData == "" {
		return &DeadStreamsFile{DeadStreams: []DeadStreamEntry{}}, nil
	}

	// Parse JSON
	var deadStreams DeadStreamsFile
	if err := json.Unmarshal(data, &deadStreams); err != nil {
		// Create a backup of the corrupted file
		backupPath := deadStreamsPath + ".corrupted." + time.Now().Format("20060102-150405")
		if backupErr := os.WriteFile(backupPath, data, 0644); backupErr != nil {
			fmt.Printf("Warning: could not backup corrupted dead streams file: %v\n", backupErr)
		}

		// Replace corrupted file with empty structure
		emptyStructure := &DeadStreamsFile{DeadStreams: []DeadStreamEntry{}}
		if saveErr := SaveDeadStreams(emptyStructure); saveErr != nil {
			fmt.Printf("Warning: could not save empty dead streams file: %v\n", saveErr)
		}
		return emptyStructure, nil
	}

	// Ensure DeadStreams is not nil
	if deadStreams.DeadStreams == nil {
		deadStreams.DeadStreams = []DeadStreamEntry{}
	}

	return &deadStreams, nil
}

// SaveDeadStreams saves the provided DeadStreamsFile to disk as JSON.
func SaveDeadStreams(deadStreams *DeadStreamsFile) error {
	deadStreamsPath := "/settings/dead-streams.json"

	data, err := json.MarshalIndent(deadStreams, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal dead streams: %w", err)
	}

	return os.WriteFile(deadStreamsPath, data, 0644)
}

// MarkStreamDead marks a given stream as dead by adding or updating an entry in the dead streams file.
// If the stream is already marked dead with a different reason, the reason and timestamp are updated.
func MarkStreamDead(channelName string, streamIndex int, url, sourceName, reason string) error {
	deadStreams, err := LoadDeadStreams()
	if err != nil {
		return err
	}

	// Check if entry already exists
	for i, entry := range deadStreams.DeadStreams {
		if entry.Channel == channelName && entry.StreamIndex == streamIndex {
			// Update reason and timestamp if reason differs
			if entry.Reason != reason {
				deadStreams.DeadStreams[i].Reason = reason
				deadStreams.DeadStreams[i].Timestamp = time.Now().Format(time.RFC3339)
				return SaveDeadStreams(deadStreams)
			}
			return nil // Already exists with same reason
		}
	}

	// Add new entry
	newEntry := DeadStreamEntry{
		Channel:     channelName,
		StreamIndex: streamIndex,
		URL:         url,
		SourceName:  sourceName,
		Timestamp:   time.Now().Format(time.RFC3339),
		Reason:      reason,
	}

	deadStreams.DeadStreams = append(deadStreams.DeadStreams, newEntry)
	return SaveDeadStreams(deadStreams)
}

// ReviveStream removes a stream from the dead streams list.
// Returns an error if the stream was not found.
func ReviveStream(channelName string, streamIndex int) error {
	deadStreams, err := LoadDeadStreams()
	if err != nil {
		return err
	}

	var newDeadStreams []DeadStreamEntry
	found := false
	for _, entry := range deadStreams.DeadStreams {
		if entry.Channel == channelName && entry.StreamIndex == streamIndex {
			found = true
			continue // Skip this entry to remove it
		}
		newDeadStreams = append(newDeadStreams, entry)
	}

	if !found {
		return fmt.Errorf("stream not found in dead streams list")
	}

	deadStreams.DeadStreams = newDeadStreams
	return SaveDeadStreams(deadStreams)
}

// IsStreamDead checks whether a given stream is marked as dead.
// Returns false if the file cannot be loaded.
func IsStreamDead(channelName string, streamIndex int) bool {
	deadStreams, err := LoadDeadStreams()
	if err != nil {
		return false
	}

	for _, entry := range deadStreams.DeadStreams {
		if entry.Channel == channelName && entry.StreamIndex == streamIndex {
			return true
		}
	}
	return false
}

// GetDeadStreamReason returns the reason why a stream was marked dead.
// Returns an empty string if the stream is not found or the file cannot be loaded.
func GetDeadStreamReason(channelName string, streamIndex int) string {
	deadStreams, err := LoadDeadStreams()
	if err != nil {
		return ""
	}

	for _, entry := range deadStreams.DeadStreams {
		if entry.Channel == channelName && entry.StreamIndex == streamIndex {
			return entry.Reason
		}
	}
	return ""
}
