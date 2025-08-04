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

		sp.Logger.Printf("Handling stream request for channel: %s (from URL: %s)", channelName, safeName)

		value, exists := sp.Channels.Load(channelName)
		if !exists {
			sp.Logger.Printf("Channel not found: %s", channelName)
			http.Error(w, "Channel not found", http.StatusNotFound)
			return
		}

		channel := value.(*types.Channel)
		sp.Logger.Printf("Found channel %s with %d streams", channelName, len(channel.Streams))

		if sp.Config.EnableRestreaming {
			// Use restreaming
			sp.HandleRestreamingClient(w, r, channel)
		} else {
			// Direct proxy mode
			success := sp.TryStreams(channel, 0, w, r)
			if !success {
				sp.Logger.Printf("All streams failed for channel: %s", channelName)
				http.Error(w, "All streams failed", http.StatusServiceUnavailable)
			}
		}
	}
}
