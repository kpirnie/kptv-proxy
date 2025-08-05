package restream

import (
	"context"
	"fmt"
	"io"
	"kptv-proxy/work/buffer"
	"kptv-proxy/work/client"
	"kptv-proxy/work/config"
	"kptv-proxy/work/metrics"
	"kptv-proxy/work/types"
	"kptv-proxy/work/utils"
	"log"
	"net/http"
	"sync/atomic"
	"time"
)

// Restream wraps types.Restreamer to allow adding methods in this package.
type Restream struct {
	*types.Restreamer
}

func NewRestreamer(channel *types.Channel, bufferSize int64, logger *log.Logger, httpClient *client.HeaderSettingClient, cfg *config.Config) *Restream {
	ctx, cancel := context.WithCancel(context.Background())
	base := &types.Restreamer{
		Channel:    channel,
		Buffer:     buffer.NewRingBuffer(bufferSize),
		Ctx:        ctx,
		Cancel:     cancel,
		Logger:     logger,
		HttpClient: httpClient,
		Config:     cfg,
	}
	base.LastActivity.Store(time.Now().Unix())
	base.Running.Store(false)
	return &Restream{base}
}

func (r *Restream) AddClient(id string, w http.ResponseWriter, flusher http.Flusher) {
	client := &types.RestreamClient{
		Id:      id,
		Writer:  w,
		Flusher: flusher,
		Done:    make(chan bool),
	}
	client.LastSeen.Store(time.Now().Unix())

	r.Clients.Store(id, client)
	r.LastActivity.Store(time.Now().Unix())

	// Update metrics
	count := 0
	r.Clients.Range(func(_, _ interface{}) bool {
		count++
		return true
	})
	metrics.ClientsConnected.WithLabelValues(r.Channel.Name).Set(float64(count))

	r.Logger.Printf("[CLIENT_CONNECT] Channel %s: ID: %s", r.Channel.Name, id)
	r.Logger.Printf("[METRIC] clients_connected: %d [%s]", count, r.Channel.Name)
	r.Logger.Printf("[CLIENT_ADD] Channel %s: Client %s added, total: %d", r.Channel.Name, id, count)

	// Start restreaming if not running
	if !r.Running.Load() && r.Running.CompareAndSwap(false, true) {
		r.Logger.Printf("[RESTREAM_START] Channel %s: Starting goroutine", r.Channel.Name)
		r.Logger.Printf("[CLIENT_READY] Channel %s: Client %s waiting", r.Channel.Name, id)
		r.Logger.Printf("[RESTREAM_ACTIVE] Channel %s: Restreamer started", r.Channel.Name)
		go r.Stream()
	}
}

func (r *Restream) RemoveClient(id string) {
	if client, ok := r.Clients.LoadAndDelete(id); ok {
		c := client.(*types.RestreamClient)

		// Signal client is done
		select {
		case <-c.Done:
			// Already closed
		default:
			close(c.Done)
		}

		r.Buffer.RemoveClient(id)

		// Update metrics
		count := 0
		r.Clients.Range(func(_, _ interface{}) bool {
			count++
			return true
		})
		metrics.ClientsConnected.WithLabelValues(r.Channel.Name).Set(float64(count))

		r.Logger.Printf("[CLIENT_DISCONNECT] Channel %s: Client: %s", r.Channel.Name, id)
		r.Logger.Printf("[METRIC] clients_connected: %d [%s]", count, r.Channel.Name)
		r.Logger.Printf("[CLIENT_REMOVE] Channel %s: Client %s removed, remaining: %d", r.Channel.Name, id, count)

		// Stop restreaming immediately if no clients
		if count == 0 {
			r.Logger.Printf("[RESTREAM_STOP] Channel %s: No more clients", r.Channel.Name)
			r.stopStream()
		}
	}
}

func (r *Restream) stopStream() {
	if r.Running.CompareAndSwap(true, false) {
		r.Cancel() // Cancel current context
		// Create new context for next time (but don't start until new client)
		r.Ctx, r.Cancel = context.WithCancel(context.Background())
		atomic.StoreInt32(&r.CurrentIndex, 0) // Reset to first stream
	}
}

func (r *Restream) Stream() {
	defer func() {
		r.Running.Store(false)
		metrics.ActiveConnections.WithLabelValues(r.Channel.Name).Set(0)
	}()

	r.Logger.Printf("[RESTREAM_CONFIG] Channel %s: MinDataSize = %d bytes", r.Channel.Name, r.Config.MinDataSize)

	r.Channel.Mu.RLock()
	streamCount := len(r.Channel.Streams)
	r.Channel.Mu.RUnlock()

	if streamCount == 0 {
		r.Logger.Printf("[STREAM_ERROR] Channel %s: No streams available", r.Channel.Name)
		return
	}

	r.Logger.Printf("[STREAM_INFO] Channel %s: Has %d streams available", r.Channel.Name, streamCount)

	maxTotalAttempts := streamCount * 2 // Try each stream twice max
	totalAttempts := 0

	for totalAttempts < maxTotalAttempts {
		// Check if we should stop immediately
		select {
		case <-r.Ctx.Done():
			r.Logger.Printf("[RESTREAM_STOP] Channel %s: Context cancelled", r.Channel.Name)
			return
		default:
		}

		// Check if we still have clients
		clientCount := 0
		r.Clients.Range(func(_, _ interface{}) bool {
			clientCount++
			return true
		})

		if clientCount == 0 {
			r.Logger.Printf("[RESTREAM_STOP] Channel %s: No clients remain", r.Channel.Name)
			return
		}

		// Get current stream index
		currentIdx := int(atomic.LoadInt32(&r.CurrentIndex))
		totalAttempts++

		r.Logger.Printf("[STREAM_TRY] Channel %s: Attempting index %d (total attempt %d/%d)",
			r.Channel.Name, currentIdx, totalAttempts, maxTotalAttempts)

		streamStart := time.Now()
		success, bytesReceived := r.StreamFromSource(currentIdx)
		streamDuration := time.Since(streamStart)

		r.Logger.Printf("[STREAM_RESULT] Channel %s: Index %d - Success: %v, Bytes: %d, Duration: %v",
			r.Channel.Name, currentIdx, success, bytesReceived, streamDuration)

		// Simple logic: If we got sufficient data, this stream works
		if success && bytesReceived >= r.Config.MinDataSize {
			r.Logger.Printf("[STREAM_SUCCESS] Channel %s: Found working stream with %d bytes, keeping connection alive",
				r.Channel.Name, bytesReceived)

			// Keep connection alive until client disconnects or context cancels
			for {
				select {
				case <-r.Ctx.Done():
					r.Logger.Printf("[RESTREAM_STOP] Channel %s: Context cancelled", r.Channel.Name)
					return
				default:
				}

				// Check if we still have clients
				clientCount := 0
				r.Clients.Range(func(_, _ interface{}) bool {
					clientCount++
					return true
				})

				if clientCount == 0 {
					r.Logger.Printf("[RESTREAM_STOP] Channel %s: No clients remain", r.Channel.Name)
					return
				}

				// Just wait - don't re-fetch anything
				r.Logger.Printf("[STREAM_IDLE] Channel %s: Keeping connection alive, waiting for client disconnect", r.Channel.Name)
				select {
				case <-r.Ctx.Done():
					return
				case <-time.After(30 * time.Second): // Just wait
					continue
				}
			}
		} else {
			r.Logger.Printf("[STREAM_FAIL] Channel %s: Stream failed or insufficient data", r.Channel.Name)
		}

		// Move to next stream only if current one failed
		if streamCount > 1 {
			newIdx := (currentIdx + 1) % streamCount
			atomic.StoreInt32(&r.CurrentIndex, int32(newIdx))
			r.Logger.Printf("[STREAM_SWITCH] Channel %s: Moving from index %d to %d",
				r.Channel.Name, currentIdx, newIdx)
		} else {
			r.Logger.Printf("[STREAM_SINGLE] Channel %s: Only 1 stream available, will retry after delay", r.Channel.Name)
		}

		// Brief pause before retry
		select {
		case <-r.Ctx.Done():
			return
		case <-time.After(2 * time.Second):
			// Continue
		}
	}

	r.Logger.Printf("[STREAM_EXHAUSTED] Channel %s: All streams tried %d times, stopping", r.Channel.Name, maxTotalAttempts)
}

func (r *Restream) StreamFromSource(index int) (bool, int64) {
	r.Channel.Mu.RLock()
	if index >= len(r.Channel.Streams) {
		r.Channel.Mu.RUnlock()
		r.Logger.Printf("Invalid stream index %d for channel: %s", index, r.Channel.Name)
		return false, 0
	}
	stream := r.Channel.Streams[index]
	r.Channel.Mu.RUnlock()

	// Check if stream is blocked
	if atomic.LoadInt32(&stream.Blocked) == 1 {
		r.Logger.Printf("Stream blocked for channel %s: %s", r.Channel.Name, utils.LogURL(r.Config, stream.URL))
		return false, 0
	}

	// CRITICAL: Check connection limit BEFORE making request
	currentConns := atomic.LoadInt32(&stream.Source.ActiveConns)
	maxConns := int32(stream.Source.MaxConnections)

	if currentConns >= maxConns {
		r.Logger.Printf("[CONNECTION_LIMIT] Channel %s: Source at limit (%d/%d): %s",
			r.Channel.Name, currentConns, maxConns, utils.LogURL(r.Config, stream.URL))
		return false, 0
	}

	// Increment connection count BEFORE making request
	newConns := atomic.AddInt32(&stream.Source.ActiveConns, 1)
	r.Logger.Printf("[CONNECTION_ACQUIRE] Channel %s: Using connection %d/%d for %s",
		r.Channel.Name, newConns, maxConns, utils.LogURL(r.Config, stream.URL))

	// CRITICAL: Always decrement connection count when done
	defer func() {
		remainingConns := atomic.AddInt32(&stream.Source.ActiveConns, -1)
		r.Logger.Printf("[CONNECTION_RELEASE] Channel %s: Released connection, remaining: %d/%d",
			r.Channel.Name, remainingConns, maxConns)
	}()

	r.Logger.Printf("Successfully connected to stream: %s", utils.LogURL(r.Config, stream.URL))

	// Create request with reasonable timeout
	req, err := http.NewRequest("GET", stream.URL, nil)
	if err != nil {
		r.Logger.Printf("Error creating request for channel %s: %v", r.Channel.Name, err)
		return false, 0
	}

	// Use context with timeout for the entire request
	requestCtx, requestCancel := context.WithTimeout(r.Ctx, 30*time.Second)
	defer requestCancel()
	req = req.WithContext(requestCtx)

	// Make request
	resp, err := r.HttpClient.Do(req)
	if err != nil {
		r.Logger.Printf("Error connecting to stream for %s: %v", r.Channel.Name, err)
		r.Logger.Printf("[STREAM_ERROR] Channel %s: Connection error: %v", r.Channel.Name, err)
		metrics.StreamErrors.WithLabelValues(r.Channel.Name, "connection").Inc()
		return false, 0
	}

	// CRITICAL: Always close response body
	defer func() {
		resp.Body.Close()
		r.Logger.Printf("[CONNECTION_CLOSE] Channel %s: HTTP connection closed", r.Channel.Name)
	}()

	if resp.StatusCode != http.StatusOK {
		r.Logger.Printf("HTTP %d from upstream for channel %s", resp.StatusCode, r.Channel.Name)
		metrics.StreamErrors.WithLabelValues(r.Channel.Name, fmt.Sprintf("http_%d", resp.StatusCode)).Inc()
		return false, 0
	}

	r.Logger.Printf("[CONNECTED] Channel %s: Streaming from index %d", r.Channel.Name, index)
	metrics.ActiveConnections.WithLabelValues(r.Channel.Name).Set(1)

	// Stream data with frequent cancellation checks
	buffer := make([]byte, 32*1024) // 32KB chunks for better performance
	totalBytes := int64(0)

	// Set a deadline for receiving first data
	firstDataTimer := time.NewTimer(10 * time.Second)
	defer firstDataTimer.Stop()
	firstData := true
	lastDataTime := time.Now()

	for {
		// Check for cancellation more frequently
		select {
		case <-r.Ctx.Done():
			r.Logger.Printf("Error reading stream for %s: context canceled", r.Channel.Name)
			r.Logger.Printf("[STREAM_ERROR] Channel %s: Error after %d bytes: context canceled", r.Channel.Name, totalBytes)
			return totalBytes >= r.Config.MinDataSize, totalBytes
		case <-requestCtx.Done():
			r.Logger.Printf("Request timeout for channel %s after %d bytes", r.Channel.Name, totalBytes)
			return totalBytes >= r.Config.MinDataSize, totalBytes
		case <-firstDataTimer.C:
			if firstData && totalBytes == 0 {
				r.Logger.Printf("[STREAM_ERROR] Channel %s: No data received in 10 seconds", r.Channel.Name)
				return false, 0
			}
		default:
		}

		// Check for data timeout (no data for 30 seconds)
		if !firstData && time.Since(lastDataTime) > 30*time.Second {
			r.Logger.Printf("[STREAM_TIMEOUT] Channel %s: No data for 30 seconds, ending stream", r.Channel.Name)
			return totalBytes >= r.Config.MinDataSize, totalBytes
		}

		n, err := resp.Body.Read(buffer)
		if n > 0 {
			if firstData {
				firstData = false
				firstDataTimer.Stop()
				r.Logger.Printf("[STREAM_DATA] Channel %s: First data received", r.Channel.Name)
			}

			lastDataTime = time.Now()
			data := buffer[:n]
			r.Buffer.Write(data)
			totalBytes += int64(n)
			metrics.BytesTransferred.WithLabelValues(r.Channel.Name, "downstream").Add(float64(n))

			// Send to all clients and check if any disconnected
			activeClients := r.DistributeToClients(data)
			if activeClients == 0 {
				r.Logger.Printf("[STREAM_STOP] Channel %s: No active clients", r.Channel.Name)
				return totalBytes >= r.Config.MinDataSize, totalBytes
			}
		}

		if err != nil {
			if err == io.EOF {
				r.Logger.Printf("Stream ended normally for channel %s after %d bytes", r.Channel.Name, totalBytes)
				// Check if we got enough data according to MIN_DATA_SIZE
				if totalBytes >= r.Config.MinDataSize {
					r.Logger.Printf("Stream provided sufficient data (%d >= %d bytes)", totalBytes, r.Config.MinDataSize)
					return true, totalBytes
				} else {
					r.Logger.Printf("Stream ended but only transferred %d bytes (minimum: %d)", totalBytes, r.Config.MinDataSize)
					return false, totalBytes
				}
			}

			// Check if error is due to context cancellation
			select {
			case <-r.Ctx.Done():
				r.Logger.Printf("Error reading stream for %s: context canceled", r.Channel.Name)
				r.Logger.Printf("[STREAM_ERROR] Channel %s: Error after %d bytes: context canceled", r.Channel.Name, totalBytes)
				return totalBytes >= r.Config.MinDataSize, totalBytes
			default:
				r.Logger.Printf("Error reading stream for channel %s after %d bytes: %v", r.Channel.Name, totalBytes, err)
				r.Logger.Printf("[STREAM_ERROR] Channel %s: Error after %d bytes: %s", r.Channel.Name, totalBytes, err.Error())
				metrics.StreamErrors.WithLabelValues(r.Channel.Name, "read").Inc()
				return false, totalBytes
			}
		}
	}
}

func (r *Restream) DistributeToClients(data []byte) int {
	activeClients := 0

	r.Clients.Range(func(key, value interface{}) bool {
		id := key.(string)
		client := value.(*types.RestreamClient)

		select {
		case <-client.Done:
			// Client disconnected, will be cleaned up
			return true
		default:
			_, writeErr := client.Writer.Write(data)
			if writeErr != nil {
				r.Logger.Printf("Error writing to client %s: %v", id, writeErr)
				// Don't remove here, let the main handler remove it
			} else {
				client.Flusher.Flush()
				client.LastSeen.Store(time.Now().Unix())
				metrics.BytesTransferred.WithLabelValues(r.Channel.Name, "upstream").Add(float64(len(data)))
				activeClients++
			}
		}
		return true
	})

	return activeClients
}
