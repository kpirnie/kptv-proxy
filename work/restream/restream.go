// work/restream/restream.go - Updated with variant testing
package restream

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"kptv-proxy/work/buffer"
	"kptv-proxy/work/client"
	"kptv-proxy/work/config"
	"kptv-proxy/work/metrics"
	"kptv-proxy/work/parser"
	"kptv-proxy/work/types"
	"kptv-proxy/work/utils"
	"log"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// Restream wraps types.Restreamer to allow adding methods in this package.
type Restream struct {
	*types.Restreamer
}

// setup the restreamer
func NewRestreamer(channel *types.Channel, bufferSize int64, logger *log.Logger, httpClient *client.HeaderSettingClient, cfg *config.Config) *Restream {
	ctx, cancel := context.WithCancel(context.Background())
	base := &types.Restreamer{
		Channel:    channel,
		Buffer:     buffer.NewRingBuffer(bufferSize),
		Ctx:        ctx,
		Cancel:     cancel,
		Logger:     logger,
		HttpClient: httpClient,
		Config:     cfg,
	}
	base.LastActivity.Store(time.Now().Unix())
	base.Running.Store(false)
	return &Restream{base}
}

// setup the client
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

	// Update metrics
	count := 0
	r.Clients.Range(func(_, _ interface{}) bool {
		count++
		return true
	})
	metrics.ClientsConnected.WithLabelValues(r.Channel.Name).Set(float64(count))
	if r.Config.Debug {
		r.Logger.Printf("[CLIENT_CONNECT] Channel %s: ID: %s", r.Channel.Name, id)
		r.Logger.Printf("[METRIC] clients_connected: %d [%s]", count, r.Channel.Name)
		r.Logger.Printf("[CLIENT_ADD] Channel %s: Client %s added, total: %d", r.Channel.Name, id, count)
	}

	// Start restreaming if not running
	if !r.Running.Load() && r.Running.CompareAndSwap(false, true) {
		if r.Config.Debug {
			r.Logger.Printf("[RESTREAM_START] Channel %s: Starting goroutine", r.Channel.Name)
			r.Logger.Printf("[CLIENT_READY] Channel %s: Client %s waiting", r.Channel.Name, id)
			r.Logger.Printf("[RESTREAM_ACTIVE] Channel %s: Restreamer started", r.Channel.Name)
		}

		go r.Stream()
	}
}

// all set? remove the client
func (r *Restream) RemoveClient(id string) {
	if client, ok := r.Clients.LoadAndDelete(id); ok {
		c := client.(*types.RestreamClient)

		// Signal client is done
		select {
		case <-c.Done:
			// Already closed
		default:
			close(c.Done)
		}

		r.Buffer.RemoveClient(id)

		// Update metrics
		count := 0
		r.Clients.Range(func(_, _ interface{}) bool {
			count++
			return true
		})
		metrics.ClientsConnected.WithLabelValues(r.Channel.Name).Set(float64(count))

		if r.Config.Debug {
			r.Logger.Printf("[CLIENT_DISCONNECT] Channel %s: Client: %s", r.Channel.Name, id)
			r.Logger.Printf("[METRIC] clients_connected: %d [%s]", count, r.Channel.Name)
			r.Logger.Printf("[CLIENT_REMOVE] Channel %s: Client %s removed, remaining: %d", r.Channel.Name, id, count)
		}

		// Stop restreaming immediately if no clients
		if count == 0 {
			if r.Config.Debug {
				r.Logger.Printf("[RESTREAM_STOP] Channel %s: No more clients", r.Channel.Name)
			}

			r.stopStream()
		}
	}
}

// force the stream to stop
func (r *Restream) stopStream() {
	if r.Running.CompareAndSwap(true, false) {
		r.Cancel()

		if r.Buffer != nil && !r.Buffer.IsDestroyed() {
			r.Buffer.Destroy()
		}
		r.Buffer = nil

		r.Ctx, r.Cancel = context.WithCancel(context.Background())
		atomic.StoreInt32(&r.CurrentIndex, 0)

		runtime.GC()
	}
}

// stream it!
func (r *Restream) Stream() {
	defer func() {
		if rec := recover(); rec != nil {
			if r.Config.Debug {
				r.Logger.Printf("[STREAM_PANIC] Channel %s: Recovered from panic: %v", r.Channel.Name, rec)
			}
		}
		r.Running.Store(false)
		metrics.ActiveConnections.WithLabelValues(r.Channel.Name).Set(0)
	}()

	r.Channel.Mu.RLock()
	streamCount := len(r.Channel.Streams)
	r.Channel.Mu.RUnlock()

	if streamCount == 0 {
		return
	}

	// Check if current index is already set (from manual stream change)
	currentIndex := int(atomic.LoadInt32(&r.CurrentIndex))
	preferredIndex := int(atomic.LoadInt32(&r.Channel.PreferredStreamIndex))

	// If current index is valid and matches preferred, use it
	if currentIndex >= 0 && currentIndex < streamCount && currentIndex == preferredIndex {
		if r.Config.Debug {
			r.Logger.Printf("[STREAM_START] Channel %s: Using manually set stream index %d", r.Channel.Name, currentIndex)
		}
	} else {
		// Otherwise, start with preferred stream index
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

	maxTotalAttempts := streamCount * 2
	totalAttempts := 0
	triedPreferred := false

	for totalAttempts < maxTotalAttempts {
		select {
		case <-r.Ctx.Done():
			if r.Config.Debug {
				r.Logger.Printf("[STREAM_CONTEXT_CANCELLED] Channel %s: Context cancelled, checking for restart", r.Channel.Name)
			}
			// Check if we still have clients and should restart
			clientCount := 0
			r.Clients.Range(func(_, _ interface{}) bool {
				clientCount++
				return true
			})
			if clientCount > 0 {
				if r.Config.Debug {
					r.Logger.Printf("[STREAM_RESTART_NEEDED] Channel %s: %d clients still connected, allowing restart", r.Channel.Name, clientCount)
				}
				r.Running.Store(false) // Allow restart
			}
			return
		default:
		}

		clientCount := 0
		r.Clients.Range(func(_, _ interface{}) bool {
			clientCount++
			return true
		})

		if clientCount == 0 {
			if r.Config.Debug {
				r.Logger.Printf("[STREAM_NO_CLIENTS] Channel %s: No clients remaining", r.Channel.Name)
			}
			return
		}

		currentIdx := int(atomic.LoadInt32(&r.CurrentIndex))
		totalAttempts++

		if r.Buffer != nil && !r.Buffer.IsDestroyed() {
			r.Buffer.Reset()
		} else {
			bufferSize := r.Config.MaxBufferSize * 1024 * 1024
			r.Buffer = buffer.NewRingBuffer(bufferSize)
		}

		success, _ := r.StreamFromSource(currentIdx)

		if success {
			return
		}

		// If preferred stream failed and we haven't tried fallbacks yet
		if currentIdx == preferredIndex && !triedPreferred {
			triedPreferred = true
			if r.Config.Debug {
				r.Logger.Printf("[FALLBACK] Channel %s: Preferred stream %d failed, trying fallback streams", r.Channel.Name, preferredIndex)
			}
		}

		if streamCount > 1 {
			newIdx := (currentIdx + 1) % streamCount
			atomic.StoreInt32(&r.CurrentIndex, int32(newIdx))
		}

		select {
		case <-r.Ctx.Done():
			if r.Config.Debug {
				r.Logger.Printf("[STREAM_CONTEXT_CANCELLED_LOOP] Channel %s: Context cancelled during retry", r.Channel.Name)
			}
			return
		case <-time.After(2 * time.Second):
		}
	}

	if r.Config.Debug {
		r.Logger.Printf("[STREAM_EXHAUSTED] Channel %s: All streams failed after %d attempts", r.Channel.Name, totalAttempts)
	}
}

// stream direct
func (r *Restream) StreamFromSource(index int) (bool, int64) {
	r.Channel.Mu.RLock()
	if index >= len(r.Channel.Streams) {
		r.Channel.Mu.RUnlock()
		if r.Config.Debug {
			r.Logger.Printf("Invalid stream index %d for channel: %s", index, r.Channel.Name)
		}
		return false, 0
	}
	stream := r.Channel.Streams[index]
	r.Channel.Mu.RUnlock()

	// Check if stream is dead
	if isStreamDead(r.Channel.Name, index) {
		if r.Config.Debug {
			r.Logger.Printf("[STREAM_DEAD] Channel %s: Stream %d is marked as dead, skipping", r.Channel.Name, index)
		}
		return false, 0
	}

	// ADD DEBUG LOGGING TO SHOW STREAM SELECTION
	if r.Config.Debug {
		r.Logger.Printf("[STREAM_SELECT] Channel %s: Attempting stream %d/%d - Source order=%d, %s=%s, URL=%s",
			r.Channel.Name, index+1, len(r.Channel.Streams),
			stream.Source.Order, r.Config.SortField, stream.Attributes[r.Config.SortField],
			utils.LogURL(r.Config, stream.URL))
	}

	// Check if stream is blocked
	if atomic.LoadInt32(&stream.Blocked) == 1 {
		if r.Config.Debug {
			r.Logger.Printf("Stream blocked for channel %s: %s", r.Channel.Name, utils.LogURL(r.Config, stream.URL))
		}
		return false, 0
	}

	// CRITICAL: Check connection limit BEFORE making request
	currentConns := atomic.LoadInt32(&stream.Source.ActiveConns)
	maxConns := int32(stream.Source.MaxConnections)

	if currentConns >= maxConns {
		if r.Config.Debug {
			r.Logger.Printf("[CONNECTION_LIMIT] Channel %s: Source at limit (%d/%d): %s",
				r.Channel.Name, currentConns, maxConns, utils.LogURL(r.Config, stream.URL))
		}
		return false, 0
	}

	// Increment connection count BEFORE making request
	newConns := atomic.AddInt32(&stream.Source.ActiveConns, 1)
	if r.Config.Debug {
		r.Logger.Printf("[CONNECTION_ACQUIRE] Channel %s: Using connection %d/%d for %s",
			r.Channel.Name, newConns, maxConns, utils.LogURL(r.Config, stream.URL))
	}

	// CRITICAL: Always decrement connection count when done
	defer func() {
		remainingConns := atomic.AddInt32(&stream.Source.ActiveConns, -1)
		if r.Config.Debug {
			r.Logger.Printf("[CONNECTION_RELEASE] Channel %s: Released connection, remaining: %d/%d",
				r.Channel.Name, remainingConns, maxConns)
		}
	}()

	// Get all available variants for this stream
	variants, isMaster, err := r.getStreamVariants(stream.URL, stream.Source)
	if err != nil {
		if r.Config.Debug {
			r.Logger.Printf("[VARIANT_ERROR] Channel %s: Error getting variants: %v", r.Channel.Name, err)
		}
		return false, 0
	}

	if isMaster {
		if r.Config.Debug {
			r.Logger.Printf("[VARIANT_TEST] Channel %s: Testing %d variants from highest to lowest quality",
				r.Channel.Name, len(variants))
		}

		// Try each variant from highest to lowest quality
		for i, variant := range variants {
			if r.Config.Debug {
				r.Logger.Printf("[VARIANT_TRY] Channel %s: Trying variant %d/%d - %s (%d kbps)",
					r.Channel.Name, i+1, len(variants), variant.Resolution, variant.Bandwidth/1000)
			}

			success, bytes := r.testAndStreamVariant(variant, stream.Source)
			if success {
				if r.Config.Debug {
					r.Logger.Printf("[VARIANT_SUCCESS] Channel %s: Variant %d worked - %s (%d kbps), streaming with %d bytes",
						r.Channel.Name, i+1, variant.Resolution, variant.Bandwidth/1000, bytes)
				}
				return true, bytes
			} else {
				if r.Config.Debug {
					r.Logger.Printf("[VARIANT_FAIL] Channel %s: Variant %d failed - %s (%d kbps)",
						r.Channel.Name, i+1, variant.Resolution, variant.Bandwidth/1000)
				}
			}
		}
		if r.Config.Debug {
			r.Logger.Printf("[VARIANT_EXHAUSTED] Channel %s: All %d variants failed", r.Channel.Name, len(variants))
		}
		return false, 0

	} else {
		// Single variant (media playlist or direct stream)
		if r.Config.Debug {
			r.Logger.Printf("[SINGLE_STREAM] Channel %s: Processing single stream/media playlist", r.Channel.Name)
		}
		return r.streamFromURL(variants[0].URL, stream.Source)
	}
}

// getStreamVariants resolves master playlist and returns all variants
func (r *Restream) getStreamVariants(url string, source *config.SourceConfig) ([]parser.StreamVariant, bool, error) {
	// Create master playlist handler
	masterHandler := parser.NewMasterPlaylistHandler(r.Logger, r.Config)

	// Create request to check the playlist
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, false, err
	}

	// Use context with timeout for playlist check
	checkCtx, checkCancel := context.WithTimeout(r.Ctx, 15*time.Second)
	defer checkCancel()
	req = req.WithContext(checkCtx)

	// Use source-specific headers
	resp, err := r.HttpClient.DoWithHeaders(req, source.UserAgent, source.ReqOrigin, source.ReqReferrer)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("HTTP %d response", resp.StatusCode)
	}

	// Check if this might be a master playlist based on response headers
	if !r.shouldCheckForMasterPlaylist(resp) {
		if r.Config.Debug {
			r.Logger.Printf("[PLAYLIST_CHECK] Channel %s: Response doesn't look like playlist", r.Channel.Name)
		}
		// Return single variant for direct stream
		singleVariant := parser.StreamVariant{
			URL:        url,
			Bandwidth:  0,
			Resolution: "unknown",
		}
		return []parser.StreamVariant{singleVariant}, false, nil
	}

	// Read the response to check content
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, false, err
	}

	content := string(body)
	if r.Config.Debug {
		r.Logger.Printf("[PLAYLIST_CONTENT] Channel %s: Downloaded %d bytes", r.Channel.Name, len(content))
	}
	// Process with master playlist handler
	variants, isMaster, err := masterHandler.ProcessMasterPlaylistVariants(content, url, r.Channel.Name)
	if err != nil {
		return nil, false, err
	}

	return variants, isMaster, nil
}

// testAndStreamVariant tests a specific variant and streams if it works
func (r *Restream) testAndStreamVariant(variant parser.StreamVariant, source *config.SourceConfig) (bool, int64) {
	if r.Config.Debug {
		r.Logger.Printf("[VARIANT_TEST_START] Testing variant: %s", utils.LogURL(r.Config, variant.URL))
	}

	testReq, err := http.NewRequest("GET", variant.URL, nil)
	if err != nil {
		if r.Config.Debug {
			r.Logger.Printf("[VARIANT_TEST_ERROR] Error creating test request: %v", err)
		}
		return false, 0
	}

	testCtx, testCancel := context.WithTimeout(r.Ctx, 10*time.Second)
	defer testCancel()
	testReq = testReq.WithContext(testCtx)

	// Use source-specific headers
	resp, err := r.HttpClient.DoWithHeaders(testReq, source.UserAgent, source.ReqOrigin, source.ReqReferrer)
	if err != nil {
		if r.Config.Debug {
			r.Logger.Printf("[VARIANT_TEST_ERROR] Error testing variant: %v", err)
		}
		return false, 0
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if r.Config.Debug {
			r.Logger.Printf("[VARIANT_TEST_ERROR] Variant returned HTTP %d", resp.StatusCode)
		}

		return false, 0
	}

	testBuffer := make([]byte, 8192)
	n, err := resp.Body.Read(testBuffer)

	if err != nil && err != io.EOF {
		if r.Config.Debug {
			r.Logger.Printf("[VARIANT_TEST_ERROR] Error reading test data: %v", err)
		}

		return false, 0
	}

	if n == 0 {
		if r.Config.Debug {
			r.Logger.Printf("[VARIANT_TEST_ERROR] No data received from variant")
		}

		return false, 0
	}

	content := string(testBuffer[:n])

	// Check if this is HLS
	if strings.Contains(content, "#EXTINF") || strings.Contains(content, "#EXT-X-TARGETDURATION") {
		if r.Config.Debug {
			r.Logger.Printf("[HLS_DETECTED] HLS media playlist detected")
		}
		// Check if this playlist contains tracking URLs
		hasTrackingURLs := strings.Contains(content, "/beacon/") ||
			strings.Contains(content, "redirect_url=") ||
			strings.Contains(content, "bcn=") ||
			strings.Contains(content, "seen-ad=")

		if hasTrackingURLs {
			if r.Config.Debug {
				r.Logger.Printf("[HLS_TRACKING] Detected tracking URLs in playlist, skipping ffprobe validation")
			}
			// For streams with tracking URLs, skip ffprobe and try streaming directly
			resp.Body.Close()
			return r.streamHLSSegments(variant.URL)
		} else {
			// Standard HLS validation with ffprobe
			if r.Config.Debug {
				r.Logger.Printf("[HLS_STANDARD] Standard HLS playlist, testing with ffprobe")
			}
			result, _ := r.testStreamWithFFprobe(variant.URL)

			switch result {
			case "video":
				if r.Config.Debug {
					r.Logger.Printf("[HLS_VALID] Stream has valid video, starting segment streaming")
				}
				resp.Body.Close()
				return r.streamHLSSegments(variant.URL)

			case "timeout":
				if r.Config.Debug {
					r.Logger.Printf("[HLS_TIMEOUT] Stream caused ffprobe timeout - invalid stream, trying next")
				}
				return false, 0

			case "invalid_format":
				if r.Config.Debug {
					r.Logger.Printf("[HLS_INVALID_FORMAT] Stream has invalid format, skipping")
				}
				return false, 0

			case "error":
				if r.Config.Debug {
					r.Logger.Printf("[HLS_ERROR] ffprobe error but no timeout - might work with proxying")
				}
				resp.Body.Close()
				return r.streamHLSSegments(variant.URL)

			case "no_video":
				if r.Config.Debug {
					r.Logger.Printf("[HLS_NO_VIDEO] No video stream found, trying next variant")
				}
				return false, 0

			default:
				if r.Config.Debug {
					r.Logger.Printf("[HLS_UNKNOWN] Unknown ffprobe result: %s, attempting to stream anyway", result)
				}
				resp.Body.Close()
				return r.streamHLSSegments(variant.URL)
			}
		}
	}
	if r.Config.Debug {
		r.Logger.Printf("[VARIANT_TEST_SUCCESS] Direct stream variant, got %d test bytes", n)
	}
	resp.Body.Close()

	return r.streamFromURL(variant.URL, source)
}

// shouldCheckForMasterPlaylist determines if response might be a master playlist
func (r *Restream) shouldCheckForMasterPlaylist(resp *http.Response) bool {
	contentType := resp.Header.Get("Content-Type")
	contentLength := resp.Header.Get("Content-Length")

	// Check for M3U8 content type
	if strings.Contains(strings.ToLower(contentType), "mpegurl") ||
		strings.Contains(strings.ToLower(contentType), "m3u8") {
		return true
	}

	// Check for small content length (master playlists are typically small)
	if contentLength != "" {
		if length, err := strconv.ParseInt(contentLength, 10, 64); err == nil {
			// Master playlists are typically under 100KB
			if length > 0 && length < 100*1024 {
				return true
			}
		}
	}

	return false
}

// streamFromURL handles the actual streaming from a resolved URL
func (r *Restream) streamFromURL(url string, source *config.SourceConfig) (bool, int64) {
	if r.Config.Debug {
		r.Logger.Printf("[STREAMING_START] Channel %s: Starting stream from %s", r.Channel.Name, utils.LogURL(r.Config, url))
	}

	// Create request with reasonable timeout
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		if r.Config.Debug {
			r.Logger.Printf("Error creating request for channel %s: %v", r.Channel.Name, err)
		}
		return false, 0
	}

	// Use context without timeout for streaming (let it run until cancelled)
	req = req.WithContext(r.Ctx)

	// Use source-specific headers
	resp, err := r.HttpClient.DoWithHeaders(req, source.UserAgent, source.ReqOrigin, source.ReqReferrer)
	if err != nil {
		if r.Config.Debug {
			r.Logger.Printf("Error connecting to stream for %s: %v", r.Channel.Name, err)
			r.Logger.Printf("[STREAM_ERROR] Channel %s: Connection error: %v", r.Channel.Name, err)
		}
		metrics.StreamErrors.WithLabelValues(r.Channel.Name, "connection").Inc()
		return false, 0
	}

	// CRITICAL: Always close response body
	defer func() {
		resp.Body.Close()
		if r.Config.Debug {
			r.Logger.Printf("[CONNECTION_CLOSE] Channel %s: HTTP connection closed", r.Channel.Name)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		if r.Config.Debug {
			r.Logger.Printf("HTTP %d from upstream for channel %s", resp.StatusCode, r.Channel.Name)
		}
		metrics.StreamErrors.WithLabelValues(r.Channel.Name, fmt.Sprintf("http_%d", resp.StatusCode)).Inc()
		return false, 0
	}
	if r.Config.Debug {
		r.Logger.Printf("[CONNECTED] Channel %s: Streaming from resolved URL", r.Channel.Name)
	}
	metrics.ActiveConnections.WithLabelValues(r.Channel.Name).Set(1)

	// Stream data with frequent cancellation checks
	buffer := make([]byte, (r.Config.BufferSizePerStream * 1024 * 1024))
	totalBytes := int64(0)

	// Set a deadline for receiving first data
	firstDataTimer := time.NewTimer(15 * time.Second) // Increased timeout for variants
	defer firstDataTimer.Stop()
	firstData := true
	lastDataTime := time.Now()

	// Get source-specific MinDataSize
	minDataSize := source.MinDataSize * 1024

	for {
		// Check for cancellation more frequently
		select {
		case <-r.Ctx.Done():
			if r.Config.Debug {
				r.Logger.Printf("[STREAM_CANCELLED] Channel %s: Context cancelled after %d bytes", r.Channel.Name, totalBytes)
			}
			return totalBytes >= minDataSize, totalBytes
		case <-firstDataTimer.C:
			if firstData && totalBytes == 0 {
				if r.Config.Debug {
					r.Logger.Printf("[STREAM_ERROR] Channel %s: No data received in 15 seconds", r.Channel.Name)
				}
				return false, 0
			}
		default:
		}

		// Check for data timeout (no data for 30 seconds)
		if !firstData && time.Since(lastDataTime) > 30*time.Second {
			if r.Config.Debug {
				r.Logger.Printf("[STREAM_TIMEOUT] Channel %s: No data for 30 seconds, ending stream", r.Channel.Name)
			}
			return totalBytes >= minDataSize, totalBytes
		}

		n, err := resp.Body.Read(buffer)
		if n > 0 {
			if firstData {
				firstData = false
				firstDataTimer.Stop()
				if r.Config.Debug {
					r.Logger.Printf("[STREAM_DATA] Channel %s: First video data received", r.Channel.Name)
				}
			}

			lastDataTime = time.Now()
			data := buffer[:n]
			if !r.SafeBufferWrite(data) {
				return false, totalBytes
			}
			totalBytes += int64(n)
			metrics.BytesTransferred.WithLabelValues(r.Channel.Name, "downstream").Add(float64(n))

			// Send to all clients and check if any disconnected
			activeClients := r.DistributeToClients(data)
			if activeClients == 0 {
				if r.Config.Debug {
					r.Logger.Printf("[STREAM_STOP] Channel %s: No active clients", r.Channel.Name)
				}
				return totalBytes >= minDataSize, totalBytes
			}

		}

		if err != nil {
			if err == io.EOF {
				if r.Config.Debug {
					r.Logger.Printf("Stream ended normally for channel %s after %d bytes", r.Channel.Name, totalBytes)
				}
				return totalBytes >= minDataSize, totalBytes
			}

			// Check if error is due to context cancellation
			select {
			case <-r.Ctx.Done():
				if r.Config.Debug {
					r.Logger.Printf("[STREAM_CANCELLED] Channel %s: Context cancelled after %d bytes", r.Channel.Name, totalBytes)
				}
				return totalBytes >= minDataSize, totalBytes
			default:
				if r.Config.Debug {
					r.Logger.Printf("Error reading stream for channel %s after %d bytes: %v", r.Channel.Name, totalBytes, err)
					r.Logger.Printf("[STREAM_ERROR] Channel %s: Error after %d bytes: %s", r.Channel.Name, totalBytes, err.Error())
				}
				metrics.StreamErrors.WithLabelValues(r.Channel.Name, "read").Inc()
				return false, totalBytes
			}
		}
	}
}

// distribute the streams to the client
func (r *Restream) DistributeToClients(data []byte) int {
	activeClients := 0

	r.Clients.Range(func(key, value interface{}) bool {
		id := key.(string)
		client := value.(*types.RestreamClient)

		select {
		case <-client.Done:
			return true
		default:
			_, writeErr := client.Writer.Write(data)
			if writeErr != nil {
				if r.Config.Debug {
					r.Logger.Printf("Write error to client %s: %v (keeping client active for HLS buffering)", id, writeErr)
				}
				// Don't fail immediately on write errors for HLS - client might be buffering
				// Update last seen anyway to prevent premature cleanup
				client.LastSeen.Store(time.Now().Unix())
				activeClients++
			} else {
				client.Flusher.Flush()
				client.LastSeen.Store(time.Now().Unix())
				metrics.BytesTransferred.WithLabelValues(r.Channel.Name, "upstream").Add(float64(len(data)))
				activeClients++
			}
		}
		return true
	})

	return activeClients
}

func (r *Restream) SafeBufferWrite(data []byte) bool {
	// Don't check buffer if context is cancelled
	select {
	case <-r.Ctx.Done():
		return false
	default:
	}

	if r.Buffer == nil || r.Buffer.IsDestroyed() {
		if r.Config.Debug {
			r.Logger.Printf("[BUFFER_INVALID] Channel %s: Cannot write to invalid buffer", r.Channel.Name)
		}
		return false
	}

	r.Buffer.Write(data)
	return true
}

// Add this function to work/restream/restream.go
func isStreamDead(channelName string, streamIndex int) bool {
	deadStreamsPath := "/settings/dead-streams.json"

	data, err := os.ReadFile(deadStreamsPath)
	if err != nil {
		return false
	}

	type DeadStreamEntry struct {
		Channel     string `json:"channel"`
		StreamIndex int    `json:"streamIndex"`
	}

	type DeadStreamsFile struct {
		DeadStreams []DeadStreamEntry `json:"deadStreams"`
	}

	var deadStreams DeadStreamsFile
	if err := json.Unmarshal(data, &deadStreams); err != nil {
		return false
	}

	for _, entry := range deadStreams.DeadStreams {
		if entry.Channel == channelName && entry.StreamIndex == streamIndex {
			return true
		}
	}
	return false
}
