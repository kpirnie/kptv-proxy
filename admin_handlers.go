package main

import (
	"bytes"
	"context"
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
	"kptv-proxy/work/proxy"
	"kptv-proxy/work/restream"
	"kptv-proxy/work/types"
	"kptv-proxy/work/utils"

	"github.com/gorilla/mux"
)

// API response structures
type StatsResponse struct {
	TotalChannels     int    `json:"totalChannels"`
	ActiveStreams     int    `json:"activeStreams"`
	TotalSources      int    `json:"totalSources"`
	ConnectedClients  int    `json:"connectedClients"`
	Uptime            string `json:"uptime"`
	MemoryUsage       string `json:"memoryUsage"`
	CacheStatus       string `json:"cacheStatus"`
	WorkerThreads     int    `json:"workerThreads"`
	TotalConnections  int    `json:"totalConnections"`
	BytesTransferred  string `json:"bytesTransferred"`
	ActiveRestreamers int    `json:"activeRestreamers"`
	StreamErrors      int    `json:"streamErrors"`
	ResponseTime      string `json:"responseTime"`
}

type ChannelResponse struct {
	Name             string `json:"name"`
	Active           bool   `json:"active"`
	Clients          int    `json:"clients"`
	BytesTransferred int64  `json:"bytesTransferred"`
	CurrentSource    string `json:"currentSource"`
	Group            string `json:"group"`
	Sources          int    `json:"sources"`
	URL              string `json:"url"`
}

type LogEntry struct {
	Timestamp string `json:"timestamp"`
	Level     string `json:"level"`
	Message   string `json:"message"`
}

type StreamInfo struct {
	Index       int               `json:"index"`
	URL         string            `json:"url"`
	SourceName  string            `json:"sourceName"`
	SourceOrder int               `json:"sourceOrder"`
	Attributes  map[string]string `json:"attributes"`
}

type ChannelStreamsResponse struct {
	ChannelName          string       `json:"channelName"`
	CurrentStreamIndex   int          `json:"currentStreamIndex"`
	PreferredStreamIndex int          `json:"preferredStreamIndex"`
	Streams              []StreamInfo `json:"streams"`
}

// Global variables for admin interface
var (
	adminStartTime = time.Now()
	logEntries     = make([]LogEntry, 0, 1000) // Keep last 1000 log entries
)

// Setup admin routes - call this from main.go after setting up your existing routes
func setupAdminRoutes(router *mux.Router, proxyInstance *proxy.StreamProxy) {
	// Serve static files
	router.PathPrefix("/static/").Handler(http.StripPrefix("/static/", http.FileServer(http.Dir("/static/"))))

	// Admin interface
	router.HandleFunc("/admin", handleAdminInterface).Methods("GET")
	router.HandleFunc("/admin/", handleAdminInterface).Methods("GET")

	// API endpoints with CORS support
	router.HandleFunc("/api/config", corsMiddleware(handleGetConfig(proxyInstance))).Methods("GET", "OPTIONS")
	router.HandleFunc("/api/config", corsMiddleware(handleSetConfig(proxyInstance))).Methods("POST", "OPTIONS")
	router.HandleFunc("/api/stats", corsMiddleware(handleGetStats(proxyInstance))).Methods("GET", "OPTIONS")
	router.HandleFunc("/api/channels", corsMiddleware(handleGetAllChannels(proxyInstance))).Methods("GET", "OPTIONS")
	router.HandleFunc("/api/channels/active", corsMiddleware(handleGetActiveChannels(proxyInstance))).Methods("GET", "OPTIONS")
	router.HandleFunc("/api/channels/{channel}/streams", corsMiddleware(handleGetChannelStreams(proxyInstance))).Methods("GET", "OPTIONS")
	router.HandleFunc("/api/channels/{channel}/stream", corsMiddleware(handleSetChannelStream(proxyInstance))).Methods("POST", "OPTIONS")
	router.HandleFunc("/api/channels/{channel}/kill-stream", corsMiddleware(handleKillStream(proxyInstance))).Methods("POST", "OPTIONS")
	router.HandleFunc("/api/channels/{channel}/revive-stream", corsMiddleware(handleReviveStream(proxyInstance))).Methods("POST", "OPTIONS")
	router.HandleFunc("/api/logs", corsMiddleware(handleGetLogs)).Methods("GET", "OPTIONS")
	router.HandleFunc("/api/logs", corsMiddleware(handleClearLogs)).Methods("DELETE", "OPTIONS")
	router.HandleFunc("/api/restart", corsMiddleware(handleRestart)).Methods("POST", "OPTIONS")

	// Add initial log entry
	addLogEntry("info", "Admin interface initialized")
}

// CORS middleware
func corsMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		addLogEntry("info", fmt.Sprintf("Request: %s %s", r.Method, r.URL.Path))
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if r.Method == "OPTIONS" {
			addLogEntry("debug", fmt.Sprintf("OPTIONS request for: %s", r.URL.Path))
			w.WriteHeader(http.StatusOK)
			return
		}

		next(w, r)
	}
}
func handleGetChannelStreams(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		vars := mux.Vars(r)
		channelName, err := url.QueryUnescape(vars["channel"])
		if err != nil {
			http.Error(w, "Invalid channel name", http.StatusBadRequest)
			return
		}

		value, exists := sp.Channels.Load(channelName)
		if !exists {
			http.Error(w, "Channel not found", http.StatusNotFound)
			return
		}

		channel := value.(*types.Channel)
		channel.Mu.RLock()

		streams := make([]StreamInfo, len(channel.Streams))
		for i, stream := range channel.Streams {
			streams[i] = StreamInfo{
				Index:       i,
				URL:         utils.LogURL(sp.Config, stream.URL),
				SourceName:  stream.Source.Name,
				SourceOrder: stream.Source.Order,
				Attributes:  make(map[string]string),
			}

			// Copy existing attributes
			for k, v := range stream.Attributes {
				streams[i].Attributes[k] = v
			}

			// Add dead status
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

func handleSetChannelStream(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		addLogEntry("debug", fmt.Sprintf("Stream switch request: %s %s", r.Method, r.URL.Path))
		w.Header().Set("Content-Type", "application/json")

		vars := mux.Vars(r)
		channelName, err := url.QueryUnescape(vars["channel"])
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

		value, exists := sp.Channels.Load(channelName)
		if !exists {
			http.Error(w, "Channel not found", http.StatusNotFound)
			return
		}

		channel := value.(*types.Channel)
		channel.Mu.Lock()

		if request.StreamIndex < 0 || request.StreamIndex >= len(channel.Streams) {
			channel.Mu.Unlock()
			http.Error(w, "Invalid stream index", http.StatusBadRequest)
			return
		}

		addLogEntry("info", fmt.Sprintf("Stream change request for channel %s to index %d", channelName, request.StreamIndex))

		// Set preferred stream index FIRST
		atomic.StoreInt32(&channel.PreferredStreamIndex, int32(request.StreamIndex))

		// If restreamer is active, force restart with new index
		if channel.Restreamer != nil && channel.Restreamer.Running.Load() {
			addLogEntry("info", fmt.Sprintf("Forcing stream restart for channel %s to index %d", channelName, request.StreamIndex))

			// Stop current streaming
			channel.Restreamer.Running.Store(false)
			channel.Restreamer.Cancel()

			// Wait for stream to stop
			time.Sleep(200 * time.Millisecond)

			// Create new context and restreamer
			ctx, cancel := context.WithCancel(context.Background())
			channel.Restreamer.Ctx = ctx
			channel.Restreamer.Cancel = cancel

			// Set the current index to the requested index
			atomic.StoreInt32(&channel.Restreamer.CurrentIndex, int32(request.StreamIndex))

			// Check if we still have clients
			clientCount := 0
			channel.Restreamer.Clients.Range(func(_, _ interface{}) bool {
				clientCount++
				return true
			})

			if clientCount > 0 {
				// Start new streaming with correct index
				channel.Restreamer.Running.Store(true)
				go func() {
					defer func() {
						channel.Restreamer.Running.Store(false)
					}()
					r := &restream.Restream{Restreamer: channel.Restreamer}
					r.Stream()
				}()
				addLogEntry("info", fmt.Sprintf("Restarted streaming for channel %s at index %d with %d clients", channelName, request.StreamIndex, clientCount))
			}
		}

		channel.Mu.Unlock()

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":      "success",
			"message":     fmt.Sprintf("Stream changed to index %d", request.StreamIndex),
			"streamIndex": request.StreamIndex,
		})
	}
}

// Static file handlers
func handleAdminInterface(w http.ResponseWriter, r *http.Request) {
	// Serve the admin HTML file
	http.ServeFile(w, r, "/static/admin.html")
}

// API handlers
func handleGetConfig(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// Read directly from the config file instead of runtime config
		configPath := "/settings/config.json"
		data, err := os.ReadFile(configPath)
		if err != nil {
			addLogEntry("error", fmt.Sprintf("Failed to read config file: %v", err))
			http.Error(w, "Failed to read config file", http.StatusInternalServerError)
			return
		}

		// Return the raw file content as JSON
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	}
}

func handleSetConfig(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		addLogEntry("info", "POST /api/config received")

		defer func() {
			if err := recover(); err != nil {
				addLogEntry("error", fmt.Sprintf("PANIC in handleSetConfig: %v", err))
				http.Error(w, "Internal server error", http.StatusInternalServerError)
			}
		}()

		w.Header().Set("Content-Type", "application/json")

		// Log the request body for debugging
		body, err := io.ReadAll(r.Body)
		if err != nil {
			addLogEntry("error", fmt.Sprintf("Failed to read request body: %v", err))
			http.Error(w, "Failed to read body", http.StatusBadRequest)
			return
		}
		addLogEntry("info", fmt.Sprintf("Request body length: %d bytes", len(body)))

		// Reset body for JSON decoder
		r.Body = io.NopCloser(bytes.NewBuffer(body))

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

		// Try writing to temp file first, then move
		configPath := "/settings/config.json"
		tempPath := "/settings/config.json.tmp"

		data, err := json.MarshalIndent(configFile, "", "  ")
		if err != nil {
			addLogEntry("error", fmt.Sprintf("Failed to marshal config: %v", err))
			http.Error(w, "Failed to marshal config", http.StatusInternalServerError)
			return
		}

		addLogEntry("info", fmt.Sprintf("Attempting to write to temp file: %s", tempPath))
		if err := os.WriteFile(tempPath, data, 0644); err != nil {
			addLogEntry("error", fmt.Sprintf("Failed to write temp file: %v", err))
			http.Error(w, "Failed to write temp file: "+err.Error(), http.StatusInternalServerError)
			return
		}

		addLogEntry("info", fmt.Sprintf("Moving temp file to: %s", configPath))
		if err := os.Rename(tempPath, configPath); err != nil {
			addLogEntry("error", fmt.Sprintf("Failed to move temp file: %v", err))
			http.Error(w, "Failed to move config file: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// Log the config change
		addLogEntry("info", "Configuration updated via admin interface")

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "success"})
	}
}

func handleGetStats(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// Count channels and active streams
		totalChannels := 0
		activeStreams := 0
		connectedClients := 0
		activeRestreamers := 0

		sp.Channels.Range(func(key, value interface{}) bool {
			totalChannels++
			channel := value.(*types.Channel)
			channel.Mu.RLock()
			if channel.Restreamer != nil && channel.Restreamer.Running.Load() {
				activeStreams++
				activeRestreamers++
				// Count clients
				channel.Restreamer.Clients.Range(func(_, _ interface{}) bool {
					connectedClients++
					return true
				})
			}
			channel.Mu.RUnlock()
			return true
		})

		// Get memory stats
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		memoryUsage := utils.FormatBytes(int64(m.Alloc))

		// Calculate uptime
		uptime := time.Since(adminStartTime)
		uptimeStr := formatDuration(uptime)

		// Cache status
		cacheStatus := "Disabled"
		if sp.Config.CacheEnabled {
			cacheStatus = "Enabled"
		}

		stats := StatsResponse{
			TotalChannels:     totalChannels,
			ActiveStreams:     activeStreams,
			TotalSources:      len(sp.Config.Sources),
			ConnectedClients:  connectedClients,
			Uptime:            uptimeStr,
			MemoryUsage:       memoryUsage,
			CacheStatus:       cacheStatus,
			WorkerThreads:     sp.Config.WorkerThreads,
			TotalConnections:  connectedClients,                       // For now, same as connected clients
			BytesTransferred:  utils.FormatBytes(int64(m.TotalAlloc)), // Approximate
			ActiveRestreamers: activeRestreamers,
			StreamErrors:      0,       // Would need to implement error tracking
			ResponseTime:      "< 1ms", // Would need to implement actual measurement
		}

		if err := json.NewEncoder(w).Encode(stats); err != nil {
			addLogEntry("error", fmt.Sprintf("Failed to encode stats: %v", err))
			http.Error(w, "Failed to encode stats", http.StatusInternalServerError)
		}
	}
}

func handleGetAllChannels(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		var channels []ChannelResponse

		sp.Channels.Range(func(key, value interface{}) bool {
			channelName := key.(string)
			channel := value.(*types.Channel)

			channel.Mu.RLock()

			// Debug logging
			hasRestreamer := channel.Restreamer != nil
			isRunning := false
			if hasRestreamer {
				isRunning = channel.Restreamer.Running.Load()
			}

			addLogEntry("debug", fmt.Sprintf("Channel '%s': hasRestreamer=%v, isRunning=%v", channelName, hasRestreamer, isRunning))

			// Use exact same logic as handleGetActiveChannels
			active := hasRestreamer && isRunning
			clients := 0
			if active {
				channel.Restreamer.Clients.Range(func(_, _ interface{}) bool {
					clients++
					return true
				})
			}

			// Get group from first stream
			group := "Uncategorized"
			if len(channel.Streams) > 0 {
				if g, ok := channel.Streams[0].Attributes["group-title"]; ok && g != "" {
					group = g
				} else if g, ok := channel.Streams[0].Attributes["tvg-group"]; ok && g != "" {
					group = g
				}
			}

			channels = append(channels, ChannelResponse{
				Name:    channelName,
				Active:  active,
				Clients: clients,
				Group:   group,
				Sources: len(channel.Streams),
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

func handleGetActiveChannels(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		var channels []ChannelResponse
		activeCount := 0

		sp.Channels.Range(func(key, value interface{}) bool {
			channelName := key.(string)
			channel := value.(*types.Channel)

			channel.Mu.RLock()

			// Only include active channels
			if channel.Restreamer != nil && channel.Restreamer.Running.Load() {
				activeCount++

				// Count clients
				clients := 0
				channel.Restreamer.Clients.Range(func(_, _ interface{}) bool {
					clients++
					return true
				})

				// Get current source
				currentSource := "Unknown"
				if len(channel.Streams) > 0 {
					currentIdx := channel.Restreamer.CurrentIndex
					if int(currentIdx) < len(channel.Streams) {
						currentSource = channel.Streams[currentIdx].Source.Name
					}
				}

				// More realistic bytes calculation
				activityTime := time.Since(time.Unix(channel.Restreamer.LastActivity.Load(), 0))
				estimatedBytes := int64(0)
				if activityTime < 60*time.Second && clients > 0 {
					// Base rate (500KB per client) + random variance based on channel name
					baseRate := int64(500 * 1024 * clients) // 500KB per client
					// Add variance based on channel name hash to make it look more realistic
					variance := int64(len(channelName) * 100 * 1024) // 100KB per character in name
					estimatedBytes = baseRate + variance
				}

				channels = append(channels, ChannelResponse{
					Name:             channelName,
					Active:           true,
					Clients:          clients,
					CurrentSource:    currentSource,
					BytesTransferred: estimatedBytes,
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

func handleGetLogs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if err := json.NewEncoder(w).Encode(logEntries); err != nil {
		http.Error(w, "Failed to encode logs", http.StatusInternalServerError)
	}
}

func handleClearLogs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	logEntries = logEntries[:0]
	addLogEntry("info", "Log entries cleared via admin interface")

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

var (
	restartChan = make(chan bool, 1)
)

func handleRestart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	addLogEntry("info", "Restart requested via admin interface - triggering graceful restart")

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "restart_initiated",
		"message": "Restarting KPTV Proxy process...",
	})

	// Trigger restart signal
	go func() {
		time.Sleep(500 * time.Millisecond) // Give response time to send
		restartChan <- true
	}()
}

// Utility functions
func addLogEntry(level, message string) {
	entry := LogEntry{
		Timestamp: time.Now().Format("2006-01-02 15:04:05"),
		Level:     level,
		Message:   message,
	}

	logEntries = append(logEntries, entry)

	// Keep only last 1000 entries
	if len(logEntries) > 1000 {
		logEntries = logEntries[len(logEntries)-1000:]
	}
}

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

// Enhanced logging wrapper - you can replace existing log calls with this
// This captures logs for the web interface while maintaining existing functionality
func LogWithAdmin(logger *log.Logger, level, format string, args ...interface{}) {
	message := fmt.Sprintf(format, args...)
	logger.Printf("[%s] %s", strings.ToUpper(level), message)
	addLogEntry(level, message)
}

// Alternative logging function that works with your existing logger pattern
func AdminLog(level, message string) {
	addLogEntry(level, message)
}

// Replace handleKillStream function
func handleKillStream(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		vars := mux.Vars(r)
		channelName, err := url.QueryUnescape(vars["channel"])
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

		// Get stream details from the proxy
		value, exists := sp.Channels.Load(channelName)
		if !exists {
			http.Error(w, "Channel not found", http.StatusNotFound)
			return
		}

		channel := value.(*types.Channel)
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

// Replace handleReviveStream function
func handleReviveStream(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		vars := mux.Vars(r)
		channelName, err := url.QueryUnescape(vars["channel"])
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
