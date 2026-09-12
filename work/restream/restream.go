package restream

import (
	"context"
	bbuffer "kptv-proxy/work/buffer"
	"kptv-proxy/work/client"
	"kptv-proxy/work/config"
	"kptv-proxy/work/constants"
	"kptv-proxy/work/logger"
	"kptv-proxy/work/types"
	"sync"
	"time"

	"github.com/puzpuzpuz/xsync/v3"
	"go.uber.org/ratelimit"
)

// fallback video cache variables
// these will be used to cache the local fallback video when it's available
// and necessary to do so
var (
	fallbackVideoCache     []byte
	fallbackVideoCacheMu   sync.RWMutex
	fallbackVideoCachePath string
)

// streamBufferPool provides a sync.Pool for reusing 32KB buffers during stream
// processing operations. This reduces memory allocations and GC pressure by
// recycling buffers across multiple stream reads instead of allocating new
// buffers for each read operation.
var streamBufferPool = sync.Pool{
	New: func() any {
		buf := make([]byte, constants.Internal.StreamBufferSize)
		return &buf
	},
}

// streamOutcome classifies the result of a single upstream attempt.
type streamOutcome int

const (
	outcomeStopped streamOutcome = iota
	outcomeSwitch
	outcomeSuccess
	outcomeFailure
)

// getStreamBuffer retrieves a 32KB buffer from the pool for stream processing.
// The buffer should be returned to the pool via putStreamBuffer when no longer needed.
//
// Returns:
//   - *[]byte: pointer to a 32KB byte slice ready for use
func getStreamBuffer() *[]byte {
	return streamBufferPool.Get().(*[]byte)
}

// putStreamBuffer returns a buffer to the pool for reuse. The buffer contents
// are not cleared, so callers should not assume zero-initialized buffers when
// retrieving from the pool.
//
// Parameters:
//   - buf: pointer to buffer to return to the pool
func putStreamBuffer(buf *[]byte) {
	streamBufferPool.Put(buf)
}

// Restream wraps types.Restreamer to allow adding methods in this package.
// This enables higher-level restreaming logic without polluting the base struct.
type Restream struct {
	*types.Restreamer
}

// NewRestreamer creates and initializes a new Restreamer instance.
// - channel: the channel object this restreamer is associated with
// - bufferSize: the size of the ring buffer in bytes
// - logger: application logger
// - httpClient: custom HTTP client for making requests
// - cfg: application configuration
func NewRestreamer(channel *types.Channel, bufferSize int64, httpClient *client.HeaderSettingClient, cfg *config.Config, rateLimiter ratelimit.Limiter) *Restream {
	logger.Debug("{restream/restream - NewRestreamer} Creating restreamer for channel %s with buffer size %d MB", channel.Name, bufferSize/(1024*1024))

	ctx, cancel := context.WithCancel(context.Background())

	base := &types.Restreamer{
		Channel:     channel,
		SourceCache: xsync.NewMapOf[string, *config.SourceConfig](),
		HttpClient:  httpClient,
		Config:      cfg,
		RateLimiter: rateLimiter,
		Stats:       &types.StreamStats{},
	}
	base.SetContext(ctx, cancel)

	base.StoreBuffer(bbuffer.NewRingBuffer(bufferSize))
	base.ReplaceSwitchNotify()

	base.LastActivity.Store(time.Now().Unix())
	base.Running.Store(false)
	base.SwitchTo.Store(-1)

	logger.Debug("{restream/restream - NewRestreamer} Restreamer initialized for channel %s", channel.Name)

	return &Restream{base}
}

// resetBufferSafely resets the buffer while preserving client connections
func (r *Restream) resetBufferSafely() {

	// if our buffer still exists
	if b := r.LoadBuffer(); b != nil && !b.IsDestroyed() {
		b.Reset()

		logger.Debug("{restream/restream - resetBufferSafely} Channel %s: Buffer reset", r.Channel.Name)
	} else {

		// Only create new buffer if none exists or it was destroyed
		bufferSize := r.Config.BufferSizePerStream * 1024 * 1024
		r.StoreBuffer(bbuffer.NewRingBuffer(bufferSize))
		logger.Debug("{restream/restream - resetBufferSafely} Channel %s: New buffer created (%d MB)", r.Channel.Name, r.Config.BufferSizePerStream)

	}

	// If buffer is destroyed, don't recreate - let Stream() handle it
}

// trackStreamStart records when a stream begins for duration tracking
func (r *Restream) trackStreamStart() time.Time {
	return time.Now()
}

// RestartMonitors restarts the background monitoring goroutines that are normally
// started by AddClient but do not survive a watcher-triggered stream switch.
func (r *Restream) RestartMonitors() {
	go r.monitorClientHealth()
	go r.StartStatsCollection()
}
