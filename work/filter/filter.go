package filter

import (
	"fmt"
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
func (fm *FilterManager) GetOrCreateFilter(source *config.SourceConfig, debug bool) *CompiledFilter {
	fm.mu.Lock()
	defer fm.mu.Unlock()

	// Use source URL as key since it's unique
	key := source.URL

	if filter, exists := fm.filters[key]; exists {
		return filter
	}

	filter := &CompiledFilter{}

	// Compile regex patterns (ignore errors, treat as no filter if invalid)
	if source.LiveIncludeRegex != "" {
		compiled, err := regexp.Compile(source.LiveIncludeRegex)
		if err != nil {
			logger.Error("[FILTER_DEBUG] Failed to compile LiveIncludeRegex '%s': %v\n", source.LiveIncludeRegex, err)
		} else {
			filter.LiveInclude = compiled
			logger.Debug("[FILTER_DEBUG] Compiled LiveIncludeRegex: '%s'\n", source.LiveIncludeRegex)

		}
	}
	if source.LiveExcludeRegex != "" {
		compiled, err := regexp.Compile(source.LiveExcludeRegex)
		if err != nil {
			logger.Error("[FILTER_DEBUG] Failed to compile LiveExcludeRegex '%s': %v\n", source.LiveExcludeRegex, err)

		} else {
			filter.LiveExclude = compiled
			logger.Debug("[FILTER_DEBUG] Compiled LiveExcludeRegex: '%s'\n", source.LiveExcludeRegex)

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
	return filter
}

// ClearFilters clears all compiled filters
func (fm *FilterManager) ClearFilters() {
	fm.mu.Lock()
	defer fm.mu.Unlock()
	fm.filters = make(map[string]*CompiledFilter)
}

// RemoveFilter removes a specific filter
func (fm *FilterManager) RemoveFilter(sourceURL string) {
	fm.mu.Lock()
	defer fm.mu.Unlock()
	delete(fm.filters, sourceURL)
}

// FilterStreams applies content filtering to streams
func FilterStreams(streams []*types.Stream, source *config.SourceConfig, filterManager *FilterManager, debug bool) []*types.Stream {

	// Debug logging to see if filtering is being called
	if debug && len(streams) > 0 && streams[0].Source != nil && streams[0].Source.Name != "" {
		fmt.Printf("[FILTER_DEBUG] Source: %s, Streams: %d, LiveInclude: %s, LiveExclude: %s, SeriesInclude: %s, SeriesExclude: %s, VODInclude: %s, VODExclude: %s\n",
			source.Name, len(streams), source.LiveIncludeRegex, source.LiveExcludeRegex,
			source.SeriesIncludeRegex, source.SeriesExcludeRegex, source.VODIncludeRegex, source.VODExcludeRegex)
	}

	if source.LiveIncludeRegex == "" && source.LiveExcludeRegex == "" &&
		source.SeriesIncludeRegex == "" && source.SeriesExcludeRegex == "" &&
		source.VODIncludeRegex == "" && source.VODExcludeRegex == "" {
		logger.Debug("[FILTER_DEBUG] No filters configured for source %s, returning %d streams unchanged\n", source.Name, len(streams))

		return streams
	}
	logger.Debug("[FILTER_DEBUG] Applying filters to %d streams from source %s\n", len(streams), source.Name)

	filter := filterManager.GetOrCreateFilter(source, debug)
	filtered := make([]*types.Stream, 0, len(streams))

	for _, stream := range streams {
		contentType := getContentType(stream, debug)
		shouldInclude := shouldIncludeStream(stream, filter, debug)
		logger.Debug("[FILTER_DEBUG] Stream: %s, Type: %s, Include: %v\n", stream.Name, contentType, shouldInclude)

		if shouldInclude {
			filtered = append(filtered, stream)
		}
	}
	logger.Debug("[FILTER_DEBUG] Filtered %d -> %d streams for source %s\n", len(streams), len(filtered), source.Name)

	return filtered
}

// shouldIncludeStream determines if a stream should be included based on filters
func shouldIncludeStream(stream *types.Stream, filter *CompiledFilter, debug bool) bool {
	streamName := strings.TrimSpace(strings.ToLower(stream.Name))
	originalName := stream.Name
	contentType := getContentType(stream, debug)

	logger.Debug("[FILTER_DEBUG] Evaluating stream: '%s', trimmed lowercase: '%s', content type: %s\n", originalName, streamName, contentType)

	// Check include filters first - if any exist, stream must match at least one
	var hasIncludeFilters bool
	var matchesInclude bool

	switch contentType {
	case "live":
		if filter.LiveInclude != nil {
			hasIncludeFilters = true
			matchesInclude = filter.LiveInclude.MatchString(streamName)
			logger.Debug("[FILTER_DEBUG] Live include pattern exists, matches: %v (pattern tested against: '%s')\n", matchesInclude, streamName)

		}
	case "series":
		if filter.SeriesInclude != nil {
			hasIncludeFilters = true
			matchesInclude = filter.SeriesInclude.MatchString(streamName)
			logger.Debug("[FILTER_DEBUG] Series include pattern exists, matches: %v\n", matchesInclude)

		}
	case "vod":
		if filter.VODInclude != nil {
			hasIncludeFilters = true
			matchesInclude = filter.VODInclude.MatchString(streamName)
			logger.Debug("[FILTER_DEBUG] VOD include pattern exists, matches: %v\n", matchesInclude)

		}
	}

	logger.Debug("[FILTER_DEBUG] hasIncludeFilters: %v, matchesInclude: %v\n", hasIncludeFilters, matchesInclude)

	// If include filters exist but stream doesn't match any, exclude it
	if hasIncludeFilters && !matchesInclude {
		logger.Debug("[FILTER_DEBUG] EXCLUDED by include filters: '%s'\n", originalName)

		return false
	}

	// Then check exclude filters
	switch contentType {
	case "live":
		if filter.LiveExclude != nil {
			if filter.LiveExclude.MatchString(streamName) {
				logger.Debug("[FILTER_DEBUG] EXCLUDED by live exclude filter: '%s'\n", originalName)

				return false
			}
			logger.Debug("[FILTER_DEBUG] Live exclude pattern exists but didn't match: '%s'\n", streamName)

		}
	case "series":
		if filter.SeriesExclude != nil {
			if filter.SeriesExclude.MatchString(streamName) {
				logger.Debug("[FILTER_DEBUG] EXCLUDED by series exclude filter: '%s'\n", originalName)

				return false
			}
		}
	case "vod":
		if filter.VODExclude != nil {
			if filter.VODExclude.MatchString(streamName) {
				logger.Debug("[FILTER_DEBUG] EXCLUDED by VOD exclude filter: '%s'\n", originalName)

				return false
			}
		}
	}

	logger.Debug("[FILTER_DEBUG] INCLUDED: '%s'\n", originalName)

	return true
}

// getContentType determines the content type from stream attributes
func getContentType(stream *types.Stream, debug bool) string {
	// First check using the same regex patterns used during import
	streamName := stream.Name
	streamURL := stream.URL

	// Check both name and URL with series regex
	seriesNameMatch := seriesRegex.MatchString(streamName)
	vodNameMatch := vodRegex.MatchString(streamName)
	seriesURLMatch := seriesRegex.MatchString(streamURL)
	vodURLMatch := vodRegex.MatchString(streamURL)

	if debug {
		fmt.Printf("[FILTER_DEBUG] Content type detection for '%s':\n", streamName)
		fmt.Printf("[FILTER_DEBUG]   SeriesName: %v, VODName: %v, SeriesURL: %v, VODURL: %v\n",
			seriesNameMatch, vodNameMatch, seriesURLMatch, vodURLMatch)
	}

	// Apply same logic as import parsing
	if seriesNameMatch || seriesURLMatch {
		logger.Debug("[FILTER_DEBUG] Classified as SERIES\n")

		return "series"
	}
	if vodNameMatch || vodURLMatch {
		logger.Debug("[FILTER_DEBUG] Classified as VOD\n")

		return "vod"
	}

	// Fallback to attribute-based detection (secondary method)
	if group, ok := stream.Attributes["group-title"]; ok {
		groupLower := strings.ToLower(group)
		logger.Debug("[FILTER_DEBUG] Content type from group-title '%s': ", group)

		switch {
		case strings.Contains(groupLower, "series"):
			logger.Debug("series\n")
			return "series"
		case strings.Contains(groupLower, "vod") || strings.Contains(groupLower, "movie"):
			logger.Debug("vod\n")
			return "vod"
		case strings.Contains(groupLower, "live") || strings.Contains(groupLower, "tv"):
			logger.Debug("live\n")
			return "live"
		}
	}

	// Also check tvg-group attribute
	if group, ok := stream.Attributes["tvg-group"]; ok {
		groupLower := strings.ToLower(group)
		logger.Debug("[FILTER_DEBUG] Content type from tvg-group '%s': ", group)

		switch {
		case strings.Contains(groupLower, "series"):
			logger.Debug("series\n")
			return "series"
		case strings.Contains(groupLower, "vod") || strings.Contains(groupLower, "movie"):
			logger.Debug("vod\n")
			return "vod"
		case strings.Contains(groupLower, "live") || strings.Contains(groupLower, "tv"):
			logger.Debug("live\n")
			return "live"
		}
	}

	// Default to live
	logger.Debug("[FILTER_DEBUG] Defaulted to LIVE\n")
	return "live"
}
