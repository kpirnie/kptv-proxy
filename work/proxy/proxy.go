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
	"kptv-proxy/work/watcher"
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

// StreamProxy represents the core application responsible for
// managing channels, importing streams, generating playlists,
// handling restream clients, and performing cleanup tasks.
type StreamProxy struct {
	Config                *config.Config                // Application configuration
	Channels              sync.Map                      // Channel map, safe for concurrent access
	Cache                 *cache.Cache                  // Cache for playlists and metadata
	Logger                *log.Logger                   // Application logger
	BufferPool            *buffer.BufferPool            // Pool for TS segment buffers
	HttpClient            *client.HeaderSettingClient   // HTTP client with custom headers
	WorkerPool            *ants.Pool                    // Worker pool for concurrent tasks
	RateLimiter           ratelimit.Limiter             // Rate limiter for outbound requests
	MasterPlaylistHandler *parser.MasterPlaylistHandler // Handler for master playlist parsing
	importStopChan        chan bool                     // Stop signal for importer refresh loop
	WatcherManager        *watcher.WatcherManager       // Manager for stream watchers
}

// New creates and initializes a new StreamProxy instance.
func New(cfg *config.Config, logger *log.Logger, bufferPool *buffer.BufferPool, httpClient *client.HeaderSettingClient, workerPool *ants.Pool, cache *cache.Cache) *StreamProxy {
	return &StreamProxy{
		Config:                cfg,
		Channels:              sync.Map{}, // Start with an empty concurrent map
		Cache:                 cache,
		Logger:                logger,
		BufferPool:            bufferPool,
		HttpClient:            httpClient,
		WorkerPool:            workerPool,
		MasterPlaylistHandler: parser.NewMasterPlaylistHandler(logger, cfg),
		importStopChan:        make(chan bool, 1),
		WatcherManager:        watcher.NewWatcherManager(logger),
	}
}

// ImportStreams fetches streams from all configured sources,
// enforces per-source connection limits, and updates channel state.
func (sp *StreamProxy) ImportStreams() {
	if sp.Config.Debug {
		sp.Logger.Println("Starting stream import...")
	}

	if len(sp.Config.Sources) == 0 {
		sp.Logger.Println("WARNING: No sources configured!")
		return
	}

	var wg sync.WaitGroup
	newChannels := sync.Map{} // Temporary channel map for new imports

	for i := range sp.Config.Sources {
		wg.Add(1)
		source := &sp.Config.Sources[i]

		go func(src *config.SourceConfig) {
			defer wg.Done()

			// Enforce source connection limits
			currentConns := atomic.LoadInt32(&src.ActiveConns)
			if currentConns >= int32(src.MaxConnections) {
				if sp.Config.Debug {
					sp.Logger.Printf("Cannot import from source (connection limit %d/%d): %s",
						currentConns, src.MaxConnections, utils.LogURL(sp.Config, src.URL))
				}
				return
			}

			// Acquire a parsing slot
			newConns := atomic.AddInt32(&src.ActiveConns, 1)
			if sp.Config.Debug {
				sp.Logger.Printf("[IMPORT_CONNECTION] Acquired connection %d/%d for parsing: %s",
					newConns, src.MaxConnections, utils.LogURL(sp.Config, src.URL))
			}
			defer func() {
				// Always release slot after parsing
				remainingConns := atomic.AddInt32(&src.ActiveConns, -1)
				if sp.Config.Debug {
					sp.Logger.Printf("[IMPORT_RELEASE] Released parsing connection, remaining: %d/%d for: %s",
						remainingConns, src.MaxConnections, utils.LogURL(sp.Config, src.URL))
				}
			}()

			// Parse M3U8 playlist for streams
			streams := parser.ParseM3U8(sp.HttpClient, sp.Logger, sp.Config, src)
			if sp.Config.Debug {
				sp.Logger.Printf("Parsed %d streams from source: %s", len(streams), utils.LogURL(sp.Config, src.URL))
			}

			// Aggregate streams by channel name
			for _, stream := range streams {
				channelName := stream.Name
				actual, _ := newChannels.LoadOrStore(channelName, &types.Channel{
					Name:                 channelName,
					Streams:              []*types.Stream{},
					PreferredStreamIndex: 0,
				})
				channel := actual.(*types.Channel)

				// Append stream safely
				channel.Mu.Lock()
				channel.Streams = append(channel.Streams, stream)
				channel.Mu.Unlock()
			}
		}(source)
	}

	// Wait with a 2-minute timeout
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

	// Finalize channels and preserve preferred stream indexes
	count := 0
	newChannels.Range(func(key, value interface{}) bool {
		channelName := key.(string)
		channel := value.(*types.Channel)

		// Sort streams (by config rules such as resolution or priority)
		parser.SortStreams(channel.Streams, sp.Config, channelName)

		// Preserve existing preferred index if channel already exists
		if existing, exists := sp.Channels.Load(channelName); exists {
			existingChannel := existing.(*types.Channel)
			existingPreferred := atomic.LoadInt32(&existingChannel.PreferredStreamIndex)
			atomic.StoreInt32(&channel.PreferredStreamIndex, existingPreferred)
		}

		sp.Channels.Store(key, channel)
		count++
		return true
	})

	// Clear cache after updates if enabled
	if sp.Config.CacheEnabled {
		sp.Cache.ClearIfNeeded()
	}

	if sp.Config.Debug {
		sp.Logger.Printf("Import complete. Found %d channels", count)
	}
}

// GeneratePlaylist writes an M3U playlist to the HTTP response,
// optionally filtering by a channel group.
func (sp *StreamProxy) GeneratePlaylist(w http.ResponseWriter, r *http.Request, groupFilter string) {
	var seenClients sync.Map

	if sp.Config.Debug {
		clientKey := r.RemoteAddr + "-" + r.Header.Get("User-Agent")
		if _, seen := seenClients.LoadOrStore(clientKey, true); !seen {
			sp.Logger.Printf("New playlist client: %s (%s)",
				r.RemoteAddr, r.Header.Get("User-Agent"))
		}
	}

	// Build cache key
	cacheKey := "playlist"
	if groupFilter != "" {
		cacheKey = "playlist_" + strings.ToLower(groupFilter)
	}

	// Serve cached playlist if available
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

	// Iterate over all channels
	sp.Channels.Range(func(key, value interface{}) bool {
		channelName := key.(string)
		channel := value.(*types.Channel)
		totalCount++

		channel.Mu.RLock()
		if len(channel.Streams) > 0 {
			attrs := channel.Streams[0].Attributes

			// Apply group filter if requested
			if groupFilter != "" {
				channelGroup := sp.GetChannelGroup(attrs)
				if !strings.EqualFold(channelGroup, groupFilter) {
					channel.Mu.RUnlock()
					return true
				}
			}

			filteredCount++

			// Write EXTINF metadata
			playlist.WriteString("#EXTINF:-1")
			for key, value := range attrs {
				if key != "tvg-name" && key != "duration" {
					if strings.ContainsAny(value, ",\"") {
						value = fmt.Sprintf("%q", value)
					}
					playlist.WriteString(fmt.Sprintf(" %s=\"%s\"", key, value))
				}
			}

			// Clean channel name and write URL
			cleanName := strings.Trim(channelName, "\"")
			playlist.WriteString(fmt.Sprintf(",%s\n", cleanName))
			safeName := utils.SanitizeChannelName(channelName)
			proxyURL := fmt.Sprintf("%s/stream/%s", sp.Config.BaseURL, safeName)
			playlist.WriteString(proxyURL + "\n")
		}
		channel.Mu.RUnlock()
		return true
	})

	result := playlist.String()

	// Cache generated playlist
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

// GetChannelGroup extracts the channel group name from attributes,
// preferring "tvg-group" over "group-title".
func (sp *StreamProxy) GetChannelGroup(attrs map[string]string) string {
	if group, exists := attrs["tvg-group"]; exists && group != "" {
		return group
	}
	if group, exists := attrs["group-title"]; exists && group != "" {
		return group
	}
	return ""
}

// StartImportRefresh periodically re-imports streams until stopped.
func (sp *StreamProxy) StartImportRefresh() {
	ticker := time.NewTicker(sp.Config.ImportRefreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-sp.importStopChan:
			if sp.Config.Debug {
				sp.Logger.Println("Import refresh stopped")
			}
			return
		case <-ticker.C:
			if sp.Config.Debug {
				sp.Logger.Println("Refreshing imports...")
			}
			sp.ImportStreams()
		}
	}
}

// StopImportRefresh signals the import refresh loop to stop.
func (sp *StreamProxy) StopImportRefresh() {
	if sp.importStopChan != nil {
		select {
		case sp.importStopChan <- true:
		default:
			// Ignore if channel is already signaled
		}
	}
}

// RestreamCleanup runs periodically to cleanup inactive restreamers
// and clients, ensuring buffers and contexts are freed safely.
func (sp *StreamProxy) RestreamCleanup() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now().Unix()

		sp.Channels.Range(func(key, value interface{}) bool {
			channel := value.(*types.Channel)
			channel.Mu.Lock()

			if channel.Restreamer != nil {
				// Handle inactive or cancelled restreamers
				if !channel.Restreamer.Running.Load() {
					lastActivity := channel.Restreamer.LastActivity.Load()
					if now-lastActivity > 30 {
						select {
						case <-channel.Restreamer.Ctx.Done():
							// Force cleanup after cancelled context
							if now-lastActivity > 60 {
								if sp.Config.Debug {
									sp.Logger.Printf("[CLEANUP_FORCE] Channel %s: Force cleaning cancelled context after 60s", channel.Name)
								}
								if channel.Restreamer.Buffer != nil && !channel.Restreamer.Buffer.IsDestroyed() {
									channel.Restreamer.Buffer.Destroy()
								}
								channel.Restreamer.Cancel()
								channel.Restreamer = nil
							}
						default:
							// Normal cleanup for inactive restreamer
							if channel.Restreamer.Buffer != nil && !channel.Restreamer.Buffer.IsDestroyed() {
								if sp.Config.Debug {
									sp.Logger.Printf("[CLEANUP_BUFFER_DESTROY] Channel %s: Safely destroying buffer", channel.Name)
								}
								channel.Restreamer.Buffer.Destroy()
							}
							channel.Restreamer.Cancel()
							channel.Restreamer = nil

							if sp.Config.Debug {
								sp.Logger.Printf("Cleaned up inactive restreamer for channel: %s", channel.Name)
							}
						}
					}

				} else {
					// Active restreamer: prune dead clients
					clientCount := 0
					channel.Restreamer.Clients.Range(func(ckey, cvalue interface{}) bool {
						client := cvalue.(*types.RestreamClient)
						lastSeen := client.LastSeen.Load()

						// Drop inactive clients after 5 minutes
						if now-lastSeen > 300 {
							if sp.Config.Debug {
								sp.Logger.Printf("Removing inactive client: %s (last seen %d seconds ago)", ckey.(string), now-lastSeen)
							}
							channel.Restreamer.Clients.Delete(ckey)
							select {
							case <-client.Done:
							default:
								close(client.Done)
							}
						} else {
							clientCount++
						}
						return true
					})

					// Stop restreamer if no clients remain
					if clientCount == 0 && channel.Restreamer.Running.Load() {
						lastActivity := channel.Restreamer.LastActivity.Load()
						if now-lastActivity > 60 {
							if sp.Config.Debug {
								sp.Logger.Printf("No active clients found, stopping restreamer for: %s", channel.Name)
							}
							channel.Restreamer.Cancel()
							channel.Restreamer.Running.Store(false)

							if channel.Restreamer.Buffer != nil && !channel.Restreamer.Buffer.IsDestroyed() {
								if sp.Config.Debug {
									sp.Logger.Printf("[CLEANUP_NO_CLIENTS] Channel %s: Safely destroying buffer", channel.Name)
								}
								channel.Restreamer.Buffer.Destroy()
							}
						}
					}
				}
			}

			channel.Mu.Unlock()
			return true
		})

		// Periodic GC to reclaim memory
		runtime.GC()
	}
}

// FindChannelBySafeName resolves a channel name from a sanitized
// identifier (used in playlist URLs).
func (sp *StreamProxy) FindChannelBySafeName(safeName string) string {
	// Try exact match after replacing underscores
	simpleName := strings.ReplaceAll(safeName, "_", " ")
	if _, exists := sp.Channels.Load(simpleName); exists {
		return simpleName
	}

	// Otherwise, match against sanitized names
	var foundName string
	sp.Channels.Range(func(key, _ interface{}) bool {
		name := key.(string)
		if utils.SanitizeChannelName(name) == safeName {
			foundName = name
			return false
		}
		return true
	})

	if foundName != "" {
		return foundName
	}
	return safeName
}

// HandleRestreamingClient attaches an HTTP client to a channel’s
// restreamer, ensuring TS segments are streamed until disconnect.
func (sp *StreamProxy) HandleRestreamingClient(w http.ResponseWriter, r *http.Request, channel *types.Channel) {
	if sp.Config.Debug {
		sp.Logger.Printf("[STREAM_REQUEST] Channel %s: URL: %s", channel.Name, channel.Name)
		sp.Logger.Printf("[FOUND] Channel %s: Streams: %d", channel.Name, len(channel.Streams))
	}

	channel.Mu.Lock()
	var restreamer *restream.Restream
	if channel.Restreamer == nil {
		// Create a new restreamer if none exists
		if sp.Config.Debug {
			sp.Logger.Printf("[RESTREAM_NEW] Channel %s: Creating new restreamer", channel.Name)
		}
		restreamer = restream.NewRestreamer(channel, (sp.Config.MaxBufferSize * 1024 * 1024), sp.Logger, sp.HttpClient, sp.Config)
		channel.Restreamer = restreamer.Restreamer
	} else {
		// Wrap existing restreamer
		restreamer = &restream.Restream{Restreamer: channel.Restreamer}
	}
	channel.Mu.Unlock()

	clientID := fmt.Sprintf("%s-%d", r.RemoteAddr, time.Now().UnixNano())

	// Set streaming headers
	w.Header().Set("Content-Type", "video/mp2t")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Accept", "*/*")

	// Ensure client supports flushing
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

	// Send headers immediately
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	if sp.Config.Debug {
		sp.Logger.Printf("[RESTREAM_START] Channel %s: Client: %s", channel.Name, clientID)
	}

	// Add client to active restreamer
	restreamer.AddClient(clientID, w, flusher)

	// Attach watcher if stream is already running
	if restreamer.Restreamer.Running.Load() {
		sp.WatcherManager.StartWatching(channel.Name, restreamer.Restreamer)
	}

	// Cleanup on disconnect
	cleanup := func() {
		restreamer.RemoveClient(clientID)
	}
	defer cleanup()

	// Monitor client context for disconnect
	done := make(chan struct{})
	go func() {
		defer close(done)
		<-r.Context().Done()
	}()

	select {
	case <-done:
		if sp.Config.Debug {
			sp.Logger.Printf("Restreaming client disconnected: %s", clientID)
		}
	case <-time.After(24 * time.Hour):
		if sp.Config.Debug {
			sp.Logger.Printf("Restreaming client timeout: %s", clientID)
		}
	}
}

// ShouldCheckForMasterPlaylist determines if a response body
// likely contains a master M3U8 playlist based on headers.
func (sp *StreamProxy) ShouldCheckForMasterPlaylist(resp *http.Response) bool {
	contentType := resp.Header.Get("Content-Type")
	contentLength := resp.Header.Get("Content-Length")

	if strings.Contains(strings.ToLower(contentType), "mpegurl") ||
		strings.Contains(strings.ToLower(contentType), "m3u8") {
		return true
	}

	if contentLength != "" {
		if length, err := strconv.ParseInt(contentLength, 10, 64); err == nil {
			if length > 0 && length < 100*1024 {
				return true
			}
		}
	}

	return false
}

// GetChannelNameFromStream resolves a display name from a stream,
// preferring the "tvg-name" attribute if present.
func (sp *StreamProxy) GetChannelNameFromStream(stream *types.Stream) string {
	if name, ok := stream.Attributes["tvg-name"]; ok && name != "" {
		return name
	}
	return stream.Name
}
