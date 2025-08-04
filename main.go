package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/VictoriaMetrics/fastcache"
	"github.com/gorilla/mux"
	"github.com/panjf2000/ants/v2"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/ratelimit"

	"kptv-proxy/work/buffer"
	"kptv-proxy/work/cache"
	"kptv-proxy/work/client"
	"kptv-proxy/work/config"
	"kptv-proxy/work/handlers"
	"kptv-proxy/work/proxy"
)

var (
	Version = "v0.1.0" // default version
)

func main() {
	cfg := config.LoadConfig()

	// Set up logging
	logger := log.New(os.Stdout, "[KPTV-PROXY] ", log.LstdFlags)

	// Debug message to confirm logging is working
	logger.Printf("Starting KPTV Proxy - Debug mode: %v", cfg.Debug)
	logger.Printf("URL obfuscation: %v", cfg.ObfuscateUrls)

	// Initialize buffer pool
	bufferPool := buffer.NewBufferPool(cfg.BufferSizePerStream)

	// Initialize HTTP client
	httpClient := client.NewHeaderSettingClient(cfg)

	// Initialize worker pool
	workerPool, err := ants.NewPool(cfg.WorkerThreads, ants.WithPreAlloc(true))
	if err != nil {
		log.Fatalf("Failed to create worker pool: %v", err)
	}
	defer workerPool.Release()

	// Initialize rate limiter
	rateLimiter := ratelimit.New(cfg.RateLimit)

	// Initialize segment cache
	segmentCache := fastcache.New(cfg.SegmentCacheSize * 1024 * 1024)

	// Initialize cache
	cacheInstance := cache.NewCache(cfg.CacheDuration)

	// Create proxy instance
	proxyInstance := proxy.New(cfg, logger, bufferPool, httpClient, workerPool, rateLimiter, segmentCache, cacheInstance)

	// Start restreamer cleanup routine
	if cfg.EnableRestreaming {
		logger.Printf("Restreaming mode enabled")
		go proxyInstance.RestreamCleanup()
	} else {
		logger.Printf("Direct proxy mode enabled")
	}

	// Start import refresh routine
	go proxyInstance.StartImportRefresh()

	// Initial import
	proxyInstance.ImportStreams()

	// Setup HTTP routes
	router := mux.NewRouter()

	// Original playlist route (all channels)
	router.HandleFunc("/playlist.m3u8", handlers.HandlePlaylist(proxyInstance)).Methods("GET")

	// Group-based playlist route
	router.HandleFunc("/{group}/playlist.m3u8", handlers.HandleGroupPlaylist(proxyInstance)).Methods("GET")

	router.HandleFunc("/stream/{channel}", handlers.HandleStream(proxyInstance)).Methods("GET")
	router.Handle("/metrics", promhttp.Handler()).Methods("GET")

	addr := fmt.Sprintf(":%s", cfg.Port)
	logger.Printf("Starting KPTV Proxy on %s", addr)
	logger.Printf("  - Version: %s", Version)
	logger.Printf("Server configuration:")
	logger.Printf("  - Port: %s", cfg.Port)
	logger.Printf("  - Base URL: %s", cfg.BaseURL)
	logger.Printf("  - Restreaming: %v", cfg.EnableRestreaming)
	logger.Printf("  - Sources: %d", len(cfg.Sources))
	logger.Printf("  - Rate Limit: %d req/s", cfg.RateLimit)
	logger.Printf("  - Worker Threads: %d", cfg.WorkerThreads)

	if err := http.ListenAndServe(addr, router); err != nil {
		log.Fatalf("Server failed to start: %v", err)
	}
}
