package restream

import (
	"context"
	"fmt"
	"kptv-proxy/work/constants"
	"kptv-proxy/work/logger"
	"kptv-proxy/work/metrics"
	"kptv-proxy/work/types"
	"net/http"
	"time"

	"github.com/puzpuzpuz/xsync/v3"
)

// AddClient registers a new client to receive stream data.
// - id: unique identifier for the client
// - w: the HTTP response writer
// - flusher: the HTTP flusher to push data immediately
func (r *Restream) AddClient(id string, w http.ResponseWriter, flusher http.Flusher) {
	if r.Clients == nil {
		r.Clients = xsync.NewMapOf[string, *types.RestreamClient]()
	}

	writeChanDepth := r.Config.SlowClientBufferChunks
	if r.Config.FFmpegMode {
		writeChanDepth = constants.Internal.FFmpegClientBufferChunks
	}

	client := &types.RestreamClient{
		Id:        id,
		Writer:    w,
		Flusher:   flusher,
		Done:      make(chan bool),
		WriteChan: make(chan *types.StreamChunk, writeChanDepth),
	}

	client.LastSeen.Store(time.Now().Unix())
	client.LastProgress.Store(time.Now().Unix())
	r.Clients.Store(id, client)
	r.LastActivity.Store(time.Now().Unix())

	// Start the per-client drain goroutine that owns all writes to this client's
	// socket, keeping the distribution loop fully decoupled from TCP drain speed.
	go r.drainClient(client)

	// setup the client counter
	clientCount := int(r.ClientCount.Add(1))

	metrics.ClientsConnected.WithLabelValues(r.Channel.Name).Set(float64(clientCount))

	logger.Debug("{restream/restream - AddClient} Channel %s: ID: %s, Total: %d", r.Channel.Name, id, clientCount)

	// serialize against stopStream: without this, a client connecting while
	// the last client's teardown is mid-flight can launch Stream() against a
	// cancelled context and destroyed buffer
	r.Lifecycle.Lock()
	started := false
	if !r.Running.Load() && r.Running.CompareAndSwap(false, true) {
		logger.Debug("{restream/restream - AddClient} Channel %s: Starting", r.Channel.Name)

		// install the lifetime context here rather than in stopStream, so a
		// context is never replaced underneath a running Stream() goroutine
		if ctx := r.LifetimeContext(); ctx.Err() != nil {
			newCtx, newCancel := context.WithCancel(context.Background())
			r.SetContext(newCtx, newCancel)
		}
		r.ClearAttemptContext()
		r.SwitchTo.Store(-1)

		go r.Stream()
		go r.monitorClientHealth()
		go r.StartStatsCollection()
		started = true
	}
	r.Lifecycle.Unlock()

	if started {
		// Brief delay to allow buffer to pre-warm before client writes
		logger.Debug("{restream/restream - AddClient} Channel %s: Starting buffer warmup delay", r.Channel.Name)
		time.Sleep(constants.Internal.BufferWarmupDelay)
	}
}

// RemoveClient unregisters a client from the restreamer.
// - id: unique identifier for the client to be removed
func (r *Restream) RemoveClient(id string) {

	// Attempt to load and delete the client from the map
	if client, ok := r.Clients.LoadAndDelete(id); ok {
		// Signal the drain goroutine to exit by closing Done. WriteChan is never
		// closed here: DistributeToClients has multiple concurrent senders, so
		// closing it would race with an in-flight send and panic.
		select {
		case <-client.Done:
			// Already closed
		default:
			close(client.Done)
		}

		// setup the client counter
		clientCount := int(r.ClientCount.Add(-1))

		// Update Prometheus metrics for clients
		metrics.ClientsConnected.WithLabelValues(r.Channel.Name).Set(float64(clientCount))

		// Debug logging
		logger.Debug("{restream/restream - RemoveClient} clients_connected: %d [%s]", clientCount, r.Channel.Name)
		logger.Debug("{restream/restream - RemoveClient} Channel %s: Client %s removed, remaining: %d", r.Channel.Name, id, clientCount)

		if clientCount == 0 {
			logger.Debug("{restream/restream - RemoveClient} Channel %s: No more clients", r.Channel.Name)
			r.stopStream()
		}
	}

}

// DistributeToClients enqueues a chunk of stream data into each active client's
// outbound channel. The send is non-blocking — if a client's channel is full the
// client is considered too slow and is scheduled for removal, preventing it from
// stalling the distribution loop and degrading faster clients.
func (r *Restream) DistributeToClients(data []byte) int {
	activeClients := 0

	// Pooled refcounted copy so every client channel holds the same buffer
	// independently of the streaming loop, which reuses its read buffer
	// immediately after return.
	chunk := types.NewStreamChunk(data)
	defer chunk.Release()

	r.Clients.Range(func(key string, value *types.RestreamClient) bool {
		client := value

		chunk.Retain()
		select {
		case client.WriteChan <- chunk:
			// Successful enqueue counts as progress; resets the slow-client clock.
			client.LastProgress.Store(time.Now().Unix())
			activeClients++
		default:
			// Channel full: the client is briefly behind the live edge, common on
			// bursty or high-bitrate (e.g. 4K HEVC) sources that deliver faster than
			// realtime. Rather than drop the client, discard the oldest queued chunk
			// and enqueue the newest so it stays on the live edge. TS decoders resync
			// after a gap, trading a momentary artifact for uninterrupted playback.
			select {
			case old := <-client.WriteChan: // shed oldest chunk
				old.Release()
			default:
			}
			select {
			case client.WriteChan <- chunk:
				client.LastProgress.Store(time.Now().Unix())
			default:
				chunk.Release()
			}
			activeClients++ // keep the client; never drop on a full buffer alone
		}
		return true
	})

	return activeClients
}

// SafeBufferWrite writes data to the buffer if it is still valid.
// It ensures data is not written if the buffer has been destroyed
// or the streaming context is cancelled.
//
// Parameters:
//   - data: the byte slice to write into the buffer
//
// Returns:
//   - bool: true if write succeeded, false if buffer closed/cancelled
func (r *Restream) SafeBufferWrite(data []byte) bool {

	// Check if context cancelled due to manual switch - allow this to succeed
	select {
	case <-r.Context().Done():
		if r.SwitchPending() {
			// still write the data if the buffer is alive so the watcher's
			// throughput tracking and stats peeks don't see a false gap —
			// previously this returned success while silently skipping the
			// write, desyncing the buffer from what clients received
			if b := r.LoadBuffer(); b != nil && !b.IsDestroyed() {
				b.Write(data)
			}
			logger.Debug("{restream/restream - SafeBufferWrite} Channel %s: Buffer write during manual switch, allowing success", r.Channel.Name)
			return true // Don't treat manual switch cancellation as buffer failure
		}
		return false
	default:
	}

	// Check buffer validity (capture once to avoid a torn read vs a concurrent swap)
	b := r.LoadBuffer()
	if b == nil || b.IsDestroyed() {
		return false
	}

	// Perform write into ring buffer
	b.Write(data)
	return true
}

// monitor client health
func (r *Restream) monitorClientHealth() {
	logger.Debug("{restream/restream - monitorClientHealth} Starting health monitor for channel %s", r.Channel.Name)

	ticker := time.NewTicker(constants.Internal.ClientHealthCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-r.LifetimeContext().Done():
			logger.Debug("{restream/restream - monitorClientHealth} Health monitor stopping for channel %s", r.Channel.Name)
			return
		case <-ticker.C:
			if !r.Running.Load() {
				logger.Debug("{restream/restream - monitorClientHealth} Stream not running for channel %s, stopping monitor", r.Channel.Name)
				return
			}

			now := time.Now().Unix()
			var staleClients []string

			r.Clients.Range(func(key string, value *types.RestreamClient) bool {
				client := value
				lastSeen := client.LastSeen.Load()

				if now-lastSeen > constants.Internal.ClientStaleTimeout {
					staleClients = append(staleClients, key)
				}
				return true
			})

			if len(staleClients) > 0 {
				logger.Debug("{restream/restream - monitorClientHealth} Health check for channel %s: found %d stale clients", r.Channel.Name, len(staleClients))
			}

			for _, clientID := range staleClients {
				logger.Debug("{restream/restream - monitorClientHealth} Removing stale client: %s", clientID)

				r.RemoveClient(clientID)
			}
		}
	}
}

// drainClient is the per-client goroutine that owns all writes to a single client's
// HTTP response writer. It reads chunks from the client's bounded writeChan and
// writes them to the socket sequentially. Exits cleanly when writeChan is closed
// by RemoveClient, or removes itself if a write or flush fails.
func (r *Restream) drainClient(client *types.RestreamClient) {
	// per-write deadlines via ResponseController — without them a client with
	// a stuck TCP window blocks Write() forever and leaks this goroutine and
	// the connection, since the server intentionally has no global WriteTimeout
	rc := http.NewResponseController(client.Writer)

	for {
		select {
		case <-client.Done:
			// Client removed. WriteChan is intentionally never closed because
			// DistributeToClients has multiple concurrent senders; Done is the
			// sole termination signal. Queued chunks are released so their
			// buffers return to the pool.
			for {
				select {
				case chunk := <-client.WriteChan:
					chunk.Release()
					continue
				default:
				}
				break
			}
			logger.Debug("{restream/restream - drainClient} Channel %s: Drain goroutine exiting for client %s",
				r.Channel.Name, client.Id)
			return
		case chunk := <-client.WriteChan:
			writeErr := func() (err error) {
				defer func() {
					if rec := recover(); rec != nil {
						err = fmt.Errorf("write/flush panic recovered: %v", rec)
					}
				}()
				rc.SetWriteDeadline(time.Now().Add(constants.Internal.ClientWriteDeadline))
				_, err = client.Writer.Write(chunk.Data)
				if err != nil {
					return err
				}
				client.Flusher.Flush()
				return nil
			}()
			chunk.Release()

			if writeErr != nil {
				logger.Debug("{restream/restream - drainClient} Channel %s: Write error for client %s, removing: %v",
					r.Channel.Name, client.Id, writeErr)
				r.RemoveClient(client.Id)
				return
			}

			client.LastSeen.Store(time.Now().Unix())
		}
	}
}
