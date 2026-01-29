package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"kptv-proxy/work/config"
	"kptv-proxy/work/deadstreams"
	"kptv-proxy/work/middleware"
	"kptv-proxy/work/parser"
	"kptv-proxy/work/proxy"
	"kptv-proxy/work/restream"
	"kptv-proxy/work/streamorder"
	"kptv-proxy/work/types"
	"kptv-proxy/work/utils"

	"github.com/gorilla/mux"
)

// StatsResponse represents comprehensive system statistics exposed through the admin API,
// providing operational metrics for monitoring, debugging, and capacity planning purposes.
// The structure includes real-time performance data, resource utilization measurements,
// and system health indicators essential for IPTV proxy administration.
type StatsResponse struct {
	TotalChannels       int     `json:"totalChannels"`
	ActiveStreams       int     `json:"activeStreams"`
	TotalSources        int     `json:"totalSources"`
	TotalEpgs           int     `json:"totalEpgs"`
	ConnectedClients    int     `json:"connectedClients"`
	Uptime              string  `json:"uptime"`
	MemoryUsage         string  `json:"memoryUsage"`
	CacheStatus         string  `json:"cacheStatus"`
	WorkerThreads       int     `json:"workerThreads"`
	TotalConnections    int     `json:"totalConnections"`
	BytesTransferred    string  `json:"bytesTransferred"`
	ActiveRestreamers   int     `json:"activeRestreamers"`
	StreamErrors        int     `json:"streamErrors"`
	ResponseTime        string  `json:"responseTime"`
	WatcherEnabled      bool    `json:"watcherEnabled"`
	UpstreamConnections int     `json:"upstreamConnections"`
	ActiveChannels      int     `json:"activeChannels"`
	AvgClientsPerStream float64 `json:"avgClientsPerStream"`
}

// ChannelResponse provides comprehensive channel information for admin interface display,
// including operational status, client statistics, and metadata for monitoring and
// management purposes. The structure supports both overview displays and detailed
// channel analysis through the administrative web interface.
type ChannelResponse struct {
	Name             string `json:"name"`             // Channel display name for user identification
	Active           bool   `json:"active"`           // Current streaming status (true=streaming, false=inactive)
	Clients          int    `json:"clients"`          // Number of clients currently connected to this channel
	BytesTransferred int64  `json:"bytesTransferred"` // Estimated data transfer volume for this channel
	CurrentSource    string `json:"currentSource"`    // Name of currently active stream source
	Group            string `json:"group"`            // Channel category/group classification
	Sources          int    `json:"sources"`          // Number of available stream sources for this channel
	URL              string `json:"url"`              // Primary stream URL (optional, not currently populated)
	LogoURL          string `json:"logoURL"`
}

// LogEntry represents individual log entries captured by the admin interface for
// real-time monitoring and debugging support. The structure provides timestamped,
// categorized log information essential for troubleshooting and operational monitoring.
type LogEntry struct {
	Timestamp string `json:"timestamp"` // Human-readable timestamp of log entry creation
	Level     string `json:"level"`     // Log severity level (info, debug, error, etc.)
	Message   string `json:"message"`   // Complete log message content for analysis
}

// StreamInfo provides detailed information about individual streams within a channel,
// including source metadata, ordering, and attributes for advanced channel management.
// This structure supports stream selection, quality assessment, and failover configuration.
type StreamInfo struct {
	Index       int               `json:"index"`       // Zero-based stream index within the channel
	URL         string            `json:"url"`         // Stream URL (potentially obfuscated for security)
	SourceName  string            `json:"sourceName"`  // Human-readable source provider name
	SourceOrder int               `json:"sourceOrder"` // Source priority order for failover operations
	Attributes  map[string]string `json:"attributes"`  // Stream metadata and extended attributes
}

// ChannelStreamsResponse provides comprehensive stream information for a specific channel,
// including current streaming state, preferred configuration, and detailed stream listings.
// This structure supports advanced channel management and stream selection operations.
type ChannelStreamsResponse struct {
	ChannelName          string       `json:"channelName"`          // Channel identifier for API operations
	CurrentStreamIndex   int          `json:"currentStreamIndex"`   // Index of currently active stream
	PreferredStreamIndex int          `json:"preferredStreamIndex"` // User/admin configured preferred stream
	Streams              []StreamInfo `json:"streams"`              // Complete list of available streams
}

// Global variables for admin interface state management and operational tracking
var (
	// adminStartTime records the admin interface initialization timestamp for uptime
	// calculation and performance monitoring across the administrative interface lifecycle.
	adminStartTime = time.Now()

	// logEntries maintains a circular buffer of recent log entries with a 1000 entry limit,
	// providing real-time debugging information through the admin interface without
	// unbounded memory growth during long-running operations.
	logEntries = make([]LogEntry, 0, 1000)
)

// Global restart coordination channel
var (
	// restartChan provides a signaling mechanism for graceful application restart
	// operations initiated through the admin interface, enabling coordinated shutdown
	// and restart sequences without abrupt process termination.
	restartChan = make(chan bool, 1)
)

// setupAdminRoutes configures all HTTP routes for the administrative web interface,
// including static file serving, API endpoints, and CORS middleware application.
// This function should be called during application initialization to enable
// web-based management capabilities.
//
// Parameters:
//   - router: configured mux router for route registration
//   - proxyInstance: StreamProxy instance for API operations
func setupAdminRoutes(router *mux.Router, proxyInstance *proxy.StreamProxy) {
	router.PathPrefix("/static/").Handler(http.StripPrefix("/static/", http.FileServer(http.Dir("/static/"))))

	router.HandleFunc("/", handleAdminInterface).Methods("GET")

	router.HandleFunc("/api/config", corsMiddleware(middleware.GzipMiddleware(handleGetConfig(proxyInstance)))).Methods("GET", "OPTIONS")
	router.HandleFunc("/api/config", corsMiddleware(handleSetConfig(proxyInstance))).Methods("POST", "OPTIONS")
	router.HandleFunc("/api/stats", corsMiddleware(middleware.GzipMiddleware(handleGetStats(proxyInstance)))).Methods("GET", "OPTIONS")
	router.HandleFunc("/api/channels", corsMiddleware(middleware.GzipMiddleware(handleGetAllChannels(proxyInstance)))).Methods("GET", "OPTIONS")
	router.HandleFunc("/api/channels/active", corsMiddleware(middleware.GzipMiddleware(handleGetActiveChannels(proxyInstance)))).Methods("GET", "OPTIONS")
	router.HandleFunc("/api/channels/{channel}/streams", corsMiddleware(middleware.GzipMiddleware(handleGetChannelStreams(proxyInstance)))).Methods("GET", "OPTIONS")
	router.HandleFunc("/api/channels/{channel}/stats", corsMiddleware(middleware.GzipMiddleware(handleGetChannelStats(proxyInstance)))).Methods("GET", "OPTIONS")
	router.HandleFunc("/api/channels/{channel}/stream", corsMiddleware(handleSetChannelStream(proxyInstance))).Methods("POST", "OPTIONS")
	router.HandleFunc("/api/channels/{channel}/kill-stream", corsMiddleware(handleKillStream(proxyInstance))).Methods("POST", "OPTIONS")
	router.HandleFunc("/api/channels/{channel}/revive-stream", corsMiddleware(handleReviveStream(proxyInstance))).Methods("POST", "OPTIONS")
	router.HandleFunc("/api/channels/{channel}/order", corsMiddleware(handleSetChannelOrder(proxyInstance))).Methods("POST", "OPTIONS")
	router.HandleFunc("/api/logs", corsMiddleware(middleware.GzipMiddleware(handleGetLogs))).Methods("GET", "OPTIONS")
	router.HandleFunc("/api/logs", corsMiddleware(handleClearLogs)).Methods("DELETE", "OPTIONS")
	router.HandleFunc("/api/restart", corsMiddleware(handleRestart)).Methods("POST", "OPTIONS")
	router.HandleFunc("/api/watcher/toggle", corsMiddleware(handleToggleWatcher(proxyInstance))).Methods("POST", "OPTIONS")

	addLogEntry("info", "Admin interface initialized")
}

// corsMiddleware provides Cross-Origin Resource Sharing (CORS) support for admin API endpoints,
// enabling web-based admin interfaces to access the API from different origins while maintaining
// security through appropriate header management and preflight request handling.
func corsMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		addLogEntry("info", fmt.Sprintf("Request: %s %s", r.Method, r.URL.Path))

		// Configure CORS headers for cross-origin support
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		// Handle preflight OPTIONS requests
		if r.Method == "OPTIONS" {
			addLogEntry("debug", fmt.Sprintf("OPTIONS request for: %s", r.URL.Path))
			w.WriteHeader(http.StatusOK)
			return
		}

		// Continue to actual handler
		next(w, r)
	}
}

// handleSetChannelOrder processes requests to update the custom ordering of streams within
// a channel, validating the new order against channel configuration and persisting changes
// to the stream order database. The handler applies changes immediately without requiring
// application restart for seamless user experience.
//
// Parameters:
//   - sp: StreamProxy instance for channel access and configuration
//
// Returns:
//   - http.HandlerFunc: handler for stream ordering update requests
func handleSetChannelOrder(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		vars := mux.Vars(r)
		channelName, err := url.PathUnescape(vars["channel"])
		if err != nil {
			http.Error(w, "Invalid channel name", http.StatusBadRequest)
			return
		}

		var request struct {
			StreamOrder []int `json:"streamOrder"`
		}

		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		channel, exists := sp.Channels.Load(channelName)
		if !exists {
			http.Error(w, "Channel not found", http.StatusNotFound)
			return
		}
		channel.Mu.RLock()
		streamCount := len(channel.Streams)
		channel.Mu.RUnlock()

		if len(request.StreamOrder) != streamCount {
			http.Error(w, "Stream order length mismatch", http.StatusBadRequest)
			return
		}

		// Only save if order is different from default [0,1,2,3...]
		isDefault := true
		for i, idx := range request.StreamOrder {
			if idx != i {
				isDefault = false
				break
			}
		}

		if isDefault {
			if sp.Config.Debug {
				addLogEntry("info", fmt.Sprintf("Removing custom order for channel %s (back to default)", channelName))
			}
			// Delete the custom order if it exists
			streamorder.DeleteChannelStreamOrder(channelName)
		} else {
			err = streamorder.SetChannelStreamOrder(channelName, request.StreamOrder)
			if err != nil {
				addLogEntry("error", fmt.Sprintf("Failed to save stream order: %v", err))
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			addLogEntry("info", fmt.Sprintf("Stream order updated for channel %s: %v", channelName, request.StreamOrder))
		}

		// Apply the new order immediately without restart
		channel.Mu.Lock()
		parser.SortStreams(channel.Streams, sp.Config, channelName)

		// Update preferred stream index to match the new first position
		atomic.StoreInt32(&channel.PreferredStreamIndex, 0)
		channel.Mu.Unlock()

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "success",
			"message": "Stream order updated and applied immediately",
		})
	}
}

// handleGetChannelStreams retrieves detailed stream information for a specific channel,
// including current streaming status, preferred configuration, and comprehensive stream
// metadata with dead stream detection for administrative management purposes.
func handleGetChannelStreams(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		vars := mux.Vars(r)
		channelName, err := url.PathUnescape(vars["channel"])
		if err != nil {
			http.Error(w, "Invalid channel name", http.StatusBadRequest)
			return
		}

		channel, exists := sp.Channels.Load(channelName)
		if !exists {
			http.Error(w, "Channel not found", http.StatusNotFound)
			return
		}
		channel.Mu.RLock()

		// Always build streams in original order first
		streams := make([]StreamInfo, len(channel.Streams))
		for i, stream := range channel.Streams {
			streams[i] = StreamInfo{
				Index:       i, // This is the original index
				URL:         utils.LogURL(sp.Config, stream.URL),
				SourceName:  stream.Source.Name,
				SourceOrder: stream.Source.Order,
				Attributes:  make(map[string]string),
			}

			for k, v := range stream.Attributes {
				streams[i].Attributes[k] = v
			}

			if deadstreams.IsStreamDead(channelName, i) {
				streams[i].Attributes["dead"] = "true"
				streams[i].Attributes["dead_reason"] = deadstreams.GetDeadStreamReason(channelName, i)
			}
		}

		currentIndex := 0
		preferredIndex := int(atomic.LoadInt32(&channel.PreferredStreamIndex))
		if channel.Restreamer != nil {
			currentIndex = int(atomic.LoadInt32(&channel.Restreamer.CurrentIndex))
		}

		response := ChannelStreamsResponse{
			ChannelName:          channelName,
			CurrentStreamIndex:   currentIndex,
			PreferredStreamIndex: preferredIndex,
			Streams:              streams,
		}

		channel.Mu.RUnlock()
		json.NewEncoder(w).Encode(response)
	}
}

// handleGetChannelStats retrieves real-time streaming statistics for a specific channel,
// including codec information, resolution, bitrate, and stream quality metrics gathered
// through FFprobe analysis for monitoring and quality assessment.
func handleGetChannelStats(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		vars := mux.Vars(r)
		channelName, err := url.PathUnescape(vars["channel"])
		if err != nil {
			http.Error(w, "Invalid channel name", http.StatusBadRequest)
			return
		}

		channel, exists := sp.Channels.Load(channelName)
		if !exists {
			http.Error(w, "Channel not found", http.StatusNotFound)
			return
		}
		channel.Mu.RLock()
		defer channel.Mu.RUnlock()

		if channel.Restreamer == nil || !channel.Restreamer.Running.Load() {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"streaming": false,
			})
			return
		}

		channel.Restreamer.Stats.Mu.RLock()
		stats := map[string]interface{}{
			"streaming":       true,
			"container":       channel.Restreamer.Stats.Container,
			"videoCodec":      channel.Restreamer.Stats.VideoCodec,
			"audioCodec":      channel.Restreamer.Stats.AudioCodec,
			"videoResolution": channel.Restreamer.Stats.VideoResolution,
			"fps":             channel.Restreamer.Stats.FPS,
			"audioChannels":   channel.Restreamer.Stats.AudioChannels,
			"bitrate":         channel.Restreamer.Stats.Bitrate,
			"streamType":      channel.Restreamer.Stats.StreamType,
			"valid":           channel.Restreamer.Stats.Valid,
			"lastUpdated":     channel.Restreamer.Stats.LastUpdated,
		}
		channel.Restreamer.Stats.Mu.RUnlock()

		json.NewEncoder(w).Encode(stats)
	}
}

// handleSetChannelStream processes manual stream switching requests, implementing
// coordinated failover operations that preserve client connections while transitioning
// to alternative stream sources. The handler supports both immediate switching for
// active streams and preference updates for future connections.
func handleSetChannelStream(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		addLogEntry("debug", fmt.Sprintf("Stream switch request: %s %s", r.Method, r.URL.Path))
		w.Header().Set("Content-Type", "application/json")

		// Extract channel name from URL parameters
		vars := mux.Vars(r)
		channelName, err := url.PathUnescape(vars["channel"])
		if err != nil {
			http.Error(w, "Invalid channel name", http.StatusBadRequest)
			return
		}

		// Parse request body for stream index
		var request struct {
			StreamIndex int `json:"streamIndex"`
		}

		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		// Locate and validate channel
		channel, exists := sp.Channels.Load(channelName)
		if !exists {
			http.Error(w, "Channel not found", http.StatusNotFound)
			return
		}
		channel.Mu.Lock()

		// Validate stream index bounds
		if request.StreamIndex < 0 || request.StreamIndex >= len(channel.Streams) {
			channel.Mu.Unlock()
			http.Error(w, "Invalid stream index", http.StatusBadRequest)
			return
		}

		addLogEntry("info", fmt.Sprintf("Stream change request for channel %s to index %d", channelName, request.StreamIndex))

		// Update preferred stream index first
		atomic.StoreInt32(&channel.PreferredStreamIndex, int32(request.StreamIndex))

		// Handle active restreamer with forced switch
		if channel.Restreamer != nil && channel.Restreamer.Running.Load() {
			addLogEntry("info", fmt.Sprintf("Forcing stream switch for channel %s to index %d", channelName, request.StreamIndex))

			// Verify client presence before switch
			clientCount := 0
			channel.Restreamer.Clients.Range(func(_ string, _ *types.RestreamClient) bool {
				clientCount++
				return true
			})

			if clientCount > 0 {
				// Use the ForceStreamSwitch method instead of manual restart
				r := &restream.Restream{Restreamer: channel.Restreamer}
				r.ForceStreamSwitch(request.StreamIndex)

				addLogEntry("info", fmt.Sprintf("Stream switch initiated for channel %s to index %d with %d clients", channelName, request.StreamIndex, clientCount))
			} else {
				// Just update the index if no clients
				atomic.StoreInt32(&channel.Restreamer.CurrentIndex, int32(request.StreamIndex))
			}
		}

		channel.Mu.Unlock()

		// Send success response
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":      "success",
			"message":     fmt.Sprintf("Stream changed to index %d", request.StreamIndex),
			"streamIndex": request.StreamIndex,
		})
	}
}

// handleToggleWatcher processes requests to enable or disable the stream watcher system,
// persisting the configuration change to disk and starting or stopping the watcher
// infrastructure immediately without requiring application restart.
//
// Parameters:
//   - sp: StreamProxy instance containing watcher manager
//
// Returns:
//   - http.HandlerFunc: handler for watcher toggle requests
func handleToggleWatcher(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		var request struct {
			Enabled bool `json:"enabled"`
		}

		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		// Update memory config
		sp.Config.WatcherEnabled = request.Enabled

		// CRITICAL: Save to config file
		configPath := "/settings/config.json"

		// Read current config from file
		data, err := os.ReadFile(configPath)
		if err != nil {
			addLogEntry("error", fmt.Sprintf("Failed to read config file: %v", err))
			http.Error(w, "Failed to read config file", http.StatusInternalServerError)
			return
		}

		// Parse config
		var configFile config.ConfigFile
		if err := json.Unmarshal(data, &configFile); err != nil {
			addLogEntry("error", fmt.Sprintf("Failed to parse config: %v", err))
			http.Error(w, "Failed to parse config", http.StatusInternalServerError)
			return
		}

		// Update watcher setting
		configFile.WatcherEnabled = request.Enabled

		// Write back to file
		newData, err := json.MarshalIndent(configFile, "", "  ")
		if err != nil {
			addLogEntry("error", fmt.Sprintf("Failed to marshal config: %v", err))
			http.Error(w, "Failed to marshal config", http.StatusInternalServerError)
			return
		}

		if err := os.WriteFile(configPath, newData, 0644); err != nil {
			addLogEntry("error", fmt.Sprintf("Failed to write config file: %v", err))
			http.Error(w, "Failed to write config file", http.StatusInternalServerError)
			return
		}

		// Start/stop watcher
		if request.Enabled {
			sp.WatcherManager.Start()
			addLogEntry("info", "Stream watcher enabled via admin interface")
		} else {
			sp.WatcherManager.Stop()
			addLogEntry("info", "Stream watcher disabled via admin interface")
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":         "success",
			"watcherEnabled": request.Enabled,
		})
	}
}

// handleAdminInterface serves the main admin HTML page
func handleAdminInterface(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "/static/admin.html")
}

// handleGetConfig retrieves the current system configuration directly from the
// configuration file, ensuring that the admin interface displays the persistent
// configuration state rather than potentially modified runtime values.
func handleGetConfig(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// Read configuration directly from file for accuracy
		configPath := "/settings/config.json"
		data, err := os.ReadFile(configPath)
		if err != nil {
			addLogEntry("error", fmt.Sprintf("Failed to read config file: %v", err))
			http.Error(w, "Failed to read config file", http.StatusInternalServerError)
			return
		}

		// Return raw file content as JSON
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	}
}

// handleSetConfig processes configuration updates through the admin interface,
// implementing atomic file operations and comprehensive validation to ensure
// configuration consistency and system stability during updates.
func handleSetConfig(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		addLogEntry("info", "POST /api/config received")

		// Panic recovery for robust error handling
		defer func() {
			if err := recover(); err != nil {
				addLogEntry("error", fmt.Sprintf("PANIC in handleSetConfig: %v", err))
				http.Error(w, "Internal server error", http.StatusInternalServerError)
			}
		}()

		w.Header().Set("Content-Type", "application/json")

		// Read and log request body for debugging
		body, err := io.ReadAll(r.Body)
		if err != nil {
			addLogEntry("error", fmt.Sprintf("Failed to read request body: %v", err))
			http.Error(w, "Failed to read body", http.StatusBadRequest)
			return
		}
		addLogEntry("info", fmt.Sprintf("Request body length: %d bytes", len(body)))

		// Reset body reader for JSON decoder
		r.Body = io.NopCloser(bytes.NewBuffer(body))

		// Clear all filters when config is updated
		// This ensures any changed regex patterns are recompiled
		if sp != nil && sp.FilterManager != nil {
			sp.FilterManager.ClearFilters()
			addLogEntry("info", "Cleared all compiled regex filters due to config update")
		}

		// Parse and validate configuration
		var configFile config.ConfigFile
		if err := json.NewDecoder(r.Body).Decode(&configFile); err != nil {
			addLogEntry("error", fmt.Sprintf("JSON decode error: %v", err))
			http.Error(w, "Invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}

		// Validate required fields
		if configFile.BaseURL == "" {
			addLogEntry("error", "Base URL is required but empty")
			http.Error(w, "Base URL is required", http.StatusBadRequest)
			return
		}

		// Ensure FFmpeg arrays are initialized
		if configFile.FFmpegPreInput == nil {
			configFile.FFmpegPreInput = []string{}
		}
		if configFile.FFmpegPreOutput == nil {
			configFile.FFmpegPreOutput = []string{}
		}

		// Atomic file write using temporary file
		configPath := "/settings/config.json"
		tempPath := "/settings/config.json.tmp"

		data, err := json.MarshalIndent(configFile, "", "  ")
		if err != nil {
			addLogEntry("error", fmt.Sprintf("Failed to marshal config: %v", err))
			http.Error(w, "Failed to marshal config", http.StatusInternalServerError)
			return
		}

		// Write to temporary file first
		addLogEntry("info", fmt.Sprintf("Attempting to write to temp file: %s", tempPath))
		if err := os.WriteFile(tempPath, data, 0644); err != nil {
			addLogEntry("error", fmt.Sprintf("Failed to write temp file: %v", err))
			http.Error(w, "Failed to write temp file: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// Atomic move to final location
		addLogEntry("info", fmt.Sprintf("Moving temp file to: %s", configPath))
		if err := os.Rename(tempPath, configPath); err != nil {
			addLogEntry("error", fmt.Sprintf("Failed to move temp file: %v", err))
			http.Error(w, "Failed to move config file: "+err.Error(), http.StatusInternalServerError)
			return
		}

		addLogEntry("info", "Configuration updated via admin interface")

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "success"})
	}
}

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
		activeChannels := 0 // NEW

		sp.Channels.Range(func(key string, value *types.Channel) bool {
			totalChannels++
			channel := value
			channel.Mu.RLock()
			if channel.Restreamer != nil && channel.Restreamer.Running.Load() {
				activeStreams++
				activeRestreamers++
				upstreamConnections++
				activeChannels++ // NEW - count active channels
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

		// Calculate average clients per stream  // NEW
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
			ActiveChannels:      activeChannels,      // NEW
			AvgClientsPerStream: avgClientsPerStream, // NEW
		}

		if err := json.NewEncoder(w).Encode(stats); err != nil {
			addLogEntry("error", fmt.Sprintf("Failed to encode stats: %v", err))
			http.Error(w, "Failed to encode stats", http.StatusInternalServerError)
		}
	}
}

// handleGetAllChannels retrieves comprehensive information about all channels
// in the system, including operational status and metadata for administrative
// overview and management purposes.
func handleGetAllChannels(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		var channels []ChannelResponse

		sp.Channels.Range(func(key string, value *types.Channel) bool {
			channel := value
			channel.Mu.RLock()

			// Determine channel activity status
			hasRestreamer := channel.Restreamer != nil
			isRunning := false
			if hasRestreamer {
				isRunning = channel.Restreamer.Running.Load()
			}

			// addLogEntry("debug", fmt.Sprintf("Channel '%s': hasRestreamer=%v, isRunning=%v", channelName, hasRestreamer, isRunning))

			// Calculate active status and client count
			active := hasRestreamer && isRunning
			clients := 0
			if active {
				channel.Restreamer.Clients.Range(func(_ string, _ *types.RestreamClient) bool {
					clients++
					return true
				})
			}

			// Extract group information from stream attributes
			group := "Uncategorized"
			logoURL := "https://cdn.kcp.im/tv/kptv-icon.png" // Default logo
			if len(channel.Streams) > 0 {
				if g, ok := channel.Streams[0].Attributes["group-title"]; ok && g != "" {
					group = g
				} else if g, ok := channel.Streams[0].Attributes["tvg-group"]; ok && g != "" {
					group = g
				}

				// Get logo URL
				if logo, ok := channel.Streams[0].Attributes["tvg-logo"]; ok && logo != "" {
					logoURL = logo
				}
			}

			channels = append(channels, ChannelResponse{
				Name:    channel.Name,
				Active:  active,
				Clients: clients,
				Group:   group,
				Sources: len(channel.Streams),
				LogoURL: logoURL,
			})

			channel.Mu.RUnlock()
			return true
		})

		if err := json.NewEncoder(w).Encode(channels); err != nil {
			addLogEntry("error", fmt.Sprintf("Failed to encode channels: %v", err))
			http.Error(w, "Failed to encode channels", http.StatusInternalServerError)
		}
	}
}

// handleGetActiveChannels retrieves information specifically about channels that
// are currently streaming content to connected clients, providing focused monitoring
// data for operational assessment and troubleshooting.
func handleGetActiveChannels(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		var channels []ChannelResponse

		sp.Channels.Range(func(key string, value *types.Channel) bool {
			channel := value
			channel.Mu.RLock()

			// Include only active streaming channels
			if channel.Restreamer != nil && channel.Restreamer.Running.Load() {
				// Count connected clients
				clients := 0
				channel.Restreamer.Clients.Range(func(_ string, _ *types.RestreamClient) bool {
					clients++
					return true
				})

				// Determine current source information
				currentSource := "Unknown"
				if len(channel.Streams) > 0 {
					currentIdx := channel.Restreamer.CurrentIndex
					if int(currentIdx) < len(channel.Streams) {
						currentSource = channel.Streams[currentIdx].Source.Name
					}
				}

				// Estimate bytes transferred based on activity
				activityTime := time.Since(time.Unix(channel.Restreamer.LastActivity.Load(), 0))
				estimatedBytes := int64(0)
				logoURL := "https://cdn.kcp.im/tv/kptv-icon.png" // Default logo

				if activityTime < 60*time.Second && clients > 0 {
					// Base estimation: 500KB per client plus variance
					baseRate := int64(500 * 1024 * clients)
					variance := int64(len(channel.Name) * 100 * 1024) // Simple variance calculation
					estimatedBytes = baseRate + variance
				}

				// Get logo URL from first stream
				if len(channel.Streams) > 0 {
					if logo, ok := channel.Streams[0].Attributes["tvg-logo"]; ok && logo != "" {
						logoURL = logo
					}
				}

				channels = append(channels, ChannelResponse{
					Name:             channel.Name,
					Active:           true,
					Clients:          clients,
					CurrentSource:    currentSource,
					BytesTransferred: estimatedBytes,
					LogoURL:          logoURL, // Add this line
				})
			}

			channel.Mu.RUnlock()
			return true
		})

		if err := json.NewEncoder(w).Encode(channels); err != nil {
			addLogEntry("error", fmt.Sprintf("Failed to encode active channels: %v", err))
			http.Error(w, "Failed to encode active channels", http.StatusInternalServerError)
		}
	}
}

// handleGetLogs retrieves the current log buffer for admin interface display
func handleGetLogs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if err := json.NewEncoder(w).Encode(logEntries); err != nil {
		http.Error(w, "Failed to encode logs", http.StatusInternalServerError)
	}
}

// handleClearLogs clears the admin log buffer and records the clearing action
func handleClearLogs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	logEntries = logEntries[:0]
	addLogEntry("info", "Log entries cleared via admin interface")

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

// handleRestart initiates a graceful application restart through coordination
// channel signaling, allowing the main application to handle the restart sequence
// cleanly without abrupt termination.
func handleRestart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	addLogEntry("info", "Restart requested via admin interface - triggering graceful restart")

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "restart_initiated",
		"message": "Restarting KPTV Proxy process...",
	})

	// Trigger restart signal after brief delay
	go func() {
		time.Sleep(500 * time.Millisecond)
		restartChan <- true
	}()
}

// addLogEntry adds a new entry to the admin log buffer with automatic size management
func addLogEntry(level, message string) {
	entry := LogEntry{
		Timestamp: time.Now().Format("2006-01-02 15:04:05"),
		Level:     level,
		Message:   message,
	}

	logEntries = append(logEntries, entry)

	// Maintain circular buffer with 1000 entry limit
	if len(logEntries) > 1000 {
		logEntries = logEntries[len(logEntries)-1000:]
	}
}

// formatDuration converts time.Duration to human-readable format
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	} else if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	} else if d < 24*time.Hour {
		hours := int(d.Hours())
		minutes := int(d.Minutes()) % 60
		return fmt.Sprintf("%dh %dm", hours, minutes)
	} else {
		days := int(d.Hours()) / 24
		hours := int(d.Hours()) % 24
		return fmt.Sprintf("%dd %dh", days, hours)
	}
}

// LogWithAdmin provides enhanced logging that captures output for both standard
// logging and admin interface display
func LogWithAdmin(logger *log.Logger, level, format string, args ...interface{}) {
	message := fmt.Sprintf(format, args...)
	logger.Printf("[%s] %s", strings.ToUpper(level), message)
	addLogEntry(level, message)
}

// AdminLog provides direct admin log entry creation for admin-specific events
func AdminLog(level, message string) {
	addLogEntry(level, message)
}

// handleKillStream manually marks a stream as dead in the dead streams database
func handleKillStream(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		vars := mux.Vars(r)
		channelName, err := url.PathUnescape(vars["channel"])
		if err != nil {
			http.Error(w, "Invalid channel name", http.StatusBadRequest)
			return
		}

		var request struct {
			StreamIndex int `json:"streamIndex"`
		}

		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		// Get stream details from proxy
		channel, exists := sp.Channels.Load(channelName)
		if !exists {
			http.Error(w, "Channel not found", http.StatusNotFound)
			return
		}
		channel.Mu.RLock()

		if request.StreamIndex >= len(channel.Streams) {
			channel.Mu.RUnlock()
			http.Error(w, "Invalid stream index", http.StatusBadRequest)
			return
		}

		stream := channel.Streams[request.StreamIndex]
		url := stream.URL
		sourceName := stream.Source.Name
		channel.Mu.RUnlock()

		// Mark stream as dead with manual reason
		if err := deadstreams.MarkStreamDead(channelName, request.StreamIndex, url, sourceName, "manual"); err != nil {
			addLogEntry("error", fmt.Sprintf("Failed to kill stream: %v", err))
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		addLogEntry("info", fmt.Sprintf("Stream %d manually marked as dead for channel %s", request.StreamIndex, channelName))

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "success",
			"message": fmt.Sprintf("Stream %d marked as dead", request.StreamIndex),
		})
	}
}

// handleReviveStream removes a stream from the dead streams database
func handleReviveStream(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		vars := mux.Vars(r)
		channelName, err := url.PathUnescape(vars["channel"])
		if err != nil {
			http.Error(w, "Invalid channel name", http.StatusBadRequest)
			return
		}

		var request struct {
			StreamIndex int `json:"streamIndex"`
		}

		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		// Revive stream by removing from dead streams database
		if err := deadstreams.ReviveStream(channelName, request.StreamIndex); err != nil {
			addLogEntry("error", fmt.Sprintf("Failed to revive stream: %v", err))
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		addLogEntry("info", fmt.Sprintf("Stream %d revived for channel %s", request.StreamIndex, channelName))

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "success",
			"message": fmt.Sprintf("Stream %d revived", request.StreamIndex),
		})
	}
}
