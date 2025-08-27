package parser

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"kptv-proxy/work/client"
	"kptv-proxy/work/config"
	"kptv-proxy/work/types"
	"kptv-proxy/work/utils"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/grafov/m3u8"
	"go.uber.org/ratelimit"
)

// ParseM3U8 fetches and parses an M3U8 playlist from a specified URL, extracting stream information
// using source-specific HTTP headers for authentication and access control. The function implements
// a two-stage parsing strategy: first attempting to use the robust grafov/m3u8 library, then
// falling back to a custom parser if the primary method fails.
//
// The parsing process handles both master playlists (containing multiple variants) and media
// playlists (direct streams), automatically detecting the playlist type and extracting appropriate
// metadata. Network requests use a 30-second timeout to prevent hanging on unresponsive sources,
// and all HTTP connections are properly closed to prevent resource leaks.
//
// Parameters:
//   - httpClient: configured HTTP client with custom header support for source authentication
//   - logger: application logger for debugging and error reporting
//   - cfg: application configuration containing debug settings and URL obfuscation preferences
//   - source: source configuration with URL, headers, and connection parameters
//
// Returns:
//   - []*types.Stream: slice of parsed stream objects, or nil if parsing fails completely
func ParseM3U8(httpClient *client.HeaderSettingClient, logger *log.Logger, cfg *config.Config, source *config.SourceConfig, rateLimiter ratelimit.Limiter) []*types.Stream {
	if cfg.Debug {
		logger.Printf("Parsing M3U8 from %s", utils.LogURL(cfg, source.URL))
	}

	// Apply rate limiting before making request
	if rateLimiter != nil {
		rateLimiter.Take()
		if cfg.Debug {
			logger.Printf("[RATE_LIMIT] Applied rate limit for source: %s", source.Name)
		}
	}

	// Create HTTP request with reasonable timeout for playlist fetching
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequest("GET", source.URL, nil)
	if err != nil {
		if cfg.Debug {
			logger.Printf("Error creating request for %s: %v", utils.LogURL(cfg, source.URL), err)
		}
		return nil
	}
	req = req.WithContext(ctx)

	// Execute request with source-specific authentication headers
	resp, err := httpClient.DoWithHeaders(req, source.UserAgent, source.ReqOrigin, source.ReqReferrer)
	if err != nil {
		if cfg.Debug {
			logger.Printf("Error fetching M3U8 from %s: %v", utils.LogURL(cfg, source.URL), err)
		}
		return nil
	}

	// Ensure HTTP response body is always closed to prevent connection leaks
	defer func() {
		resp.Body.Close()
		if cfg.Debug {
			logger.Printf("[PARSE_CONNECTION_CLOSE] Closed connection for: %s", utils.LogURL(cfg, source.URL))
		}
	}()

	// Reject non-200 HTTP responses as invalid playlists
	if resp.StatusCode != http.StatusOK {
		if cfg.Debug {
			logger.Printf("HTTP error %d when fetching %s", resp.StatusCode, utils.LogURL(cfg, source.URL))
		}
		return nil
	}

	// Attempt primary parsing using grafov/m3u8 library
	playlist, listType, err := m3u8.DecodeFrom(bufio.NewReader(resp.Body), true)
	if err == nil {
		if cfg.Debug {
			logger.Printf("Successfully parsed with grafov parser: %s", utils.LogURL(cfg, source.URL))
		}
		return ParseWithGrafov(playlist, listType, source, cfg, logger)
	}

	// Log grafov parser failure and prepare for fallback parsing
	if cfg.Debug {
		logger.Printf("Grafov parser failed, using fallback parser: %v", err)
	}

	// Close current response body before making new request
	resp.Body.Close()

	// Create second request for fallback parser (grafov consumed the first body)
	req2, err := http.NewRequest("GET", source.URL, nil)
	if err != nil {
		if cfg.Debug {
			logger.Printf("Error creating fallback request for %s: %v", utils.LogURL(cfg, source.URL), err)
		}
		return nil
	}
	req2 = req2.WithContext(ctx)

	// Execute fallback request with same headers as primary attempt
	resp2, err := httpClient.DoWithHeaders(req2, source.UserAgent, source.ReqOrigin, source.ReqReferrer)
	if err != nil {
		if cfg.Debug {
			logger.Printf("Error re-fetching for fallback parser: %v", err)
		}
		return nil
	}
	defer func() {
		resp2.Body.Close()
		if cfg.Debug {
			logger.Printf("[PARSE_FALLBACK_CLOSE] Closed fallback connection for: %s", utils.LogURL(cfg, source.URL))
		}
	}()

	// Verify second request also succeeded
	if resp2.StatusCode != http.StatusOK {
		if cfg.Debug {
			logger.Printf("HTTP error %d on fallback fetch from %s", resp2.StatusCode, utils.LogURL(cfg, source.URL))
		}
		return nil
	}

	if cfg.Debug {
		logger.Printf("Using fallback parser for: %s", utils.LogURL(cfg, source.URL))
	}

	// Execute custom fallback parser on fresh response body
	return ParseM3U8Fallback(resp2.Body, source, cfg, logger)
}

// ParseWithGrafov processes a successfully parsed M3U8 playlist using the grafov/m3u8 library,
// handling both master playlists (containing multiple stream variants) and media playlists
// (direct streamable content). The function extracts comprehensive metadata including bandwidth,
// resolution, and codec information when available from playlist attributes.
//
// For master playlists, each variant becomes a separate Stream object with quality metadata.
// For media playlists, a single Stream object is created representing the direct stream URL.
// All generated Stream objects are associated with the provided source configuration for
// proper authentication and connection management during streaming.
//
// Parameters:
//   - playlist: parsed playlist object from grafov/m3u8 library
//   - listType: detected playlist type (MASTER or MEDIA)
//   - source: source configuration for associating streams with connection parameters
//   - cfg: application configuration for debug logging
//   - logger: application logger for debugging and progress reporting
//
// Returns:
//   - []*types.Stream: slice of Stream objects extracted from the playlist variants or media
func ParseWithGrafov(playlist m3u8.Playlist, listType m3u8.ListType, source *config.SourceConfig, cfg *config.Config, logger *log.Logger) []*types.Stream {
	var streams []*types.Stream

	switch listType {
	case m3u8.MEDIA:

		// Media playlists contain segments - use the original playlist URL as the stream
		stream := &types.Stream{
			URL:        source.URL,
			Name:       "Direct Stream",
			Source:     source,
			Attributes: make(map[string]string),
		}
		streams = append(streams, stream)

	case m3u8.MASTER:

		// Master playlists contain multiple variants - extract each as a separate stream
		masterpl := playlist.(*m3u8.MasterPlaylist)
		for _, variant := range masterpl.Variants {
			if variant == nil {
				break // End of variants list
			}

			// Generate meaningful name from variant metadata
			name := variant.Name
			if name == "" && variant.Resolution != "" {
				name = fmt.Sprintf("Stream_%s", variant.Resolution)
			} else if name == "" {
				name = fmt.Sprintf("Stream_%d", variant.Bandwidth)
			}

			// Create stream object with variant information
			stream := &types.Stream{
				URL:        variant.URI,
				Name:       name,
				Source:     source,
				Attributes: make(map[string]string),
			}

			// Extract and store bandwidth information for quality selection
			if variant.Bandwidth > 0 {
				stream.Attributes["bandwidth"] = fmt.Sprintf("%d", variant.Bandwidth)
			}

			// Store resolution for display and quality selection
			if variant.Resolution != "" {
				stream.Attributes["resolution"] = variant.Resolution
			}

			streams = append(streams, stream)
		}
	}

	if cfg.Debug {
		logger.Printf("Grafov parser found %d streams from %s", len(streams), utils.LogURL(cfg, source.URL))
	}

	return streams
}

// ParseM3U8Fallback provides a custom parsing implementation for M3U8 playlists when the
// primary grafov parser fails. This fallback parser manually scans playlist content line by line,
// extracting EXTINF metadata and associated stream URLs using string processing techniques.
//
// The parser handles the standard M3U8 format where each stream entry consists of:
//  1. An #EXTINF line containing duration and metadata attributes
//  2. A subsequent line containing the actual stream URL (HTTP/HTTPS)
//
// This implementation is more tolerant of format variations and malformed playlists that
// might cause structured parsers to fail, ensuring maximum compatibility with diverse
// IPTV sources that may not strictly follow M3U8 specifications.
//
// Parameters:
//   - reader: IO reader containing the raw M3U8 playlist content
//   - source: source configuration for associating streams with authentication parameters
//   - cfg: application configuration for debug logging and URL obfuscation
//   - logger: application logger for debugging and progress reporting
//
// Returns:
//   - []*types.Stream: slice of Stream objects parsed from playlist content
func ParseM3U8Fallback(reader io.Reader, source *config.SourceConfig, cfg *config.Config, logger *log.Logger) []*types.Stream {
	var streams []*types.Stream
	scanner := bufio.NewScanner(reader)
	var currentAttrs map[string]string
	lineNum := 0

	// Scan playlist content line by line
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())

		// Process EXTINF lines containing stream metadata
		if strings.HasPrefix(line, "#EXTINF:") {
			currentAttrs = ParseEXTINF(line)
			if cfg.Debug {
				logger.Printf("Parsed EXTINF attributes: %+v", currentAttrs)
			}
		} else if currentAttrs != nil && (strings.HasPrefix(line, "http://") || strings.HasPrefix(line, "https://")) {

			// Found stream URL following EXTINF line
			stream := &types.Stream{
				URL:        line,
				Name:       currentAttrs["tvg-name"],
				Attributes: currentAttrs,
				Source:     source,
			}

			// Ensure stream has a valid display name
			if stream.Name == "" {
				stream.Name = "Unknown"
			}

			streams = append(streams, stream)
			if cfg.Debug {
				logger.Printf("Added stream: %s (URL: %s)", stream.Name, utils.LogURL(cfg, stream.URL))
			}

			// Reset attributes for next stream entry
			currentAttrs = nil
		}
	}

	if cfg.Debug {
		logger.Printf("Fallback parser found %d streams from %s", len(streams), utils.LogURL(cfg, source.URL))
	}

	return streams
}

// ParseEXTINF extracts metadata attributes from an M3U8 EXTINF line, which contains stream
// duration, channel name, and various extended attributes used for EPG integration and
// stream categorization. The parser handles both quoted and unquoted attribute values
// and properly separates the channel name from the attribute section.
//
// The EXTINF format follows the pattern:
// #EXTINF:duration [attribute=value]..., "Channel Name"
//
// Common attributes include:
//   - tvg-id: Electronic Program Guide identifier
//   - tvg-name: Display name for the channel
//   - tvg-logo: URL to channel logo image
//   - group-title: Category/group name for channel organization
//   - tvg-group: Alternative group specification
//
// Parameters:
//   - line: complete EXTINF line from the M3U8 playlist
//
// Returns:
//   - map[string]string: parsed attributes with duration, tvg-name, and extended metadata
func ParseEXTINF(line string) map[string]string {

	// setup the attributes
	attrs := make(map[string]string)

	// Remove the #EXTINF: prefix to access content
	line = strings.TrimPrefix(line, "#EXTINF:")

	// Find the last comma that separates attributes from channel name
	// Must account for quoted channel names that may contain commas
	lastComma := -1
	inQuotes := false

	for i := len(line) - 1; i >= 0; i-- {
		if line[i] == '"' {
			inQuotes = !inQuotes
		} else if line[i] == ',' && !inQuotes {
			lastComma = i
			break
		}
	}

	// Return empty attributes if no proper separation found
	if lastComma == -1 {
		return attrs
	}

	// Split line into attribute section and channel name
	attrPart := strings.TrimSpace(line[:lastComma])
	channelName := strings.TrimSpace(line[lastComma+1:])

	// Parse duration and space-separated attributes
	parts := strings.Fields(attrPart)
	if len(parts) > 0 {
		attrs["duration"] = parts[0]
	}

	// Extract key-value pairs from remaining attribute fields
	for i := 1; i < len(parts); i++ {
		part := parts[i]
		if eqIdx := strings.Index(part, "="); eqIdx != -1 {
			key := part[:eqIdx]
			value := strings.Trim(part[eqIdx+1:], "\"")
			attrs[key] = value
		}
	}

	// Store channel name as tvg-name attribute if present
	if channelName != "" {
		attrs["tvg-name"] = channelName
	}

	return attrs
}

// SortStreams organizes a slice of Stream objects according to application configuration
// settings, implementing a two-tier sorting strategy. The primary sort is by source order
// (lower order values indicate higher priority for failover), while the secondary sort
// uses the configured field and direction for quality-based organization.
//
// The sorting process enables predictable stream selection during failover scenarios
// and allows administrators to control quality preferences through configuration.
// Debug logging provides detailed before/after comparisons when multiple streams exist.
//
// Sorting criteria:
//   - Primary: Source order (ascending - lower numbers = higher priority)
//   - Secondary: Configured field (bandwidth, resolution, etc.) in specified direction
//
// Parameters:
//   - streams: slice of Stream objects to sort in-place
//   - cfg: application configuration containing sort field and direction preferences
//   - channelName: channel name for debug logging context
func SortStreams(streams []*types.Stream, cfg *config.Config, channelName string) {

	// Log initial stream order for debugging comparison
	if cfg.Debug && len(streams) > 1 {
		log.Printf("[SORT_BEFORE] Channel %s: Sorting %d streams by source order, then by %s (%s)",
			channelName, len(streams), cfg.SortField, cfg.SortDirection)
		for i, stream := range streams {
			log.Printf("[SORT_BEFORE] Channel %s: Stream %d: Source order=%d, %s=%s, URL=%s",
				channelName, i, stream.Source.Order, cfg.SortField, stream.Attributes[cfg.SortField],
				utils.LogURL(cfg, stream.URL))
		}
	}

	// Perform stable sort with two-tier comparison logic
	sort.SliceStable(streams, func(i, j int) bool {
		stream1 := streams[i]
		stream2 := streams[j]

		// Primary sort: by source order (lower order = higher priority for failover)
		if stream1.Source.Order != stream2.Source.Order {
			return stream1.Source.Order < stream2.Source.Order
		}

		// Secondary sort: by configured field and direction for quality selection
		val1 := stream1.Attributes[cfg.SortField]
		val2 := stream2.Attributes[cfg.SortField]

		if cfg.SortDirection == "desc" {
			return val1 > val2
		}
		return val1 < val2
	})

	// Log final stream order for debugging verification
	if cfg.Debug && len(streams) > 1 {
		log.Printf("[SORT_AFTER] Channel %s: Streams sorted:", channelName)
		for i, stream := range streams {
			log.Printf("[SORT_AFTER] Channel %s: Stream %d: Source order=%d, %s=%s, URL=%s",
				channelName, i, stream.Source.Order, cfg.SortField, stream.Attributes[cfg.SortField],
				utils.LogURL(cfg, stream.URL))
		}
	}
}
