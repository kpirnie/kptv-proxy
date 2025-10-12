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
	"kptv-proxy/work/streamorder"
	"kptv-proxy/work/cache"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"
	"encoding/json"

	"github.com/grafana/regexp"
	"github.com/grafov/m3u8"
	"go.uber.org/ratelimit"
)

// Content type detection regexes - should match the ones used in XC API parsing
var (
	seriesRegex = regexp.MustCompile(`(?i)24\/7|247|\/series\/|\/shows\/|\/show\/`)
	vodRegex    = regexp.MustCompile(`(?i)\/vods\/|\/vod\/|\/movies\/|\/movie\/`)
)

// classifyStreamContent applies the same content classification logic as XC API parsing
func classifyStreamContent(streamName, streamURL string, existingGroup string) string {
	// If there's already a meaningful group-title, preserve it unless we have a better classification
	if existingGroup != "" && !strings.EqualFold(existingGroup, "uncategorized") {
		// Still check if our regex patterns suggest a different classification
		seriesNameMatch := seriesRegex.MatchString(streamName)
		vodNameMatch := vodRegex.MatchString(streamName)
		seriesURLMatch := seriesRegex.MatchString(streamURL)
		vodURLMatch := vodRegex.MatchString(streamURL)

		if seriesNameMatch || seriesURLMatch {
			return "series"
		}
		if vodNameMatch || vodURLMatch {
			return "vod"
		}

		// Keep existing group if our patterns don't match
		return existingGroup
	}

	// Apply regex-based classification
	seriesNameMatch := seriesRegex.MatchString(streamName)
	vodNameMatch := vodRegex.MatchString(streamName)
	seriesURLMatch := seriesRegex.MatchString(streamURL)
	vodURLMatch := vodRegex.MatchString(streamURL)

	if seriesNameMatch || seriesURLMatch {
		return "series"
	}
	if vodNameMatch || vodURLMatch {
		return "vod"
	}

	// Default to live
	return "live"
}

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
func ParseM3U8(httpClient *client.HeaderSettingClient, logger *log.Logger, cfg *config.Config, source *config.SourceConfig, rateLimiter ratelimit.Limiter, cache *cache.Cache) []*types.Stream {
	if cfg.Debug {
		logger.Printf("Parsing M3U8 from %s", utils.LogURL(cfg, source.URL))
	}

	cacheKey := fmt.Sprintf("m3u8:%s", source.URL)
	if cached, found := cache.GetXCData(cacheKey); found {
		if cfg.Debug {
			logger.Printf("[M3U8_CACHE_HIT] Using cached M3U8 data for %s", source.Name)
		}
		var streams []*types.Stream
		if err := json.Unmarshal([]byte(cached), &streams); err == nil {
			return streams
		}
	}

	if rateLimiter != nil {
		rateLimiter.Take()
		if cfg.Debug {
			logger.Printf("[RATE_LIMIT] Applied rate limit for source: %s", source.Name)
		}
	}

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

	resp, err := httpClient.DoWithHeaders(req, source.UserAgent, source.ReqOrigin, source.ReqReferrer)
	if err != nil {
		if cfg.Debug {
			logger.Printf("Error fetching M3U8 from %s: %v", utils.LogURL(cfg, source.URL), err)
		}
		return nil
	}

	defer func() {
		resp.Body.Close()
		if cfg.Debug {
			logger.Printf("[PARSE_CONNECTION_CLOSE] Closed connection for: %s", utils.LogURL(cfg, source.URL))
		}
	}()

	if resp.StatusCode != http.StatusOK {
		if cfg.Debug {
			logger.Printf("HTTP error %d when fetching %s", resp.StatusCode, utils.LogURL(cfg, source.URL))
		}
		return nil
	}

	var streams []*types.Stream
	playlist, listType, err := m3u8.DecodeFrom(bufio.NewReader(resp.Body), true)
	if err == nil {
		if cfg.Debug {
			logger.Printf("Successfully parsed with grafov parser: %s", utils.LogURL(cfg, source.URL))
		}
		streams = ParseWithGrafov(playlist, listType, source, cfg, logger)
	} else {
		if cfg.Debug {
			logger.Printf("Grafov parser failed, using fallback parser: %v", err)
		}

		resp.Body.Close()

		req2, err := http.NewRequest("GET", source.URL, nil)
		if err != nil {
			if cfg.Debug {
				logger.Printf("Error creating fallback request for %s: %v", utils.LogURL(cfg, source.URL), err)
			}
			return nil
		}
		req2 = req2.WithContext(ctx)

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

		if resp2.StatusCode != http.StatusOK {
			if cfg.Debug {
				logger.Printf("HTTP error %d on fallback fetch from %s", resp2.StatusCode, utils.LogURL(cfg, source.URL))
			}
			return nil
		}

		if cfg.Debug {
			logger.Printf("Using fallback parser for: %s", utils.LogURL(cfg, source.URL))
		}

		streams = ParseM3U8Fallback(resp2.Body, source, cfg, logger)
	}

	if len(streams) > 0 {
		if data, err := json.Marshal(streams); err == nil {
			cache.SetXCData(cacheKey, string(data))
			if cfg.Debug {
				logger.Printf("[M3U8_CACHE_SET] Cached %d streams for %s", len(streams), source.Name)
			}
		}
	}

	return streams
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
		stream := &types.Stream{
			URL:        source.URL,
			Name:       "Direct Stream",
			Source:     source,
			Attributes: make(map[string]string),
		}

		group := classifyStreamContent(stream.Name, stream.URL, "")
		stream.Attributes["group-title"] = group

		streams = append(streams, stream)

	case m3u8.MASTER:
		masterpl := playlist.(*m3u8.MasterPlaylist)
		for _, variant := range masterpl.Variants {
			if variant == nil {
				break
			}

			name := variant.Name
			if name == "" && variant.Resolution != "" {
				name = fmt.Sprintf("Stream_%s", variant.Resolution)
			} else if name == "" {
				name = fmt.Sprintf("Stream_%d", variant.Bandwidth)
			}

			stream := &types.Stream{
				URL:        variant.URI,
				Name:       name,
				Source:     source,
				Attributes: make(map[string]string),
			}

			if variant.Bandwidth > 0 {
				stream.Attributes["bandwidth"] = fmt.Sprintf("%d", variant.Bandwidth)
			}

			if variant.Resolution != "" {
				stream.Attributes["resolution"] = variant.Resolution
			}

			group := classifyStreamContent(stream.Name, stream.URL, "")
			stream.Attributes["group-title"] = group

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

			// Apply content classification, preserving existing group-title if meaningful
			existingGroup := currentAttrs["group-title"]
			if existingGroup == "" {
				existingGroup = currentAttrs["tvg-group"] // Also check tvg-group
			}

			group := classifyStreamContent(stream.Name, stream.URL, existingGroup)
			stream.Attributes["group-title"] = group

			if cfg.Debug {
				logger.Printf("Classified stream '%s' as '%s' (URL: %s)", stream.Name, group, utils.LogURL(cfg, stream.URL))
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
	if cfg.Debug && len(streams) > 1 {
		log.Printf("[SORT_BEFORE] Channel %s: Sorting %d streams", channelName, len(streams))
	}

	// Check if there's a custom order for this channel
	customOrder, err := streamorder.GetChannelStreamOrder(channelName)
	if err == nil && customOrder != nil && len(customOrder) == len(streams) {
		// Validate that all indices in customOrder are valid
		valid := true
		usedIndices := make(map[int]bool)
		for _, idx := range customOrder {
			if idx < 0 || idx >= len(streams) || usedIndices[idx] {
				valid = false
				break
			}
			usedIndices[idx] = true
		}
		
		if valid {
			if cfg.Debug {
				log.Printf("[SORT_CUSTOM] Channel %s: Applying custom order %v", channelName, customOrder)
			}
			
			// Create a new slice with the custom order
			orderedStreams := make([]*types.Stream, len(streams))
			for newPosition, originalIndex := range customOrder {
				orderedStreams[newPosition] = streams[originalIndex]
			}
			
			// Copy back to original slice
			copy(streams, orderedStreams)
			
			if cfg.Debug {
				log.Printf("[SORT_AFTER] Channel %s: Applied custom ordering", channelName)
			}
			return
		} else {
			if cfg.Debug {
				log.Printf("[SORT_CUSTOM] Channel %s: Invalid custom order, falling back to default", channelName)
			}
		}
	}

	// ORIGINAL DEFAULT SORTING LOGIC
	sort.SliceStable(streams, func(i, j int) bool {
		stream1 := streams[i]
		stream2 := streams[j]

		if stream1.Source.Order != stream2.Source.Order {
			return stream1.Source.Order < stream2.Source.Order
		}

		val1 := stream1.Attributes[cfg.SortField]
		val2 := stream2.Attributes[cfg.SortField]

		if cfg.SortDirection == "desc" {
			return val1 > val2
		}
		return val1 < val2
	})

	if cfg.Debug && len(streams) > 1 {
		log.Printf("[SORT_AFTER] Channel %s: Applied default sorting", channelName)
	}
}
