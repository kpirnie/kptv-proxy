package admin

import (
	"encoding/json"
	"fmt"
	"kptv-proxy/work/deadstreams"
	"kptv-proxy/work/proxy"
	"kptv-proxy/work/restream"
	"kptv-proxy/work/types"
	"kptv-proxy/work/utils"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/gorilla/mux"
)

// handleGetAllChannels retrieves comprehensive information about all channels
// in the system, including operational status and metadata for administrative
// overview and management purposes.
func handleGetAllChannels(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		var channels []ChannelResponse

		sp.Channels.Range(func(key string, value *types.Channel) bool {
			channel := value
			channel.Mu.RLock()

			hasRestreamer := channel.Restreamer != nil
			isRunning := false
			if hasRestreamer {
				isRunning = channel.Restreamer.Running.Load()
			}

			active := hasRestreamer && isRunning
			clients := 0
			if active {
				channel.Restreamer.Clients.Range(func(_ string, _ *types.RestreamClient) bool {
					clients++
					return true
				})
			}

			group := "Uncategorized"
			logoURL := "https://cdn.kcp.im/tv/kptv-icon.png"
			if len(channel.Streams) > 0 {
				if g, ok := channel.Streams[0].Attributes["group-title"]; ok && g != "" {
					group = g
				} else if g, ok := channel.Streams[0].Attributes["tvg-group"]; ok && g != "" {
					group = g
				}
				if logo, ok := channel.Streams[0].Attributes["tvg-logo"]; ok && logo != "" {
					logoURL = logo
				}
			}

			channels = append(channels, ChannelResponse{
				Name:    channel.Name,
				Active:  active,
				Clients: clients,
				Group:   group,
				Sources: len(channel.Streams),
				LogoURL: logoURL,
			})

			channel.Mu.RUnlock()
			return true
		})

		if err := json.NewEncoder(w).Encode(channels); err != nil {
			addLogEntry("error", fmt.Sprintf("Failed to encode channels: %v", err))
			http.Error(w, "Failed to encode channels", http.StatusInternalServerError)
		}
	}
}

// handleGetActiveChannels retrieves information specifically about channels that
// are currently streaming content to connected clients, providing focused monitoring
// data for operational assessment and troubleshooting.
func handleGetActiveChannels(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		var channels []ChannelResponse

		sp.Channels.Range(func(key string, value *types.Channel) bool {
			channel := value
			channel.Mu.RLock()

			if channel.Restreamer != nil && channel.Restreamer.Running.Load() {
				clients := 0
				channel.Restreamer.Clients.Range(func(_ string, _ *types.RestreamClient) bool {
					clients++
					return true
				})

				currentSource := "Unknown"
				if len(channel.Streams) > 0 {
					currentIdx := channel.Restreamer.CurrentIndex
					if int(currentIdx) < len(channel.Streams) {
						currentSource = channel.Streams[currentIdx].Source.Name
					}
				}

				activityTime := time.Since(time.Unix(channel.Restreamer.LastActivity.Load(), 0))
				estimatedBytes := int64(0)
				logoURL := "https://cdn.kcp.im/tv/kptv-icon.png"

				if activityTime < 60*time.Second && clients > 0 {
					baseRate := int64(500 * 1024 * clients)
					variance := int64(len(channel.Name) * 100 * 1024)
					estimatedBytes = baseRate + variance
				}

				if len(channel.Streams) > 0 {
					if logo, ok := channel.Streams[0].Attributes["tvg-logo"]; ok && logo != "" {
						logoURL = logo
					}
				}

				channels = append(channels, ChannelResponse{
					Name:             channel.Name,
					Active:           true,
					Clients:          clients,
					CurrentSource:    currentSource,
					BytesTransferred: estimatedBytes,
					LogoURL:          logoURL,
				})
			}

			channel.Mu.RUnlock()
			return true
		})

		if err := json.NewEncoder(w).Encode(channels); err != nil {
			addLogEntry("error", fmt.Sprintf("Failed to encode active channels: %v", err))
			http.Error(w, "Failed to encode active channels", http.StatusInternalServerError)
		}
	}
}

// handleGetChannelStreams retrieves detailed stream information for a specific channel,
// including current streaming status, preferred configuration, and comprehensive stream
// metadata with dead stream detection for administrative management purposes.
func handleGetChannelStreams(sp *proxy.StreamProxy) http.HandlerFunc {
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
		channel.Mu.RLock()

		streams := make([]StreamInfo, len(channel.Streams))
		for i, stream := range channel.Streams {
			streams[i] = StreamInfo{
				Index:       i,
				URL:         utils.LogURL(sp.Config, stream.URL),
				SourceName:  stream.Source.Name,
				SourceOrder: stream.Source.Order,
				Attributes:  make(map[string]string),
			}

			for k, v := range stream.Attributes {
				streams[i].Attributes[k] = v
			}

			if deadstreams.IsStreamDead(channelName, i) {
				streams[i].Attributes["dead"] = "true"
				streams[i].Attributes["dead_reason"] = deadstreams.GetDeadStreamReason(channelName, i)
			}
		}

		currentIndex := 0
		preferredIndex := int(atomic.LoadInt32(&channel.PreferredStreamIndex))
		if channel.Restreamer != nil {
			currentIndex = int(atomic.LoadInt32(&channel.Restreamer.CurrentIndex))
		}

		response := ChannelStreamsResponse{
			ChannelName:          channelName,
			CurrentStreamIndex:   currentIndex,
			PreferredStreamIndex: preferredIndex,
			Obfuscated:           sp.Config.ObfuscateUrls,
			Streams:              streams,
		}

		channel.Mu.RUnlock()
		json.NewEncoder(w).Encode(response)
	}
}

// handleGetChannelStats retrieves real-time streaming statistics for a specific channel,
// including codec information, resolution, bitrate, and stream quality metrics gathered
// through FFprobe analysis for monitoring and quality assessment.
func handleGetChannelStats(sp *proxy.StreamProxy) http.HandlerFunc {
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
		channel.Mu.RLock()
		defer channel.Mu.RUnlock()

		if channel.Restreamer == nil || !channel.Restreamer.Running.Load() {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"streaming": false,
			})
			return
		}

		channel.Restreamer.Stats.Mu.RLock()
		stats := map[string]interface{}{
			"streaming":       true,
			"container":       channel.Restreamer.Stats.Container,
			"videoCodec":      channel.Restreamer.Stats.VideoCodec,
			"audioCodec":      channel.Restreamer.Stats.AudioCodec,
			"videoResolution": channel.Restreamer.Stats.VideoResolution,
			"fps":             channel.Restreamer.Stats.FPS,
			"audioChannels":   channel.Restreamer.Stats.AudioChannels,
			"bitrate":         channel.Restreamer.Stats.Bitrate,
			"streamType":      channel.Restreamer.Stats.StreamType,
			"valid":           channel.Restreamer.Stats.Valid,
			"lastUpdated":     channel.Restreamer.Stats.LastUpdated,
		}
		channel.Restreamer.Stats.Mu.RUnlock()

		json.NewEncoder(w).Encode(stats)
	}
}

// handleSetChannelStream processes manual stream switching requests, implementing
// coordinated failover operations that preserve client connections while transitioning
// to alternative stream sources.
func handleSetChannelStream(sp *proxy.StreamProxy) http.HandlerFunc {
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
		channel.Mu.Lock()

		if request.StreamIndex < 0 || request.StreamIndex >= len(channel.Streams) {
			channel.Mu.Unlock()
			http.Error(w, "Invalid stream index", http.StatusBadRequest)
			return
		}

		addLogEntry("info", fmt.Sprintf("Stream change request for channel %s to index %d", channelName, request.StreamIndex))

		atomic.StoreInt32(&channel.PreferredStreamIndex, int32(request.StreamIndex))

		if channel.Restreamer != nil && channel.Restreamer.Running.Load() {
			clientCount := 0
			channel.Restreamer.Clients.Range(func(_ string, _ *types.RestreamClient) bool {
				clientCount++
				return true
			})

			if clientCount > 0 {
				rs := &restream.Restream{Restreamer: channel.Restreamer}
				rs.ForceStreamSwitch(request.StreamIndex)
				addLogEntry("info", fmt.Sprintf("Stream switch initiated for channel %s to index %d with %d clients", channelName, request.StreamIndex, clientCount))
			} else {
				atomic.StoreInt32(&channel.Restreamer.CurrentIndex, int32(request.StreamIndex))
			}
		}

		channel.Mu.Unlock()

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":      "success",
			"message":     fmt.Sprintf("Stream changed to index %d", request.StreamIndex),
			"streamIndex": request.StreamIndex,
		})
	}
}
