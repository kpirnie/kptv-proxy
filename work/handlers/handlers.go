package handlers

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"

	"kptv-proxy/work/proxy"
)

func (sp *proxy.StreamProxy) handlePlaylist(w http.ResponseWriter, r *http.Request) {
	sp.GeneratePlaylist(w, r, "")
}

func (sp *proxy.StreamProxy) handleGroupPlaylist(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	group := vars["group"]
	sp.GeneratePlaylist(w, r, group)
}

func (sp *proxy.StreamProxy) generatePlaylist(w http.ResponseWriter, r *http.Request, groupFilter string) {
	if groupFilter == "" {
		sp.logger.Println("Handling playlist request (all groups)")
	} else {
		sp.logger.Printf("Handling playlist request for group: %s", groupFilter)
	}

	// Rate limit
	sp.rateLimiter.Take()

	// Create cache key
	cacheKey := "playlist"
	if groupFilter != "" {
		cacheKey = "playlist_" + strings.ToLower(groupFilter)
	}

	// Check cache
	if sp.config.CacheEnabled {
		if cached, ok := sp.cache.getM3U8(cacheKey); ok {
			w.Header().Set("Content-Type", "application/x-mpegURL")
			w.Header().Set("Cache-Control", "no-cache")
			w.Write([]byte(cached))
			return
		}
	}

	var playlist strings.Builder
	playlist.WriteString("#EXTM3U\n")

	filteredCount := 0
	totalCount := 0

	// Iterate through channels
	sp.channels.Range(func(key, value interface{}) bool {
		channelName := key.(string)
		channel := value.(*Channel)
		totalCount++

		channel.mu.RLock()
		if len(channel.Streams) > 0 {
			// Use attributes from first stream
			attrs := channel.Streams[0].Attributes

			// Apply group filter if specified
			if groupFilter != "" {
				channelGroup := sp.getChannelGroup(attrs)
				if !strings.EqualFold(channelGroup, groupFilter) {
					channel.mu.RUnlock()
					return true // Continue to next channel
				}
			}

			filteredCount++

			playlist.WriteString("#EXTINF:-1")
			for key, value := range attrs {
				if key != "tvg-name" && key != "duration" {
					// Ensure values are properly quoted if they contain special characters
					if strings.ContainsAny(value, ",\"") {
						value = fmt.Sprintf("%q", value)
					}
					playlist.WriteString(fmt.Sprintf(" %s=\"%s\"", key, value))
				}
			}

			// Clean channel name for display
			cleanName := strings.Trim(channelName, "\"")
			playlist.WriteString(fmt.Sprintf(",%s\n", cleanName))

			// Generate proxy URL
			safeName := sanitizeChannelName(channelName)
			proxyURL := fmt.Sprintf("%s/stream/%s", sp.config.BaseURL, safeName)
			playlist.WriteString(proxyURL + "\n")
		}
		channel.mu.RUnlock()
		return true
	})

	result := playlist.String()

	// Cache result
	if sp.config.CacheEnabled {
		sp.cache.setM3U8(cacheKey, result)
	}

	w.Header().Set("Content-Type", "application/x-mpegURL")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write([]byte(result))

	if groupFilter == "" {
		sp.logger.Printf("Generated playlist with %d channels", totalCount)
	} else {
		sp.logger.Printf("Generated playlist for group '%s' with %d channels (out of %d total)", groupFilter, filteredCount, totalCount)
	}
}

// getChannelGroup extracts the group from channel attributes (tvg-group or group-title)
func (sp *proxy.StreamProxy) getChannelGroup(attrs map[string]string) string {
	// Check for tvg-group first
	if group, exists := attrs["tvg-group"]; exists && group != "" {
		return group
	}

	// Fall back to group-title
	if group, exists := attrs["group-title"]; exists && group != "" {
		return group
	}

	return ""
}

func (sp *proxy.StreamProxy) handleStream(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	safeName := vars["channel"]

	// Find original channel name
	channelName := sp.findChannelBySafeName(safeName)

	sp.logger.Printf("Handling stream request for channel: %s (from URL: %s)", channelName, safeName)

	value, exists := sp.channels.Load(channelName)
	if !exists {
		sp.logger.Printf("Channel not found: %s", channelName)
		http.Error(w, "Channel not found", http.StatusNotFound)
		return
	}

	channel := value.(*Channel)
	sp.logger.Printf("Found channel %s with %d streams", channelName, len(channel.Streams))

	if sp.config.EnableRestreaming {
		// Use restreaming
		sp.handleRestreamingClient(w, r, channel)
	} else {
		// Direct proxy mode
		success := sp.tryStreams(channel, 0, w, r)
		if !success {
			sp.logger.Printf("All streams failed for channel: %s", channelName)
			http.Error(w, "All streams failed", http.StatusServiceUnavailable)
		}
	}
}

func (sp *proxy.StreamProxy) handleRestreamingClient(w http.ResponseWriter, r *http.Request, channel *Channel) {
	sp.logger.Printf("Starting restreaming client for channel: %s", channel.Name)

	channel.mu.Lock()
	if channel.restreamer == nil {
		sp.logger.Printf("Creating new restreamer for channel: %s", channel.Name)
		channel.restreamer = NewRestreamer(channel, sp.config.MaxBufferSize, sp.logger, sp.httpClient, sp.rateLimiter, sp.config)
	}
	restreamer := channel.restreamer
	channel.mu.Unlock()

	// Generate client ID
	clientID := fmt.Sprintf("%s-%d", r.RemoteAddr, time.Now().UnixNano())
	sp.logger.Printf("New client connected: %s for channel: %s", clientID, channel.Name)

	// Set headers before checking for flusher
	w.Header().Set("Content-Type", "video/mp2t")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Get flusher - use the underlying ResponseWriter if it's a CustomResponseWriter
	var flusher http.Flusher
	var ok bool

	if crw, isCustom := w.(*CustomResponseWriter); isCustom {
		flusher, ok = crw.ResponseWriter.(http.Flusher)
	} else {
		flusher, ok = w.(http.Flusher)
	}

	if !ok {
		sp.logger.Printf("Streaming not supported for client: %s", clientID)
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	// Write headers
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	restreamer.AddClient(clientID, w, flusher)
	defer restreamer.RemoveClient(clientID)

	sp.logger.Printf("Client %s added to restreamer, waiting for disconnect", clientID)

	// Wait for client disconnect
	<-r.Context().Done()
	sp.logger.Printf("Restreaming client disconnected: %s", clientID)
}
