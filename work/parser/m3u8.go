package parser

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"kptv-proxy/work/config"
	"kptv-proxy/work/parser"
	"kptv-proxy/work/proxy"
	"kptv-proxy/work/stream"
	"kptv-proxy/work/utils"

	"github.com/grafov/m3u8"
)

func (sp *proxy.StreamProxy) parseM3U8(source *config.SourceConfig) []*stream.Stream {
	sp.logger.Printf("Parsing M3U8 from %s", utils.LogURL(config, source.URL))

	req, err := http.NewRequest("GET", source.URL, nil)
	if err != nil {
		sp.logger.Printf("Error creating request for %s: %v", utils.LogURL(source.URL), err)
		return nil
	}

	resp, err := client.httpClient.Do(req)
	if err != nil {
		sp.logger.Printf("Error fetching M3U8 from %s: %v", utils.LogURL(source.URL), err)
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		sp.logger.Printf("HTTP error %d when fetching %s", resp.StatusCode, utils.LogURL(source.URL))
		return nil
	}

	// Try parsing with grafov/m3u8 first
	playlist, listType, err := m3u8.DecodeFrom(bufio.NewReader(resp.Body), true)
	if err == nil {
		return parser.ParseWithGrafov(playlist, listType, source)
	}

	// Fallback to original parsing if grafov fails
	sp.logger.Printf("Grafov parser failed, using fallback parser: %v", err)
	resp.Body.Close()

	// Re-fetch for fallback parser
	resp2, err := sp.httpClient.Do(req)
	if err != nil {
		return nil
	}
	defer resp2.Body.Close()

	return sp.parseM3U8Fallback(resp2.Body, source)
}

func (sp *StreamProxy) parseWithGrafov(playlist m3u8.Playlist, listType m3u8.ListType, source *SourceConfig) []*Stream {
	var streams []*Stream

	switch listType {
	case m3u8.MEDIA:
		//mediapl := playlist.(*m3u8.MediaPlaylist)
		// For media playlists, we typically want the playlist URL itself, not individual segments
		stream := &Stream{
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

			stream := &Stream{
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

	sp.logger.Printf("Grafov parser found %d streams from %s", len(streams), sp.logURL(source.URL))
	return streams
}

func (sp *StreamProxy) parseM3U8Fallback(reader io.Reader, source *SourceConfig) []*Stream {
	var streams []*Stream
	scanner := bufio.NewScanner(reader)
	var currentAttrs map[string]string
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())

		if sp.config.Debug && lineNum <= 10 {
			sp.logger.Printf("Line %d: %s", lineNum, line)
		}

		if strings.HasPrefix(line, "#EXTINF:") {
			currentAttrs = parseEXTINF(line)
			if sp.config.Debug {
				sp.logger.Printf("Parsed EXTINF attributes: %+v", currentAttrs)
			}
		} else if currentAttrs != nil && (strings.HasPrefix(line, "http://") || strings.HasPrefix(line, "https://")) {
			stream := &Stream{
				URL:        line,
				Name:       currentAttrs["tvg-name"],
				Attributes: currentAttrs,
				Source:     source,
			}

			if stream.Name == "" {
				stream.Name = "Unknown"
			}

			streams = append(streams, stream)
			if sp.config.Debug {
				sp.logger.Printf("Added stream: %s (URL: %s)", stream.Name, sp.logURL(stream.URL))
			}
			currentAttrs = nil
		}
	}

	sp.logger.Printf("Fallback parser found %d streams from %s", len(streams), sp.logURL(source.URL))
	return streams
}

func parseEXTINF(line string) map[string]string {
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

func (sp *StreamProxy) sortStreams(streams []*Stream) {
	sort.SliceStable(streams, func(i, j int) bool {
		val1 := streams[i].Attributes[sp.config.SortField]
		val2 := streams[j].Attributes[sp.config.SortField]

		if sp.config.SortDirection == "desc" {
			return val1 > val2
		}
		return val1 < val2
	})
}
