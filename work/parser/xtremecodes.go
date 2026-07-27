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
	"strings"
	"sync"
	"time"

	"go.uber.org/ratelimit"
)

// XCID normalizes an Xtreme Codes identifier, which providers encode inconsistently
// as either a JSON string or a JSON number.
type XCID string

// UnmarshalJSON decodes an XC identifier from either a string or a numeric JSON value.
func (id *XCID) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*id = ""
		return nil
	}

	var value string
	if err := json.Unmarshal(data, &value); err == nil {
		*id = XCID(value)
		return nil
	}

	var number json.Number
	if err := json.Unmarshal(data, &number); err != nil {
		return err
	}
	*id = XCID(number.String())
	return nil
}

// XCCategory holds the flat category metadata used to resolve display group names.
type XCCategory struct {
	CategoryID   XCID   `json:"category_id"`
	CategoryName string `json:"category_name"`
}

// XCLiveStream represents a single live stream entry from the Xtreme Codes API response,
// containing essential metadata for live television channels including stream identification,
// categorization, and EPG (Electronic Program Guide) integration information.
// This structure maps directly to the JSON response format from the get_live_streams endpoint.
type XCLiveStream struct {
	StreamID     int    `json:"stream_id"`      // Unique identifier for the live stream used in stream URL construction
	Name         string `json:"name"`           // Display name of the live channel for user interfaces and playlists
	CategoryID   XCID   `json:"category_id"`    // Category identifier for grouping related channels
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
	CategoryID XCID   `json:"category_id"` // Category identifier for grouping related series content
	Cover      string `json:"cover"`       // URL to series cover artwork/poster image for display purposes
}

// XCVODStream represents a single video-on-demand stream entry from the Xtreme Codes API response,
// containing metadata for movies and other on-demand video content including identification,
// categorization, artwork, and format information. This structure maps to the JSON response
// format from the get_vod_streams endpoint.
type XCVODStream struct {
	StreamID           int    `json:"stream_id"`           // Unique identifier for the VOD stream used in stream URL construction
	Name               string `json:"name"`                // Display name of the video content for user interfaces and playlists
	CategoryID         XCID   `json:"category_id"`         // Category identifier for grouping related video content
	StreamIcon         string `json:"stream_icon"`         // URL to video thumbnail/poster image for display purposes
	ContainerExtension string `json:"container_extension"` // File format extension (mp4, mkv, etc.) for container type identification
}

// processLiveBatchWorker processes a batch of XC live streams into internal Stream
// objects. Category labels are resolved from the provider's flat category list, and
// 24/7 style pseudo-channels are reclassified off live by name.
//
// Parameters:
//   - batch: slice of XCLiveStream objects to process
//   - categoryMap: provider category ID to display-name mapping
//   - source: source configuration containing credentials and connection parameters
//
// Returns:
//   - []*types.Stream: slice of processed streams ready for channel aggregation
func processLiveBatchWorker(batch []XCLiveStream, categoryMap map[string]string, source *config.SourceConfig) []*types.Stream {
	results := make([]*types.Stream, 0, len(batch))
	logger.Debug("{parser/xtremecodes - processLiveBatchWorker} process the live batch")

	for _, stream := range batch {
		streamURL := fmt.Sprintf("%s/live/%s/%s/%d.ts", source.URL, source.Username, source.Password, stream.StreamID)

		// name only, the URL is ours and its numeric ID would false-match the 247 pattern
		contentType := types.ContentTypeLive
		if utils.SeriesRegex != nil && (utils.SeriesRegex.MatchString(stream.Name) || utils.SeriesRegex.MatchString(streamURL)) {
			contentType = types.ContentTypeSeries
		} else if utils.VodRegex != nil && (utils.VodRegex.MatchString(stream.Name) || utils.VodRegex.MatchString(streamURL)) {
			contentType = types.ContentTypeVOD
		}

		s := &types.Stream{
			URL:         streamURL,
			Name:        stream.Name,
			Source:      source,
			ContentType: contentType,
			Attributes: map[string]string{
				"tvg-name":    stream.Name,
				"group-title": categoryName(categoryMap, string(stream.CategoryID), "live"),
				"tvg-id":      fmt.Sprintf("%d", stream.StreamID),
				"category-id": string(stream.CategoryID),
			},
		}

		if stream.StreamIcon != "" {
			s.Attributes["tvg-logo"] = stream.StreamIcon
		}
		if stream.EpgChannelID != "" {
			s.Attributes["tvg-epgid"] = stream.EpgChannelID
		}

		results = append(results, s)
	}
	return results
}

// processSeriesBatchWorker processes a batch of XC series into internal Stream
// objects. Category labels are resolved from the provider's flat category list.
//
// Parameters:
//   - batch: slice of XCSeries objects to process
//   - categoryMap: provider category ID to display-name mapping
//   - source: source configuration containing credentials and connection parameters
//
// Returns:
//   - []*types.Stream: slice of processed series streams ready for channel aggregation
func processSeriesBatchWorker(batch []XCSeries, categoryMap map[string]string, source *config.SourceConfig) []*types.Stream {
	results := make([]*types.Stream, 0, len(batch))
	logger.Debug("{parser/xtremecodes - processSeriesBatchWorker} process the series batch")

	for _, serie := range batch {
		streamURL := fmt.Sprintf("%s/series/%s/%s/%d.ts", source.URL, source.Username, source.Password, serie.SeriesID)

		s := &types.Stream{
			URL:                streamURL,
			Name:               serie.Name,
			Source:             source,
			ContentType:        types.ContentTypeSeries,
			ContainerExtension: "ts",
			Attributes: map[string]string{
				"tvg-name":    serie.Name,
				"group-title": categoryName(categoryMap, string(serie.CategoryID), "series"),
				"tvg-id":      fmt.Sprintf("%d", serie.SeriesID),
				"category-id": string(serie.CategoryID),
			},
		}

		if serie.Cover != "" {
			s.Attributes["tvg-logo"] = serie.Cover
		}

		results = append(results, s)
	}
	return results
}

// processVODBatchWorker processes a batch of XC VOD streams into internal Stream
// objects, preserving each entry's container extension for URL construction.
//
// Parameters:
//   - batch: slice of XCVODStream objects to process
//   - categoryMap: provider category ID to display-name mapping
//   - source: source configuration containing credentials and connection parameters
//
// Returns:
//   - []*types.Stream: slice of processed VOD streams ready for channel aggregation
func processVODBatchWorker(batch []XCVODStream, categoryMap map[string]string, source *config.SourceConfig) []*types.Stream {
	results := make([]*types.Stream, 0, len(batch))
	logger.Debug("{parser/xtremecodes - processVODBatchWorker} process the vod batch")

	for _, stream := range batch {
		extension := utils.NormalizeContainerExtension(stream.ContainerExtension)
		streamURL := fmt.Sprintf("%s/movie/%s/%s/%d.%s", source.URL, source.Username, source.Password, stream.StreamID, extension)

		s := &types.Stream{
			URL:                streamURL,
			Name:               stream.Name,
			Source:             source,
			ContentType:        types.ContentTypeVOD,
			ContainerExtension: extension,
			Attributes: map[string]string{
				"tvg-name":    stream.Name,
				"group-title": categoryName(categoryMap, string(stream.CategoryID), "vod"),
				"tvg-id":      fmt.Sprintf("%d", stream.StreamID),
				"category-id": string(stream.CategoryID),
			},
		}

		if stream.StreamIcon != "" {
			s.Attributes["tvg-logo"] = stream.StreamIcon
		}

		results = append(results, s)
	}
	return results
}

// categoryName resolves a provider category ID to its display name, returning the
// supplied fallback when the provider gave no usable name.
func categoryName(categoryMap map[string]string, categoryID, fallback string) string {
	if name, ok := categoryMap[categoryID]; ok && strings.TrimSpace(name) != "" {
		return name
	}
	return fallback
}

// buildCategoryMap converts a provider's flat category list into an ID to name lookup,
// discarding entries with a blank ID or name.
func buildCategoryMap(categories []XCCategory) map[string]string {
	categoryMap := make(map[string]string, len(categories))
	for _, category := range categories {
		if strings.TrimSpace(string(category.CategoryID)) == "" || strings.TrimSpace(category.CategoryName) == "" {
			continue
		}
		categoryMap[string(category.CategoryID)] = category.CategoryName
	}
	return categoryMap
}

// processXCBatches distributes a slice of API items across a worker pool, reassembling
// the per-batch results in their original order so import ordering stays deterministic.
//
// Parameters:
//   - ctx: context governing cancellation of the worker pool
//   - items: full slice of API items to process
//   - workers: number of concurrent workers, floored at one
//   - process: per-batch conversion function producing internal Stream objects
//
// Returns:
//   - []*types.Stream: ordered concatenation of every batch result
func processXCBatches[T any](ctx context.Context, items []T, workers int, process func([]T) []*types.Stream) []*types.Stream {
	if len(items) == 0 {
		return nil
	}
	if workers < 1 {
		workers = 1
	}

	const batchSize = 1000

	type batchJob struct {
		index int
		items []T
	}
	type batchResult struct {
		index   int
		streams []*types.Stream
	}

	workChan := make(chan batchJob)
	resultsChan := make(chan batchResult)

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case job, ok := <-workChan:
					if !ok {
						return
					}
					results := process(job.items)
					select {
					case resultsChan <- batchResult{index: job.index, streams: results}:
					case <-ctx.Done():
						return
					}
				}
			}
		}()
	}

	go func() {
		defer close(workChan)
		for start := 0; start < len(items); start += batchSize {
			end := start + batchSize
			if end > len(items) {
				end = len(items)
			}
			select {
			case workChan <- batchJob{index: start / batchSize, items: items[start:end]}:
			case <-ctx.Done():
				return
			}
		}
	}()

	go func() {
		wg.Wait()
		close(resultsChan)
	}()

	batchCount := (len(items) + batchSize - 1) / batchSize
	ordered := make([][]*types.Stream, batchCount)
	for result := range resultsChan {
		if result.index >= 0 && result.index < len(ordered) {
			ordered[result.index] = result.streams
		}
	}

	var results []*types.Stream
	for _, batch := range ordered {
		results = append(results, batch...)
	}
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
// Xtreme Codes URL format specifications. ContentType carries the semantic classification;
// group-title preserves the provider's category name, falling back to the type name.
//
// Parameters:
//   - httpClient: configured HTTP client for API requests with header support
//   - cfg: application configuration containing debug settings and URL obfuscation preferences
//   - source: source configuration with URL, credentials, and connection parameters
//   - rateLimiter: rate limiter for controlling API request frequency to prevent server overload
//   - cache: playlist cache used to short-circuit repeat imports
//
// Returns:
//   - []*types.Stream: aggregated collection of streams from all three API endpoints
func ParseXtremeCodesAPI(httpClient *client.HeaderSettingClient, cfg *config.Config, source *config.SourceConfig, rateLimiter ratelimit.Limiter, cache *cache.Cache) []*types.Stream {
	logger.Debug("{parser/xtremecodes - ParseXtremeCodesAPI} from %s with optimized batch processing", utils.LogURL(cfg, source.URL))

	cacheKey := fmt.Sprintf("xc:v2:%s:%s:%s", source.URL, source.Username, source.Password)
	if cached, found := cache.GetXCData(cacheKey); found {
		logger.Debug("{parser/xtremecodes - ParseXtremeCodesAPI} Using cached XC API data for %s", source.Name)
		var streams []*types.Stream
		if err := json.Unmarshal([]byte(cached), &streams); err == nil {
			return streams
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	var liveCategories, seriesCategories, vodCategories []XCCategory
	var liveStreams []XCLiveStream
	var series []XCSeries
	var vodStreams []XCVODStream
	var liveCategoryOK, seriesCategoryOK, vodCategoryOK bool
	var liveOK, seriesOK, vodOK bool

	var fetchWG sync.WaitGroup
	fetchWG.Add(6)
	go func() {
		defer fetchWG.Done()
		liveCategories, liveCategoryOK = fetchXCCategories(ctx, httpClient, cfg, source, rateLimiter, types.ContentTypeLive)
	}()
	go func() {
		defer fetchWG.Done()
		seriesCategories, seriesCategoryOK = fetchXCCategories(ctx, httpClient, cfg, source, rateLimiter, types.ContentTypeSeries)
	}()
	go func() {
		defer fetchWG.Done()
		vodCategories, vodCategoryOK = fetchXCCategories(ctx, httpClient, cfg, source, rateLimiter, types.ContentTypeVOD)
	}()
	go func() {
		defer fetchWG.Done()
		liveStreams, liveOK = fetchXCLiveStreamsWithContext(ctx, httpClient, cfg, source, rateLimiter)
	}()
	go func() {
		defer fetchWG.Done()
		series, seriesOK = fetchXCSeriesWithContext(ctx, httpClient, cfg, source, rateLimiter)
	}()
	go func() {
		defer fetchWG.Done()
		vodStreams, vodOK = fetchXCVODStreamsWithContext(ctx, httpClient, cfg, source, rateLimiter)
	}()
	fetchWG.Wait()

	liveCategoryMap := buildCategoryMap(liveCategories)
	seriesCategoryMap := buildCategoryMap(seriesCategories)
	vodCategoryMap := buildCategoryMap(vodCategories)

	allStreams := processXCBatches(ctx, liveStreams, cfg.WorkerThreads, func(batch []XCLiveStream) []*types.Stream {
		return processLiveBatchWorker(batch, liveCategoryMap, source)
	})
	allStreams = append(allStreams, processXCBatches(ctx, series, cfg.WorkerThreads, func(batch []XCSeries) []*types.Stream {
		return processSeriesBatchWorker(batch, seriesCategoryMap, source)
	})...)
	allStreams = append(allStreams, processXCBatches(ctx, vodStreams, cfg.WorkerThreads, func(batch []XCVODStream) []*types.Stream {
		return processVODBatchWorker(batch, vodCategoryMap, source)
	})...)

	logger.Debug("{parser/xtremecodes - ParseXtremeCodesAPI} XC API parsing complete: %d total streams", len(allStreams))

	// only cache a complete catalog, a partial fetch would otherwise be served until it expires
	if ctx.Err() == nil && len(allStreams) > 0 && liveCategoryOK && seriesCategoryOK && vodCategoryOK && liveOK && seriesOK && vodOK {
		if data, err := json.Marshal(allStreams); err == nil {
			cache.SetXCData(cacheKey, string(data))
			logger.Debug("{parser/xtremecodes - ParseXtremeCodesAPI} Cached %d streams for %s", len(allStreams), source.Name)
		}
	} else {
		logger.Debug("{parser/xtremecodes - ParseXtremeCodesAPI} Skipping cache after incomplete XC fetch (live-category=%t, series-category=%t, vod-category=%t, live=%t, series=%t, vod=%t)", liveCategoryOK, seriesCategoryOK, vodCategoryOK, liveOK, seriesOK, vodOK)
	}

	return allStreams
}

// fetchXCCategories retrieves the category list for a single XC content type.
//
// Returns:
//   - []XCCategory: the provider's category entries, nil on failure
//   - bool: false when the request failed, signalling an incomplete catalog
func fetchXCCategories(ctx context.Context, httpClient *client.HeaderSettingClient, cfg *config.Config, source *config.SourceConfig, rateLimiter ratelimit.Limiter, contentType types.ContentType) ([]XCCategory, bool) {
	action := ""
	switch contentType {
	case types.ContentTypeLive:
		action = "get_live_categories"
	case types.ContentTypeSeries:
		action = "get_series_categories"
	case types.ContentTypeVOD:
		action = "get_vod_categories"
	default:
		return nil, false
	}

	if rateLimiter != nil {
		rateLimiter.Take()
		logger.Debug("{parser/xtremecodes - fetchXCCategories} Applied rate limit for XC %s categories request: %s", contentType, source.Name)
	}

	url := fmt.Sprintf("%s/player_api.php?username=%s&password=%s&action=%s", source.URL, source.Username, source.Password, action)
	categories, err := fetchXCDataWithContext[XCCategory](ctx, httpClient, cfg, source, url)
	if err != nil {
		logger.Error("{parser/xtremecodes - fetchXCCategories} Failed to fetch %s categories from %s: %v", contentType, utils.LogURL(cfg, source.URL), err)
		return nil, false
	}

	logger.Debug("{parser/xtremecodes - fetchXCCategories} Successfully fetched %d %s categories from XC API", len(categories), contentType)
	return categories, true
}

// fetchXCVODStreamsWithContext retrieves video-on-demand stream data with context support.
//
// Returns:
//   - []XCVODStream: the provider's VOD entries, nil on failure
//   - bool: false when the request failed, signalling an incomplete catalog
func fetchXCVODStreamsWithContext(ctx context.Context, httpClient *client.HeaderSettingClient, cfg *config.Config, source *config.SourceConfig, rateLimiter ratelimit.Limiter) ([]XCVODStream, bool) {

	// Apply rate limiting before making API request to prevent server overload
	if rateLimiter != nil {
		rateLimiter.Take()
		logger.Debug("{parser/xtremecodes - fetchXCVODStreamsWithContext} Applied rate limit for XC VOD streams request: %s", source.Name)
	}

	// Construct API URL for VOD streams endpoint with authentication parameters
	url := fmt.Sprintf("%s/player_api.php?username=%s&password=%s&action=get_vod_streams", source.URL, source.Username, source.Password)

	// Execute generic API data fetching with proper error handling
	streams, err := fetchXCDataWithContext[XCVODStream](ctx, httpClient, cfg, source, url)
	if err != nil {
		logger.Error("{parser/xtremecodes - fetchXCVODStreamsWithContext} Failed to fetch XC VOD streams from %s: %v", utils.LogURL(cfg, source.URL), err)
		return nil, false
	}

	logger.Debug("{parser/xtremecodes - fetchXCVODStreamsWithContext} Successfully fetched %d VOD streams from XC API", len(streams))
	return streams, true
}

// fetchXCLiveStreamsWithContext retrieves live television stream data with context support
func fetchXCLiveStreamsWithContext(ctx context.Context, httpClient *client.HeaderSettingClient, cfg *config.Config, source *config.SourceConfig, rateLimiter ratelimit.Limiter) ([]XCLiveStream, bool) {
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
		return nil, false
	}

	logger.Debug("{parser/xtremecodes - fetchXCLiveStreamsWithContext} Successfully fetched %d live streams from XC API", len(streams))
	return streams, true
}

// fetchXCSeriesWithContext retrieves television series data with context support
func fetchXCSeriesWithContext(ctx context.Context, httpClient *client.HeaderSettingClient, cfg *config.Config, source *config.SourceConfig, rateLimiter ratelimit.Limiter) ([]XCSeries, bool) {
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
		return nil, false
	}
	logger.Debug("{parser/xtremecodes - fetchXCSeriesWithContext} Successfully fetched %d series from XC API", len(series))
	return series, true
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

	// invalid response, must be an error so the caller can skip caching a partial catalog
	if resp.StatusCode != http.StatusOK {
		logger.Error("{parser/xtremecodes - fetchXCDataWithContext} XC API returned HTTP %d for: %s", resp.StatusCode, utils.LogURL(cfg, source.URL))
		return nil, fmt.Errorf("XC API returned HTTP %d", resp.StatusCode)
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
