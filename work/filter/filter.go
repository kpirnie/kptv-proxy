package filter

import (
	"kptv-proxy/work/config"
	"kptv-proxy/work/types"
	"strings"
	"sync"

	"github.com/grafana/regexp"
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
		filter.LiveInclude, _ = regexp.Compile(source.LiveIncludeRegex)
	}
	if source.LiveExcludeRegex != "" {
		filter.LiveExclude, _ = regexp.Compile(source.LiveExcludeRegex)
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
func FilterStreams(streams []*types.Stream, source *config.SourceConfig, filterManager *FilterManager) []*types.Stream {
	if source.LiveIncludeRegex == "" && source.LiveExcludeRegex == "" &&
		source.SeriesIncludeRegex == "" && source.SeriesExcludeRegex == "" &&
		source.VODIncludeRegex == "" && source.VODExcludeRegex == "" {
		return streams
	}

	filter := filterManager.GetOrCreateFilter(source)
	filtered := make([]*types.Stream, 0, len(streams))

	for _, stream := range streams {
		if shouldIncludeStream(stream, filter) {
			filtered = append(filtered, stream)
		}
	}

	return filtered
}

// shouldIncludeStream determines if a stream should be included based on filters
func shouldIncludeStream(stream *types.Stream, filter *CompiledFilter) bool {
	streamName := strings.ToLower(stream.Name)
	contentType := getContentType(stream)

	// Check include filters first (take precedence)
	switch contentType {
	case "live":
		if filter.LiveInclude != nil {
			if filter.LiveInclude.MatchString(streamName) {
				return true
			}
		}
	case "series":
		if filter.SeriesInclude != nil {
			if filter.SeriesInclude.MatchString(streamName) {
				return true
			}
		}
	case "vod":
		if filter.VODInclude != nil {
			if filter.VODInclude.MatchString(streamName) {
				return true
			}
		}
	}

	// Then check exclude filters
	switch contentType {
	case "live":
		if filter.LiveExclude != nil {
			if filter.LiveExclude.MatchString(streamName) {
				return false
			}
		}
	case "series":
		if filter.SeriesExclude != nil {
			if filter.SeriesExclude.MatchString(streamName) {
				return false
			}
		}
	case "vod":
		if filter.VODExclude != nil {
			if filter.VODExclude.MatchString(streamName) {
				return false
			}
		}
	}

	// If no specific filters matched, include the stream
	return true
}

// getContentType determines the content type from stream attributes
func getContentType(stream *types.Stream) string {
	// Check stream attributes for content type hints
	if group, ok := stream.Attributes["group-title"]; ok {
		groupLower := strings.ToLower(group)
		switch {
		case strings.Contains(groupLower, "live") || strings.Contains(groupLower, "tv"):
			return "live"
		case strings.Contains(groupLower, "series"):
			return "series"
		case strings.Contains(groupLower, "vod") || strings.Contains(groupLower, "movie"):
			return "vod"
		}
	}

	// Also check tvg-group attribute
	if group, ok := stream.Attributes["tvg-group"]; ok {
		groupLower := strings.ToLower(group)
		switch {
		case strings.Contains(groupLower, "live") || strings.Contains(groupLower, "tv"):
			return "live"
		case strings.Contains(groupLower, "series"):
			return "series"
		case strings.Contains(groupLower, "vod") || strings.Contains(groupLower, "movie"):
			return "vod"
		}
	}

	// Check stream name for hints
	nameLower := strings.ToLower(stream.Name)
	switch {
	case strings.Contains(nameLower, "series") || strings.Contains(nameLower, "episode"):
		return "series"
	case strings.Contains(nameLower, "movie") || strings.Contains(nameLower, "vod"):
		return "vod"
	default:
		return "live" // Default to live
	}
}
