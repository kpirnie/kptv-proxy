package restream

import (
	"context"
	"fmt"
	"io"
	"kptv-proxy/work/config"
	"kptv-proxy/work/logger"
	"kptv-proxy/work/types"
	"kptv-proxy/work/utils"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

/**
 * SegmentTracker uses a circular buffer approach to track processed segments
 * with bounded memory usage and efficient lookups
 *
 * This structure implements a fixed-size circular buffer to track which HLS segments
 * have been processed, preventing duplicate processing while maintaining constant memory
 * usage regardless of stream duration. The circular buffer automatically evicts the
 * oldest entries when capacity is reached.
 *
 * Key features:
 * - O(1) lookups via map-based tracking
 * - O(1) insertions with automatic eviction
 * - Bounded memory usage for long-running streams
 * - Thread-safe operations via RWMutex
 */
type SegmentTracker struct {
	segments    []string       // Circular buffer of segment URLs
	segmentMap  map[string]int // Maps segment URL to position in buffer
	head        int            // Current head position in circular buffer
	maxSize     int            // Maximum number of segments to track
	currentSize int            // Current number of segments in tracker
	mutex       sync.RWMutex   // Protects concurrent access
}

/**
 * NewSegmentTracker creates a new segment tracker with bounded memory
 *
 * @param maxSize Maximum number of segments to track before eviction
 * @return Initialized segment tracker ready for use
 */
func NewSegmentTracker(maxSize int) *SegmentTracker {
	logger.Debug("{restream/hls - NewSegmentTracker} Creating new tracker with max size: %d", maxSize)
	return &SegmentTracker{
		segments:    make([]string, maxSize),
		segmentMap:  make(map[string]int),
		maxSize:     maxSize,
		head:        0,
		currentSize: 0,
	}
}

/**
 * HasProcessed checks if a segment has been processed recently
 *
 * @param segmentURL The segment URL to check
 * @return true if segment is in the tracking cache
 */
func (st *SegmentTracker) HasProcessed(segmentURL string) bool {
	st.mutex.RLock()
	defer st.mutex.RUnlock()

	_, exists := st.segmentMap[segmentURL]
	return exists
}

/**
 * MarkProcessed marks a segment as processed, automatically handling cleanup
 *
 * When the circular buffer is full, this automatically evicts the oldest segment
 * to make room for the new entry, ensuring constant memory usage.
 *
 * @param segmentURL The segment URL to mark as processed
 */
func (st *SegmentTracker) MarkProcessed(segmentURL string) {
	st.mutex.Lock()
	defer st.mutex.Unlock()

	// If we're at capacity, remove the oldest segment from tracking
	if st.currentSize >= st.maxSize {
		// Remove the segment that's about to be overwritten
		oldSegment := st.segments[st.head]
		if oldSegment != "" {
			delete(st.segmentMap, oldSegment)
			logger.Debug("{restream/hls - MarkProcessed} Evicting old segment from position %d", st.head)
		}
	} else {
		st.currentSize++
	}

	// Add new segment at head position
	st.segments[st.head] = segmentURL
	st.segmentMap[segmentURL] = st.head

	// Move head to next position (circular wraparound)
	st.head = (st.head + 1) % st.maxSize
}

/**
 * Size returns the current number of tracked segments
 *
 * @return Number of segments currently in the tracker
 */
func (st *SegmentTracker) Size() int {
	st.mutex.RLock()
	defer st.mutex.RUnlock()
	return st.currentSize
}

/**
 * Clear removes all tracked segments (useful for cleanup)
 *
 * Resets the tracker to initial state, freeing all segment references
 */
func (st *SegmentTracker) Clear() {
	st.mutex.Lock()
	defer st.mutex.Unlock()

	logger.Debug("{restream/hls - Clear} Clearing all tracked segments (size was: %d)", st.currentSize)

	// Clear the map
	st.segmentMap = make(map[string]int)

	// Clear the slice
	for i := range st.segments {
		st.segments[i] = ""
	}

	st.head = 0
	st.currentSize = 0
}

/**
 * GetTrackedSegments returns a copy of currently tracked segments (for debugging)
 *
 * @return Slice containing all currently tracked segment URLs
 */
func (st *SegmentTracker) GetTrackedSegments() []string {
	st.mutex.RLock()
	defer st.mutex.RUnlock()

	result := make([]string, 0, st.currentSize)
	for _, segment := range st.segments {
		if segment != "" {
			result = append(result, segment)
		}
	}
	return result
}

/**
 * streamHLSSegments implements continuous HLS segment streaming from a master or media playlist URL,
 * managing the complete lifecycle of segment discovery, fetching, and distribution to connected clients.
 *
 * This function implements the core HLS streaming loop:
 * - Continuously polls the HLS playlist for new segments
 * - Maintains a processed segment cache to avoid duplicates
 * - Handles both live streams (new segments appear) and VOD (static playlist)
 * - Implements memory limits to prevent unbounded growth
 * - Detects stalled streams and terminates appropriately
 *
 * Memory management features:
 * - Maximum segment cache size limits (20 segments default)
 * - Periodic cache cleanup to remove old segment references
 * - Efficient segment URL tracking to minimize memory overhead
 *
 * Stream health monitoring:
 * - Tracks consecutive empty playlist refreshes
 * - Monitors time since last successful segment
 * - Terminates stalled streams after 30 seconds of inactivity
 *
 * @param playlistURL URL of the HLS playlist containing segment references
 * @return (success bool, totalBytes int64) - success if data was streamed, total bytes transferred
 */
func (r *Restream) streamHLSSegments(playlistURL string) (bool, int64) {
	logger.Debug("{restream/hls - streamHLSSegments} Starting HLS stream for channel %s from: %s", r.Channel.Name, utils.LogURL(r.Config, playlistURL))

	// If FFmpeg mode is enabled, delegate to FFmpeg instead of native Go streaming
	if r.Config.FFmpegMode {
		logger.Debug("{restream/hls - streamHLSSegments} FFmpeg mode enabled, delegating to FFmpeg for channel %s", r.Channel.Name)
		return r.streamWithFFmpeg(playlistURL)
	}

	// Initialize streaming state
	totalBytes := int64(0)
	segmentTracker := NewSegmentTracker(20) // Track last 20 segments to prevent duplicates
	defer segmentTracker.Clear()
	consecutiveEmptyRefresh := 0
	maxEmptyRefresh := 10
	lastSuccessfulSegment := time.Now()

	logger.Debug("{restream/hls - streamHLSSegments} Initialized segment tracker for channel %s (max size: 20)", r.Channel.Name)

	// Main streaming loop - continuously poll playlist for new segments
	for {
		// Check if context was cancelled (shutdown or manual switch)
		select {
		case <-r.Ctx.Done():
			success := totalBytes > 1024*1024
			logger.Debug("{restream/hls - streamHLSSegments} Context cancelled for channel %s (total bytes: %d, success: %v)",
				r.Channel.Name, totalBytes, success)
			return success, totalBytes
		default:
		}

		// Count active clients - stop streaming if none remain
		clientCount := 0
		r.Clients.Range(func(key string, value *types.RestreamClient) bool {
			clientCount++
			return true
		})

		if clientCount == 0 {
			logger.Debug("{restream/hls - streamHLSSegments} No clients remaining for channel %s, stopping (total bytes: %d)",
				r.Channel.Name, totalBytes)
			return totalBytes > 1024*1024, totalBytes
		}

		// Fetch current playlist and extract segment URLs
		segments, err := r.getHLSSegments(playlistURL)
		if err != nil {
			logger.Error("{restream/hls - streamHLSSegments} Error fetching playlist for channel %s: %v", r.Channel.Name, err)
			return false, totalBytes
		}

		if len(segments) > 0 {
			logger.Debug("{restream/hls - streamHLSSegments} Fetched playlist for channel %s: found %d total segments",
				r.Channel.Name, len(segments))
		}

		// Process new segments (skip already-processed ones)
		newSegmentCount := 0
		segmentErrors := 0

		for _, segmentURL := range segments {
			// Skip segments we've already processed
			if segmentTracker.HasProcessed(segmentURL) {
				continue
			}

			// Check for cancellation before processing each segment
			select {
			case <-r.Ctx.Done():
				logger.Debug("{restream/hls - streamHLSSegments} Context cancelled during segment processing for channel %s", r.Channel.Name)
				return totalBytes > 1024*1024, totalBytes
			default:
			}

			logger.Debug("{restream/hls - streamHLSSegments} Processing new segment for channel %s: %s",
				r.Channel.Name, utils.LogURL(r.Config, segmentURL))

			// Stream this segment to all connected clients
			segmentBytes, err := r.streamSegment(segmentURL, playlistURL)
			if err != nil {
				segmentErrors++
				logger.Warn("{restream/hls - streamHLSSegments} Error streaming segment for channel %s: %v (errors: %d/5)",
					r.Channel.Name, err, segmentErrors)

				// Too many errors indicates serious problem - abort streaming
				if segmentErrors > 5 {
					logger.Error("{restream/hls - streamHLSSegments} Too many segment errors for channel %s, aborting", r.Channel.Name)
					return false, totalBytes
				}
				continue
			}

			// Successfully streamed segment - update tracking
			segmentTracker.MarkProcessed(segmentURL)
			totalBytes += segmentBytes
			newSegmentCount++
			lastSuccessfulSegment = time.Now()
			consecutiveEmptyRefresh = 0

			logger.Debug("{restream/hls - streamHLSSegments} Successfully streamed segment for channel %s: %d bytes (total: %d MB)",
				r.Channel.Name, segmentBytes, totalBytes/(1024*1024))
		}

		// Track empty refreshes to detect stalled streams
		if newSegmentCount == 0 {
			consecutiveEmptyRefresh++
			logger.Debug("{restream/hls - streamHLSSegments} No new segments for channel %s (empty refreshes: %d/%d)",
				r.Channel.Name, consecutiveEmptyRefresh, maxEmptyRefresh)

			// After max empty refreshes, check if stream is truly stalled
			if consecutiveEmptyRefresh >= maxEmptyRefresh {
				timeSinceSuccess := time.Since(lastSuccessfulSegment)
				if timeSinceSuccess > 30*time.Second {
					logger.Warn("{restream/hls - streamHLSSegments} Stream stalled for channel %s: no new segments for %v",
						r.Channel.Name, timeSinceSuccess)
					return false, totalBytes
				}
			}
		} else {
			// Reset empty refresh counter when we find new segments
			consecutiveEmptyRefresh = 0
		}

		// Log batch processing results
		if newSegmentCount > 0 {
			logger.Debug("{restream/hls - streamHLSSegments} Processed batch for channel %s: %d new segments, tracker size: %d, total: %d MB",
				r.Channel.Name, newSegmentCount, segmentTracker.Size(), totalBytes/(1024*1024))
		}

		// Wait before next playlist refresh (HLS standard is typically 1-3 seconds)
		select {
		case <-r.Ctx.Done():
			logger.Debug("{restream/hls - streamHLSSegments} Context cancelled during refresh wait for channel %s", r.Channel.Name)
			return totalBytes > 1024*1024, totalBytes
		case <-time.After(2 * time.Second):
			continue
		}
	}
}

/**
 * getHLSSegments fetches and parses an HLS playlist to extract individual segment URLs,
 * implementing comprehensive URL resolution for both relative and absolute segment paths.
 *
 * This function handles the complete playlist fetch and parse cycle:
 * - Applies rate limiting to prevent overwhelming source servers
 * - Locates source configuration for authentication headers
 * - Fetches playlist content with appropriate timeouts
 * - Parses playlist to extract segment URLs
 * - Resolves relative URLs to absolute paths
 * - Handles tracking/redirect URLs that require extraction
 *
 * URL resolution logic:
 * - Absolute URLs (starting with http) are used directly
 * - Relative URLs are resolved against the playlist's base URL
 * - Tracking URLs with redirect parameters are decoded and extracted
 *
 * Authentication handling:
 * - Uses source-specific User-Agent, Origin, and Referer headers when available
 * - Falls back to basic HTTP requests for unconfigured sources
 *
 * @param playlistURL Complete URL of the HLS playlist to fetch and parse
 * @return ([]string, error) - slice of absolute segment URLs, or error if fetch/parse fails
 */
func (r *Restream) getHLSSegments(playlistURL string) ([]string, error) {
	logger.Debug("{restream/hls - getHLSSegments} Fetching playlist for channel %s: %s", r.Channel.Name, utils.LogURL(r.Config, playlistURL))

	// Apply rate limiting before HLS playlist request to prevent server overload
	if r.RateLimiter != nil {
		r.RateLimiter.Take()
		logger.Debug("{restream/hls - getHLSSegments} Rate limit applied for channel %s", r.Channel.Name)
	}

	// Locate source configuration for authentication headers (cached)
	source := r.resolveSourceForURL(playlistURL)

	// Build HTTP request with appropriate timeout for playlist fetching
	req, err := http.NewRequest("GET", playlistURL, nil)
	if err != nil {
		logger.Error("{restream/hls - getHLSSegments} Failed to create request for channel %s: %v", r.Channel.Name, err)
		return nil, err
	}

	// Create context with timeout to prevent hanging on slow/dead servers
	ctx, cancel := context.WithTimeout(r.Ctx, 10*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	// Execute request with source-specific headers if available
	var resp *http.Response
	if source != nil {
		logger.Debug("{restream/hls - getHLSSegments} Using source-specific headers for channel %s (UA: %s)",
			r.Channel.Name, source.UserAgent)
		// Use source-specific authentication and headers
		resp, err = r.HttpClient.DoWithHeaders(req, source.UserAgent, source.ReqOrigin, source.ReqReferrer)
	} else {
		logger.Debug("{restream/hls - getHLSSegments} Using default HTTP client for channel %s", r.Channel.Name)
		// Fallback to basic HTTP request without custom headers
		resp, err = r.HttpClient.Do(req)
	}

	if err != nil {
		logger.Error("{restream/hls - getHLSSegments} Request failed for channel %s: %v", r.Channel.Name, err)
		return nil, err
	}
	defer resp.Body.Close()

	// Verify successful HTTP response before processing content
	if resp.StatusCode != http.StatusOK {
		logger.Error("{restream/hls - getHLSSegments} HTTP error for channel %s: status %d", r.Channel.Name, resp.StatusCode)
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	// Read complete playlist content for parsing
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Error("{restream/hls - getHLSSegments} Failed to read response body for channel %s: %v", r.Channel.Name, err)
		return nil, err
	}

	logger.Debug("{restream/hls - getHLSSegments} Successfully fetched playlist for channel %s: %d bytes",
		r.Channel.Name, len(body))

	var segments []string
	lines := strings.Split(string(body), "\n")

	// Parse playlist content line by line to extract segment URLs
	for _, line := range lines {
		line = strings.TrimSpace(line)

		// Identify segment lines (non-comment lines containing URLs)
		if line != "" && !strings.HasPrefix(line, "#") {
			var segmentURL string

			if strings.HasPrefix(line, "http") {
				// Absolute URL - use directly
				segmentURL = line
				logger.Debug("{restream/hls - getHLSSegments} Found absolute URL: %s", utils.LogURL(r.Config, segmentURL))
			} else {
				// Relative URL - resolve against playlist base URL
				baseURL := playlistURL[:strings.LastIndex(playlistURL, "/")]
				segmentURL = baseURL + "/" + line
				logger.Debug("{restream/hls - getHLSSegments} Resolved relative URL to: %s", utils.LogURL(r.Config, segmentURL))
			}

			// Check for tracking/beacon URLs requiring redirect resolution
			if resolvedURL := r.resolveRedirectURL(segmentURL); resolvedURL != "" {
				logger.Debug("{restream/hls - getHLSSegments} Resolved tracking URL for channel %s: %s -> %s",
					r.Channel.Name, utils.LogURL(r.Config, segmentURL), utils.LogURL(r.Config, resolvedURL))
				segmentURL = resolvedURL
			}

			segments = append(segments, segmentURL)
		}
	}

	logger.Debug("{restream/hls - getHLSSegments} Parsed %d segments from playlist for channel %s", len(segments), r.Channel.Name)

	return segments, nil
}

/**
 * resolveRedirectURL extracts and decodes actual stream URLs from tracking/beacon URLs
 * commonly used in IPTV sources for analytics and access control.
 *
 * Many IPTV providers wrap actual segment URLs in tracking/beacon URLs for analytics,
 * access control, or monetization. This function handles multiple URL encoding patterns
 * to extract the actual streamable URL from these wrapper URLs.
 *
 * Common patterns handled:
 * - redirect_url parameter containing URL-encoded destination
 * - beacon URLs with embedded redirect parameters
 * - Multiple levels of URL encoding requiring iterative decoding
 *
 * The function performs safe parsing and returns empty string on any error,
 * allowing the caller to fall back to the original URL if resolution fails.
 *
 * @param segmentURL Potentially tracking URL containing embedded redirect information
 * @return Resolved actual stream URL if extraction successful, empty string otherwise
 */
func (r *Restream) resolveRedirectURL(segmentURL string) string {
	// Handle standard redirect_url parameter pattern
	if strings.Contains(segmentURL, "redirect_url=") {
		logger.Debug("{restream/hls - resolveRedirectURL} Attempting to resolve redirect_url parameter")
		if parsedURL, err := url.Parse(segmentURL); err == nil {
			if redirectURL := parsedURL.Query().Get("redirect_url"); redirectURL != "" {
				if decodedURL, err := url.QueryUnescape(redirectURL); err == nil {
					logger.Debug("{restream/hls - resolveRedirectURL} Successfully decoded redirect_url: %s", utils.LogURL(r.Config, decodedURL))
					return decodedURL
				} else {
					logger.Warn("{restream/hls - resolveRedirectURL} Failed to unescape redirect URL: %v", err)
				}
			}
		} else {
			logger.Warn("{restream/hls - resolveRedirectURL} Failed to parse URL for redirect resolution: %v", err)
		}
	}

	// Handle beacon URLs with embedded redirect parameters
	if strings.Contains(segmentURL, "/beacon/") && strings.Contains(segmentURL, "redirect_url") {
		logger.Debug("{restream/hls - resolveRedirectURL} Attempting to resolve beacon URL with redirect")
		if parsedURL, err := url.Parse(segmentURL); err == nil {
			if redirectURL := parsedURL.Query().Get("redirect_url"); redirectURL != "" {
				if decodedURL, err := url.QueryUnescape(redirectURL); err == nil {
					logger.Debug("{restream/hls - resolveRedirectURL} Successfully decoded beacon redirect: %s", utils.LogURL(r.Config, decodedURL))
					return decodedURL
				} else {
					logger.Warn("{restream/hls - resolveRedirectURL} Failed to unescape beacon redirect: %v", err)
				}
			}
		} else {
			logger.Warn("{restream/hls - resolveRedirectURL} Failed to parse beacon URL: %v", err)
		}
	}

	// Return empty string if no redirect pattern found or resolution failed
	return ""
}

/**
 * streamSegment fetches and distributes a single HLS segment to all connected clients,
 * implementing efficient data streaming with proper error handling and byte counting.
 *
 * This function handles the complete lifecycle of streaming a single segment:
 * - Applies rate limiting to prevent server overload
 * - Locates source configuration for authentication
 * - Resolves tracking/redirect URLs if present
 * - Fetches segment data with appropriate timeout
 * - Streams data to clients in chunks using buffer pool
 * - Tracks metrics and activity timestamps
 * - Handles errors with retry logic
 *
 * Buffer management:
 * - Uses pooled buffers from getStreamBuffer() for memory efficiency
 * - Reads and distributes data in chunks to balance memory vs latency
 * - Returns buffers to pool when done
 *
 * Client distribution:
 * - Uses SafeBufferWrite() to add data to ring buffer
 * - DistributeToClients() sends to all connected clients
 * - Aborts if no clients remain active
 *
 * Error handling:
 * - Retries buffer write failures up to 5 times
 * - Retries read errors up to 5 times
 * - Returns total bytes even on partial success
 *
 * @param segmentURL Complete URL of the segment to fetch and stream
 * @param playlistURL Original playlist URL for source config lookup and referrer headers
 * @return (int64, error) - total bytes streamed, error if streaming fails
 */
func (r *Restream) streamSegment(segmentURL, playlistURL string) (int64, error) {
	logger.Debug("{restream/hls - streamSegment} Starting segment stream for channel %s: %s",
		r.Channel.Name, utils.LogURL(r.Config, segmentURL))

	// Apply rate limiting to prevent overwhelming source servers
	if r.RateLimiter != nil {
		r.RateLimiter.Take()
		logger.Debug("{restream/hls - streamSegment} Rate limit applied for channel %s", r.Channel.Name)
	}

	// Locate source configuration for authentication headers (cached)
	source := r.resolveSourceForURL(playlistURL)

	// Resolve tracking URLs to actual segment URLs
	originalURL := segmentURL
	if resolvedURL := r.resolveRedirectURL(segmentURL); resolvedURL != "" {
		logger.Debug("{restream/hls - streamSegment} Resolved redirect for channel %s: %s -> %s",
			r.Channel.Name, utils.LogURL(r.Config, segmentURL), utils.LogURL(r.Config, resolvedURL))
		segmentURL = resolvedURL
	}

	// Build HTTP request for segment
	req, err := http.NewRequest("GET", segmentURL, nil)
	if err != nil {
		logger.Error("{restream/hls - streamSegment} Failed to create request for channel %s: %v", r.Channel.Name, err)
		return 0, err
	}

	// If URL was resolved from tracking URL, set original as Referer for analytics
	if originalURL != segmentURL {
		req.Header.Set("Referer", originalURL)
		logger.Debug("{restream/hls - streamSegment} Set Referer header to tracking URL for channel %s", r.Channel.Name)
	}

	// Create context with timeout to prevent hanging on slow segments
	ctx, cancel := context.WithTimeout(r.Ctx, 30*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	// Execute request with source-specific headers if available
	var resp *http.Response
	if source != nil {
		logger.Debug("{restream/hls - streamSegment} Using source-specific headers for channel %s", r.Channel.Name)
		resp, err = r.HttpClient.DoWithHeaders(req, source.UserAgent, source.ReqOrigin, source.ReqReferrer)
	} else {
		logger.Debug("{restream/hls - streamSegment} Using default HTTP client for channel %s", r.Channel.Name)
		resp, err = r.HttpClient.Do(req)
	}

	if err != nil {
		logger.Error("{restream/hls - streamSegment} Request failed for channel %s: %v", r.Channel.Name, err)
		return 0, err
	}
	defer resp.Body.Close()

	// Verify successful HTTP response
	if resp.StatusCode != http.StatusOK {
		logger.Error("{restream/hls - streamSegment} HTTP error for channel %s: status %d", r.Channel.Name, resp.StatusCode)
		return 0, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	logger.Debug("{restream/hls - streamSegment} Successfully fetched segment for channel %s, starting data stream", r.Channel.Name)

	// Get buffer from pool for efficient memory usage
	bufPtr := getStreamBuffer()
	buf := *bufPtr
	defer putStreamBuffer(bufPtr)

	// Initialize streaming state
	totalBytes := int64(0)
	lastActivityUpdate := time.Now()
	consecutiveErrors := 0
	maxConsecutiveErrors := 5

	// Stream segment data to clients in chunks
	for {
		// Read chunk from segment
		n, err := resp.Body.Read(buf)
		if n > 0 {
			data := buf[:n]
			totalBytes += int64(n)

			// Attempt to write chunk to ring buffer
			if !r.SafeBufferWrite(data) {
				consecutiveErrors++
				logger.Warn("{restream/hls - streamSegment} Buffer write failed for channel %s (consecutive: %d/%d)",
					r.Channel.Name, consecutiveErrors, maxConsecutiveErrors)
				if consecutiveErrors >= maxConsecutiveErrors {
					logger.Error("{restream/hls - streamSegment} Max buffer write errors for channel %s", r.Channel.Name)
					return totalBytes, fmt.Errorf("buffer write failed")
				}
				time.Sleep(10 * time.Millisecond)
				continue
			}

			// Reset error counter on successful write
			consecutiveErrors = 0

			// Distribute chunk to all connected clients
			activeClients := r.DistributeToClients(data)
			if activeClients == 0 {
				logger.Warn("{restream/hls - streamSegment} No active clients for channel %s, aborting segment", r.Channel.Name)
				return totalBytes, fmt.Errorf("no active clients")
			}

			// Update activity timestamp periodically
			now := time.Now()
			if now.Sub(lastActivityUpdate) > 5*time.Second {
				r.LastActivity.Store(now.Unix())
				lastActivityUpdate = now
				logger.Debug("{restream/hls - streamSegment} Streaming segment for channel %s: %d bytes transferred to %d clients",
					r.Channel.Name, totalBytes, activeClients)
			}
		}

		// Handle read errors
		if err != nil {
			if err == io.EOF {
				// Segment completely read - success
				logger.Debug("{restream/hls - streamSegment} Completed segment for channel %s: %d bytes total",
					r.Channel.Name, totalBytes)
				return totalBytes, nil
			}

			// Transient error - retry with backoff
			consecutiveErrors++
			if consecutiveErrors >= maxConsecutiveErrors {
				logger.Error("{restream/hls - streamSegment} Max read errors for channel %s: %v (count: %d)",
					r.Channel.Name, err, consecutiveErrors)
				return totalBytes, err
			}

			logger.Warn("{restream/hls - streamSegment} Read error for channel %s: %v (consecutive: %d, retrying...)",
				r.Channel.Name, err, consecutiveErrors)
			time.Sleep(10 * time.Millisecond)
			continue
		}

		// Successful read with no errors - reset counter
		consecutiveErrors = 0
	}
}

// resolveSourceForURL returns the best matching source config and memoizes the result
// to avoid repeated linear scans during per-segment HLS streaming.
func (r *Restream) resolveSourceForURL(streamURL string) *config.SourceConfig {
	if r.SourceCache != nil {
		if cached, ok := r.SourceCache.Load(streamURL); ok && cached != nil {
			return cached
		}
	}

	source := r.Config.GetSourceByURL(streamURL)
	if source == nil {
		r.Channel.Mu.RLock()
		for _, stream := range r.Channel.Streams {
			if strings.Contains(streamURL, stream.Source.URL) || strings.Contains(stream.URL, streamURL) {
				source = stream.Source
				break
			}
		}
		r.Channel.Mu.RUnlock()
	}

	if source != nil && r.SourceCache != nil {
		r.SourceCache.Store(streamURL, source)
	}

	return source
}
