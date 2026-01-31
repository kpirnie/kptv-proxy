package filter

import (
	"kptv-proxy/work/config"
	"kptv-proxy/work/logger"
	"kptv-proxy/work/types"
	"strings"
	"sync"

	"github.com/grafana/regexp"
)

// Content type detection regexes - should match the ones used during import
var (
	seriesRegex = regexp.MustCompile(`(?i)24\/7|247|\/series\/|\/shows\/|\/show\/`)
	vodRegex    = regexp.MustCompile(`(?i)\/vods\/|\/vod\/|\/movies\/|\/movie\/`)
)

// CompiledFilter holds compiled regex patterns for a source
type CompiledFilter struct {
	LiveInclude   *regexp.Regexp
	LiveExclude   *regexp.Regexp
	SeriesInclude *regexp.Regexp
	SeriesExclude *regexp.Regexp
	VODInclude    *regexp.Regexp
	VODExclude    *regexp.Regexp
}

// FilterManager manages compiled filters for sources
type FilterManager struct {
	filters map[string]*CompiledFilter
	mu      sync.RWMutex
}

// NewFilterManager creates a new filter manager
func NewFilterManager() *FilterManager {
	return &FilterManager{
		filters: make(map[string]*CompiledFilter),
	}
}

// GetOrCreateFilter gets or creates a compiled filter for a source
func (fm *FilterManager) GetOrCreateFilter(source *config.SourceConfig) *CompiledFilter {

	// et a filter lock
	fm.mu.Lock()
	defer fm.mu.Unlock()

	// Use source URL as key since it's unique
	key := source.URL

	// if it already exists
	if filter, exists := fm.filters[key]; exists {
		return filter
	}

	// setup the compiled filter
	filter := &CompiledFilter{}

	// Compile regex patterns (ignore errors, treat as no filter if invalid)
	if source.LiveIncludeRegex != "" {
		compiled, err := regexp.Compile(source.LiveIncludeRegex)
		if err != nil {
			logger.Error("{filter - GetOrCreateFilter} failed to compile LiveIncludeRegex '%s': %v\n", source.LiveIncludeRegex, err)
		} else {
			filter.LiveInclude = compiled
			logger.Debug("{filter - GetOrCreateFilter} Compiled LiveIncludeRegex: '%s'\n", source.LiveIncludeRegex)

		}
	}
	if source.LiveExcludeRegex != "" {
		compiled, err := regexp.Compile(source.LiveExcludeRegex)
		if err != nil {
			logger.Error("{filter - GetOrCreateFilter} failed to compile LiveExcludeRegex '%s': %v\n", source.LiveExcludeRegex, err)

		} else {
			filter.LiveExclude = compiled
			logger.Debug("{filter - GetOrCreateFilter} Compiled LiveExcludeRegex: '%s'\n", source.LiveExcludeRegex)

		}
	}
	if source.SeriesIncludeRegex != "" {
		filter.SeriesInclude, _ = regexp.Compile(source.SeriesIncludeRegex)
	}
	if source.SeriesExcludeRegex != "" {
		filter.SeriesExclude, _ = regexp.Compile(source.SeriesExcludeRegex)
	}
	if source.VODIncludeRegex != "" {
		filter.VODInclude, _ = regexp.Compile(source.VODIncludeRegex)
	}
	if source.VODExcludeRegex != "" {
		filter.VODExclude, _ = regexp.Compile(source.VODExcludeRegex)
	}

	fm.filters[key] = filter
	logger.Debug("{filter - GetOrCreateFilter} setup the compiled filter")
	// return it
	return filter
}

// ClearFilters clears all compiled filters
func (fm *FilterManager) ClearFilters() {
	logger.Debug("{filter - ClearFilters} clear the filter map")
	fm.mu.Lock()
	defer fm.mu.Unlock()
	fm.filters = make(map[string]*CompiledFilter)
}

// RemoveFilter removes a specific filter
func (fm *FilterManager) RemoveFilter(sourceURL string) {
	logger.Debug("{filter - RemoveFilter} remove the filter for %v", sourceURL)
	fm.mu.Lock()
	defer fm.mu.Unlock()
	delete(fm.filters, sourceURL)
}

// FilterStreams applies content filtering to streams
func FilterStreams(streams []*types.Stream, source *config.SourceConfig, filterManager *FilterManager) []*types.Stream {

	// Debug logging to see if filtering is being called
	if len(streams) > 0 && streams[0].Source != nil && streams[0].Source.Name != "" {
		logger.Debug("{filter - FilterStreams} Source: %s, Streams: %d, LiveInclude: %s, LiveExclude: %s, SeriesInclude: %s, SeriesExclude: %s, VODInclude: %s, VODExclude: %s\n",
			source.Name, len(streams), source.LiveIncludeRegex, source.LiveExcludeRegex,
			source.SeriesIncludeRegex, source.SeriesExcludeRegex, source.VODIncludeRegex, source.VODExcludeRegex)
	}

	if source.LiveIncludeRegex == "" && source.LiveExcludeRegex == "" &&
		source.SeriesIncludeRegex == "" && source.SeriesExcludeRegex == "" &&
		source.VODIncludeRegex == "" && source.VODExcludeRegex == "" {
		logger.Debug("{filter - FilterStreams} No filters configured for source %s, returning %d streams unchanged\n", source.Name, len(streams))
		// return the streams
		return streams
	}
	logger.Debug("{filter - FilterStreams} Applying filters to %d streams from source %s\n", len(streams), source.Name)

	filter := filterManager.GetOrCreateFilter(source)
	filtered := make([]*types.Stream, 0, len(streams))

	for _, stream := range streams {
		contentType := getContentType(stream)
		shouldInclude := shouldIncludeStream(stream, filter)
		logger.Debug("{filter - FilterStreams} Stream: %s, Type: %s, Include: %v\n", stream.Name, contentType, shouldInclude)

		if shouldInclude {
			filtered = append(filtered, stream)
		}
	}
	logger.Debug("{filter - FilterStreams} Filtered %d -> %d streams for source %s\n", len(streams), len(filtered), source.Name)

	// return the filtered streams
	return filtered
}

// shouldIncludeStream determines if a stream should be included based on filters
func shouldIncludeStream(stream *types.Stream, filter *CompiledFilter) bool {
	streamName := strings.TrimSpace(strings.ToLower(stream.Name))
	originalName := stream.Name
	contentType := getContentType(stream)
	logger.Debug("{filter - shouldIncludeStream} Evaluating stream: '%s', trimmed lowercase: '%s', content type: %s\n", originalName, streamName, contentType)

	// Check include filters first - if any exist, stream must match at least one
	var hasIncludeFilters bool
	var matchesInclude bool

	switch contentType {
	case "live":
		if filter.LiveInclude != nil {
			hasIncludeFilters = true
			matchesInclude = filter.LiveInclude.MatchString(streamName)
			logger.Debug("{filter - shouldIncludeStream} Live include pattern exists, matches: %v (pattern tested against: '%s')\n", matchesInclude, streamName)

		}
	case "series":
		if filter.SeriesInclude != nil {
			hasIncludeFilters = true
			matchesInclude = filter.SeriesInclude.MatchString(streamName)
			logger.Debug("{filter - shouldIncludeStream} Series include pattern exists, matches: %v\n", matchesInclude)

		}
	case "vod":
		if filter.VODInclude != nil {
			hasIncludeFilters = true
			matchesInclude = filter.VODInclude.MatchString(streamName)
			logger.Debug("{filter - shouldIncludeStream} VOD include pattern exists, matches: %v\n", matchesInclude)

		}
	}

	logger.Debug("{filter - shouldIncludeStream} hasIncludeFilters: %v, matchesInclude: %v\n", hasIncludeFilters, matchesInclude)

	// If include filters exist but stream doesn't match any, exclude it
	if hasIncludeFilters && !matchesInclude {
		logger.Debug("{filter - shouldIncludeStream} EXCLUDED by include filters: '%s'\n", originalName)
		return false
	}

	// Then check exclude filters
	switch contentType {
	case "live":
		if filter.LiveExclude != nil {
			if filter.LiveExclude.MatchString(streamName) {
				logger.Debug("{filter - shouldIncludeStream} EXCLUDED by live exclude filter: '%s'\n", originalName)
				return false
			}
			logger.Debug("{filter - shouldIncludeStream} Live exclude pattern exists but didn't match: '%s'\n", streamName)
		}
	case "series":
		if filter.SeriesExclude != nil {
			if filter.SeriesExclude.MatchString(streamName) {
				logger.Debug("{filter - shouldIncludeStream} EXCLUDED by series exclude filter: '%s'\n", originalName)
				return false
			}
		}
	case "vod":
		if filter.VODExclude != nil {
			if filter.VODExclude.MatchString(streamName) {
				logger.Debug("{filter - shouldIncludeStream} EXCLUDED by VOD exclude filter: '%s'\n", originalName)
				return false
			}
		}
	}

	logger.Debug("{filter - shouldIncludeStream} INCLUDED: '%s'\n", originalName)
	return true
}

// getContentType determines the content type from stream attributes
func getContentType(stream *types.Stream) string {
	// First check using the same regex patterns used during import
	streamName := stream.Name
	streamURL := stream.URL

	// Check both name and URL with series regex
	seriesNameMatch := seriesRegex.MatchString(streamName)
	vodNameMatch := vodRegex.MatchString(streamName)
	seriesURLMatch := seriesRegex.MatchString(streamURL)
	vodURLMatch := vodRegex.MatchString(streamURL)

	logger.Debug("{filter - getContentType} Content type detection for '%s':\n", streamName)
	logger.Debug("{filter - getContentType}   SeriesName: %v, VODName: %v, SeriesURL: %v, VODURL: %v\n",
		seriesNameMatch, vodNameMatch, seriesURLMatch, vodURLMatch)

	// Apply same logic as import parsing
	if seriesNameMatch || seriesURLMatch {
		logger.Debug("{filter - getContentType} Classified as SERIES\n")
		return "series"
	}
	if vodNameMatch || vodURLMatch {
		logger.Debug("{filter - getContentType} Classified as VOD\n")
		return "vod"
	}

	// Fallback to attribute-based detection (secondary method)
	if group, ok := stream.Attributes["group-title"]; ok {
		groupLower := strings.ToLower(group)
		logger.Debug("{filter - getContentType} Content type from group-title '%s': ", group)

		switch {
		case strings.Contains(groupLower, "series"):
			return "series"
		case strings.Contains(groupLower, "vod") || strings.Contains(groupLower, "movie"):
			return "vod"
		case strings.Contains(groupLower, "live") || strings.Contains(groupLower, "tv"):
			return "live"
		}
	}

	// Also check tvg-group attribute
	if group, ok := stream.Attributes["tvg-group"]; ok {
		groupLower := strings.ToLower(group)
		logger.Debug("{filter - getContentType} Content type from tvg-group '%s': ", group)

		switch {
		case strings.Contains(groupLower, "series"):
			return "series"
		case strings.Contains(groupLower, "vod") || strings.Contains(groupLower, "movie"):
			return "vod"
		case strings.Contains(groupLower, "live") || strings.Contains(groupLower, "tv"):
			return "live"
		}
	}

	// Default to live
	logger.Debug("{filter - getContentType} Defaulted to LIVE")
	return "live"
}
