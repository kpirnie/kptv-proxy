package admin

import (
	"encoding/json"
	"kptv-proxy/work/proxy"
	"kptv-proxy/work/types"
	"kptv-proxy/work/utils"
	"net/http"
	"runtime"
	"time"
)

// handleGetStats generates comprehensive system statistics for monitoring and
// administrative purposes, including performance metrics, resource utilization,
// and operational status information essential for system management.
func handleGetStats(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		totalChannels := 0
		activeStreams := 0
		connectedClients := 0
		activeRestreamers := 0
		upstreamConnections := 0
		activeChannels := 0

		sp.Channels.Range(func(key string, value *types.Channel) bool {
			totalChannels++
			channel := value
			channel.Mu.RLock()
			if channel.Restreamer != nil && channel.Restreamer.Running.Load() {
				activeStreams++
				activeRestreamers++
				upstreamConnections++
				activeChannels++
				channel.Restreamer.Clients.Range(func(_ string, _ *types.RestreamClient) bool {
					connectedClients++
					return true
				})
			}
			channel.Mu.RUnlock()
			return true
		})

		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		memoryUsage := utils.FormatBytes(int64(m.Alloc))

		uptime := time.Since(adminStartTime)
		uptimeStr := formatDuration(uptime)

		cacheStatus := "Disabled"
		if sp.Config.CacheEnabled {
			cacheStatus = "Enabled"
		}

		avgClientsPerStream := 0.0
		if upstreamConnections > 0 {
			avgClientsPerStream = float64(connectedClients) / float64(upstreamConnections)
		}

		stats := StatsResponse{
			TotalChannels:       totalChannels,
			ActiveStreams:       activeStreams,
			TotalSources:        len(sp.Config.Sources),
			TotalEpgs:           len(sp.Config.EPGs),
			ConnectedClients:    connectedClients,
			Uptime:              uptimeStr,
			MemoryUsage:         memoryUsage,
			CacheStatus:         cacheStatus,
			WorkerThreads:       sp.Config.WorkerThreads,
			TotalConnections:    connectedClients,
			BytesTransferred:    utils.FormatBytes(int64(m.TotalAlloc)),
			ActiveRestreamers:   activeRestreamers,
			StreamErrors:        0,
			ResponseTime:        "< 1ms",
			WatcherEnabled:      sp.Config.WatcherEnabled,
			UpstreamConnections: upstreamConnections,
			ActiveChannels:      activeChannels,
			AvgClientsPerStream: avgClientsPerStream,
		}

		if err := json.NewEncoder(w).Encode(stats); err != nil {
			addLogEntry("error", "Failed to encode stats")
			http.Error(w, "Failed to encode stats", http.StatusInternalServerError)
		}
	}
}
