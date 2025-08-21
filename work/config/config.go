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

// Config holds all configuration values
type Config struct {
	Port                  string         `json:"port"`
	BaseURL               string         `json:"baseURL"`
	MaxBufferSize         int64          `json:"maxBufferSize"`
	BufferSizePerStream   int64          `json:"bufferSizePerStream"`
	CacheEnabled          bool           `json:"cacheEnabled"`
	CacheDuration         time.Duration  `json:"cacheDuration"`
	ImportRefreshInterval time.Duration  `json:"importRefreshInterval"`
	WorkerThreads         int            `json:"workerThreads"`
	Debug                 bool           `json:"debug"`
	ObfuscateUrls         bool           `json:"obfuscateUrls"`
	SortField             string         `json:"sortField"`
	SortDirection         string         `json:"sortDirection"`
	StreamTimeout         time.Duration  `json:"streamTimeout"`
	MaxConnectionsToApp   int            `json:"maxConnectionsToApp"`
	Sources               []SourceConfig `json:"sources"`
}

// SourceConfig represents a stream source configuration
type SourceConfig struct {
	Name                   string        `json:"name"`
	URL                    string        `json:"url"`
	Order                  int           `json:"order"`
	MaxConnections         int           `json:"maxConnections"`
	MaxStreamTimeout       time.Duration `json:"maxStreamTimeout"`
	RetryDelay             time.Duration `json:"retryDelay"`
	MaxRetries             int           `json:"maxRetries"`
	MaxFailuresBeforeBlock int           `json:"maxFailuresBeforeBlock"`
	MinDataSize            int64         `json:"minDataSize"`
	UserAgent              string        `json:"userAgent"`   // Added
	ReqOrigin              string        `json:"reqOrigin"`   // Added
	ReqReferrer            string        `json:"reqReferrer"` // Added
	ActiveConns            int32         `json:"-"`           // Not serialized, runtime tracking
}

// ConfigFile represents the JSON structure for marshaling/unmarshaling
type ConfigFile struct {
	Port                  string             `json:"port"`
	BaseURL               string             `json:"baseURL"`
	MaxBufferSize         int64              `json:"maxBufferSize"`
	BufferSizePerStream   int64              `json:"bufferSizePerStream"`
	CacheEnabled          bool               `json:"cacheEnabled"`
	CacheDuration         string             `json:"cacheDuration"`
	ImportRefreshInterval string             `json:"importRefreshInterval"`
	WorkerThreads         int                `json:"workerThreads"`
	Debug                 bool               `json:"debug"`
	ObfuscateUrls         bool               `json:"obfuscateUrls"`
	SortField             string             `json:"sortField"`
	SortDirection         string             `json:"sortDirection"`
	StreamTimeout         string             `json:"streamTimeout"`
	MaxConnectionsToApp   int                `json:"maxConnectionsToApp"`
	Sources               []SourceConfigFile `json:"sources"`
}

type SourceConfigFile struct {
	Name                   string `json:"name"`
	URL                    string `json:"url"`
	Order                  int    `json:"order"`
	MaxConnections         int    `json:"maxConnections"`
	MaxStreamTimeout       string `json:"maxStreamTimeout"`
	RetryDelay             string `json:"retryDelay"`
	MaxRetries             int    `json:"maxRetries"`
	MaxFailuresBeforeBlock int    `json:"maxFailuresBeforeBlock"`
	MinDataSize            int64  `json:"minDataSize"`
	UserAgent              string `json:"userAgent"`   // Added
	ReqOrigin              string `json:"reqOrigin"`   // Added
	ReqReferrer            string `json:"reqReferrer"` // Added
}

var (
	configCache *Config
	configMutex sync.RWMutex
)

func LoadConfig() *Config {
	configMutex.RLock()
	if configCache != nil {
		defer configMutex.RUnlock()
		return configCache
	}
	configMutex.RUnlock()

	configMutex.Lock()
	defer configMutex.Unlock()

	// Double-check after acquiring write lock
	if configCache != nil {
		return configCache
	}

	// Try to load from settings directory first
	configPath := "/settings/config.json"
	config, err := loadFromFile(configPath)
	if err != nil {
		log.Printf("Failed to load config from %s: %v", configPath, err)
		log.Printf("Falling back to default configuration...")
		config = getDefaultConfig()
	}

	// Validate and set defaults for any missing fields
	validateAndSetDefaults(config)

	configCache = config

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

func loadFromFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var configFile ConfigFile
	if err := json.Unmarshal(data, &configFile); err != nil {
		return nil, fmt.Errorf("failed to parse config JSON: %w", err)
	}

	return convertFromFile(&configFile)
}

func convertFromFile(cf *ConfigFile) (*Config, error) {
	config := &Config{
		Port:                cf.Port,
		BaseURL:             cf.BaseURL,
		MaxBufferSize:       cf.MaxBufferSize,
		BufferSizePerStream: cf.BufferSizePerStream,
		CacheEnabled:        cf.CacheEnabled,
		WorkerThreads:       cf.WorkerThreads,
		Debug:               cf.Debug,
		ObfuscateUrls:       cf.ObfuscateUrls,
		SortField:           cf.SortField,
		SortDirection:       cf.SortDirection,
		MaxConnectionsToApp: cf.MaxConnectionsToApp,
		// Removed: UserAgent, ReqOrigin, ReqReferrer
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
		src.UserAgent = srcFile.UserAgent     // Added
		src.ReqOrigin = srcFile.ReqOrigin     // Added
		src.ReqReferrer = srcFile.ReqReferrer // Added

		if src.MaxStreamTimeout, err = time.ParseDuration(srcFile.MaxStreamTimeout); err != nil {
			return nil, fmt.Errorf("invalid maxStreamTimeout for source %s: %w", src.Name, err)
		}
		if src.RetryDelay, err = time.ParseDuration(srcFile.RetryDelay); err != nil {
			return nil, fmt.Errorf("invalid retryDelay for source %s: %w", src.Name, err)
		}
	}

	return config, nil
}

func getDefaultConfig() *Config {
	return &Config{
		Port:                  "8080",
		BaseURL:               "http://localhost:8080",
		MaxBufferSize:         16,
		BufferSizePerStream:   1,
		CacheEnabled:          true,
		CacheDuration:         30 * time.Minute,
		ImportRefreshInterval: 12 * time.Hour,
		WorkerThreads:         8,
		Debug:                 false,
		ObfuscateUrls:         false,
		SortField:             "tvg-name",
		SortDirection:         "asc",
		StreamTimeout:         10 * time.Second,
		MaxConnectionsToApp:   100,
		Sources:               []SourceConfig{},
		// Removed: UserAgent, ReqOrigin, ReqReferrer
	}
}

func validateAndSetDefaults(config *Config) {
	if config.Port == "" {
		config.Port = "8080"
	}
	if config.BaseURL == "" {
		config.BaseURL = "http://localhost:8080"
	}
	if config.MaxBufferSize <= 0 {
		config.MaxBufferSize = 16
	}
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

	// Validate and set defaults for sources
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
		// Add defaults for new header fields
		if src.UserAgent == "" {
			src.UserAgent = "VLC/3.0.18 LibVLC/3.0.18"
		}
		// ReqOrigin and ReqReferrer can be empty by default
	}
}

// GetSourceByURL returns the source config for a given URL
func (c *Config) GetSourceByURL(url string) *SourceConfig {
	for i := range c.Sources {
		if c.Sources[i].URL == url {
			return &c.Sources[i]
		}
	}
	return nil
}

// GetSourcesByOrder returns sources sorted by their order
func (c *Config) GetSourcesByOrder() []SourceConfig {
	sources := make([]SourceConfig, len(c.Sources))
	copy(sources, c.Sources)

	// Sort by order
	for i := 0; i < len(sources)-1; i++ {
		for j := i + 1; j < len(sources); j++ {
			if sources[i].Order > sources[j].Order {
				sources[i], sources[j] = sources[j], sources[i]
			}
		}
	}

	return sources
}

// CreateExampleConfig creates an example configuration file
func CreateExampleConfig(path string) error {
	example := ConfigFile{
		Port:                  "8080",
		BaseURL:               "http://localhost:8080",
		MaxBufferSize:         256,
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
		// Removed: UserAgent, ReqOrigin, ReqReferrer
		Sources: []SourceConfigFile{
			{
				Name:                   "Primary IPTV Source",
				URL:                    "http://example.com/playlist1.m3u8",
				Order:                  1,
				MaxConnections:         5,
				MaxStreamTimeout:       "30s",
				RetryDelay:             "5s",
				MaxRetries:             3,
				MaxFailuresBeforeBlock: 5,
				MinDataSize:            2,
				UserAgent:              "VLC/3.0.18 LibVLC/3.0.18", // Added
				ReqOrigin:              "",                         // Added
				ReqReferrer:            "",                         // Added
			},
			{
				Name:                   "Backup IPTV Source",
				URL:                    "http://example.com/playlist2.m3u8",
				Order:                  2,
				MaxConnections:         10,
				MaxStreamTimeout:       "45s",
				RetryDelay:             "10s",
				MaxRetries:             2,
				MaxFailuresBeforeBlock: 3,
				MinDataSize:            1,
				UserAgent:              "Mozilla/5.0 (Smart TV; Linux)", // Added - different user agent
				ReqOrigin:              "https://provider2.com",         // Added
				ReqReferrer:            "https://provider2.com/player",  // Added
			},
		},
	}

	data, err := json.MarshalIndent(example, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}

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
