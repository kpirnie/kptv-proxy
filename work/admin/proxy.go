// work/admin/proxy.go
package admin

import (
	"kptv-proxy/work/config"
	"kptv-proxy/work/logger"
)

// watcherManager defines the watcher lifecycle methods required by admin handlers.
type watcherManager interface {
	Start()
	Stop()
}

// filterManager defines the filter management methods required by admin handlers.
type filterManager interface {
	ClearFilters()
}

// persistWatcherConfig updates the watcherEnabled setting in SQLite.
// Called by handleToggleWatcher to ensure the new state survives restarts.
func persistWatcherConfig(enabled bool) error {
	cfg := config.LoadConfig()
	cfg.WatcherEnabled = enabled

	if err := config.PersistConfig(cfg); err != nil {
		logger.Error("{admin/proxy - persistWatcherConfig} Failed to persist watcher config: %v", err)
		return err
	}

	config.ClearConfigCache()
	logger.Debug("{admin/proxy - persistWatcherConfig} Watcher enabled=%v persisted to SQLite", enabled)
	return nil
}
