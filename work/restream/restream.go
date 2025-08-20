// work/restream/restream.go - Updated with variant testing
package restream

import (
	"context"
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
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// Restream wraps types.Restreamer to allow adding methods in this package.
type Restream struct {
	*types.Restreamer
}

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

	r.Logger.Printf("[CLIENT_CONNECT] Channel %s: ID: %s", r.Channel.Name, id)
	r.Logger.Printf("[METRIC] clients_connected: %d [%s]", count, r.Channel.Name)
	r.Logger.Printf("[CLIENT_ADD] Channel %s: Client %s added, total: %d", r.Channel.Name, id, count)

	// Start restreaming if not running
	if !r.Running.Load() && r.Running.CompareAndSwap(false, true) {
		r.Logger.Printf("[RESTREAM_START] Channel %s: Starting goroutine", r.Channel.Name)
		r.Logger.Printf("[CLIENT_READY] Channel %s: Client %s waiting", r.Channel.Name, id)
		r.Logger.Printf("[RESTREAM_ACTIVE] Channel %s: Restreamer started", r.Channel.Name)
		go r.Stream()
	}
}

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

		r.Logger.Printf("[CLIENT_DISCONNECT] Channel %s: Client: %s", r.Channel.Name, id)
		r.Logger.Printf("[METRIC] clients_connected: %d [%s]", count, r.Channel.Name)
		r.Logger.Printf("[CLIENT_REMOVE] Channel %s: Client %s removed, remaining: %d", r.Channel.Name, id, count)

		// Stop restreaming immediately if no clients
		if count == 0 {
			r.Logger.Printf("[RESTREAM_STOP] Channel %s: No more clients", r.Channel.Name)
			r.stopStream()
		}
	}
}

func (r *Restream) stopStream() {
	if r.Running.CompareAndSwap(true, false) {
		r.Cancel() // Cancel current context
		// Create new context for next time (but don't start until new client)
		r.Ctx, r.Cancel = context.WithCancel(context.Background())
		atomic.StoreInt32(&r.CurrentIndex, 0) // Reset to first stream
	}
}

func (r *Restream) Stream() {
	defer func() {
		r.Running.Store(false)
		metrics.ActiveConnections.WithLabelValues(r.Channel.Name).Set(0)
	}()

	r.Logger.Printf("[RESTREAM_CONFIG] Channel %s: MinDataSize = %d bytes", r.Channel.Name, r.Config.MinDataSize)

	r.Channel.Mu.RLock()
	streamCount := len(r.Channel.Streams)
	r.Channel.Mu.RUnlock()

	if streamCount == 0 {
		r.Logger.Printf("[STREAM_ERROR] Channel %s: No streams available", r.Channel.Name)
		return
	}

	r.Logger.Printf("[STREAM_INFO] Channel %s: Has %d streams available", r.Channel.Name, streamCount)

	maxTotalAttempts := streamCount * 2 // Try each stream twice max
	totalAttempts := 0

	for totalAttempts < maxTotalAttempts {
		// Check if we should stop immediately
		select {
		case <-r.Ctx.Done():
			r.Logger.Printf("[RESTREAM_STOP] Channel %s: Context cancelled", r.Channel.Name)
			return
		default:
		}

		// Check if we still have clients
		clientCount := 0
		r.Clients.Range(func(_, _ interface{}) bool {
			clientCount++
			return true
		})

		if clientCount == 0 {
			r.Logger.Printf("[RESTREAM_STOP] Channel %s: No clients remain", r.Channel.Name)
			return
		}

		// Get current stream index
		currentIdx := int(atomic.LoadInt32(&r.CurrentIndex))
		totalAttempts++

		r.Logger.Printf("[STREAM_TRY] Channel %s: Attempting index %d (total attempt %d/%d)",
			r.Channel.Name, currentIdx, totalAttempts, maxTotalAttempts)

		streamStart := time.Now()
		success, bytesReceived := r.StreamFromSource(currentIdx)
		streamDuration := time.Since(streamStart)

		r.Logger.Printf("[STREAM_RESULT] Channel %s: Index %d - Success: %v, Bytes: %d, Duration: %v",
			r.Channel.Name, currentIdx, success, bytesReceived, streamDuration)

		// For master playlists, success means we found a working variant and started streaming
		// For media streams, success means we got sufficient data
		if success {
			r.Logger.Printf("[STREAM_SUCCESS] Channel %s: Stream successful with %d bytes",
				r.Channel.Name, bytesReceived)

			// If we have a successful stream, we should already be streaming to clients
			// The stream will continue until context is cancelled or clients disconnect
			return
		} else {
			r.Logger.Printf("[STREAM_FAIL] Channel %s: Stream failed", r.Channel.Name)
		}

		// Move to next stream only if current one failed
		if streamCount > 1 {
			newIdx := (currentIdx + 1) % streamCount
			atomic.StoreInt32(&r.CurrentIndex, int32(newIdx))
			r.Logger.Printf("[STREAM_SWITCH] Channel %s: Moving from index %d to %d",
				r.Channel.Name, currentIdx, newIdx)
		} else {
			r.Logger.Printf("[STREAM_SINGLE] Channel %s: Only 1 stream available, will retry after delay", r.Channel.Name)
		}

		// Brief pause before retry
		select {
		case <-r.Ctx.Done():
			return
		case <-time.After(2 * time.Second):
			// Continue
		}
	}

	r.Logger.Printf("[STREAM_EXHAUSTED] Channel %s: All streams tried %d times, stopping", r.Channel.Name, maxTotalAttempts)
}

func (r *Restream) StreamFromSource(index int) (bool, int64) {
	r.Channel.Mu.RLock()
	if index >= len(r.Channel.Streams) {
		r.Channel.Mu.RUnlock()
		r.Logger.Printf("Invalid stream index %d for channel: %s", index, r.Channel.Name)
		return false, 0
	}
	stream := r.Channel.Streams[index]
	r.Channel.Mu.RUnlock()

	// Check if stream is blocked
	if atomic.LoadInt32(&stream.Blocked) == 1 {
		r.Logger.Printf("Stream blocked for channel %s: %s", r.Channel.Name, utils.LogURL(r.Config, stream.URL))
		return false, 0
	}

	// CRITICAL: Check connection limit BEFORE making request
	currentConns := atomic.LoadInt32(&stream.Source.ActiveConns)
	maxConns := int32(stream.Source.MaxConnections)

	if currentConns >= maxConns {
		r.Logger.Printf("[CONNECTION_LIMIT] Channel %s: Source at limit (%d/%d): %s",
			r.Channel.Name, currentConns, maxConns, utils.LogURL(r.Config, stream.URL))
		return false, 0
	}

	// Increment connection count BEFORE making request
	newConns := atomic.AddInt32(&stream.Source.ActiveConns, 1)
	r.Logger.Printf("[CONNECTION_ACQUIRE] Channel %s: Using connection %d/%d for %s",
		r.Channel.Name, newConns, maxConns, utils.LogURL(r.Config, stream.URL))

	// CRITICAL: Always decrement connection count when done
	defer func() {
		remainingConns := atomic.AddInt32(&stream.Source.ActiveConns, -1)
		r.Logger.Printf("[CONNECTION_RELEASE] Channel %s: Released connection, remaining: %d/%d",
			r.Channel.Name, remainingConns, maxConns)
	}()

	// Get all available variants for this stream
	variants, isMaster, err := r.getStreamVariants(stream.URL)
	if err != nil {
		r.Logger.Printf("[VARIANT_ERROR] Channel %s: Error getting variants: %v", r.Channel.Name, err)
		return false, 0
	}

	if isMaster {
		r.Logger.Printf("[VARIANT_TEST] Channel %s: Testing %d variants from highest to lowest quality",
			r.Channel.Name, len(variants))

		// Try each variant from highest to lowest quality
		for i, variant := range variants {
			r.Logger.Printf("[VARIANT_TRY] Channel %s: Trying variant %d/%d - %s (%d kbps)",
				r.Channel.Name, i+1, len(variants), variant.Resolution, variant.Bandwidth/1000)

			success, bytes := r.testAndStreamVariant(variant)
			if success {
				r.Logger.Printf("[VARIANT_SUCCESS] Channel %s: Variant %d worked - %s (%d kbps), streaming with %d bytes",
					r.Channel.Name, i+1, variant.Resolution, variant.Bandwidth/1000, bytes)
				return true, bytes
			} else {
				r.Logger.Printf("[VARIANT_FAIL] Channel %s: Variant %d failed - %s (%d kbps)",
					r.Channel.Name, i+1, variant.Resolution, variant.Bandwidth/1000)
			}
		}

		r.Logger.Printf("[VARIANT_EXHAUSTED] Channel %s: All %d variants failed", r.Channel.Name, len(variants))
		return false, 0

	} else {
		// Single variant (media playlist or direct stream)
		r.Logger.Printf("[SINGLE_STREAM] Channel %s: Processing single stream/media playlist", r.Channel.Name)
		return r.streamFromURL(variants[0].URL)
	}
}

// getStreamVariants resolves master playlist and returns all variants
func (r *Restream) getStreamVariants(url string) ([]parser.StreamVariant, bool, error) {
	// Create master playlist handler
	masterHandler := parser.NewMasterPlaylistHandler(r.Logger)

	// Create request to check the playlist
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, false, err
	}

	// Use context with timeout for playlist check
	checkCtx, checkCancel := context.WithTimeout(r.Ctx, 15*time.Second)
	defer checkCancel()
	req = req.WithContext(checkCtx)

	resp, err := r.HttpClient.Do(req)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("HTTP %d response", resp.StatusCode)
	}

	// Check if this might be a master playlist based on response headers
	if !r.shouldCheckForMasterPlaylist(resp) {
		r.Logger.Printf("[PLAYLIST_CHECK] Channel %s: Response doesn't look like playlist", r.Channel.Name)
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
	r.Logger.Printf("[PLAYLIST_CONTENT] Channel %s: Downloaded %d bytes", r.Channel.Name, len(content))

	// Process with master playlist handler
	variants, isMaster, err := masterHandler.ProcessMasterPlaylistVariants(content, url, r.Channel.Name)
	if err != nil {
		return nil, false, err
	}

	return variants, isMaster, nil
}

// testAndStreamVariant tests a specific variant and streams if it works
func (r *Restream) testAndStreamVariant(variant parser.StreamVariant) (bool, int64) {
	r.Logger.Printf("[VARIANT_TEST_START] Testing variant: %s", utils.LogURL(r.Config, variant.URL))

	testReq, err := http.NewRequest("GET", variant.URL, nil)
	if err != nil {
		r.Logger.Printf("[VARIANT_TEST_ERROR] Error creating test request: %v", err)
		return false, 0
	}

	testCtx, testCancel := context.WithTimeout(r.Ctx, 10*time.Second)
	defer testCancel()
	testReq = testReq.WithContext(testCtx)

	resp, err := r.HttpClient.Do(testReq)
	if err != nil {
		r.Logger.Printf("[VARIANT_TEST_ERROR] Error testing variant: %v", err)
		return false, 0
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		r.Logger.Printf("[VARIANT_TEST_ERROR] Variant returned HTTP %d", resp.StatusCode)
		return false, 0
	}

	testBuffer := make([]byte, 8192)
	n, err := resp.Body.Read(testBuffer)

	if err != nil && err != io.EOF {
		r.Logger.Printf("[VARIANT_TEST_ERROR] Error reading test data: %v", err)
		return false, 0
	}

	if n == 0 {
		r.Logger.Printf("[VARIANT_TEST_ERROR] No data received from variant")
		return false, 0
	}

	content := string(testBuffer[:n])

	// Check if this is HLS
	if strings.Contains(content, "#EXTINF") || strings.Contains(content, "#EXT-X-TARGETDURATION") {
		r.Logger.Printf("[HLS_DETECTED] HLS media playlist detected")

		// Check if this playlist contains tracking URLs
		hasTrackingURLs := strings.Contains(content, "/beacon/") ||
			strings.Contains(content, "redirect_url=") ||
			strings.Contains(content, "bcn=") ||
			strings.Contains(content, "seen-ad=")

		if hasTrackingURLs {
			r.Logger.Printf("[HLS_TRACKING] Detected tracking URLs in playlist, skipping ffprobe validation")
			// For streams with tracking URLs, skip ffprobe and try streaming directly
			resp.Body.Close()
			return r.streamHLSSegments(variant.URL)
		} else {
			// Standard HLS validation with ffprobe
			r.Logger.Printf("[HLS_STANDARD] Standard HLS playlist, testing with ffprobe")
			result, _ := r.testStreamWithFFprobe(variant.URL)

			switch result {
			case "video":
				r.Logger.Printf("[HLS_VALID] Stream has valid video, starting segment streaming")
				resp.Body.Close()
				return r.streamHLSSegments(variant.URL)

			case "timeout":
				r.Logger.Printf("[HLS_TIMEOUT] Stream caused ffprobe timeout - invalid stream, trying next")
				return false, 0

			case "invalid_format":
				r.Logger.Printf("[HLS_INVALID_FORMAT] Stream has invalid format, skipping")
				return false, 0

			case "error":
				r.Logger.Printf("[HLS_ERROR] ffprobe error but no timeout - might work with proxying")
				resp.Body.Close()
				return r.streamHLSSegments(variant.URL)

			case "no_video":
				r.Logger.Printf("[HLS_NO_VIDEO] No video stream found, trying next variant")
				return false, 0

			default:
				r.Logger.Printf("[HLS_UNKNOWN] Unknown ffprobe result: %s, attempting to stream anyway", result)
				resp.Body.Close()
				return r.streamHLSSegments(variant.URL)
			}
		}
	}

	r.Logger.Printf("[VARIANT_TEST_SUCCESS] Direct stream variant, got %d test bytes", n)
	resp.Body.Close()

	return r.streamFromURL(variant.URL)
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
func (r *Restream) streamFromURL(url string) (bool, int64) {
	r.Logger.Printf("[STREAMING_START] Channel %s: Starting stream from %s", r.Channel.Name, utils.LogURL(r.Config, url))

	// Create request with reasonable timeout
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		r.Logger.Printf("Error creating request for channel %s: %v", r.Channel.Name, err)
		return false, 0
	}

	// Use context without timeout for streaming (let it run until cancelled)
	req = req.WithContext(r.Ctx)

	// Make request
	resp, err := r.HttpClient.Do(req)
	if err != nil {
		r.Logger.Printf("Error connecting to stream for %s: %v", r.Channel.Name, err)
		r.Logger.Printf("[STREAM_ERROR] Channel %s: Connection error: %v", r.Channel.Name, err)
		metrics.StreamErrors.WithLabelValues(r.Channel.Name, "connection").Inc()
		return false, 0
	}

	// CRITICAL: Always close response body
	defer func() {
		resp.Body.Close()
		r.Logger.Printf("[CONNECTION_CLOSE] Channel %s: HTTP connection closed", r.Channel.Name)
	}()

	if resp.StatusCode != http.StatusOK {
		r.Logger.Printf("HTTP %d from upstream for channel %s", resp.StatusCode, r.Channel.Name)
		metrics.StreamErrors.WithLabelValues(r.Channel.Name, fmt.Sprintf("http_%d", resp.StatusCode)).Inc()
		return false, 0
	}

	r.Logger.Printf("[CONNECTED] Channel %s: Streaming from resolved URL", r.Channel.Name)
	metrics.ActiveConnections.WithLabelValues(r.Channel.Name).Set(1)

	// Stream data with frequent cancellation checks
	buffer := make([]byte, 32*1024) // 32KB chunks for better performance
	totalBytes := int64(0)

	// Set a deadline for receiving first data
	firstDataTimer := time.NewTimer(15 * time.Second) // Increased timeout for variants
	defer firstDataTimer.Stop()
	firstData := true
	lastDataTime := time.Now()

	for {
		// Check for cancellation more frequently
		select {
		case <-r.Ctx.Done():
			r.Logger.Printf("[STREAM_CANCELLED] Channel %s: Context cancelled after %d bytes", r.Channel.Name, totalBytes)
			return totalBytes >= r.Config.MinDataSize, totalBytes
		case <-firstDataTimer.C:
			if firstData && totalBytes == 0 {
				r.Logger.Printf("[STREAM_ERROR] Channel %s: No data received in 15 seconds", r.Channel.Name)
				return false, 0
			}
		default:
		}

		// Check for data timeout (no data for 30 seconds)
		if !firstData && time.Since(lastDataTime) > 30*time.Second {
			r.Logger.Printf("[STREAM_TIMEOUT] Channel %s: No data for 30 seconds, ending stream", r.Channel.Name)
			return totalBytes >= r.Config.MinDataSize, totalBytes
		}

		n, err := resp.Body.Read(buffer)
		if n > 0 {
			if firstData {
				firstData = false
				firstDataTimer.Stop()
				r.Logger.Printf("[STREAM_DATA] Channel %s: First video data received", r.Channel.Name)
			}

			lastDataTime = time.Now()
			data := buffer[:n]
			r.Buffer.Write(data)
			totalBytes += int64(n)
			metrics.BytesTransferred.WithLabelValues(r.Channel.Name, "downstream").Add(float64(n))

			// Send to all clients and check if any disconnected
			activeClients := r.DistributeToClients(data)
			if activeClients == 0 {
				r.Logger.Printf("[STREAM_STOP] Channel %s: No active clients", r.Channel.Name)
				return totalBytes >= r.Config.MinDataSize, totalBytes
			}
		}

		if err != nil {
			if err == io.EOF {
				r.Logger.Printf("Stream ended normally for channel %s after %d bytes", r.Channel.Name, totalBytes)
				return totalBytes >= r.Config.MinDataSize, totalBytes
			}

			// Check if error is due to context cancellation
			select {
			case <-r.Ctx.Done():
				r.Logger.Printf("[STREAM_CANCELLED] Channel %s: Context cancelled after %d bytes", r.Channel.Name, totalBytes)
				return totalBytes >= r.Config.MinDataSize, totalBytes
			default:
				r.Logger.Printf("Error reading stream for channel %s after %d bytes: %v", r.Channel.Name, totalBytes, err)
				r.Logger.Printf("[STREAM_ERROR] Channel %s: Error after %d bytes: %s", r.Channel.Name, totalBytes, err.Error())
				metrics.StreamErrors.WithLabelValues(r.Channel.Name, "read").Inc()
				return false, totalBytes
			}
		}
	}
}

func (r *Restream) DistributeToClients(data []byte) int {
	activeClients := 0

	r.Clients.Range(func(key, value interface{}) bool {
		id := key.(string)
		client := value.(*types.RestreamClient)

		select {
		case <-client.Done:
			// Client disconnected, will be cleaned up
			return true
		default:
			_, writeErr := client.Writer.Write(data)
			if writeErr != nil {
				r.Logger.Printf("Error writing to client %s: %v", id, writeErr)
				// Don't remove here, let the main handler remove it
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
