// work/admin/config.go
package admin

import (
	"encoding/json"
	"fmt"
	"io"
	"kptv-proxy/work/config"
	"kptv-proxy/work/proxy"
	"net/http"
)

// handleGetConfig serialises the current runtime configuration to JSON for
// the admin interface. Reads directly from the live config on the proxy
// instance rather than from disk.
func handleGetConfig(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		cfg := sp.Config

		// Marshal sources with duration fields as strings.
		type sourceOut struct {
			Name                   string `json:"name"`
			URL                    string `json:"url"`
			Order                  int    `json:"order"`
			MaxConnections         int    `json:"maxConnections"`
			MaxStreamTimeout       string `json:"maxStreamTimeout"`
			RetryDelay             string `json:"retryDelay"`
			MaxRetries             int    `json:"maxRetries"`
			MaxFailuresBeforeBlock int    `json:"maxFailuresBeforeBlock"`
			MinDataSize            int64  `json:"minDataSize"`
			UserAgent              string `json:"userAgent"`
			ReqOrigin              string `json:"reqOrigin"`
			ReqReferrer            string `json:"reqReferrer"`
			Username               string `json:"username"`
			Password               string `json:"password"`
			LiveIncludeRegex       string `json:"liveIncludeRegex"`
			LiveExcludeRegex       string `json:"liveExcludeRegex"`
			SeriesIncludeRegex     string `json:"seriesIncludeRegex"`
			SeriesExcludeRegex     string `json:"seriesExcludeRegex"`
			VODIncludeRegex        string `json:"vodIncludeRegex"`
			VODExcludeRegex        string `json:"vodExcludeRegex"`
		}
		sources := make([]sourceOut, len(cfg.Sources))
		for i, s := range cfg.Sources {
			sources[i] = sourceOut{
				Name: s.Name, URL: s.URL, Order: s.Order,
				MaxConnections:         s.MaxConnections,
				MaxStreamTimeout:       s.MaxStreamTimeout.String(),
				RetryDelay:             s.RetryDelay.String(),
				MaxRetries:             s.MaxRetries,
				MaxFailuresBeforeBlock: s.MaxFailuresBeforeBlock,
				MinDataSize:            s.MinDataSize, UserAgent: s.UserAgent,
				ReqOrigin: s.ReqOrigin, ReqReferrer: s.ReqReferrer,
				Username: s.Username, Password: s.Password,
				LiveIncludeRegex: s.LiveIncludeRegex, LiveExcludeRegex: s.LiveExcludeRegex,
				SeriesIncludeRegex: s.SeriesIncludeRegex, SeriesExcludeRegex: s.SeriesExcludeRegex,
				VODIncludeRegex: s.VODIncludeRegex, VODExcludeRegex: s.VODExcludeRegex,
			}
		}

		out := map[string]interface{}{
			"baseURL":               cfg.BaseURL,
			"bufferSizePerStream":   cfg.BufferSizePerStream,
			"cacheEnabled":          cfg.CacheEnabled,
			"cacheDuration":         cfg.CacheDuration.String(),
			"importRefreshInterval": cfg.ImportRefreshInterval.String(),
			"workerThreads":         cfg.WorkerThreads,
			"debug":                 cfg.Debug,
			"logLevel":              cfg.LogLevel,
			"obfuscateUrls":         cfg.ObfuscateUrls,
			"sortField":             cfg.SortField,
			"sortDirection":         cfg.SortDirection,
			"streamTimeout":         cfg.StreamTimeout.String(),
			"maxConnectionsToApp":   cfg.MaxConnectionsToApp,
			"watcherEnabled":        cfg.WatcherEnabled,
			"ffmpegMode":            cfg.FFmpegMode,
			"ffmpegPreInput":        cfg.FFmpegPreInput,
			"ffmpegPreOutput":       cfg.FFmpegPreOutput,
			"responseHeaderTimeout": cfg.ResponseHeaderTimeout.String(),
			"sources":               sources,
		}

		if err := json.NewEncoder(w).Encode(out); err != nil {
			addLogEntry("error", fmt.Sprintf("Failed to encode config: %v", err))
			http.Error(w, "Failed to encode config", http.StatusInternalServerError)
		}
	}
}

// handleSetConfig decodes a JSON config payload from the admin interface,
// validates the base URL, persists every field to SQLite via PersistConfig,
// and clears the in-memory config cache so the next LoadConfig call returns
// fresh data.
func handleSetConfig(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		addLogEntry("info", "POST /api/config received")

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

		// Decode into the runtime Config type directly — no intermediate
		// ConfigFile needed now that persistence goes through SQLite.
		var incoming config.Config
		if err := json.Unmarshal(body, &incoming); err != nil {
			addLogEntry("error", fmt.Sprintf("JSON decode error: %v", err))
			http.Error(w, "Invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}

		if incoming.BaseURL == "" {
			addLogEntry("error", "Base URL is required but empty")
			http.Error(w, "Base URL is required", http.StatusBadRequest)
			return
		}

		// Ensure FFmpeg slices are never nil in the persisted config.
		if incoming.FFmpegPreInput == nil {
			incoming.FFmpegPreInput = []string{}
		}
		if incoming.FFmpegPreOutput == nil {
			incoming.FFmpegPreOutput = []string{}
		}

		// Clear compiled regex filters so changed patterns are recompiled on next use.
		if sp.FilterManager != nil {
			sp.FilterManager.ClearFilters()
			addLogEntry("info", "Cleared compiled regex filters due to config update")
		}

		if err := config.PersistConfig(&incoming); err != nil {
			addLogEntry("error", fmt.Sprintf("Failed to persist config: %v", err))
			http.Error(w, "Failed to save config: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// Invalidate the in-memory cache so the next LoadConfig reads fresh data.
		config.ClearConfigCache()

		addLogEntry("info", "Configuration updated via admin interface")

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "success"})
	}
}
