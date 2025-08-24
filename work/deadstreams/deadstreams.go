package deadstreams

import (
	"encoding/json"
	"fmt"
	"strings"

	//"kptv-proxy/work/config"
	//"kptv-proxy/work/types"
	"os"
	"time"
)

type DeadStreamEntry struct {
	Channel     string `json:"channel"`
	StreamIndex int    `json:"streamIndex"`
	URL         string `json:"url"`
	SourceName  string `json:"sourceName"`
	Timestamp   string `json:"timestamp"`
	Reason      string `json:"reason"` // "manual" or "auto_blocked"
}

type DeadStreamsFile struct {
	DeadStreams []DeadStreamEntry `json:"deadStreams"`
}

func LoadDeadStreams() (*DeadStreamsFile, error) {
	deadStreamsPath := "/settings/dead-streams.json"

	// Check if file exists
	if _, err := os.Stat(deadStreamsPath); os.IsNotExist(err) {
		// File doesn't exist, return empty structure
		return &DeadStreamsFile{DeadStreams: []DeadStreamEntry{}}, nil
	}

	data, err := os.ReadFile(deadStreamsPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read dead streams file: %w", err)
	}

	// Handle empty file or whitespace-only file
	trimmedData := strings.TrimSpace(string(data))
	if len(data) == 0 || trimmedData == "" {
		// File is empty or contains only whitespace, return empty structure
		return &DeadStreamsFile{DeadStreams: []DeadStreamEntry{}}, nil
	}

	var deadStreams DeadStreamsFile
	if err := json.Unmarshal(data, &deadStreams); err != nil {
		// If JSON parsing fails for any reason, create a backup and return empty structure
		backupPath := deadStreamsPath + ".corrupted." + time.Now().Format("20060102-150405")

		// Try to backup the corrupted file
		if backupErr := os.WriteFile(backupPath, data, 0644); backupErr != nil {
			// If backup fails, just log it but continue
			fmt.Printf("Warning: could not backup corrupted dead streams file: %v\n", backupErr)
		}

		// Always return empty structure instead of error - this makes the system resilient
		emptyStructure := &DeadStreamsFile{DeadStreams: []DeadStreamEntry{}}

		// Try to save the empty structure to fix the corrupted file
		if saveErr := SaveDeadStreams(emptyStructure); saveErr != nil {
			fmt.Printf("Warning: could not save empty dead streams file: %v\n", saveErr)
		}

		return emptyStructure, nil
	}

	// Ensure DeadStreams slice is not nil
	if deadStreams.DeadStreams == nil {
		deadStreams.DeadStreams = []DeadStreamEntry{}
	}

	return &deadStreams, nil
}

func SaveDeadStreams(deadStreams *DeadStreamsFile) error {
	deadStreamsPath := "/settings/dead-streams.json"

	data, err := json.MarshalIndent(deadStreams, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal dead streams: %w", err)
	}

	return os.WriteFile(deadStreamsPath, data, 0644)
}

func MarkStreamDead(channelName string, streamIndex int, url, sourceName, reason string) error {
	deadStreams, err := LoadDeadStreams()
	if err != nil {
		return err
	}

	// Check if already exists
	for i, entry := range deadStreams.DeadStreams {
		if entry.Channel == channelName && entry.StreamIndex == streamIndex {
			// Update existing entry with new reason if different
			if entry.Reason != reason {
				deadStreams.DeadStreams[i].Reason = reason
				deadStreams.DeadStreams[i].Timestamp = time.Now().Format(time.RFC3339)
				return SaveDeadStreams(deadStreams)
			}
			return nil // Already exists with same reason
		}
	}

	// Add new dead stream entry
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

func ReviveStream(channelName string, streamIndex int) error {
	deadStreams, err := LoadDeadStreams()
	if err != nil {
		return err
	}

	// Remove from dead streams
	var newDeadStreams []DeadStreamEntry
	found := false
	for _, entry := range deadStreams.DeadStreams {
		if entry.Channel == channelName && entry.StreamIndex == streamIndex {
			found = true
			continue // Skip this entry (remove it)
		}
		newDeadStreams = append(newDeadStreams, entry)
	}

	if !found {
		return fmt.Errorf("stream not found in dead streams list")
	}

	deadStreams.DeadStreams = newDeadStreams
	return SaveDeadStreams(deadStreams)
}

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
