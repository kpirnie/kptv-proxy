package proxy

import (
	"fmt"
	"kptv-proxy/work/buffer"
	"kptv-proxy/work/cache"
	"kptv-proxy/work/client"
	"kptv-proxy/work/config"
	"kptv-proxy/work/filter"
	"kptv-proxy/work/logger"
	"kptv-proxy/work/parser"
	"kptv-proxy/work/restream"
	"kptv-proxy/work/streamorder"
	"kptv-proxy/work/types"
	"kptv-proxy/work/utils"
	"kptv-proxy/work/watcher"
	"runtime"
	"strconv"

	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/panjf2000/ants/v2"
	"github.com/puzpuzpuz/xsync/v3"
	"go.uber.org/ratelimit"
)

// setup the proxy-wide client semaphore for limiting concurrent outbound requests
var (
	globalClientSemaphore chan struct{}
	semaphoreOnce         sync.Once
)

// StreamProxy represents the core application orchestrator responsible for managing
// the complete IPTV proxy lifecycle. It coordinates stream discovery, playlist
// generation, client connection handling, restreaming, and background maintenance
// tasks across all configured sources and channels.
type StreamProxy struct {
	Config                *config.Config                       // application configuration
	Channels              *xsync.MapOf[string, *types.Channel] // concurrent map of all discovered channels keyed by name
	Cache                 *cache.Cache                         // shared cache instance for playlists, EPG, and stream data
	BufferPool            *buffer.BufferPool                   // pooled byte buffers for efficient memory reuse during streaming
	HttpClient            *client.HeaderSettingClient          // pre-configured HTTP client with custom header injection
	WorkerPool            *ants.Pool                           // bounded goroutine pool for controlled concurrency
	MasterPlaylistHandler *parser.MasterPlaylistHandler        // HLS master playlist detection and resolution handler
	importStopChan        chan bool                            // signal channel to gracefully terminate the import refresh loop
	WatcherManager        *watcher.WatcherManager              // manages stream quality watchers for active restreaming sessions
	SourceRateLimiters    map[string]ratelimit.Limiter         // per-source rate limiters keyed by source URL
	rateLimiterMutex      sync.RWMutex                         // protects concurrent access to the rate limiter map
	FilterManager         *filter.FilterManager                // handles stream filtering rules from configuration
}

// New creates and initializes a new StreamProxy instance with all required dependencies.
// It wires together the configuration, buffer pool, HTTP client, worker pool, and cache
// into a fully operational proxy, including pre-initialization of per-source rate limiters
// to avoid lazy creation overhead during stream imports.
func New(cfg *config.Config, bufferPool *buffer.BufferPool, httpClient *client.HeaderSettingClient, workerPool *ants.Pool, cacheInstance *cache.Cache) *StreamProxy {
	logger.Debug("{proxy/stream - New} Initializing new StreamProxy instance")

	sp := &StreamProxy{
		Config:                cfg,
		Channels:              xsync.NewMapOf[string, *types.Channel](),
		Cache:                 cacheInstance,
		BufferPool:            bufferPool,
		HttpClient:            httpClient,
		WorkerPool:            workerPool,
		MasterPlaylistHandler: parser.NewMasterPlaylistHandler(cfg),
		importStopChan:        make(chan bool, 1),
		WatcherManager:        watcher.NewWatcherManager(),
		SourceRateLimiters:    make(map[string]ratelimit.Limiter),
		rateLimiterMutex:      sync.RWMutex{},
		FilterManager:         filter.NewFilterManager(),
	}

	// initialize all rate limiters upfront to avoid lazy creation during imports
	sp.initializeRateLimiters()

	// setup the global client semaphore based on configuration
	semaphoreOnce.Do(func() {
		globalClientSemaphore = make(chan struct{}, cfg.MaxConnectionsToApp)
	})

	logger.Debug("{proxy/stream - New} StreamProxy initialization complete")
	return sp
}

// initializeRateLimiters pre-creates all rate limiters during proxy initialization.
// Each configured source gets a dedicated limiter based on its MaxConnections setting,
// defaulting to 5 requests per second when no explicit limit is defined. Pre-creating
// these avoids contention on the rate limiter mutex during concurrent stream imports.
func (sp *StreamProxy) initializeRateLimiters() {
	logger.Debug("{proxy/stream - initializeRateLimiters} Initializing rate limiters for %d sources", len(sp.Config.Sources))

	for i := range sp.Config.Sources {
		source := &sp.Config.Sources[i]
		rateLimit := source.MaxConnections
		if rateLimit <= 0 {
			rateLimit = 5
			logger.Debug("{proxy/stream - initializeRateLimiters} No max connections set for %s, defaulting to %d req/sec", source.Name, rateLimit)
		}
		limiter := ratelimit.New(rateLimit)
		sp.SourceRateLimiters[source.URL] = limiter

		logger.Debug("{proxy/stream - initializeRateLimiters} Created rate limiter for source %s: %d req/sec",
			source.Name, rateLimit)
	}

	logger.Debug("{proxy/stream - initializeRateLimiters} All rate limiters initialized")
}

// channelBatch is a lightweight struct pairing a channel name with its channel pointer,
// used for efficient batch operations like sorting and playlist generation without
// needing to re-query the concurrent map during iteration.
type channelBatch struct {
	name    string         // channel name as stored in the map key
	channel *types.Channel // pointer to the channel data
}

// getChannelBatch snapshots the current channel map into an ordered slice for batch
// processing. This avoids holding read locks on the concurrent map during potentially
// expensive operations like sorting and playlist rendering.
func (sp *StreamProxy) getChannelBatch() []channelBatch {
	batch := make([]channelBatch, 0, 1000)
	sp.Channels.Range(func(name string, ch *types.Channel) bool {
		batch = append(batch, channelBatch{name, ch})
		return true
	})
	return batch
}

// ImportStreams performs comprehensive stream discovery and aggregation from all configured
// sources. Each source is fetched concurrently in its own goroutine, with connection
// tracking and rate limiting enforced per-source. Discovered streams are filtered,
// deduplicated by channel name, sorted according to configuration, and optionally
// reordered based on persisted custom stream order preferences.
//
// The import operation enforces a 2-minute global timeout to prevent indefinite blocking
// from unresponsive sources. Existing channel state (such as preferred stream indices)
// is preserved across import cycles when no custom ordering overrides are present.
func (sp *StreamProxy) ImportStreams() {
	logger.Debug("{proxy/stream - ImportStreams} Starting stream import for %d configured sources", len(sp.Config.Sources))

	if len(sp.Config.Sources) == 0 {
		logger.Warn("{proxy/stream - ImportStreams} No sources configured, skipping import")
		return
	}

	var wg sync.WaitGroup
	newChannels := xsync.NewMapOf[string, *types.Channel]()

	importSemaphore := make(chan struct{}, sp.Config.WorkerThreads)
	for i := range sp.Config.Sources {
		wg.Add(1)
		source := &sp.Config.Sources[i]

		importSemaphore <- struct{}{}
		go func(src *config.SourceConfig) {
			defer wg.Done()
			defer func() { <-importSemaphore }()

			// check if the source has capacity for another connection
			currentConns := src.ActiveConns.Load()
			if currentConns >= int32(src.MaxConnections) {
				logger.Warn("{proxy/stream - ImportStreams} Cannot import from source (connection limit %d/%d): %s",
					currentConns, src.MaxConnections, utils.LogURL(sp.Config, src.URL))
				return
			}

			// acquire a connection slot for parsing
			newConns := src.ActiveConns.Add(1)
			logger.Debug("{proxy/stream - ImportStreams} Acquired connection %d/%d for parsing: %s",
				newConns, src.MaxConnections, utils.LogURL(sp.Config, src.URL))

			defer func() {
				remainingConns := src.ActiveConns.Add(-1)
				logger.Debug("{proxy/stream - ImportStreams} Released parsing connection, remaining: %d/%d for: %s",
					remainingConns, src.MaxConnections, utils.LogURL(sp.Config, src.URL))
			}()

			rateLimiter := sp.getRateLimiterForSource(src)

			// parse streams based on source type
			var streams []*types.Stream
			if src.Username != "" && src.Password != "" {
				logger.Debug("{proxy/stream - ImportStreams} Parsing Xtreme Codes API source: %s", src.Name)
				streams = parser.ParseXtremeCodesAPI(sp.HttpClient, sp.Config, src, rateLimiter, sp.Cache)
			} else {
				logger.Debug("{proxy/stream - ImportStreams} Parsing M3U8 source: %s", src.Name)
				streams = parser.ParseM3U8(sp.HttpClient, sp.Config, src, rateLimiter, sp.Cache)
			}

			// apply stream filters if the filter manager is available
			if sp != nil && sp.FilterManager != nil {
				beforeFilter := len(streams)
				streams = filter.FilterStreams(streams, src, sp.FilterManager)
				if beforeFilter != len(streams) {
					logger.Debug("{proxy/stream - ImportStreams} Filtered %d streams down to %d for source: %s", beforeFilter, len(streams), src.Name)
				}
			}

			if src.Username != "" && src.Password != "" {
				logger.Debug("{proxy/stream - ImportStreams} Parsed %d streams from Xtreme Codes API: %s", len(streams), utils.LogURL(sp.Config, src.URL))
			} else {
				logger.Debug("{proxy/stream - ImportStreams} Parsed %d streams from M3U8 source: %s", len(streams), utils.LogURL(sp.Config, src.URL))
			}

			// aggregate streams into channels, merging duplicates by name
			for _, stream := range streams {
				channelName := stream.Name

				channel, _ := newChannels.LoadOrStore(channelName, &types.Channel{
					Name:                 channelName,
					Streams:              []*types.Stream{},
					PreferredStreamIndex: 0,
				})

				channel.Mu.Lock()
				channel.Streams = append(channel.Streams, stream)
				channel.Mu.Unlock()
			}
		}(source)
	}

	// wait for all goroutines with a global timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		logger.Debug("{proxy/stream - ImportStreams} All source imports completed successfully")
	case <-time.After(2 * time.Minute):
		logger.Warn("{proxy/stream - ImportStreams} Global timeout reached (2 minutes), some sources may not have completed")
	}

	// sort streams within each channel and apply custom ordering if configured
	count := 0
	newChannels.Range(func(key string, value *types.Channel) bool {
		channelName := key
		channel := value

		parser.SortStreams(channel.Streams, sp.Config, channelName)

		// apply persisted custom stream order if one exists for this channel
		customOrder, _ := streamorder.GetChannelStreamOrder(channelName)
		if len(customOrder) > 0 {
			reorderedStreams := make([]*types.Stream, 0, len(channel.Streams))

			// place streams in the custom order first
			for _, idx := range customOrder {
				if idx >= 0 && idx < len(channel.Streams) {
					reorderedStreams = append(reorderedStreams, channel.Streams[idx])
				}
			}

			// append any remaining streams not covered by the custom order
			usedIndices := make(map[int]bool)
			for _, idx := range customOrder {
				usedIndices[idx] = true
			}

			for i, stream := range channel.Streams {
				if !usedIndices[i] {
					reorderedStreams = append(reorderedStreams, stream)
				}
			}

			channel.Streams = reorderedStreams
			atomic.StoreInt32(&channel.PreferredStreamIndex, 0)
			logger.Debug("{proxy/stream - ImportStreams} Applied custom stream order for channel: %s (%d ordered, %d total)", channelName, len(customOrder), len(channel.Streams))
		} else {
			// preserve the existing preferred stream index across import cycles
			if existingChannel, exists := sp.Channels.Load(channelName); exists {
				existingPreferred := atomic.LoadInt32(&existingChannel.PreferredStreamIndex)
				atomic.StoreInt32(&channel.PreferredStreamIndex, existingPreferred)
			}
		}

		sp.Channels.Store(key, channel)
		count++
		return true
	})

	// clear stale cache entries after a full import cycle
	if sp.Config.CacheEnabled {
		sp.Cache.ClearIfNeeded()
		logger.Debug("{proxy/stream - ImportStreams} Cache cleanup triggered after import")
	}

	logger.Debug("{proxy/stream - ImportStreams} Import complete: %d channels discovered", count)
}

// GeneratePlaylist creates and serves a complete M3U8 playlist containing all discovered
// channels. When a group filter is provided, only channels matching that group are included.
// The generated playlist is cached when caching is enabled to avoid regeneration on
// subsequent requests within the cache TTL.
//
// Channels are sorted according to the configured sort field and direction before
// rendering. Each channel entry includes its stream attributes and a proxy URL pointing
// back to this server for transparent stream proxying.
func (sp *StreamProxy) GeneratePlaylist(w http.ResponseWriter, r *http.Request, groupFilter string) {
	var seenClients sync.Map

	clientKey := r.RemoteAddr + "-" + r.Header.Get("User-Agent")
	if _, seen := seenClients.LoadOrStore(clientKey, true); !seen {
		logger.Debug("{proxy/stream - GeneratePlaylist} New playlist client: %s (%s)",
			r.RemoteAddr, r.Header.Get("User-Agent"))
	}

	// construct cache key with optional group filter suffix
	cacheKey := "playlist"
	if groupFilter != "" {
		cacheKey = "playlist_" + strings.ToLower(groupFilter)
	}

	// serve from cache if available
	if sp.Config.CacheEnabled {
		if cached, ok := sp.Cache.GetM3U8(cacheKey); ok {
			logger.Debug("{proxy/stream - GeneratePlaylist} Serving cached playlist (key: %s)", cacheKey)
			w.Header().Set("Content-Type", "application/x-mpegURL")
			w.Header().Set("Cache-Control", "no-cache")
			w.Write([]byte(cached))
			return
		}
	}

	channels := sp.getChannelBatch()
	logger.Debug("{proxy/stream - GeneratePlaylist} Building playlist from %d channels", len(channels))

	// sort channels according to configured field and direction
	sort.SliceStable(channels, func(i, j int) bool {
		iVal := sp.getChannelSortValue(channels[i])
		jVal := sp.getChannelSortValue(channels[j])

		if sp.Config.SortDirection == "desc" {
			return iVal > jVal
		}
		return iVal < jVal
	})

	// pre-allocate the builder with a reasonable estimate
	estimatedSize := len(channels) * 250
	var playlist strings.Builder
	playlist.Grow(estimatedSize)
	playlist.WriteString("#EXTM3U\n")

	filteredCount := 0

	for _, ch := range channels {
		ch.channel.Mu.RLock()
		if len(ch.channel.Streams) > 0 {
			attrs := ch.channel.Streams[0].Attributes

			// skip channels that don't match the group filter
			if groupFilter != "" {
				channelGroup := sp.GetChannelGroup(attrs)
				if !strings.EqualFold(channelGroup, groupFilter) {
					ch.channel.Mu.RUnlock()
					continue
				}
			}

			filteredCount++

			// write the EXTINF line with all stream attributes
			playlist.WriteString("#EXTINF:-1")
			for key, value := range attrs {
				if key != "tvg-name" && key != "duration" {
					if strings.ContainsAny(value, ",\"") {
						value = fmt.Sprintf("%q", value)
					}
					playlist.WriteString(fmt.Sprintf(" %s=\"%s\"", key, value))
				}
			}

			// write the channel name and proxy URL
			cleanName := strings.Trim(ch.name, "\"")
			playlist.WriteString(fmt.Sprintf(",%s\n", cleanName))
			safeName := utils.SanitizeChannelName(ch.name)
			proxyURL := fmt.Sprintf("%s/s/%s", sp.Config.BaseURL, safeName)
			playlist.WriteString(proxyURL + "\n")
		}
		ch.channel.Mu.RUnlock()
	}

	result := playlist.String()

	// cache the generated playlist for subsequent requests
	if sp.Config.CacheEnabled {
		sp.Cache.SetM3U8(cacheKey, result)
		logger.Debug("{proxy/stream - GeneratePlaylist} Cached generated playlist (key: %s)", cacheKey)
	}

	w.Header().Set("Content-Type", "application/x-mpegURL")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write([]byte(result))

	if groupFilter == "" {
		logger.Debug("{proxy/stream - GeneratePlaylist} Generated playlist with %d channels", len(channels))
	} else {
		logger.Debug("{proxy/stream - GeneratePlaylist} Generated playlist for group '%s' with %d channels (out of %d total)", groupFilter, filteredCount, len(channels))
	}
}

// GetChannelGroup extracts the group classification from channel attributes by checking
// for the standard "tvg-group" attribute first, then falling back to "group-title".
// Returns an empty string if no group classification is found in either attribute.
func (sp *StreamProxy) GetChannelGroup(attrs map[string]string) string {
	if group, exists := attrs["tvg-group"]; exists && group != "" {
		return group
	}
	if group, exists := attrs["group-title"]; exists && group != "" {
		return group
	}
	return ""
}

// StartImportRefresh initiates periodic background import refresh at the interval
// configured in ImportRefreshInterval. It runs in a blocking loop and should be
// launched in its own goroutine. The loop terminates gracefully when a signal is
// received on the import stop channel via StopImportRefresh.
func (sp *StreamProxy) StartImportRefresh() {
	logger.Debug("{proxy/stream - StartImportRefresh} Starting import refresh loop (interval: %s)", sp.Config.ImportRefreshInterval)

	ticker := time.NewTicker(sp.Config.ImportRefreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-sp.importStopChan:
			logger.Debug("{proxy/stream - StartImportRefresh} Import refresh loop stopped")
			return
		case <-ticker.C:
			logger.Debug("{proxy/stream - StartImportRefresh} Triggering scheduled import refresh")
			sp.ImportStreams()
			logger.Debug("{proxy/stream - StartImportRefresh} Scheduled import refresh complete")
		}
	}
}

// StopImportRefresh signals the periodic import refresh loop to terminate gracefully.
// It sends a non-blocking signal to the stop channel, ensuring the caller never blocks
// even if the refresh loop has already stopped or hasn't started yet.
func (sp *StreamProxy) StopImportRefresh() {
	logger.Debug("{proxy/stream - StopImportRefresh} Sending stop signal to import refresh loop")
	if sp.importStopChan != nil {
		select {
		case sp.importStopChan <- true:
			logger.Debug("{proxy/stream - StopImportRefresh} Stop signal sent successfully")
		default:
			logger.Warn("{proxy/stream - StopImportRefresh} Stop channel already full, refresh loop may have already stopped")
		}
	}
}

// RestreamCleanup implements background maintenance for inactive restreaming connections.
// It runs every 10 seconds and performs two categories of cleanup:
//   - Stopped restreamers: cleaned up after 30 seconds of inactivity, force-cleaned after 60
//   - Running restreamers: removes individual clients inactive for 120+ seconds, and stops
//     the entire restreamer if no active clients remain for 120+ seconds
//
// After each cleanup cycle, a GC pass and buffer pool cleanup are triggered to reclaim
// memory from destroyed stream buffers and disconnected client resources.
func (sp *StreamProxy) RestreamCleanup() {
	logger.Debug("{proxy/stream - RestreamCleanup} Starting restream cleanup loop (interval: 10s)")

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now().Unix()

		sp.Channels.Range(func(key string, channel *types.Channel) bool {
			channel.Mu.Lock()

			if channel.Restreamer != nil {
				if !channel.Restreamer.Running.Load() {
					lastActivity := channel.Restreamer.LastActivity.Load()

					if now-lastActivity > 30 {
						select {
						case <-channel.Restreamer.Ctx.Done():
							// context already cancelled, force clean after 60 seconds
							if now-lastActivity > 60 {
								logger.Debug("{proxy/stream - RestreamCleanup} Channel %s: Force cleaning cancelled context after 60s", channel.Name)

								if channel.Restreamer.Buffer != nil && !channel.Restreamer.Buffer.IsDestroyed() {
									channel.Restreamer.Buffer.Destroy()
								}
								channel.Restreamer.Cancel()
								channel.Restreamer = nil
							}
						default:
							// gracefully destroy the buffer and cancel the context
							if channel.Restreamer.Buffer != nil && !channel.Restreamer.Buffer.IsDestroyed() {
								logger.Debug("{proxy/stream - RestreamCleanup} Channel %s: Safely destroying buffer", channel.Name)
								channel.Restreamer.Buffer.Destroy()
							}
							channel.Restreamer.Cancel()
							channel.Restreamer = nil
							logger.Debug("{proxy/stream - RestreamCleanup} Cleaned up inactive restreamer for channel: %s (idle %ds)", channel.Name, now-lastActivity)
						}
					}

				} else {
					// check individual client activity on running restreamers
					clientCount := 0
					channel.Restreamer.Clients.Range(func(ckey string, cvalue *types.RestreamClient) bool {
						client := cvalue
						lastSeen := client.LastSeen.Load()

						if now-lastSeen > 120 {
							logger.Debug("{proxy/stream - RestreamCleanup} Removing inactive client: %s (last seen %ds ago)", ckey, now-lastSeen)
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

					// stop the restreamer entirely if no active clients remain
					if clientCount == 0 && channel.Restreamer.Running.Load() {
						lastActivity := channel.Restreamer.LastActivity.Load()
						if now-lastActivity > 120 {
							logger.Debug("{proxy/stream - RestreamCleanup} No active clients for channel %s (idle %ds), stopping restreamer", channel.Name, now-lastActivity)
							channel.Restreamer.Cancel()
							channel.Restreamer.Running.Store(false)

							if channel.Restreamer.Buffer != nil && !channel.Restreamer.Buffer.IsDestroyed() {
								logger.Debug("{proxy/stream - RestreamCleanup} Channel %s: Safely destroying buffer", channel.Name)
								channel.Restreamer.Buffer.Destroy()
							}
						}
					}
				}
			}

			channel.Mu.Unlock()
			return true
		})

		// trigger garbage collection and buffer pool cleanup after each sweep
		runtime.GC()

		if sp.BufferPool != nil {
			sp.BufferPool.Cleanup()
		}
	}
}

// FindChannelBySafeName resolves original channel names from URL-safe identifiers.
// It attempts resolution in three stages:
//   - Direct underscore-to-space replacement for simple matches
//   - Full channel map scan comparing sanitized names against the input
//   - Passthrough of the original input as a last resort
//
// URL-encoded inputs are automatically decoded before resolution begins.
func (sp *StreamProxy) FindChannelBySafeName(safeName string) string {
	if decoded, err := url.QueryUnescape(safeName); err == nil {
		safeName = decoded
	}

	// try the simple underscore-to-space replacement first
	simpleName := strings.ReplaceAll(safeName, "_", " ")
	if _, exists := sp.Channels.Load(simpleName); exists {
		logger.Debug("{proxy/stream - FindChannelBySafeName} Resolved channel by simple name: %s", simpleName)
		return simpleName
	}

	// fall back to scanning the full channel map for a sanitized match
	var foundName string
	sp.Channels.Range(func(name string, _ *types.Channel) bool {
		if utils.SanitizeChannelName(name) == safeName {
			foundName = name
			return false
		}
		return true
	})

	if foundName != "" {
		logger.Debug("{proxy/stream - FindChannelBySafeName} Resolved channel by sanitized name scan: %s -> %s", safeName, foundName)
		return foundName
	}

	logger.Debug("{proxy/stream - FindChannelBySafeName} No exact match found, using input as-is: %s", safeName)
	return safeName
}

// HandleRestreamingClient manages the complete lifecycle of a streaming client connection.
// It initializes or reuses an existing restreamer for the requested channel, registers
// the client, sets appropriate streaming headers, and blocks until the client disconnects
// or a 24-hour maximum session timeout is reached.
//
// When the watcher system is enabled, stream quality monitoring is automatically started
// for the active restreaming session to enable automatic failover on quality degradation.
func (sp *StreamProxy) HandleRestreamingClient(w http.ResponseWriter, r *http.Request, channel *types.Channel) {

	// Acquire global connection slot
	select {
	case globalClientSemaphore <- struct{}{}:
		defer func() { <-globalClientSemaphore }()
	default:
		logger.Debug("{proxy/stream - HandleRestreamingClient} Max connections reached (%d), rejecting client", sp.Config.MaxConnectionsToApp)
		http.Error(w, "Server at capacity", http.StatusServiceUnavailable)
		return
	}

	logger.Debug("{proxy/stream - HandleRestreamingClient} Channel %s: New client request from %s", channel.Name, r.RemoteAddr)
	logger.Debug("{proxy/stream - HandleRestreamingClient} Channel %s: %d available streams", channel.Name, len(channel.Streams))

	if sp.Config.FFmpegMode {
		logger.Debug("{proxy/stream - HandleRestreamingClient} Channel %s: Using FFMPEG mode", channel.Name)
	} else {
		logger.Debug("{proxy/stream - HandleRestreamingClient} Channel %s: Using RESTREAMING mode", channel.Name)
	}

	channel.Mu.Lock()
	var restreamer *restream.Restream
	if channel.Restreamer == nil {
		var rateLimiter ratelimit.Limiter
		if len(channel.Streams) > 0 {
			rateLimiter = sp.getRateLimiterForSource(channel.Streams[0].Source)
		}

		logger.Debug("{proxy/stream - HandleRestreamingClient} Channel %s: Creating new restreamer with rate limiting", channel.Name)

		restreamer = restream.NewRestreamer(channel, (sp.Config.BufferSizePerStream * 1024 * 1024), sp.HttpClient, sp.Config, rateLimiter)
		channel.Restreamer = restreamer.Restreamer
	} else {
		// reuse the existing restreamer, ensuring the client map is initialized
		if channel.Restreamer.Clients == nil {
			channel.Restreamer.Clients = xsync.NewMapOf[string, *types.RestreamClient]()
			logger.Debug("{proxy/stream - HandleRestreamingClient} Channel %s: Re-initialized client map on existing restreamer", channel.Name)
		}
		restreamer = &restream.Restream{Restreamer: channel.Restreamer}
		logger.Debug("{proxy/stream - HandleRestreamingClient} Channel %s: Reusing existing restreamer", channel.Name)
	}
	channel.Mu.Unlock()

	// generate a unique client identifier
	clientID := fmt.Sprintf("%s-%d", r.RemoteAddr, time.Now().UnixNano())

	// set streaming response headers
	w.Header().Set("Content-Type", "video/mp2t")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Accept", "*/*")

	// resolve the flusher interface, handling custom response writer wrappers
	var flusher http.Flusher
	var ok bool
	if crw, isCustom := w.(*client.CustomResponseWriter); isCustom {
		flusher, ok = crw.ResponseWriter.(http.Flusher)
	} else {
		flusher, ok = w.(http.Flusher)
	}
	if !ok {
		logger.Error("{proxy/stream - HandleRestreamingClient} Streaming not supported for client: %s (ResponseWriter does not implement http.Flusher)", clientID)
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	logger.Debug("{proxy/stream - HandleRestreamingClient} Channel %s: Registered client %s", channel.Name, clientID)

	restreamer.AddClient(clientID, w, flusher)

	// start stream quality watcher if enabled and the restreamer is actively running
	if sp.Config.WatcherEnabled && restreamer.Restreamer.Running.Load() {
		preferredIndex := int(atomic.LoadInt32(&channel.PreferredStreamIndex))
		currentIndex := int(atomic.LoadInt32(&restreamer.Restreamer.CurrentIndex))

		actualIndex := preferredIndex
		if preferredIndex < 0 || preferredIndex >= len(channel.Streams) {
			actualIndex = currentIndex
		}

		logger.Debug("{proxy/stream - HandleRestreamingClient} Channel %s: Preferred=%d, Current=%d, Using=%d",
			channel.Name, preferredIndex, currentIndex, actualIndex)

		sp.WatcherManager.StartWatching(channel.Name, restreamer.Restreamer)
	}

	// deferred cleanup to remove the client on disconnect
	cleanup := func() {
		restreamer.RemoveClient(clientID)
		logger.Debug("{proxy/stream - HandleRestreamingClient} Channel %s: Removed client %s", channel.Name, clientID)
	}
	defer cleanup()

	// block until the client disconnects or the session timeout is reached
	done := make(chan struct{})
	go func() {
		defer close(done)
		<-r.Context().Done()
	}()

	select {
	case <-done:
		logger.Debug("{proxy/stream - HandleRestreamingClient} Client disconnected: %s (channel: %s)", clientID, channel.Name)
	case <-time.After(24 * time.Hour):
		logger.Warn("{proxy/stream - HandleRestreamingClient} Client session timeout after 24h: %s (channel: %s)", clientID, channel.Name)
	}
}

// ShouldCheckForMasterPlaylist analyzes HTTP response headers to determine whether
// the response body likely contains an HLS master playlist rather than a media segment.
// It checks for M3U8/mpegURL content types and small content lengths (under 100KB) as
// indicators that the response is a playlist requiring further resolution rather than
// streamable media data.
func (sp *StreamProxy) ShouldCheckForMasterPlaylist(resp *http.Response) bool {
	contentType := resp.Header.Get("Content-Type")
	contentLength := resp.Header.Get("Content-Length")

	if strings.Contains(strings.ToLower(contentType), "mpegurl") ||
		strings.Contains(strings.ToLower(contentType), "m3u8") {
		logger.Debug("{proxy/stream - ShouldCheckForMasterPlaylist} Detected playlist content type: %s", contentType)
		return true
	}

	if contentLength != "" {
		if length, err := strconv.ParseInt(contentLength, 10, 64); err == nil {
			if length > 0 && length < 100*1024 {
				logger.Debug("{proxy/stream - ShouldCheckForMasterPlaylist} Small content length detected (%d bytes), may be a playlist", length)
				return true
			}
		}
	}

	return false
}

// GetChannelNameFromStream extracts the most appropriate display name for a channel
// from its stream metadata. It prefers the "tvg-name" attribute when available and
// non-empty, falling back to the stream's Name field as a default.
func (sp *StreamProxy) GetChannelNameFromStream(stream *types.Stream) string {
	if name, ok := stream.Attributes["tvg-name"]; ok && name != "" {
		return name
	}
	return stream.Name
}

// getRateLimiterForSource retrieves the pre-initialized rate limiter for a given source.
// It performs a double-checked lock pattern: first attempting a read-only lookup, then
// falling back to a write-locked creation if the limiter doesn't exist yet. This handles
// dynamically discovered sources that weren't present during initial configuration.
func (sp *StreamProxy) getRateLimiterForSource(source *config.SourceConfig) ratelimit.Limiter {
	// fast path: read-only lookup for pre-initialized limiters
	sp.rateLimiterMutex.RLock()
	limiter, exists := sp.SourceRateLimiters[source.URL]
	sp.rateLimiterMutex.RUnlock()

	if exists {
		return limiter
	}

	// slow path: acquire write lock and create the limiter if still missing
	sp.rateLimiterMutex.Lock()
	defer sp.rateLimiterMutex.Unlock()

	// re-check after acquiring the write lock to avoid duplicate creation
	if limiter, exists := sp.SourceRateLimiters[source.URL]; exists {
		return limiter
	}

	rateLimit := source.MaxConnections
	if rateLimit <= 0 {
		rateLimit = 5
	}

	limiter = ratelimit.New(rateLimit)
	sp.SourceRateLimiters[source.URL] = limiter

	logger.Debug("{proxy/stream - getRateLimiterForSource} Created rate limiter for dynamic source %s: %d req/sec",
		source.Name, rateLimit)

	return limiter
}

// getChannelSortValue extracts the sort value from a channel based on the configured
// sort field. It reads from the first stream's attributes using the field specified in
// Config.SortField, defaulting to "tvg-name" when no sort field is configured. All
// values are lowercased for case-insensitive sorting. Falls back to the channel name
// if the sort field attribute doesn't exist on the stream.
func (sp *StreamProxy) getChannelSortValue(ch channelBatch) string {
	ch.channel.Mu.RLock()
	defer ch.channel.Mu.RUnlock()

	if len(ch.channel.Streams) == 0 {
		return ""
	}

	sortField := sp.Config.SortField
	if sortField == "" {
		sortField = "tvg-name"
	}

	if value, exists := ch.channel.Streams[0].Attributes[sortField]; exists {
		return strings.ToLower(value)
	}

	return strings.ToLower(ch.name)
}
