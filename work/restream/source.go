package restream

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"kptv-proxy/work/config"
	"kptv-proxy/work/constants"
	"kptv-proxy/work/deadstreams"
	"kptv-proxy/work/logger"
	"kptv-proxy/work/metrics"
	"kptv-proxy/work/parser"
	"kptv-proxy/work/types"
	"net/http"
	neturl "net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

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

	stream := r.Channel.Streams[index]
	r.Channel.Mu.RUnlock()

	// Dead and blocked streams are skipped; an auto-blocked one is probed again
	// once its retry interval has passed (see blockedUntilRetry)
	if blockedUntilRetry(r.Channel.Name, stream) {
		logger.Debug("{restream/restream - StreamFromSource} Channel %s: Stream %d is dead or blocked", r.Channel.Name, index)

		return false, 0
	}
	if dead := deadstreams.IsStreamDead(r.Channel.Name, stream.URLHash); dead || atomic.LoadInt32(&stream.Blocked) == 1 {
		logger.Info("{restream/restream - StreamFromSource} Channel %s: Retrying auto-blocked stream %d", r.Channel.Name, index)
		atomic.StoreInt32(&stream.Blocked, 0)
		if dead {
			if err := deadstreams.ReviveStream(r.Channel.Name, stream.URLHash); err != nil {
				logger.Error("{restream/restream - StreamFromSource} Channel %s: Failed to clear auto-block for stream %d: %v", r.Channel.Name, index, err)
			}
		}
	}

	// CRITICAL: Apply rate limiting BEFORE attempting connection to provider
	// This prevents overwhelming the provider with too many simultaneous connection attempts
	if !r.takeRateLimit(stream.Source) {
		return false, 0
	}
	if r.Config.Debug {
		logger.Debug("{restream/restream - StreamFromSource} Channel %s: Applied rate limit for stream %d (source: %s)",
			r.Channel.Name, index, stream.Source.Name)
	}

	// Enforce connection limit for this source — reserve the slot in one atomic
	// step so two channels starting at once can't both pass a separate check
	if stream.Source.ActiveConns.Add(1) > int32(stream.Source.MaxConnections) {
		stream.Source.ActiveConns.Add(-1)
		logger.Debug("{restream/restream - StreamFromSource} Channel %s: Stream %d source at max connections (%d)", r.Channel.Name, index, stream.Source.MaxConnections)
		return false, 0
	}
	defer stream.Source.ActiveConns.Add(-1) // ensure decrement when function exits

	// FFmpeg mode hands the URL straight to ffmpeg, but only after the same
	// dead/blocked checks, rate limiting, and connection accounting as a native fetch.
	// HLS playlists are resolved below first, so a master playlist streams the
	// best-ranked variant (with its separate audio rendition, if any) instead
	// of leaving the choice to ffmpeg.
	if r.Config.FFmpegMode && !isHLSPlaylistURL(stream.URL) {
		logger.Debug("{restream/restream - StreamFromSource} Channel %s: Streaming stream %d via FFmpeg", r.Channel.Name, index)
		return r.streamWithFFmpeg(stream.URL)
	}

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

			ok, bytes := r.testAndStreamVariant(variant, stream.Source)
			if ok {
				logger.Debug("{restream/restream - StreamFromSource} Channel %s: Successfully streamed variant %d (%s)", r.Channel.Name, i, variant.URL)

				return true, bytes
			}

			// a stall is restarted by the loop from a fresh master (new
			// session, best variant), not by dropping to a lower variant
			// of the session that just stalled
			if r.AttemptStalled.Load() {
				return false, bytes
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

	// Read the body for playlist parsing, capped so a hostile upstream can't OOM us
	body, err := io.ReadAll(io.LimitReader(resp.Body, constants.Internal.MaxPlaylistBytes))
	validationTimer.Stop()
	resp.Body.Close()
	if err != nil {
		cancel()
		logger.Error("{restream/restream - getStreamVariants} Failed to read response body for channel %s: %v", r.Channel.Name, err)
		return nil, false, nil, nil, err
	}

	// Parse the body as a master playlist and return variants
	// Resolve relative variant URIs against the final URL after redirects;
	// redirecting playlist hosts otherwise produce child URLs that 404
	baseURL := url
	if resp.Request != nil && resp.Request.URL != nil {
		baseURL = resp.Request.URL.String()
	}
	variants, isMaster, perr := masterHandler.ProcessMasterPlaylistVariants(string(body), baseURL, r.Channel.Name)
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
		return r.streamWithFFmpegAV(variant.URL, variant.AudioURL, source)
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

		body, err := io.ReadAll(io.LimitReader(resp.Body, constants.Internal.MaxPlaylistBytes))
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

		rest, err := io.ReadAll(io.LimitReader(resp.Body, constants.Internal.MaxPlaylistBytes))
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

	// an upstream that keeps the socket open but stops sending blocks Read
	// indefinitely — closing the body from the timer unblocks it, so the
	// attempt fails over in seconds instead of waiting on the watcher
	idleTimer := time.AfterFunc(constants.Internal.StreamReadIdleTimeout, func() {
		logger.Warn("{restream/restream - streamFromResponse} Channel %s: No upstream data for %v, abandoning stream", r.Channel.Name, constants.Internal.StreamReadIdleTimeout)
		resp.Body.Close()
	})
	defer idleTimer.Stop()

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
			if r.SwitchPending() {
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
				idleTimer.Reset(constants.Internal.StreamReadIdleTimeout)
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

			if r.Context().Err() != nil && r.SwitchPending() {
				return true, totalBytes
			}

			// a response body that errored can't recover — re-reading it only
			// returns the same error, so fail the attempt straight away
			logger.Error("{restream/restream - streamFromResponse} Channel %s: %v", r.Channel.Name, err)
			return false, totalBytes
		}

		consecutiveErrors = 0
	}
}

// isHLSPlaylistURL reports whether a stream URL points at an HLS playlist. In
// FFmpeg mode only these are fetched and parsed first; other URLs go straight
// to ffmpeg so single-use stream URLs are requested once.
func isHLSPlaylistURL(streamURL string) bool {
	u, err := neturl.Parse(streamURL)
	if err != nil {
		return false
	}
	p := strings.ToLower(u.Path)
	return strings.HasSuffix(p, ".m3u8") || strings.HasSuffix(p, ".m3u")
}

// blockedUntilRetry reports whether a dead or blocked stream must still be
// skipped. Manually killed streams always are. Auto-blocked streams become
// eligible again once AutoBlockRetryInterval has passed since their last
// failure, or straight away when none is on record this run (the block
// persists in the database across restarts); a failed probe re-blocks
// through the usual failure path.
func blockedUntilRetry(channelName string, s *types.Stream) bool {
	dead := deadstreams.IsStreamDead(channelName, s.URLHash)
	if dead && deadstreams.GetDeadStreamReason(channelName, s.URLHash) == "manual" {
		return true
	}
	if !dead && atomic.LoadInt32(&s.Blocked) == 0 {
		return false
	}

	s.Mu.Lock()
	sinceFail := time.Since(s.LastFail)
	s.Mu.Unlock()
	return sinceFail < constants.Internal.AutoBlockRetryInterval
}
