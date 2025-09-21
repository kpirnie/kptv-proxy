package parser

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"kptv-proxy/work/client"
	"kptv-proxy/work/config"
	"kptv-proxy/work/types"
	"kptv-proxy/work/utils"
	"log"
	"net/http"
	"time"

	regexp "github.com/grafana/regexp"
	"go.uber.org/ratelimit"
)

// XCLiveStream represents a single live stream entry from the Xtreme Codes API response,
// containing essential metadata for live television channels including stream identification,
// categorization, and EPG (Electronic Program Guide) integration information.
// This structure maps directly to the JSON response format from the get_live_streams endpoint.
type XCLiveStream struct {
	StreamID     int    `json:"stream_id"`      // Unique identifier for the live stream used in stream URL construction
	Name         string `json:"name"`           // Display name of the live channel for user interfaces and playlists
	CategoryID   string `json:"category_id"`    // Category identifier for grouping related channels
	StreamIcon   string `json:"stream_icon"`    // URL to channel logo/icon image for display purposes
	EpgChannelID string `json:"epg_channel_id"` // EPG channel identifier for program guide integration
}

// XCSeries represents a single series entry from the Xtreme Codes API response,
// containing metadata for television series and episodic content including identification,
// categorization, and artwork information. This structure maps to the JSON response
// format from the get_series endpoint.
type XCSeries struct {
	SeriesID   int    `json:"series_id"`   // Unique identifier for the series used in stream URL construction
	Name       string `json:"name"`        // Display name of the series for user interfaces and playlists
	CategoryID string `json:"category_id"` // Category identifier for grouping related series content
	Cover      string `json:"cover"`       // URL to series cover artwork/poster image for display purposes
}

// XCVODStream represents a single video-on-demand stream entry from the Xtreme Codes API response,
// containing metadata for movies and other on-demand video content including identification,
// categorization, artwork, and format information. This structure maps to the JSON response
// format from the get_vod_streams endpoint.
type XCVODStream struct {
	StreamID           int    `json:"stream_id"`           // Unique identifier for the VOD stream used in stream URL construction
	Name               string `json:"name"`                // Display name of the video content for user interfaces and playlists
	CategoryID         string `json:"category_id"`         // Category identifier for grouping related video content
	StreamIcon         string `json:"stream_icon"`         // URL to video thumbnail/poster image for display purposes
	ContainerExtension string `json:"container_extension"` // File format extension (mp4, mkv, etc.) for container type identification
}

// ParseXtremeCodesAPI fetches and parses content from all three Xtreme Codes API endpoints
// (live streams, series, and VOD), aggregating the results into a unified stream collection
// with proper URL construction and metadata mapping. This function serves as the primary
// entry point for Xtreme Codes API integration, replacing standard M3U8 parsing when
// authentication credentials are available.
//
// The parsing process implements comprehensive error handling, rate limiting, and debug
// logging while constructing appropriate stream URLs for each content type using the
// Xtreme Codes URL format specifications. All streams are properly categorized with
// group-title attributes matching their content type for playlist organization.
//
// Parameters:
//   - httpClient: configured HTTP client for API requests with header support
//   - logger: application logger for debugging and progress reporting
//   - cfg: application configuration containing debug settings and URL obfuscation preferences
//   - source: source configuration with URL, credentials, and connection parameters
//   - rateLimiter: rate limiter for controlling API request frequency to prevent server overload
//
// Returns:
//   - []*types.Stream: aggregated collection of streams from all three API endpoints
func ParseXtremeCodesAPI(httpClient *client.HeaderSettingClient, logger *log.Logger, cfg *config.Config, source *config.SourceConfig, rateLimiter ratelimit.Limiter) []*types.Stream {
	if cfg.Debug {
		logger.Printf("Parsing Xtreme Codes API from %s with early filtering", utils.LogURL(cfg, source.URL))
	}

	// Pre-compile regex patterns for efficiency
	var liveInclude, liveExclude, seriesInclude, seriesExclude, vodInclude, vodExclude *regexp.Regexp
	var err error

	if source.LiveIncludeRegex != "" {
		liveInclude, err = regexp.Compile(source.LiveIncludeRegex)
		if err != nil && cfg.Debug {
			logger.Printf("Invalid LiveIncludeRegex: %v", err)
		}
	}
	if source.LiveExcludeRegex != "" {
		liveExclude, err = regexp.Compile(source.LiveExcludeRegex)
		if err != nil && cfg.Debug {
			logger.Printf("Invalid LiveExcludeRegex: %v", err)
		}
	}
	if source.SeriesIncludeRegex != "" {
		seriesInclude, err = regexp.Compile(source.SeriesIncludeRegex)
		if err != nil && cfg.Debug {
			logger.Printf("Invalid SeriesIncludeRegex: %v", err)
		}
	}
	if source.SeriesExcludeRegex != "" {
		seriesExclude, err = regexp.Compile(source.SeriesExcludeRegex)
		if err != nil && cfg.Debug {
			logger.Printf("Invalid SeriesExcludeRegex: %v", err)
		}
	}
	if source.VODIncludeRegex != "" {
		vodInclude, err = regexp.Compile(source.VODIncludeRegex)
		if err != nil && cfg.Debug {
			logger.Printf("Invalid VODIncludeRegex: %v", err)
		}
	}
	if source.VODExcludeRegex != "" {
		vodExclude, err = regexp.Compile(source.VODExcludeRegex)
		if err != nil && cfg.Debug {
			logger.Printf("Invalid VODExcludeRegex: %v", err)
		}
	}

	// Content type detection regexes
	seriesRegex := regexp.MustCompile(`(?i)24\/7|247|\/series\/|\/shows\/|\/show\/`)
	vodRegex := regexp.MustCompile(`(?i)\/vods\/|\/vod\/|\/movies\/|\/movie\/`)

	// Initialize collection for filtered streams
	var allStreams []*types.Stream
	filteredCount := 0
	skippedCount := 0

	// Helper function to check if a name passes include/exclude filters
	checkFilters := func(name string, include, exclude *regexp.Regexp) bool {
		if include != nil && !include.MatchString(name) {
			return false // Doesn't match required include pattern
		}
		if exclude != nil && exclude.MatchString(name) {
			return false // Matches exclude pattern
		}
		return true // Passed all filters
	}

	// Process live streams with early filtering
	if cfg.Debug {
		logger.Printf("Fetching and filtering live streams from XC API")
	}
	
	liveStreams := fetchXCLiveStreams(httpClient, logger, cfg, source, rateLimiter)
	for _, stream := range liveStreams {
		// Apply early filtering BEFORE creating Stream objects
		if !checkFilters(stream.Name, liveInclude, liveExclude) {
			skippedCount++
			continue
		}

		// Only create Stream object if it passes filters
		streamURL := fmt.Sprintf("%s/live/%s/%s/%d.ts", source.URL, source.Username, source.Password, stream.StreamID)
		
		// Determine group classification
		group := "live"
		if seriesRegex.MatchString(stream.Name) || seriesRegex.MatchString(streamURL) {
			group = "series"
		} else if vodRegex.MatchString(stream.Name) || vodRegex.MatchString(streamURL) {
			group = "vod"
		}

		s := &types.Stream{
			URL:    streamURL,
			Name:   stream.Name,
			Source: source,
			Attributes: map[string]string{
				"tvg-name":    stream.Name,
				"group-title": group,
				"tvg-id":      fmt.Sprintf("%d", stream.StreamID),
				"category-id": stream.CategoryID,
			},
		}

		if stream.StreamIcon != "" {
			s.Attributes["tvg-logo"] = stream.StreamIcon
		}
		if stream.EpgChannelID != "" {
			s.Attributes["tvg-id"] = stream.EpgChannelID
		}

		allStreams = append(allStreams, s)
		filteredCount++
	}

	if cfg.Debug {
		logger.Printf("Live streams: %d kept, %d filtered out", filteredCount, skippedCount)
	}

	// Process series with early filtering
	if cfg.Debug {
		logger.Printf("Fetching and filtering series from XC API")
	}
	
	seriesFiltered := 0
	seriesSkipped := 0
	
	series := fetchXCSeries(httpClient, logger, cfg, source, rateLimiter)
	for _, serie := range series {
		// Apply early filtering
		if !checkFilters(serie.Name, seriesInclude, seriesExclude) {
			seriesSkipped++
			continue
		}

		streamURL := fmt.Sprintf("%s/series/%s/%s/%d.ts", source.URL, source.Username, source.Password, serie.SeriesID)
		
		s := &types.Stream{
			URL:    streamURL,
			Name:   serie.Name,
			Source: source,
			Attributes: map[string]string{
				"tvg-name":    serie.Name,
				"group-title": "series",
				"tvg-id":      fmt.Sprintf("%d", serie.SeriesID),
				"category-id": serie.CategoryID,
			},
		}

		if serie.Cover != "" {
			s.Attributes["tvg-logo"] = serie.Cover
		}

		allStreams = append(allStreams, s)
		seriesFiltered++
	}

	if cfg.Debug {
		logger.Printf("Series: %d kept, %d filtered out", seriesFiltered, seriesSkipped)
	}

	// Process VOD streams with early filtering
	if cfg.Debug {
		logger.Printf("Fetching and filtering VOD streams from XC API")
	}
	
	vodFiltered := 0
	vodSkipped := 0
	
	vodStreams := fetchXCVODStreams(httpClient, logger, cfg, source, rateLimiter)
	for _, stream := range vodStreams {
		// Apply early filtering
		if !checkFilters(stream.Name, vodInclude, vodExclude) {
			vodSkipped++
			continue
		}

		streamURL := fmt.Sprintf("%s/movie/%s/%s/%d.ts", source.URL, source.Username, source.Password, stream.StreamID)
		
		s := &types.Stream{
			URL:    streamURL,
			Name:   stream.Name,
			Source: source,
			Attributes: map[string]string{
				"tvg-name":    stream.Name,
				"group-title": "vod",
				"tvg-id":      fmt.Sprintf("%d", stream.StreamID),
				"category-id": stream.CategoryID,
			},
		}

		if stream.StreamIcon != "" {
			s.Attributes["tvg-logo"] = stream.StreamIcon
		}

		allStreams = append(allStreams, s)
		vodFiltered++
	}

	if cfg.Debug {
		logger.Printf("VOD streams: %d kept, %d filtered out", vodFiltered, vodSkipped)
		logger.Printf("XC API filtering complete: %d total streams kept, %d filtered out", 
			len(allStreams), skippedCount+seriesSkipped+vodSkipped)
	}

	return allStreams
}


// fetchXCLiveStreams retrieves live television stream data from the Xtreme Codes API
// get_live_streams endpoint, implementing proper rate limiting, error handling, and
// debug logging. The function constructs the appropriate API URL with authentication
// parameters and delegates to the generic data fetching function for HTTP operations.
//
// Parameters:
//   - httpClient: configured HTTP client for API requests with header support
//   - logger: application logger for debugging and error reporting
//   - cfg: application configuration for debug logging control
//   - source: source configuration containing URL, credentials, and request parameters
//   - rateLimiter: rate limiter for controlling API request frequency
//
// Returns:
//   - []XCLiveStream: array of live stream objects from API response, or nil on error
func fetchXCLiveStreams(httpClient *client.HeaderSettingClient, logger *log.Logger, cfg *config.Config, source *config.SourceConfig, rateLimiter ratelimit.Limiter) []XCLiveStream {

	// Apply rate limiting before making API request to prevent server overload
	if rateLimiter != nil {
		rateLimiter.Take()
		if cfg.Debug {
			logger.Printf("Applied rate limit for XC live streams request: %s", source.Name)
		}
	}

	// Construct API URL for live streams endpoint with authentication parameters
	url := fmt.Sprintf("%s/player_api.php?username=%s&password=%s&action=get_live_streams", source.URL, source.Username, source.Password)

	// Execute generic API data fetching with proper error handling
	streams, err := fetchXCData[XCLiveStream](httpClient, logger, cfg, source, url)
	if err != nil {
		if cfg.Debug {
			logger.Printf("Failed to fetch XC live streams from %s: %v", utils.LogURL(cfg, source.URL), err)
		}
		return nil
	}

	if cfg.Debug {
		logger.Printf("Successfully fetched %d live streams from XC API", len(streams))
	}

	return streams
}

// fetchXCSeries retrieves television series data from the Xtreme Codes API
// get_series endpoint, implementing proper rate limiting, error handling, and
// debug logging. The function constructs the appropriate API URL with authentication
// parameters and delegates to the generic data fetching function for HTTP operations.
//
// Parameters:
//   - httpClient: configured HTTP client for API requests with header support
//   - logger: application logger for debugging and error reporting
//   - cfg: application configuration for debug logging control
//   - source: source configuration containing URL, credentials, and request parameters
//   - rateLimiter: rate limiter for controlling API request frequency
//
// Returns:
//   - []XCSeries: array of series objects from API response, or nil on error
func fetchXCSeries(httpClient *client.HeaderSettingClient, logger *log.Logger, cfg *config.Config, source *config.SourceConfig, rateLimiter ratelimit.Limiter) []XCSeries {

	// Apply rate limiting before making API request to prevent server overload
	if rateLimiter != nil {
		rateLimiter.Take()
		if cfg.Debug {
			logger.Printf("Applied rate limit for XC series request: %s", source.Name)
		}
	}

	// Construct API URL for series endpoint with authentication parameters
	url := fmt.Sprintf("%s/player_api.php?username=%s&password=%s&action=get_series", source.URL, source.Username, source.Password)

	// Execute generic API data fetching with proper error handling
	series, err := fetchXCData[XCSeries](httpClient, logger, cfg, source, url)
	if err != nil {
		if cfg.Debug {
			logger.Printf("Failed to fetch XC series from %s: %v", utils.LogURL(cfg, source.URL), err)
		}
		return nil
	}

	if cfg.Debug {
		logger.Printf("Successfully fetched %d series from XC API", len(series))
	}

	return series
}

// fetchXCVODStreams retrieves video-on-demand stream data from the Xtreme Codes API
// get_vod_streams endpoint, implementing proper rate limiting, error handling, and
// debug logging. The function constructs the appropriate API URL with authentication
// parameters and delegates to the generic data fetching function for HTTP operations.
//
// Parameters:
//   - httpClient: configured HTTP client for API requests with header support
//   - logger: application logger for debugging and error reporting
//   - cfg: application configuration for debug logging control
//   - source: source configuration containing URL, credentials, and request parameters
//   - rateLimiter: rate limiter for controlling API request frequency
//
// Returns:
//   - []XCVODStream: array of VOD stream objects from API response, or nil on error
func fetchXCVODStreams(httpClient *client.HeaderSettingClient, logger *log.Logger, cfg *config.Config, source *config.SourceConfig, rateLimiter ratelimit.Limiter) []XCVODStream {

	// Apply rate limiting before making API request to prevent server overload
	if rateLimiter != nil {
		rateLimiter.Take()
		if cfg.Debug {
			logger.Printf("Applied rate limit for XC VOD streams request: %s", source.Name)
		}
	}

	// Construct API URL for VOD streams endpoint with authentication parameters
	url := fmt.Sprintf("%s/player_api.php?username=%s&password=%s&action=get_vod_streams", source.URL, source.Username, source.Password)

	// Execute generic API data fetching with proper error handling
	streams, err := fetchXCData[XCVODStream](httpClient, logger, cfg, source, url)
	if err != nil {
		if cfg.Debug {
			logger.Printf("Failed to fetch XC VOD streams from %s: %v", utils.LogURL(cfg, source.URL), err)
		}
		return nil
	}

	if cfg.Debug {
		logger.Printf("Successfully fetched %d VOD streams from XC API", len(streams))
	}

	return streams
}

// fetchXCData implements a generic HTTP request handler for Xtreme Codes API endpoints,
// providing consistent error handling, timeout management, and JSON parsing across all
// API operations. The function uses Go generics to support different response types
// while maintaining type safety and reducing code duplication.
//
// The implementation includes comprehensive error handling for network issues, HTTP
// status codes, and JSON parsing failures, ensuring robust operation across diverse
// network conditions and API response variations. Request timeouts prevent hanging
// on unresponsive servers while maintaining reasonable wait times for API responses.
//
// Parameters:
//   - T: generic type parameter representing the expected response structure
//   - httpClient: configured HTTP client for API requests with header support
//   - logger: application logger for debugging and error reporting
//   - cfg: application configuration for debug logging control
//   - source: source configuration containing authentication headers and request parameters
//   - url: complete API endpoint URL with authentication parameters
//
// Returns:
//   - []T: array of parsed response objects of the specified type
//   - error: non-nil if request fails, HTTP error occurs, or JSON parsing fails
func fetchXCData[T any](httpClient *client.HeaderSettingClient, logger *log.Logger, cfg *config.Config, source *config.SourceConfig, url string) ([]T, error) {

	// Create context with reasonable timeout for API requests to prevent hanging
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Build HTTP GET request for the specified API endpoint
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		if cfg.Debug {
			logger.Printf("Failed to create XC API request: %v", err)
		}
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Apply timeout context to prevent hanging on unresponsive servers
	req = req.WithContext(ctx)

	// Execute HTTP request with source-specific headers (User-Agent, Origin, Referer)
	// Note: Username and password are in URL parameters, not HTTP Basic Auth
	resp, err := httpClient.DoWithHeaders(req, source.UserAgent, source.ReqOrigin, source.ReqReferrer)
	if err != nil {
		if cfg.Debug {
			logger.Printf("XC API request failed: %v", err)
		}
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}

	// Ensure response body is always closed to prevent connection leaks
	defer func() {
		resp.Body.Close()
		if cfg.Debug {
			logger.Printf("Closed XC API connection for: %s", utils.LogURL(cfg, source.URL))
		}
	}()

	// Validate HTTP status code before attempting to parse response body
	if resp.StatusCode != http.StatusOK {
		if cfg.Debug {
			logger.Printf("XC API returned HTTP %d for: %s", resp.StatusCode, utils.LogURL(cfg, source.URL))
		}
		return nil, fmt.Errorf("API returned HTTP %d", resp.StatusCode)
	}

	// Read complete response body for JSON parsing
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		if cfg.Debug {
			logger.Printf("Failed to read XC API response body: %v", err)
		}
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Log response size for debugging and monitoring purposes
	if cfg.Debug {
		logger.Printf("XC API response size: %d bytes", len(body))
	}

	// Parse JSON response into the specified generic type array
	var data []T
	if err := json.Unmarshal(body, &data); err != nil {
		if cfg.Debug {
			logger.Printf("Failed to parse XC API JSON response: %v", err)
			// Log first 200 characters of response for debugging malformed JSON
			if len(body) > 0 {
				preview := string(body)
				if len(preview) > 200 {
					preview = preview[:200] + "..."
				}
				logger.Printf("XC API response preview: %s", preview)
			}
		}
		return nil, fmt.Errorf("failed to parse JSON response: %w", err)
	}

	// Log successful parsing result for debugging and monitoring
	if cfg.Debug {
		logger.Printf("Successfully parsed %d items from XC API response", len(data))
	}

	return data, nil
}
