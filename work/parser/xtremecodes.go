package parser

import (
	"context"
	"encoding/json"
	"fmt"
	"kptv-proxy/work/cache"
	"kptv-proxy/work/client"
	"kptv-proxy/work/config"
	"kptv-proxy/work/logger"
	"kptv-proxy/work/types"
	"kptv-proxy/work/utils"
	"net/http"
	"sync"
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

// xcWork represents a batch of live streams to be processed by a worker.
// The worker pool pattern distributes XC API live stream processing across
// multiple goroutines for improved CPU utilization and reduced processing time.
type xcWork struct {
	streams []XCLiveStream
	start   int
}

// xcSeriesWork represents a batch of series to be processed by a worker.
// Similar to xcWork but specifically for series content, allowing different
// processing logic while maintaining the same worker pool pattern.
type xcSeriesWork struct {
	series []XCSeries
	start  int
}

// processLiveBatchWorker processes a batch of XC live streams, applying regex filters
// and converting XC API responses to internal Stream objects. This function is designed
// to be called by worker goroutines in a concurrent processing pool.
//
// The function applies include/exclude filters, classifies content type (live/series/vod),
// constructs proper stream URLs, and populates stream metadata from XC API responses.
//
// Parameters:
//   - batch: slice of XCLiveStream objects to process
//   - liveInclude: optional regex pattern - only streams matching this pattern are included
//   - liveExclude: optional regex pattern - streams matching this pattern are excluded
//   - source: source configuration containing credentials and connection parameters
//
// Returns:
//   - []*types.Stream: slice of processed streams ready for channel aggregation
func processLiveBatchWorker(batch []XCLiveStream, liveInclude, liveExclude *regexp.Regexp, source *config.SourceConfig) []*types.Stream {
	results := make([]*types.Stream, 0, len(batch))
	logger.Debug("{parser/xtremecodes - processLiveBatchWorker} process the live batch")

	// loop the stream objects
	for _, stream := range batch {
		if liveInclude != nil && !liveInclude.MatchString(stream.Name) {
			continue
		}
		if liveExclude != nil && liveExclude.MatchString(stream.Name) {
			continue
		}

		// set the stream URL
		streamURL := fmt.Sprintf("%s/live/%s/%s/%d.ts", source.URL, source.Username, source.Password, stream.StreamID)

		// setup the group
		group := "live"
		if utils.SeriesRegex.MatchString(stream.Name) || utils.SeriesRegex.MatchString(streamURL) {
			group = "series"
		} else if utils.VodRegex.MatchString(stream.Name) || utils.VodRegex.MatchString(streamURL) {
			group = "vod"
		}

		// create the stream group
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

		// if there's a logo or tvg-id
		if stream.StreamIcon != "" {
			s.Attributes["tvg-logo"] = stream.StreamIcon
		}
		if stream.EpgChannelID != "" {
			s.Attributes["tvg-id"] = stream.EpgChannelID
		}
		logger.Debug("{parser/xtremecodes - processLiveBatchWorker} process the live stream %v", stream.Name)
		results = append(results, s)
	}
	logger.Debug("{parser/xtremecodes - processLiveBatchWorker} live batch results")
	return results
}

// processSeriesBatchWorker processes a batch of XC series, applying regex filters
// and converting XC API responses to internal Stream objects. This function is designed
// to be called by worker goroutines in a concurrent processing pool.
//
// The function applies include/exclude filters specific to series content, constructs
// proper series URLs, and populates metadata from XC API responses.
//
// Parameters:
//   - batch: slice of XCSeries objects to process
//   - seriesInclude: optional regex pattern - only series matching this pattern are included
//   - seriesExclude: optional regex pattern - series matching this pattern are excluded
//   - source: source configuration containing credentials and connection parameters
//
// Returns:
//   - []*types.Stream: slice of processed series streams ready for channel aggregation
func processSeriesBatchWorker(batch []XCSeries, seriesInclude, seriesExclude *regexp.Regexp, source *config.SourceConfig) []*types.Stream {
	results := make([]*types.Stream, 0, len(batch))
	logger.Debug("{parser/xtremecodes - processSeriesBatchWorker} process the series batch")

	// loop the stream objects
	for _, serie := range batch {
		if seriesInclude != nil && !seriesInclude.MatchString(serie.Name) {
			continue
		}
		if seriesExclude != nil && seriesExclude.MatchString(serie.Name) {
			continue
		}

		// setup the stream url
		streamURL := fmt.Sprintf("%s/series/%s/%s/%d.ts", source.URL, source.Username, source.Password, serie.SeriesID)

		// setup the stream
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
		// if theres a logo
		if serie.Cover != "" {
			s.Attributes["tvg-logo"] = serie.Cover
		}
		logger.Debug("{parser/xtremecodes - processSeriesBatchWorker} process the live stream %v", serie.Name)
		results = append(results, s)
	}
	logger.Debug("{parser/xtremecodes - processSeriesBatchWorker} series batch results")
	return results
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
func ParseXtremeCodesAPI(httpClient *client.HeaderSettingClient, cfg *config.Config, source *config.SourceConfig, rateLimiter ratelimit.Limiter, cache *cache.Cache) []*types.Stream {
	logger.Debug("{parser/xtremecodes - ParseXtremeCodesAPI} from %s with optimized batch processing", utils.LogURL(cfg, source.URL))

	cacheKey := fmt.Sprintf("xc:%s:%s:%s", source.URL, source.Username, source.Password)
	if cached, found := cache.GetXCData(cacheKey); found {
		logger.Debug("{parser/xtremecodes - ParseXtremeCodesAPI} Using cached XC API data for %s", source.Name)
		var streams []*types.Stream
		if err := json.Unmarshal([]byte(cached), &streams); err == nil {
			return streams
		}
	}

	// setup the timeout context
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	// setup the filters and error
	var liveInclude, liveExclude, seriesInclude, seriesExclude *regexp.Regexp
	var err error

	// the filters
	if source.LiveIncludeRegex != "" {
		liveInclude, err = regexp.Compile(source.LiveIncludeRegex)
		if err != nil {
			logger.Error("{parser/xtremecodes - ParseXtremeCodesAPI} Invalid LiveIncludeRegex: %v", err)
		}
	}
	if source.LiveExcludeRegex != "" {
		liveExclude, err = regexp.Compile(source.LiveExcludeRegex)
		if err != nil {
			logger.Error("{parser/xtremecodes - ParseXtremeCodesAPI} Invalid LiveExcludeRegex: %v", err)
		}
	}
	if source.SeriesIncludeRegex != "" {
		seriesInclude, err = regexp.Compile(source.SeriesIncludeRegex)
		if err != nil {
			logger.Error("{parser/xtremecodes - ParseXtremeCodesAPI} Invalid SeriesIncludeRegex: %v", err)
		}
	}
	if source.SeriesExcludeRegex != "" {
		seriesExclude, err = regexp.Compile(source.SeriesExcludeRegex)
		if err != nil {
			logger.Error("{parser/xtremecodes - ParseXtremeCodesAPI} Invalid SeriesExcludeRegex: %v", err)
		}
	}

	// hold all the streams
	var allStreams []*types.Stream
	var allStreamsMu sync.Mutex

	// try to get the streams
	liveStreams := fetchXCLiveStreams(httpClient, cfg, source, rateLimiter)
	logger.Debug("{parser/xtremecodes - ParseXtremeCodesAPI} Fetched %d live streams", len(liveStreams))

	// if we actually have live streams
	if len(liveStreams) > 0 {

		// setup the batch and background workers
		const batchSize = 1000
		workers := cfg.WorkerThreads
		workChan := make(chan xcWork, workers)
		resultsChan := make(chan []*types.Stream, workers)

		// setup the background workder then loop them
		var wg sync.WaitGroup
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for work := range workChan {
					select {
					case <-ctx.Done():
						return
					default:
					}
					logger.Debug("{parser/xtremecodes - ParseXtremeCodesAPI} process the live batch %v", i)
					results := processLiveBatchWorker(work.streams, liveInclude, liveExclude, source)
					resultsChan <- results
				}
			}()
		}

		// run the batch and close it
		go func() {
			for i := 0; i < len(liveStreams); i += batchSize {
				end := i + batchSize
				if end > len(liveStreams) {
					end = len(liveStreams)
				}
				workChan <- xcWork{streams: liveStreams[i:end], start: i}
			}
			close(workChan)
		}()

		// wait the thread then do the closing
		go func() {
			wg.Wait()
			close(resultsChan)
		}()

		// loop the results channel
		for results := range resultsChan {
			allStreamsMu.Lock()
			allStreams = append(allStreams, results...)
			allStreamsMu.Unlock()
		}
		logger.Debug("{parser/xtremecodes - ParseXtremeCodesAPI} Live streams completed: %d total kept", len(allStreams))

	}

	select {
	case <-ctx.Done():
		logger.Debug("{parser/xtremecodes - ParseXtremeCodesAPI} Context cancelled after live streams")
		return allStreams
	default:
	}

	logger.Debug("{parser/xtremecodes - ParseXtremeCodesAPI} Starting series fetch")
	series := fetchXCSeries(httpClient, cfg, source, rateLimiter)
	logger.Debug("{parser/xtremecodes - ParseXtremeCodesAPI} Fetched %d series", len(series))

	// if there are series streams
	if len(series) > 0 {

		// setup the batch and background workers
		const batchSize = 1000
		workers := cfg.WorkerThreads
		workChan := make(chan xcSeriesWork, workers)
		resultsChan := make(chan []*types.Stream, workers)

		// setup the background worker then loop them
		var wg sync.WaitGroup
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for work := range workChan {
					select {
					case <-ctx.Done():
						return
					default:
					}
					logger.Debug("{parser/xtremecodes - ParseXtremeCodesAPI} process the series batch %v", i)
					results := processSeriesBatchWorker(work.series, seriesInclude, seriesExclude, source)
					resultsChan <- results
				}
			}()
		}

		// run the batch and close it
		go func() {
			for i := 0; i < len(series); i += batchSize {
				end := i + batchSize
				if end > len(series) {
					end = len(series)
				}
				workChan <- xcSeriesWork{series: series[i:end], start: i}
			}
			close(workChan)
		}()

		// wait the thread then really close it
		go func() {
			wg.Wait()
			close(resultsChan)
		}()

		// loop the results channel
		for results := range resultsChan {
			allStreamsMu.Lock()
			allStreams = append(allStreams, results...)
			allStreamsMu.Unlock()
		}
		logger.Debug("{parser/xtremecodes - ParseXtremeCodesAPI} Series completed: total streams now %d", len(allStreams))

	}
	logger.Debug("{parser/xtremecodes - ParseXtremeCodesAPI} XC API parsing complete: %d total streams", len(allStreams))

	// do we actually have any streams?
	if len(allStreams) > 0 {
		if data, err := json.Marshal(allStreams); err == nil {
			cache.SetXCData(cacheKey, string(data))
			logger.Debug("{parser/xtremecodes - ParseXtremeCodesAPI} Cached %d streams for %s", len(allStreams), source.Name)
		}
	}
	return allStreams
}

// fetchXCLiveStreamsWithContext retrieves live television stream data with context support
func fetchXCLiveStreamsWithContext(ctx context.Context, httpClient *client.HeaderSettingClient, cfg *config.Config, source *config.SourceConfig, rateLimiter ratelimit.Limiter) []XCLiveStream {
	// Apply rate limiting before making API request to prevent server overload
	if rateLimiter != nil {
		rateLimiter.Take()
		logger.Debug("{parser/xtremecodes - fetchXCLiveStreamsWithContext} Applied rate limit for XC live streams request: %s", source.Name)
	}

	// Construct API URL for live streams endpoint with authentication parameters
	url := fmt.Sprintf("%s/player_api.php?username=%s&password=%s&action=get_live_streams", source.URL, source.Username, source.Password)

	// Execute generic API data fetching with proper error handling
	streams, err := fetchXCDataWithContext[XCLiveStream](ctx, httpClient, cfg, source, url)
	if err != nil {
		logger.Error("{parser/xtremecodes - fetchXCLiveStreamsWithContext} Failed to fetch XC live streams from %s: %v", utils.LogURL(cfg, source.URL), err)
		return nil
	}

	logger.Debug("{parser/xtremecodes - fetchXCLiveStreamsWithContext} Successfully fetched %d live streams from XC API", len(streams))
	return streams
}

// fetchXCSeriesWithContext retrieves television series data with context support
func fetchXCSeriesWithContext(ctx context.Context, httpClient *client.HeaderSettingClient, cfg *config.Config, source *config.SourceConfig, rateLimiter ratelimit.Limiter) []XCSeries {
	// Apply rate limiting before making API request to prevent server overload
	if rateLimiter != nil {
		rateLimiter.Take()
		logger.Debug("{parser/xtremecodes - fetchXCSeriesWithContext} Applied rate limit for XC series request: %s", source.Name)
	}

	// Construct API URL for series endpoint with authentication parameters
	url := fmt.Sprintf("%s/player_api.php?username=%s&password=%s&action=get_series", source.URL, source.Username, source.Password)

	// Execute generic API data fetching with proper error handling
	series, err := fetchXCDataWithContext[XCSeries](ctx, httpClient, cfg, source, url)
	if err != nil {
		logger.Error("{parser/xtremecodes - fetchXCSeriesWithContext} Failed to fetch XC series from %s: %v", utils.LogURL(cfg, source.URL), err)
		return nil
	}
	logger.Debug("{parser/xtremecodes - fetchXCSeriesWithContext} Successfully fetched %d series from XC API", len(series))
	return series
}

// fetchXCDataWithContext implements context-aware HTTP request handler for Xtreme Codes API endpoints
func fetchXCDataWithContext[T any](ctx context.Context, httpClient *client.HeaderSettingClient, cfg *config.Config, source *config.SourceConfig, url string) ([]T, error) {
	// Create request with the provided context
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		logger.Error("{parser/xtremecodes - fetchXCDataWithContext} Failed to create XC API request: %v", err)
		return nil, err
	}

	// set the keep-alive
	req.Header.Set("Connection", "keep-alive")

	// do the request with the right headers
	resp, err := httpClient.DoWithHeaders(req, source.UserAgent, source.ReqOrigin, source.ReqReferrer)
	if err != nil {
		logger.Error("{parser/xtremecodes - fetchXCDataWithContext} XC API request failed: %v", err)
		return nil, err
	}

	// close the connection
	defer func() {
		resp.Body.Close()
		logger.Debug("{parser/xtremecodes - fetchXCDataWithContext} Closed XC API connection for: %s", utils.LogURL(cfg, source.URL))

	}()

	// invalid response
	if resp.StatusCode != http.StatusOK {
		logger.Error("{parser/xtremecodes - fetchXCDataWithContext} XC API returned HTTP %d for: %s", resp.StatusCode, utils.LogURL(cfg, source.URL))
		return nil, nil
	}

	decoder := json.NewDecoder(resp.Body)
	var data []T

	// decode the response
	if err := decoder.Decode(&data); err != nil {
		logger.Error("{parser/xtremecodes - fetchXCDataWithContext} Failed to parse XC API JSON response: %v", err)
		return nil, err
	}

	logger.Debug("{parser/xtremecodes - fetchXCDataWithContext} Successfully parsed %d items from XC API response", len(data))
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
func fetchXCLiveStreams(httpClient *client.HeaderSettingClient, cfg *config.Config, source *config.SourceConfig, rateLimiter ratelimit.Limiter) []XCLiveStream {

	// Apply rate limiting before making API request to prevent server overload
	if rateLimiter != nil {
		rateLimiter.Take()
		logger.Debug("{parser/xtremecodes - fetchXCLiveStreams} Applied rate limit for XC live streams request: %s", source.Name)
	}

	// Construct API URL for live streams endpoint with authentication parameters
	url := fmt.Sprintf("%s/player_api.php?username=%s&password=%s&action=get_live_streams", source.URL, source.Username, source.Password)

	// Execute generic API data fetching with proper error handling
	streams, err := fetchXCData[XCLiveStream](httpClient, cfg, source, url)
	if err != nil {
		logger.Error("{parser/xtremecodes - fetchXCLiveStreams} Failed to fetch XC live streams from %s: %v", utils.LogURL(cfg, source.URL), err)
		return nil
	}

	logger.Debug("{parser/xtremecodes - fetchXCLiveStreams} Successfully fetched %d live streams from XC API", len(streams))
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
func fetchXCSeries(httpClient *client.HeaderSettingClient, cfg *config.Config, source *config.SourceConfig, rateLimiter ratelimit.Limiter) []XCSeries {

	// Apply rate limiting before making API request to prevent server overload
	if rateLimiter != nil {
		rateLimiter.Take()
		logger.Debug("{parser/xtremecodes - fetchXCSeries} Applied rate limit for XC series request: %s", source.Name)
	}

	// Construct API URL for series endpoint with authentication parameters
	url := fmt.Sprintf("%s/player_api.php?username=%s&password=%s&action=get_series", source.URL, source.Username, source.Password)

	// Execute generic API data fetching with proper error handling
	series, err := fetchXCData[XCSeries](httpClient, cfg, source, url)
	if err != nil {
		logger.Error("{parser/xtremecodes - fetchXCSeries} Failed to fetch XC series from %s: %v", utils.LogURL(cfg, source.URL), err)
		return nil
	}

	logger.Debug("{parser/xtremecodes - fetchXCSeries} Successfully fetched %d series from XC API", len(series))
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
func fetchXCVODStreams(httpClient *client.HeaderSettingClient, cfg *config.Config, source *config.SourceConfig, rateLimiter ratelimit.Limiter) []XCVODStream {

	// Apply rate limiting before making API request to prevent server overload
	if rateLimiter != nil {
		rateLimiter.Take()
		logger.Debug("{parser/xtremecodes - fetchXCVODStreams} Applied rate limit for XC VOD streams request: %s", source.Name)
	}

	// Construct API URL for VOD streams endpoint with authentication parameters
	url := fmt.Sprintf("%s/player_api.php?username=%s&password=%s&action=get_vod_streams", source.URL, source.Username, source.Password)

	// Execute generic API data fetching with proper error handling
	streams, err := fetchXCData[XCVODStream](httpClient, cfg, source, url)
	if err != nil {
		logger.Error("{parser/xtremecodes - fetchXCVODStreams} Failed to fetch XC VOD streams from %s: %v", utils.LogURL(cfg, source.URL), err)
		return nil
	}

	logger.Debug("{parser/xtremecodes - fetchXCVODStreams} Successfully fetched %d VOD streams from XC API", len(streams))
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
func fetchXCData[T any](httpClient *client.HeaderSettingClient, cfg *config.Config, source *config.SourceConfig, url string) ([]T, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// make the request
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		logger.Error("{parser/xtremecodes - fetchXCData} Failed to create XC API request: %v", err)
		return nil, err
	}

	// setup the closing context
	req = req.WithContext(ctx)

	// set the keep-alive header
	req.Header.Set("Connection", "keep-alive")

	// make the request with the proper headers
	resp, err := httpClient.DoWithHeaders(req, source.UserAgent, source.ReqOrigin, source.ReqReferrer)
	if err != nil {
		logger.Debug("{parser/xtremecodes - fetchXCData} XC API request failed: %v", err)
		return nil, err
	}

	// close hte connection
	defer func() {
		resp.Body.Close()
		logger.Debug("{parser/xtremecodes - fetchXCData} Closed XC API connection for: %s", utils.LogURL(cfg, source.URL))
	}()

	// invalid response code
	if resp.StatusCode != http.StatusOK {
		logger.Error("{parser/xtremecodes - fetchXCData} XC API returned HTTP %d for: %s", resp.StatusCode, utils.LogURL(cfg, source.URL))
		return nil, nil
	}

	// setup the json decoder
	decoder := json.NewDecoder(resp.Body)
	var data []T
	if err := decoder.Decode(&data); err != nil {
		logger.Error("{parser/xtremecodes - fetchXCData} Failed to parse XC API JSON response: %v", err)
		return nil, err
	}

	logger.Debug("{parser/xtremecodes - fetchXCData} Successfully parsed %d items from XC API response", len(data))
	return data, nil
}
