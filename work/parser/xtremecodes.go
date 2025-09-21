package parser

import (
	"context"
	"encoding/json"
	"fmt"
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
		logger.Printf("Parsing Xtreme Codes API from %s with optimized processing", utils.LogURL(cfg, source.URL))
	}

	// Create a context with timeout for the entire parsing operation
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

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

	var allStreams []*types.Stream
	filteredCount := 0
	skippedCount := 0

	// Helper function to check if a name passes include/exclude filters
	checkFilters := func(name string, include, exclude *regexp.Regexp) bool {
		if include != nil && !include.MatchString(name) {
			return false
		}
		if exclude != nil && exclude.MatchString(name) {
			return false
		}
		return true
	}

	// Process live streams
	if cfg.Debug {
		logger.Printf("[XC_DEBUG] Starting live streams fetch")
	}
	
	liveStreams := fetchXCLiveStreams(httpClient, logger, cfg, source, rateLimiter)
	if cfg.Debug {
		logger.Printf("[XC_DEBUG] Fetched %d live streams", len(liveStreams))
	}

	if len(liveStreams) > 0 {
		liveFiltered := make([]*types.Stream, 0, len(liveStreams)/10)
		
		for i, stream := range liveStreams {
			// Aggressive cancellation check every 100 items
			if i%100 == 0 {
				select {
				case <-ctx.Done():
					if cfg.Debug {
						logger.Printf("[XC_DEBUG] Live streams processing cancelled at item %d", i)
					}
					return allStreams
				default:
				}
			}

			if cfg.Debug && i > 0 && i%5000 == 0 {
				logger.Printf("[XC_DEBUG] Live streams processing: %d/%d processed, %d kept", i, len(liveStreams), len(liveFiltered))
			}
			
			if !checkFilters(stream.Name, liveInclude, liveExclude) {
				skippedCount++
				continue
			}

			streamURL := fmt.Sprintf("%s/live/%s/%s/%d.ts", source.URL, source.Username, source.Password, stream.StreamID)
			
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

			liveFiltered = append(liveFiltered, s)
		}
		
		allStreams = append(allStreams, liveFiltered...)
		filteredCount += len(liveFiltered)
		
		if cfg.Debug {
			logger.Printf("[XC_DEBUG] Live streams completed: %d kept, %d filtered out", len(liveFiltered), len(liveStreams)-len(liveFiltered))
		}
	}

	// Check if cancelled before proceeding to series
	select {
	case <-ctx.Done():
		if cfg.Debug {
			logger.Printf("[XC_DEBUG] Context cancelled after live streams")
		}
		return allStreams
	default:
	}

	// Process series
	if cfg.Debug {
		logger.Printf("[XC_DEBUG] Starting series fetch")
	}
	
	series := fetchXCSeries(httpClient, logger, cfg, source, rateLimiter)
	if cfg.Debug {
		logger.Printf("[XC_DEBUG] Fetched %d series", len(series))
	}

	if len(series) > 0 {
		seriesFiltered := make([]*types.Stream, 0, len(series)/10)
		
		for i, serie := range series {
			// Aggressive cancellation check every 100 items
			if i%100 == 0 {
				select {
				case <-ctx.Done():
					if cfg.Debug {
						logger.Printf("[XC_DEBUG] Series processing cancelled at item %d", i)
					}
					return allStreams
				default:
				}
			}

			if cfg.Debug && i > 0 && i%5000 == 0 {
				logger.Printf("[XC_DEBUG] Series processing: %d/%d processed, %d kept", i, len(series), len(seriesFiltered))
			}
			
			if !checkFilters(serie.Name, seriesInclude, seriesExclude) {
				skippedCount++
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

			seriesFiltered = append(seriesFiltered, s)
		}
		
		allStreams = append(allStreams, seriesFiltered...)
		filteredCount += len(seriesFiltered)
		
		if cfg.Debug {
			logger.Printf("[XC_DEBUG] Series completed: %d kept, %d filtered out", len(seriesFiltered), len(series)-len(seriesFiltered))
		}
	}

	if cfg.Debug {
		logger.Printf("[XC_DEBUG] XC API parsing complete: %d total streams kept, %d filtered out", filteredCount, skippedCount)
	}

	return allStreams
}

// fetchXCLiveStreamsWithContext retrieves live television stream data with context support
func fetchXCLiveStreamsWithContext(ctx context.Context, httpClient *client.HeaderSettingClient, logger *log.Logger, cfg *config.Config, source *config.SourceConfig, rateLimiter ratelimit.Limiter) []XCLiveStream {
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
	streams, err := fetchXCDataWithContext[XCLiveStream](ctx, httpClient, logger, cfg, source, url)
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

// fetchXCSeriesWithContext retrieves television series data with context support
func fetchXCSeriesWithContext(ctx context.Context, httpClient *client.HeaderSettingClient, logger *log.Logger, cfg *config.Config, source *config.SourceConfig, rateLimiter ratelimit.Limiter) []XCSeries {
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
	series, err := fetchXCDataWithContext[XCSeries](ctx, httpClient, logger, cfg, source, url)
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

// fetchXCDataWithContext implements context-aware HTTP request handler for Xtreme Codes API endpoints
func fetchXCDataWithContext[T any](ctx context.Context, httpClient *client.HeaderSettingClient, logger *log.Logger, cfg *config.Config, source *config.SourceConfig, url string) ([]T, error) {
	// Create request with the provided context
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		if cfg.Debug {
			logger.Printf("Failed to create XC API request: %v", err)
		}
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Connection", "keep-alive")

	resp, err := httpClient.DoWithHeaders(req, source.UserAgent, source.ReqOrigin, source.ReqReferrer)
	if err != nil {
		if cfg.Debug {
			logger.Printf("XC API request failed: %v", err)
		}
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}

	defer func() {
		resp.Body.Close()
		if cfg.Debug {
			logger.Printf("Closed XC API connection for: %s", utils.LogURL(cfg, source.URL))
		}
	}()

	if resp.StatusCode != http.StatusOK {
		if cfg.Debug {
			logger.Printf("XC API returned HTTP %d for: %s", resp.StatusCode, utils.LogURL(cfg, source.URL))
		}
		return nil, fmt.Errorf("API returned HTTP %d", resp.StatusCode)
	}

	decoder := json.NewDecoder(resp.Body)
	var data []T
	
	if err := decoder.Decode(&data); err != nil {
		if cfg.Debug {
			logger.Printf("Failed to parse XC API JSON response: %v", err)
		}
		return nil, fmt.Errorf("failed to parse JSON response: %w", err)
	}

	if cfg.Debug {
		logger.Printf("Successfully parsed %d items from XC API response", len(data))
	}

	return data, nil
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
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		if cfg.Debug {
			logger.Printf("Failed to create XC API request: %v", err)
		}
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req = req.WithContext(ctx)
	req.Header.Set("Connection", "keep-alive")

	resp, err := httpClient.DoWithHeaders(req, source.UserAgent, source.ReqOrigin, source.ReqReferrer)
	if err != nil {
		if cfg.Debug {
			logger.Printf("XC API request failed: %v", err)
		}
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}

	defer func() {
		resp.Body.Close()
		if cfg.Debug {
			logger.Printf("Closed XC API connection for: %s", utils.LogURL(cfg, source.URL))
		}
	}()

	if resp.StatusCode != http.StatusOK {
		if cfg.Debug {
			logger.Printf("XC API returned HTTP %d for: %s", resp.StatusCode, utils.LogURL(cfg, source.URL))
		}
		return nil, fmt.Errorf("API returned HTTP %d", resp.StatusCode)
	}

	decoder := json.NewDecoder(resp.Body)
	var data []T
	
	if err := decoder.Decode(&data); err != nil {
		if cfg.Debug {
			logger.Printf("Failed to parse XC API JSON response: %v", err)
		}
		return nil, fmt.Errorf("failed to parse JSON response: %w", err)
	}

	if cfg.Debug {
		logger.Printf("Successfully parsed %d items from XC API response", len(data))
	}

	return data, nil
}
