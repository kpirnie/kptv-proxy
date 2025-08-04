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

	"go.uber.org/ratelimit"
)

// Restream wraps types.Restreamer to allow adding methods in this package.
type Restream struct {
	*types.Restreamer
}

func NewRestreamer(channel *types.Channel, bufferSize int64, logger *log.Logger, httpClient *client.HeaderSettingClient, rateLimiter ratelimit.Limiter, cfg *config.Config) *Restream {
	ctx, cancel := context.WithCancel(context.Background())
	base := &types.Restreamer{
		Channel:     channel,
		Buffer:      buffer.NewRingBuffer(bufferSize),
		Ctx:         ctx,
		Cancel:      cancel,
		Logger:      logger,
		HttpClient:  httpClient,
		RateLimiter: rateLimiter,
		Config:      cfg,
	}
	base.LastActivity.Store(time.Now().Unix())
	base.Running.Store(false)
	return &Restream{base} // Wrap the base Restreamer
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

	r.Logger.Printf("Added client %s to restreamer, total clients: %d", id, count)

	// Start restreaming if not running
	if !r.Running.Load() {
		r.Running.Store(true)
		r.Logger.Printf("Starting restream goroutine for channel: %s", r.Channel.Name)
		go r.Stream()
	}
}

func (r *Restream) RemoveClient(id string) {
	if client, ok := r.Clients.LoadAndDelete(id); ok {
		c := client.(*types.RestreamClient)
		close(c.Done)
		r.Buffer.RemoveClient(id)

		// Update metrics
		count := 0
		r.Clients.Range(func(_, _ interface{}) bool {
			count++
			return true
		})
		metrics.ClientsConnected.WithLabelValues(r.Channel.Name).Set(float64(count))

		r.Logger.Printf("Removed client %s from restreamer, remaining clients: %d", id, count)

		// Stop restreaming if no clients
		if count == 0 {
			r.Logger.Printf("No more clients for channel %s, stopping restreamer", r.Channel.Name)
			r.Cancel()
			// Create new context for next time
			r.Ctx, r.Cancel = context.WithCancel(context.Background())
			r.Running.Store(false)
		}
	}
}

func (r *Restream) Stream() {
	defer func() {
		r.Running.Store(false)
		metrics.ActiveConnections.WithLabelValues(r.Channel.Name).Set(0)
	}()

	r.Logger.Printf("Restreamer started for channel: %s", r.Channel.Name)

	for {
		select {
		case <-r.Ctx.Done():
			r.Logger.Printf("Restreamer stopped for channel: %s", r.Channel.Name)
			return
		default:
		}

		// Try current stream
		currentIdx := atomic.LoadInt32(&r.CurrentIndex)
		r.Logger.Printf("Attempting stream index %d for channel: %s", currentIdx, r.Channel.Name)
		success := r.StreamFromSource(int(currentIdx))
		if !success {
			r.Logger.Printf("Stream failed for channel: %s, trying next", r.Channel.Name)
			// Try next stream
			newIdx := (currentIdx + 1) % int32(len(r.Channel.Streams))
			atomic.StoreInt32(&r.CurrentIndex, newIdx)
			time.Sleep(time.Second) // Brief pause before retry
		}
	}
}

func (r *Restream) StreamFromSource(index int) bool {
	r.Channel.Mu.RLock()
	if index >= len(r.Channel.Streams) {
		r.Channel.Mu.RUnlock()
		r.Logger.Printf("Invalid stream index %d for channel: %s", index, r.Channel.Name)
		return false
	}
	stream := r.Channel.Streams[index]
	r.Channel.Mu.RUnlock()

	// Check if stream is blocked
	if atomic.LoadInt32(&stream.Blocked) == 1 {
		r.Logger.Printf("Stream blocked for channel %s: %s", r.Channel.Name, utils.LogURL(r.Config, stream.URL))
		return false
	}

	// Rate limit the request
	r.RateLimiter.Take()

	r.Logger.Printf("Connecting to upstream for channel %s: %s", r.Channel.Name, utils.LogURL(r.Config, stream.URL))

	// Create request
	req, err := http.NewRequest("GET", stream.URL, nil)
	if err != nil {
		r.Logger.Printf("Error creating request for channel %s: %v", r.Channel.Name, err)
		return false
	}

	// Use longer timeout for restreaming
	ctx, cancel := context.WithTimeout(r.Ctx, 5*time.Minute)
	defer cancel()
	req = req.WithContext(ctx)

	// Make request using httpClient with proper headers
	resp, err := r.HttpClient.Do(req)
	if err != nil {
		r.Logger.Printf("Error connecting to upstream for channel %s: %v", r.Channel.Name, err)
		metrics.StreamErrors.WithLabelValues(r.Channel.Name, "connection").Inc()
		return false
	}

	if resp.StatusCode != http.StatusOK {
		r.Logger.Printf("HTTP %d from upstream for channel %s", resp.StatusCode, r.Channel.Name)
		metrics.StreamErrors.WithLabelValues(r.Channel.Name, fmt.Sprintf("http_%d", resp.StatusCode)).Inc()
		resp.Body.Close()
		return false
	}
	defer resp.Body.Close()

	r.Logger.Printf("Successfully connected to upstream for channel %s, starting stream", r.Channel.Name)
	metrics.ActiveConnections.WithLabelValues(r.Channel.Name).Set(1)

	// Stream data to all clients
	buffer := make([]byte, 32*1024) // 32KB chunks
	totalBytes := int64(0)
	lastLog := time.Now()

	for {
		select {
		case <-r.Ctx.Done():
			r.Logger.Printf("Restreamer context cancelled for channel: %s (transferred %d bytes)", r.Channel.Name, totalBytes)
			return true
		default:
		}

		n, err := resp.Body.Read(buffer)
		if n > 0 {
			data := buffer[:n]
			r.Buffer.Write(data)
			totalBytes += int64(n)
			metrics.BytesTransferred.WithLabelValues(r.Channel.Name, "downstream").Add(float64(n))

			// Log progress every 10 seconds
			if time.Since(lastLog) > 10*time.Second {
				clientCount := 0
				r.Clients.Range(func(_, _ interface{}) bool {
					clientCount++
					return true
				})
				r.Logger.Printf("Channel %s streaming: %d bytes transferred, %d clients", r.Channel.Name, totalBytes, clientCount)
				lastLog = time.Now()
			}

			// Send to all clients
			r.DistributeToClients(data)
		}

		if err != nil {
			if err == io.EOF {
				r.Logger.Printf("Stream ended normally for channel %s after %d bytes", r.Channel.Name, totalBytes)
				return true // Normal end
			}
			r.Logger.Printf("Error reading stream for channel %s after %d bytes: %v", r.Channel.Name, totalBytes, err)
			metrics.StreamErrors.WithLabelValues(r.Channel.Name, "read").Inc()
			return false // Error
		}
	}
}

func (r *Restream) DistributeToClients(data []byte) {
	r.Clients.Range(func(key, value interface{}) bool {
		id := key.(string)
		client := value.(*types.RestreamClient)

		select {
		case <-client.Done:
			// Client disconnected, remove it
			go r.Buffer.RemoveClient(id)
			return true
		default:
			_, writeErr := client.Writer.Write(data)
			if writeErr != nil {
				r.Logger.Printf("Error writing to client %s: %v", id, writeErr)
				go r.Buffer.RemoveClient(id)
			} else {
				client.Flusher.Flush()
				client.LastSeen.Store(time.Now().Unix())
				metrics.BytesTransferred.WithLabelValues(r.Channel.Name, "upstream").Add(float64(len(data)))
			}
		}
		return true
	})
}
