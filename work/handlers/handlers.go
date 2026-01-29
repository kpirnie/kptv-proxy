package handlers

import (
	"context"
	"fmt"
	"io"
	"kptv-proxy/work/middleware"
	"kptv-proxy/work/proxy"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
)

// HandlePlaylist returns an HTTP handler function that generates a complete M3U8 playlist
// containing all available channels from all configured sources. The handler delegates
// to the StreamProxy's GeneratePlaylist method with an empty group filter, ensuring
// all channels are included regardless of their group classification.
//
// This handler is typically mounted at the root playlist endpoint (e.g., "/playlist.m3u8")
// and serves as the primary entry point for IPTV clients that want access to the full
// channel lineup without any filtering applied.
//
// Parameters:
//   - sp: pointer to the StreamProxy instance containing channel data and configuration
//
// Returns:
//   - http.HandlerFunc: HTTP handler that processes playlist requests and writes M3U8 responses
func HandlePlaylist(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		middleware.GzipMiddleware(func(w http.ResponseWriter, r *http.Request) {
			sp.GeneratePlaylist(w, r, "")
		})(w, r)
	}
}

// HandleGroupPlaylist returns an HTTP handler function that generates a filtered M3U8 playlist
// containing only channels belonging to a specific group. The group name is extracted from
// the URL path variables and used to filter the channel list before playlist generation.
//
// This handler enables IPTV clients to request focused playlists containing only channels
// from categories of interest (e.g., "Sports", "News", "Entertainment"), reducing bandwidth
// and improving user experience by eliminating irrelevant content.
//
// The group matching is performed case-insensitively against channel attributes such as
// "tvg-group" and "group-title" from the original M3U8 sources.
//
// Parameters:
//   - sp: pointer to the StreamProxy instance containing channel data and configuration
//
// Returns:
//   - http.HandlerFunc: HTTP handler that processes group playlist requests and writes filtered M3U8 responses
func HandleGroupPlaylist(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		middleware.GzipMiddleware(func(w http.ResponseWriter, r *http.Request) {
			vars := mux.Vars(r)
			group := vars["group"]
			sp.GeneratePlaylist(w, r, group)
		})(w, r)
	}
}

// HandleStream returns an HTTP handler function that initiates streaming of a specific channel
// to the requesting client. The handler performs channel lookup, validation, and client
// attachment to enable real-time video/audio streaming through the restreaming infrastructure.
//
// The process involves several key steps:
//  1. Extract the safe (URL-encoded) channel name from the request path
//  2. Resolve the safe name back to the original channel name
//  3. Locate the channel in the proxy's channel store
//  4. Validate channel existence and availability
//  5. Attach the client to the channel's restreamer for data distribution
//
// The handler supports automatic failover between multiple stream sources per channel,
// buffer management for efficient data distribution, and proper cleanup when clients disconnect.
// It operates in restreaming mode, where a single upstream connection serves multiple clients
// to minimize load on source servers while maintaining scalability.
//
// Parameters:
//   - sp: pointer to the StreamProxy instance containing channels and streaming infrastructure
//
// Returns:
//   - http.HandlerFunc: HTTP handler that processes stream requests and manages client connections
func HandleStream(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		safeName := vars["channel"]
		channelName := sp.FindChannelBySafeName(safeName)
		channel, exists := sp.Channels.Load(channelName)
		if !exists {
			if sp.Config.Debug {
				sp.Logger.Printf("Channel not found: %s", channelName)
			}
			http.Error(w, "Channel not found", http.StatusNotFound)
			return
		}

		if sp.Config.Debug {
			sp.Logger.Printf("Using RESTREAMING mode for channel: %s", channelName)
		}

		sp.HandleRestreamingClient(w, r, channel)
	}
}

// HandleEPG serves combined EPG data from all XC sources, M3U8 sources with EPG URLs, and manually configured EPGs
func HandleEPG(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {

		// Find all sources with EPG (XC, M3U8 with EPG URL, or manual EPGs)
		var epgSources []struct {
			url        string
			name       string
			sourceType string // "xc", "m3u8", or "manual"
		}

		// 1. Collect XC sources
		for i := range sp.Config.Sources {
			src := &sp.Config.Sources[i]
			if src.Username != "" && src.Password != "" {
				epgSources = append(epgSources, struct {
					url        string
					name       string
					sourceType string
				}{
					url:        fmt.Sprintf("%s/xmltv.php?username=%s&password=%s", src.URL, src.Username, src.Password),
					name:       src.Name,
					sourceType: "xc",
				})
			}
		}

		// 2. Collect M3U8 sources with EPG URLs
		for i := range sp.Config.Sources {
			src := &sp.Config.Sources[i]
			if src.EPGURL != "" {
				epgSources = append(epgSources, struct {
					url        string
					name       string
					sourceType string
				}{
					url:        src.EPGURL,
					name:       src.Name,
					sourceType: "m3u8",
				})
			}
		}

		// 3. Collect manually configured EPGs
		for i := range sp.Config.EPGs {
			epg := &sp.Config.EPGs[i]
			epgSources = append(epgSources, struct {
				url        string
				name       string
				sourceType string
			}{
				url:        epg.URL,
				name:       epg.Name,
				sourceType: "manual",
			})
		}

		if len(epgSources) == 0 {
			http.Error(w, "No EPG sources available", http.StatusNotFound)
			return
		}

		if sp.Config.Debug {
			sp.Logger.Printf("[EPG] Found %d total EPG sources (%d XC, %d M3U8, %d manual)",
				len(epgSources),
				countSourceType(epgSources, "xc"),
				countSourceType(epgSources, "m3u8"),
				countSourceType(epgSources, "manual"))
		}

		// Fetch EPG from all sources concurrently
		type epgResult struct {
			data []byte
			err  error
			name string
		}

		results := make(chan epgResult, len(epgSources))

		for _, epgSrc := range epgSources {
			go func(source struct {
				url        string
				name       string
				sourceType string
			}) {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()

				req, err := http.NewRequestWithContext(ctx, "GET", source.url, nil)
				if err != nil {
					results <- epgResult{err: err, name: source.name}
					return
				}

				resp, err := sp.HttpClient.Do(req)
				if err != nil {
					results <- epgResult{err: err, name: source.name}
					return
				}
				defer resp.Body.Close()

				if resp.StatusCode != http.StatusOK {
					results <- epgResult{err: fmt.Errorf("HTTP %d", resp.StatusCode), name: source.name}
					return
				}

				data, err := io.ReadAll(resp.Body)
				results <- epgResult{data: data, err: err, name: source.name}
			}(epgSrc)
		}

		// Collect results
		var epgDataList [][]byte
		for i := 0; i < len(epgSources); i++ {
			result := <-results
			if result.err == nil && len(result.data) > 0 {
				epgDataList = append(epgDataList, result.data)
				if sp.Config.Debug {
					sp.Logger.Printf("[EPG] Successfully fetched EPG from: %s (%d bytes)", result.name, len(result.data))
				}
			} else if sp.Config.Debug && result.err != nil {
				sp.Logger.Printf("[EPG] Failed to fetch from %s: %v", result.name, result.err)
			}
		}

		if len(epgDataList) == 0 {
			http.Error(w, "Failed to fetch EPG from any source", http.StatusBadGateway)
			return
		}

		// Merge XMLTV data
		merged := mergeXMLTV(epgDataList)

		if sp.Config.Debug {
			sp.Logger.Printf("[EPG] Merged %d EPG sources into final response (%d bytes)", len(epgDataList), len(merged))
		}

		w.Header().Set("Content-Type", "application/xml")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Write(merged)
	}
}

// countSourceType is a helper function to count sources by type for debug logging
func countSourceType(sources []struct {
	url        string
	name       string
	sourceType string
}, sourceType string) int {
	count := 0
	for _, src := range sources {
		if src.sourceType == sourceType {
			count++
		}
	}
	return count
}

// mergeXMLTV combines multiple XMLTV documents into one
func mergeXMLTV(xmltvDocs [][]byte) []byte {
	if len(xmltvDocs) == 1 {
		return xmltvDocs[0]
	}

	// Simple merge: extract all <channel> and <programme> elements
	var channels [][]byte
	var programmes [][]byte

	for _, doc := range xmltvDocs {
		docStr := string(doc)

		// Extract channel elements
		channelStart := 0
		for {
			start := strings.Index(docStr[channelStart:], "<channel ")
			if start == -1 {
				break
			}
			start += channelStart
			end := strings.Index(docStr[start:], "</channel>")
			if end == -1 {
				break
			}
			end += start + len("</channel>")
			channels = append(channels, []byte(docStr[start:end]))
			channelStart = end
		}

		// Extract programme elements
		progStart := 0
		for {
			start := strings.Index(docStr[progStart:], "<programme ")
			if start == -1 {
				break
			}
			start += progStart
			end := strings.Index(docStr[start:], "</programme>")
			if end == -1 {
				break
			}
			end += start + len("</programme>")
			programmes = append(programmes, []byte(docStr[start:end]))
			progStart = end
		}
	}

	// Build merged document
	var result strings.Builder
	result.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	result.WriteString("\n")
	result.WriteString(`<tv generator-info-name="KPTV-Proxy">`)
	result.WriteString("\n")

	for _, ch := range channels {
		result.Write(ch)
		result.WriteString("\n")
	}

	for _, prog := range programmes {
		result.Write(prog)
		result.WriteString("\n")
	}

	result.WriteString("</tv>")

	return []byte(result.String())
}
