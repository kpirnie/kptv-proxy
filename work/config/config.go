package config

import (
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"sync"
	"time"
)

// Config holds all application configuration values for the IPTV proxy server.
// It includes settings for buffering, caching, streaming, and multiple source configurations.
type Config struct {
	BaseURL               string         `json:"baseURL"`               // Base URL for the application (used for API and links)
	//MaxBufferSize         int64          `json:"maxBufferSize"`         // Maximum total buffer size in MB (all streams combined)
	BufferSizePerStream   int64          `json:"bufferSizePerStream"`   // Buffer size per individual stream in MB
	CacheEnabled          bool           `json:"cacheEnabled"`          // Whether caching is enabled globally
	CacheDuration         time.Duration  `json:"cacheDuration"`         // Duration before cache entries expire
	ImportRefreshInterval time.Duration  `json:"importRefreshInterval"` // Interval for refreshing imported content
	WorkerThreads         int            `json:"workerThreads"`         // Number of worker threads for background tasks
	Debug                 bool           `json:"debug"`                 // Enable debug logging
	ObfuscateUrls         bool           `json:"obfuscateUrls"`         // Obfuscate URLs in logs for security
	SortField             string         `json:"sortField"`             // Field to sort channels by (e.g., tvg-name)
	SortDirection         string         `json:"sortDirection"`         // Sort direction: "asc" or "desc"
	StreamTimeout         time.Duration  `json:"streamTimeout"`         // Timeout for stream operations
	MaxConnectionsToApp   int            `json:"maxConnectionsToApp"`   // Maximum concurrent connections allowed to the app
	Sources               []SourceConfig `json:"sources"`               // List of configured stream sources
	WatcherEnabled        bool           `json:"watcherEnabled"`
	FFmpegMode        	  bool           `json:"ffmpegMode"`        // Use FFmpeg instead of Go proxy/restreamer
	FFmpegPreInput    	  []string       `json:"ffmpegPreInput"`    // FFmpeg arguments before -i
	FFmpegPreOutput   	  []string       `json:"ffmpegPreOutput"`   // FFmpeg arguments before output URL
}

// SourceConfig represents the configuration for a single stream source.
// It includes connection limits, timeouts, retry settings, and HTTP headers.
type SourceConfig struct {
	Name                   string        `json:"name"`                   // Descriptive name for the source
	URL                    string        `json:"url"`                    // URL of the stream/playlist
	Order                  int           `json:"order"`                  // Priority order for failover/selection
	MaxConnections         int           `json:"maxConnections"`         // Maximum concurrent connections to this source
	MaxStreamTimeout       time.Duration `json:"maxStreamTimeout"`       // Maximum timeout per stream request
	RetryDelay             time.Duration `json:"retryDelay"`             // Delay between retry attempts
	MaxRetries             int           `json:"maxRetries"`             // Maximum retry attempts before failing
	MaxFailuresBeforeBlock int           `json:"maxFailuresBeforeBlock"` // Failures before marking source as blocked
	MinDataSize            int64         `json:"minDataSize"`            // Minimum valid data size for a stream
	UserAgent              string        `json:"userAgent"`              // HTTP User-Agent header for requests
	ReqOrigin              string        `json:"reqOrigin"`              // HTTP Origin header for requests
	ReqReferrer            string        `json:"reqReferrer"`            // HTTP Referer header for requests
	ActiveConns            int32         `json:"-"`                      // Runtime-only: tracks active connections (not serialized)
	Username               string        `json:"username"`               // XC Username
	Password               string        `json:"password"`               // XC Password
	LiveIncludeRegex       string        `json:"liveIncludeRegex,omitempty"`
	LiveExcludeRegex       string        `json:"liveExcludeRegex,omitempty"`
	SeriesIncludeRegex     string        `json:"seriesIncludeRegex,omitempty"`
	SeriesExcludeRegex     string        `json:"seriesExcludeRegex,omitempty"`
	VODIncludeRegex        string        `json:"vodIncludeRegex,omitempty"`
	VODExcludeRegex        string        `json:"vodExcludeRegex,omitempty"`
}

// ConfigFile represents the JSON file structure for marshaling/unmarshaling configuration.
// String duration fields (e.g., "30m") are parsed into time.Duration values.
type ConfigFile struct {
	BaseURL               string             `json:"baseURL"`
	//MaxBufferSize         int64              `json:"maxBufferSize"`
	BufferSizePerStream   int64              `json:"bufferSizePerStream"`
	CacheEnabled          bool               `json:"cacheEnabled"`
	CacheDuration         string             `json:"cacheDuration"`         // Duration as string (e.g., "30m")
	ImportRefreshInterval string             `json:"importRefreshInterval"` // Duration as string (e.g., "12h")
	WorkerThreads         int                `json:"workerThreads"`
	Debug                 bool               `json:"debug"`
	ObfuscateUrls         bool               `json:"obfuscateUrls"`
	SortField             string             `json:"sortField"`
	SortDirection         string             `json:"sortDirection"`
	StreamTimeout         string             `json:"streamTimeout"` // Duration as string (e.g., "10s")
	MaxConnectionsToApp   int                `json:"maxConnectionsToApp"`
	Sources               []SourceConfigFile `json:"sources"`
	WatcherEnabled        bool               `json:"watcherEnabled"`
	FFmpegMode        bool     `json:"ffmpegMode"`
	FFmpegPreInput    []string `json:"ffmpegPreInput"`
	FFmpegPreOutput   []string `json:"ffmpegPreOutput"`
}

// SourceConfigFile represents the source configuration in JSON format.
// Duration fields are stored as strings (parsed later into time.Duration).
type SourceConfigFile struct {
	Name                   string `json:"name"`
	URL                    string `json:"url"`
	Order                  int    `json:"order"`
	MaxConnections         int    `json:"maxConnections"`
	MaxStreamTimeout       string `json:"maxStreamTimeout"` // Duration string (e.g., "30s")
	RetryDelay             string `json:"retryDelay"`       // Duration string (e.g., "5s")
	MaxRetries             int    `json:"maxRetries"`
	MaxFailuresBeforeBlock int    `json:"maxFailuresBeforeBlock"`
	MinDataSize            int64  `json:"minDataSize"`
	UserAgent              string `json:"userAgent"`   // User-Agent header
	ReqOrigin              string `json:"reqOrigin"`   // Origin header
	ReqReferrer            string `json:"reqReferrer"` // Referer header
	Username               string `json:"username"`
	Password               string `json:"password"`
	LiveIncludeRegex       string `json:"liveIncludeRegex,omitempty"`
	LiveExcludeRegex       string `json:"liveExcludeRegex,omitempty"`
	SeriesIncludeRegex     string `json:"seriesIncludeRegex,omitempty"`
	SeriesExcludeRegex     string `json:"seriesExcludeRegex,omitempty"`
	VODIncludeRegex        string `json:"vodIncludeRegex,omitempty"`
	VODExcludeRegex        string `json:"vodExcludeRegex,omitempty"`
}

var (
	configCache *Config      // Cached configuration instance (singleton)
	configMutex sync.RWMutex // Mutex for safe concurrent access to configCache
)

// LoadConfig loads the configuration from file or returns the cached instance.
//
// Process:
//   - Uses double-checked locking to avoid redundant reloads.
//   - Attempts to load from `/settings/config.json`.
//   - Falls back to default config if file is missing or invalid.
//   - Runs validation to ensure safe defaults.
//
// Returns:
//   - *Config: fully validated configuration object
func LoadConfig() *Config {
	configMutex.RLock()
	if configCache != nil {
		defer configMutex.RUnlock()
		return configCache
	}
	configMutex.RUnlock()

	configMutex.Lock()
	defer configMutex.Unlock()

	// Double-check under write lock
	if configCache != nil {
		return configCache
	}

	// Attempt to load from file
	configPath := "/settings/config.json"
	config, err := loadFromFile(configPath)
	if err != nil {
		log.Printf("Failed to load config from %s: %v", configPath, err)
		log.Printf("Falling back to default configuration...")
		config = getDefaultConfig()
	}

	// Ensure safe defaults for missing values
	validateAndSetDefaults(config)

	// Cache for future calls
	configCache = config

	// Debug logging of loaded config
	if config.Debug {
		log.Printf("Configuration loaded:")
		log.Printf("  Sources: %d configured", len(config.Sources))
		for i := range config.Sources {
			src := &config.Sources[i]
			urlToLog := obfuscateURL(src.URL)
			log.Printf("    Source %d (%s): %s (max connections: %d, order: %d)",
				i+1, src.Name, urlToLog, src.MaxConnections, src.Order)
		}
		log.Printf("  Debug: %v", config.Debug)
		log.Printf("  Obfuscate URLs: %v", config.ObfuscateUrls)
		log.Printf("  Max Connections to App: %d", config.MaxConnectionsToApp)
	}

	return config
}

// loadFromFile reads and parses the configuration from a JSON file.
//
// Parameters:
//   - path: path to JSON config file
//
// Returns:
//   - *Config: parsed configuration
//   - error: if reading/parsing failed
func loadFromFile(path string) (*Config, error) {

	// read from tthe file
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	// unmarshal the config file
	var configFile ConfigFile
	if err := json.Unmarshal(data, &configFile); err != nil {
		return nil, fmt.Errorf("failed to parse config JSON: %w", err)
	}

	// convert to our settings
	return convertFromFile(&configFile)
}

// convertFromFile converts a ConfigFile to Config,
// parsing duration strings into time.Duration.
func convertFromFile(cf *ConfigFile) (*Config, error) {
	config := &Config{
		BaseURL:             cf.BaseURL,
		//MaxBufferSize:       cf.MaxBufferSize,
		BufferSizePerStream: cf.BufferSizePerStream,
		//CacheEnabled:        cf.CacheEnabled,
		CacheEnabled:        true,
		WorkerThreads:       cf.WorkerThreads,
		Debug:               cf.Debug,
		ObfuscateUrls:       cf.ObfuscateUrls,
		SortField:           cf.SortField,
		SortDirection:       cf.SortDirection,
		MaxConnectionsToApp: cf.MaxConnectionsToApp,
		WatcherEnabled:      cf.WatcherEnabled,
		FFmpegMode:          cf.FFmpegMode,
		FFmpegPreInput:      cf.FFmpegPreInput,
		FFmpegPreOutput:     cf.FFmpegPreOutput,
	}

	// Parse duration fields
	var err error
	if config.CacheDuration, err = time.ParseDuration(cf.CacheDuration); err != nil {
		return nil, fmt.Errorf("invalid cacheDuration: %w", err)
	}
	if config.ImportRefreshInterval, err = time.ParseDuration(cf.ImportRefreshInterval); err != nil {
		return nil, fmt.Errorf("invalid importRefreshInterval: %w", err)
	}
	if config.StreamTimeout, err = time.ParseDuration(cf.StreamTimeout); err != nil {
		return nil, fmt.Errorf("invalid streamTimeout: %w", err)
	}

	// Convert sources
	config.Sources = make([]SourceConfig, len(cf.Sources))
	for i, srcFile := range cf.Sources {
		src := &config.Sources[i]
		src.Name = srcFile.Name
		src.URL = srcFile.URL
		src.Order = srcFile.Order
		src.MaxConnections = srcFile.MaxConnections
		src.MaxRetries = srcFile.MaxRetries
		src.MaxFailuresBeforeBlock = srcFile.MaxFailuresBeforeBlock
		src.MinDataSize = srcFile.MinDataSize
		src.UserAgent = srcFile.UserAgent
		src.ReqOrigin = srcFile.ReqOrigin
		src.ReqReferrer = srcFile.ReqReferrer
		src.Username = srcFile.Username
		src.Password = srcFile.Password
		src.LiveIncludeRegex = srcFile.LiveIncludeRegex
		src.LiveExcludeRegex = srcFile.LiveExcludeRegex
		src.SeriesIncludeRegex = srcFile.SeriesIncludeRegex
		src.SeriesExcludeRegex = srcFile.SeriesExcludeRegex
		src.VODIncludeRegex = srcFile.VODIncludeRegex
		src.VODExcludeRegex = srcFile.VODExcludeRegex

		// Parse per-source durations
		if src.MaxStreamTimeout, err = time.ParseDuration(srcFile.MaxStreamTimeout); err != nil {
			return nil, fmt.Errorf("invalid maxStreamTimeout for source %s: %w", src.Name, err)
		}
		if src.RetryDelay, err = time.ParseDuration(srcFile.RetryDelay); err != nil {
			return nil, fmt.Errorf("invalid retryDelay for source %s: %w", src.Name, err)
		}
	}

	return config, nil
}

// getDefaultConfig returns a baseline configuration
// with sensible defaults when no file is present.
func getDefaultConfig() *Config {
	return &Config{
		BaseURL:               "http://localhost:8080",
		//MaxBufferSize:         16,               // Default: 16 MB total buffer
		BufferSizePerStream:   1,                // Default: 1 MB per stream
		CacheEnabled:          true,             // Enable caching
		CacheDuration:         30 * time.Minute, // Default 30 min expiration
		ImportRefreshInterval: 12 * time.Hour,   // Default: refresh imports every 12 hours
		WorkerThreads:         8,                // Default worker threads
		Debug:                 false,            // Debug disabled
		ObfuscateUrls:         false,            // Do not obfuscate by default
		SortField:             "tvg-name",       // Default sort field
		SortDirection:         "asc",            // Default ascending order
		StreamTimeout:         10 * time.Second, // Default stream timeout
		MaxConnectionsToApp:   100,              // Default connection limit
		Sources:               []SourceConfig{}, // No sources configured
		WatcherEnabled:        true,
	}
}

// validateAndSetDefaults ensures all config values are valid,
// filling in defaults for missing/invalid ones.
func validateAndSetDefaults(config *Config) {
	if config.BaseURL == "" {
		config.BaseURL = "http://localhost:8080"
	}
	/*if config.MaxBufferSize <= 0 {
		config.MaxBufferSize = 16
	}*/
	if config.BufferSizePerStream <= 0 {
		config.BufferSizePerStream = 1
	}
	if config.CacheDuration <= 0 {
		config.CacheDuration = 30 * time.Minute
	}
	if config.ImportRefreshInterval <= 0 {
		config.ImportRefreshInterval = 12 * time.Hour
	}
	if config.WorkerThreads <= 0 {
		config.WorkerThreads = 8
	}
	if config.SortField == "" {
		config.SortField = "tvg-name"
	}
	if config.SortDirection == "" {
		config.SortDirection = "asc"
	}
	if config.StreamTimeout <= 0 {
		config.StreamTimeout = 10 * time.Second
	}
	if config.MaxConnectionsToApp <= 0 {
		config.MaxConnectionsToApp = 100
	}

	// Validate each source
	for i := range config.Sources {
		src := &config.Sources[i]
		if src.Name == "" {
			src.Name = fmt.Sprintf("Source_%d", i+1)
		}
		if src.Order <= 0 {
			src.Order = i + 1
		}
		if src.MaxConnections <= 0 {
			src.MaxConnections = 5
		}
		if src.MaxStreamTimeout <= 0 {
			src.MaxStreamTimeout = 30 * time.Second
		}
		if src.RetryDelay <= 0 {
			src.RetryDelay = 5 * time.Second
		}
		if src.MaxRetries <= 0 {
			src.MaxRetries = 3
		}
		if src.MaxFailuresBeforeBlock <= 0 {
			src.MaxFailuresBeforeBlock = 5
		}
		if src.MinDataSize <= 0 {
			src.MinDataSize = 1
		}
		if src.UserAgent == "" {
			src.UserAgent = "VLC/3.0.18 LibVLC/3.0.18"
		}
		// ReqOrigin and ReqReferrer may remain empty
	}
}

// GetSourceByURL returns a pointer to the SourceConfig matching the given URL.
// Returns nil if no match is found.
func (c *Config) GetSourceByURL(url string) *SourceConfig {

	// loop the sources to make sure the url is valid
	for i := range c.Sources {
		if c.Sources[i].URL == url {
			return &c.Sources[i]
		}
	}
	return nil
}

// GetSourcesByOrder returns a copy of sources sorted by their Order field.
// Original slice remains unmodified.
func (c *Config) GetSourcesByOrder() []SourceConfig {

	// setup the original sources
	sources := make([]SourceConfig, len(c.Sources))
	copy(sources, c.Sources)

	// Simple bubble sort (sufficient since number of sources is small)
	for i := 0; i < len(sources)-1; i++ {
		for j := i + 1; j < len(sources); j++ {
			if sources[i].Order > sources[j].Order {
				sources[i], sources[j] = sources[j], sources[i]
			}
		}
	}

	return sources
}

// CreateExampleConfig creates an example config file on disk.
//
// Parameters:
//   - path: file path to write example config
//
// Returns:
//   - error: if write fails
func CreateExampleConfig(path string) error {
	example := ConfigFile{
		BaseURL:               "http://localhost:8080",
		//MaxBufferSize:         256,
		BufferSizePerStream:   16,
		CacheEnabled:          true,
		CacheDuration:         "30m",
		ImportRefreshInterval: "12h",
		WorkerThreads:         4,
		Debug:                 false,
		ObfuscateUrls:         true,
		SortField:             "tvg-name",
		SortDirection:         "asc",
		StreamTimeout:         "10s",
		MaxConnectionsToApp:   100,
		Sources: []SourceConfigFile{
			{
				Name:                   "Primary IPTV Source",
				URL:                    "http://example.com/playlist1.m3u8",
				Username:               "",
				Password:               "",
				Order:                  1,
				MaxConnections:         5,
				MaxStreamTimeout:       "30s",
				RetryDelay:             "5s",
				MaxRetries:             3,
				MaxFailuresBeforeBlock: 5,
				MinDataSize:            2,
				UserAgent:              "VLC/3.0.18 LibVLC/3.0.18",
				ReqOrigin:              "",
				ReqReferrer:            "",
				LiveIncludeRegex:       "",
				LiveExcludeRegex:       "",
				SeriesIncludeRegex:     "",
				SeriesExcludeRegex:     "",
				VODIncludeRegex:        "",
				VODExcludeRegex:        "",
			},
			{
				Name:                   "Backup IPTV Source",
				URL:                    "http://example.com/playlist2.m3u8",
				Username:               "",
				Password:               "",
				Order:                  2,
				MaxConnections:         10,
				MaxStreamTimeout:       "45s",
				RetryDelay:             "10s",
				MaxRetries:             2,
				MaxFailuresBeforeBlock: 3,
				MinDataSize:            1,
				UserAgent:              "Mozilla/5.0 (Smart TV; Linux)",
				ReqOrigin:              "https://provider2.com",
				ReqReferrer:            "https://provider2.com/player",
				LiveIncludeRegex:       "",
				LiveExcludeRegex:       "",
				SeriesIncludeRegex:     "",
				SeriesExcludeRegex:     "",
				VODIncludeRegex:        "",
				VODExcludeRegex:        "",
			},
		},
	}

	// setup the data properly
	data, err := json.MarshalIndent(example, "", "  ")
	if err != nil {
		return err
	}

	// write the config file
	return os.WriteFile(path, data, 0644)
}

// ClearConfigCache resets the configCache to nil.
// Forces a reload on the next LoadConfig() call.
func ClearConfigCache() {
	configMutex.Lock()
	defer configMutex.Unlock()
	configCache = nil
}

// obfuscateURL masks sensitive parts of a URL for logging.
//
// Example:
//
//	Input:  "http://example.com/secret/stream.m3u8?token=abc"
//	Output: "http://example.com/***?***"
func obfuscateURL(urlStr string) string {
	if urlStr == "" {
		return ""
	}
	u, err := url.Parse(urlStr)
	if err != nil {
		return "***OBFUSCATED***"
	}
	result := u.Scheme + "://" + u.Host
	if u.Path != "" && u.Path != "/" {
		result += "/***"
	}
	if u.RawQuery != "" {
		result += "?***"
	}
	if u.Fragment != "" {
		result += "#***"
	}
	return result
}
