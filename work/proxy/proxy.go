package proxy

import (
	"fmt"
	"kptv-proxy/work/buffer"
	"kptv-proxy/work/cache"
	"kptv-proxy/work/client"
	"kptv-proxy/work/config"
	"kptv-proxy/work/filter"
	"kptv-proxy/work/parser"
	"kptv-proxy/work/restream"
	"kptv-proxy/work/types"
	"kptv-proxy/work/utils"
	"kptv-proxy/work/watcher"
	"runtime"
	"strconv"

	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/panjf2000/ants/v2"
	"github.com/puzpuzpuz/xsync/v3"
	"go.uber.org/ratelimit"
)

// StreamProxy represents the core application orchestrator responsible for managing
// the complete IPTV proxy lifecycle including channel discovery, stream aggregation,
// playlist generation, client connection handling, and resource cleanup. It serves
// as the central coordination point between all subsystems and maintains the global
// application state through thread-safe concurrent data structures.
//
// The proxy operates in a hub-and-spoke model where it aggregates streams from multiple
// sources, normalizes channel metadata, handles client requests for playlists and streams,
// and manages the restreaming infrastructure that enables efficient one-to-many distribution
// of video content while minimizing upstream bandwidth usage and source server load.
type StreamProxy struct {
	Config                *config.Config                		// Application configuration with source URLs and operational parameters
	Channels              *xsync.MapOf[string, *types.Channel]	// Thread-safe map of channel name -> *types.Channel for concurrent access
	Cache                 *cache.Cache                  		// Playlist and metadata caching system for performance optimization
	Logger                *log.Logger                   		// Centralized logging system for debugging and operational monitoring
	BufferPool            *buffer.BufferPool            		// Reusable buffer pool for TS segment processing and memory management
	HttpClient            *client.HeaderSettingClient   		// HTTP client with custom header support for source authentication
	WorkerPool            *ants.Pool                    		// Goroutine pool for concurrent task execution and resource control
	MasterPlaylistHandler *parser.MasterPlaylistHandler 		// Specialized handler for HLS master playlist parsing and variant selection
	importStopChan        chan bool                     		// Signal channel for gracefully stopping the periodic import refresh loop
	WatcherManager        *watcher.WatcherManager       		// Stream health monitoring and automatic failover management system
	SourceRateLimiters    map[string]ratelimit.Limiter
	rateLimiterMutex      sync.RWMutex
	FilterManager         *filter.FilterManager
}

// New creates and initializes a new StreamProxy instance with all required dependencies
// and subsystems properly configured. The constructor establishes the foundational
// infrastructure for IPTV proxy operations including concurrent data structures,
// worker pools, and specialized handlers for different content types.
//
// All provided dependencies must be pre-configured and ready for use as the StreamProxy
// does not perform additional initialization on injected components. The resulting
// instance is immediately ready for stream import and client serving operations.
//
// Parameters:
//   - cfg: complete application configuration with source definitions and operational settings
//   - logger: configured logger for debugging and operational event recording
//   - bufferPool: initialized buffer pool for efficient memory management during streaming
//   - httpClient: HTTP client with custom header and authentication capabilities
//   - workerPool: goroutine pool for managing concurrent operations and resource limits
//   - cache: caching system for playlist and metadata performance optimization
//
// Returns:
//   - *StreamProxy: fully initialized proxy instance ready for operation
func New(cfg *config.Config, logger *log.Logger, bufferPool *buffer.BufferPool, httpClient *client.HeaderSettingClient, workerPool *ants.Pool, cache *cache.Cache) *StreamProxy {
	return &StreamProxy{
		Config:                cfg,
		Channels:              xsync.NewMapOf[string, *types.Channel](), // Initialize empty thread-safe channel store
		Cache:                 cache,
		Logger:                logger,
		BufferPool:            bufferPool,
		HttpClient:            httpClient,
		WorkerPool:            workerPool,
		MasterPlaylistHandler: parser.NewMasterPlaylistHandler(logger, cfg),
		importStopChan:        make(chan bool, 1), // Buffered channel for non-blocking stop signals
		WatcherManager:        watcher.NewWatcherManager(logger),
		SourceRateLimiters:    make(map[string]ratelimit.Limiter),
		rateLimiterMutex:      sync.RWMutex{},
		FilterManager:         filter.NewFilterManager(),
	}
}

// ImportStreams performs comprehensive stream discovery and aggregation from all configured
// sources, implementing concurrent fetching with per-source connection limits and graceful
// error handling. The process fetches M3U8 playlists from each source, parses channel
// definitions, and consolidates streams by channel name while preserving individual
// source characteristics and failover ordering.
//
// The import process operates with strict connection management to prevent overwhelming
// source servers, timeout handling to avoid hanging on unresponsive sources, and
// atomic updates to ensure consistent channel state during refresh operations.
// Existing preferred stream indexes are preserved across imports to maintain user
// preferences and manual stream selections.
//
// Concurrent Safety: This method is thread-safe and can be called during active streaming
// operations without disrupting connected clients or corrupting channel data.
func (sp *StreamProxy) ImportStreams() {
	if sp.Config.Debug {
		sp.Logger.Println("Starting stream import...")
	}

	// Validate that sources are configured before attempting import
	if len(sp.Config.Sources) == 0 {
		sp.Logger.Println("WARNING: No sources configured!")
		return
	}

	var wg sync.WaitGroup
	newChannels := xsync.NewMapOf[string, *types.Channel]() // Temporary staging area for imported channels

	// Launch concurrent import goroutine for each configured source
	for i := range sp.Config.Sources {
		wg.Add(1)
		source := &sp.Config.Sources[i] // Capture source pointer for goroutine safety

		go func(src *config.SourceConfig) {
			defer wg.Done()

			// Enforce per-source connection limits to prevent resource exhaustion
			currentConns := atomic.LoadInt32(&src.ActiveConns)
			if currentConns >= int32(src.MaxConnections) {
				if sp.Config.Debug {
					sp.Logger.Printf("Cannot import from source (connection limit %d/%d): %s",
						currentConns, src.MaxConnections, utils.LogURL(sp.Config, src.URL))
				}
				return
			}

			// Acquire connection slot with atomic increment for thread safety
			newConns := atomic.AddInt32(&src.ActiveConns, 1)
			if sp.Config.Debug {
				sp.Logger.Printf("[IMPORT_CONNECTION] Acquired connection %d/%d for parsing: %s",
					newConns, src.MaxConnections, utils.LogURL(sp.Config, src.URL))
			}
			defer func() {

				// Always release connection slot when parsing completes
				remainingConns := atomic.AddInt32(&src.ActiveConns, -1)
				if sp.Config.Debug {
					sp.Logger.Printf("[IMPORT_RELEASE] Released parsing connection, remaining: %d/%d for: %s",
						remainingConns, src.MaxConnections, utils.LogURL(sp.Config, src.URL))
				}
			}()

			// Get rate limiter for this source
			rateLimiter := sp.getRateLimiterForSource(src)

			// pull and parse the streams based on the username/password passed
			var streams []*types.Stream
			if src.Username != "" && src.Password != "" {

				// Use Xtreme Codes API parser when credentials are available
				streams = parser.ParseXtremeCodesAPI(sp.HttpClient, sp.Logger, sp.Config, src, rateLimiter)
			} else {

				// Use standard M3U8 parser
				streams = parser.ParseM3U8(sp.HttpClient, sp.Logger, sp.Config, src, rateLimiter)
			}

			// Apply content filtering
			if sp != nil && sp.FilterManager != nil {
				streams = filter.FilterStreams(streams, src, sp.FilterManager, sp.Config.Debug)
			}

			// debug logging
			if sp.Config.Debug {
				if src.Username != "" && src.Password != "" {
					sp.Logger.Printf("Parsed %d streams from Xtreme Codes API: %s", len(streams), utils.LogURL(sp.Config, src.URL))
				} else {
					sp.Logger.Printf("Parsed %d streams from M3U8 source: %s", len(streams), utils.LogURL(sp.Config, src.URL))
				}
			}

			// Aggregate streams by channel name, supporting multiple sources per channel
			for _, stream := range streams {
				channelName := stream.Name

				// Create or retrieve channel object for this channel name
				actual, _ := newChannels.LoadOrStore(channelName, &types.Channel{
					Name:                 channelName,
					Streams:              []*types.Stream{},
					PreferredStreamIndex: 0,
				})
				channel := actual.(*types.Channel)

				// Thread-safe append of stream to channel's stream list
				channel.Mu.Lock()
				channel.Streams = append(channel.Streams, stream)
				channel.Mu.Unlock()
			}
		}(source)
	}

	// Wait for all import operations to complete with timeout protection
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

	// Finalize imported channels with sorting and preference preservation
	count := 0
	newChannels.Range(func(key, value interface{}) bool {
		channelName := key.(string)
		channel := value.(*types.Channel)

		// Apply configured sorting rules for predictable stream ordering
		parser.SortStreams(channel.Streams, sp.Config, channelName)

		// Preserve existing preferred stream index from previous imports
		if existing, exists := sp.Channels.Load(channelName); exists {
			existingChannel := existing.(*types.Channel)
			existingPreferred := atomic.LoadInt32(&existingChannel.PreferredStreamIndex)
			atomic.StoreInt32(&channel.PreferredStreamIndex, existingPreferred)
		}

		// Atomically update channel store with new channel data
		sp.Channels.Store(key, channel)
		count++
		return true
	})

	// Trigger cache cleanup if caching is enabled
	if sp.Config.CacheEnabled {
		sp.Cache.ClearIfNeeded()
	}

	if sp.Config.Debug {
		sp.Logger.Printf("Import complete. Found %d channels", count)
	}
}

// GeneratePlaylist creates and serves a complete M3U8 playlist containing channel entries
// from the proxy's channel store, with optional filtering by channel group classification.
// The generator implements intelligent caching, client tracking, and comprehensive metadata
// preservation while ensuring compatibility with standard IPTV client applications.
//
// The playlist generation process handles channel attribute normalization, URL construction
// for proxy streaming endpoints, group-based filtering, and proper M3U8 formatting with
// EXTINF headers containing EPG metadata, logos, and categorization information.
// Generated playlists are automatically cached when caching is enabled to improve
// response times for subsequent requests.
//
// Parameters:
//   - w: HTTP response writer for sending the generated playlist
//   - r: HTTP request containing client information and parameters
//   - groupFilter: optional group name for filtering channels (empty string = no filter)
func (sp *StreamProxy) GeneratePlaylist(w http.ResponseWriter, r *http.Request, groupFilter string) {
	var seenClients sync.Map

	// Track unique clients for debugging and monitoring purposes
	if sp.Config.Debug {
		clientKey := r.RemoteAddr + "-" + r.Header.Get("User-Agent")
		if _, seen := seenClients.LoadOrStore(clientKey, true); !seen {
			sp.Logger.Printf("New playlist client: %s (%s)",
				r.RemoteAddr, r.Header.Get("User-Agent"))
		}
	}

	// Construct cache key incorporating group filter for separate caching
	cacheKey := "playlist"
	if groupFilter != "" {
		cacheKey = "playlist_" + strings.ToLower(groupFilter)
	}

	// Attempt to serve cached playlist if available and caching is enabled
	if sp.Config.CacheEnabled {
		if cached, ok := sp.Cache.GetM3U8(cacheKey); ok {
			w.Header().Set("Content-Type", "application/x-mpegURL")
			w.Header().Set("Cache-Control", "no-cache")
			w.Write([]byte(cached))
			return
		}
	}

	// Build M3U8 playlist content using efficient string builder
	var playlist strings.Builder
	playlist.WriteString("#EXTM3U\n") // Required M3U8 header

	filteredCount := 0
	totalCount := 0

	// Iterate through all channels in the concurrent channel store
	sp.Channels.Range(func(channelName string, channel *types.Channel) bool {
		totalCount++

		channel.Mu.RLock()
		if len(channel.Streams) > 0 {
			attrs := channel.Streams[0].Attributes

			if groupFilter != "" {
				channelGroup := sp.GetChannelGroup(attrs)
				if !strings.EqualFold(channelGroup, groupFilter) {
					channel.Mu.RUnlock()
					return true
				}
			}

			filteredCount++

			playlist.WriteString("#EXTINF:-1")
			for key, value := range attrs {
				if key != "tvg-name" && key != "duration" {
					if strings.ContainsAny(value, ",\"") {
						value = fmt.Sprintf("%q", value)
					}
					playlist.WriteString(fmt.Sprintf(" %s=\"%s\"", key, value))
				}
			}

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

	// Cache the generated playlist for future requests
	if sp.Config.CacheEnabled {
		sp.Cache.SetM3U8(cacheKey, result)
	}

	// Set appropriate HTTP headers for M3U8 content
	w.Header().Set("Content-Type", "application/x-mpegURL")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write([]byte(result))

	// Log playlist generation statistics for debugging
	if sp.Config.Debug {
		if groupFilter == "" {
			sp.Logger.Printf("Generated playlist with %d channels", totalCount)
		} else {
			sp.Logger.Printf("Generated playlist for group '%s' with %d channels (out of %d total)", groupFilter, filteredCount, totalCount)
		}
	}
}

// GetChannelGroup extracts the group classification from channel attributes using
// a priority-based attribute lookup. The function checks multiple attribute names
// commonly used for channel categorization in IPTV sources, implementing a fallback
// hierarchy to maximize compatibility with diverse playlist formats.
//
// The group extraction follows this priority order:
//  1. "tvg-group" - standard TVG format group specification
//  2. "group-title" - alternative group title format
//  3. Empty string if no group information found
//
// Parameters:
//   - attrs: channel attributes map from the stream's EXTINF metadata
//
// Returns:
//   - string: extracted group name or empty string if no group classification found
func (sp *StreamProxy) GetChannelGroup(attrs map[string]string) string {

	// Check primary group attribute (tvg-group)
	if group, exists := attrs["tvg-group"]; exists && group != "" {
		return group
	}

	// Check alternative group attribute (group-title)
	if group, exists := attrs["group-title"]; exists && group != "" {
		return group
	}

	// Return empty string if no group information available
	return ""
}

// StartImportRefresh initiates a periodic background process that automatically refreshes
// stream imports at configured intervals, ensuring channel availability and stream URLs
// remain current without manual intervention. The refresh loop operates independently
// of client connections and can be stopped gracefully through the StopImportRefresh method.
//
// The refresh process maintains service continuity by updating channel definitions while
// preserving active streaming sessions and user preferences. Import failures are handled
// gracefully without disrupting existing operations, and the refresh interval is
// configurable through application settings.
//
// This method should be called once during application startup and will run until
// explicitly stopped or the application terminates.
func (sp *StreamProxy) StartImportRefresh() {

	// Create ticker for periodic refresh at configured interval
	ticker := time.NewTicker(sp.Config.ImportRefreshInterval)
	defer ticker.Stop()

	// Run refresh loop until stop signal received
	for {
		select {
		case <-sp.importStopChan:

			// Graceful shutdown requested
			if sp.Config.Debug {
				sp.Logger.Println("Import refresh stopped")
			}
			return
		case <-ticker.C:

			// Periodic refresh triggered
			if sp.Config.Debug {
				sp.Logger.Println("Refreshing imports...")
			}
			sp.ImportStreams()
		}
	}
}

// StopImportRefresh signals the periodic import refresh loop to terminate gracefully.
// This method provides a clean shutdown mechanism for the background refresh process
// without forcing immediate termination, allowing any in-progress import operations
// to complete before stopping.
//
// The method uses a non-blocking channel send to avoid deadlocks if the refresh
// loop has already terminated or if multiple stop signals are sent concurrently.
func (sp *StreamProxy) StopImportRefresh() {
	if sp.importStopChan != nil {
		select {
		case sp.importStopChan <- true:
			// Stop signal sent successfully
		default:
			// Channel already has pending signal or is closed - ignore
		}
	}
}

// RestreamCleanup implements a comprehensive background maintenance system that periodically
// identifies and cleans up inactive restreaming connections, expired client sessions,
// and abandoned resources. The cleanup process ensures optimal memory usage and prevents
// resource leaks while maintaining active streaming sessions for connected clients.
//
// The cleanup algorithm handles multiple scenarios:
//   - Inactive restreamers after client disconnection
//   - Cancelled contexts with extended cleanup grace periods
//   - Dead clients that have exceeded inactivity thresholds
//   - Buffer destruction for unused streams
//   - Periodic garbage collection for memory optimization
//
// This critical maintenance process runs continuously and should be started once during
// application initialization to ensure long-term system stability.
func (sp *StreamProxy) RestreamCleanup() {

	// Create ticker for periodic cleanup at 10-second intervals
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	// Run cleanup loop continuously
	for range ticker.C {
		now := time.Now().Unix()

		// Scan all channels for cleanup opportunities
		sp.Channels.Range(func(key string, channel *types.Channel) bool {
			channel.Mu.Lock()

			if channel.Restreamer != nil {
				if !channel.Restreamer.Running.Load() {
					lastActivity := channel.Restreamer.LastActivity.Load()

					if now-lastActivity > 30 {
						select {
						case <-channel.Restreamer.Ctx.Done():
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
					clientCount := 0
					channel.Restreamer.Clients.Range(func(ckey, cvalue interface{}) bool {
						client := cvalue.(*types.RestreamClient)
						lastSeen := client.LastSeen.Load()

						if now-lastSeen > 600 {
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

					if clientCount == 0 && channel.Restreamer.Running.Load() {
						lastActivity := channel.Restreamer.LastActivity.Load()
						if now-lastActivity > 120 {
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

		// Trigger garbage collection to reclaim cleaned up memory
		runtime.GC()

		// clean up the buffer pool
		if sp.BufferPool != nil {
			sp.BufferPool.Cleanup()
		}
	}
}

// FindChannelBySafeName resolves original channel names from URL-safe identifiers used
// in streaming endpoints. The function implements a two-stage resolution process:
// first attempting exact matching with underscore-to-space conversion, then performing
// a comprehensive scan for channels whose sanitized names match the provided safe name.
//
// This capability is essential for the streaming endpoint which receives sanitized
// channel names in URLs and must locate the corresponding channel in the internal
// channel store where original names (potentially containing special characters) are used.
//
// Parameters:
//   - safeName: URL-safe channel identifier from streaming endpoint path
//
// Returns:
//   - string: original channel name for channel store lookup, or safeName if not found
func (sp *StreamProxy) FindChannelBySafeName(safeName string) string {
	
	// Decode URL encoding first
	if decoded, err := url.QueryUnescape(safeName); err == nil {
		safeName = decoded
	}

	// Attempt direct resolution with simple underscore-to-space conversion
	simpleName := strings.ReplaceAll(safeName, "_", " ")
	if _, exists := sp.Channels.Load(simpleName); exists {
		return simpleName
	}

	// Perform comprehensive scan for matching sanitized names
	var foundName string
		sp.Channels.Range(func(name string, _ *types.Channel) bool {
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

// HandleRestreamingClient manages the complete lifecycle of a streaming client connection,
// from initial attachment through data distribution to cleanup upon disconnection.
// The handler implements the restreaming architecture where a single upstream connection
// serves multiple clients efficiently through shared buffering and concurrent distribution.
//
// The process involves several key phases:
//  1. Channel validation and restreamer initialization
//  2. Client registration and streaming header configuration
//  3. HTTP flushing capability verification for real-time streaming
//  4. Client monitoring and automatic cleanup on disconnection
//  5. Stream watcher integration for quality monitoring
//
// The handler supports HTTP keep-alive connections and implements proper cleanup
// to prevent resource leaks when clients disconnect unexpectedly.
//
// Parameters:
//   - w: HTTP response writer for streaming TS data to the client
//   - r: HTTP request containing client connection and context information
//   - channel: validated channel object containing stream definitions and metadata
func (sp *StreamProxy) HandleRestreamingClient(w http.ResponseWriter, r *http.Request, channel *types.Channel) {

	// Log connection request details for debugging
	if sp.Config.Debug {
		sp.Logger.Printf("[STREAM_REQUEST] Channel %s: URL: %s", channel.Name, channel.Name)
		sp.Logger.Printf("[FOUND] Channel %s: Streams: %d", channel.Name, len(channel.Streams))
		if sp.Config.FFmpegMode {
			sp.Logger.Printf("Using FFMPEG mode for channel: %s", channel.Name)
		} else {
			sp.Logger.Printf("Using RESTREAMING mode for channel: %s", channel.Name)
		}
	}

	// Initialize or reuse existing restreamer with thread-safe access
	channel.Mu.Lock()
	var restreamer *restream.Restream
	if channel.Restreamer == nil {

		// Get rate limiter for the first stream's source
		var rateLimiter ratelimit.Limiter
		if len(channel.Streams) > 0 {
			rateLimiter = sp.getRateLimiterForSource(channel.Streams[0].Source)
		}

		if sp.Config.Debug {
			sp.Logger.Printf("[RESTREAM_NEW] Channel %s: Creating new restreamer with rate limiting", channel.Name)
		}
		restreamer = restream.NewRestreamer(channel, (sp.Config.BufferSizePerStream  * 1024 * 1024), sp.Logger, sp.HttpClient, sp.Config, rateLimiter)
		channel.Restreamer = restreamer.Restreamer
	} else {
		restreamer = &restream.Restream{Restreamer: channel.Restreamer}
	}
	channel.Mu.Unlock()

	// Generate unique client identifier for tracking and cleanup
	clientID := fmt.Sprintf("%s-%d", r.RemoteAddr, time.Now().UnixNano())

	// Configure HTTP headers for MPEG-TS streaming
	w.Header().Set("Content-Type", "video/mp2t")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Accept", "*/*")

	// Verify client supports HTTP flushing for real-time streaming
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

	// Send initial HTTP response headers immediately
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	if sp.Config.Debug {
		sp.Logger.Printf("[RESTREAM_START] Channel %s: Client: %s", channel.Name, clientID)
	}

	// Register client with the restreamer for data distribution
	restreamer.AddClient(clientID, w, flusher)

	// Integrate with stream watcher if enabled and stream is active
	if sp.Config.WatcherEnabled && restreamer.Restreamer.Running.Load() {

		// Get the correct stream index that will actually be used
		preferredIndex := int(atomic.LoadInt32(&channel.PreferredStreamIndex))
		currentIndex := int(atomic.LoadInt32(&restreamer.Restreamer.CurrentIndex))

		// Use preferred index if it's valid, otherwise use current
		actualIndex := preferredIndex
		if preferredIndex < 0 || preferredIndex >= len(channel.Streams) {
			actualIndex = currentIndex
		}

		if sp.Config.Debug {
			sp.Logger.Printf("[WATCHER_DEBUG] Channel %s: Preferred=%d, Current=%d, Using=%d",
				channel.Name, preferredIndex, currentIndex, actualIndex)
		}

		sp.WatcherManager.StartWatching(channel.Name, restreamer.Restreamer)
	}

	// Setup cleanup function for client disconnection
	cleanup := func() {
		restreamer.RemoveClient(clientID)
	}
	defer cleanup()

	// Monitor for client disconnection using request context
	done := make(chan struct{})
	go func() {
		defer close(done)
		<-r.Context().Done()
	}()

	// Wait for disconnection or timeout
	select {
	case <-done:
		if sp.Config.Debug {
			sp.Logger.Printf("Restreaming client disconnected: %s", clientID)
		}
	case <-time.After(24 * time.Hour):

		// Maximum connection time limit for resource management
		if sp.Config.Debug {
			sp.Logger.Printf("Restreaming client timeout: %s", clientID)
		}
	}
}

// ShouldCheckForMasterPlaylist analyzes HTTP response headers to determine whether
// the response body likely contains an HLS master playlist that should be parsed
// for variant extraction. The function uses content-type hints and content-length
// heuristics to make intelligent decisions about playlist processing.
//
// The detection logic considers:
//   - Content-Type headers indicating M3U8/MPEGURL content
//   - Content-Length suggesting small playlist files rather than media streams
//   - Size thresholds to distinguish playlists from actual video content
//
// This optimization prevents unnecessary processing of large media files while
// ensuring proper handling of playlist content that requires variant selection.
//
// Parameters:
//   - resp: HTTP response from source server containing headers and metadata
//
// Returns:
//   - bool: true if response should be processed as potential master playlist
func (sp *StreamProxy) ShouldCheckForMasterPlaylist(resp *http.Response) bool {

	// get the content type and length
	contentType := resp.Header.Get("Content-Type")
	contentLength := resp.Header.Get("Content-Length")

	// Check for explicit M3U8/MPEGURL content type
	if strings.Contains(strings.ToLower(contentType), "mpegurl") ||
		strings.Contains(strings.ToLower(contentType), "m3u8") {
		return true
	}

	// Use content length heuristic for playlist detection
	if contentLength != "" {
		if length, err := strconv.ParseInt(contentLength, 10, 64); err == nil {

			// Small files (under 100KB) are likely playlists rather than media
			if length > 0 && length < 100*1024 {
				return true
			}
		}
	}

	return false
}

// GetChannelNameFromStream extracts the most appropriate display name for a channel
// from stream metadata, implementing a priority-based attribute lookup to ensure
// consistent and meaningful channel identification across diverse source formats.
//
// The name resolution follows this priority order:
//  1. "tvg-name" attribute - standard TVG format channel name
//  2. Stream.Name field - fallback to stream's internal name
//
// This function ensures that channels are displayed with their intended names
// in playlists and user interfaces, regardless of variations in source playlist
// formatting and attribute naming conventions.
//
// Parameters:
//   - stream: stream object containing metadata and attributes
//
// Returns:
//   - string: most appropriate display name for the channel
func (sp *StreamProxy) GetChannelNameFromStream(stream *types.Stream) string {

	// Prefer tvg-name attribute if available and non-empty
	if name, ok := stream.Attributes["tvg-name"]; ok && name != "" {
		return name
	}

	// Fallback to stream's internal name
	return stream.Name
}

func (sp *StreamProxy) getRateLimiterForSource(source *config.SourceConfig) ratelimit.Limiter {
	sp.rateLimiterMutex.RLock()
	if limiter, exists := sp.SourceRateLimiters[source.URL]; exists {
		sp.rateLimiterMutex.RUnlock()
		return limiter
	}
	sp.rateLimiterMutex.RUnlock()

	sp.rateLimiterMutex.Lock()
	defer sp.rateLimiterMutex.Unlock()

	// Double-check after acquiring write lock
	if limiter, exists := sp.SourceRateLimiters[source.URL]; exists {
		return limiter
	}

	// Create rate limiter based on MaxConnections
	// Use MaxConnections as requests per second (reasonable default)
	rateLimit := source.MaxConnections
	if rateLimit <= 0 {
		rateLimit = 5 // Fallback default
	}

	limiter := ratelimit.New(rateLimit)
	sp.SourceRateLimiters[source.URL] = limiter

	if sp.Config.Debug {
		sp.Logger.Printf("[RATE_LIMITER] Created rate limiter for source %s: %d req/sec",
			source.Name, rateLimit)
	}

	return limiter
}
