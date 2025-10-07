package handlers

import (
	"kptv-proxy/work/proxy"
	"net/http"

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

		// Generate playlist with no group filtering (empty string = include all channels)
		sp.GeneratePlaylist(w, r, "")
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

		// Extract group name from URL path variables
		vars := mux.Vars(r)
		group := vars["group"]

		// Generate playlist filtered by the specified group
		sp.GeneratePlaylist(w, r, group)
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

		// Extract URL-safe channel name from path variables
		vars := mux.Vars(r)
		safeName := vars["channel"]

		// Resolve safe name back to original channel name using internal mapping
		channelName := sp.FindChannelBySafeName(safeName)

		// Attempt to load channel from the concurrent channel store
		channel, exists := sp.Channels.Load(channelName)
		if !exists {
			// Channel not found - log for debugging and return 404
			if sp.Config.Debug {
				sp.Logger.Printf("Channel not found: %s", channelName)
			}
			http.Error(w, "Channel not found", http.StatusNotFound)
			return
		}

		// Log streaming mode selection for debugging
		if sp.Config.Debug {
			sp.Logger.Printf("Using RESTREAMING mode for channel: %s", channelName)
		}

		// Initiate restreaming client handling - this blocks until client disconnects
		sp.HandleRestreamingClient(w, r, channel)
	}
}
