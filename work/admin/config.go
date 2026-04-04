package admin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"kptv-proxy/work/config"
	"kptv-proxy/work/proxy"
	"net/http"
	"os"
)

// handleGetConfig retrieves the current system configuration directly from the
// configuration file, ensuring that the admin interface displays the persistent
// configuration state rather than potentially modified runtime values.
func handleGetConfig(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		configPath := "/settings/config.json"
		data, err := os.ReadFile(configPath)
		if err != nil {
			addLogEntry("error", fmt.Sprintf("Failed to read config file: %v", err))
			http.Error(w, "Failed to read config file", http.StatusInternalServerError)
			return
		}

		w.Write(data)
	}
}

// handleSetConfig processes configuration updates through the admin interface,
// implementing atomic file operations and comprehensive validation to ensure
// configuration consistency and system stability during updates.
func handleSetConfig(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		addLogEntry("info", "POST /api/config received")

		// Panic recovery for robust error handling
		defer func() {
			if err := recover(); err != nil {
				addLogEntry("error", fmt.Sprintf("PANIC in handleSetConfig: %v", err))
				http.Error(w, "Internal server error", http.StatusInternalServerError)
			}
		}()

		w.Header().Set("Content-Type", "application/json")

		body, err := io.ReadAll(r.Body)
		if err != nil {
			addLogEntry("error", fmt.Sprintf("Failed to read request body: %v", err))
			http.Error(w, "Failed to read body", http.StatusBadRequest)
			return
		}
		addLogEntry("info", fmt.Sprintf("Request body length: %d bytes", len(body)))

		r.Body = io.NopCloser(bytes.NewBuffer(body))

		// Clear all compiled regex filters so changed patterns are recompiled on next use
		if sp.FilterManager != nil {
			sp.FilterManager.ClearFilters()
			addLogEntry("info", "Cleared all compiled regex filters due to config update")
		}

		var configFile config.ConfigFile
		if err := json.NewDecoder(r.Body).Decode(&configFile); err != nil {
			addLogEntry("error", fmt.Sprintf("JSON decode error: %v", err))
			http.Error(w, "Invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}

		if configFile.BaseURL == "" {
			addLogEntry("error", "Base URL is required but empty")
			http.Error(w, "Base URL is required", http.StatusBadRequest)
			return
		}

		// Ensure FFmpeg argument slices are never nil in the persisted config
		if configFile.FFmpegPreInput == nil {
			configFile.FFmpegPreInput = []string{}
		}
		if configFile.FFmpegPreOutput == nil {
			configFile.FFmpegPreOutput = []string{}
		}

		configPath := "/settings/config.json"
		tempPath := "/settings/config.json.tmp"

		data, err := json.MarshalIndent(configFile, "", "  ")
		if err != nil {
			addLogEntry("error", fmt.Sprintf("Failed to marshal config: %v", err))
			http.Error(w, "Failed to marshal config", http.StatusInternalServerError)
			return
		}

		if err := os.WriteFile(tempPath, data, 0644); err != nil {
			addLogEntry("error", fmt.Sprintf("Failed to write temp file: %v", err))
			http.Error(w, "Failed to write temp file: "+err.Error(), http.StatusInternalServerError)
			return
		}

		if err := os.Rename(tempPath, configPath); err != nil {
			addLogEntry("error", fmt.Sprintf("Failed to move temp file: %v", err))
			http.Error(w, "Failed to move config file: "+err.Error(), http.StatusInternalServerError)
			return
		}

		addLogEntry("info", "Configuration updated via admin interface")

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "success"})
	}
}
