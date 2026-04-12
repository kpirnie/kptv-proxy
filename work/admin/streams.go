package admin

import (
	"encoding/json"
	"fmt"
	"kptv-proxy/work/db"
	"kptv-proxy/work/deadstreams"
	"kptv-proxy/work/parser"
	"kptv-proxy/work/proxy"
	"kptv-proxy/work/restream"
	"kptv-proxy/work/streamorder"
	"net/http"
	"net/url"
	"sync/atomic"

	"github.com/gorilla/mux"
)

// handleSetChannelOrder processes requests to update the custom ordering of streams
// within a channel, validating the new order against channel configuration and
// persisting changes to the stream order database. Applies changes immediately
// without requiring application restart.
func handleSetChannelOrder(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		vars := mux.Vars(r)
		channelName, err := url.PathUnescape(vars["channel"])
		if err != nil {
			http.Error(w, "Invalid channel name", http.StatusBadRequest)
			return
		}

		var request struct {
			StreamOrder []int `json:"streamOrder"`
		}

		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		channel, exists := sp.Channels.Load(channelName)
		if !exists {
			http.Error(w, "Channel not found", http.StatusNotFound)
			return
		}
		channel.Mu.RLock()
		streamCount := len(channel.Streams)
		channel.Mu.RUnlock()

		if len(request.StreamOrder) != streamCount {
			http.Error(w, "Stream order length mismatch", http.StatusBadRequest)
			return
		}

		// Detect if order is unchanged from default [0,1,2,3...]
		isDefault := true
		for i, idx := range request.StreamOrder {
			if idx != i {
				isDefault = false
				break
			}
		}

		if isDefault {
			streamorder.DeleteChannelStreamOrder(channelName)
		} else {
			// Resolve positional indices to hash-based entries for stable ordering
			channel.Mu.RLock()
			entries := make([]streamorder.StreamOrderEntry, len(request.StreamOrder))
			for newPos, origIdx := range request.StreamOrder {
				hash := ""
				if origIdx >= 0 && origIdx < len(channel.Streams) {
					hash = channel.Streams[origIdx].URLHash
				}
				entries[newPos] = streamorder.StreamOrderEntry{
					Index: newPos,
					Hash:  hash,
				}
			}
			channel.Mu.RUnlock()

			if err := streamorder.SetChannelStreamOrder(channelName, entries); err != nil {
				addLogEntry("error", fmt.Sprintf("Failed to save stream order: %v", err))
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			addLogEntry("info", fmt.Sprintf("Stream order updated for channel %s", channelName))
		}

		// Apply the new order immediately without restart
		channel.Mu.Lock()
		parser.SortStreams(channel.Streams, sp.Config, channelName, map[string]map[string]db.StreamOverride{})
		atomic.StoreInt32(&channel.PreferredStreamIndex, 0)

		// If a restreamer is active, force it to switch to the new first stream
		if channel.Restreamer != nil && channel.Restreamer.Running.Load() {
			rs := &restream.Restream{Restreamer: channel.Restreamer}
			rs.ForceStreamSwitch(0)
			addLogEntry("info", fmt.Sprintf("Forced stream switch to index 0 after reorder for channel %s", channelName))
		}
		channel.Mu.Unlock()

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "success",
			"message": "Stream order updated and applied immediately",
		})
	}
}

// handleKillStream manually marks a stream as dead in the dead streams database,
// preventing it from being selected during automatic failover operations.
func handleKillStream(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		vars := mux.Vars(r)
		channelName, err := url.PathUnescape(vars["channel"])
		if err != nil {
			http.Error(w, "Invalid channel name", http.StatusBadRequest)
			return
		}

		var request struct {
			StreamIndex int `json:"streamIndex"`
		}

		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		channel, exists := sp.Channels.Load(channelName)
		if !exists {
			http.Error(w, "Channel not found", http.StatusNotFound)
			return
		}
		channel.Mu.RLock()

		if request.StreamIndex >= len(channel.Streams) {
			channel.Mu.RUnlock()
			http.Error(w, "Invalid stream index", http.StatusBadRequest)
			return
		}

		stream := channel.Streams[request.StreamIndex]
		hash := stream.URLHash
		channel.Mu.RUnlock()

		if err := deadstreams.MarkStreamDeadByHash(channelName, hash, "manual"); err != nil {
			addLogEntry("error", fmt.Sprintf("Failed to kill stream: %v", err))
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		addLogEntry("info", fmt.Sprintf("Stream %d manually marked as dead for channel %s", request.StreamIndex, channelName))

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "success",
			"message": fmt.Sprintf("Stream %d marked as dead", request.StreamIndex),
		})
	}
}

// handleReviveStream removes a stream from the dead streams database,
// restoring it to active rotation for failover and manual selection.
func handleReviveStream(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		vars := mux.Vars(r)
		channelName, err := url.PathUnescape(vars["channel"])
		if err != nil {
			http.Error(w, "Invalid channel name", http.StatusBadRequest)
			return
		}

		var request struct {
			StreamIndex int `json:"streamIndex"`
		}

		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		channel, exists := sp.Channels.Load(channelName)
		if !exists {
			http.Error(w, "Channel not found", http.StatusNotFound)
			return
		}
		channel.Mu.RLock()

		if request.StreamIndex >= len(channel.Streams) {
			channel.Mu.RUnlock()
			http.Error(w, "Invalid stream index", http.StatusBadRequest)
			return
		}

		hash := channel.Streams[request.StreamIndex].URLHash
		channel.Mu.RUnlock()

		if err := deadstreams.ReviveStream(channelName, hash); err != nil {
			addLogEntry("error", fmt.Sprintf("Failed to revive stream: %v", err))
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		addLogEntry("info", fmt.Sprintf("Stream %d revived for channel %s", request.StreamIndex, channelName))

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "success",
			"message": fmt.Sprintf("Stream %d revived", request.StreamIndex),
		})
	}
}
