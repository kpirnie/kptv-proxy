package app

import (
	"kptv-proxy/work/buffer"
	"kptv-proxy/work/cache"
	"kptv-proxy/work/client"
	"kptv-proxy/work/config"
	"kptv-proxy/work/db"
	"kptv-proxy/work/logger"
	"kptv-proxy/work/proxy"
	"os"
	"os/signal"
	"syscall"

	"github.com/panjf2000/ants/v2"
)

// App holds all bootstrapped application dependencies and provides
// lifecycle management for startup, shutdown, and graceful restart.
type App struct {
	Proxy      *proxy.StreamProxy     // Core proxy instance managing channels, streams, and clients
	WorkerPool interface{ Release() } // Bounded goroutine pool for parallel import processing
	BufferPool interface{ Cleanup() } // Pooled ring buffers for efficient stream memory management
	Cache      interface{ Close() }   // Shared cache instance for playlists, EPG, and XC data
}

// New bootstraps all core application dependencies in the correct order,
// wiring together the buffer pool, HTTP client, worker pool, cache, and proxy instance.
// Returns an initialized App ready for use, or an error if any dependency fails.
func New(cfg *config.Config) (*App, error) {

	// Initialize the shared ring buffer pool sized per-stream from config
	bufferPool := buffer.NewBufferPool(cfg.BufferSizePerStream * 1024 * 1024)

	// Initialize the HTTP client with response header timeout from config
	httpClient := client.NewHeaderSettingClient(cfg.ResponseHeaderTimeout)

	// Initialize the bounded goroutine pool for parallel stream import processing
	workerPool, err := ants.NewPool(cfg.WorkerThreads, ants.WithPreAlloc(true))
	if err != nil {
		logger.Error("Failed to create worker pool: %v", err)
		return nil, err
	}

	// Initialize the cache instance with TTL from config
	cacheInstance, err := cache.NewCache(cfg.CacheDuration)
	if err != nil {
		workerPool.Release()
		logger.Error("Failed to create cache: %v", err)
		return nil, err
	}

	// Wire all dependencies into the core proxy instance
	sp := proxy.New(cfg, bufferPool, httpClient, workerPool, cacheInstance)

	return &App{
		Proxy:      sp,
		WorkerPool: workerPool,
		BufferPool: bufferPool,
		Cache:      cacheInstance,
	}, nil
}

// Cleanup releases all application resources in the correct order.
// Should be called via defer after New() succeeds, and is also called
// by WaitForShutdown() before the process exits.
func (a *App) Cleanup() {
	a.WorkerPool.Release()
	a.BufferPool.Cleanup()
	a.Cache.Close()
	db.Close()
}

// WaitForShutdown blocks until SIGINT or SIGTERM is received, then performs
// a clean shutdown sequence: stops the stream watcher, halts the import refresh
// loop, and releases all pooled resources before the process exits.
func (a *App) WaitForShutdown() {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit

	logger.Info("Received signal: %v, initiating graceful shutdown...", sig)

	// Stop stream health monitoring before import loop to avoid watcher-triggered
	// stream switches racing with the shutdown sequence
	a.Proxy.WatcherManager.Stop()

	// Halt the periodic source re-import loop
	a.Proxy.StopImportRefresh()

	// Release buffer pool, worker pool, and cache
	a.Cleanup()
}
