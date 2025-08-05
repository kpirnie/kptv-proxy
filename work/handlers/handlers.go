package handlers

import (
	"kptv-proxy/work/proxy"
	"kptv-proxy/work/types"
	"net/http"

	"github.com/gorilla/mux"
)

func HandlePlaylist(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sp.GeneratePlaylist(w, r, "")
	}
}

func HandleGroupPlaylist(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		group := vars["group"]
		sp.GeneratePlaylist(w, r, group)
	}
}

func HandleStream(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		safeName := vars["channel"]

		// Find original channel name
		channelName := sp.FindChannelBySafeName(safeName)

		// DEBUG: Log the restreaming mode setting
		sp.Logger.Printf("DEBUG: EnableRestreaming = %v for channel: %s", sp.Config.EnableRestreaming, channelName)

		value, exists := sp.Channels.Load(channelName)
		if !exists {
			sp.Logger.Printf("Channel not found: %s", channelName)
			http.Error(w, "Channel not found", http.StatusNotFound)
			return
		}

		channel := value.(*types.Channel)

		// FORCE restreaming mode for debugging - change this back to sp.Config.EnableRestreaming after testing
		if sp.Config.EnableRestreaming {
			sp.Logger.Printf("Using RESTREAMING mode for channel: %s", channelName)
			sp.HandleRestreamingClient(w, r, channel)
		} else {
			sp.Logger.Printf("Using DIRECT PROXY mode for channel: %s", channelName)
			// Direct proxy mode
			success := sp.TryStreams(channel, 0, w, r)
			if !success {
				sp.Logger.Printf("All streams failed for channel: %s", channelName)
				http.Error(w, "All streams failed", http.StatusServiceUnavailable)
			}
		}
	}
}
