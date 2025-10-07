package database

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"kptv-proxy/work/config"
	"time"
)

// LoadConfig loads the complete configuration from the database
func (db *DB) LoadConfig() (*config.Config, error) {
	cfg := &config.Config{}

	// Load scalar config values
	rows, err := db.Query("SELECT key, value FROM config")
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}
	defer rows.Close()

	configMap := make(map[string]string)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			continue
		}
		configMap[key] = value
	}

	// Parse config values
	if val, ok := configMap["base_url"]; ok {
		cfg.BaseURL = val
	} else {
		cfg.BaseURL = "http://localhost:8080"
	}

	if val, ok := configMap["buffer_size_per_stream"]; ok {
		var bufSize int64
		json.Unmarshal([]byte(val), &bufSize)
		cfg.BufferSizePerStream = bufSize
	} else {
		cfg.BufferSizePerStream = 16
	}

	if val, ok := configMap["cache_enabled"]; ok {
		var enabled bool
		json.Unmarshal([]byte(val), &enabled)
		cfg.CacheEnabled = enabled
	} else {
		cfg.CacheEnabled = true
	}

	if val, ok := configMap["cache_duration"]; ok {
		var duration time.Duration
		json.Unmarshal([]byte(val), &duration)
		cfg.CacheDuration = duration
	} else {
		cfg.CacheDuration = 30 * time.Minute
	}

	if val, ok := configMap["import_refresh_interval"]; ok {
		var interval time.Duration
		json.Unmarshal([]byte(val), &interval)
		cfg.ImportRefreshInterval = interval
	} else {
		cfg.ImportRefreshInterval = 12 * time.Hour
	}

	if val, ok := configMap["worker_threads"]; ok {
		var threads int
		json.Unmarshal([]byte(val), &threads)
		cfg.WorkerThreads = threads
	} else {
		cfg.WorkerThreads = 8
	}

	if val, ok := configMap["debug"]; ok {
		var debug bool
		json.Unmarshal([]byte(val), &debug)
		cfg.Debug = debug
	} else {
		cfg.Debug = false
	}

	if val, ok := configMap["obfuscate_urls"]; ok {
		var obfuscate bool
		json.Unmarshal([]byte(val), &obfuscate)
		cfg.ObfuscateUrls = obfuscate
	} else {
		cfg.ObfuscateUrls = true
	}

	if val, ok := configMap["sort_field"]; ok {
		cfg.SortField = val
	} else {
		cfg.SortField = "tvg-name"
	}

	if val, ok := configMap["sort_direction"]; ok {
		cfg.SortDirection = val
	} else {
		cfg.SortDirection = "asc"
	}

	if val, ok := configMap["stream_timeout"]; ok {
		var timeout time.Duration
		json.Unmarshal([]byte(val), &timeout)
		cfg.StreamTimeout = timeout
	} else {
		cfg.StreamTimeout = 10 * time.Second
	}

	if val, ok := configMap["max_connections_to_app"]; ok {
		var maxConn int
		json.Unmarshal([]byte(val), &maxConn)
		cfg.MaxConnectionsToApp = maxConn
	} else {
		cfg.MaxConnectionsToApp = 100
	}

	if val, ok := configMap["watcher_enabled"]; ok {
		var enabled bool
		json.Unmarshal([]byte(val), &enabled)
		cfg.WatcherEnabled = enabled
	} else {
		cfg.WatcherEnabled = true
	}

	if val, ok := configMap["ffmpeg_mode"]; ok {
		var mode bool
		json.Unmarshal([]byte(val), &mode)
		cfg.FFmpegMode = mode
	} else {
		cfg.FFmpegMode = false
	}

	if val, ok := configMap["ffmpeg_pre_input"]; ok {
		var args []string
		json.Unmarshal([]byte(val), &args)
		cfg.FFmpegPreInput = args
	}

	if val, ok := configMap["ffmpeg_pre_output"]; ok {
		var args []string
		json.Unmarshal([]byte(val), &args)
		cfg.FFmpegPreOutput = args
	}

	// Load sources
	cfg.Sources, err = db.LoadSources()
	if err != nil {
		return nil, fmt.Errorf("failed to load sources: %w", err)
	}

	return cfg, nil
}

// SaveConfig saves the complete configuration to the database
func (db *DB) SaveConfig(cfg *config.Config) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare("INSERT OR REPLACE INTO config (key, value, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP)")
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()

	// Helper to save config value
	save := func(key string, value interface{}) error {
		jsonValue, err := json.Marshal(value)
		if err != nil {
			return err
		}
		_, err = stmt.Exec(key, string(jsonValue))
		return err
	}

	// Save all config values
	if err := save("base_url", cfg.BaseURL); err != nil {
		return err
	}
	if err := save("buffer_size_per_stream", cfg.BufferSizePerStream); err != nil {
		return err
	}
	if err := save("cache_enabled", cfg.CacheEnabled); err != nil {
		return err
	}
	if err := save("cache_duration", cfg.CacheDuration); err != nil {
		return err
	}
	if err := save("import_refresh_interval", cfg.ImportRefreshInterval); err != nil {
		return err
	}
	if err := save("worker_threads", cfg.WorkerThreads); err != nil {
		return err
	}
	if err := save("debug", cfg.Debug); err != nil {
		return err
	}
	if err := save("obfuscate_urls", cfg.ObfuscateUrls); err != nil {
		return err
	}
	if err := save("sort_field", cfg.SortField); err != nil {
		return err
	}
	if err := save("sort_direction", cfg.SortDirection); err != nil {
		return err
	}
	if err := save("stream_timeout", cfg.StreamTimeout); err != nil {
		return err
	}
	if err := save("max_connections_to_app", cfg.MaxConnectionsToApp); err != nil {
		return err
	}
	if err := save("watcher_enabled", cfg.WatcherEnabled); err != nil {
		return err
	}
	if err := save("ffmpeg_mode", cfg.FFmpegMode); err != nil {
		return err
	}
	if err := save("ffmpeg_pre_input", cfg.FFmpegPreInput); err != nil {
		return err
	}
	if err := save("ffmpeg_pre_output", cfg.FFmpegPreOutput); err != nil {
		return err
	}

	return tx.Commit()
}

// InitializeDefaultConfig creates default configuration if none exists
func (db *DB) InitializeDefaultConfig() error {
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM config").Scan(&count)
	if err != nil {
		return err
	}

	if count > 0 {
		return nil // Config already exists
	}

	if db.logger != nil {
		db.logger.Println("[DATABASE] Initializing default configuration")
	}

	cfg := config.GetDefaultConfig()
	return db.SaveConfig(cfg)
}