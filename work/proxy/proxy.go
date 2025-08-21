package proxy

import (
	"fmt"
	"kptv-proxy/work/buffer"
	"kptv-proxy/work/cache"
	"kptv-proxy/work/client"
	"kptv-proxy/work/config"
	"kptv-proxy/work/parser"
	"kptv-proxy/work/restream"
	"kptv-proxy/work/types"
	"kptv-proxy/work/utils"
	"runtime"
	"strconv"

	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/panjf2000/ants/v2"
	"go.uber.org/ratelimit"
)

// StreamProxy is the main application struct
type StreamProxy struct {
	Config                *config.Config
	Channels              sync.Map // Use sync.Map for concurrent access
	Cache                 *cache.Cache
	Logger                *log.Logger
	BufferPool            *buffer.BufferPool
	HttpClient            *client.HeaderSettingClient
	WorkerPool            *ants.Pool
	RateLimiter           ratelimit.Limiter
	MasterPlaylistHandler *parser.MasterPlaylistHandler
}

// ensure we fire up the struct
func New(cfg *config.Config, logger *log.Logger, bufferPool *buffer.BufferPool, httpClient *client.HeaderSettingClient, workerPool *ants.Pool, cache *cache.Cache) *StreamProxy {
	return &StreamProxy{
		Config:                cfg,
		Channels:              sync.Map{}, // Initialize empty sync.Map
		Cache:                 cache,
		Logger:                logger,
		BufferPool:            bufferPool,
		HttpClient:            httpClient,
		WorkerPool:            workerPool,
		MasterPlaylistHandler: parser.NewMasterPlaylistHandler(logger, cfg), // Add this line
	}
}

// import streams from the sources
func (sp *StreamProxy) ImportStreams() {
	if sp.Config.Debug {
		sp.Logger.Println("Starting stream import...")
	}

	if len(sp.Config.Sources) == 0 {
		sp.Logger.Println("WARNING: No sources configured!")
		return
	}

	var wg sync.WaitGroup
	newChannels := sync.Map{}

	for i := range sp.Config.Sources {
		wg.Add(1)
		source := &sp.Config.Sources[i]

		// Use a goroutine but respect the source's connection limit for parsing
		go func(src *config.SourceConfig) {
			defer wg.Done()

			// Check if we can acquire a connection for parsing
			currentConns := atomic.LoadInt32(&src.ActiveConns)
			if currentConns >= int32(src.MaxConnections) {
				if sp.Config.Debug {
					sp.Logger.Printf("Cannot import from source (connection limit %d/%d): %s",
						currentConns, src.MaxConnections, utils.LogURL(sp.Config, src.URL))
				}

				return
			}

			// Acquire connection for parsing
			newConns := atomic.AddInt32(&src.ActiveConns, 1)
			if sp.Config.Debug {
				sp.Logger.Printf("[IMPORT_CONNECTION] Acquired connection %d/%d for parsing: %s",
					newConns, src.MaxConnections, utils.LogURL(sp.Config, src.URL))
			}
			// Always release connection when done parsing
			defer func() {
				remainingConns := atomic.AddInt32(&src.ActiveConns, -1)
				if sp.Config.Debug {
					sp.Logger.Printf("[IMPORT_RELEASE] Released parsing connection, remaining: %d/%d for: %s",
						remainingConns, src.MaxConnections, utils.LogURL(sp.Config, src.URL))
				}
			}()

			streams := parser.ParseM3U8(sp.HttpClient, sp.Logger, sp.Config, src)
			if sp.Config.Debug {
				sp.Logger.Printf("Parsed %d streams from source: %s", len(streams), utils.LogURL(sp.Config, src.URL))
			}

			for _, stream := range streams {
				channelName := stream.Name
				actual, _ := newChannels.LoadOrStore(channelName, &types.Channel{
					Name:    channelName,
					Streams: []*types.Stream{},
				})
				channel := actual.(*types.Channel)
				channel.Mu.Lock()
				channel.Streams = append(channel.Streams, stream)
				channel.Mu.Unlock()
			}
		}(source)
	}

	// Wait for all imports to complete with timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		if sp.Config.Debug {
			sp.Logger.Println("All imports completed successfully")
		}
	case <-time.After(2 * time.Minute):
		if sp.Config.Debug {
			sp.Logger.Println("WARNING: Import timeout reached, some sources may not have completed")
		}
	}

	// Sort streams in each channel and migrate to main map
	count := 0
	newChannels.Range(func(key, value interface{}) bool {
		channel := value.(*types.Channel)
		parser.SortStreams(channel.Streams, sp.Config)
		sp.Channels.Store(key, channel)
		count++
		return true
	})

	// Clear cache if needed
	if sp.Config.CacheEnabled {
		sp.Cache.ClearIfNeeded()
	}

	if sp.Config.Debug {
		sp.Logger.Printf("Import complete. Found %d channels", count)
	}
}

// generate our playlist
func (sp *StreamProxy) GeneratePlaylist(w http.ResponseWriter, r *http.Request, groupFilter string) {
	if sp.Config.Debug {
		if groupFilter == "" {
			sp.Logger.Println("Handling playlist request (all groups)")
		} else {
			sp.Logger.Printf("Handling playlist request for group: %s", groupFilter)
		}
	}

	// Create cache key
	cacheKey := "playlist"
	if groupFilter != "" {
		cacheKey = "playlist_" + strings.ToLower(groupFilter)
	}

	// Check cache
	if sp.Config.CacheEnabled {
		if cached, ok := sp.Cache.GetM3U8(cacheKey); ok {
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
	sp.Channels.Range(func(key, value interface{}) bool {
		channelName := key.(string)
		channel := value.(*types.Channel)
		totalCount++

		channel.Mu.RLock()
		if len(channel.Streams) > 0 {
			// Use attributes from first stream
			attrs := channel.Streams[0].Attributes

			// Apply group filter if specified
			if groupFilter != "" {
				channelGroup := sp.GetChannelGroup(attrs)
				if !strings.EqualFold(channelGroup, groupFilter) {
					channel.Mu.RUnlock()
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
			safeName := utils.SanitizeChannelName(channelName)
			proxyURL := fmt.Sprintf("%s/stream/%s", sp.Config.BaseURL, safeName)
			playlist.WriteString(proxyURL + "\n")
		}
		channel.Mu.RUnlock()
		return true
	})

	result := playlist.String()

	// Cache result
	if sp.Config.CacheEnabled {
		sp.Cache.SetM3U8(cacheKey, result)
	}

	w.Header().Set("Content-Type", "application/x-mpegURL")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write([]byte(result))

	if sp.Config.Debug {
		if groupFilter == "" {
			sp.Logger.Printf("Generated playlist with %d channels", totalCount)
		} else {
			sp.Logger.Printf("Generated playlist for group '%s' with %d channels (out of %d total)", groupFilter, filteredCount, totalCount)
		}
	}

}

// setup the channel groups
func (sp *StreamProxy) GetChannelGroup(attrs map[string]string) string {
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

// refresh the importer
func (sp *StreamProxy) StartImportRefresh() {
	ticker := time.NewTicker(sp.Config.ImportRefreshInterval)
	defer ticker.Stop()

	for range ticker.C {
		if sp.Config.Debug {
			sp.Logger.Println("Refreshing imports...")
		}

		sp.ImportStreams()
	}

}

// clean up the streamer
func (sp *StreamProxy) RestreamCleanup() {
	ticker := time.NewTicker(10 * time.Second) // More frequent cleanup
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now().Unix()

		sp.Channels.Range(func(key, value interface{}) bool {
			channel := value.(*types.Channel)
			channel.Mu.Lock()

			if channel.Restreamer != nil {
				// Check if restreamer is inactive
				if !channel.Restreamer.Running.Load() {
					lastActivity := channel.Restreamer.LastActivity.Load()
					if now-lastActivity > 30 { // 30 second grace period
						// CRITICAL: Properly destroy the restreamer and free memory
						if channel.Restreamer.Buffer != nil {
							channel.Restreamer.Buffer.Destroy()
						}
						channel.Restreamer.Cancel()
						channel.Restreamer = nil

						if sp.Config.Debug {
							sp.Logger.Printf("Cleaned up inactive restreamer for channel: %s", channel.Name)
						}
					}
				} else {
					// Check for dead clients
					clientCount := 0
					channel.Restreamer.Clients.Range(func(ckey, cvalue interface{}) bool {
						client := cvalue.(*types.RestreamClient)
						lastSeen := client.LastSeen.Load()
						if now-lastSeen > 60 { // 60 seconds without activity
							if sp.Config.Debug {
								sp.Logger.Printf("Removing inactive client: %s", ckey.(string))
							}
							channel.Restreamer.Clients.Delete(ckey)
							select {
							case <-client.Done:
								// Already closed
							default:
								close(client.Done)
							}
						} else {
							clientCount++
						}
						return true
					})

					// If no active clients, stop the restreamer
					if clientCount == 0 && channel.Restreamer.Running.Load() {
						if sp.Config.Debug {
							sp.Logger.Printf("No active clients found, stopping restreamer for: %s", channel.Name)
						}
						channel.Restreamer.Cancel()
						channel.Restreamer.Running.Store(false)

						// CRITICAL: Destroy buffer when no clients
						if channel.Restreamer.Buffer != nil {
							channel.Restreamer.Buffer.Destroy()
						}
					}
				}
			}

			channel.Mu.Unlock()
			return true
		})

		// CRITICAL: Force garbage collection during cleanup
		runtime.GC()
	}
}

// find channels by name
func (sp *StreamProxy) FindChannelBySafeName(safeName string) string {
	// First try exact match after simple space replacement
	simpleName := strings.ReplaceAll(safeName, "_", " ")
	if _, exists := sp.Channels.Load(simpleName); exists {
		return simpleName
	}

	// Then try to find by matching sanitized names
	var foundName string
	sp.Channels.Range(func(key, _ interface{}) bool {
		name := key.(string)
		if utils.SanitizeChannelName(name) == safeName {
			foundName = name
			return false // Stop iteration
		}
		return true
	})

	if foundName != "" {
		return foundName
	}
	return safeName

}

// handle the clients
func (sp *StreamProxy) HandleRestreamingClient(w http.ResponseWriter, r *http.Request, channel *types.Channel) {
	if sp.Config.Debug {
		sp.Logger.Printf("[STREAM_REQUEST] Channel %s: URL: %s", channel.Name, channel.Name)
		sp.Logger.Printf("[FOUND] Channel %s: Streams: %d", channel.Name, len(channel.Streams))
	}

	channel.Mu.Lock()
	var restreamer *restream.Restream
	if channel.Restreamer == nil {
		if sp.Config.Debug {
			sp.Logger.Printf("[RESTREAM_NEW] Channel %s: Creating new restreamer", channel.Name)
		}

		restreamer = restream.NewRestreamer(channel, (sp.Config.MaxBufferSize * 1024 * 1024), sp.Logger, sp.HttpClient, sp.Config)
		channel.Restreamer = restreamer.Restreamer
	} else {
		restreamer = &restream.Restream{Restreamer: channel.Restreamer}
	}
	channel.Mu.Unlock()

	// Generate client ID
	clientID := fmt.Sprintf("%s-%d", r.RemoteAddr, time.Now().UnixNano())

	// Set headers immediately
	w.Header().Set("Content-Type", "video/mp2t")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Accept", "*/*")

	// Get flusher
	var flusher http.Flusher
	var ok bool

	if crw, isCustom := w.(*client.CustomResponseWriter); isCustom {
		flusher, ok = crw.ResponseWriter.(http.Flusher)
	} else {
		flusher, ok = w.(http.Flusher)
	}

	if !ok {
		if sp.Config.Debug {
			sp.Logger.Printf("Streaming not supported for client: %s", clientID)
		}

		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	// Write headers
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	if sp.Config.Debug {
		sp.Logger.Printf("[RESTREAM_START] Channel %s: Client: %s", channel.Name, clientID)
	}

	// Add client to restreamer
	restreamer.AddClient(clientID, w, flusher)

	// Create a cleanup function
	cleanup := func() {
		restreamer.RemoveClient(clientID)
	}
	defer cleanup()

	// Create a channel to detect client disconnection
	done := make(chan struct{})

	// Start a goroutine to monitor the connection
	go func() {
		defer close(done)
		<-r.Context().Done()
	}()

	// Wait for client disconnect with timeout
	select {
	case <-done:
		if sp.Config.Debug {
			sp.Logger.Printf("Restreaming client disconnected: %s", clientID)
		}

	case <-time.After(24 * time.Hour): // Max connection time
		if sp.Config.Debug {
			sp.Logger.Printf("Restreaming client timeout: %s", clientID)
		}

	}
}

// Helper method to determine if we should check for master playlist
func (sp *StreamProxy) ShouldCheckForMasterPlaylist(resp *http.Response) bool {
	contentType := resp.Header.Get("Content-Type")
	contentLength := resp.Header.Get("Content-Length")

	// Check for M3U8 content type
	if strings.Contains(strings.ToLower(contentType), "mpegurl") ||
		strings.Contains(strings.ToLower(contentType), "m3u8") {
		return true
	}

	// Check for small content length (master playlists are typically small)
	if contentLength != "" {
		if length, err := strconv.ParseInt(contentLength, 10, 64); err == nil {
			// Master playlists are typically under 100KB
			if length > 0 && length < 100*1024 {
				return true
			}
		}
	}

	return false
}

// Helper method to get channel name from stream
func (sp *StreamProxy) GetChannelNameFromStream(stream *types.Stream) string {
	if name, ok := stream.Attributes["tvg-name"]; ok && name != "" {
		return name
	}
	return stream.Name
}
