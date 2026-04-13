package constants

import "time"

// InternalConstants defines the structure for all hardcoded operational values.
type InternalConstants struct {

	// work/restream/restream.go - Stream()
	StreamBufferSize         int
	BufferWarmupDelay        time.Duration
	BriefSuccessThreshold    int64
	EOFSuccessThreshold      int64
	RetryDelay               time.Duration
	BufferWriteRetryDelay    time.Duration
	MaxClientSessionDuration time.Duration

	// work/restream/restream.go - RestreamCleanup()
	CleanupTickerInterval     time.Duration
	InactiveRestreamerTimeout time.Duration
	ForceCleanTimeout         time.Duration
	ClientInactivityTimeout   time.Duration

	// work/restream/restream.go - streamFallbackVideo()
	OversizedBufferMultiplier int

	// work/restream/stats.go - analyzeStreamStats()
	StatsBufferPeekSize   int64
	StatsFFprobeMaxData   int
	StatsFFprobeTimeout   time.Duration
	FFprobeSemaphoreLimit int

	// work/restream/stats.go - collectStreamStats()
	StatsCollectionInterval time.Duration
	StatsDebugInterval      time.Duration

	// work/restream/hls.go - streamHLS()
	HLSSegmentTrackerSize      int
	HLSMaxEmptyRefreshes       int
	HLSStallThreshold          time.Duration
	HLSPlaylistRefreshInterval time.Duration
	HLSPlaylistFetchTimeout    time.Duration
	HLSSegmentFetchTimeout     time.Duration

	// work/restream/ffmpeg.go - stopFFmpeg()
	FFmpegGracefulTermTimeout time.Duration

	// work/watcher/watcher.go - NewStreamWatcher()
	WatcherMaxConcurrent int

	// work/watcher/watcher.go - startCleanupRoutine()
	WatcherCleanupInterval time.Duration

	// work/watcher/watcher.go - watchStream()
	WatcherCheckInterval       time.Duration
	WatcherDebugCheckInterval  time.Duration
	WatcherGracePeriod         time.Duration
	WatcherRestartDeadline     time.Duration
	WatcherContextStuckTimeout time.Duration

	// work/watcher/watcher.go - evaluateStreamHealthFromState()
	WatcherThroughputThreshold int64
	WatcherActivityTimeout     int64
	WatcherStatsStaleTimeout   int64

	// work/watcher/watcher.go - recordFailure()
	WatcherFailureThreshold   int32
	WatcherFailureResetWindow time.Duration

	// work/proxy/stream.go - ImportStreams()
	ImportGlobalTimeout time.Duration

	// work/proxy/stream.go - RestreamCleanup()
	ProxyCleanupTickerInterval     time.Duration
	ProxyInactiveRestreamerTimeout time.Duration
	ProxyForceCleanTimeout         time.Duration
	ProxyClientInactivityTimeout   time.Duration

	// work/proxy/epg.go - startEPGRefresh()
	EPGRefreshInterval time.Duration

	// work/proxy/epg.go - fetchAndStoreEPG()
	EPGMaxRetries     int
	EPGRetryBaseDelay time.Duration

	// work/cache/cache.go - NewCache()
	CacheMaxSize int

	// work/cache/cache.go - storeEPGToDisk() / loadEPGFromDisk()
	EPGDiskTTL time.Duration

	// work/users/session.go - CreateSession()
	SessionTTL         time.Duration
	SessionTTLExtended time.Duration

	// work/users/session.go - startCleanupRoutine()
	SessionCleanupTick time.Duration

	// work/users/handlers.go - HandleRegister()
	RegistrationMaxAttempts int
	RegistrationWindow      time.Duration

	// work/users/argon2.go - HashPassword()
	Argon2Memory      uint32
	Argon2Iterations  uint32
	Argon2Parallelism uint8
	Argon2SaltLength  int
	Argon2KeyLength   uint32

	// work/schedulesdirect/auth.go - IsTokenValid()
	SDTokenValidDuration time.Duration
	SDRefreshThreshold   time.Duration

	// work/schedulesdirect/auth.go - Login()
	SDLoginTimeout time.Duration

	// work/schedulesdirect/fetch.go - FetchLineup()
	SDBatchSize int

	// work/admin/logs.go - GetLogs()
	AdminMaxLogEntries int

	// work/admin/system.go - HandleRestart()
	AdminRestartDelay time.Duration

	// main.go
	ServerPort int

	// work/db/db.go - initDB()
	DatabasePath string

	// work/restream/ffmpeg.go - streamWithFFmpeg()
	FFmpegActivityUpdateInterval time.Duration
	FFmpegMetricUpdateInterval   time.Duration
	FFmpegMaxConsecutiveErrors   int
	FFmpegLogProgressInterval    int64

	// work/restream/hls.go - streamHLSSegments()
	HLSMaxSegmentErrors int

	// work/restream/hls.go - streamSegment()
	HLSMaxConsecutiveSegmentErrors int

	// work/restream/restream.go - testAndStreamVariant()
	StreamTestBufferSize int

	// work/restream/restream.go - streamFromURL()
	StreamActivityUpdateInterval time.Duration
	StreamMetricUpdateInterval   time.Duration
	StreamMaxConsecutiveErrors   int

	// work/restream/restream.go - Stream()
	StreamJitterMin time.Duration
	StreamJitterMax time.Duration

	// work/restream/restream.go - monitorClientHealth()
	ClientHealthCheckInterval time.Duration
	ClientStaleTimeout        int64

	// work/restream/restream.go - streamFallbackVideo()
	FallbackVideoPath      string
	FallbackVideoLoopDelay time.Duration

	// work/restream/stats.go - collectStreamStats()
	StatsJitterMaxSeconds int

	// work/watcher/watcher.go - Watch()
	WatcherPollingInterval time.Duration

	// work/watcher/watcher.go - forceStreamRestart()
	WatcherRestartPollInterval time.Duration

	// work/watcher/watcher.go - evaluateStreamHealthFromState()
	WatcherLowThroughputBytes int64
}

// Internal holds all hardcoded operational values for the application.
// Modify values here rather than hunting through the codebase.
var Internal = InternalConstants{

	// work/restream/restream.go - Stream()
	StreamBufferSize:         32 * 1024,
	BufferWarmupDelay:        500 * time.Millisecond,
	BriefSuccessThreshold:    64 * 1024,
	EOFSuccessThreshold:      2 * 1024 * 1024,
	RetryDelay:               100 * time.Millisecond,
	BufferWriteRetryDelay:    10 * time.Millisecond,
	MaxClientSessionDuration: 24 * time.Hour,

	// work/restream/restream.go - RestreamCleanup()
	CleanupTickerInterval:     10 * time.Second,
	InactiveRestreamerTimeout: 30 * time.Second,
	ForceCleanTimeout:         60 * time.Second,
	ClientInactivityTimeout:   120 * time.Second,

	// work/restream/restream.go - streamFallbackVideo()
	OversizedBufferMultiplier: 4,

	// work/restream/stats.go - analyzeStreamStats()
	StatsBufferPeekSize:   3 * 1024 * 1024,
	StatsFFprobeMaxData:   2 * 1024 * 1024,
	StatsFFprobeTimeout:   15 * time.Second,
	FFprobeSemaphoreLimit: 4,

	// work/restream/stats.go - collectStreamStats()
	StatsCollectionInterval: 5 * time.Minute,
	StatsDebugInterval:      1 * time.Minute,

	// work/restream/hls.go - streamHLS()
	HLSSegmentTrackerSize:      20,
	HLSMaxEmptyRefreshes:       10,
	HLSStallThreshold:          30 * time.Second,
	HLSPlaylistRefreshInterval: 2 * time.Second,
	HLSPlaylistFetchTimeout:    10 * time.Second,
	HLSSegmentFetchTimeout:     30 * time.Second,

	// work/restream/ffmpeg.go - stopFFmpeg()
	FFmpegGracefulTermTimeout: 3 * time.Second,

	// work/watcher/watcher.go - NewStreamWatcher()
	WatcherMaxConcurrent: 100,

	// work/watcher/watcher.go - startCleanupRoutine()
	WatcherCleanupInterval: 30 * time.Second,

	// work/watcher/watcher.go - watchStream()
	WatcherCheckInterval:       30 * time.Second,
	WatcherDebugCheckInterval:  15 * time.Second,
	WatcherGracePeriod:         30 * time.Second,
	WatcherRestartDeadline:     3 * time.Second,
	WatcherContextStuckTimeout: 300 * time.Second,

	// work/watcher/watcher.go - evaluateStreamHealthFromState()
	WatcherThroughputThreshold: 50 * 1024,
	WatcherActivityTimeout:     120,
	WatcherStatsStaleTimeout:   600,

	// work/watcher/watcher.go - recordFailure()
	WatcherFailureThreshold:   3,
	WatcherFailureResetWindow: 15 * time.Minute,

	// work/proxy/stream.go - ImportStreams()
	ImportGlobalTimeout: 2 * time.Minute,

	// work/proxy/stream.go - RestreamCleanup()
	ProxyCleanupTickerInterval:     10 * time.Second,
	ProxyInactiveRestreamerTimeout: 30 * time.Second,
	ProxyForceCleanTimeout:         60 * time.Second,
	ProxyClientInactivityTimeout:   120 * time.Second,

	// work/proxy/epg.go - startEPGRefresh()
	EPGRefreshInterval: 12 * time.Hour,

	// work/proxy/epg.go - fetchAndStoreEPG()
	EPGMaxRetries:     5,
	EPGRetryBaseDelay: 10 * time.Second,

	// work/cache/cache.go - NewCache()
	CacheMaxSize: 10_000,

	// work/cache/cache.go - storeEPGToDisk() / loadEPGFromDisk()
	EPGDiskTTL: 12 * time.Hour,

	// work/users/session.go - CreateSession()
	SessionTTL:         24 * time.Hour,
	SessionTTLExtended: 30 * 24 * time.Hour,

	// work/users/session.go - startCleanupRoutine()
	SessionCleanupTick: 15 * time.Minute,

	// work/users/handlers.go - HandleRegister()
	RegistrationMaxAttempts: 5,
	RegistrationWindow:      1 * time.Hour,

	// work/users/argon2.go - HashPassword()
	Argon2Memory:      64 * 1024,
	Argon2Iterations:  3,
	Argon2Parallelism: 2,
	Argon2SaltLength:  16,
	Argon2KeyLength:   32,

	// work/schedulesdirect/auth.go - IsTokenValid()
	SDTokenValidDuration: 24 * time.Hour,
	SDRefreshThreshold:   12 * time.Hour,

	// work/schedulesdirect/auth.go - Login()
	SDLoginTimeout: 15 * time.Second,

	// work/schedulesdirect/fetch.go - FetchLineup()
	SDBatchSize: 1000,

	// work/admin/logs.go - GetLogs()
	AdminMaxLogEntries: 1000,

	// work/admin/system.go - HandleRestart()
	AdminRestartDelay: 500 * time.Millisecond,

	// main.go
	ServerPort: 8080,

	// work/db/db.go - initDB()
	DatabasePath: "/settings/kptv.db",

	// work/restream/ffmpeg.go - streamWithFFmpeg()
	FFmpegActivityUpdateInterval: 5 * time.Second,
	FFmpegMetricUpdateInterval:   10 * time.Second,
	FFmpegMaxConsecutiveErrors:   10,
	FFmpegLogProgressInterval:    20 * 1024 * 1024,

	// work/restream/hls.go - streamHLSSegments()
	HLSMaxSegmentErrors: 5,

	// work/restream/hls.go - streamSegment()
	HLSMaxConsecutiveSegmentErrors: 5,

	// work/restream/restream.go - testAndStreamVariant()
	StreamTestBufferSize: 512,

	// work/restream/restream.go - streamFromURL()
	StreamActivityUpdateInterval: 1 * time.Second,
	StreamMetricUpdateInterval:   10 * time.Second,
	StreamMaxConsecutiveErrors:   5,

	// work/restream/restream.go - Stream()
	StreamJitterMin: 50 * time.Millisecond,
	StreamJitterMax: 500 * time.Millisecond,

	// work/restream/restream.go - monitorClientHealth()
	ClientHealthCheckInterval: 10 * time.Second,
	ClientStaleTimeout:        120,

	// work/restream/restream.go - streamFallbackVideo()
	FallbackVideoPath:      "/static/loading.ts",
	FallbackVideoLoopDelay: 1 * time.Second,

	// work/restream/stats.go - collectStreamStats()
	StatsJitterMaxSeconds: 30,

	// work/watcher/watcher.go - Watch()
	WatcherPollingInterval: 500 * time.Millisecond,

	// work/watcher/watcher.go - forceStreamRestart()
	WatcherRestartPollInterval: 50 * time.Millisecond,

	// work/watcher/watcher.go - evaluateStreamHealthFromState()
	WatcherLowThroughputBytes: 200 * 1024,
}
