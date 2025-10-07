package restream

import (
	"context"
	"fmt"
	"io"
	"kptv-proxy/work/utils"
	"kptv-proxy/work/types"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// SegmentTracker uses a circular buffer approach to track processed segments
// with bounded memory usage and efficient lookups
type SegmentTracker struct {
	segments    []string       // Circular buffer of segment URLs
	segmentMap  map[string]int // Maps segment URL to position in buffer
	head        int            // Current head position in circular buffer
	maxSize     int            // Maximum number of segments to track
	currentSize int            // Current number of segments in tracker
	mutex       sync.RWMutex   // Protects concurrent access
}

// NewSegmentTracker creates a new segment tracker with bounded memory
func NewSegmentTracker(maxSize int) *SegmentTracker {
	return &SegmentTracker{
		segments:    make([]string, maxSize),
		segmentMap:  make(map[string]int),
		maxSize:     maxSize,
		head:        0,
		currentSize: 0,
	}
}

// HasProcessed checks if a segment has been processed recently
func (st *SegmentTracker) HasProcessed(segmentURL string) bool {
	st.mutex.RLock()
	defer st.mutex.RUnlock()

	_, exists := st.segmentMap[segmentURL]
	return exists
}

// MarkProcessed marks a segment as processed, automatically handling cleanup
func (st *SegmentTracker) MarkProcessed(segmentURL string) {
	st.mutex.Lock()
	defer st.mutex.Unlock()

	// If we're at capacity, remove the oldest segment
	if st.currentSize >= st.maxSize {
		// Remove the segment that's about to be overwritten
		oldSegment := st.segments[st.head]
		if oldSegment != "" {
			delete(st.segmentMap, oldSegment)
		}
	} else {
		st.currentSize++
	}

	// Add new segment at head position
	st.segments[st.head] = segmentURL
	st.segmentMap[segmentURL] = st.head

	// Move head to next position (circular)
	st.head = (st.head + 1) % st.maxSize
}

// Size returns the current number of tracked segments
func (st *SegmentTracker) Size() int {
	st.mutex.RLock()
	defer st.mutex.RUnlock()
	return st.currentSize
}

// Clear removes all tracked segments (useful for cleanup)
func (st *SegmentTracker) Clear() {
	st.mutex.Lock()
	defer st.mutex.Unlock()

	// Clear the map
	st.segmentMap = make(map[string]int)

	// Clear the slice
	for i := range st.segments {
		st.segments[i] = ""
	}

	st.head = 0
	st.currentSize = 0
}

// GetTrackedSegments returns a copy of currently tracked segments (for debugging)
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

// streamHLSSegments implements continuous HLS segment streaming from a master or media playlist URL,
// managing the complete lifecycle of segment discovery, fetching, and distribution to connected clients.
// The function implements adaptive playlist refreshing, memory-efficient segment tracking, and
// aggressive cleanup to prevent memory growth during long-running streaming sessions.
//
// The streaming process continuously polls the HLS playlist for new segments, maintaining a
// processed segment cache to avoid duplicate streaming while implementing memory limits to
// prevent unbounded growth. The function handles both live streams (where new segments
// appear periodically) and static playlists (where segments are processed once).
//
// Memory management is critical for long-running streams, so the function implements:
//   - Maximum segment cache size limits
//   - Periodic cache cleanup to remove old segment references
//   - Efficient segment URL tracking to minimize memory overhead
//
// Parameters:
//   - playlistURL: URL of the HLS playlist containing segment references
//
// Returns:
//   - bool: true if any data was successfully streamed to clients
//   - int64: total number of bytes streamed during the session
func (r *Restream) streamHLSSegments(playlistURL string) (bool, int64) {
	if r.Config.Debug {
		r.Logger.Printf("[HLS_STREAM] Starting for: %s", utils.LogURL(r.Config, playlistURL))
	}

	if r.Config.FFmpegMode {
		return r.streamWithFFmpeg(playlistURL)
	}

	totalBytes := int64(0)
	segmentTracker := NewSegmentTracker(20)
	consecutiveEmptyRefresh := 0
	maxEmptyRefresh := 10
	lastSuccessfulSegment := time.Now()

	for {
		select {
		case <-r.Ctx.Done():
			success := totalBytes > 1024*1024
			if r.Config.Debug {
				r.Logger.Printf("[HLS_STREAM] Context done: %d bytes", totalBytes)
			}
			return success, totalBytes
		default:
		}

		clientCount := 0
		r.Clients.Range(func(key string, value *types.RestreamClient) bool {
			clientCount++
			return true
		})

		if clientCount == 0 {
			if r.Config.Debug {
				r.Logger.Printf("[HLS_STREAM] No clients remaining")
			}
			return totalBytes > 1024*1024, totalBytes
		}

		segments, err := r.getHLSSegments(playlistURL)
		if err != nil {
			if r.Config.Debug {
				r.Logger.Printf("[HLS_STREAM_ERROR] Error getting segments: %v", err)
			}
			return false, totalBytes
		}

		if r.Config.Debug && len(segments) > 0 {
			r.Logger.Printf("[HLS_PLAYLIST_REFRESH] Found %d segments", len(segments))
		}

		newSegmentCount := 0
		segmentErrors := 0

		for _, segmentURL := range segments {
			if segmentTracker.HasProcessed(segmentURL) {
				continue
			}

			select {
			case <-r.Ctx.Done():
				return totalBytes > 1024*1024, totalBytes
			default:
			}

			if r.Config.Debug {
				r.Logger.Printf("[HLS_SEGMENT_NEW] Processing: %s", utils.LogURL(r.Config, segmentURL))
			}

			segmentBytes, err := r.streamSegment(segmentURL, playlistURL)
			if err != nil {
				segmentErrors++
				if r.Config.Debug {
					r.Logger.Printf("[HLS_SEGMENT_ERROR] %v", err)
				}

				if segmentErrors > 5 {
					if r.Config.Debug {
						r.Logger.Printf("[HLS_SEGMENT_ERROR] Too many segment errors")
					}
					return false, totalBytes
				}
				continue
			}

			segmentTracker.MarkProcessed(segmentURL)
			totalBytes += segmentBytes
			newSegmentCount++
			lastSuccessfulSegment = time.Now()
			consecutiveEmptyRefresh = 0

			if r.Config.Debug {
				r.Logger.Printf("[HLS_SEGMENT] Streamed: %d bytes", segmentBytes)
			}
		}

		if newSegmentCount == 0 {
			consecutiveEmptyRefresh++
			if consecutiveEmptyRefresh >= maxEmptyRefresh {
				timeSinceSuccess := time.Since(lastSuccessfulSegment)
				if timeSinceSuccess > 30*time.Second {
					if r.Config.Debug {
						r.Logger.Printf("[HLS_STALLED] No new segments for %v", timeSinceSuccess)
					}
					return false, totalBytes
				}
			}
		} else {
			consecutiveEmptyRefresh = 0
		}

		if r.Config.Debug && newSegmentCount > 0 {
			r.Logger.Printf("[HLS_BATCH] Streamed %d new segments (tracker size: %d)", newSegmentCount, segmentTracker.Size())
		}

		select {
		case <-r.Ctx.Done():
			return totalBytes > 1024*1024, totalBytes
		case <-time.After(2 * time.Second):
			continue
		}
	}
}

// getHLSSegments fetches and parses an HLS playlist to extract individual segment URLs,
// implementing comprehensive URL resolution for both relative and absolute segment paths.
// The function handles authentication through source-specific headers and resolves
// tracking/redirect URLs that may be embedded in segment references.
//
// The parsing process identifies segment lines (non-comment lines containing URLs) and
// performs intelligent URL resolution to convert relative paths to absolute URLs using
// the playlist's base URL as the resolution context. Special handling is provided for
// tracking URLs that contain redirect parameters requiring extraction and decoding.
//
// Authentication and request handling uses source-specific configuration when available,
// falling back to basic HTTP requests for playlists not associated with configured sources.
// This ensures compatibility with diverse IPTV providers while maintaining security
// through appropriate header handling.
//
// Parameters:
//   - playlistURL: complete URL of the HLS playlist to fetch and parse
//
// Returns:
//   - []string: slice of absolute segment URLs ready for streaming
//   - error: non-nil if playlist cannot be fetched, parsed, or contains no valid segments
func (r *Restream) getHLSSegments(playlistURL string) ([]string, error) {

	// Apply rate limiting before HLS playlist request
	if r.RateLimiter != nil {
		r.RateLimiter.Take()
	}

	// Locate source configuration for authentication headers
	source := r.Config.GetSourceByURL(playlistURL)
	if source == nil {

		// Attempt to find source by matching stream URLs in channel configuration
		r.Channel.Mu.RLock()
		for _, stream := range r.Channel.Streams {
			if strings.Contains(playlistURL, stream.Source.URL) || strings.Contains(stream.URL, playlistURL) {
				source = stream.Source
				break
			}
		}
		r.Channel.Mu.RUnlock()
	}

	// Build HTTP request with appropriate timeout for playlist fetching
	req, err := http.NewRequest("GET", playlistURL, nil)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(r.Ctx, 10*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	// Execute request with source-specific headers if available
	var resp *http.Response
	if source != nil {

		// Use source-specific authentication and headers
		resp, err = r.HttpClient.DoWithHeaders(req, source.UserAgent, source.ReqOrigin, source.ReqReferrer)
	} else {

		// Fallback to basic HTTP request without custom headers
		resp, err = r.HttpClient.Do(req)
	}

	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Verify successful HTTP response before processing content
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	// Read complete playlist content for parsing
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

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
			} else {

				// Relative URL - resolve against playlist base URL
				baseURL := playlistURL[:strings.LastIndex(playlistURL, "/")]
				segmentURL = baseURL + "/" + line
			}

			// Check for tracking/beacon URLs requiring redirect resolution
			if resolvedURL := r.resolveRedirectURL(segmentURL); resolvedURL != "" {
				segmentURL = resolvedURL
				if r.Config.Debug {
					r.Logger.Printf("[HLS_REDIRECT] Resolved tracking URL to: %s", utils.LogURL(r.Config, segmentURL))
				}
			}

			segments = append(segments, segmentURL)
		}
	}

	return segments, nil
}

// resolveRedirectURL extracts and decodes actual stream URLs from tracking/beacon URLs
// commonly used in IPTV sources for analytics and access control. The function handles
// multiple URL encoding patterns and parameter structures used by different providers
// to embed actual stream URLs within tracking mechanisms.
//
// The resolution process examines URL parameters for redirect_url specifications and
// performs URL decoding to extract the actual streamable URL. This handling is essential
// for IPTV sources that use intermediary tracking services before redirecting to actual
// content servers.
//
// Common patterns handled include:
//   - redirect_url parameter containing URL-encoded destination
//   - beacon URLs with embedded redirect parameters
//   - Multiple levels of URL encoding requiring iterative decoding
//
// Parameters:
//   - segmentURL: potentially tracking URL containing embedded redirect information
//
// Returns:
//   - string: resolved actual stream URL if extraction successful, empty string otherwise
func (r *Restream) resolveRedirectURL(segmentURL string) string {

	// Handle standard redirect_url parameter pattern
	if strings.Contains(segmentURL, "redirect_url=") {
		if parsedURL, err := url.Parse(segmentURL); err == nil {
			if redirectURL := parsedURL.Query().Get("redirect_url"); redirectURL != "" {
				if decodedURL, err := url.QueryUnescape(redirectURL); err == nil {
					return decodedURL
				}
			}
		}
	}

	// Handle beacon URLs with embedded redirect parameters
	if strings.Contains(segmentURL, "/beacon/") && strings.Contains(segmentURL, "redirect_url") {
		if parsedURL, err := url.Parse(segmentURL); err == nil {
			if redirectURL := parsedURL.Query().Get("redirect_url"); redirectURL != "" {
				if decodedURL, err := url.QueryUnescape(redirectURL); err == nil {
					return decodedURL
				}
			}
		}
	}

	// Return empty string if no redirect pattern found or resolution failed
	return ""
}

// streamSegment fetches and distributes a single HLS segment to all connected clients,
// implementing efficient data streaming with proper error handling and byte counting.
// The function handles authentication through source-specific headers, redirect resolution
// for tracking URLs, and implements chunked reading for memory-efficient processing
// of large segment files.
//
// The streaming process reads segment data in configurable chunks, distributing each
// chunk to clients immediately while maintaining accurate byte count statistics.
// Special handling is provided for tracking URLs that require redirect resolution,
// with appropriate referrer headers to maintain compatibility with analytics systems.
//
// Buffer management uses the configured buffer size to balance memory usage with
// streaming performance, while client distribution ensures all connected clients
// receive identical data streams for synchronized playback.
//
// Parameters:
//   - segmentURL: complete URL of the segment to fetch and stream
//   - playlistURL: original playlist URL for source configuration lookup and referrer headers
//
// Returns:
//   - int64: total number of bytes successfully streamed to clients
//   - error: non-nil if segment cannot be fetched or streaming fails
func (r *Restream) streamSegment(segmentURL, playlistURL string) (int64, error) {
	if r.RateLimiter != nil {
		r.RateLimiter.Take()
	}

	source := r.Config.GetSourceByURL(playlistURL)
	if source == nil {
		r.Channel.Mu.RLock()
		for _, stream := range r.Channel.Streams {
			if strings.Contains(playlistURL, stream.Source.URL) || strings.Contains(stream.URL, playlistURL) {
				source = stream.Source
				break
			}
		}
		r.Channel.Mu.RUnlock()
	}

	originalURL := segmentURL
	if resolvedURL := r.resolveRedirectURL(segmentURL); resolvedURL != "" {
		segmentURL = resolvedURL
		if r.Config.Debug {
			r.Logger.Printf("[HLS_SEGMENT_REDIRECT] Using: %s", utils.LogURL(r.Config, segmentURL))
		}
	}

	req, err := http.NewRequest("GET", segmentURL, nil)
	if err != nil {
		return 0, err
	}

	if originalURL != segmentURL {
		req.Header.Set("Referer", originalURL)
	}

	ctx, cancel := context.WithTimeout(r.Ctx, 30*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	var resp *http.Response
	if source != nil {
		resp, err = r.HttpClient.DoWithHeaders(req, source.UserAgent, source.ReqOrigin, source.ReqReferrer)
	} else {
		resp, err = r.HttpClient.Do(req)
	}

	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	// Use 32KB buffer for live streams
	buf := make([]byte, 32*1024)
	totalBytes := int64(0)
	lastActivityUpdate := time.Now()
	consecutiveErrors := 0
	maxConsecutiveErrors := 5

	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			data := buf[:n]
			totalBytes += int64(n)

			if !r.SafeBufferWrite(data) {
				consecutiveErrors++
				if consecutiveErrors >= maxConsecutiveErrors {
					return totalBytes, fmt.Errorf("buffer write failed")
				}
				time.Sleep(10 * time.Millisecond)
				continue
			}

			consecutiveErrors = 0
			activeClients := r.DistributeToClients(data)
			if activeClients == 0 {
				return totalBytes, fmt.Errorf("no active clients")
			}

			now := time.Now()
			if now.Sub(lastActivityUpdate) > 5*time.Second {
				r.LastActivity.Store(now.Unix())
				lastActivityUpdate = now
			}
		}

		if err != nil {
			if err == io.EOF {
				return totalBytes, nil
			}
			consecutiveErrors++
			if consecutiveErrors >= maxConsecutiveErrors {
				return totalBytes, err
			}
			time.Sleep(10 * time.Millisecond)
			continue
		}

		consecutiveErrors = 0
	}
}
