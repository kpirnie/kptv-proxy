package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/VictoriaMetrics/fastcache"
	"github.com/gorilla/mux"
	"github.com/panjf2000/ants/v2"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/ratelimit"

	"kptv-proxy/work/buffer"
	"kptv-proxy/work/cache"
	"kptv-proxy/work/client"
	"kptv-proxy/work/config"
	"kptv-proxy/work/proxy"
)

var (
	Version = "v0.1.0" // default version
)

func main() {
	config := config.LoadConfig()

	// Set up logging
	logger := log.New(os.Stdout, "[KPTV-PROXY] ", log.LstdFlags)

	// Debug message to confirm logging is working
	logger.Printf("Starting KPTV Proxy - Debug mode: %v", config.Debug)
	logger.Printf("URL obfuscation: %v", config.ObfuscateUrls)

	// Initialize buffer pool
	bufferPool := buffer.NewBufferPool(config.BufferSizePerStream)

	// Initialize HTTP client
	httpClient := client.NewHeaderSettingClient(config)

	// Initialize worker pool
	workerPool, err := ants.NewPool(config.WorkerThreads, ants.WithPreAlloc(true))
	if err != nil {
		log.Fatalf("Failed to create worker pool: %v", err)
	}
	defer workerPool.Release()

	// Initialize rate limiter
	rateLimiter := ratelimit.New(config.RateLimit)

	// Initialize segment cache
	segmentCache := fastcache.New(config.SegmentCacheSize * 1024 * 1024)

	proxy := &proxy.StreamProxy{
		config: config,
		cache: &cache.Cache{
			m3u8Cache:    make(map[string]cacheEntry),
			channelCache: make(map[string]cacheEntry),
			duration:     config.CacheDuration,
			lastClear:    time.Now(),
		},
		segmentCache: segmentCache,
		logger:       logger,
		bufferPool:   bufferPool,
		httpClient:   httpClient,
		workerPool:   workerPool,
		rateLimiter:  rateLimiter,
	}

	// Start restreamer cleanup routine
	if config.EnableRestreaming {
		logger.Printf("Restreaming mode enabled")
		go proxy.restreamCleanup()
	} else {
		logger.Printf("Direct proxy mode enabled")
	}

	// Start import refresh routine
	go proxy.startImportRefresh()

	// Initial import
	proxy.importStreams()

	// Setup HTTP routes
	router := mux.NewRouter()

	// Original playlist route (all channels)
	router.HandleFunc("/playlist.m3u8", proxy.handlePlaylist).Methods("GET")

	// Group-based playlist route
	router.HandleFunc("/{group}/playlist.m3u8", proxy.handleGroupPlaylist).Methods("GET")

	router.HandleFunc("/stream/{channel}", proxy.handleStream).Methods("GET")
	router.Handle("/metrics", promhttp.Handler()).Methods("GET")

	addr := fmt.Sprintf(":%s", config.Port)
	logger.Printf("Starting KPTV Proxy on %s", addr)
	logger.Printf("  - Version: %s", Version)
	logger.Printf("Server configuration:")
	logger.Printf("  - Port: %s", config.Port)
	logger.Printf("  - Base URL: %s", config.BaseURL)
	logger.Printf("  - Restreaming: %v", config.EnableRestreaming)
	logger.Printf("  - Sources: %d", len(config.Sources))
	logger.Printf("  - Rate Limit: %d req/s", config.RateLimit)
	logger.Printf("  - Worker Threads: %d", config.WorkerThreads)

	if err := http.ListenAndServe(addr, router); err != nil {
		log.Fatalf("Server failed to start: %v", err)
	}
}
