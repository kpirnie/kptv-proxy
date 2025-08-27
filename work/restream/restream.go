package restream

import (
	"context"
	"fmt"
	"io"
	"kptv-proxy/work/buffer"
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

	// Create a cancellable context for the restream lifecycle
	ctx, cancel := context.WithCancel(context.Background())

	// Initialize the base Restreamer struct with all dependencies
	base := &types.Restreamer{
		Channel:     channel,
		Buffer:      buffer.NewRingBuffer(bufferSize), // initialize ring buffer for stream data
		Ctx:         ctx,                              // streaming context
		Cancel:      cancel,                           // cancel func
		Logger:      logger,
		HttpClient:  httpClient,
		Config:      cfg,
		RateLimiter: rateLimiter,
	}

	// Set initial timestamps and flags
	base.LastActivity.Store(time.Now().Unix()) // record start activity time
	base.Running.Store(false)                  // initially not running

	// return the restream
	return &Restream{base}
}

// AddClient registers a new client to receive stream data.
// - id: unique identifier for the client
// - w: the HTTP response writer
// - flusher: the HTTP flusher to push data immediately
func (r *Restream) AddClient(id string, w http.ResponseWriter, flusher http.Flusher) {

	// Create a new restream client object
	client := &types.RestreamClient{
		Id:      id,              // unique identifier for the client
		Writer:  w,               // HTTP response writer for TS data
		Flusher: flusher,         // HTTP flusher for real-time streaming
		Done:    make(chan bool), // channel to signal when client is closed
	}

	// Store the last seen time as current time
	client.LastSeen.Store(time.Now().Unix())

	// Add client to the restreamer's active clients map
	r.Clients.Store(id, client)

	// Update last activity timestamp for the restreamer
	r.LastActivity.Store(time.Now().Unix())

	// Count active clients for metrics
	count := 0
	r.Clients.Range(func(_, _ interface{}) bool {
		count++
		return true
	})

	// Update Prometheus metric for connected clients
	metrics.ClientsConnected.WithLabelValues(r.Channel.Name).Set(float64(count))

	// Debug logging
	if r.Config.Debug {
		r.Logger.Printf("[CLIENT_CONNECT] Channel %s: ID: %s", r.Channel.Name, id)
		r.Logger.Printf("[METRIC] clients_connected: %d [%s]", count, r.Channel.Name)
		r.Logger.Printf("[CLIENT_ADD] Channel %s: Client %s added, total: %d", r.Channel.Name, id, count)
	}

	// Start restreaming if it isn't already running
	if !r.Running.Load() && r.Running.CompareAndSwap(false, true) {
		if r.Config.Debug {
			r.Logger.Printf("[RESTREAM_START] Channel %s: Starting goroutine", r.Channel.Name)
			r.Logger.Printf("[CLIENT_READY] Channel %s: Client %s waiting", r.Channel.Name, id)
			r.Logger.Printf("[RESTREAM_ACTIVE] Channel %s: Restreamer started", r.Channel.Name)
		}

		// Start the streaming loop in a goroutine
		go r.Stream()
	}
}

// RemoveClient unregisters a client from the restreamer.
// - id: unique identifier for the client to be removed
func (r *Restream) RemoveClient(id string) {

	// Attempt to load and delete the client from the map
	if client, ok := r.Clients.LoadAndDelete(id); ok {

		// hold the client
		c := client.(*types.RestreamClient)

		// Mark client as finished by closing its Done channel
		select {
		case <-c.Done:
			// Already closed
		default:
			close(c.Done)
		}

		// Remove the client from the buffer’s internal tracking
		r.Buffer.RemoveClient(id)

		// Count remaining clients
		count := 0
		r.Clients.Range(func(_, _ interface{}) bool {
			count++
			return true
		})

		// Update Prometheus metrics for clients
		metrics.ClientsConnected.WithLabelValues(r.Channel.Name).Set(float64(count))

		// Debug logging
		if r.Config.Debug {
			r.Logger.Printf("[CLIENT_DISCONNECT] Channel %s: Client: %s", r.Channel.Name, id)
			r.Logger.Printf("[METRIC] clients_connected: %d [%s]", count, r.Channel.Name)
			r.Logger.Printf("[CLIENT_REMOVE] Channel %s: Client %s removed, remaining: %d", r.Channel.Name, id, count)
		}

		// If no clients remain, stop the stream immediately
		if count == 0 {
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

	// Decide starting index:
	// - If current index matches preferred, keep it
	// - Otherwise reset to preferred or fallback to 0
	if currentIndex >= 0 && currentIndex < streamCount && currentIndex == preferredIndex {
		if r.Config.Debug {
			r.Logger.Printf("[STREAM_START] Channel %s: Using manually set stream index %d", r.Channel.Name, currentIndex)
		}
	} else {
		if preferredIndex >= 0 && preferredIndex < streamCount {
			atomic.StoreInt32(&r.CurrentIndex, int32(preferredIndex))
			if r.Config.Debug {
				r.Logger.Printf("[STREAM_START] Channel %s: Starting with preferred stream index %d", r.Channel.Name, preferredIndex)
			}
		} else {
			atomic.StoreInt32(&r.CurrentIndex, 0)
			if r.Config.Debug {
				r.Logger.Printf("[STREAM_START] Channel %s: Starting with default stream index 0", r.Channel.Name)
			}
		}
	}

	// Retry configuration
	maxTotalAttempts := streamCount * 2      // maximum attempts across streams
	totalAttempts := 0                       // attempts counter
	triedPreferred := false                  // whether the preferred was tried
	consecutiveFailures := make(map[int]int) // map of stream index → consecutive failures

	// Loop until all attempts exhausted
	for totalAttempts < maxTotalAttempts {
		select {
		case <-r.Ctx.Done():

			// Context cancelled → check if restart is needed
			if r.Config.Debug {
				r.Logger.Printf("[STREAM_CONTEXT_CANCELLED] Channel %s: Context cancelled, checking for restart", r.Channel.Name)
			}

			// Count clients still connected
			clientCount := 0
			r.Clients.Range(func(_, _ interface{}) bool {
				clientCount++
				return true
			})
			if clientCount > 0 {
				if r.Config.Debug {
					r.Logger.Printf("[STREAM_RESTART_NEEDED] Channel %s: %d clients still connected, allowing restart", r.Channel.Name, clientCount)
				}

				// Mark as not running so that AddClient may restart it
				r.Running.Store(false)
			}
			return
		default:
		}

		// Count active clients
		clientCount := 0
		r.Clients.Range(func(_, _ interface{}) bool {
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

		// Reset buffer for new attempt
		if r.Buffer != nil && !r.Buffer.IsDestroyed() {
			r.Buffer.Reset()
		} else {
			bufferSize := r.Config.MaxBufferSize * 1024 * 1024
			r.Buffer = buffer.NewRingBuffer(bufferSize)
		}

		// Attempt to stream from source
		success, _ := r.StreamFromSource(currentIdx)

		if success {

			// Reset failure count for successful stream
			consecutiveFailures[currentIdx] = 0
			if r.Config.Debug {
				r.Logger.Printf("[STREAM_SUCCESS] Channel %s: Stream %d succeeded, resetting failure count", r.Channel.Name, currentIdx)
			}
			return
		}

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
			if r.Config.Debug {
				r.Logger.Printf("[STREAM_CONTEXT_CANCELLED_LOOP] Channel %s: Context cancelled during retry", r.Channel.Name)
			}
			return
		case <-time.After(2 * time.Second):
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

	// Acquire read lock to access the channel’s stream list safely
	r.Channel.Mu.RLock()
	if index >= len(r.Channel.Streams) {

		// If the requested index is invalid, unlock and exit
		r.Channel.Mu.RUnlock()
		return false, 0
	}
	stream := r.Channel.Streams[index]
	r.Channel.Mu.RUnlock()

	// Check if the stream was previously marked as dead
	if deadstreams.IsStreamDead(r.Channel.Name, index) {
		if r.Config.Debug {
			r.Logger.Printf("[STREAM_SKIP] Channel %s: Stream %d is marked dead", r.Channel.Name, index)
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

	// If single URL, stream directly
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

	// Apply rate limiting before streaming request
	if r.RateLimiter != nil {
		r.RateLimiter.Take()
	}

	// Construct HTTP request for the stream URL
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		if r.Config.Debug {
			r.Logger.Printf("[STREAM_REQUEST_ERROR] Channel %s: Failed to create request for %s: %v",
				r.Channel.Name, url, err)
		}
		return false, 0
	}

	// Attach cancellable context to the request
	req = req.WithContext(r.Ctx)

	// Execute HTTP request with source-specific headers
	resp, err := r.HttpClient.DoWithHeaders(req, source.UserAgent, source.ReqOrigin, source.ReqReferrer)
	if err != nil {
		if r.Config.Debug {
			r.Logger.Printf("[STREAM_CONNECT_ERROR] Channel %s: Failed to connect to %s: %v",
				r.Channel.Name, url, err)
		}
		return false, 0
	}
	defer resp.Body.Close()

	// Reject on bad HTTP status
	if resp.StatusCode != http.StatusOK {
		if r.Config.Debug {
			r.Logger.Printf("[STREAM_HTTP_ERROR] Channel %s: %s returned HTTP %d",
				r.Channel.Name, url, resp.StatusCode)
		}
		return false, 0
	}

	// Track total bytes streamed
	var totalBytes int64

	// Allocate reusable buffer
	buf := make([]byte, 32*1024) // 32 KB chunks

	// fire up a loop
	for {

		// Check if streaming context was cancelled
		select {
		case <-r.Ctx.Done():
			if r.Config.Debug {
				r.Logger.Printf("[STREAM_CANCELLED] Channel %s: Context cancelled during stream", r.Channel.Name)
			}
			return true, totalBytes
		default:
		}

		// Read from the HTTP body
		n, err := resp.Body.Read(buf)
		if n > 0 {

			// Slice to only include actual bytes read
			chunk := buf[:n]

			// Write into buffer safely
			if !r.SafeBufferWrite(chunk) {
				if r.Config.Debug {
					r.Logger.Printf("[STREAM_BUFFER_ERROR] Channel %s: Buffer write failed", r.Channel.Name)
				}
				return false, totalBytes
			}

			// Distribute chunk to all active clients
			activeClients := r.DistributeToClients(chunk)

			// Update byte counter
			totalBytes += int64(n)

			// Update Prometheus metrics
			metrics.BytesTransferred.WithLabelValues(r.Channel.Name).Add(float64(n))
			metrics.ActiveConnections.WithLabelValues(r.Channel.Name).Set(float64(activeClients))

			// Debug log every 1 MB
			if r.Config.Debug && totalBytes%(1024*1024) < int64(n) {
				r.Logger.Printf("[STREAM_PROGRESS] Channel %s: Streamed %d MB",
					r.Channel.Name, totalBytes/(1024*1024))
			}
		}

		// Handle read errors
		if err != nil {

			// EOF → end of stream gracefully
			if err == io.EOF {
				if r.Config.Debug {
					r.Logger.Printf("[STREAM_EOF] Channel %s: Source ended", r.Channel.Name)
				}
				return true, totalBytes
			}

			// Any other error → failure
			if r.Config.Debug {
				r.Logger.Printf("[STREAM_READ_ERROR] Channel %s: %v", r.Channel.Name, err)
			}
			return false, totalBytes
		}
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
func (r *Restream) DistributeToClients(data []byte) int {

	// initial active clients
	activeClients := 0

	// Iterate over all connected clients
	r.Clients.Range(func(key, value interface{}) bool {
		client := value.(*types.RestreamClient)

		// Attempt to write data to client
		_, err := client.Writer.Write(data)
		if err != nil {

			// Remove failed client
			if r.Config.Debug {
				r.Logger.Printf("[CLIENT_WRITE_ERROR] Channel %s: Client %s write failed: %v",
					r.Channel.Name, client.Id, err)
			}
			r.RemoveClient(client.Id)
			return true
		}

		// Flush data immediately
		client.Flusher.Flush()

		// Update last seen timestamp
		client.LastSeen.Store(time.Now().Unix())

		// Count client as active
		activeClients++

		return true
	})

	// return the active clients
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

	// Abort if context cancelled
	select {
	case <-r.Ctx.Done():
		return false
	default:
	}

	// Check buffer validity
	if r.Buffer == nil || r.Buffer.IsDestroyed() {
		return false
	}

	// Perform write into ring buffer
	r.Buffer.Write(data)
	return true
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
