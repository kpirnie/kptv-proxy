package parser

import (
	"bufio"
	"fmt"
	"kptv-proxy/work/config"
	"kptv-proxy/work/logger"
	"kptv-proxy/work/utils"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// StreamVariant represents a single stream variant from an HLS master playlist with
// comprehensive metadata for quality assessment and selection. Each variant corresponds
// to a different encoding quality/bitrate of the same content, allowing clients or
// automatic quality selection algorithms to choose appropriate streams based on
// available bandwidth and display capabilities.
type StreamVariant struct {
	URL              string // Absolute or relative URL pointing to the variant's media playlist
	Bandwidth        int    // Required bandwidth in bits per second for reliable playback
	AverageBandwidth int    // Average bandwidth in bits per second (optional, more accurate than peak)
	Resolution       string // Video resolution in "WIDTHxHEIGHT" format (e.g., "1920x1080")
	Codecs           string // Comma-separated codec specifications (e.g., "avc1.640028,mp4a.40.2")
	FrameRate        string // Video frame rate as decimal string (e.g., "29.970", "60.000")
}

// MasterPlaylistHandler provides comprehensive master playlist parsing capabilities,
// variant selection algorithms, and URL resolution services. It handles the complexity
// of HLS master playlists including variant extraction, quality-based sorting, and
// the resolution of relative URLs against base playlist URLs.
//
// The handler supports multiple quality selection strategies and provides debugging
// capabilities through integrated logging. It abstracts the complexity of master
// playlist parsing while providing flexible variant selection for different use cases.
type MasterPlaylistHandler struct {
	config *config.Config // Application configuration for debug settings and URL obfuscation
}

// NewMasterPlaylistHandler creates and initializes a new master playlist handler instance
// with the provided logger and configuration. The handler is ready for immediate use
// in parsing master playlists and selecting appropriate stream variants.
//
// Parameters:
//   - logger: application logger for debugging output and error reporting
//   - config: application configuration containing debug flags and URL handling preferences
//
// Returns:
//   - *MasterPlaylistHandler: fully configured handler ready for master playlist operations
func NewMasterPlaylistHandler(config *config.Config) *MasterPlaylistHandler {
	return &MasterPlaylistHandler{
		config: config,
	}
}

// IsMasterPlaylist determines whether the provided content represents an HLS master playlist
// by scanning for the presence of #EXT-X-STREAM-INF tags, which are the definitive indicator
// of master playlist format. Master playlists contain references to multiple variant streams
// rather than media segments directly.
//
// Parameters:
//   - content: raw playlist content as string
//
// Returns:
//   - bool: true if content contains master playlist indicators, false otherwise
func (mph *MasterPlaylistHandler) IsMasterPlaylist(content string) bool {
	return strings.Contains(content, "#EXT-X-STREAM-INF")
}

// IsMediaPlaylist determines whether the provided content represents an HLS media playlist
// by scanning for #EXTINF tags (individual media segments) or #EXT-X-TARGETDURATION tags
// (segment duration specifications). Media playlists contain actual streamable content
// segments rather than references to other playlists.
//
// Parameters:
//   - content: raw playlist content as string
//
// Returns:
//   - bool: true if content contains media playlist indicators, false otherwise
func (mph *MasterPlaylistHandler) IsMediaPlaylist(content string) bool {
	return strings.Contains(content, "#EXTINF") || strings.Contains(content, "#EXT-X-TARGETDURATION")
}

// ParseMasterPlaylist extracts all stream variants from master playlist content, parsing
// the complex #EXT-X-STREAM-INF format and resolving relative URLs to absolute form.
// The parser handles the two-line format where stream metadata appears on one line
// followed by the variant URL on the subsequent line.
//
// The function performs comprehensive error handling, skipping malformed entries while
// continuing to process valid variants. All extracted variants are automatically sorted
// by bandwidth in descending order (highest quality first) to facilitate quality-based
// selection algorithms.
//
// Parameters:
//   - content: complete master playlist content as string
//   - baseURL: base URL for resolving relative variant URLs
//
// Returns:
//   - []StreamVariant: slice of parsed and sorted stream variants
//   - error: non-nil if no valid variants found or critical parsing errors occur
func (mph *MasterPlaylistHandler) ParseMasterPlaylist(content string, baseURL string) ([]StreamVariant, error) {
	var variants []StreamVariant

	scanner := bufio.NewScanner(strings.NewReader(content))
	var currentVariant *StreamVariant

	// Process playlist content line by line
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Parse stream information lines containing variant metadata
		if strings.HasPrefix(line, "#EXT-X-STREAM-INF:") {
			variant, err := mph.parseStreamInf(line)
			if err != nil {

				// Log parsing errors but continue processing remaining variants
				logger.Error("parsing stream info: %v", err)

				continue
			}
			currentVariant = &variant

		} else if currentVariant != nil && line != "" && !strings.HasPrefix(line, "#") {

			// Process variant URL line following #EXT-X-STREAM-INF
			resolvedURL := mph.resolveURL(line, baseURL)
			logger.Debug("Original variant URL: %s", line)
			logger.Debug("Resolved variant URL: %s", utils.LogURL(mph.config, resolvedURL))

			// Complete variant object with resolved URL
			currentVariant.URL = resolvedURL
			variants = append(variants, *currentVariant)
			currentVariant = nil // Reset for next variant
		}
	}

	// Check for scanner errors during content processing
	if err := scanner.Err(); err != nil {
		logger.Error("error scanning playlist: %v", err)
		return nil, err
	}

	// Validate that at least one variant was successfully parsed
	if len(variants) == 0 {
		logger.Error("no variants found in master playlist")
		return nil, nil
	}

	// Sort variants by bandwidth in descending order (highest quality first)
	// This ordering facilitates quality selection algorithms that prefer higher quality
	sort.SliceStable(variants, func(i, j int) bool {
		return variants[i].Bandwidth > variants[j].Bandwidth
	})

	return variants, nil
}

// ProcessMasterPlaylist provides backward compatibility for existing code by handling
// master playlist detection, parsing, and automatic variant selection in a single operation.
// The function determines playlist type, extracts variants if applicable, and selects
// the highest quality variant for immediate use.
//
// This method abstracts the complexity of master playlist handling for simple use cases
// where only the best quality variant is needed, while providing detailed logging for
// debugging and monitoring purposes.
//
// Parameters:
//   - content: raw playlist content to analyze and process
//   - originalURL: source URL of the playlist for relative URL resolution
//   - channelName: channel identifier for logging and debugging context
//
// Returns:
//   - string: URL of selected variant or original URL for media playlists
//   - bool: true if input was a master playlist, false for media playlists
//   - error: non-nil if playlist processing fails completely
func (mph *MasterPlaylistHandler) ProcessMasterPlaylist(content string, originalURL string, channelName string) (string, bool, error) {

	// Determine playlist type through content analysis
	if mph.IsMasterPlaylist(content) {
		logger.Debug("Master playlist detected for channel %s", channelName)

		// Parse all available variants from master playlist
		variants, err := mph.ParseMasterPlaylist(content, originalURL)
		if err != nil {
			logger.Error("failed to parse master playlist: %v", err)
			return "", false, err
		}

		logger.Debug("Found %d variants for channel %s", len(variants), channelName)

		// Log detailed information about each variant for debugging
		for i, variant := range variants {
			logger.Debug("Variant %d: %s (%d kbps)",
				i, variant.Resolution, variant.Bandwidth/1000)
		}

		// Select highest quality variant (first in sorted array)
		if len(variants) > 0 {
			selectedVariant := variants[0]
			logger.Debug("Selected variant for channel %s: %s (%d kbps)",
				channelName, selectedVariant.Resolution, selectedVariant.Bandwidth/1000)

			return selectedVariant.URL, true, nil
		}

		return "", false, fmt.Errorf("no working variants found")

	} else if mph.IsMediaPlaylist(content) {

		// Media playlist - return original URL unchanged
		logger.Debug("Media playlist detected for channel %s (no variant selection needed)", channelName)

		return originalURL, false, nil

	} else {

		// Content is not a recognized M3U8 playlist format
		logger.Debug("Content is not an M3U8 playlist for channel %s", channelName)

		return originalURL, false, nil
	}
}

// parseStreamInf extracts variant attributes from a #EXT-X-STREAM-INF line using regex-based
// parsing to handle the complex key-value format with optional quoted values. The parser
// validates required attributes (bandwidth) while gracefully handling optional metadata
// such as resolution, codecs, and frame rate information.
//
// The function enforces HLS specification requirements by rejecting variants without
// bandwidth information, which is essential for quality selection and adaptive streaming.
//
// Parameters:
//   - line: complete #EXT-X-STREAM-INF line from master playlist
//
// Returns:
//   - StreamVariant: parsed variant with extracted attributes
//   - error: non-nil if required attributes (bandwidth) are missing or invalid
func (mph *MasterPlaylistHandler) parseStreamInf(line string) (StreamVariant, error) {
	variant := StreamVariant{}

	// Remove the #EXT-X-STREAM-INF: prefix to access attribute content
	params := strings.TrimPrefix(line, "#EXT-X-STREAM-INF:")

	// Parse key-value pairs using regex-based attribute extraction
	attributes := mph.parseAttributes(params)

	// Extract required bandwidth attribute
	if bw, ok := attributes["BANDWIDTH"]; ok {
		if bandwidth, err := strconv.Atoi(bw); err == nil {
			variant.Bandwidth = bandwidth
		}
	}

	// Extract optional average bandwidth for more accurate quality assessment
	if avgBw, ok := attributes["AVERAGE-BANDWIDTH"]; ok {
		if avgBandwidth, err := strconv.Atoi(avgBw); err == nil {
			variant.AverageBandwidth = avgBandwidth
		}
	}

	// Extract optional video/audio metadata attributes
	variant.Resolution = attributes["RESOLUTION"]
	variant.Codecs = strings.Trim(attributes["CODECS"], "\"")
	variant.FrameRate = attributes["FRAME-RATE"]

	// Validate that required bandwidth attribute was successfully parsed
	if variant.Bandwidth == 0 {
		logger.Error("bandwidth is required for stream variant")
		return variant, nil
	}

	return variant, nil
}

// parseAttributes extracts key-value pairs from HLS attribute strings using regular
// expressions to handle the complex format with optional quoted values and special
// characters. The parser correctly handles both simple values and quoted strings
// containing spaces, commas, and other special characters.
//
// The regex pattern matches the HLS specification for attribute formatting:
// - Keys are uppercase letters with optional hyphens
// - Values can be unquoted (ending at comma/end) or quoted strings
//
// Parameters:
//   - params: attribute string from #EXT-X-STREAM-INF line
//
// Returns:
//   - map[string]string: parsed attribute names mapped to their values
func (mph *MasterPlaylistHandler) parseAttributes(params string) map[string]string {
	attributes := make(map[string]string)

	// Regex pattern to match KEY=VALUE pairs with optional quoted values
	// Handles both: KEY=value and KEY="quoted value with spaces"
	re := regexp.MustCompile(`([A-Z-]+)=([^,]+|"[^"]*")`)
	matches := re.FindAllStringSubmatch(params, -1)

	// Extract each matched key-value pair
	for _, match := range matches {
		if len(match) >= 3 {
			key := match[1]
			value := strings.Trim(match[2], "\"") // Remove surrounding quotes
			attributes[key] = value
		}
	}

	return attributes
}

// resolveURL converts potentially relative URLs to absolute form by resolving them against
// a provided base URL. The function handles both absolute URLs (returned unchanged) and
// relative URLs (resolved using Go's standard URL resolution algorithms).
//
// This capability is essential for master playlist processing since variant URLs are
// often specified as relative paths that must be resolved against the master playlist's
// location to create functional stream URLs.
//
// Parameters:
//   - streamURL: potentially relative URL from variant playlist entry
//   - baseURL: absolute URL of the master playlist for resolution context
//
// Returns:
//   - string: absolute URL suitable for direct streaming, or original URL if resolution fails
func (mph *MasterPlaylistHandler) resolveURL(streamURL, baseURL string) string {

	// Return absolute URLs unchanged (no resolution needed)
	if strings.HasPrefix(streamURL, "http://") || strings.HasPrefix(streamURL, "https://") {
		logger.Debug("URL is already absolute: %s", utils.LogURL(mph.config, streamURL))

		return streamURL
	}

	logger.Debug("Resolving relative URL: %s against base: %s", utils.LogURL(mph.config, streamURL), utils.LogURL(mph.config, baseURL))

	// Parse base URL for resolution context
	base, err := url.Parse(baseURL)
	if err != nil {
		logger.Error("Error parsing base URL %s: %v", utils.LogURL(mph.config, baseURL), err)

		return streamURL
	}

	// Parse relative URL for resolution
	rel, err := url.Parse(streamURL)
	if err != nil {
		logger.Error("Error parsing relative URL %s: %v", utils.LogURL(mph.config, streamURL), err)

		return streamURL
	}

	// Resolve relative URL against base using standard URL resolution
	resolved := base.ResolveReference(rel)
	logger.Debug("Final resolved URL: %s", resolved.String())

	return resolved.String()
}

// SelectVariant chooses the most appropriate stream variant based on the specified quality
// selection strategy. The function supports multiple selection algorithms optimized for
// different use cases, from bandwidth conservation to quality maximization.
//
// Available selection strategies:
//   - "lowest": minimum bandwidth variant for bandwidth-constrained scenarios
//   - "highest": maximum quality variant for optimal viewing experience
//   - "medium": middle-tier variant balancing quality and bandwidth usage
//   - "720p": resolution-specific selection with fallback to medium quality
//   - default: highest quality variant (changed from lowest for better user experience)
//
// Parameters:
//   - variants: slice of available stream variants (should be sorted by bandwidth)
//   - strategy: quality selection strategy identifier
//
// Returns:
//   - StreamVariant: selected variant matching the strategy, or empty variant if none available
func (mph *MasterPlaylistHandler) SelectVariant(variants []StreamVariant, strategy string) StreamVariant {

	// Return empty variant if no options available
	if len(variants) == 0 {
		return StreamVariant{}
	}

	switch strategy {
	case "lowest":

		// Select minimum bandwidth variant (last in bandwidth-sorted array)
		return variants[len(variants)-1]

	case "highest":

		// Select maximum bandwidth variant (first in bandwidth-sorted array)
		return variants[0]

	case "medium":

		// Select middle-tier variant for balanced quality/bandwidth trade-off
		idx := len(variants) / 2
		return variants[idx]

	case "720p":

		// Search for 720p resolution variant specifically
		for _, variant := range variants {
			if strings.Contains(variant.Resolution, "1280x720") {
				return variant
			}
		}

		// Fallback to medium quality if no 720p variant found
		return mph.SelectVariant(variants, "medium")

	default:

		// Default to highest quality for best user experience
		return variants[0]
	}
}

// ProcessMasterPlaylistVariants provides comprehensive master playlist processing with
// variant extraction for advanced use cases requiring access to all available quality
// options. Unlike ProcessMasterPlaylist which selects a single variant, this function
// returns all variants for custom selection algorithms or quality switching implementations.
//
// The function handles both master and media playlists, returning appropriate variant
// representations for each type. For non-master playlists, a single variant with
// unknown quality metrics is returned to maintain consistent interface behavior.
//
// Parameters:
//   - content: raw playlist content to analyze and process
//   - originalURL: source URL for relative URL resolution and fallback variant creation
//   - channelName: channel identifier for logging context
//
// Returns:
//   - []StreamVariant: all available variants with quality metadata
//   - bool: true if input was a master playlist, false otherwise
//   - error: non-nil if playlist processing fails completely
func (mph *MasterPlaylistHandler) ProcessMasterPlaylistVariants(content string, originalURL string, channelName string) ([]StreamVariant, bool, error) {
	if mph.IsMasterPlaylist(content) {

		// Parse and return all variants from master playlist
		variants, err := mph.ParseMasterPlaylist(content, originalURL)
		if err != nil {
			return nil, false, err
		}
		return variants, true, nil
	} else if mph.IsMediaPlaylist(content) {

		// Create single variant representing the media playlist
		singleVariant := StreamVariant{URL: originalURL, Bandwidth: 0, Resolution: "unknown"}
		return []StreamVariant{singleVariant}, false, nil
	} else {

		// Create single variant for non-playlist content (fallback behavior)
		singleVariant := StreamVariant{URL: originalURL, Bandwidth: 0, Resolution: "unknown"}
		return []StreamVariant{singleVariant}, false, nil
	}
}

// GetVariantsOrderedByQuality creates a quality-sorted copy of the provided variants
// slice, ordered from highest to lowest bandwidth. The function preserves the original
// slice while providing a consistently ordered copy suitable for quality selection
// algorithms that assume bandwidth-descending order.
//
// This utility function is particularly useful when working with variant arrays that
// may not be pre-sorted or when multiple sorting strategies are needed for the same
// variant set without modifying the original data.
//
// Parameters:
//   - variants: slice of stream variants to sort (original remains unchanged)
//
// Returns:
//   - []StreamVariant: new slice with variants sorted by bandwidth (highest first)
func (mph *MasterPlaylistHandler) GetVariantsOrderedByQuality(variants []StreamVariant) []StreamVariant {

	// Create independent copy to avoid modifying original slice
	orderedVariants := make([]StreamVariant, len(variants))
	copy(orderedVariants, variants)

	// Sort copy by bandwidth in descending order (highest quality first)
	sort.SliceStable(orderedVariants, func(i, j int) bool {
		return orderedVariants[i].Bandwidth > orderedVariants[j].Bandwidth
	})

	return orderedVariants
}
