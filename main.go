package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/gorilla/mux"
	"github.com/panjf2000/ants/v2"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"kptv-proxy/work/buffer"
	"kptv-proxy/work/cache"
	"kptv-proxy/work/client"
	"kptv-proxy/work/config"
	"kptv-proxy/work/handlers"
	"kptv-proxy/work/parser"
	"kptv-proxy/work/proxy"
	"kptv-proxy/work/types"
	"kptv-proxy/work/utils"
)

// default version
var (
	Version = "v0.1.0"
)

// our main app worker
func main() {

	// Ensure config file exists before loading
	if err := config.EnsureConfigExists(); err != nil {
		log.Fatalf("Failed to ensure config exists: %v", err)
	}

	// load our config
	cfg := config.LoadConfig()

	// Set up logging
	logger := log.New(os.Stdout, "[KPTV-PROXY] ", log.LstdFlags)

	// Initialize buffer pool
	bufferPool := buffer.NewBufferPool(cfg.BufferSizePerStream * 1024 * 1024)

	// Initialize HTTP client
	httpClient := client.NewHeaderSettingClient()

	// Initialize worker pool, then make sure it gets released
	workerPool, err := ants.NewPool(cfg.WorkerThreads, ants.WithPreAlloc(true))
	if err != nil {
		log.Fatalf("Failed to create worker pool: %v", err)
	}
	defer workerPool.Release()
	defer bufferPool.Cleanup()

	// Initialize cache and defer killing it.
	cacheInstance := cache.NewCache(cfg.CacheDuration)
	defer cacheInstance.Close()

	// Create proxy instance
	proxyInstance := proxy.New(cfg, logger, bufferPool, httpClient, workerPool, cacheInstance)

	// Initialize master playlist handler
	proxyInstance.MasterPlaylistHandler = parser.NewMasterPlaylistHandler(logger, cfg)

	// Start restreamer cleanup routine
	go proxyInstance.RestreamCleanup()

	// Start import refresh routine
	go proxyInstance.StartImportRefresh()

	// Start watcher if enabled
	if cfg.WatcherEnabled {
		proxyInstance.WatcherManager.Start()
	}

	// Initial import
	proxyInstance.ImportStreams()

	// Setup HTTP routes
	router := mux.NewRouter()

	// Original playlist route (all channels)
	router.HandleFunc("/pl", handlers.HandlePlaylist(proxyInstance)).Methods("GET")
	router.HandleFunc("/playlist", handlers.HandlePlaylist(proxyInstance)).Methods("GET")

	// Group-based playlist route
	router.HandleFunc("/pl/{group}", handlers.HandleGroupPlaylist(proxyInstance)).Methods("GET")
	router.HandleFunc("/playlist/{group}", handlers.HandleGroupPlaylist(proxyInstance)).Methods("GET")

	// Channel stream handler
	router.HandleFunc("/s/{channel}", handlers.HandleStream(proxyInstance)).Methods("GET")

	// EPG handler for XC sources
	router.HandleFunc("/epg", handlers.HandleEPG(proxyInstance)).Methods("GET")
	router.HandleFunc("/epg.xml", handlers.HandleEPG(proxyInstance)).Methods("GET")

	// Metrics handler
	router.Handle("/metrics", promhttp.Handler()).Methods("GET")

	// add the admin routes
	setupAdminRoutes(router, proxyInstance)

	addr := fmt.Sprintf(":%d", 8080)

	// show info
	logger.Printf("Starting KPTV Proxy %s", Version)
	logger.Printf("Server configuration:")
	logger.Printf("  - Base URL: %s", cfg.BaseURL)
	logger.Printf("  - Worker Threads: %d", cfg.WorkerThreads)
	logger.Printf("  - Sources: %d", len(cfg.Sources))
	logger.Printf("  - EPGs: %d", len(cfg.Sources))
	logger.Printf("  - Pre-Stream Buffer Size: %s", utils.FormatBytes(cfg.BufferSizePerStream*1024*1024))
	logger.Printf("  - Cache Enabled: %v", cfg.CacheEnabled)
	logger.Printf("  - Cache Duration: %s", cfg.CacheDuration)
	logger.Printf("  - Source Refresh Rate: %s", cfg.ImportRefreshInterval)
	logger.Printf("  - Stream Sort Attr.: %s", cfg.SortField)
	logger.Printf("  - Stream Sort Dir.: %s", cfg.SortDirection)
	logger.Printf("  - Debug Enabled: %v", cfg.Debug)
	logger.Printf("  - URL Obfuscation: %v", cfg.ObfuscateUrls)

	// gracefully restart if it's requested to do.
	go func() {

		// start a loop
		for {
			<-restartChan

			// debug logging
			if cfg.Debug {
				logger.Printf("Graceful restart requested...")
			}

			// Stop/Start watcher based on new config
			if cfg.Debug {
				logger.Printf("Managing watcher state during restart...")
			}
			proxyInstance.WatcherManager.Stop()

			// if its enabled
			if cfg.WatcherEnabled {
				proxyInstance.WatcherManager.Start()
			}

			// Stop import refresh
			proxyInstance.StopImportRefresh()

			// CLEAR CONFIG CACHE FIRST
			config.ClearConfigCache()

			// Reload config from file
			newConfig := config.LoadConfig()
			proxyInstance.Config = newConfig

			// Clear existing channels
			proxyInstance.Channels.Range(func(key string, value *types.Channel) bool {
				proxyInstance.Channels.Delete(key)
				return true
			})

			// Restart import process
			proxyInstance.ImportStreams()
			go proxyInstance.StartImportRefresh()

			// debug logging
			if cfg.Debug {
				logger.Printf("Graceful restart completed - loaded %d sources", len(newConfig.Sources))
			}

		}

	}()

	// fire us up, if there's an error log it
	if err := http.ListenAndServe(addr, router); err != nil {
		log.Fatalf("Server failed to start: %v", err)
	}

}
