package handlers

import (
	"kptv-proxy/work/proxy"
	"kptv-proxy/work/types"
	"net/http"

	"github.com/gorilla/mux"
)

// HandlePlaylist returns an HTTP handler that generates the full playlist.
// It delegates to StreamProxy.GeneratePlaylist with an empty group filter.
func HandlePlaylist(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sp.GeneratePlaylist(w, r, "")
	}
}

// HandleGroupPlaylist returns an HTTP handler that generates a playlist
// filtered by a specific group name provided in the URL variables.
func HandleGroupPlaylist(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		group := vars["group"]
		sp.GeneratePlaylist(w, r, group)
	}
}

// HandleStream returns an HTTP handler that streams a given channel to the client.
// It looks up the channel by its safe (URL-friendly) name, verifies existence,
// and then initiates the restreaming process.
func HandleStream(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		safeName := vars["channel"]

		// Find original channel name from the safe name
		channelName := sp.FindChannelBySafeName(safeName)

		// Look up channel in the proxy's channel store
		value, exists := sp.Channels.Load(channelName)
		if !exists {
			if sp.Config.Debug {
				sp.Logger.Printf("Channel not found: %s", channelName)
			}
			http.Error(w, "Channel not found", http.StatusNotFound)
			return
		}

		// Cast to Channel type
		channel := value.(*types.Channel)

		if sp.Config.Debug {
			sp.Logger.Printf("Using RESTREAMING mode for channel: %s", channelName)
		}

		// Start handling the client restream
		sp.HandleRestreamingClient(w, r, channel)
	}
}
