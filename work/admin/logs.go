package admin

import (
	"encoding/json"
	"kptv-proxy/work/logger"
	"net/http"
	"time"
)

// Global log buffer vars
var (
	// logEntries maintains a circular buffer of recent log entries with a 1000 entry limit,
	// providing real-time debugging information through the admin interface without
	// unbounded memory growth during long-running operations.
	logEntries = make([]LogEntry, 0, 1000)
)

// LogEntry represents individual log entries captured by the admin interface for
// real-time monitoring and debugging support.
type LogEntry struct {
	Timestamp string `json:"timestamp"` // Human-readable timestamp of log entry creation
	Level     string `json:"level"`     // Log severity level (info, debug, error, etc.)
	Message   string `json:"message"`   // Complete log message content for analysis
}

// addLogEntry adds a new entry to the admin log buffer with automatic size management.
// Maintains a circular buffer capped at 1000 entries to prevent unbounded memory growth.
func addLogEntry(level, message string) {
	entry := LogEntry{
		Timestamp: time.Now().Format("2006-01-02 15:04:05"),
		Level:     level,
		Message:   message,
	}

	logEntries = append(logEntries, entry)

	// Maintain circular buffer with 1000 entry limit
	if len(logEntries) > 1000 {
		logEntries = logEntries[len(logEntries)-1000:]
	}
}

// handleGetLogs retrieves the current log buffer for admin interface display.
func handleGetLogs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	logs := logger.GetLogs()
	if err := json.NewEncoder(w).Encode(logs); err != nil {
		http.Error(w, "Failed to encode logs", http.StatusInternalServerError)
	}
}

// handleClearLogs clears the admin log buffer and records the clearing action.
func handleClearLogs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	logger.ClearLogs()
	logger.Info("Log entries cleared")

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

// AdminLog provides direct admin log entry creation for admin-specific events.
func AdminLog(level, message string) {
	addLogEntry(level, message)
}
