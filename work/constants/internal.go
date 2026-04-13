package constants

import "time"

// InternalConstants defines all hardcoded operational values for the application.
// Modify values here rather than hunting through the codebase.
type InternalConstants struct {

	// -------------------------------------------------------------------------
	// work/restream/restream.go — Stream() / AddClient()
	// -------------------------------------------------------------------------
	StreamBufferSize         int           // Size of the per-read byte buffer used in streaming loops
	BufferWarmupDelay        time.Duration // Pre-warm delay before first client write after AddClient
	BriefSuccessThreshold    int64         // Minimum bytes for a connection to be considered non-trivially successful
	EOFSuccessThreshold      int64         // Minimum bytes transferred before EOF is treated as a clean end
	RetryDelay               time.Duration // Pause between retry attempts on transient errors
	BufferWriteRetryDelay    time.Duration // Pause between ring-buffer write retries on back-pressure
	MaxClientSessionDuration time.Duration // Hard cap on a single client streaming session
	StreamJitterMinMs        int64         // Minimum jitter (ms) added before stream retry to prevent thundering herd
	StreamJitterRangeMs      int64         // Random range (ms) added on top of the minimum jitter value

	// -------------------------------------------------------------------------
	// work/restream/restream.go — StreamFromSource() / stream loop
	// -------------------------------------------------------------------------
	StreamMaxAttemptsMultiplier       int   // Multiplier applied to stream count to derive max total attempts
	StreamConsecutiveFailureThreshold int   // Consecutive failures before a stream is marked for auto-blocking
	StreamMinViableBytes              int64 // Minimum bytes transferred for a stream attempt to be deemed viable

	// -------------------------------------------------------------------------
	// work/restream/restream.go — streamFromURL()
	// -------------------------------------------------------------------------
	StreamActivityUpdateInterval time.Duration // How often the LastActivity timestamp is refreshed while streaming
	StreamMetricUpdateInterval   time.Duration // How often Prometheus byte/connection metrics are updated
	StreamMaxConsecutiveErrors   int           // Max sequential read errors before streamFromURL aborts

	// -------------------------------------------------------------------------
	// work/restream/restream.go — getStreamVariants() / testAndStreamVariant()
	// -------------------------------------------------------------------------
	StreamVariantFetchTimeout time.Duration // HTTP timeout when probing a URL to determine variant type
	StreamVariantTestTimeout  time.Duration // HTTP timeout when test-reading the first bytes of a variant
	StreamTestBufferSize      int           // Byte count read during variant content-inspection test

	// -------------------------------------------------------------------------
	// work/restream/restream.go — streamFallbackVideo() / streamLocalFallback()
	// -------------------------------------------------------------------------
	OversizedBufferMultiplier int           // Multiplier used to detect and discard oversized buffers in the pool
	FallbackVideoPath         string        // Container-local path to the fallback .ts file streamed when all sources fail
	FallbackVideoLoopDelay    time.Duration // Pause between fallback video loop iterations

	// -------------------------------------------------------------------------
	// work/restream/restream.go — monitorClientHealth()
	// -------------------------------------------------------------------------
	ClientHealthCheckInterval time.Duration // Ticker interval for the per-channel client health goroutine
	ClientStaleTimeout        int64         // Seconds of inactivity before a client is removed as stale

	// -------------------------------------------------------------------------
	// work/restream/hls.go — streamHLSSegments()
	// -------------------------------------------------------------------------
	HLSSegmentTrackerSize      int           // Max segments tracked in the circular dedup buffer
	HLSMaxEmptyRefreshes       int           // Consecutive empty playlist refreshes before stall is declared
	HLSStallThreshold          time.Duration // Time with no new segments before the stream is considered stalled
	HLSPlaylistRefreshInterval time.Duration // Wait between successive HLS playlist polls
	HLSMaxSegmentErrors        int           // Max segment fetch errors per playlist refresh cycle before aborting

	// -------------------------------------------------------------------------
	// work/restream/hls.go — getHLSSegments()
	// -------------------------------------------------------------------------
	HLSPlaylistFetchTimeout time.Duration // HTTP context timeout when fetching an HLS playlist

	// -------------------------------------------------------------------------
	// work/restream/hls.go — streamSegment()
	// -------------------------------------------------------------------------
	HLSSegmentFetchTimeout           time.Duration // HTTP context timeout when fetching a single HLS segment
	HLSMaxConsecutiveSegmentErrors   int           // Max consecutive read errors within a single segment before giving up
	HLSSegmentActivityUpdateInterval time.Duration // How often LastActivity is refreshed while streaming a segment

	// -------------------------------------------------------------------------
	// work/restream/ffmpeg.go — streamWithFFmpeg()
	// -------------------------------------------------------------------------
	FFmpegActivityUpdateInterval time.Duration // How often LastActivity is refreshed in the FFmpeg streaming loop
	FFmpegMetricUpdateInterval   time.Duration // How often Prometheus metrics are updated in the FFmpeg loop
	FFmpegMaxConsecutiveErrors   int           // Max consecutive read/write errors before the FFmpeg loop aborts
	FFmpegLogProgressInterval    int64         // Byte interval at which FFmpeg streaming progress is logged

	// -------------------------------------------------------------------------
	// work/restream/ffmpeg.go — stopFFmpeg()
	// -------------------------------------------------------------------------
	FFmpegGracefulTermTimeout time.Duration // Time allowed for graceful SIGTERM before SIGKILL is sent

	// -------------------------------------------------------------------------
	// work/restream/stats.go — analyzeStreamStats()
	// -------------------------------------------------------------------------
	StatsBufferPeekSize   int64         // Bytes peeked from ring buffer for FFprobe analysis
	StatsFFprobeMaxData   int           // Max bytes written to FFprobe stdin
	StatsFFprobeTimeout   time.Duration // Context timeout for each FFprobe invocation
	FFprobeSemaphoreLimit int           // Max concurrent FFprobe processes across all channels

	// -------------------------------------------------------------------------
	// work/restream/stats.go — collectStreamStats()
	// -------------------------------------------------------------------------
	StatsCollectionInterval time.Duration // Ticker interval for periodic stats collection in production
	StatsDebugInterval      time.Duration // Ticker interval for periodic stats collection in debug mode
	StatsJitterMaxSeconds   int           // Max random jitter (seconds) added before the first periodic stats collection

	// -------------------------------------------------------------------------
	// work/proxy/stream.go — ImportStreams()
	// -------------------------------------------------------------------------
	ImportGlobalTimeout time.Duration // Global timeout for the full ImportStreams operation

	// -------------------------------------------------------------------------
	// work/proxy/stream.go — New() / initializeRateLimiters()
	// -------------------------------------------------------------------------
	SourceDefaultRateLimit int // Default requests-per-second rate limit when a source has no MaxConnections set

	// -------------------------------------------------------------------------
	// work/proxy/stream.go — HandleRestreamingClient()
	// -------------------------------------------------------------------------
	MasterPlaylistSizeThreshold int64 // Content-Length threshold (bytes) below which a response may be a master playlist

	// -------------------------------------------------------------------------
	// work/proxy/stream.go — RestreamCleanup()
	// -------------------------------------------------------------------------
	ProxyCleanupTickerInterval     time.Duration // Ticker interval for the proxy-level restream cleanup loop
	ProxyInactiveRestreamerTimeout int64         // Idle seconds before a stopped restreamer is cleaned up (proxy copy)
	ProxyForceCleanTimeout         int64         // Idle seconds before force-cleaning a cancelled restreamer (proxy copy)
	ProxyClientInactivityTimeout   int64         // Client idle seconds before removal (proxy copy)

	// -------------------------------------------------------------------------
	// work/proxy/epg.go — StartEPGWarmup() / startEPGRefresh()
	// -------------------------------------------------------------------------
	EPGRefreshInterval time.Duration // Interval between scheduled background EPG cache refreshes

	// -------------------------------------------------------------------------
	// work/proxy/epg.go — FetchEPGData()
	// -------------------------------------------------------------------------
	EPGMaxRetries     int           // Max fetch attempts per EPG source before giving up
	EPGRetryBaseDelay time.Duration // Base delay multiplied by attempt number between EPG retries

	// -------------------------------------------------------------------------
	// work/stream/stream.go — HandleStreamFailure()
	// -------------------------------------------------------------------------
	StreamDefaultMaxFailures int32 // Default consecutive-failure limit before a stream is auto-blocked

	// -------------------------------------------------------------------------
	// work/watcher/watcher.go — NewStreamWatcher()
	// -------------------------------------------------------------------------
	WatcherMaxConcurrent int // System-wide cap on concurrent stream watchers via semaphore

	// -------------------------------------------------------------------------
	// work/watcher/watcher.go — cleanupRoutine()
	// -------------------------------------------------------------------------
	WatcherCleanupInterval time.Duration // Ticker interval for the watcher manager cleanup goroutine

	// -------------------------------------------------------------------------
	// work/watcher/watcher.go — Watch()
	// -------------------------------------------------------------------------
	WatcherCheckInterval      time.Duration // Health check interval in production mode
	WatcherDebugCheckInterval time.Duration // Health check interval in debug mode
	WatcherPollingInterval    time.Duration // Polling interval while waiting for the restreamer to start

	// -------------------------------------------------------------------------
	// work/watcher/watcher.go — evaluateStreamHealthFromState()
	// -------------------------------------------------------------------------
	WatcherGracePeriod         time.Duration // Grace period before health checks begin on a new stream
	WatcherContextStuckTimeout time.Duration // Time a cancelled context must persist before being flagged as stuck
	WatcherActivityTimeout     int64         // Seconds of no activity before the stream is flagged as stalled
	WatcherStatsStaleTimeout   int64         // Seconds since last stats update before stats are considered stale
	WatcherLowThroughputBytes  int64         // Minimum bytes between health checks; below this is treated as a stall

	// -------------------------------------------------------------------------
	// work/watcher/watcher.go — checkStreamHealth()
	// -------------------------------------------------------------------------
	WatcherFailureThreshold   int32         // Total failures within the window that trigger a stream switch
	WatcherFailureResetWindow time.Duration // Window after which the long-term failure counter is reset

	// -------------------------------------------------------------------------
	// work/watcher/watcher.go — forceStreamRestart()
	// -------------------------------------------------------------------------
	WatcherRestartDeadline     time.Duration // Max wait for a running restreamer to stop before proceeding with restart
	WatcherRestartPollInterval time.Duration // Polling interval when waiting for the restreamer to stop before restart

	// -------------------------------------------------------------------------
	// work/cache/cache.go — NewCache()
	// -------------------------------------------------------------------------
	CacheMaxSize int // Maximum number of entries in the otter in-memory cache

	// -------------------------------------------------------------------------
	// work/cache/cache.go — newEPGStore()
	// -------------------------------------------------------------------------
	EPGDiskTTL time.Duration // TTL for EPG data written to the disk cache

	// -------------------------------------------------------------------------
	// work/users/session.go — CreateSession()
	// -------------------------------------------------------------------------
	SessionTTL         time.Duration // Standard session lifetime
	SessionTTLExtended time.Duration // Extended session lifetime when "remember me" is checked

	// -------------------------------------------------------------------------
	// work/users/session.go — cleanup()
	// -------------------------------------------------------------------------
	SessionCleanupTick time.Duration // Ticker interval for purging expired sessions

	// -------------------------------------------------------------------------
	// work/users/handlers.go — HandleRegister()
	// -------------------------------------------------------------------------
	RegistrationMaxAttempts int           // Max registration attempts allowed within the rate-limit window
	RegistrationWindow      time.Duration // Rolling window duration for registration attempt rate limiting
	PasswordMinLength       int           // Minimum acceptable password length enforced at registration and change

	// -------------------------------------------------------------------------
	// work/users/argon2.go — HashPassword()
	// -------------------------------------------------------------------------
	Argon2Memory      uint32 // Argon2id memory parameter (KB)
	Argon2Iterations  uint32 // Argon2id time/iteration parameter
	Argon2Parallelism uint8  // Argon2id parallelism parameter
	Argon2SaltLength  int    // Salt length in bytes
	Argon2KeyLength   uint32 // Output key/hash length in bytes

	// -------------------------------------------------------------------------
	// work/schedulesdirect/auth.go — GetToken()
	// -------------------------------------------------------------------------
	SDTokenValidDuration time.Duration // How long a cached SD token is considered valid
	SDRefreshThreshold   time.Duration // Minimum time between token refresh attempts
	SDLoginTimeout       time.Duration // HTTP timeout for the SD authentication request
	SDBaseUrl            string        // Base URL for Schedules Direct API requests

	// -------------------------------------------------------------------------
	// work/schedulesdirect/fetch.go — FetchAccount()
	// -------------------------------------------------------------------------
	SDDefaultDaysToFetch int // Default schedule lookahead window when an account has no DaysToFetch configured
	SDBatchSize          int // Max program/schedule IDs per SD API batch request

	// -------------------------------------------------------------------------
	// work/admin/logs.go
	// -------------------------------------------------------------------------
	AdminMaxLogEntries int // Maximum entries retained in the admin log circular buffer

	// -------------------------------------------------------------------------
	// work/admin/system.go — handleRestart()
	// -------------------------------------------------------------------------
	AdminRestartDelay time.Duration // Delay before signalling restart to allow HTTP response to flush

	// -------------------------------------------------------------------------
	// main.go
	// -------------------------------------------------------------------------
	ServerPort int // TCP port the HTTP server listens on

	// -------------------------------------------------------------------------
	// work/db/db.go — Get()
	// -------------------------------------------------------------------------
	DatabasePath string // Filesystem path to the SQLite database file
}

// Internal holds all hardcoded operational values for the application.
var Internal = InternalConstants{

	// -------------------------------------------------------------------------
	// Stream core
	// -------------------------------------------------------------------------
	StreamBufferSize:         32 * 1024,
	BufferWarmupDelay:        500 * time.Millisecond,
	BriefSuccessThreshold:    64 * 1024,
	EOFSuccessThreshold:      2 * 1024 * 1024,
	RetryDelay:               100 * time.Millisecond,
	BufferWriteRetryDelay:    10 * time.Millisecond,
	MaxClientSessionDuration: 24 * time.Hour,
	StreamJitterMinMs:        50,
	StreamJitterRangeMs:      450,

	// -------------------------------------------------------------------------
	// Stream loop / source selection
	// -------------------------------------------------------------------------
	StreamMaxAttemptsMultiplier:       2,
	StreamConsecutiveFailureThreshold: 2,
	StreamMinViableBytes:              1024 * 1024,

	// -------------------------------------------------------------------------
	// streamFromURL
	// -------------------------------------------------------------------------
	StreamActivityUpdateInterval: 1 * time.Second,
	StreamMetricUpdateInterval:   10 * time.Second,
	StreamMaxConsecutiveErrors:   5,

	// -------------------------------------------------------------------------
	// Variant detection
	// -------------------------------------------------------------------------
	StreamVariantFetchTimeout: 15 * time.Second,
	StreamVariantTestTimeout:  10 * time.Second,
	StreamTestBufferSize:      512,

	// -------------------------------------------------------------------------
	// Fallback video
	// -------------------------------------------------------------------------
	OversizedBufferMultiplier: 4,
	FallbackVideoPath:         "/static/loading.ts",
	FallbackVideoLoopDelay:    1 * time.Second,

	// -------------------------------------------------------------------------
	// Client health
	// -------------------------------------------------------------------------
	ClientHealthCheckInterval: 10 * time.Second,
	ClientStaleTimeout:        120,

	// -------------------------------------------------------------------------
	// HLS
	// -------------------------------------------------------------------------
	HLSSegmentTrackerSize:            20,
	HLSMaxEmptyRefreshes:             10,
	HLSStallThreshold:                30 * time.Second,
	HLSPlaylistRefreshInterval:       2 * time.Second,
	HLSPlaylistFetchTimeout:          10 * time.Second,
	HLSSegmentFetchTimeout:           30 * time.Second,
	HLSMaxSegmentErrors:              5,
	HLSMaxConsecutiveSegmentErrors:   5,
	HLSSegmentActivityUpdateInterval: 5 * time.Second,

	// -------------------------------------------------------------------------
	// FFmpeg
	// -------------------------------------------------------------------------
	FFmpegGracefulTermTimeout:    3 * time.Second,
	FFmpegActivityUpdateInterval: 5 * time.Second,
	FFmpegMetricUpdateInterval:   10 * time.Second,
	FFmpegMaxConsecutiveErrors:   10,
	FFmpegLogProgressInterval:    20 * 1024 * 1024,

	// -------------------------------------------------------------------------
	// Stats
	// -------------------------------------------------------------------------
	StatsBufferPeekSize:     3 * 1024 * 1024,
	StatsFFprobeMaxData:     2 * 1024 * 1024,
	StatsFFprobeTimeout:     15 * time.Second,
	FFprobeSemaphoreLimit:   4,
	StatsCollectionInterval: 5 * time.Minute,
	StatsDebugInterval:      1 * time.Minute,
	StatsJitterMaxSeconds:   30,

	// -------------------------------------------------------------------------
	// Proxy
	// -------------------------------------------------------------------------
	ImportGlobalTimeout:            2 * time.Minute,
	SourceDefaultRateLimit:         5,
	MasterPlaylistSizeThreshold:    100 * 1024,
	ProxyCleanupTickerInterval:     10 * time.Second,
	ProxyInactiveRestreamerTimeout: 30,
	ProxyForceCleanTimeout:         60,
	ProxyClientInactivityTimeout:   120,

	// -------------------------------------------------------------------------
	// EPG
	// -------------------------------------------------------------------------
	EPGRefreshInterval: 12 * time.Hour,
	EPGMaxRetries:      5,
	EPGRetryBaseDelay:  10 * time.Second,
	EPGDiskTTL:         12 * time.Hour,

	// -------------------------------------------------------------------------
	// Stream failure
	// -------------------------------------------------------------------------
	StreamDefaultMaxFailures: 5,

	// -------------------------------------------------------------------------
	// Watcher
	// -------------------------------------------------------------------------
	WatcherMaxConcurrent:       100,
	WatcherCleanupInterval:     30 * time.Second,
	WatcherCheckInterval:       30 * time.Second,
	WatcherDebugCheckInterval:  15 * time.Second,
	WatcherPollingInterval:     500 * time.Millisecond,
	WatcherGracePeriod:         30 * time.Second,
	WatcherContextStuckTimeout: 300 * time.Second,
	WatcherActivityTimeout:     120,
	WatcherStatsStaleTimeout:   600,
	WatcherLowThroughputBytes:  50 * 1024,
	WatcherFailureThreshold:    3,
	WatcherFailureResetWindow:  15 * time.Minute,
	WatcherRestartDeadline:     3 * time.Second,
	WatcherRestartPollInterval: 50 * time.Millisecond,

	// -------------------------------------------------------------------------
	// Cache
	// -------------------------------------------------------------------------
	CacheMaxSize: 10_000,

	// -------------------------------------------------------------------------
	// Users / auth
	// -------------------------------------------------------------------------
	SessionTTL:              24 * time.Hour,
	SessionTTLExtended:      30 * 24 * time.Hour,
	SessionCleanupTick:      15 * time.Minute,
	RegistrationMaxAttempts: 5,
	RegistrationWindow:      1 * time.Hour,
	PasswordMinLength:       12,
	Argon2Memory:            64 * 1024,
	Argon2Iterations:        3,
	Argon2Parallelism:       2,
	Argon2SaltLength:        16,
	Argon2KeyLength:         32,

	// -------------------------------------------------------------------------
	// Schedules Direct
	// -------------------------------------------------------------------------
	SDTokenValidDuration: 24 * time.Hour,
	SDRefreshThreshold:   12 * time.Hour,
	SDLoginTimeout:       15 * time.Second,
	SDDefaultDaysToFetch: 7,
	SDBatchSize:          1000,
	SDBaseUrl:            "https://json.schedulesdirect.org/20141201",

	// -------------------------------------------------------------------------
	// Admin
	// -------------------------------------------------------------------------
	AdminMaxLogEntries: 1000,
	AdminRestartDelay:  500 * time.Millisecond,

	// -------------------------------------------------------------------------
	// Server / database
	// -------------------------------------------------------------------------
	ServerPort:   8080,
	DatabasePath: "/settings/kptv.db",
}
