package app

import (
	"kptv-proxy/work/admin"
	"kptv-proxy/work/client"
	"kptv-proxy/work/config"
	"kptv-proxy/work/logger"
	"kptv-proxy/work/parser"
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

		// Drop cached playlists and XC responses generated under the old
		// config so URLs are rebuilt with the new base URL
		a.Proxy.Cache.ClearIfNeeded()

		// Reapply settings held by components constructed at boot,
		// otherwise they keep serving the pre-restart configuration
		logger.SetLogLevel(newConfig.LogLevel)
		a.Proxy.MasterPlaylistHandler = parser.NewMasterPlaylistHandler(newConfig)
		a.Proxy.ImportClient = client.NewHeaderSettingClient(newConfig.ResponseHeaderTimeout)
		a.Proxy.ReinitRateLimiters()

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
