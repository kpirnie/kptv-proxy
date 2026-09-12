package proxy

import (
	"kptv-proxy/work/buffer"
	"kptv-proxy/work/cache"
	"kptv-proxy/work/client"
	"kptv-proxy/work/config"
	"kptv-proxy/work/constants"
	"kptv-proxy/work/filter"
	"kptv-proxy/work/logger"
	"kptv-proxy/work/parser"
	"kptv-proxy/work/types"
	"kptv-proxy/work/utils"
	"kptv-proxy/work/watcher"

	"net/url"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/panjf2000/ants/v2"
	"github.com/puzpuzpuz/xsync/v3"
	"go.uber.org/ratelimit"
	"golang.org/x/sync/singleflight"
)

// setup the proxy-wide client semaphore for limiting concurrent outbound requests
var (
	globalClientSemaphore chan struct{}
	semaphoreOnce         sync.Once
)

// externalChannelSources holds channel collections owned outside the proxy that
// still need restreamer maintenance. Episode channels are not part of the
// imported channel map, so without this their restreamers and buffers would
// never be reclaimed.
var (
	externalChannelSources   []func(func(*types.Channel) bool)
	externalChannelSourcesMu sync.RWMutex
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
	ImportClient          *client.HeaderSettingClient          // separate client for source imports so a slow import cannot starve streaming or EPG fetches
	WorkerPool            *ants.Pool                           // bounded goroutine pool for controlled concurrency
	MasterPlaylistHandler *parser.MasterPlaylistHandler        // HLS master playlist detection and resolution handler
	importStopChan        chan bool                            // signal channel to gracefully terminate the import refresh loop
	WatcherManager        *watcher.WatcherManager              // manages stream quality watchers for active restreaming sessions
	SourceRateLimiters    map[string]ratelimit.Limiter         // per-source rate limiters keyed by source URL
	rateLimiterMutex      sync.RWMutex                         // protects concurrent access to the rate limiter map
	FilterManager         *filter.FilterManager                // handles stream filtering rules from configuration
	importGeneration      atomic.Uint64                        // bumped on each committed import so cached playlists are not reused across imports
	groupIndex            atomic.Pointer[map[string]struct{}]  // lowercased set of known group titles, rebuilt on each committed import
	nameIndex             atomic.Pointer[map[string]string]    // sanitized channel name -> real channel name, rebuilt on each committed import
	playlistBuilds        singleflight.Group                   // collapses concurrent misses on the same playlist cache key into one build
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
		ImportClient:          client.NewHeaderSettingClient(cfg.ResponseHeaderTimeout),
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
			rateLimit = constants.Internal.SourceDefaultRateLimit
			logger.Debug("{proxy/stream - initializeRateLimiters} No max connections set for %s, defaulting to %d req/sec", source.Name, rateLimit)
		}
		limiter := ratelimit.New(rateLimit)
		sp.SourceRateLimiters[source.URL] = limiter

		logger.Debug("{proxy/stream - initializeRateLimiters} Created rate limiter for source %s: %d req/sec",
			source.Name, rateLimit)
	}

	logger.Debug("{proxy/stream - initializeRateLimiters} All rate limiters initialized")
}

// ReinitRateLimiters rebuilds all per-source rate limiters from the current
// config, used after a graceful restart when the source list may have changed
func (sp *StreamProxy) ReinitRateLimiters() {
	sp.rateLimiterMutex.Lock()
	sp.SourceRateLimiters = make(map[string]ratelimit.Limiter)
	sp.rateLimiterMutex.Unlock()
	sp.initializeRateLimiters()
}

// RateLimiterForSource exposes a source's rate limiter to callers outside the
// proxy package, so out-of-band API requests obey the same per-source pacing
// that imports and restreaming do.
func (sp *StreamProxy) RateLimiterForSource(source *config.SourceConfig) ratelimit.Limiter {
	return sp.getRateLimiterForSource(source)
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
		rateLimit = constants.Internal.SourceDefaultRateLimit
	}

	limiter = ratelimit.New(rateLimit)
	sp.SourceRateLimiters[source.URL] = limiter

	logger.Debug("{proxy/stream - getRateLimiterForSource} Created rate limiter for dynamic source %s: %d req/sec",
		source.Name, rateLimit)

	return limiter
}

// AcquireClientSlot takes a slot in the app-wide connection ceiling for a
// delivery that does not go through the restreamer, returning a release func and
// whether a slot was available. Passthrough responses hold a connection for as
// long as a restreamed one does and must count against the same limit.
func (sp *StreamProxy) AcquireClientSlot() (func(), bool) {
	select {
	case globalClientSemaphore <- struct{}{}:
		return func() { <-globalClientSemaphore }, true
	default:
		logger.Debug("{proxy/stream - AcquireClientSlot} Max connections reached (%d), rejecting client", sp.Config.MaxConnectionsToApp)
		return func() {}, false
	}
}

// ChannelCount returns the current number of channels in the channel map.
func (sp *StreamProxy) ChannelCount() int {
	count := 0
	sp.Channels.Range(func(_ string, _ *types.Channel) bool {
		count++
		return true
	})
	return count
}

// RegisterChannelSource registers a channel collection for restreamer cleanup.
// The supplied function must call the visitor for every channel it holds, and
// is responsible for its own locking and for discarding entries it no longer
// needs once their restreamer has been torn down.
func RegisterChannelSource(source func(func(*types.Channel) bool)) {
	externalChannelSourcesMu.Lock()
	defer externalChannelSourcesMu.Unlock()
	externalChannelSources = append(externalChannelSources, source)
}

// rangeExternalChannels walks every registered external channel collection.
func rangeExternalChannels(visit func(*types.Channel) bool) {
	externalChannelSourcesMu.RLock()
	sources := make([]func(func(*types.Channel) bool), len(externalChannelSources))
	copy(sources, externalChannelSources)
	externalChannelSourcesMu.RUnlock()

	for _, source := range sources {
		source(visit)
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

	// fall back to the sanitized-name index built at import
	var foundName string
	if m := sp.nameIndex.Load(); m != nil {
		foundName = (*m)[safeName]
	}

	// make sure it's not empty
	if foundName != "" {
		logger.Debug("{proxy/stream - FindChannelBySafeName} Resolved channel by sanitized name scan: %s -> %s", safeName, foundName)
		return foundName
	}

	logger.Debug("{proxy/stream - FindChannelBySafeName} No exact match found, using input as-is: %s", safeName)
	return safeName
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

// GetChannelGroup extracts the group classification from channel attributes by checking
// for the standard "tvg-group" attribute first, then falling back to "group-title".
// Uncategorized channels default to All so they always land in a real category.
func (sp *StreamProxy) GetChannelGroup(attrs map[string]string) string {
	if group, exists := attrs["tvg-group"]; exists && group != "" {
		return group
	}
	if group, exists := attrs["group-title"]; exists && group != "" {
		return group
	}
	return "All"
}

// rebuildGroupIndex snapshots the group titles present in the current channel
// catalog. Built once per import so playlist requests can reject unknown groups
// without walking the channel map.
func (sp *StreamProxy) rebuildGroupIndex() {
	groups := make(map[string]struct{})

	sp.Channels.Range(func(_ string, channel *types.Channel) bool {
		channel.Mu.RLock()
		if len(channel.Streams) > 0 {
			groups[strings.ToLower(sp.GetChannelGroup(channel.Streams[0].Attributes))] = struct{}{}
		}
		channel.Mu.RUnlock()
		return true
	})

	sp.groupIndex.Store(&groups)
	logger.Debug("{proxy/stream - rebuildGroupIndex} Indexed %d groups", len(groups))
}

// rebuildNameIndex maps every sanitized channel name back to its real name so
// stream requests resolve with a map lookup instead of a full catalog walk.
func (sp *StreamProxy) rebuildNameIndex() {
	names := make(map[string]string)

	sp.Channels.Range(func(name string, _ *types.Channel) bool {
		names[utils.SanitizeChannelName(name)] = name
		return true
	})

	sp.nameIndex.Store(&names)
	logger.Debug("{proxy/stream - rebuildNameIndex} Indexed %d channel names", len(names))
}

// ImportGeneration returns the current import generation, bumped on every
// committed import. Callers use it to invalidate their own derived indexes.
func (sp *StreamProxy) ImportGeneration() uint64 {
	return sp.importGeneration.Load()
}

// IsKnownGroup reports whether the supplied group title exists in the current
// catalog. The {group} path segment is client-controlled and forms part of the
// playlist cache key, so an unknown value must never reach a render or a Set.
func (sp *StreamProxy) IsKnownGroup(group string) bool {
	m := sp.groupIndex.Load()
	if m == nil {
		return false
	}
	_, ok := (*m)[strings.ToLower(group)]
	return ok
}
