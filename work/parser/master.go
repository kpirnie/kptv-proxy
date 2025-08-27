package parser

import (
	"bufio"
	"fmt"
	"kptv-proxy/work/config"
	"kptv-proxy/work/utils"
	"log"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// StreamVariant represents a single variant in a master playlist with its metadata.
type StreamVariant struct {
	URL              string // The URL of the variant stream
	Bandwidth        int    // Bandwidth in bits per second
	AverageBandwidth int    // Average bandwidth in bits per second (optional)
	Resolution       string // Video resolution (e.g., "1280x720")
	Codecs           string // Codec information
	FrameRate        string // Frame rate (e.g., "30.000")
}

// MasterPlaylistHandler handles master playlist parsing, variant selection, and URL resolution.
type MasterPlaylistHandler struct {
	logger *log.Logger
	config *config.Config
}

// NewMasterPlaylistHandler creates a new master playlist handler instance.
func NewMasterPlaylistHandler(logger *log.Logger, config *config.Config) *MasterPlaylistHandler {
	return &MasterPlaylistHandler{
		logger: logger,
		config: config,
	}
}

// IsMasterPlaylist checks if the content contains master playlist indicators (#EXT-X-STREAM-INF tags).
func (mph *MasterPlaylistHandler) IsMasterPlaylist(content string) bool {
	return strings.Contains(content, "#EXT-X-STREAM-INF")
}

// IsMediaPlaylist checks if the content contains media playlist indicators (#EXTINF or #EXT-X-TARGETDURATION tags).
func (mph *MasterPlaylistHandler) IsMediaPlaylist(content string) bool {
	return strings.Contains(content, "#EXTINF") || strings.Contains(content, "#EXT-X-TARGETDURATION")
}

// ParseMasterPlaylist parses a master playlist content and extracts all available stream variants.
// Returns a slice of StreamVariant objects sorted by bandwidth (highest first).
func (mph *MasterPlaylistHandler) ParseMasterPlaylist(content string, baseURL string) ([]StreamVariant, error) {
	var variants []StreamVariant

	scanner := bufio.NewScanner(strings.NewReader(content))
	var currentVariant *StreamVariant

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if strings.HasPrefix(line, "#EXT-X-STREAM-INF:") {
			// Parse stream info line
			variant, err := mph.parseStreamInf(line)
			if err != nil {
				if mph.config.Debug {
					mph.logger.Printf("Error parsing stream info: %v", err)
				}

				continue
			}
			currentVariant = &variant

		} else if currentVariant != nil && line != "" && !strings.HasPrefix(line, "#") {
			// This should be the URL for the previous #EXT-X-STREAM-INF
			resolvedURL := mph.resolveURL(line, baseURL)
			if mph.config.Debug {
				mph.logger.Printf("Original variant URL: %s", line)
				mph.logger.Printf("Resolved variant URL: %s", utils.LogURL(mph.config, resolvedURL))
			}

			currentVariant.URL = resolvedURL
			variants = append(variants, *currentVariant)
			currentVariant = nil
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error scanning playlist: %v", err)
	}

	if len(variants) == 0 {
		return nil, fmt.Errorf("no variants found in master playlist")
	}

	// Sort variants by bandwidth (HIGHEST first for better quality preference)
	sort.SliceStable(variants, func(i, j int) bool {
		return variants[i].Bandwidth > variants[j].Bandwidth
	})

	return variants, nil
}

// ProcessMasterPlaylist handles master playlist detection and variant selection (original method for backward compatibility).
// Returns the selected variant URL, a boolean indicating if it was a master playlist, and any error.
func (mph *MasterPlaylistHandler) ProcessMasterPlaylist(content string, originalURL string, channelName string) (string, bool, error) {
	// Check what type of playlist this is
	if mph.IsMasterPlaylist(content) {
		if mph.config.Debug {
			mph.logger.Printf("Master playlist detected for channel %s", channelName)
		}

		// Parse master playlist
		variants, err := mph.ParseMasterPlaylist(content, originalURL)
		if err != nil {
			return "", false, fmt.Errorf("failed to parse master playlist: %v", err)
		}
		if mph.config.Debug {
			mph.logger.Printf("Found %d variants for channel %s", len(variants), channelName)

			for i, variant := range variants {
				mph.logger.Printf("Variant %d: %s (%d kbps)",
					i, variant.Resolution, variant.Bandwidth/1000)
			}
		}

		if len(variants) > 0 {
			// Select highest quality variant (first in sorted array since we sort highest first)
			selectedVariant := variants[0]
			if mph.config.Debug {
				mph.logger.Printf("Selected variant for channel %s: %s (%d kbps)",
					channelName, selectedVariant.Resolution, selectedVariant.Bandwidth/1000)
			}

			return selectedVariant.URL, true, nil
		}

		return "", false, fmt.Errorf("no working variants found")

	} else if mph.IsMediaPlaylist(content) {
		if mph.config.Debug {
			mph.logger.Printf("Media playlist detected for channel %s (no variant selection needed)", channelName)
		}

		return originalURL, false, nil

	} else {
		// Not an M3U8 playlist at all
		if mph.config.Debug {
			mph.logger.Printf("Content is not an M3U8 playlist for channel %s", channelName)
		}

		return originalURL, false, nil
	}
}

// parseStreamInf parses a #EXT-X-STREAM-INF line and extracts variant attributes.
func (mph *MasterPlaylistHandler) parseStreamInf(line string) (StreamVariant, error) {
	variant := StreamVariant{}

	// Remove the tag prefix
	params := strings.TrimPrefix(line, "#EXT-X-STREAM-INF:")

	// Parse key-value pairs
	attributes := mph.parseAttributes(params)

	// Extract bandwidth (required)
	if bw, ok := attributes["BANDWIDTH"]; ok {
		if bandwidth, err := strconv.Atoi(bw); err == nil {
			variant.Bandwidth = bandwidth
		}
	}

	// Extract average bandwidth (optional)
	if avgBw, ok := attributes["AVERAGE-BANDWIDTH"]; ok {
		if avgBandwidth, err := strconv.Atoi(avgBw); err == nil {
			variant.AverageBandwidth = avgBandwidth
		}
	}

	// Extract other attributes
	variant.Resolution = attributes["RESOLUTION"]
	variant.Codecs = strings.Trim(attributes["CODECS"], "\"")
	variant.FrameRate = attributes["FRAME-RATE"]

	if variant.Bandwidth == 0 {
		return variant, fmt.Errorf("bandwidth is required for stream variant")
	}

	return variant, nil
}

// parseAttributes parses key=value pairs from a parameter string using regex.
// Handles quoted values and returns a map of attribute names to values.
func (mph *MasterPlaylistHandler) parseAttributes(params string) map[string]string {
	attributes := make(map[string]string)

	// Regex to match KEY=VALUE pairs, handling quoted values
	re := regexp.MustCompile(`([A-Z-]+)=([^,]+|"[^"]*")`)
	matches := re.FindAllStringSubmatch(params, -1)

	for _, match := range matches {
		if len(match) >= 3 {
			key := match[1]
			value := strings.Trim(match[2], "\"")
			attributes[key] = value
		}
	}

	return attributes
}

// resolveURL resolves a potentially relative URL against a base URL.
// Returns the absolute URL, or the original URL if resolution fails.
func (mph *MasterPlaylistHandler) resolveURL(streamURL, baseURL string) string {
	// If it's already an absolute URL, return as-is
	if strings.HasPrefix(streamURL, "http://") || strings.HasPrefix(streamURL, "https://") {
		if mph.config.Debug {
			mph.logger.Printf("URL is already absolute: %s", utils.LogURL(mph.config, streamURL))
		}

		return streamURL
	}
	if mph.config.Debug {
		mph.logger.Printf("Resolving relative URL: %s against base: %s", utils.LogURL(mph.config, streamURL), utils.LogURL(mph.config, baseURL))
	}

	// Parse base URL
	base, err := url.Parse(baseURL)
	if err != nil {
		if mph.config.Debug {
			mph.logger.Printf("Error parsing base URL %s: %v", utils.LogURL(mph.config, baseURL), err)
		}

		return streamURL
	}

	// Parse relative URL
	rel, err := url.Parse(streamURL)
	if err != nil {
		if mph.config.Debug {
			mph.logger.Printf("Error parsing relative URL %s: %v", utils.LogURL(mph.config, streamURL), err)
		}

		return streamURL
	}

	// Resolve relative URL against base
	resolved := base.ResolveReference(rel)
	if mph.config.Debug {
		mph.logger.Printf("Final resolved URL: %s", resolved.String())
	}

	return resolved.String()
}

// SelectVariant selects the best variant based on the specified quality strategy.
// Supported strategies: "lowest", "highest", "medium", "720p".
func (mph *MasterPlaylistHandler) SelectVariant(variants []StreamVariant, strategy string) StreamVariant {
	if len(variants) == 0 {
		return StreamVariant{}
	}

	switch strategy {
	case "lowest":
		// Select lowest bandwidth (last in sorted array since we sort highest first)
		return variants[len(variants)-1]

	case "highest":
		// Select highest bandwidth (first in sorted array)
		return variants[0]

	case "medium":
		// Select middle variant
		idx := len(variants) / 2
		return variants[idx]

	case "720p":
		// Prefer 720p resolution
		for _, variant := range variants {
			if strings.Contains(variant.Resolution, "1280x720") {
				return variant
			}
		}
		// Fallback to medium quality
		return mph.SelectVariant(variants, "medium")

	default:
		// Default to highest for best quality (changed from lowest)
		return variants[0]
	}
}

// ProcessMasterPlaylistVariants handles master playlist detection and returns ALL variants for testing purposes.
// Returns a slice of variants, a boolean indicating if it was a master playlist, and any error.
func (mph *MasterPlaylistHandler) ProcessMasterPlaylistVariants(content string, originalURL string, channelName string) ([]StreamVariant, bool, error) {
	if mph.IsMasterPlaylist(content) {
		variants, err := mph.ParseMasterPlaylist(content, originalURL)
		if err != nil {
			return nil, false, err
		}
		return variants, true, nil
	} else if mph.IsMediaPlaylist(content) {
		singleVariant := StreamVariant{URL: originalURL, Bandwidth: 0, Resolution: "unknown"}
		return []StreamVariant{singleVariant}, false, nil
	} else {
		singleVariant := StreamVariant{URL: originalURL, Bandwidth: 0, Resolution: "unknown"}
		return []StreamVariant{singleVariant}, false, nil
	}
}

// GetVariantsOrderedByQuality returns variants ordered from highest to lowest quality (bandwidth).
// Creates a copy of the input slice to avoid modifying the original.
func (mph *MasterPlaylistHandler) GetVariantsOrderedByQuality(variants []StreamVariant) []StreamVariant {
	// Make a copy to avoid modifying the original slice
	orderedVariants := make([]StreamVariant, len(variants))
	copy(orderedVariants, variants)

	// Sort by bandwidth, highest first
	sort.SliceStable(orderedVariants, func(i, j int) bool {
		return orderedVariants[i].Bandwidth > orderedVariants[j].Bandwidth
	})

	return orderedVariants
}
