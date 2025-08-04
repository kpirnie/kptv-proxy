package config

import (
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all configuration values
type Config struct {
	Port                   string
	BaseURL                string
	MaxBufferSize          int64
	BufferSizePerStream    int64
	CacheEnabled           bool
	CacheDuration          time.Duration
	ImportRefreshInterval  time.Duration
	MaxRetries             int
	MaxFailuresBeforeBlock int
	RetryDelay             time.Duration
	WorkerThreads          int
	MinDataSize            int64
	Debug                  bool
	ObfuscateUrls          bool
	SortField              string
	SortDirection          string
	Sources                []SourceConfig
	UserAgent              string
	ReqOrigin              string
	ReqReferrer            string
	HealthCheckTimeout     time.Duration
	EnableRestreaming      bool
	RateLimit              int // requests per second
	SegmentCacheSize       int // MB
}

// SourceConfig represents a stream source configuration
type SourceConfig struct {
	URL            string
	MaxConnections int
	ActiveConns    int32 // Use atomic
	//mu             sync.Mutex
}

func LoadConfig() *Config {
	config := &Config{
		Port:                   getEnv("PORT", "8080"),
		BaseURL:                getEnv("BASE_URL", "http://localhost:8080"),
		MaxBufferSize:          getEnvInt64("MAX_BUFFER_SIZE", 16*1024*1024),
		BufferSizePerStream:    getEnvInt64("BUFFER_SIZE_PER_STREAM", 1*1024*1024),
		CacheEnabled:           getEnvBool("CACHE_ENABLED", true),
		CacheDuration:          getEnvDuration("CACHE_DURATION", 30*time.Minute),
		ImportRefreshInterval:  getEnvDuration("IMPORT_REFRESH_INTERVAL", 12*time.Hour),
		MaxRetries:             getEnvInt("MAX_RETRIES", 3),
		MaxFailuresBeforeBlock: getEnvInt("MAX_FAILURES_BEFORE_BLOCK", 5),
		RetryDelay:             getEnvDuration("RETRY_DELAY", 5*time.Second),
		WorkerThreads:          getEnvInt("WORKER_THREADS", 8),
		MinDataSize:            getEnvInt64("MIN_DATA_SIZE", 2048),
		Debug:                  getEnvBool("DEBUG", false),
		ObfuscateUrls:          getEnvBool("OBFUSCATE_URLS", false),
		SortField:              getEnv("SORT_FIELD", "tvg-name"),
		SortDirection:          getEnv("SORT_DIRECTION", "asc"),
		UserAgent:              getEnv("USER_AGENT", "VLC/3.0.18 LibVLC/3.0.18"),
		ReqOrigin:              getEnv("REQ_ORIGIN", ""),
		ReqReferrer:            getEnv("REQ_REFERRER", ""),
		HealthCheckTimeout:     getEnvDuration("HEALTH_CHECK_TIMEOUT", 15*time.Second),
		EnableRestreaming:      getEnvBool("ENABLE_RESTREAMING", true),
		RateLimit:              getEnvInt("RATE_LIMIT", 100),
		SegmentCacheSize:       getEnvInt("SEGMENT_CACHE_SIZE", 512), // MB
	}

	// Load sources
	sourcesStr := os.Getenv("SOURCES")
	if sourcesStr != "" {
		sources := strings.Split(sourcesStr, ",")
		for _, s := range sources {
			s = strings.TrimSpace(s)
			parts := strings.Split(s, "|")
			if len(parts) == 2 {
				maxConns, _ := strconv.Atoi(strings.TrimSpace(parts[1]))
				config.Sources = append(config.Sources, SourceConfig{
					URL:            strings.TrimSpace(parts[0]),
					MaxConnections: maxConns,
				})
			}
		}
	}

	if config.Debug {
		log.Printf("Configuration loaded:")
		log.Printf("  Sources: %d configured", len(config.Sources))
		for i := range config.Sources {
			src := &config.Sources[i]
			urlToLog := src.URL
			log.Printf("    Source %d: %s (max connections: %d)", i+1, urlToLog, src.MaxConnections)
		}
		log.Printf("  Debug: %v", config.Debug)
		log.Printf("  Obfuscate URLs: %v", config.ObfuscateUrls)
		log.Printf("  Enable Restreaming: %v", config.EnableRestreaming)
	}

	return config
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
	}
	return defaultValue
}

func getEnvInt64(key string, defaultValue int64) int64 {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.ParseInt(value, 10, 64); err == nil {
			return intVal
		}
	}
	return defaultValue
}

func getEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		if boolVal, err := strconv.ParseBool(value); err == nil {
			return boolVal
		}
	}
	return defaultValue
}

func getEnvDuration(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if duration, err := time.ParseDuration(value); err == nil {
			return duration
		}
	}
	return defaultValue
}
