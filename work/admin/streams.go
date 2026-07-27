package admin

import (
	"encoding/json"
	"fmt"
	"kptv-proxy/work/db"
	"kptv-proxy/work/deadstreams"
	"kptv-proxy/work/parser"
	"kptv-proxy/work/proxy"
	"kptv-proxy/work/restream"
	"kptv-proxy/work/types"
	"net/http"
	"net/url"
	"sync/atomic"

	"github.com/gorilla/mux"
)

// handleSetChannelOrder persists a new stream ordering for a channel. The request
// carries stream hashes in the desired display order; positions are derived from
// the array itself, so the result does not depend on the server's current ordering
// at the time the request arrives. Applied immediately without a restart.
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
			StreamOrder []string `json:"streamOrder"`
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
		known := make(map[string]bool, len(channel.Streams))
		for _, s := range channel.Streams {
			known[s.URLHash] = true
		}
		channel.Mu.RUnlock()

		for _, hash := range request.StreamOrder {
			if !known[hash] {
				http.Error(w, "Unknown stream hash", http.StatusBadRequest)
				return
			}
		}

		if err := db.SetChannelOrder(channelName, request.StreamOrder); err != nil {
			addLogEntry("error", fmt.Sprintf("Failed to save stream order: %v", err))
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		addLogEntry("info", fmt.Sprintf("Stream order updated for channel %s", channelName))

		applyChannelOrder(sp, channel, channelName)

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{
			"status":  "success",
			"message": "Stream order updated and applied immediately",
		})
	}
}

// handleResetChannelOrder clears any custom ordering for a channel, returning its
// streams to the globally configured sort.
func handleResetChannelOrder(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		vars := mux.Vars(r)
		channelName, err := url.PathUnescape(vars["channel"])
		if err != nil {
			http.Error(w, "Invalid channel name", http.StatusBadRequest)
			return
		}

		channel, exists := sp.Channels.Load(channelName)
		if !exists {
			http.Error(w, "Channel not found", http.StatusNotFound)
			return
		}

		if err := db.ClearChannelOrder(channelName); err != nil {
			addLogEntry("error", fmt.Sprintf("Failed to reset stream order: %v", err))
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		addLogEntry("info", fmt.Sprintf("Stream order reset for channel %s", channelName))

		applyChannelOrder(sp, channel, channelName)

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{
			"status":  "success",
			"message": "Stream order reset to default",
		})
	}
}

// applyChannelOrder re-sorts a channel's streams in memory from the persisted
// order and forces any active restreamer onto the new first stream.
func applyChannelOrder(sp *proxy.StreamProxy, channel *types.Channel, channelName string) {
	chOrder, err := db.GetChannelOrder(channelName)
	if err != nil {
		chOrder = map[string]int{}
	}

	channel.Mu.Lock()
	channel.Streams = parser.SortStreams(channel.Streams, sp.Config, channelName,
		map[string]map[string]int{channelName: chOrder})
	atomic.StoreInt32(&channel.PreferredStreamIndex, 0)

	if channel.Restreamer != nil && channel.Restreamer.Running.Load() {
		rs := &restream.Restream{Restreamer: channel.Restreamer}
		rs.ForceStreamSwitch(0)
		addLogEntry("info", fmt.Sprintf("Forced stream switch to index 0 after reorder for channel %s", channelName))
	}
	channel.Mu.Unlock()
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
		json.NewEncoder(w).Encode(map[string]any{
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
		json.NewEncoder(w).Encode(map[string]any{
			"status":  "success",
			"message": fmt.Sprintf("Stream %d revived", request.StreamIndex),
		})
	}
}
