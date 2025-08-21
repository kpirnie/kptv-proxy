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
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/grafov/m3u8"
)

func ParseM3U8(httpClient *client.HeaderSettingClient, logger *log.Logger, cfg *config.Config, source *config.SourceConfig) []*types.Stream {
	if cfg.Debug {
		logger.Printf("Parsing M3U8 from %s", utils.LogURL(cfg, source.URL))
	}

	// Create request with timeout for parsing
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

	resp, err := httpClient.Do(req)
	if err != nil {
		if cfg.Debug {
			logger.Printf("Error fetching M3U8 from %s: %v", utils.LogURL(cfg, source.URL), err)
		}

		return nil
	}

	// CRITICAL: Always close the response body
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

	// Try parsing with grafov/m3u8 first
	playlist, listType, err := m3u8.DecodeFrom(bufio.NewReader(resp.Body), true)
	if err == nil {
		if cfg.Debug {
			logger.Printf("Successfully parsed with grafov parser: %s", utils.LogURL(cfg, source.URL))
		}

		return ParseWithGrafov(playlist, listType, source, cfg, logger)
	}

	// Fallback to original parsing if grafov fails
	if cfg.Debug {
		logger.Printf("Grafov parser failed, using fallback parser: %v", err)
	}

	// Close current response and re-fetch for fallback parser
	resp.Body.Close()

	// Re-fetch for fallback parser
	req2, err := http.NewRequest("GET", source.URL, nil)
	if err != nil {
		if cfg.Debug {
			logger.Printf("Error creating fallback request for %s: %v", utils.LogURL(cfg, source.URL), err)
		}

		return nil
	}
	req2 = req2.WithContext(ctx)

	resp2, err := httpClient.Do(req2)
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

	return ParseM3U8Fallback(resp2.Body, source, cfg, logger)
}

func ParseWithGrafov(playlist m3u8.Playlist, listType m3u8.ListType, source *config.SourceConfig, cfg *config.Config, logger *log.Logger) []*types.Stream {
	var streams []*types.Stream

	switch listType {
	case m3u8.MEDIA:
		// For media playlists, we typically want the playlist URL itself, not individual segments
		stream := &types.Stream{
			URL:        source.URL,
			Name:       "Direct Stream",
			Source:     source,
			Attributes: make(map[string]string),
		}
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

			streams = append(streams, stream)
		}
	}

	if cfg.Debug {
		logger.Printf("Grafov parser found %d streams from %s", len(streams), utils.LogURL(cfg, source.URL))
	}

	return streams
}

func ParseM3U8Fallback(reader io.Reader, source *config.SourceConfig, cfg *config.Config, logger *log.Logger) []*types.Stream {
	var streams []*types.Stream
	scanner := bufio.NewScanner(reader)
	var currentAttrs map[string]string
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())

		if strings.HasPrefix(line, "#EXTINF:") {
			currentAttrs = ParseEXTINF(line)
			if cfg.Debug {
				logger.Printf("Parsed EXTINF attributes: %+v", currentAttrs)
			}
		} else if currentAttrs != nil && (strings.HasPrefix(line, "http://") || strings.HasPrefix(line, "https://")) {
			stream := &types.Stream{
				URL:        line,
				Name:       currentAttrs["tvg-name"],
				Attributes: currentAttrs,
				Source:     source,
			}

			if stream.Name == "" {
				stream.Name = "Unknown"
			}

			streams = append(streams, stream)
			if cfg.Debug {
				logger.Printf("Added stream: %s (URL: %s)", stream.Name, utils.LogURL(cfg, stream.URL))
			}
			currentAttrs = nil
		}
	}

	if cfg.Debug {
		logger.Printf("Fallback parser found %d streams from %s", len(streams), utils.LogURL(cfg, source.URL))
	}

	return streams
}

func ParseEXTINF(line string) map[string]string {
	attrs := make(map[string]string)

	// Remove #EXTINF: prefix
	line = strings.TrimPrefix(line, "#EXTINF:")

	// Find the last comma that separates attributes from channel name
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

	if lastComma == -1 {
		return attrs
	}

	// Extract the parts
	attrPart := strings.TrimSpace(line[:lastComma])
	channelName := strings.TrimSpace(line[lastComma+1:])

	// Parse duration and attributes
	parts := strings.Fields(attrPart)
	if len(parts) > 0 {
		attrs["duration"] = parts[0]
	}

	// Parse key-value attributes
	for i := 1; i < len(parts); i++ {
		part := parts[i]
		if eqIdx := strings.Index(part, "="); eqIdx != -1 {
			key := part[:eqIdx]
			value := strings.Trim(part[eqIdx+1:], "\"")
			attrs[key] = value
		}
	}

	// Store the channel name
	if channelName != "" {
		attrs["tvg-name"] = channelName
	}

	return attrs
}

func SortStreams(streams []*types.Stream, cfg *config.Config) {
	sort.SliceStable(streams, func(i, j int) bool {
		val1 := streams[i].Attributes[cfg.SortField]
		val2 := streams[j].Attributes[cfg.SortField]

		if cfg.SortDirection == "desc" {
			return val1 > val2
		}
		return val1 < val2
	})
}
