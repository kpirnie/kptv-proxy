package main

import (
	"fmt"
	"net/http"
	"os"

	"github.com/gorilla/mux"

	"kptv-proxy/work/app"
	"kptv-proxy/work/config"
	"kptv-proxy/work/constants"
	"kptv-proxy/work/db"
	"kptv-proxy/work/handlers"
	"kptv-proxy/work/logger"
	"kptv-proxy/work/users"
	"kptv-proxy/work/utils"
)

func main() {

	// Initialize the SQLite database before loading config.
	db.Get()

	// migrate the existing settings: this'll only happen once, but will backup the original config.json
	db.MigrateFromJSON()

	// Check if admin user exists, log a warning if not so operator knows to visit /register
	count, err := users.UserCount()
	if err != nil {
		logger.Error("Failed to check user count: %v", err)
	} else if count == 0 {
		logger.Warn("No admin user found — visit /register to create one before accessing the admin interface")
	}

	// Load configuration from settings
	cfg := config.LoadConfig()

	// Apply log level from config and compile content classification regexes
	logger.SetLogLevel(cfg.LogLevel)
	utils.InitContentRegexes()

	// Bootstrap all core dependencies: buffer pool, HTTP client, worker pool, cache, and proxy instance
	a, err := app.New(cfg)
	if err != nil {
		os.Exit(1)
	}

	// create the proxy instance before starting any background loops so they can access it for necessary functions like stream importing and restream cleanup
	proxyInstance := a.Proxy

	// Start background maintenance loops
	go proxyInstance.RestreamCleanup()    // Cleans up inactive restreamers and disconnected clients
	go proxyInstance.StartImportRefresh() // Periodically re-imports source playlists on configured interval
	go proxyInstance.StartEPGWarmup()     // Pre-warms the EPG disk cache on startup

	// Start stream watcher if enabled in config
	if cfg.WatcherEnabled {
		proxyInstance.WatcherManager.Start()
	}

	// Perform initial import of all configured stream sources
	proxyInstance.ImportStreams()

	// Register all HTTP routes: playlists, streams, XC API, EPG, metrics, admin, HDHomeRun
	router := mux.NewRouter()
	app.RegisterRoutes(router, proxyInstance)

	// set the application address and port
	addr := fmt.Sprintf(":%d", constants.Internal.ServerPort)

	// Log startup summary
	logger.Info("KPTV Proxy - https://kevinpirnie.com/")
	logger.Info("Server configuration:")
	logger.Info("  - Version: %s", app.VersionString())
	logger.Info("  - HDHomeRun Device ID: %s", handlers.HDHRDeviceID(cfg.BaseURL))
	logger.Info("  - Base URL: %s", cfg.BaseURL)
	logger.Info("  - Worker Threads: %d", cfg.WorkerThreads)
	logger.Info("  - Sources: %d", len(cfg.Sources))
	logger.Info("  - Channels: %d", proxyInstance.ChannelCount())
	logger.Info("  - EPGs: %d", len(cfg.EPGs))
	logger.Info("  - Per-Stream Buffer: %s", utils.FormatBytes(cfg.BufferSizePerStream*1024*1024))
	logger.Info("  - Cache Enabled: %v", cfg.CacheEnabled)
	logger.Info("  - Cache Duration: %s", cfg.CacheDuration)
	logger.Info("  - Source Refresh Rate: %s", cfg.ImportRefreshInterval)
	logger.Info("  - Stream Sort Attr.: %s", cfg.SortField)
	logger.Info("  - Stream Sort Dir.: %s", cfg.SortDirection)
	logger.Info("  - Log Level: %v", cfg.LogLevel)
	logger.Info("  - URL Obfuscation: %v", cfg.ObfuscateUrls)

	// Start the graceful restart loop, listening for signals from the admin interface
	go a.RunRestartLoop()

	// Start HTTP server in a goroutine so main can block on the shutdown signal below
	go func() {
		if err := http.ListenAndServe(addr, router); err != nil {
			logger.Error("{main} Server failed to start: %v", err)
			os.Exit(1)
		}
	}()

	// Block until SIGINT or SIGTERM is received, then cleanly stop watchers, import loop, and cache
	a.WaitForShutdown()
}
