package proxy

import (
	"fmt"
	"kptv-proxy/work/client"
	"kptv-proxy/work/constants"
	"kptv-proxy/work/deadstreams"
	"kptv-proxy/work/logger"
	"kptv-proxy/work/restream"
	"kptv-proxy/work/types"
	"strconv"

	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/puzpuzpuz/xsync/v3"
	"go.uber.org/ratelimit"
)

// HandleRestreamingClient manages the complete lifecycle of a streaming client connection.
// It initializes or reuses an existing restreamer for the requested channel, registers
// the client, sets appropriate streaming headers, and blocks until the client disconnects
// or a 24-hour maximum session timeout is reached.
//
// When the watcher system is enabled, stream quality monitoring is automatically started
// for the active restreaming session to enable automatic failover on quality degradation.
func (sp *StreamProxy) HandleRestreamingClient(w http.ResponseWriter, r *http.Request, channel *types.Channel) {

	// a HEAD probe wants headers only; answering it before the semaphore,
	// restreamer, and client registration keeps a probing player from
	// consuming a connection slot and an upstream stream it will never read
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Type", streamResponseContentType(channel))
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		logger.Debug("{proxy/stream - HandleRestreamingClient} Channel %s: HEAD probe from %s", channel.Name, r.RemoteAddr)
		return
	}

	// Acquire global connection slot
	select {
	case globalClientSemaphore <- struct{}{}:
		defer func() { <-globalClientSemaphore }()
	default:
		logger.Debug("{proxy/stream - HandleRestreamingClient} Max connections reached (%d), rejecting client", sp.Config.MaxConnectionsToApp)
		http.Error(w, "Server at capacity", http.StatusServiceUnavailable)
		return
	}

	logger.Debug("{proxy/stream - HandleRestreamingClient} Channel %s: New client request from %s", channel.Name, r.RemoteAddr)
	logger.Debug("{proxy/stream - HandleRestreamingClient} Channel %s: %d available streams", channel.Name, len(channel.Streams))

	if sp.Config.FFmpegMode {
		logger.Debug("{proxy/stream - HandleRestreamingClient} Channel %s: Using FFMPEG mode", channel.Name)
	}

	channel.Mu.Lock()
	var restreamer *restream.Restream
	if channel.Restreamer == nil {
		var rateLimiter ratelimit.Limiter
		if len(channel.Streams) > 0 {
			rateLimiter = sp.getRateLimiterForSource(channel.Streams[0].Source)
		}

		logger.Debug("{proxy/stream - HandleRestreamingClient} Channel %s: Creating new restreamer with rate limiting", channel.Name)

		restreamer = restream.NewRestreamer(channel, (sp.Config.BufferSizePerStream * 1024 * 1024), sp.HttpClient, sp.Config, rateLimiter)
		channel.Restreamer = restreamer.Restreamer
	} else {
		// reuse the existing restreamer, ensuring the client map is initialized
		if channel.Restreamer.Clients == nil {
			channel.Restreamer.Clients = xsync.NewMapOf[string, *types.RestreamClient]()
			logger.Debug("{proxy/stream - HandleRestreamingClient} Channel %s: Re-initialized client map on existing restreamer", channel.Name)
		}
		// If not running, reset CurrentIndex to PreferredStreamIndex so the
		// next Stream() call starts from the correct custom-ordered position.
		if !channel.Restreamer.Running.Load() && !channel.Restreamer.LastStreamFailed.Load() {
			preferred := atomic.LoadInt32(&channel.PreferredStreamIndex)
			atomic.StoreInt32(&channel.Restreamer.CurrentIndex, preferred)
		}
		restreamer = &restream.Restream{Restreamer: channel.Restreamer}
		logger.Debug("{proxy/stream - HandleRestreamingClient} Channel %s: Reusing existing restreamer", channel.Name)
	}

	channel.Mu.Unlock()

	if channel.Restreamer != nil && channel.Restreamer.Running.Load() {
		// running: switch away from a dead current stream to the first live one
		curIdx := int(atomic.LoadInt32(&channel.Restreamer.CurrentIndex))
		channel.Mu.RLock()
		n := len(channel.Streams)
		deadCurrent := curIdx < n && deadstreams.IsStreamDead(channel.Name, channel.Streams[curIdx].URLHash)
		switchIdx := -1
		if deadCurrent {
			for i := 1; i < n; i++ {
				next := (curIdx + i) % n
				if !deadstreams.IsStreamDead(channel.Name, channel.Streams[next].URLHash) && atomic.LoadInt32(&channel.Streams[next].Blocked) == 0 {
					switchIdx = next
					break
				}
			}
		}
		channel.Mu.RUnlock()
		if deadCurrent && switchIdx >= 0 {
			rs := &restream.Restream{Restreamer: channel.Restreamer}
			rs.ForceStreamSwitch(switchIdx)
		}
	} else {
		// not running: advance PreferredStreamIndex past any dead/blocked streams
		preferredIdx := int(atomic.LoadInt32(&channel.PreferredStreamIndex))
		channel.Mu.RLock()
		n := len(channel.Streams)
		for i := 0; i < n; i++ {
			checkIdx := (preferredIdx + i) % n
			if checkIdx < n {
				s := channel.Streams[checkIdx]
				if !deadstreams.IsStreamDead(channel.Name, s.URLHash) && atomic.LoadInt32(&s.Blocked) == 0 {
					if checkIdx != preferredIdx {
						atomic.StoreInt32(&channel.PreferredStreamIndex, int32(checkIdx))
					}
					break
				}
			}
		}
		channel.Mu.RUnlock()
	}

	// generate a unique client identifier
	clientID := fmt.Sprintf("%s-%d", r.RemoteAddr, time.Now().UnixNano())

	// set streaming response headers
	w.Header().Set("Content-Type", streamResponseContentType(channel))
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Accept", "*/*")

	// resolve the flusher interface, handling custom response writer wrappers
	var flusher http.Flusher
	var ok bool
	if crw, isCustom := w.(*client.CustomResponseWriter); isCustom {
		flusher, ok = crw.ResponseWriter.(http.Flusher)
	} else {
		flusher, ok = w.(http.Flusher)
	}
	if !ok {
		logger.Error("{proxy/stream - HandleRestreamingClient} Streaming not supported for client: %s (ResponseWriter does not implement http.Flusher)", clientID)
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	logger.Debug("{proxy/stream - HandleRestreamingClient} Channel %s: Registered client %s", channel.Name, clientID)

	restreamer.AddClient(clientID, w, flusher)

	// only start a watcher if one is not already running for this channel —
	// calling StartWatching per client connection leaks semaphore slots
	if sp.Config.WatcherEnabled && restreamer.Restreamer.Running.Load() && !sp.WatcherManager.IsWatching(channel.Name) {
		preferredIndex := int(atomic.LoadInt32(&channel.PreferredStreamIndex))
		currentIndex := int(atomic.LoadInt32(&restreamer.Restreamer.CurrentIndex))

		actualIndex := preferredIndex
		if preferredIndex < 0 || preferredIndex >= len(channel.Streams) {
			actualIndex = currentIndex
		}

		logger.Debug("{proxy/stream - HandleRestreamingClient} Channel %s: Preferred=%d, Current=%d, Using=%d",
			channel.Name, preferredIndex, currentIndex, actualIndex)

		sp.WatcherManager.StartWatching(channel.Name, restreamer.Restreamer)
	}

	// deferred cleanup to remove the client on disconnect
	cleanup := func() {
		restreamer.RemoveClient(clientID)
		logger.Debug("{proxy/stream - HandleRestreamingClient} Channel %s: Removed client %s", channel.Name, clientID)
	}
	defer cleanup()

	// block until the client disconnects or the session timeout is reached
	done := make(chan struct{})
	go func() {
		defer close(done)
		<-r.Context().Done()
	}()

	// resolve this client's Done channel so removal by the health monitor or
	// a failed write also releases this handler, the connection, and the
	// global semaphore slot instead of holding them until TCP gives up
	var clientDone chan bool
	if c, ok := restreamer.Restreamer.Clients.Load(clientID); ok {
		clientDone = c.Done
	}

	select {
	case <-done:
		logger.Debug("{proxy/stream - HandleRestreamingClient} Client disconnected: %s (channel: %s)", clientID, channel.Name)
	case <-clientDone:
		logger.Debug("{proxy/stream - HandleRestreamingClient} Client removed by server: %s (channel: %s)", clientID, channel.Name)
	case <-time.After(constants.Internal.MaxClientSessionDuration):
		logger.Warn("{proxy/stream - HandleRestreamingClient} Client session timeout after 24h: %s (channel: %s)", clientID, channel.Name)
	case <-restreamer.Restreamer.SwitchNotifyChan():
		// watcher switched stream sources; close this connection so the client
		// reconnects fresh and negotiates the new stream from a clean state
		logger.Debug("{proxy/stream - HandleRestreamingClient} Channel %s: Stream switch signalled reconnect for client %s", channel.Name, clientID)
	}
}

// ShouldCheckForMasterPlaylist analyzes HTTP response headers to determine whether
// the response body likely contains an HLS master playlist rather than a media segment.
// It checks for M3U8/mpegURL content types and small content lengths (under 100KB) as
// indicators that the response is a playlist requiring further resolution rather than
// streamable media data.
func (sp *StreamProxy) ShouldCheckForMasterPlaylist(resp *http.Response) bool {
	contentType := resp.Header.Get("Content-Type")
	contentLength := resp.Header.Get("Content-Length")

	if strings.Contains(strings.ToLower(contentType), "mpegurl") ||
		strings.Contains(strings.ToLower(contentType), "m3u8") {
		logger.Debug("{proxy/stream - ShouldCheckForMasterPlaylist} Detected playlist content type: %s", contentType)
		return true
	}

	if contentLength != "" {
		if length, err := strconv.ParseInt(contentLength, 10, 64); err == nil {
			if length > 0 && length < constants.Internal.MasterPlaylistSizeThreshold {
				logger.Debug("{proxy/stream - ShouldCheckForMasterPlaylist} Small content length detected (%d bytes), may be a playlist", length)
				return true
			}
		}
	}

	return false
}

// RestreamCleanup implements background maintenance for inactive restreaming connections.
// It runs every 10 seconds and performs two categories of cleanup:
//   - Stopped restreamers: cleaned up after 30 seconds of inactivity, force-cleaned after 60
//   - Running restreamers: removes individual clients inactive for 120+ seconds, and stops
//     the entire restreamer if no active clients remain for 120+ seconds
//
// After each cleanup cycle, a GC pass and buffer pool cleanup are triggered to reclaim
// memory from destroyed stream buffers and disconnected client resources.
func (sp *StreamProxy) RestreamCleanup() {
	logger.Debug("{proxy/stream - RestreamCleanup} Starting restream cleanup loop (interval: 10s)")

	ticker := time.NewTicker(constants.Internal.ProxyCleanupTickerInterval)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now().Unix()

		sp.Channels.Range(func(key string, channel *types.Channel) bool {
			sp.cleanupChannelRestreamer(channel, now)
			return true
		})

		rangeExternalChannels(func(channel *types.Channel) bool {
			sp.cleanupChannelRestreamer(channel, now)
			return true
		})

		if sp.BufferPool != nil {
			sp.BufferPool.Cleanup()
		}
	}
}

// cleanupChannelRestreamer performs one maintenance pass over a single channel's
// restreamer, tearing down inactive restreamers and evicting stale clients.
func (sp *StreamProxy) cleanupChannelRestreamer(channel *types.Channel, now int64) {
	channel.Mu.Lock()
	defer channel.Mu.Unlock()

	if channel.Restreamer == nil {
		return
	}

	if !channel.Restreamer.Running.Load() {
		lastActivity := channel.Restreamer.LastActivity.Load()

		if now-lastActivity > constants.Internal.ProxyInactiveRestreamerTimeout {
			select {
			case <-channel.Restreamer.Context().Done():
				// context already cancelled, force clean after 60 seconds
				if now-lastActivity > constants.Internal.ProxyForceCleanTimeout {
					logger.Debug("{proxy/stream - RestreamCleanup} Channel %s: Force cleaning cancelled context after 60s", channel.Name)

					if b := channel.Restreamer.LoadBuffer(); b != nil && !b.IsDestroyed() {
						b.Destroy()
					}
					channel.Restreamer.CancelStream()
					channel.Restreamer = nil
				}
			default:
				// only clean up if a switch is not in progress
				// during a switch Running briefly goes false but the restreamer is still needed
				if channel.Restreamer.SwitchPending() {
					logger.Debug("{proxy/stream - RestreamCleanup} Channel %s: Skipping cleanup, manual switch in progress", channel.Name)
					break
				}

				if b := channel.Restreamer.LoadBuffer(); b != nil && !b.IsDestroyed() {
					logger.Debug("{proxy/stream - RestreamCleanup} Channel %s: Safely destroying buffer", channel.Name)
					b.Destroy()
				}
				channel.Restreamer.CancelStream()
				channel.Restreamer = nil
				logger.Debug("{proxy/stream - RestreamCleanup} Cleaned up inactive restreamer for channel: %s (idle %ds)", channel.Name, now-lastActivity)
			}
		}

		return
	}

	// check individual client activity on running restreamers
	clientCount := 0
	channel.Restreamer.Clients.Range(func(ckey string, cvalue *types.RestreamClient) bool {
		client := cvalue
		lastSeen := client.LastSeen.Load()

		if now-lastSeen > constants.Internal.ProxyClientInactivityTimeout {
			logger.Debug("{proxy/stream - RestreamCleanup} Removing inactive client: %s (last seen %ds ago)", ckey, now-lastSeen)
			// LoadAndDelete makes map removal the single ownership gate so
			// exactly one path closes Done (avoids a double-close race with
			// RemoveClient). WriteChan is never closed — drainClient exits on
			// Done, and closing WriteChan would race with an in-flight send
			// in DistributeToClients and panic.
			if c, ok := channel.Restreamer.Clients.LoadAndDelete(ckey); ok {
				select {
				case <-c.Done:
				default:
					close(c.Done)
				}
			}
		} else {
			clientCount++
		}
		return true
	})

	// stop the restreamer entirely if no active clients remain
	if clientCount == 0 && channel.Restreamer.Running.Load() {
		lastActivity := channel.Restreamer.LastActivity.Load()
		if now-lastActivity > constants.Internal.ProxyClientInactivityTimeout {
			logger.Debug("{proxy/stream - RestreamCleanup} No active clients for channel %s (idle %ds), stopping restreamer", channel.Name, now-lastActivity)
			channel.Restreamer.CancelStream()
			channel.Restreamer.Running.Store(false)

			if b := channel.Restreamer.LoadBuffer(); b != nil && !b.IsDestroyed() {
				logger.Debug("{proxy/stream - RestreamCleanup} Channel %s: Safely destroying buffer", channel.Name)
				b.Destroy()
			}
		}
	}
}
