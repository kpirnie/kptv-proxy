package admin

import (
	"encoding/json"
	"fmt"
	"io"
	"kptv-proxy/work/client"
	"kptv-proxy/work/config"
	"kptv-proxy/work/constants"
	"kptv-proxy/work/logger"
	"kptv-proxy/work/parser"
	"kptv-proxy/work/proxy"
	"kptv-proxy/work/utils"
	"net/http"
)

// maskedSecret stands in for stored credentials on config reads. A value posted
// back unchanged means "keep what is stored".
const maskedSecret = "********"

// maskSecret replaces a non-empty stored credential with the mask sentinel.
func maskSecret(v string) string {
	if v == "" {
		return ""
	}
	return maskedSecret
}

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
		for i := range cfg.Sources {
			s := &cfg.Sources[i]
			sources[i] = sourceOut{
				Name: s.Name, URL: s.URL, Order: s.Order,
				MaxConnections:         s.MaxConnections,
				MaxStreamTimeout:       s.MaxStreamTimeout.String(),
				RetryDelay:             s.RetryDelay.String(),
				MaxRetries:             s.MaxRetries,
				MaxFailuresBeforeBlock: s.MaxFailuresBeforeBlock,
				MinDataSize:            s.MinDataSize, UserAgent: s.UserAgent,
				ReqOrigin: s.ReqOrigin, ReqReferrer: s.ReqReferrer,
				Username: s.Username, Password: maskSecret(s.Password),
				LiveIncludeRegex: s.LiveIncludeRegex, LiveExcludeRegex: s.LiveExcludeRegex,
				SeriesIncludeRegex: s.SeriesIncludeRegex, SeriesExcludeRegex: s.SeriesExcludeRegex,
				VODIncludeRegex: s.VODIncludeRegex, VODExcludeRegex: s.VODExcludeRegex,
			}
		}

		out := map[string]any{
			"baseURL":                cfg.BaseURL,
			"bufferSizePerStream":    cfg.BufferSizePerStream,
			"cacheEnabled":           cfg.CacheEnabled,
			"cacheDuration":          cfg.CacheDuration.String(),
			"importRefreshInterval":  cfg.ImportRefreshInterval.String(),
			"workerThreads":          cfg.WorkerThreads,
			"debug":                  cfg.Debug,
			"logLevel":               cfg.LogLevel,
			"obfuscateUrls":          cfg.ObfuscateUrls,
			"sortField":              cfg.SortField,
			"sortDirection":          cfg.SortDirection,
			"streamTimeout":          cfg.StreamTimeout.String(),
			"maxConnectionsToApp":    cfg.MaxConnectionsToApp,
			"watcherEnabled":         cfg.WatcherEnabled,
			"ffmpegMode":             cfg.FFmpegMode,
			"ffmpegPreInput":         cfg.FFmpegPreInput,
			"ffmpegPreOutput":        cfg.FFmpegPreOutput,
			"responseHeaderTimeout":  cfg.ResponseHeaderTimeout.String(),
			"slowClientBufferChunks": cfg.SlowClientBufferChunks,
			"tmdbEnabled":            cfg.TMDBEnabled,
			"tmdbApiKey":             maskSecret(cfg.TMDBAPIKey),
			"sources":                sources,
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

		r.Body = http.MaxBytesReader(w, r.Body, constants.Internal.MaxConfigBodyBytes)

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

		// Credentials are masked on read; a value posted back unchanged means
		// keep the stored secret rather than persisting the mask itself
		if incoming.TMDBAPIKey == maskedSecret {
			incoming.TMDBAPIKey = sp.Config.TMDBAPIKey
		}
		for i := range incoming.Sources {
			if incoming.Sources[i].Password != maskedSecret {
				continue
			}
			incoming.Sources[i].Password = ""
			for j := range sp.Config.Sources {
				if sp.Config.Sources[j].Name == incoming.Sources[i].Name && sp.Config.Sources[j].URL == incoming.Sources[i].URL {
					incoming.Sources[i].Password = sp.Config.Sources[j].Password
					break
				}
			}
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

		// Reload from SQLite and swap the live config pointer so the saved
		// settings apply immediately, not only after a graceful restart
		sp.Config = config.LoadConfig()

		addLogEntry("info", "Configuration updated via admin interface")

		w.WriteHeader(http.StatusOK)
		utils.WriteJSON(w, map[string]string{"status": "success"})
	}
}

// handleGetSettings serialises the global settings only. Sources, EPGs, XC
// accounts and SD accounts each have their own endpoints.
func handleGetSettings(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		cfg := sp.Config

		out := map[string]any{
			"baseURL":                cfg.BaseURL,
			"bufferSizePerStream":    cfg.BufferSizePerStream,
			"cacheEnabled":           cfg.CacheEnabled,
			"cacheDuration":          cfg.CacheDuration.String(),
			"importRefreshInterval":  cfg.ImportRefreshInterval.String(),
			"workerThreads":          cfg.WorkerThreads,
			"debug":                  cfg.Debug,
			"logLevel":               cfg.LogLevel,
			"obfuscateUrls":          cfg.ObfuscateUrls,
			"sortField":              cfg.SortField,
			"sortDirection":          cfg.SortDirection,
			"streamTimeout":          cfg.StreamTimeout.String(),
			"maxConnectionsToApp":    cfg.MaxConnectionsToApp,
			"watcherEnabled":         cfg.WatcherEnabled,
			"ffmpegMode":             cfg.FFmpegMode,
			"ffmpegPreInput":         cfg.FFmpegPreInput,
			"ffmpegPreOutput":        cfg.FFmpegPreOutput,
			"responseHeaderTimeout":  cfg.ResponseHeaderTimeout.String(),
			"slowClientBufferChunks": cfg.SlowClientBufferChunks,
			"tmdbEnabled":            cfg.TMDBEnabled,
			"tmdbApiKey":             maskSecret(cfg.TMDBAPIKey),
		}

		if err := json.NewEncoder(w).Encode(out); err != nil {
			addLogEntry("error", fmt.Sprintf("Failed to encode settings: %v", err))
			http.Error(w, "Failed to encode settings", http.StatusInternalServerError)
		}
	}
}

// handleSetSettings persists the global settings and applies them to the running
// components, so changing a setting no longer requires a restart and no longer
// round-trips the source list through the browser.
func handleSetSettings(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		r.Body = http.MaxBytesReader(w, r.Body, constants.Internal.MaxConfigBodyBytes)

		body, err := io.ReadAll(r.Body)
		if err != nil {
			addLogEntry("error", fmt.Sprintf("Failed to read request body: %v", err))
			http.Error(w, "Failed to read body", http.StatusBadRequest)
			return
		}

		var incoming config.Config
		if err := json.Unmarshal(body, &incoming); err != nil {
			addLogEntry("error", fmt.Sprintf("JSON decode error: %v", err))
			http.Error(w, "Invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}

		if incoming.BaseURL == "" {
			http.Error(w, "Base URL is required", http.StatusBadRequest)
			return
		}

		// a key posted back unchanged means keep the stored secret
		if incoming.TMDBAPIKey == maskedSecret {
			incoming.TMDBAPIKey = sp.Config.TMDBAPIKey
		}

		if incoming.FFmpegPreInput == nil {
			incoming.FFmpegPreInput = []string{}
		}
		if incoming.FFmpegPreOutput == nil {
			incoming.FFmpegPreOutput = []string{}
		}

		if err := config.PersistSettings(&incoming); err != nil {
			addLogEntry("error", fmt.Sprintf("Failed to persist settings: %v", err))
			http.Error(w, "Failed to save settings: "+err.Error(), http.StatusInternalServerError)
			return
		}

		config.ClearConfigCache()

		fresh := config.LoadConfig()
		carryConnCounters(sp.Config, fresh)
		sp.Config = fresh

		// reapply settings held by components built from the previous config
		if sp.FilterManager != nil {
			sp.FilterManager.ClearFilters()
		}
		logger.SetLogLevel(fresh.LogLevel)
		sp.MasterPlaylistHandler = parser.NewMasterPlaylistHandler(fresh)
		sp.ImportClient = client.NewHeaderSettingClient(fresh.ResponseHeaderTimeout)
		sp.ReinitRateLimiters()

		addLogEntry("info", "Global settings updated via admin interface")

		w.WriteHeader(http.StatusOK)
		utils.WriteJSON(w, map[string]string{"status": "success"})
	}
}

// carryConnCounters copies live connection counters from the previous config onto
// a freshly loaded one, so swapping the config does not reset accounting for
// entities that survived the change.
func carryConnCounters(old, fresh *config.Config) {
	if old == nil || fresh == nil {
		return
	}

	sources := make(map[string]*config.SourceConfig, len(old.Sources))
	for i := range old.Sources {
		sources[old.Sources[i].Name+"|"+old.Sources[i].URL] = &old.Sources[i]
	}
	for i := range fresh.Sources {
		if prior, ok := sources[fresh.Sources[i].Name+"|"+fresh.Sources[i].URL]; ok {
			fresh.Sources[i].ActiveConns.Store(prior.ActiveConns.Load())
		}
	}

	accounts := make(map[string]*config.XCOutputAccount, len(old.XCOutputAccounts))
	for i := range old.XCOutputAccounts {
		accounts[old.XCOutputAccounts[i].Username] = &old.XCOutputAccounts[i]
	}
	for i := range fresh.XCOutputAccounts {
		if prior, ok := accounts[fresh.XCOutputAccounts[i].Username]; ok {
			fresh.XCOutputAccounts[i].ActiveConns.Store(prior.ActiveConns.Load())
		}
	}
}
