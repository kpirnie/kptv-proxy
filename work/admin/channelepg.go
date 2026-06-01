package admin

import (
	"encoding/json"
	"fmt"
	"kptv-proxy/work/db"
	"kptv-proxy/work/epgindex"
	"kptv-proxy/work/proxy"
	"net/http"
	"net/url"

	"github.com/gorilla/mux"
)

// handleGetChannelEPG returns the saved EPG channel mapping for a channel.
func handleGetChannelEPG(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		vars := mux.Vars(r)
		channelName, err := url.PathUnescape(vars["channel"])
		if err != nil {
			http.Error(w, "Invalid channel name", http.StatusBadRequest)
			return
		}

		mapping, ok := db.GetChannelEPG(channelName)
		if !ok {
			json.NewEncoder(w).Encode(map[string]string{"epg_id": "", "epg_name": ""})
			return
		}

		json.NewEncoder(w).Encode(map[string]string{
			"epg_id":   mapping.EPGID,
			"epg_name": mapping.EPGName,
		})
	}
}

// handleSetChannelEPG saves or updates the EPG channel mapping for a channel.
func handleSetChannelEPG(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		vars := mux.Vars(r)
		channelName, err := url.PathUnescape(vars["channel"])
		if err != nil {
			http.Error(w, "Invalid channel name", http.StatusBadRequest)
			return
		}

		var req struct {
			EPGID   string `json:"epg_id"`
			EPGName string `json:"epg_name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		if err := db.UpsertChannelEPG(channelName, req.EPGID, req.EPGName); err != nil {
			http.Error(w, "Failed to save EPG mapping", http.StatusInternalServerError)
			return
		}

		addLogEntry("info", fmt.Sprintf("EPG mapping set for channel %s: %s (%s)", channelName, req.EPGName, req.EPGID))
		json.NewEncoder(w).Encode(map[string]string{"status": "success"})
	}
}

// handleDeleteChannelEPG removes the EPG channel mapping for a channel.
func handleDeleteChannelEPG(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		vars := mux.Vars(r)
		channelName, err := url.PathUnescape(vars["channel"])
		if err != nil {
			http.Error(w, "Invalid channel name", http.StatusBadRequest)
			return
		}

		if err := db.DeleteChannelEPG(channelName); err != nil {
			http.Error(w, "Failed to delete EPG mapping", http.StatusInternalServerError)
			return
		}

		addLogEntry("info", fmt.Sprintf("EPG mapping cleared for channel %s", channelName))
		json.NewEncoder(w).Encode(map[string]string{"status": "success"})
	}
}

// handleSearchEPGChannels performs a fuzzy search against the in-memory EPG
// channel index, returning up to 50 matching entries.
func handleSearchEPGChannels(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		q := r.URL.Query().Get("q")
		if q == "" {
			json.NewEncoder(w).Encode([]epgindex.EPGChannel{})
			return
		}

		results := epgindex.Search(q, 50)
		json.NewEncoder(w).Encode(results)
	}
}

// handleGetAllChannelEPGs returns all saved EPG channel mappings as a
// map of channel name -> epg_id for bulk status rendering.
func handleGetAllChannelEPGs(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		rows, err := db.Get().Query(`SELECT channel, epg_id FROM kp_channel_epg WHERE epg_id != ''`)
		if err != nil {
			http.Error(w, "Failed to query EPG mappings", http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		result := make(map[string]string)
		for rows.Next() {
			var channel, epgID string
			if err := rows.Scan(&channel, &epgID); err != nil {
				continue
			}
			result[channel] = epgID
		}

		json.NewEncoder(w).Encode(result)
	}
}
