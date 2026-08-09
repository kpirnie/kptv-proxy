package main

import (
	"fmt"
	"net"
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
	//_ "net/http/pprof"
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
	go proxyInstance.RestreamCleanup()         // Cleans up inactive restreamers and disconnected clients
	go proxyInstance.StartImportRefresh()      // Periodically re-imports source playlists on configured interval
	epgReady := proxyInstance.StartEPGWarmup() // Pre-warms the EPG disk cache on startup

	// Start stream watcher if enabled in config
	if cfg.WatcherEnabled {
		proxyInstance.WatcherManager.Start()
	}

	// Perform the initial import in the background so HTTP starts serving immediately;
	// the startup summary reports once it lands
	importDone := make(chan struct{})
	go func() {
		proxyInstance.ImportStreams()
		close(importDone)
	}()

	// Register all HTTP routes: playlists, streams, XC API, EPG, metrics, admin, HDHomeRun
	router := mux.NewRouter()
	app.RegisterRoutes(router, proxyInstance)

	// expose net/http/pprof under /debug/pprof/ for profiling
	// router.PathPrefix("/debug/pprof/").Handler(http.DefaultServeMux)

	// set the application address and port
	addr := fmt.Sprintf(":%d", constants.Internal.ServerPort)

	// Log startup summary
	logger.Info("  KPTV Proxy - https://kevinpirnie.com/")
	logger.Info("  Server configuration:")
	logger.Info("	- Version: %s", app.VersionString())
	logger.Info("	- HDHomeRun Device ID: %s", handlers.HDHRDeviceID(cfg.BaseURL))
	logger.Info("	- Base URL: %s", cfg.BaseURL)
	logger.Info("	- Worker Threads: %d", cfg.WorkerThreads)
	logger.Info("	- Sources: %d", len(cfg.Sources))
	logger.Info("	- EPGs: %d", len(cfg.EPGs))
	logger.Info("	- Per-Stream Buffer: %s", utils.FormatBytes(cfg.BufferSizePerStream*1024*1024))
	logger.Info("	- Cache Enabled: %v", cfg.CacheEnabled)
	logger.Info("	- Cache Duration: %s", cfg.CacheDuration)
	logger.Info("	- Source Refresh Rate: %s", cfg.ImportRefreshInterval)
	logger.Info("	- Stream Sort Attr.: %s", cfg.SortField)
	logger.Info("	- Stream Sort Dir.: %s", cfg.SortDirection)
	logger.Info("	- Log Level: %v", cfg.LogLevel)
	logger.Info("	- URL Obfuscation: %v", cfg.ObfuscateUrls)

	// Start the graceful restart loop, listening for signals from the admin interface
	go a.RunRestartLoop()

	// Bind before serving so the ready line only prints once the port is actually accepting
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		logger.Error("{main} Server failed to bind %s: %v", addr, err)
		os.Exit(1)
	}

	// Serve in a goroutine so main can block on the shutdown signal below
	go func() {
		if err := http.Serve(listener, router); err != nil {
			logger.Error("{main} Server failed to start: %v", err)
			os.Exit(1)
		}
	}()

	// report the initial warmup once, without holding up the listener
	go func() {
		if <-epgReady {
			logger.Info("	- EPG warmup complete, cached to disk")
		}
	}()

	// the port is already accepting; announce readiness once the initial import
	// has committed so the channel count is real
	go func() {
		<-importDone
		logger.Info("	- Channels Imported: %d", proxyInstance.ChannelCount())
		logger.Info("	- Proxy is now started, you may move about the cabin...")
	}()

	// Block until SIGINT or SIGTERM is received, then cleanly stop watchers, import loop, and cache
	a.WaitForShutdown()
}
