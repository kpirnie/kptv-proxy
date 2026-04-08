// work/db/migrate.go
package db

import (
	"encoding/json"
	"fmt"
	"kptv-proxy/work/logger"
	"os"
	"strings"
)

const configJSONPath = "/settings/config.json"

// MigrateFromJSON checks if kp_settings is empty and config.json exists.
// If so, it reads config.json and imports all settings into SQLite, then
// renames config.json to config.json.migrated so it is not imported again.
func MigrateFromJSON() {
	// Check if migration is needed — skip if settings already populated.
	settings, err := AllSettings()
	if err != nil || len(settings) > 0 {
		return
	}

	data, err := os.ReadFile(configJSONPath)
	if err != nil {
		// No config.json — fresh install, nothing to migrate.
		return
	}

	logger.Info("Migrating config.json to SQLite...")

	var cf struct {
		BaseURL               string   `json:"baseURL"`
		BufferSizePerStream   int64    `json:"bufferSizePerStream"`
		CacheEnabled          bool     `json:"cacheEnabled"`
		CacheDuration         string   `json:"cacheDuration"`
		ImportRefreshInterval string   `json:"importRefreshInterval"`
		WorkerThreads         int      `json:"workerThreads"`
		Debug                 bool     `json:"debug"`
		LogLevel              string   `json:"logLevel"`
		ObfuscateUrls         bool     `json:"obfuscateUrls"`
		SortField             string   `json:"sortField"`
		SortDirection         string   `json:"sortDirection"`
		StreamTimeout         string   `json:"streamTimeout"`
		MaxConnectionsToApp   int      `json:"maxConnectionsToApp"`
		WatcherEnabled        bool     `json:"watcherEnabled"`
		FFmpegMode            bool     `json:"ffmpegMode"`
		FFmpegPreInput        []string `json:"ffmpegPreInput"`
		FFmpegPreOutput       []string `json:"ffmpegPreOutput"`
		ResponseHeaderTimeout string   `json:"responseHeaderTimeout"`
		Sources               []struct {
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
		} `json:"sources"`
		EPGs []struct {
			Name  string `json:"name"`
			URL   string `json:"url"`
			Order int    `json:"order"`
		} `json:"epgs"`
		XCOutputAccounts []struct {
			Name           string `json:"name"`
			Username       string `json:"username"`
			Password       string `json:"password"`
			MaxConnections int    `json:"maxConnections"`
			EnableLive     bool   `json:"enableLive"`
			EnableSeries   bool   `json:"enableSeries"`
			EnableVOD      bool   `json:"enableVOD"`
		} `json:"xcOutputAccounts"`
		SDAccounts []struct {
			Name            string   `json:"name"`
			Username        string   `json:"username"`
			Password        string   `json:"password"`
			Enabled         bool     `json:"enabled"`
			SelectedLineups []string `json:"selectedLineups"`
			DaysToFetch     int      `json:"daysToFetch"`
		} `json:"sdAccounts"`
	}

	if err := json.Unmarshal(data, &cf); err != nil {
		logger.Error("Migration failed — could not parse config.json: %v", err)
		return
	}

	// Persist scalar settings.
	import_strings := map[string]string{
		"baseURL":               cf.BaseURL,
		"bufferSizePerStream":   int64str(cf.BufferSizePerStream),
		"cacheEnabled":          boolstr(cf.CacheEnabled),
		"cacheDuration":         cf.CacheDuration,
		"importRefreshInterval": cf.ImportRefreshInterval,
		"workerThreads":         intstr(cf.WorkerThreads),
		"debug":                 boolstr(cf.Debug),
		"logLevel":              cf.LogLevel,
		"obfuscateUrls":         boolstr(cf.ObfuscateUrls),
		"sortField":             cf.SortField,
		"sortDirection":         cf.SortDirection,
		"streamTimeout":         cf.StreamTimeout,
		"maxConnectionsToApp":   intstr(cf.MaxConnectionsToApp),
		"watcherEnabled":        boolstr(cf.WatcherEnabled),
		"ffmpegMode":            boolstr(cf.FFmpegMode),
		"ffmpegPreInput":        strings_join(cf.FFmpegPreInput),
		"ffmpegPreOutput":       strings_join(cf.FFmpegPreOutput),
		"responseHeaderTimeout": cf.ResponseHeaderTimeout,
	}
	for k, v := range import_strings {
		if err := SetSetting(k, v); err != nil {
			logger.Error("Migration: failed to set %s: %v", k, err)
			return
		}
	}

	// Migrate sources.
	for _, s := range cf.Sources {
		if _, err := InsertSource(Source{
			Name: s.Name, URI: s.URL, Username: s.Username, Password: s.Password,
			SortOrder: s.Order, MaxCnx: s.MaxConnections,
			MaxStreamTo: s.MaxStreamTimeout, RetryDelay: s.RetryDelay,
			MaxRetries: s.MaxRetries, MaxFailures: s.MaxFailuresBeforeBlock,
			MinDataSize: int(s.MinDataSize), UserAgent: s.UserAgent,
			ReqOrigin: s.ReqOrigin, ReqReferer: s.ReqReferrer,
			LiveIncRegex: s.LiveIncludeRegex, LiveExcRegex: s.LiveExcludeRegex,
			SeriesIncRegex: s.SeriesIncludeRegex, SeriesExcRegex: s.SeriesExcludeRegex,
			VODIncRegex: s.VODIncludeRegex, VODExcRegex: s.VODExcludeRegex,
		}); err != nil {
			logger.Error("Migration: failed to insert source %s: %v", s.Name, err)
			return
		}
	}

	// Migrate EPGs.
	for _, e := range cf.EPGs {
		if _, err := InsertEPG(EPG{Name: e.Name, URL: e.URL, SortOrder: e.Order}); err != nil {
			logger.Error("Migration: failed to insert EPG %s: %v", e.Name, err)
			return
		}
	}

	// Migrate XC accounts.
	for _, a := range cf.XCOutputAccounts {
		if _, err := InsertXCAccount(XCAccount{
			Name: a.Name, Username: a.Username, Password: a.Password,
			MaxCnx: a.MaxConnections, EnableLive: a.EnableLive,
			EnableSeries: a.EnableSeries, EnableVOD: a.EnableVOD,
		}); err != nil {
			logger.Error("Migration: failed to insert XC account %s: %v", a.Name, err)
			return
		}
	}

	// Migrate SD accounts.
	for _, a := range cf.SDAccounts {
		if _, err := InsertSDAccount(SDAccount{
			Name: a.Name, Username: a.Username, Password: a.Password,
			Enabled: a.Enabled, DaysToFetch: a.DaysToFetch, Lineups: a.SelectedLineups,
		}); err != nil {
			logger.Error("Migration: failed to insert SD account %s: %v", a.Name, err)
			return
		}
	}

	// Rename config.json so migration does not run again.
	if err := os.Rename(configJSONPath, configJSONPath+".migrated"); err != nil {
		logger.Warn("Migration complete but could not rename config.json: %v", err)
	} else {
		logger.Info("Migration complete — config.json renamed to config.json.migrated")
	}
}

// Small helpers to avoid importing fmt/strconv into this file.
func int64str(n int64) string { return fmt.Sprintf("%d", n) }
func intstr(n int) string     { return fmt.Sprintf("%d", n) }
func boolstr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
func strings_join(s []string) string { return strings.Join(s, " ") }
