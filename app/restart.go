package app

import (
	"kptv-proxy/work/admin"
	"kptv-proxy/work/config"
	"kptv-proxy/work/logger"
	"kptv-proxy/work/types"
)

// RunRestartLoop blocks on the admin restart channel and performs a full graceful
// restart sequence each time a restart is requested through the admin interface.
// Should be launched in its own goroutine from main.
func (a *App) RunRestartLoop() {
	for {
		// Block until a restart signal is sent via the admin interface
		<-admin.GetRestartChan()

		logger.Debug("{app/restart - RunRestartLoop} Graceful restart requested...")

		// Stop the stream watcher before config reload to prevent health checks
		// racing against the channel map being cleared below
		logger.Debug("{app/restart - RunRestartLoop} Managing watcher state during restart...")
		a.Proxy.WatcherManager.Stop()

		// Halt the periodic source re-import loop before clearing state
		a.Proxy.StopImportRefresh()

		// Invalidate the in-memory config cache so LoadConfig reads fresh from disk
		config.ClearConfigCache()

		// Load the updated configuration
		newConfig := config.LoadConfig()
		a.Proxy.Config = newConfig

		// Clear all existing channels so the fresh import starts from a clean state
		a.Proxy.Channels.Range(func(key string, value *types.Channel) bool {
			a.Proxy.Channels.Delete(key)
			return true
		})

		// Re-import all streams from the updated source list
		a.Proxy.ImportStreams()

		// Restart the periodic import refresh loop with the new config interval
		go a.Proxy.StartImportRefresh()

		// Restart the stream watcher if enabled in the new config
		if newConfig.WatcherEnabled {
			a.Proxy.WatcherManager.Start()
		}

		logger.Debug("{app/restart - RunRestartLoop} Graceful restart completed - loaded %d sources", len(newConfig.Sources))
	}
}
