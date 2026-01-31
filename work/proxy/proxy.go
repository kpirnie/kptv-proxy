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

// StreamProxy represents the core application orchestrator responsible for managing
// the complete IPTV proxy lifecycle.
type StreamProxy struct {
	Config                *config.Config
	Channels              *xsync.MapOf[string, *types.Channel]
	Cache                 *cache.Cache
	BufferPool            *buffer.BufferPool
	HttpClient            *client.HeaderSettingClient
	WorkerPool            *ants.Pool
	MasterPlaylistHandler *parser.MasterPlaylistHandler
	importStopChan        chan bool
	WatcherManager        *watcher.WatcherManager
	SourceRateLimiters    map[string]ratelimit.Limiter
	rateLimiterMutex      sync.RWMutex
	FilterManager         *filter.FilterManager
}

// New creates and initializes a new StreamProxy instance with all required dependencies.
func New(cfg *config.Config, bufferPool *buffer.BufferPool, httpClient *client.HeaderSettingClient, workerPool *ants.Pool, cacheInstance *cache.Cache) *StreamProxy {
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

	// Initialize all rate limiters upfront
	sp.initializeRateLimiters()

	return sp
}

// initializeRateLimiters pre-creates all rate limiters during proxy initialization.
func (sp *StreamProxy) initializeRateLimiters() {
	for i := range sp.Config.Sources {
		source := &sp.Config.Sources[i]
		rateLimit := source.MaxConnections
		if rateLimit <= 0 {
			rateLimit = 5
		}
		limiter := ratelimit.New(rateLimit)
		sp.SourceRateLimiters[source.URL] = limiter

		logger.Debug("[RATE_LIMITER] Created rate limiter for source %s: %d req/sec",
			source.Name, rateLimit)
	}
}

type channelBatch struct {
	name    string
	channel *types.Channel
}

func (sp *StreamProxy) getChannelBatch() []channelBatch {
	batch := make([]channelBatch, 0, 1000)
	sp.Channels.Range(func(name string, ch *types.Channel) bool {
		batch = append(batch, channelBatch{name, ch})
		return true
	})
	return batch
}

// ImportStreams performs comprehensive stream discovery and aggregation from all configured sources.
func (sp *StreamProxy) ImportStreams() {
	logger.Debug("Starting stream import...")

	if len(sp.Config.Sources) == 0 {
		logger.Warn("WARNING: No sources configured!")
		return
	}

	var wg sync.WaitGroup
	newChannels := xsync.NewMapOf[string, *types.Channel]()

	for i := range sp.Config.Sources {
		wg.Add(1)
		source := &sp.Config.Sources[i]

		go func(src *config.SourceConfig) {
			defer wg.Done()

			currentConns := src.ActiveConns.Load()
			if currentConns >= int32(src.MaxConnections) {
				logger.Warn("Cannot import from source (connection limit %d/%d): %s",
					currentConns, src.MaxConnections, utils.LogURL(sp.Config, src.URL))
				return
			}

			newConns := src.ActiveConns.Add(1)
			logger.Debug("[IMPORT_CONNECTION] Acquired connection %d/%d for parsing: %s",
				newConns, src.MaxConnections, utils.LogURL(sp.Config, src.URL))

			defer func() {
				remainingConns := src.ActiveConns.Add(-1)
				logger.Debug("[IMPORT_RELEASE] Released parsing connection, remaining: %d/%d for: %s",
					remainingConns, src.MaxConnections, utils.LogURL(sp.Config, src.URL))
			}()

			rateLimiter := sp.getRateLimiterForSource(src)

			var streams []*types.Stream
			if src.Username != "" && src.Password != "" {
				streams = parser.ParseXtremeCodesAPI(sp.HttpClient, sp.Config, src, rateLimiter, sp.Cache)
			} else {
				streams = parser.ParseM3U8(sp.HttpClient, sp.Config, src, rateLimiter, sp.Cache)
			}

			if sp != nil && sp.FilterManager != nil {
				streams = filter.FilterStreams(streams, src, sp.FilterManager)
			}

			if src.Username != "" && src.Password != "" {
				logger.Debug("Parsed %d streams from Xtreme Codes API: %s", len(streams), utils.LogURL(sp.Config, src.URL))
			} else {
				logger.Debug("Parsed %d streams from M3U8 source: %s", len(streams), utils.LogURL(sp.Config, src.URL))
			}

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

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		logger.Debug("All imports completed successfully")
	case <-time.After(2 * time.Minute):
		logger.Warn("Import timeout reached, some sources may not have completed")
	}

	count := 0
	newChannels.Range(func(key string, value *types.Channel) bool {
		channelName := key
		channel := value

		parser.SortStreams(channel.Streams, sp.Config, channelName)

		customOrder, _ := streamorder.GetChannelStreamOrder(channelName)
		if len(customOrder) > 0 {
			reorderedStreams := make([]*types.Stream, 0, len(channel.Streams))

			for _, idx := range customOrder {
				if idx >= 0 && idx < len(channel.Streams) {
					reorderedStreams = append(reorderedStreams, channel.Streams[idx])
				}
			}

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
		} else {
			if existingChannel, exists := sp.Channels.Load(channelName); exists {
				existingPreferred := atomic.LoadInt32(&existingChannel.PreferredStreamIndex)
				atomic.StoreInt32(&channel.PreferredStreamIndex, existingPreferred)
			}
		}

		sp.Channels.Store(key, channel)
		count++
		return true
	})

	if sp.Config.CacheEnabled {
		sp.Cache.ClearIfNeeded()
	}

	logger.Debug("Import complete. Found %d channels", count)
}

// GeneratePlaylist creates and serves a complete M3U8 playlist.
func (sp *StreamProxy) GeneratePlaylist(w http.ResponseWriter, r *http.Request, groupFilter string) {
	var seenClients sync.Map

	clientKey := r.RemoteAddr + "-" + r.Header.Get("User-Agent")
	if _, seen := seenClients.LoadOrStore(clientKey, true); !seen {
		logger.Debug("New playlist client: %s (%s)",
			r.RemoteAddr, r.Header.Get("User-Agent"))
	}

	cacheKey := "playlist"
	if groupFilter != "" {
		cacheKey = "playlist_" + strings.ToLower(groupFilter)
	}

	if sp.Config.CacheEnabled {
		if cached, ok := sp.Cache.GetM3U8(cacheKey); ok {
			w.Header().Set("Content-Type", "application/x-mpegURL")
			w.Header().Set("Cache-Control", "no-cache")
			w.Write([]byte(cached))
			return
		}
	}

	channels := sp.getChannelBatch()

	sort.SliceStable(channels, func(i, j int) bool {
		iVal := sp.getChannelSortValue(channels[i])
		jVal := sp.getChannelSortValue(channels[j])

		if sp.Config.SortDirection == "desc" {
			return iVal > jVal
		}
		return iVal < jVal
	})

	estimatedSize := len(channels) * 250
	var playlist strings.Builder
	playlist.Grow(estimatedSize)
	playlist.WriteString("#EXTM3U\n")

	filteredCount := 0

	for _, ch := range channels {
		ch.channel.Mu.RLock()
		if len(ch.channel.Streams) > 0 {
			attrs := ch.channel.Streams[0].Attributes

			if groupFilter != "" {
				channelGroup := sp.GetChannelGroup(attrs)
				if !strings.EqualFold(channelGroup, groupFilter) {
					ch.channel.Mu.RUnlock()
					continue
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

			cleanName := strings.Trim(ch.name, "\"")
			playlist.WriteString(fmt.Sprintf(",%s\n", cleanName))
			safeName := utils.SanitizeChannelName(ch.name)
			proxyURL := fmt.Sprintf("%s/s/%s", sp.Config.BaseURL, safeName)
			playlist.WriteString(proxyURL + "\n")
		}
		ch.channel.Mu.RUnlock()
	}

	result := playlist.String()

	if sp.Config.CacheEnabled {
		sp.Cache.SetM3U8(cacheKey, result)
	}

	w.Header().Set("Content-Type", "application/x-mpegURL")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write([]byte(result))

	if groupFilter == "" {
		logger.Debug("Generated playlist with %d channels", len(channels))
	} else {
		logger.Debug("Generated playlist for group '%s' with %d channels (out of %d total)", groupFilter, filteredCount, len(channels))
	}
}

// GetChannelGroup extracts the group classification from channel attributes.
func (sp *StreamProxy) GetChannelGroup(attrs map[string]string) string {
	if group, exists := attrs["tvg-group"]; exists && group != "" {
		return group
	}
	if group, exists := attrs["group-title"]; exists && group != "" {
		return group
	}
	return ""
}

// StartImportRefresh initiates periodic background import refresh.
func (sp *StreamProxy) StartImportRefresh() {
	ticker := time.NewTicker(sp.Config.ImportRefreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-sp.importStopChan:
			logger.Debug("Import refresh stopped")
			return
		case <-ticker.C:
			logger.Debug("Refreshing imports...")
			sp.ImportStreams()
		}
	}
}

// StopImportRefresh signals the periodic import refresh loop to terminate.
func (sp *StreamProxy) StopImportRefresh() {
	if sp.importStopChan != nil {
		select {
		case sp.importStopChan <- true:
		default:
		}
	}
}

// RestreamCleanup implements background maintenance for inactive restreaming connections.
func (sp *StreamProxy) RestreamCleanup() {
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
							if now-lastActivity > 60 {
								logger.Debug("[CLEANUP_FORCE] Channel %s: Force cleaning cancelled context after 60s", channel.Name)

								if channel.Restreamer.Buffer != nil && !channel.Restreamer.Buffer.IsDestroyed() {
									channel.Restreamer.Buffer.Destroy()
								}
								channel.Restreamer.Cancel()
								channel.Restreamer = nil
							}
						default:
							if channel.Restreamer.Buffer != nil && !channel.Restreamer.Buffer.IsDestroyed() {
								logger.Debug("[CLEANUP_BUFFER_DESTROY] Channel %s: Safely destroying buffer", channel.Name)
								channel.Restreamer.Buffer.Destroy()
							}
							channel.Restreamer.Cancel()
							channel.Restreamer = nil
							logger.Debug("Cleaned up inactive restreamer for channel: %s", channel.Name)
						}
					}

				} else {
					clientCount := 0
					channel.Restreamer.Clients.Range(func(ckey string, cvalue *types.RestreamClient) bool {
						client := cvalue
						lastSeen := client.LastSeen.Load()

						if now-lastSeen > 120 {
							logger.Debug("Removing inactive client: %s (last seen %d seconds ago)", ckey, now-lastSeen)
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
							logger.Debug("No active clients found, stopping restreamer for: %s", channel.Name)
							channel.Restreamer.Cancel()
							channel.Restreamer.Running.Store(false)

							if channel.Restreamer.Buffer != nil && !channel.Restreamer.Buffer.IsDestroyed() {
								logger.Debug("[CLEANUP_NO_CLIENTS] Channel %s: Safely destroying buffer", channel.Name)
								channel.Restreamer.Buffer.Destroy()
							}
						}
					}
				}
			}

			channel.Mu.Unlock()
			return true
		})

		runtime.GC()

		if sp.BufferPool != nil {
			sp.BufferPool.Cleanup()
		}
	}
}

// FindChannelBySafeName resolves original channel names from URL-safe identifiers.
func (sp *StreamProxy) FindChannelBySafeName(safeName string) string {
	if decoded, err := url.QueryUnescape(safeName); err == nil {
		safeName = decoded
	}

	simpleName := strings.ReplaceAll(safeName, "_", " ")
	if _, exists := sp.Channels.Load(simpleName); exists {
		return simpleName
	}

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

// HandleRestreamingClient manages the complete lifecycle of a streaming client connection.
func (sp *StreamProxy) HandleRestreamingClient(w http.ResponseWriter, r *http.Request, channel *types.Channel) {
	logger.Debug("[STREAM_REQUEST] Channel %s: URL: %s", channel.Name, channel.Name)
	logger.Debug("[FOUND] Channel %s: Streams: %d", channel.Name, len(channel.Streams))
	if sp.Config.FFmpegMode {
		logger.Debug("Using FFMPEG mode for channel: %s", channel.Name)
	} else {
		logger.Debug("Using RESTREAMING mode for channel: %s", channel.Name)
	}

	channel.Mu.Lock()
	var restreamer *restream.Restream
	if channel.Restreamer == nil {
		var rateLimiter ratelimit.Limiter
		if len(channel.Streams) > 0 {
			rateLimiter = sp.getRateLimiterForSource(channel.Streams[0].Source)
		}

		logger.Debug("[RESTREAM_NEW] Channel %s: Creating new restreamer with rate limiting", channel.Name)

		restreamer = restream.NewRestreamer(channel, (sp.Config.BufferSizePerStream * 1024 * 1024), sp.HttpClient, sp.Config, rateLimiter)
		channel.Restreamer = restreamer.Restreamer
	} else {
		if channel.Restreamer.Clients == nil {
			channel.Restreamer.Clients = xsync.NewMapOf[string, *types.RestreamClient]()
		}
		restreamer = &restream.Restream{Restreamer: channel.Restreamer}
	}
	channel.Mu.Unlock()

	clientID := fmt.Sprintf("%s-%d", r.RemoteAddr, time.Now().UnixNano())

	w.Header().Set("Content-Type", "video/mp2t")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Accept", "*/*")

	var flusher http.Flusher
	var ok bool
	if crw, isCustom := w.(*client.CustomResponseWriter); isCustom {
		flusher, ok = crw.ResponseWriter.(http.Flusher)
	} else {
		flusher, ok = w.(http.Flusher)
	}
	if !ok {
		logger.Error("Streaming not supported for client: %s", clientID)
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	logger.Debug("[RESTREAM_START] Channel %s: Client: %s", channel.Name, clientID)

	restreamer.AddClient(clientID, w, flusher)

	if sp.Config.WatcherEnabled && restreamer.Restreamer.Running.Load() {
		preferredIndex := int(atomic.LoadInt32(&channel.PreferredStreamIndex))
		currentIndex := int(atomic.LoadInt32(&restreamer.Restreamer.CurrentIndex))

		actualIndex := preferredIndex
		if preferredIndex < 0 || preferredIndex >= len(channel.Streams) {
			actualIndex = currentIndex
		}

		logger.Debug("[WATCHER_DEBUG] Channel %s: Preferred=%d, Current=%d, Using=%d",
			channel.Name, preferredIndex, currentIndex, actualIndex)

		sp.WatcherManager.StartWatching(channel.Name, restreamer.Restreamer)
	}

	cleanup := func() {
		restreamer.RemoveClient(clientID)
	}
	defer cleanup()

	done := make(chan struct{})
	go func() {
		defer close(done)
		<-r.Context().Done()
	}()

	select {
	case <-done:
		logger.Debug("Restreaming client disconnected: %s", clientID)
	case <-time.After(24 * time.Hour):
		logger.Debug("Restreaming client timeout: %s", clientID)
	}
}

// ShouldCheckForMasterPlaylist analyzes HTTP response headers to determine whether
// the response body likely contains an HLS master playlist.
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

// GetChannelNameFromStream extracts the most appropriate display name for a channel.
func (sp *StreamProxy) GetChannelNameFromStream(stream *types.Stream) string {
	if name, ok := stream.Attributes["tvg-name"]; ok && name != "" {
		return name
	}
	return stream.Name
}

// getRateLimiterForSource retrieves the pre-initialized rate limiter for a given source.
func (sp *StreamProxy) getRateLimiterForSource(source *config.SourceConfig) ratelimit.Limiter {
	sp.rateLimiterMutex.RLock()
	limiter, exists := sp.SourceRateLimiters[source.URL]
	sp.rateLimiterMutex.RUnlock()

	if exists {
		return limiter
	}

	sp.rateLimiterMutex.Lock()
	defer sp.rateLimiterMutex.Unlock()

	if limiter, exists := sp.SourceRateLimiters[source.URL]; exists {
		return limiter
	}

	rateLimit := source.MaxConnections
	if rateLimit <= 0 {
		rateLimit = 5
	}

	limiter = ratelimit.New(rateLimit)
	sp.SourceRateLimiters[source.URL] = limiter

	logger.Debug("[RATE_LIMITER] Created rate limiter for dynamic source %s: %d req/sec",
		source.Name, rateLimit)

	return limiter
}

// getChannelSortValue extracts the sort value from a channel.
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
