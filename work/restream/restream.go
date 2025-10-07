package restream

import (
	"context"
	"fmt"
	"io"
	bbuffer "kptv-proxy/work/buffer"
	"kptv-proxy/work/client"
	"kptv-proxy/work/config"
	"kptv-proxy/work/deadstreams"
	"kptv-proxy/work/metrics"
	"kptv-proxy/work/parser"
	"kptv-proxy/work/stream"
	"kptv-proxy/work/types"
	"log"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"go.uber.org/ratelimit"
)

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
func NewRestreamer(channel *types.Channel, bufferSize int64, logger *log.Logger, httpClient *client.HeaderSettingClient, cfg *config.Config, rateLimiter ratelimit.Limiter) *Restream {
	ctx, cancel := context.WithCancel(context.Background())

	base := &types.Restreamer{
		Channel:     channel,
		Buffer:      bbuffer.NewRingBuffer(bufferSize),
		Ctx:         ctx,
		Cancel:      cancel,
		Logger:      logger,
		HttpClient:  httpClient,
		Config:      cfg,
		RateLimiter: rateLimiter,
		Stats:       &types.StreamStats{},
	}

	base.LastActivity.Store(time.Now().Unix())
	base.Running.Store(false)

	return &Restream{base}
}

// AddClient registers a new client to receive stream data.
// - id: unique identifier for the client
// - w: the HTTP response writer
// - flusher: the HTTP flusher to push data immediately
func (r *Restream) AddClient(id string, w http.ResponseWriter, flusher http.Flusher) {
	client := &types.RestreamClient{
		Id:      id,
		Writer:  w,
		Flusher: flusher,
		Done:    make(chan bool),
	}

	client.LastSeen.Store(time.Now().Unix())
	r.Clients.Store(id, client)
	r.LastActivity.Store(time.Now().Unix())

	clientCount := 0
	r.Clients.Range(func(key string, value *types.RestreamClient) bool {
		clientCount++
		return true
	})

	metrics.ClientsConnected.WithLabelValues(r.Channel.Name).Set(float64(clientCount))

	if r.Config.Debug {
		r.Logger.Printf("[CLIENT_CONNECT] Channel %s: ID: %s, Total: %d", r.Channel.Name, id, clientCount)
	}

	if !r.Running.Load() && r.Running.CompareAndSwap(false, true) {
		if r.Config.Debug {
			r.Logger.Printf("[RESTREAM_START] Channel %s: Starting", r.Channel.Name)
		}
		go r.Stream()
		go r.monitorClientHealth()
		go r.StartStatsCollection()
	}
}

// RemoveClient unregisters a client from the restreamer.
// - id: unique identifier for the client to be removed
func (r *Restream) RemoveClient(id string) {

	// Attempt to load and delete the client from the map
    if client, ok := r.Clients.LoadAndDelete(id); ok {

		// Mark client as finished by closing its Done channel
        select {
        case <-client.Done:
            // Already closed
        default:
            close(client.Done)
        }

        // Remove the client from the buffer's internal tracking - with nil check
        if r.Buffer != nil && !r.Buffer.IsDestroyed() {
            r.Buffer.RemoveClient(id)
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
        if r.Config.Debug {
            r.Logger.Printf("[CLIENT_DISCONNECT] Channel %s: Client: %s", r.Channel.Name, id)
            r.Logger.Printf("[METRIC] clients_connected: %d [%s]", clientCount, r.Channel.Name)
            r.Logger.Printf("[CLIENT_REMOVE] Channel %s: Client %s removed, remaining: %d", r.Channel.Name, id, clientCount)
        }

        // If no clients remain, stop the stream immediately
        if clientCount == 0 {
            if r.Config.Debug {
                r.Logger.Printf("[RESTREAM_STOP] Channel %s: No more clients", r.Channel.Name)
            }
            r.stopStream()
        }
    }
}

// stopStream forces the restreamer to stop streaming immediately.
// It cancels the context, destroys the buffer, resets state, and runs GC.
func (r *Restream) stopStream() {

	// Only proceed if running state changes from true → false
	if r.Running.CompareAndSwap(true, false) {

		// Cancel the current streaming context
		r.Cancel()

		// Destroy buffer if valid
		if r.Buffer != nil && !r.Buffer.IsDestroyed() {
			r.Buffer.Destroy()
		}
		r.Buffer = nil

		// Reset streaming context and index for future restarts
		r.Ctx, r.Cancel = context.WithCancel(context.Background())
		atomic.StoreInt32(&r.CurrentIndex, 0)

		// Force garbage collection to release memory
		runtime.GC()
	}
}

// Stream is the main streaming loop for the restreamer.
// It attempts to stream from preferred or fallback sources, handles failures,
// switches streams when necessary, and manages retry logic.
func (r *Restream) Stream() {

	// Ensure panic recovery to avoid crashing the whole process
	defer func() {

		if rec := recover(); rec != nil {
			if r.Config.Debug {
				r.Logger.Printf("[STREAM_PANIC] Channel %s: Recovered from panic: %v", r.Channel.Name, rec)
			}
		}

		// Mark restreamer as no longer running
		r.Running.Store(false)

		// Reset active connections metric
		metrics.ActiveConnections.WithLabelValues(r.Channel.Name).Set(0)
	}()

	if r.Config.Debug {
		r.Logger.Printf("[STREAM_LOOP_START] Channel %s: Starting streaming loop", r.Channel.Name)
	}

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
		if r.Config.Debug {
			r.Logger.Printf("[STREAM_START] Channel %s: Using manually set stream index %d", r.Channel.Name, currentIndex)
		}
	} else {
		if preferredIndex >= 0 && preferredIndex < streamCount {
			startingIndex = preferredIndex
			if r.Config.Debug {
				r.Logger.Printf("[STREAM_START] Channel %s: Starting with preferred stream index %d", r.Channel.Name, preferredIndex)
			}
		} else {
			startingIndex = 0
			if r.Config.Debug {
				r.Logger.Printf("[STREAM_START] Channel %s: Starting with default stream index 0", r.Channel.Name)
			}
		}
	}

	// Set the current index immediately so other components can read it correctly
	atomic.StoreInt32(&r.CurrentIndex, int32(startingIndex))

	// Retry configuration
	maxTotalAttempts := streamCount * 2      // maximum attempts across streams
	totalAttempts := 0                       // attempts counter
	triedPreferred := false                  // whether the preferred was tried
	consecutiveFailures := make(map[int]int) // map of stream index → consecutive failures

	// Loop until all attempts exhausted
	for totalAttempts < maxTotalAttempts {
		select {
		case <-r.Ctx.Done():
			isManualSwitch := r.ManualSwitch.Load()

			if r.Config.Debug {

				r.Logger.Printf("[STREAM_CONTEXT_CHECK] Channel %s: Context done, manual=%v, attempts=%d/%d", 
					r.Channel.Name, isManualSwitch, totalAttempts, maxTotalAttempts)

				if isManualSwitch {
					r.Logger.Printf("[STREAM_MANUAL_SWITCH] Channel %s: Manual switch initiated", r.Channel.Name)
				} else {
					r.Logger.Printf("[STREAM_CONTEXT_CANCELLED] Channel %s: Context cancelled", r.Channel.Name)
				}
			}

			// Count clients still connected
			clientCount := 0
			r.Clients.Range(func(key string, value *types.RestreamClient) bool {
				clientCount++
				return true
			})

			// still have clients connected
			if clientCount > 0 {
				if r.Config.Debug {
					r.Logger.Printf("[STREAM_RESTART_NEEDED] Channel %s: %d clients still connected, restarting immediately", r.Channel.Name, clientCount)
				}

				// Create fresh context for restart
				r.Ctx, r.Cancel = context.WithCancel(context.Background())

				// Reset running state and restart the streaming loop
				r.Running.Store(false)

				// Brief pause to allow cleanup
				time.Sleep(100 * time.Millisecond)

				// Set running again and continue the outer loop
				r.Running.Store(true)

				// For manual switches, don't reset failure counters or attempt counts
				if !isManualSwitch {

					// Reset attempt counters for fresh start only on real failures
					totalAttempts = 0
					consecutiveFailures = make(map[int]int)
				} else {
					if r.Config.Debug {
						r.Logger.Printf("[STREAM_MANUAL_RESTART] Channel %s: Restarting for manual switch to stream %d", r.Channel.Name, int(atomic.LoadInt32(&r.CurrentIndex)))
					}
				}

				// Continue the main streaming loop
				continue
			}

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
			if r.Config.Debug {
				r.Logger.Printf("[STREAM_NO_CLIENTS] Channel %s: No clients remaining", r.Channel.Name)
			}
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
		if r.Config.Debug {
			r.Logger.Printf("[STREAM_ATTEMPT_DEBUG] Channel %s: Attempting stream %d, manual switch flag: %t",
				r.Channel.Name, currentIdx, r.ManualSwitch.Load())
		}
		success, bytesTransferred := r.StreamFromSource(currentIdx)

		// Check if this was a manual switch AFTER the stream attempt
		wasManualSwitch := r.ManualSwitch.Load()
		if r.Config.Debug {
			r.Logger.Printf("[STREAM_RESULT_DEBUG] Channel %s: Stream %d success: %t, manual switch: %t",
				r.Channel.Name, currentIdx, success, wasManualSwitch)
		}

		// Reset manual switch flag for new stream attempt
		r.ManualSwitch.Store(false)

		// if the stream was successful
		if success {
			
			// Check if this was a very brief success (likely a failure)
			if bytesTransferred < 1024*1024 { // Less than 1MB suggests very brief connection
				consecutiveFailures[currentIdx]++
				if r.Config.Debug {
					r.Logger.Printf("[STREAM_BRIEF_SUCCESS] Channel %s: Stream %d succeeded briefly (%d bytes), treating as failure", r.Channel.Name, currentIdx, bytesTransferred)
				}
				// Don't return, continue to try next stream
			} else {
				// Reset failure count for substantial success
				consecutiveFailures[currentIdx] = 0
				if r.Config.Debug {
					r.Logger.Printf("[STREAM_SUCCESS] Channel %s: Stream %d succeeded with %d bytes, resetting failure count", r.Channel.Name, currentIdx, bytesTransferred)
				}

				// For manual switches, don't return - continue to stream the new index
				if wasManualSwitch {
					r.ManualSwitch.Store(false)
					if r.Config.Debug {
						newIdx := int(atomic.LoadInt32(&r.CurrentIndex))
						r.Logger.Printf("[STREAM_MANUAL_SWITCH_RESTART] Channel %s: Manual switch succeeded, continuing with stream %d", r.Channel.Name, newIdx)
					}

					// Reset attempt counters but keep going
					totalAttempts = 0
					consecutiveFailures = make(map[int]int)

					if r.Config.Debug {
						r.Logger.Printf("[STREAM_CONTINUING] Channel %s: Continuing streaming loop after manual switch", r.Channel.Name)
					}

					continue
				}

				// Check if context was cancelled due to manual switch
				if r.Ctx.Err() != nil {
					isManualSwitch := r.ManualSwitch.Load()
					if isManualSwitch {
						if r.Config.Debug {
							r.Logger.Printf("[STREAM_MANUAL_CONTINUE] Channel %s: Context cancelled due to manual switch, continuing", r.Channel.Name)
						}

						r.ManualSwitch.Store(false)

						select {
						case <-time.After(100 * time.Millisecond):
						case <-r.Ctx.Done():
							return
						}

						continue
					}
				}

				// Only return (exit) for substantial successes
				return
			}
		}

		// If we reach here, either it was a failure or brief success - continue to next stream
		// Increment consecutive failure count
		consecutiveFailures[currentIdx]++

		// debug logging
		if r.Config.Debug {
			r.Logger.Printf("[STREAM_FAILURE] Channel %s: Stream %d failed (consecutive failures: %d)",
				r.Channel.Name, currentIdx, consecutiveFailures[currentIdx])
		}

		// Handle multiple failures → mark stream as bad
		if consecutiveFailures[currentIdx] >= 2 {
			r.Channel.Mu.RLock()
			if currentIdx < len(r.Channel.Streams) {
				currentStream := r.Channel.Streams[currentIdx]
				r.Channel.Mu.RUnlock()

				// Record the failure for monitoring/blocking
				stream.HandleStreamFailure(currentStream, r.Config, r.Logger, r.Channel.Name, currentIdx)

				// debug logging
				if r.Config.Debug {
					r.Logger.Printf("[STREAM_FAILURE_TRACKED] Channel %s: Stream %d failed %d consecutive times, tracked for potential auto-blocking",
						r.Channel.Name, currentIdx, consecutiveFailures[currentIdx])
				}
			} else {
				r.Channel.Mu.RUnlock()
			}
		}

		// Mark preferred as tried
		if currentIdx == preferredIndex && !triedPreferred {
			triedPreferred = true
			if r.Config.Debug {
				r.Logger.Printf("[FALLBACK] Channel %s: Preferred stream %d failed, trying fallback streams", r.Channel.Name, preferredIndex)
			}
		}

		// If multiple streams, rotate index
		if streamCount > 1 {
			newIdx := (currentIdx + 1) % streamCount
			atomic.StoreInt32(&r.CurrentIndex, int32(newIdx))
			if r.Config.Debug {
				r.Logger.Printf("[STREAM_SWITCH] Channel %s: Switching from stream %d to stream %d", r.Channel.Name, currentIdx, newIdx)
			}
		}

		// Sleep briefly before retry
		select {
		case <-r.Ctx.Done():
			isManualSwitch := r.ManualSwitch.Load()
			
			if r.Config.Debug {
				if isManualSwitch {
					r.Logger.Printf("[STREAM_RETRY_MANUAL_SWITCH] Channel %s: Manual switch during retry delay", r.Channel.Name)
				} else {
					r.Logger.Printf("[STREAM_RETRY_CONTEXT_CANCELLED] Channel %s: Context cancelled during retry", r.Channel.Name)
				}
			}
			
			// Count clients
			clientCount := 0
			r.Clients.Range(func(key string, value *types.RestreamClient) bool {
				clientCount++
				return true
			})
			
			// If we have clients, restart the loop
			if clientCount > 0 {
				if r.Config.Debug {
					r.Logger.Printf("[STREAM_RETRY_RESTART] Channel %s: %d clients connected, continuing failover", r.Channel.Name, clientCount)
				}
				
				// Create fresh context
				r.Ctx, r.Cancel = context.WithCancel(context.Background())
				
				// For manual switches, reset the flag
				if isManualSwitch {
					r.ManualSwitch.Store(false)
				}
				
				// Brief pause then continue
				time.Sleep(100 * time.Millisecond)
				continue
			}
			
			return
		case <-time.After(500 * time.Millisecond): // only .5 seconds
		}
		
	}

	// If we reached here, all streams failed
	if r.Config.Debug {
		r.Logger.Printf("[STREAM_EXHAUSTED] Channel %s: All streams failed after %d attempts", r.Channel.Name, totalAttempts)

		// Log final failure counts
		for streamIdx, failures := range consecutiveFailures {
			if failures > 0 {
				r.Logger.Printf("[STREAM_FINAL_FAILURES] Channel %s: Stream %d had %d consecutive failures",
					r.Channel.Name, streamIdx, failures)
			}
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

	if r.Config.Debug {
		r.Logger.Printf("[STREAM_ATTEMPT] Channel %s: Attempting to stream from index %d", r.Channel.Name, index)
		r.Logger.Printf("[FFMPEG_MODE] Channel %s: FFMPEG Mode: %v", r.Channel.Name, r.Config.FFmpegMode)
	}

	// CHECK FFMPEG MODE FIRST - BEFORE ANYTHING ELSE
	if r.Config.FFmpegMode {
		// Get the stream URL for FFmpeg
		r.Channel.Mu.RLock()
		if index >= len(r.Channel.Streams) {
			r.Channel.Mu.RUnlock()
			return false, 0
		}
		streamURL := r.Channel.Streams[index].URL
		r.Channel.Mu.RUnlock()
		
		if r.Config.Debug {
			r.Logger.Printf("[FFMPEG_MODE] Channel %s", r.Channel.Name)
		}
		return r.streamWithFFmpeg(streamURL)
	}

	// Acquire read lock to access the channel’s stream list safely
	r.Channel.Mu.RLock()
	if index >= len(r.Channel.Streams) {

		// If the requested index is invalid, unlock and exit
		r.Channel.Mu.RUnlock()
		return false, 0
	}
	stream := r.Channel.Streams[index]
	r.Channel.Mu.RUnlock()

	// Check if the stream was previously marked as dead, but allow occasional retries
	if deadstreams.IsStreamDead(r.Channel.Name, index) {
		deadReason := deadstreams.GetDeadStreamReason(r.Channel.Name, index)
		if deadReason == "manual" {
			// Always skip manually killed streams
			if r.Config.Debug {
				r.Logger.Printf("[STREAM_SKIP] Channel %s: Stream %d is manually marked dead", r.Channel.Name, index)
			}
			return false, 0
		}
		// For auto-blocked streams, skip most of the time but allow occasional retry
		if r.Config.Debug {
			r.Logger.Printf("[STREAM_SKIP] Channel %s: Stream %d is marked dead (reason: %s)", r.Channel.Name, index, deadReason)
		}
		return false, 0
	}

	// Skip stream if explicitly blocked
	if atomic.LoadInt32(&stream.Blocked) == 1 {
		if r.Config.Debug {
			r.Logger.Printf("[STREAM_SKIP] Channel %s: Stream %d is blocked", r.Channel.Name, index)
		}
		return false, 0
	}

	// Enforce connection limit for this source
	if atomic.LoadInt32(&stream.Source.ActiveConns) >= int32(stream.Source.MaxConnections) {
		if r.Config.Debug {
			r.Logger.Printf("[STREAM_LIMIT] Channel %s: Stream %d source at max connections (%d)", r.Channel.Name, index, stream.Source.MaxConnections)
		}
		return false, 0
	}

	// Increment active connections for the source
	atomic.AddInt32(&stream.Source.ActiveConns, 1)
	defer atomic.AddInt32(&stream.Source.ActiveConns, -1) // ensure decrement when function exits

	// Retrieve variants (single or master playlist)
	variants, isMaster, err := r.getStreamVariants(stream.URL, stream.Source)
	if err != nil {
		if r.Config.Debug {
			r.Logger.Printf("[STREAM_VARIANT_ERROR] Channel %s: Failed to get variants from stream %d: %v", r.Channel.Name, index, err)
		}
		return false, 0
	}

	// If master playlist → try all variants
	if isMaster {
		if r.Config.Debug {
			r.Logger.Printf("[STREAM_MASTER] Channel %s: Master playlist detected with %d variants", r.Channel.Name, len(variants))
		}

		// loop over all viriants to test them
		for i, variant := range variants {
			if r.Config.Debug {
				r.Logger.Printf("[STREAM_MASTER_VARIANT] Channel %s: Testing variant %d (%s)", r.Channel.Name, i, variant.URL)
			}
			if ok, bytes := r.testAndStreamVariant(variant, stream.Source); ok {
				if r.Config.Debug {
					r.Logger.Printf("[STREAM_MASTER_SUCCESS] Channel %s: Successfully streamed variant %d (%s)", r.Channel.Name, i, variant.URL)
				}
				return true, bytes
			}
		}

		// None of the variants succeeded
		if r.Config.Debug {
			r.Logger.Printf("[STREAM_MASTER_FAILURE] Channel %s: All variants failed", r.Channel.Name)
		}
		return false, 0
	}

	return r.streamFromURL(variants[0].URL, stream.Source)
}

// getStreamVariants fetches a stream URL and determines if it is a master playlist.
// Returns:
//   - []parser.StreamVariant: a list of parsed variants
//   - bool: true if master playlist, false if single URL
//   - error: any encountered error
func (r *Restream) getStreamVariants(url string, source *config.SourceConfig) ([]parser.StreamVariant, bool, error) {

	// Get rate limiter from StreamProxy (need to add this to Restreamer)
	if r.RateLimiter != nil {
		r.RateLimiter.Take()
		if r.Config.Debug {
			r.Logger.Printf("[RATE_LIMIT] Applied rate limit for stream request: %s", source.Name)
		}
	}

	// Initialize a master playlist handler
	masterHandler := parser.NewMasterPlaylistHandler(r.Logger, r.Config)

	// Build HTTP GET request for the stream URL
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, false, err
	}

	// Apply a timeout context for safety (15s)
	checkCtx, cancel := context.WithTimeout(r.Ctx, 15*time.Second)
	defer cancel()
	req = req.WithContext(checkCtx)

	// Execute HTTP request with custom headers from the source
	resp, err := r.HttpClient.DoWithHeaders(req, source.UserAgent, source.ReqOrigin, source.ReqReferrer)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()

	// Non-200 response codes are considered fatal
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("HTTP %d response", resp.StatusCode)
	}

	// Decide whether to check the body as a potential master playlist
	if !r.shouldCheckForMasterPlaylist(resp) {

		// If not, return single variant
		return []parser.StreamVariant{{URL: url, Resolution: "unknown"}}, false, nil
	}

	// Read the entire body for playlist parsing
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, false, err
	}

	// Parse the body as a master playlist and return variants
	return masterHandler.ProcessMasterPlaylistVariants(string(body), url, r.Channel.Name)
}

// testAndStreamVariant attempts to validate and stream from a variant URL.
// - It fetches the variant and checks the first chunk of data.
// - If the data resembles an HLS playlist (#EXTINF markers), it streams HLS segments.
// - Otherwise, it streams directly from the variant URL.
// Returns:
//   - bool: success flag
//   - int64: number of bytes streamed
func (r *Restream) testAndStreamVariant(variant parser.StreamVariant, source *config.SourceConfig) (bool, int64) {

	// Use FFmpeg if enabled, bypassing all variant testing
	if r.Config.FFmpegMode {
		return r.streamWithFFmpeg(variant.URL)
	}

	// Build HTTP GET request for the variant
	testReq, err := http.NewRequest("GET", variant.URL, nil)
	if err != nil {
		return false, 0
	}

	// Apply 10-second timeout for initial validation
	testCtx, cancel := context.WithTimeout(r.Ctx, 10*time.Second)
	defer cancel()
	testReq = testReq.WithContext(testCtx)

	// Execute the request
	resp, err := r.HttpClient.DoWithHeaders(testReq, source.UserAgent, source.ReqOrigin, source.ReqReferrer)
	if err != nil {
		return false, 0
	}
	defer resp.Body.Close()

	// Reject if status code is not OK
	if resp.StatusCode != http.StatusOK {
		return false, 0
	}

	// Read a small buffer to validate the response content
	testBuffer := make([]byte, 8192) // 8 KB peek
	n, err := resp.Body.Read(testBuffer)
	if err != nil && err != io.EOF {
		return false, 0
	}
	if n == 0 {
		return false, 0
	}

	// Convert to string for content inspection
	content := string(testBuffer[:n])

	// If this looks like an HLS playlist (contains EXTINF tags)
	if strings.Contains(content, "#EXTINF") {
		// Use HLS segment streaming method
		return r.streamHLSSegments(variant.URL)
	}

	// Otherwise, stream directly from the URL
	return r.streamFromURL(variant.URL, source)
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

	// Check content-type header
	if strings.Contains(strings.ToLower(contentType), "mpegurl") ||
		strings.Contains(strings.ToLower(contentType), "m3u8") {
		return true
	}

	// If length is very small, it’s likely a playlist
	if contentLength != "" {
		if length, err := strconv.ParseInt(contentLength, 10, 64); err == nil {
			if length > 0 && length < 100*1024 { // under 100 KB
				return true
			}
		}
	}

	return false
}

// streamFromURL handles the main streaming loop for a single direct URL.
// It continuously reads data from the source and distributes it to clients
// until cancelled, an error occurs, or clients disconnect.
//
// Parameters:
//   - url: the stream URL to fetch
//   - source: the source configuration (headers, limits, etc.)
//
// Returns:
//   - bool: success flag
//   - int64: total number of bytes streamed
func (r *Restream) streamFromURL(url string, source *config.SourceConfig) (bool, int64) {
	if r.RateLimiter != nil {
		r.RateLimiter.Take()
	}

	if r.Config.FFmpegMode {
		return r.streamWithFFmpeg(url)
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		if r.Config.Debug {
			r.Logger.Printf("[STREAM_REQUEST_ERROR] Channel %s: Failed to create request: %v", r.Channel.Name, err)
		}
		return false, 0
	}

	req = req.WithContext(r.Ctx)
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Accept", "*/*")

	resp, err := r.HttpClient.DoWithHeaders(req, source.UserAgent, source.ReqOrigin, source.ReqReferrer)
	if err != nil {
		if r.Config.Debug {
			r.Logger.Printf("[STREAM_CONNECT_ERROR] Channel %s: %v", r.Channel.Name, err)
		}
		return false, 0
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if r.Config.Debug {
			r.Logger.Printf("[STREAM_HTTP_ERROR] Channel %s: HTTP %d", r.Channel.Name, resp.StatusCode)
		}
		return false, 0
	}

	var totalBytes int64
	// Create a buffer pool instance and use it properly
    bufferPool := bbuffer.NewBufferPool(32 * 1024)
    buf := bufferPool.Get()
    defer bufferPool.Put(buf)
	lastActivityUpdate := time.Now()
	lastMetricUpdate := time.Now()
	consecutiveErrors := 0
	maxConsecutiveErrors := 5

	for {
		select {
		case <-r.Ctx.Done():
			if r.ManualSwitch.Load() {
				if r.Config.Debug {
					r.Logger.Printf("[STREAM_MANUAL_SWITCH] Channel %s: Graceful switch", r.Channel.Name)
				}
				return true, totalBytes
			}
			return totalBytes > 1024*1024, totalBytes
		default:
		}

		n, err := resp.Body.Read(buf)
		if n > 0 {
			chunk := buf[:n]

			if !r.SafeBufferWrite(chunk) {
				consecutiveErrors++
				if consecutiveErrors >= maxConsecutiveErrors {
					if r.Config.Debug {
						r.Logger.Printf("[STREAM_BUFFER_ERROR] Channel %s: Buffer write failed %d times", r.Channel.Name, consecutiveErrors)
					}
					return false, totalBytes
				}
				time.Sleep(10 * time.Millisecond)
				continue
			}

			consecutiveErrors = 0
			activeClients := r.DistributeToClients(chunk)
			if activeClients == 0 {
				if r.Config.Debug {
					r.Logger.Printf("[STREAM_NO_CLIENTS] Channel %s: No active clients", r.Channel.Name)
				}
				return totalBytes > 1024*1024, totalBytes
			}

			totalBytes += int64(n)

			now := time.Now()
			if now.Sub(lastActivityUpdate) > 5*time.Second {
				r.LastActivity.Store(now.Unix())
				lastActivityUpdate = now
			}

			if now.Sub(lastMetricUpdate) > 10*time.Second {
				metrics.BytesTransferred.WithLabelValues(r.Channel.Name, "downstream").Add(float64(n))
				metrics.ActiveConnections.WithLabelValues(r.Channel.Name).Set(float64(activeClients))
				lastMetricUpdate = now
			}
		}

		if err != nil {
			if err == io.EOF {
				success := totalBytes > 2*1024*1024
				if r.Config.Debug {
					status := "insufficient"
					if success {
						status = "success"
					}
					r.Logger.Printf("[STREAM_EOF] Channel %s: Stream ended (%s, %d bytes)", r.Channel.Name, status, totalBytes)
				}
				return success, totalBytes
			}

			if r.Ctx.Err() != nil && r.ManualSwitch.Load() {
				return true, totalBytes
			}

			consecutiveErrors++
			if consecutiveErrors >= maxConsecutiveErrors {
				if r.Config.Debug {
					r.Logger.Printf("[STREAM_READ_ERROR] Channel %s: %v (consecutive: %d)", r.Channel.Name, err, consecutiveErrors)
				}
				return false, totalBytes
			}

			time.Sleep(100 * time.Millisecond)
			continue
		}

		consecutiveErrors = 0
	}
}

// DistributeToClients sends a chunk of stream data to all active clients.
// Clients that fail to receive data are removed.
//
// Parameters:
//   - data: TS/HLS chunk of stream data
//
// Returns:
//   - int: number of active clients remaining after distribution
//
// DistributeToClients sends a chunk of stream data to all active clients.
// Clients that fail to receive data are removed.
//
// Parameters:
//   - data: TS/HLS chunk of stream data
//
// Returns:
//   - int: number of active clients remaining after distribution
func (r *Restream) DistributeToClients(data []byte) int {
	activeClients := 0
	var failedClients []string

	r.Clients.Range(func(key string, value *types.RestreamClient) bool {
		client := value
		clientID := key

		_, err := client.Writer.Write(data)
		if err != nil {
			failedClients = append(failedClients, clientID)
			return true
		}

		client.Flusher.Flush()
		client.LastSeen.Store(time.Now().Unix())
		activeClients++
		return true
	})

	for _, clientID := range failedClients {
		if r.Config.Debug {
			r.Logger.Printf("[CLIENT_WRITE_ERROR] Channel %s: Removing failed client %s", r.Channel.Name, clientID)
		}
		r.RemoveClient(clientID)
	}

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
	case <-r.Ctx.Done():
		if r.ManualSwitch.Load() {
			if r.Config.Debug {
				r.Logger.Printf("[BUFFER_MANUAL_SWITCH] Channel %s: Buffer write during manual switch, allowing success", r.Channel.Name)
			}
			return true // Don't treat manual switch cancellation as buffer failure
		}
		return false
	default:
	}

	// Check buffer validity with nil check
	if r.Buffer == nil || r.Buffer.IsDestroyed() {
		return false
	}

	// Perform write into ring buffer
	r.Buffer.Write(data)
	return true
}

// monitor client health
func (r *Restream) monitorClientHealth() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Ctx.Done():
			return
		case <-ticker.C:
			if !r.Running.Load() {
				return
			}

			now := time.Now().Unix()
			var staleClients []string

			r.Clients.Range(func(key string, value *types.RestreamClient) bool {
				client := value
				lastSeen := client.LastSeen.Load()

				if now-lastSeen > 300 {
					staleClients = append(staleClients, key)
				}
				return true
			})

			for _, clientID := range staleClients {
				if r.Config.Debug {
					r.Logger.Printf("[CLIENT_HEALTH] Removing stale client: %s", clientID)
				}
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
	if r.Config.Debug {
		r.Logger.Printf("[FORCE_SWITCH] Channel %s: Switching to stream %d", r.Channel.Name, newIndex)
	}

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

	if r.Config.Debug {
		r.Logger.Printf("[FORCE_SWITCH] Channel %s: Forcing switch to stream %d with %d clients", r.Channel.Name, newIndex, clientCount)
	}

	// Mark this as a manual switch so context cancellation won't be treated as failure
	r.ManualSwitch.Store(true)

	// Cancel current context to trigger restart with new stream index
	r.Cancel()
}

// resetBufferSafely resets the buffer while preserving client connections
func (r *Restream) resetBufferSafely() {
	if r.Buffer != nil && !r.Buffer.IsDestroyed() {
		r.Buffer.Reset()
		if r.Config.Debug {
			r.Logger.Printf("[BUFFER_RESET_SAFE] Channel %s: Buffer reset", r.Channel.Name)
		}
	} else {
		// Use BufferSizePerStream instead of MaxBufferSize
		bufferSize := r.Config.BufferSizePerStream * 1024 * 1024
		r.Buffer = bbuffer.NewRingBuffer(bufferSize)
		if r.Config.Debug {
			r.Logger.Printf("[BUFFER_RECREATED] Channel %s: New buffer created (%d MB)", r.Channel.Name, r.Config.BufferSizePerStream)
		}
	}
}

// trackStreamStart records when a stream begins for duration tracking
func (r *Restream) trackStreamStart() time.Time {
	return time.Now()
}

// streamFallbackVideo streams the offline video in a loop when all streams fail
func (r *Restream) streamFallbackVideo() {
	fallbackURL := "https://cdn.kcp.im/tv/loading.mkv"
	
	if r.Config.Debug {
		r.Logger.Printf("[FALLBACK] Channel %s: Starting fallback video loop", r.Channel.Name)
	}

	for {
		select {
		case <-r.Ctx.Done():
			if r.Config.Debug {
				r.Logger.Printf("[FALLBACK] Channel %s: Context cancelled", r.Channel.Name)
			}
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
			if r.Config.Debug {
				r.Logger.Printf("[FALLBACK] Channel %s: No clients remaining", r.Channel.Name)
			}
			return
		}

		if r.Config.Debug {
			r.Logger.Printf("[FALLBACK] Channel %s: Starting fallback video playback for %d clients", r.Channel.Name, clientCount)
		}

		// Stream the fallback video
		r.streamSingleFallbackLoop(fallbackURL)

		// Brief pause before restarting loop
		select {
		case <-r.Ctx.Done():
			return
		case <-time.After(1 * time.Second):
			continue
		}
	}
}

// streamSingleFallbackLoop streams the fallback video once
func (r *Restream) streamSingleFallbackLoop(url string) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return
	}

	req = req.WithContext(r.Ctx)

	resp, err := r.HttpClient.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return
	}

	buf := make([]byte, 32*1024)
	lastActivityUpdate := time.Now()

	for {
		select {
		case <-r.Ctx.Done():
			return
		default:
		}

		n, err := resp.Body.Read(buf)
		if n > 0 {
			chunk := buf[:n]

			if !r.SafeBufferWrite(chunk) {
				return
			}

			activeClients := r.DistributeToClients(chunk)
			if activeClients == 0 {
				return
			}

			// Update activity timestamp periodically
			now := time.Now()
			if now.Sub(lastActivityUpdate) > 10*time.Second {
				r.LastActivity.Store(now.Unix())
				lastActivityUpdate = now
			}
		}

		if err != nil {
			if err == io.EOF {
				// Video finished, return to restart loop
				return
			}
			return
		}
	}
}