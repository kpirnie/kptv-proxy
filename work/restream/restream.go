package restream

import (
	"bytes"
	"context"
	"fmt"
	"io"
	bbuffer "kptv-proxy/work/buffer"
	"kptv-proxy/work/client"
	"kptv-proxy/work/config"
	"kptv-proxy/work/constants"
	"kptv-proxy/work/deadstreams"
	"kptv-proxy/work/logger"
	"kptv-proxy/work/metrics"
	"kptv-proxy/work/parser"
	"kptv-proxy/work/stream"
	"kptv-proxy/work/types"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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

	logger.Debug("{restream/restream - NewRestreamer} Restreamer initialized for channel %s", channel.Name)

	return &Restream{base}
}

// AddClient registers a new client to receive stream data.
// - id: unique identifier for the client
// - w: the HTTP response writer
// - flusher: the HTTP flusher to push data immediately
func (r *Restream) AddClient(id string, w http.ResponseWriter, flusher http.Flusher) {
	if r.Clients == nil {
		r.Clients = xsync.NewMapOf[string, *types.RestreamClient]()
	}

	writeChanDepth := r.Config.SlowClientBufferChunks
	if r.Config.FFmpegMode {
		writeChanDepth = constants.Internal.FFmpegClientBufferChunks
	}

	client := &types.RestreamClient{
		Id:        id,
		Writer:    w,
		Flusher:   flusher,
		Done:      make(chan bool),
		WriteChan: make(chan []byte, writeChanDepth),
	}

	client.LastSeen.Store(time.Now().Unix())
	client.LastProgress.Store(time.Now().Unix())
	r.Clients.Store(id, client)
	r.LastActivity.Store(time.Now().Unix())

	// Start the per-client drain goroutine that owns all writes to this client's
	// socket, keeping the distribution loop fully decoupled from TCP drain speed.
	go r.drainClient(client)

	clientCount := 0
	r.Clients.Range(func(key string, value *types.RestreamClient) bool {
		clientCount++
		return true
	})

	metrics.ClientsConnected.WithLabelValues(r.Channel.Name).Set(float64(clientCount))

	logger.Debug("{restream/restream - AddClient} Channel %s: ID: %s, Total: %d", r.Channel.Name, id, clientCount)

	// serialize against stopStream: without this, a client connecting while
	// the last client's teardown is mid-flight can launch Stream() against a
	// cancelled context and destroyed buffer
	r.Lifecycle.Lock()
	started := false
	if !r.Running.Load() && r.Running.CompareAndSwap(false, true) {
		logger.Debug("{restream/restream - AddClient} Channel %s: Starting", r.Channel.Name)
		go r.Stream()
		go r.monitorClientHealth()
		go r.StartStatsCollection()
		started = true
	}
	r.Lifecycle.Unlock()

	if started {
		// Brief delay to allow buffer to pre-warm before client writes
		logger.Debug("{restream/restream - AddClient} Channel %s: Starting buffer warmup delay", r.Channel.Name)
		time.Sleep(constants.Internal.BufferWarmupDelay)
	}
}

// RemoveClient unregisters a client from the restreamer.
// - id: unique identifier for the client to be removed
func (r *Restream) RemoveClient(id string) {

	// Attempt to load and delete the client from the map
	if client, ok := r.Clients.LoadAndDelete(id); ok {
		// Signal the drain goroutine to exit by closing Done. WriteChan is never
		// closed here: DistributeToClients has multiple concurrent senders, so
		// closing it would race with an in-flight send and panic.
		select {
		case <-client.Done:
			// Already closed
		default:
			close(client.Done)
		}

		// Count remaining clients
		clientCount := 0
		r.Clients.Range(func(key string, value *types.RestreamClient) bool {
			clientCount++
			return true
		})

		// Update Prometheus metrics for clients
		metrics.ClientsConnected.WithLabelValues(r.Channel.Name).Set(float64(clientCount))

		// Debug logging
		logger.Debug("{restream/restream - RemoveClient} clients_connected: %d [%s]", clientCount, r.Channel.Name)
		logger.Debug("{restream/restream - RemoveClient} Channel %s: Client %s removed, remaining: %d", r.Channel.Name, id, clientCount)

		if clientCount == 0 {
			// do not stop the stream if a watcher-initiated switch is in progress —
			// the new goroutine is starting up and needs the context and buffer intact
			if r.Switching.Load() {
				logger.Debug("{restream/restream - RemoveClient} Channel %s: No more clients but switch in progress, skipping stop", r.Channel.Name)
			} else {
				logger.Debug("{restream/restream - RemoveClient} Channel %s: No more clients", r.Channel.Name)
				r.stopStream()
			}
		}
	}

}

// stopStream forces the restreamer to stop streaming immediately.
// It cancels the context, destroys the buffer, resets state, and runs GC.
func (r *Restream) stopStream() {

	// serialize against AddClient's start block, and re-check the client
	// count under the lock — a client may have connected between the
	// caller's zero-count observation and now, in which case stopping
	// would tear down a stream that just gained a viewer
	r.Lifecycle.Lock()
	defer r.Lifecycle.Unlock()

	clientCount := 0
	r.Clients.Range(func(key string, value *types.RestreamClient) bool {
		clientCount++
		return true
	})
	if clientCount > 0 {
		logger.Debug("{restream/restream - stopStream} Channel %s: Client connected during stop, aborting", r.Channel.Name)
		return
	}

	// Only proceed if running state changes from true → false
	if r.Running.CompareAndSwap(true, false) {
		logger.Debug("{restream/restream - stopStream} Stopping stream for channel %s", r.Channel.Name)

		// Cancel the current streaming context
		r.CancelStream()
		logger.Debug("{restream/restream - stopStream} Context cancelled for channel %s", r.Channel.Name)

		// Destroy buffer if valid
		if b := r.LoadBuffer(); b != nil && !b.IsDestroyed() {
			b.Destroy()
			logger.Debug("{restream/restream - stopStream} Buffer destroyed for channel %s", r.Channel.Name)
		}
		r.StoreBuffer(nil)

		// Reset streaming context and index for future restarts
		newCtx, newCancel := context.WithCancel(context.Background())
		r.SetContext(newCtx, newCancel)

	}
}

// Stream is the main streaming loop for the restreamer.
// It attempts to stream from preferred or fallback sources, handles failures,
// switches streams when necessary, and manages retry logic.
func (r *Restream) Stream() {

	// Ensure panic recovery to avoid crashing the whole process
	defer func() {

		if rec := recover(); rec != nil {
			logger.Debug("{restream/restream - Stream} Channel %s: Recovered from panic: %v", r.Channel.Name, rec)
		}

		// Mark restreamer as no longer running
		r.Running.Store(false)

		// Reset active connections metric
		metrics.ActiveConnections.WithLabelValues(r.Channel.Name).Set(0)
	}()

	logger.Debug("{restream/restream - Stream} Channel %s: Starting streaming loop", r.Channel.Name)

	// Lock channel to get stream count
	r.Channel.Mu.RLock()
	streamCount := len(r.Channel.Streams)
	r.Channel.Mu.RUnlock()

	// Bail out if no streams exist
	if streamCount == 0 {
		return
	}

	// Load indexes for current and preferred streams
	currentIndex := int(atomic.LoadInt32(&r.CurrentIndex))
	preferredIndex := int(atomic.LoadInt32(&r.Channel.PreferredStreamIndex))

	// Decide starting index and set it immediately
	var startingIndex int
	if currentIndex >= 0 && currentIndex < streamCount && currentIndex == preferredIndex {
		startingIndex = currentIndex
		logger.Debug("{restream/restream - Stream} Channel %s: Using manually set stream index %d", r.Channel.Name, currentIndex)

	} else {
		if preferredIndex >= 0 && preferredIndex < streamCount {
			startingIndex = preferredIndex
			logger.Debug("{restream/restream - Stream} Channel %s: Starting with preferred stream index %d", r.Channel.Name, preferredIndex)

		} else {
			startingIndex = 0
			logger.Debug("{restream/restream - Stream} Channel %s: Starting with default stream index 0", r.Channel.Name)

		}
	}

	// Set the current index immediately so other components can read it correctly
	atomic.StoreInt32(&r.CurrentIndex, int32(startingIndex))

	// Retry configuration
	maxTotalAttempts := streamCount * constants.Internal.StreamMaxAttemptsMultiplier // maximum attempts across streams
	totalAttempts := 0                                                               // attempts counter
	triedPreferred := false                                                          // whether the preferred was tried
	consecutiveFailures := make(map[int]int)                                         // map of stream index → consecutive failures

	// Loop until all attempts exhausted
	for totalAttempts < maxTotalAttempts {
		select {
		case <-r.Context().Done():
			isManualSwitch := r.ManualSwitch.Load()

			if isManualSwitch {
				// watcher owns this restart - exit cleanly and let restartWithExistingLogic handle it
				logger.Debug("{restream/restream - Stream} Channel %s: Exiting for watcher-controlled switch", r.Channel.Name)
				r.ManualSwitch.Store(false)
				return
			}

			logger.Debug("{restream/restream - Stream} Channel %s: Context done, manual=%v, attempts=%d/%d",
				r.Channel.Name, isManualSwitch, totalAttempts, maxTotalAttempts)

			// Count clients still connected
			clientCount := 0
			r.Clients.Range(func(key string, value *types.RestreamClient) bool {
				clientCount++
				return true
			})

			// non-manual cancellation is always deliberate (stopStream or app
			// shutdown) — exit rather than self-heal with a fresh context,
			// which resurrected killed streams and could clobber the context
			// of a newly launched Stream() goroutine, leaving two running
			logger.Debug("{restream/restream - Stream} Channel %s: Deliberate cancellation with %d clients, exiting", r.Channel.Name, clientCount)
			return

		default:
		}

		// Count active clients
		clientCount := 0
		r.Clients.Range(func(key string, value *types.RestreamClient) bool {
			clientCount++
			return true
		})

		// Bail if no clients
		if clientCount == 0 {
			logger.Debug("{restream/restream - Stream} Channel %s: No clients remaining", r.Channel.Name)
			r.LastStreamFailed.Store(true)
			return
		}

		// Get current index and increment attempts
		currentIdx := int(atomic.LoadInt32(&r.CurrentIndex))
		totalAttempts++

		// DON'T reset buffer during manual switch
		if !r.ManualSwitch.Load() {
			r.resetBufferSafely()
		}

		// Attempt to stream from source
		logger.Debug("{restream/restream - Stream} Channel %s: Attempting stream %d, manual switch flag: %t",
			r.Channel.Name, currentIdx, r.ManualSwitch.Load())

		success, bytesTransferred := r.StreamFromSource(currentIdx)

		// Check if this was a manual switch AFTER the stream attempt
		wasManualSwitch := r.ManualSwitch.Load()
		logger.Debug("{restream/restream - Stream} Channel %s: Stream %d success: %t, manual switch: %t",
			r.Channel.Name, currentIdx, success, wasManualSwitch)

		// Reset manual switch flag for new stream attempt
		r.ManualSwitch.Store(false)

		// if the stream was successful
		if success {

			// Check if this was a very brief success (likely a failure)
			if bytesTransferred < constants.Internal.BriefSuccessThreshold { // Less than 64K suggests very brief connection
				consecutiveFailures[currentIdx]++
				logger.Debug("{restream/restream - Stream} Channel %s: Stream %d succeeded briefly (%d bytes), treating as failure", r.Channel.Name, currentIdx, bytesTransferred)

				// Don't return, continue to try next stream
			} else {

				// Reset failure count for substantial success
				consecutiveFailures[currentIdx] = 0
				logger.Debug("{restream/restream - Stream} Channel %s: Stream %d succeeded with %d bytes, resetting failure count", r.Channel.Name, currentIdx, bytesTransferred)

				// For manual switches, don't return - continue to stream the new index
				if wasManualSwitch {
					r.ManualSwitch.Store(false)
					// if this is a watcher-initiated switch, exit immediately —
					// restartWithExistingLogic is already launching a new Stream() goroutine
					// and continuing here would create two goroutines streaming simultaneously
					if r.Switching.Load() {
						logger.Debug("{restream/restream - Stream} Channel %s: Watcher switch detected, exiting loop to let restartWithExistingLogic take over", r.Channel.Name)
						return
					}
					newIdx := int(atomic.LoadInt32(&r.CurrentIndex))
					logger.Debug("{restream/restream - Stream} Channel %s: Manual switch succeeded, continuing with stream %d", r.Channel.Name, newIdx)
					totalAttempts = 0
					consecutiveFailures = make(map[int]int)
					continue
				}

				// Check if context was cancelled due to manual switch
				if r.Context().Err() != nil {
					isManualSwitch := r.ManualSwitch.Load()
					if isManualSwitch {
						logger.Debug("{restream/restream - Stream} Channel %s: Context cancelled due to manual switch, continuing", r.Channel.Name)

						r.ManualSwitch.Store(false)

						select {
						case <-time.After(constants.Internal.RetryDelay):
						case <-r.Context().Done():
							return
						}

						continue
					}
				}

				// check if clients are still connected before deciding to loop or exit
				clientCount := 0
				r.Clients.Range(func(key string, value *types.RestreamClient) bool {
					clientCount++
					return true
				})

				if clientCount == 0 {
					// no clients, legitimate stop
					r.LastStreamFailed.Store(false)
					return
				}

				// clients still connected — segment boundary, reconnect immediately
				totalAttempts = 0
				consecutiveFailures = make(map[int]int)
				triedPreferred = false
				r.LastStreamFailed.Store(false)

				// Pause before restarting to prevent rapid cycling on short .ts
				// segments — gives the client's WriteChan time to drain fully.
				// Use Sleep instead of select on r.Ctx.Done() since stopStream
				// cancels and immediately recreates the context, causing a false exit.
				time.Sleep(constants.Internal.EOFRestartDelay)

				continue
			}

		}

		// A manual (non-watcher) switch cancels the in-flight stream, which
		// surfaces here as a forced-close "failure" of the OLD index. Honor the
		// operator's choice: don't rotate or penalize, just resume at the
		// manually selected CurrentIndex. Watcher switches set Switching and keep
		// their own failover logic, so they are excluded here.
		if wasManualSwitch && !r.Switching.Load() {
			logger.Debug("{restream/restream - Stream} Channel %s: Manual switch interrupted stream %d, resuming at selected index %d",
				r.Channel.Name, currentIdx, int(atomic.LoadInt32(&r.CurrentIndex)))
			totalAttempts = 0
			triedPreferred = false
			consecutiveFailures = make(map[int]int)
			continue
		}

		// If we reach here, either it was a failure or brief success - continue to next stream
		// Increment consecutive failure count
		consecutiveFailures[currentIdx]++

		// debug logging
		logger.Debug("{restream/restream - Stream} Channel %s: Stream %d failed (consecutive failures: %d)",
			r.Channel.Name, currentIdx, consecutiveFailures[currentIdx])

		// Handle multiple failures → mark stream as bad
		if consecutiveFailures[currentIdx] >= constants.Internal.StreamConsecutiveFailureThreshold {
			r.Channel.Mu.RLock()
			if currentIdx < len(r.Channel.Streams) {
				currentStream := r.Channel.Streams[currentIdx]
				r.Channel.Mu.RUnlock()

				// Record the failure for monitoring/blocking
				stream.HandleStreamFailure(currentStream, r.Config, r.Channel.Name, currentIdx)

				// debug logging
				logger.Debug("{restream/restream - Stream} Channel %s: Stream %d failed %d consecutive times, tracked for potential auto-blocking",
					r.Channel.Name, currentIdx, consecutiveFailures[currentIdx])

			} else {
				r.Channel.Mu.RUnlock()
			}
		}

		// Mark preferred as tried — reload the index since the watcher or an
		// admin action may have changed it after the loop started
		if livePreferred := int(atomic.LoadInt32(&r.Channel.PreferredStreamIndex)); currentIdx == livePreferred && !triedPreferred {
			triedPreferred = true
			logger.Debug("{restream/restream - Stream} Channel %s: Preferred stream %d failed, trying fallback streams", r.Channel.Name, livePreferred)

		}

		// If multiple streams, rotate index
		if streamCount > 1 {
			newIdx := (currentIdx + 1) % streamCount
			atomic.StoreInt32(&r.CurrentIndex, int32(newIdx))
			logger.Debug("{restream/restream - Stream} Channel %s: Switching from stream %d to stream %d", r.Channel.Name, currentIdx, newIdx)

		}

		// Add jitter to prevent thundering herd when multiple channels fail simultaneously
		jitter := constants.Internal.StreamJitterMinMs + time.Duration(time.Now().UnixNano())%constants.Internal.StreamJitterRangeMs

		// Sleep briefly before retry
		select {
		case <-r.Context().Done():
			isManualSwitch := r.ManualSwitch.Load()

			if isManualSwitch {
				logger.Debug("{restream/restream - Stream} Channel %s: Manual switch during retry delay", r.Channel.Name)
			} else {
				logger.Debug("{restream/restream - Stream} Channel %s: Context cancelled during retry", r.Channel.Name)
			}

			// Count clients
			clientCount := 0
			r.Clients.Range(func(key string, value *types.RestreamClient) bool {
				clientCount++
				return true
			})

			// manual switch: the switcher (ForceStreamSwitch) already installed
			// a fresh context before cancelling the old one — just resume the
			// loop on it; creating another here clobbered the switcher's
			if isManualSwitch && clientCount > 0 {
				logger.Debug("{restream/restream - Stream} Channel %s: %d clients connected, continuing after manual switch", r.Channel.Name, clientCount)
				r.ManualSwitch.Store(false)
				time.Sleep(constants.Internal.RetryDelay)
				continue
			}

			// non-manual: deliberate stop or shutdown — exit
			return
		case <-time.After(jitter): // between .05 and .5 seconds
		}

	}

	// If we reached here, all streams failed
	logger.Debug("{restream/restream - Stream} Channel %s: All streams failed after %d attempts", r.Channel.Name, totalAttempts)

	// Log final failure counts
	for streamIdx, failures := range consecutiveFailures {
		if failures > 0 {
			logger.Debug("{restream/restream - Stream} Channel %s: Stream %d had %d consecutive failures",
				r.Channel.Name, streamIdx, failures)
		}
	}

	// Start fallback video if we still have clients
	clientCount := 0
	r.Clients.Range(func(key string, value *types.RestreamClient) bool {
		clientCount++
		return true
	})

	if clientCount > 0 {
		r.streamFallbackVideo()

		// fallback returned with clients still connected — reset the attempt
		// counters and re-enter the source retry loop so a transient provider
		// outage does not strand clients on the loading video permanently
		clientCount = 0
		r.Clients.Range(func(key string, value *types.RestreamClient) bool {
			clientCount++
			return true
		})

		if clientCount > 0 && r.Context().Err() == nil {
			logger.Debug("{restream/restream - Stream} Channel %s: Retrying real sources after fallback period", r.Channel.Name)
			r.Stream()
		}
	}

}

// StreamFromSource attempts to stream from a specific source index.
// It performs the following checks and steps:
//   - Ensure the index is valid
//   - Check if the stream is marked dead or blocked
//   - Enforce per-source connection limits
//   - Retrieve variants (master playlists or single URLs)
//   - Stream the variant (or all variants in master mode)
//
// Returns:
//   - bool: whether the streaming attempt succeeded
//   - int64: number of bytes successfully transferred
func (r *Restream) StreamFromSource(index int) (bool, int64) {

	// debug logger
	logger.Debug("{restream/restream - StreamFromSource} Channel %s: Attempting to stream from index %d", r.Channel.Name, index)

	// Acquire read lock ONCE to access the channel's stream list safely
	r.Channel.Mu.RLock()
	if index >= len(r.Channel.Streams) {
		r.Channel.Mu.RUnlock()
		return false, 0
	}

	// CHECK FFMPEG MODE - with lock already held
	if r.Config.FFmpegMode {
		streamURL := r.Channel.Streams[index].URL
		r.Channel.Mu.RUnlock()

		// debug logger
		logger.Debug("{restream/restream - StreamFromSource} Channel %s", r.Channel.Name)

		return r.streamWithFFmpeg(streamURL)
	}

	if index >= len(r.Channel.Streams) {

		// If the requested index is invalid, unlock and exit
		r.Channel.Mu.RUnlock()
		return false, 0
	}
	stream := r.Channel.Streams[index]
	r.Channel.Mu.RUnlock()

	// Check if the stream was previously marked as dead, but allow occasional retries
	if deadstreams.IsStreamDead(r.Channel.Name, stream.URLHash) {
		deadReason := deadstreams.GetDeadStreamReason(r.Channel.Name, stream.URLHash)
		if deadReason == "manual" {
			// Always skip manually killed streams
			logger.Debug("{restream/restream - StreamFromSource} Channel %s: Stream %d is manually marked dead", r.Channel.Name, index)

			return false, 0
		}
		// For auto-blocked streams, skip most of the time but allow occasional retry
		logger.Debug("{restream/restream - StreamFromSource} Channel %s: Stream %d is marked dead (reason: %s)", r.Channel.Name, index, deadReason)

		return false, 0
	}

	// Skip stream if explicitly blocked
	if atomic.LoadInt32(&stream.Blocked) == 1 {
		logger.Debug("{restream/restream - StreamFromSource} Channel %s: Stream %d is blocked", r.Channel.Name, index)

		return false, 0
	}

	// CRITICAL: Apply rate limiting BEFORE attempting connection to provider
	// This prevents overwhelming the provider with too many simultaneous connection attempts
	if r.RateLimiter != nil {
		r.RateLimiter.Take()
		if r.Config.Debug {
			logger.Debug("{restream/restream - StreamFromSource} Channel %s: Applied rate limit for stream %d (source: %s)",
				r.Channel.Name, index, stream.Source.Name)
		}
	}

	// Enforce connection limit for this source
	if stream.Source.ActiveConns.Load() >= int32(stream.Source.MaxConnections) {
		logger.Debug("{restream/restream - StreamFromSource} Channel %s: Stream %d source at max connections (%d)", r.Channel.Name, index, stream.Source.MaxConnections)
		return false, 0
	}

	// Increment active connections for the source
	stream.Source.ActiveConns.Add(1)
	defer stream.Source.ActiveConns.Add(-1) // ensure decrement when function exits

	// Retrieve variants (master playlist) or a live response (direct URL)
	variants, isMaster, liveResp, cancelLive, err := r.getStreamVariants(stream.URL, stream.Source)
	if err != nil {
		logger.Error("{restream/restream - StreamFromSource} Channel %s: Failed to get variants from stream %d: %v", r.Channel.Name, index, err)
		return false, 0
	}

	// If master playlist → try all variants
	if isMaster {
		logger.Debug("{restream/restream - StreamFromSource} Channel %s: Master playlist detected with %d variants", r.Channel.Name, len(variants))

		// loop over all variants to test them
		for i, variant := range variants {
			logger.Debug("{restream/restream - StreamFromSource} Channel %s: Testing variant %d (%s)", r.Channel.Name, i, variant.URL)

			if ok, bytes := r.testAndStreamVariant(variant, stream.Source); ok {
				logger.Debug("{restream/restream - StreamFromSource} Channel %s: Successfully streamed variant %d (%s)", r.Channel.Name, i, variant.URL)

				return true, bytes
			}
		}

		// None of the variants succeeded
		logger.Error("{restream/restream - StreamFromSource} Channel %s: All variants failed", r.Channel.Name)

		return false, 0
	}

	// Direct URL — sniff and stream from the already-open response,
	// no second GET, preserving single-use token URLs
	if liveResp == nil || cancelLive == nil {
		logger.Error("{restream/restream - StreamFromSource} Channel %s: No live response returned for non-master stream %d", r.Channel.Name, index)
		if cancelLive != nil {
			cancelLive()
		}
		return false, 0
	}
	defer cancelLive()
	return r.sniffAndStreamResponse(liveResp, stream.URL, stream.Source)
}

// getStreamVariants fetches a stream URL and determines if it is a master playlist.
// For non-master URLs the live *http.Response is returned so the caller can stream
// from the same connection (single GET; preserves single-use token URLs).
// Returns:
//   - []parser.StreamVariant: parsed variants (master playlists only)
//   - bool: true if master playlist
//   - *http.Response: live response for direct streaming (non-master only)
//   - context.CancelFunc: caller must invoke when done with the response
//   - error: any encountered error
func (r *Restream) getStreamVariants(url string, source *config.SourceConfig) ([]parser.StreamVariant, bool, *http.Response, context.CancelFunc, error) {
	logger.Debug("{restream/restream - getStreamVariants} Fetching variants for channel %s from URL: %s", r.Channel.Name, url)

	// Initialize a master playlist handler
	masterHandler := parser.NewMasterPlaylistHandler(r.Config)

	// Build HTTP GET request for the stream URL
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		logger.Error("{restream/restream - getStreamVariants} Failed to create request for channel %s: %v", r.Channel.Name, err)
		return nil, false, nil, nil, err
	}

	// Cancellable child context with a validation timer instead of a hard
	// deadline — the response may be handed back for direct streaming and
	// must outlive the validation window
	checkCtx, cancel := context.WithCancel(r.Context())
	validationTimer := time.AfterFunc(constants.Internal.StreamVariantFetchTimeout, cancel)
	req = req.WithContext(checkCtx)

	// Execute HTTP request with custom headers from the source
	resp, err := r.HttpClient.DoWithHeaders(req, source.UserAgent, source.ReqOrigin, source.ReqReferrer)
	if err != nil {
		validationTimer.Stop()
		cancel()
		logger.Error("{restream/restream - getStreamVariants} HTTP request failed for channel %s: %v", r.Channel.Name, err)
		return nil, false, nil, nil, err
	}

	// Non-200 response codes are considered fatal
	if resp.StatusCode != http.StatusOK {
		validationTimer.Stop()
		cancel()
		resp.Body.Close()
		logger.Error("{restream/restream - getStreamVariants} HTTP %d response for channel %s", resp.StatusCode, r.Channel.Name)
		return nil, false, nil, nil, fmt.Errorf("HTTP %d response", resp.StatusCode)
	}

	// Decide whether to check the body as a potential master playlist
	if !r.shouldCheckForMasterPlaylist(resp) {
		// Not a playlist — stop the validation timer and hand back the live
		// response; stream lifetime is bounded by r.Context() via the child
		// cancel, which the caller invokes when streaming ends
		validationTimer.Stop()
		logger.Debug("{restream/restream - getStreamVariants} Returning live response for direct streaming for channel %s", r.Channel.Name)
		return nil, false, resp, cancel, nil
	}

	logger.Debug("{restream/restream - getStreamVariants} Processing as master playlist for channel %s", r.Channel.Name)

	// Read the entire body for playlist parsing
	body, err := io.ReadAll(resp.Body)
	validationTimer.Stop()
	resp.Body.Close()
	if err != nil {
		cancel()
		logger.Error("{restream/restream - getStreamVariants} Failed to read response body for channel %s: %v", r.Channel.Name, err)
		return nil, false, nil, nil, err
	}

	// Parse the body as a master playlist and return variants
	variants, isMaster, perr := masterHandler.ProcessMasterPlaylistVariants(string(body), url, r.Channel.Name)
	if perr != nil {
		cancel()
		return nil, false, nil, nil, perr
	}

	// Detection consumed the body on a non-master response, so hand it back over the
	// buffered bytes rather than forcing the caller to re-request the stream
	if !isMaster {
		resp.Body = io.NopCloser(bytes.NewReader(body))
		logger.Debug("{restream/restream - getStreamVariants} Returning buffered response for direct streaming for channel %s", r.Channel.Name)
		return variants, false, resp, cancel, nil
	}

	cancel()
	return variants, true, nil, nil, nil
}

// testAndStreamVariant attempts to validate and stream from a variant URL.
// - It fetches the variant and checks the first chunk of data.
// - If the data resembles an HLS playlist (#EXTINF markers), it streams HLS segments.
// - Otherwise, it streams directly from the variant URL.
// Returns:
//   - bool: success flag
//   - int64: number of bytes streamed
func (r *Restream) testAndStreamVariant(variant parser.StreamVariant, source *config.SourceConfig) (bool, int64) {
	logger.Debug("{restream/restream - testAndStreamVariant} Testing variant for channel %s: %s (resolution: %s)", r.Channel.Name, variant.URL, variant.Resolution)

	// Use FFmpeg if enabled, bypassing all variant testing
	if r.Config.FFmpegMode {
		logger.Debug("{restream/restream - testAndStreamVariant} FFmpeg mode enabled for channel %s, bypassing variant test", r.Channel.Name)
		return r.streamWithFFmpeg(variant.URL)
	}

	// Build HTTP GET request for the variant
	testReq, err := http.NewRequest("GET", variant.URL, nil)
	if err != nil {
		logger.Warn("{restream/restream - testAndStreamVariant} Failed to create request for channel %s: %v", r.Channel.Name, err)
		return false, 0
	}

	// Cancellable context with a validation timer — a deadline context would
	// kill the stream mid-flight since this response is streamed directly
	testCtx, cancel := context.WithCancel(r.Context())
	validationTimer := time.AfterFunc(constants.Internal.StreamVariantTestTimeout, cancel)
	defer cancel()
	testReq = testReq.WithContext(testCtx)

	// Execute the request
	resp, err := r.HttpClient.DoWithHeaders(testReq, source.UserAgent, source.ReqOrigin, source.ReqReferrer)
	if err != nil {
		validationTimer.Stop()
		logger.Warn("{restream/restream - testAndStreamVariant} HTTP request failed for channel %s: %v", r.Channel.Name, err)
		return false, 0
	}

	// Reject if status code is not OK
	if resp.StatusCode != http.StatusOK {
		validationTimer.Stop()
		resp.Body.Close()
		logger.Warn("{restream/restream - testAndStreamVariant} HTTP %d for channel %s", resp.StatusCode, r.Channel.Name)
		return false, 0
	}

	validationTimer.Stop()

	// Sniff and stream from this same response — no re-GET
	return r.sniffAndStreamResponse(resp, variant.URL, source)
}

// shouldCheckForMasterPlaylist decides whether a given HTTP response
// should be parsed as a potential master playlist.
// Criteria:
//   - Content-Type contains "mpegurl" or "m3u8"
//   - Content-Length is below 100 KB (heuristic for playlists)
func (r *Restream) shouldCheckForMasterPlaylist(resp *http.Response) bool {

	// get the content type and length
	contentType := resp.Header.Get("Content-Type")
	contentLength := resp.Header.Get("Content-Length")

	logger.Debug("{restream/restream - shouldCheckForMasterPlaylist} Checking master playlist criteria for channel %s: content-type=%s, length=%s", r.Channel.Name, contentType, contentLength)

	// Check content-type header
	if strings.Contains(strings.ToLower(contentType), "mpegurl") ||
		strings.Contains(strings.ToLower(contentType), "m3u8") {
		logger.Debug("{restream/restream - shouldCheckForMasterPlaylist} Master playlist detected by content-type for channel %s", r.Channel.Name)
		return true
	}

	// If length is very small, it's likely a playlist
	if contentLength != "" {
		if length, err := strconv.ParseInt(contentLength, 10, 64); err == nil {
			if length > 0 && length < constants.Internal.MasterPlaylistSizeThreshold { // under 100 KB
				logger.Debug("{restream/restream - shouldCheckForMasterPlaylist} Master playlist detected by content-length for channel %s: %d bytes", r.Channel.Name, length)
				return true
			}
		}
	}

	return false
}

// sniffAndStreamResponse determines whether an already-open response is an HLS
// playlist or a direct stream, then streams it. Direct streams continue on the
// same connection; peeked detection bytes are forwarded, not discarded.
func (r *Restream) sniffAndStreamResponse(resp *http.Response, url string, source *config.SourceConfig) (bool, int64) {

	// Check Content-Type header first - most efficient detection
	contentType := strings.ToLower(resp.Header.Get("Content-Type"))

	// If Content-Type clearly indicates MPEG-TS, stream directly
	if strings.Contains(contentType, "video/mp2t") ||
		strings.Contains(contentType, "video/mpeg") {
		logger.Debug("{restream/restream - sniffAndStreamResponse} Direct stream detected via Content-Type for channel %s: %s", r.Channel.Name, contentType)

		return r.streamFromResponse(resp, nil)
	}

	// If Content-Type clearly indicates playlist, use HLS
	if strings.Contains(contentType, "application/vnd.apple.mpegurl") ||
		strings.Contains(contentType, "application/x-mpegurl") ||
		strings.Contains(contentType, "audio/mpegurl") {
		logger.Debug("{restream/restream - sniffAndStreamResponse} HLS playlist detected via Content-Type for channel %s: %s", r.Channel.Name, contentType)

		body, err := io.ReadAll(resp.Body)
		effectiveURL := resp.Request.URL.String()
		resp.Body.Close()
		if err != nil {
			logger.Warn("{restream/restream - sniffAndStreamResponse} Failed to read playlist body for channel %s, re-fetching: %v", r.Channel.Name, err)
			return r.streamHLSSegments(url)
		}
		return r.streamHLSSegmentsFrom(url, body, effectiveURL)
	}

	// Content-Type ambiguous or missing - need to peek at content
	testBuffer := make([]byte, constants.Internal.StreamTestBufferSize)
	n, err := resp.Body.Read(testBuffer)
	if err != nil && err != io.EOF {
		logger.Warn("{restream/restream - sniffAndStreamResponse} Failed to read test buffer for channel %s: %v", r.Channel.Name, err)
		resp.Body.Close()
		return false, 0
	}
	if n == 0 {
		logger.Debug("{restream/restream - sniffAndStreamResponse} Empty response for channel %s", r.Channel.Name)
		resp.Body.Close()
		return false, 0
	}

	// Convert to string for content inspection
	content := string(testBuffer[:n])

	// If this looks like an HLS playlist (contains EXTINF tags)
	if strings.Contains(content, "#EXTINF") || strings.Contains(content, "#EXTM3U") {
		logger.Debug("{restream/restream - sniffAndStreamResponse} HLS playlist detected via content inspection for channel %s", r.Channel.Name)

		rest, err := io.ReadAll(resp.Body)
		effectiveURL := resp.Request.URL.String()
		resp.Body.Close()
		if err != nil {
			logger.Warn("{restream/restream - sniffAndStreamResponse} Failed to read remaining playlist body for channel %s, re-fetching: %v", r.Channel.Name, err)
			return r.streamHLSSegments(url)
		}
		body := append(append([]byte{}, testBuffer[:n]...), rest...)
		return r.streamHLSSegmentsFrom(url, body, effectiveURL)
	}

	logger.Debug("{restream/restream - sniffAndStreamResponse} Direct stream detected via content inspection for channel %s", r.Channel.Name)
	// Continue on the same connection, forwarding the peeked bytes first
	return r.streamFromResponse(resp, testBuffer[:n])
}

// streamFromResponse handles the main streaming loop for an already-open response.
// The optional prefix (peeked detection bytes) is distributed before the read loop
// so the start of the stream is not lost.
func (r *Restream) streamFromResponse(resp *http.Response, prefix []byte) (bool, int64) {
	defer resp.Body.Close()

	var totalBytes int64
	bufPtr := getStreamBuffer()
	buf := *bufPtr
	defer putStreamBuffer(bufPtr)
	lastActivityUpdate := time.Now()
	lastMetricUpdate := time.Now()
	consecutiveErrors := 0
	maxConsecutiveErrors := constants.Internal.StreamMaxConsecutiveErrors

	// Forward peeked detection bytes before entering the read loop
	if len(prefix) > 0 {
		if r.SafeBufferWrite(prefix) {
			r.DistributeToClients(prefix)
			totalBytes += int64(len(prefix))
			metrics.TotalBytesTransferred.Add(int64(len(prefix)))
		}
	}

	for {
		select {
		case <-r.Context().Done():
			if r.ManualSwitch.Load() {
				logger.Debug("{restream/restream - streamFromResponse} Channel %s: Graceful switch", r.Channel.Name)
				return true, totalBytes
			}
			return totalBytes > constants.Internal.StreamMinViableBytes, totalBytes
		default:
		}

		n, err := resp.Body.Read(buf)
		if n > 0 {
			chunk := buf[:n]

			if !r.SafeBufferWrite(chunk) {
				consecutiveErrors++
				if consecutiveErrors >= maxConsecutiveErrors {
					logger.Error("{restream/restream - streamFromResponse} Channel %s: Buffer write failed %d times", r.Channel.Name, consecutiveErrors)
					return false, totalBytes
				}
				time.Sleep(constants.Internal.BufferWriteRetryDelay)
				continue
			}

			consecutiveErrors = 0
			activeClients := r.DistributeToClients(chunk)
			if activeClients == 0 {
				logger.Debug("{restream/restream - streamFromResponse} Channel %s: No active clients", r.Channel.Name)
				return totalBytes > constants.Internal.StreamMinViableBytes, totalBytes
			}

			totalBytes += int64(n)
			metrics.TotalBytesTransferred.Add(int64(n))

			now := time.Now()
			if now.Sub(lastActivityUpdate) > constants.Internal.StreamActivityUpdateInterval {
				r.LastActivity.Store(now.Unix())
				lastActivityUpdate = now
				// Check if stream was marked dead mid-stream
				r.Channel.Mu.RLock()
				idx := int(atomic.LoadInt32(&r.CurrentIndex))
				var isDead bool
				if idx < len(r.Channel.Streams) {
					isDead = deadstreams.IsStreamDead(r.Channel.Name, r.Channel.Streams[idx].URLHash)
				}
				r.Channel.Mu.RUnlock()
				if isDead {
					return false, totalBytes
				}
			}

			if now.Sub(lastMetricUpdate) > constants.Internal.StreamMetricUpdateInterval {
				metrics.BytesTransferred.WithLabelValues(r.Channel.Name, "downstream").Add(float64(n))
				metrics.ActiveConnections.WithLabelValues(r.Channel.Name).Set(float64(activeClients))
				lastMetricUpdate = now
			}
		}

		if err != nil {
			if err == io.EOF {
				success := totalBytes > constants.Internal.EOFSuccessThreshold
				status := "insufficient"
				if success {
					status = "success"
				}
				logger.Debug("{restream/restream - streamFromResponse} Channel %s: Stream ended (%s, %d bytes)", r.Channel.Name, status, totalBytes)

				// EOF drain pause is handled once by the caller (Stream)
				return success, totalBytes
			}

			if r.Context().Err() != nil && r.ManualSwitch.Load() {
				return true, totalBytes
			}

			consecutiveErrors++
			if consecutiveErrors >= maxConsecutiveErrors {
				logger.Error("{restream/restream - streamFromResponse} Channel %s: %v (consecutive: %d)", r.Channel.Name, err, consecutiveErrors)
				return false, totalBytes
			}

			time.Sleep(constants.Internal.RetryDelay)
			continue
		}

		consecutiveErrors = 0
	}
}

// DistributeToClients enqueues a chunk of stream data into each active client's
// outbound channel. The send is non-blocking — if a client's channel is full the
// client is considered too slow and is scheduled for removal, preventing it from
// stalling the distribution loop and degrading faster clients.
func (r *Restream) DistributeToClients(data []byte) int {
	activeClients := 0

	// Copy the chunk once so every client channel holds an independent slice;
	// the source buffer may be reused by the streaming loop immediately after return.
	chunk := make([]byte, len(data))
	copy(chunk, data)

	r.Clients.Range(func(key string, value *types.RestreamClient) bool {
		client := value

		select {
		case client.WriteChan <- chunk:
			// Successful enqueue counts as progress; resets the slow-client clock.
			client.LastProgress.Store(time.Now().Unix())
			activeClients++
		default:
			// Channel full: the client is briefly behind the live edge, common on
			// bursty or high-bitrate (e.g. 4K HEVC) sources that deliver faster than
			// realtime. Rather than drop the client, discard the oldest queued chunk
			// and enqueue the newest so it stays on the live edge. TS decoders resync
			// after a gap, trading a momentary artifact for uninterrupted playback.
			select {
			case <-client.WriteChan: // shed oldest chunk
			default:
			}
			select {
			case client.WriteChan <- chunk:
				client.LastProgress.Store(time.Now().Unix())
			default:
			}
			activeClients++ // keep the client; never drop on a full buffer alone
		}
		return true
	})

	return activeClients
}

// SafeBufferWrite writes data to the buffer if it is still valid.
// It ensures data is not written if the buffer has been destroyed
// or the streaming context is cancelled.
//
// Parameters:
//   - data: the byte slice to write into the buffer
//
// Returns:
//   - bool: true if write succeeded, false if buffer closed/cancelled
func (r *Restream) SafeBufferWrite(data []byte) bool {

	// Check if context cancelled due to manual switch - allow this to succeed
	select {
	case <-r.Context().Done():
		if r.ManualSwitch.Load() {
			// still write the data if the buffer is alive so the watcher's
			// throughput tracking and stats peeks don't see a false gap —
			// previously this returned success while silently skipping the
			// write, desyncing the buffer from what clients received
			if b := r.LoadBuffer(); b != nil && !b.IsDestroyed() {
				b.Write(data)
			}
			logger.Debug("{restream/restream - SafeBufferWrite} Channel %s: Buffer write during manual switch, allowing success", r.Channel.Name)
			return true // Don't treat manual switch cancellation as buffer failure
		}
		return false
	default:
	}

	// Check buffer validity (capture once to avoid a torn read vs a concurrent swap)
	b := r.LoadBuffer()
	if b == nil || b.IsDestroyed() {
		return false
	}

	// Perform write into ring buffer
	b.Write(data)
	return true
}

// monitor client health
func (r *Restream) monitorClientHealth() {
	logger.Debug("{restream/restream - monitorClientHealth} Starting health monitor for channel %s", r.Channel.Name)

	ticker := time.NewTicker(constants.Internal.ClientHealthCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			logger.Debug("{restream/restream - monitorClientHealth} Health monitor stopping for channel %s", r.Channel.Name)
			return
		case <-ticker.C:
			if !r.Running.Load() {
				logger.Debug("{restream/restream - monitorClientHealth} Stream not running for channel %s, stopping monitor", r.Channel.Name)
				return
			}

			now := time.Now().Unix()
			var staleClients []string

			r.Clients.Range(func(key string, value *types.RestreamClient) bool {
				client := value
				lastSeen := client.LastSeen.Load()

				if now-lastSeen > constants.Internal.ClientStaleTimeout {
					staleClients = append(staleClients, key)
				}
				return true
			})

			if len(staleClients) > 0 {
				logger.Debug("{restream/restream - monitorClientHealth} Health check for channel %s: found %d stale clients", r.Channel.Name, len(staleClients))
			}

			for _, clientID := range staleClients {
				logger.Debug("{restream/restream - monitorClientHealth} Removing stale client: %s", clientID)

				r.RemoveClient(clientID)
			}
		}
	}
}

// WatcherStreamFromSource provides an external entry point
// for observers/watchers to call StreamFromSource.
// This is useful for testing or monitoring streams.
func (r *Restream) WatcherStreamFromSource(index int) (bool, int64) {
	return r.StreamFromSource(index)
}

// WatcherStream provides an external entry point for observers
// to run the full Stream loop directly.
func (r *Restream) WatcherStream() {
	r.Stream()
}

// ForceStreamSwitch forces a switch to a specific stream index while preserving clients
func (r *Restream) ForceStreamSwitch(newIndex int) {
	logger.Debug("{restream/restream - ForceStreamSwitch} Channel %s: Switching to stream %d", r.Channel.Name, newIndex)

	// Mark this as a manual switch so context cancellation won't be treated as failure
	r.ManualSwitch.Store(true)

	// Update preferred stream index on the channel
	atomic.StoreInt32(&r.Channel.PreferredStreamIndex, int32(newIndex))

	// Update current index
	atomic.StoreInt32(&r.CurrentIndex, int32(newIndex))

	// If not running, just update index
	if !r.Running.Load() {
		return
	}

	// Count clients before switch
	clientCount := 0
	r.Clients.Range(func(key string, value *types.RestreamClient) bool {
		clientCount++
		return true
	})

	logger.Debug("{restream/restream - ForceStreamSwitch} Channel %s: Forcing switch to stream %d with %d clients", r.Channel.Name, newIndex, clientCount)

	// create new context and install it atomically before cancelling the old one
	newCtx, newCancel := context.WithCancel(context.Background())
	oldBundle := r.SetContext(newCtx, newCancel)

	// restart background monitors since they are tied to the previous context
	go r.RestartMonitors()

	// cancel the OLD context so the running goroutine stops. Clients stay on the
	// same HTTP connection — VLC treats a closed connection as end-of-stream and
	// will not re-request, so we keep it open and let Stream() resume into it.
	if oldBundle != nil && oldBundle.Cancel != nil {
		oldBundle.Cancel()
	}

	// Reset (not destroy) the buffer for reuse by the resuming loop.
	if b := r.LoadBuffer(); b != nil && !b.IsDestroyed() {
		b.Reset()
		logger.Debug("{restream/restream - ForceStreamSwitch} Channel %s: Buffer reset for switch", r.Channel.Name)
	}
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

// streamFallbackVideo streams the offline video in a loop when all streams fail
func (r *Restream) streamFallbackVideo() {
	// Local path inside container - copy loading.ts here
	fallbackPath := constants.Internal.FallbackVideoPath

	logger.Debug("{restream/restream - streamFallbackVideo} Channel %s: Starting fallback video loop", r.Channel.Name)

	// ensure buffer is valid before attempting fallback streaming —
	// it may have been destroyed by a prior ForceStreamSwitch
	if b := r.LoadBuffer(); b == nil || b.IsDestroyed() {
		bufferSize := r.Config.BufferSizePerStream * 1024 * 1024
		r.StoreBuffer(bbuffer.NewRingBuffer(bufferSize))
		logger.Debug("{restream/restream - streamFallbackVideo} Channel %s: Recreated destroyed buffer for fallback", r.Channel.Name)
	}

	for {
		select {
		case <-r.Context().Done():
			logger.Debug("{restream/restream - streamFallbackVideo} Channel %s: Context cancelled", r.Channel.Name)

			return
		default:
		}

		// Check if we still have clients
		clientCount := 0
		r.Clients.Range(func(key string, value *types.RestreamClient) bool {
			clientCount++
			return true
		})

		if clientCount == 0 {
			logger.Debug("{restream/restream - streamFallbackVideo} Channel %s: No clients remaining", r.Channel.Name)

			return
		}

		logger.Debug("{restream/restream - streamFallbackVideo} Channel %s: Starting fallback video playback for %d clients", r.Channel.Name, clientCount)

		// Stream the local fallback video
		r.streamLocalFallback(fallbackPath)

		// Brief pause before restarting loop
		select {
		case <-r.Context().Done():
			return
		case <-time.After(constants.Internal.FallbackVideoLoopDelay):
			continue
		}
	}
}

// streamLocalFallback streams a local .ts file in a loop
func (r *Restream) streamLocalFallback(filePath string) {
	logger.Debug("{restream/restream - streamLocalFallback} Channel %s: Starting local fallback from %s", r.Channel.Name, filePath)

	// Load fallback video into cache if not already loaded
	fallbackVideoCacheMu.RLock()
	needsLoad := fallbackVideoCachePath != filePath || len(fallbackVideoCache) == 0
	fallbackVideoCacheMu.RUnlock()

	if needsLoad {
		fallbackVideoCacheMu.Lock()
		// Double-check after acquiring write lock
		if fallbackVideoCachePath != filePath || len(fallbackVideoCache) == 0 {
			data, err := os.ReadFile(filePath)
			if err != nil {
				fallbackVideoCacheMu.Unlock()
				logger.Debug("{restream/restream - streamLocalFallback} Channel %s: Failed to load file: %v", r.Channel.Name, err)

				return
			}
			fallbackVideoCache = data
			fallbackVideoCachePath = filePath
			logger.Debug("{restream/restream - streamLocalFallback} Cached fallback video: %d bytes", len(data))

		}
		fallbackVideoCacheMu.Unlock()
	}

	fallbackVideoCacheMu.RLock()
	videoData := fallbackVideoCache
	fallbackVideoCacheMu.RUnlock()

	bufPtr := getStreamBuffer()
	buf := *bufPtr
	defer putStreamBuffer(bufPtr)

	lastActivityUpdate := time.Now()
	totalBytes := int64(0)
	offset := 0

	retryDeadline := time.Now().Add(constants.Internal.FallbackRetryInterval)

	for {
		if time.Now().After(retryDeadline) {
			logger.Debug("{restream/restream - streamFallbackVideo} Channel %s: Fallback period elapsed, returning to retry sources", r.Channel.Name)
			return
		}

		select {
		case <-r.Context().Done():
			logger.Debug("{restream/restream - streamLocalFallback} Channel %s: Context cancelled after %d bytes", r.Channel.Name, totalBytes)

			return
		default:
		}

		// Check if we still have clients
		clientCount := 0
		r.Clients.Range(func(key string, value *types.RestreamClient) bool {
			clientCount++
			return true
		})

		if clientCount == 0 {
			logger.Debug("{restream/restream - streamLocalFallback} Channel %s: No clients remaining", r.Channel.Name)
			return
		}

		// Read from cached memory
		remaining := len(videoData) - offset
		if remaining <= 0 {

			// Loop back to beginning
			offset = 0
			remaining = len(videoData)
			logger.Debug("{restream/restream - streamLocalFallback} Channel %s: Looping fallback video", r.Channel.Name)

		}

		n := copy(buf, videoData[offset:])
		offset += n
		var err error
		if offset >= len(videoData) {
			err = io.EOF
		}

		if n > 0 {
			totalBytes += int64(n)
			chunk := buf[:n]

			if !r.SafeBufferWrite(chunk) {
				logger.Debug("{restream/restream - streamLocalFallback} Channel %s: Buffer write failed", r.Channel.Name)
				return
			}

			activeClients := r.DistributeToClients(chunk)
			if activeClients == 0 {
				logger.Debug("{restream/restream - streamLocalFallback} Channel %s: No active clients after distribute", r.Channel.Name)
				return
			}

			// Update activity timestamp periodically
			now := time.Now()
			if now.Sub(lastActivityUpdate) > constants.Internal.StreamActivityUpdateInterval {
				r.LastActivity.Store(now.Unix())
				lastActivityUpdate = now
			}

			// Throttle to approximate real-time playback at the configured pace
			time.Sleep(time.Duration(n) * time.Second / time.Duration(constants.Internal.FallbackVideoPaceBytesPerSec))
		}

		if err != nil {
			if err == io.EOF {
				// Already handled by offset reset above
				continue
			}
		}
	}
}

// RestartMonitors restarts the background monitoring goroutines that are normally
// started by AddClient but do not survive a watcher-triggered stream switch.
func (r *Restream) RestartMonitors() {
	go r.monitorClientHealth()
	go r.StartStatsCollection()
}

// drainClient is the per-client goroutine that owns all writes to a single client's
// HTTP response writer. It reads chunks from the client's bounded writeChan and
// writes them to the socket sequentially. Exits cleanly when writeChan is closed
// by RemoveClient, or removes itself if a write or flush fails.
func (r *Restream) drainClient(client *types.RestreamClient) {
	// per-write deadlines via ResponseController — without them a client with
	// a stuck TCP window blocks Write() forever and leaks this goroutine and
	// the connection, since the server intentionally has no global WriteTimeout
	rc := http.NewResponseController(client.Writer)

	for {
		select {
		case <-client.Done:
			// Client removed. WriteChan is intentionally never closed because
			// DistributeToClients has multiple concurrent senders; Done is the
			// sole termination signal. Any buffered chunks are dropped.
			logger.Debug("{restream/restream - drainClient} Channel %s: Drain goroutine exiting for client %s",
				r.Channel.Name, client.Id)
			return
		case chunk := <-client.WriteChan:
			writeErr := func() (err error) {
				defer func() {
					if rec := recover(); rec != nil {
						err = fmt.Errorf("write/flush panic recovered: %v", rec)
					}
				}()
				rc.SetWriteDeadline(time.Now().Add(constants.Internal.ClientWriteDeadline))
				_, err = client.Writer.Write(chunk)
				if err != nil {
					return err
				}
				client.Flusher.Flush()
				return nil
			}()

			if writeErr != nil {
				logger.Debug("{restream/restream - drainClient} Channel %s: Write error for client %s, removing: %v",
					r.Channel.Name, client.Id, writeErr)
				r.RemoveClient(client.Id)
				return
			}

			client.LastSeen.Store(time.Now().Unix())
		}
	}
}
