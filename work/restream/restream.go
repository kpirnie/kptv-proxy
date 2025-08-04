package restream

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/ratelimit"
)

// Restreamer handles single connection to upstream and multiple clients
type Restreamer struct {
	channel      *Channel
	clients      sync.Map // Use sync.Map for better concurrent access
	buffer       *RingBuffer
	running      atomic.Bool
	ctx          context.Context
	cancel       context.CancelFunc
	currentIndex int32
	lastActivity atomic.Int64 // Unix timestamp
	logger       *log.Logger
	httpClient   *HeaderSettingClient
	rateLimiter  ratelimit.Limiter
	config       *Config // Add config reference for URL obfuscation
}

// RestreamClient represents a connected client
type RestreamClient struct {
	id       string
	writer   http.ResponseWriter
	flusher  http.Flusher
	done     chan bool
	lastSeen atomic.Int64
}

func NewRestreamer(channel *Channel, bufferSize int64, logger *log.Logger, httpClient *HeaderSettingClient, rateLimiter ratelimit.Limiter, config *Config) *Restreamer {
	ctx, cancel := context.WithCancel(context.Background())
	r := &Restreamer{
		channel:     channel,
		buffer:      NewRingBuffer(bufferSize),
		ctx:         ctx,
		cancel:      cancel,
		logger:      logger,
		httpClient:  httpClient,
		rateLimiter: rateLimiter,
		config:      config,
	}
	r.lastActivity.Store(time.Now().Unix())
	r.running.Store(false)
	return r
}

func (r *Restreamer) AddClient(id string, w http.ResponseWriter, flusher http.Flusher) {
	client := &RestreamClient{
		id:      id,
		writer:  w,
		flusher: flusher,
		done:    make(chan bool),
	}
	client.lastSeen.Store(time.Now().Unix())

	r.clients.Store(id, client)
	r.lastActivity.Store(time.Now().Unix())

	// Update metrics
	count := 0
	r.clients.Range(func(_, _ interface{}) bool {
		count++
		return true
	})
	clientsConnected.WithLabelValues(r.channel.Name).Set(float64(count))

	r.logger.Printf("Added client %s to restreamer, total clients: %d", id, count)

	// Start restreaming if not running
	if !r.running.Load() {
		r.running.Store(true)
		r.logger.Printf("Starting restream goroutine for channel: %s", r.channel.Name)
		go r.stream()
	}
}

func (r *Restreamer) RemoveClient(id string) {
	if client, ok := r.clients.LoadAndDelete(id); ok {
		c := client.(*RestreamClient)
		close(c.done)
		r.buffer.RemoveClient(id)

		// Update metrics
		count := 0
		r.clients.Range(func(_, _ interface{}) bool {
			count++
			return true
		})
		clientsConnected.WithLabelValues(r.channel.Name).Set(float64(count))

		r.logger.Printf("Removed client %s from restreamer, remaining clients: %d", id, count)

		// Stop restreaming if no clients
		if count == 0 {
			r.logger.Printf("No more clients for channel %s, stopping restreamer", r.channel.Name)
			r.cancel()
			// Create new context for next time
			r.ctx, r.cancel = context.WithCancel(context.Background())
			r.running.Store(false)
		}
	}
}

func (r *Restreamer) stream() {
	defer func() {
		r.running.Store(false)
		activeConnections.WithLabelValues(r.channel.Name).Set(0)
	}()

	r.logger.Printf("Restreamer started for channel: %s", r.channel.Name)

	for {
		select {
		case <-r.ctx.Done():
			r.logger.Printf("Restreamer stopped for channel: %s", r.channel.Name)
			return
		default:
		}

		// Try current stream
		currentIdx := atomic.LoadInt32(&r.currentIndex)
		r.logger.Printf("Attempting stream index %d for channel: %s", currentIdx, r.channel.Name)
		success := r.streamFromSource(int(currentIdx))
		if !success {
			r.logger.Printf("Stream failed for channel: %s, trying next", r.channel.Name)
			// Try next stream
			newIdx := (currentIdx + 1) % int32(len(r.channel.Streams))
			atomic.StoreInt32(&r.currentIndex, newIdx)
			time.Sleep(time.Second) // Brief pause before retry
		}
	}
}

func (r *Restreamer) streamFromSource(index int) bool {
	r.channel.mu.RLock()
	if index >= len(r.channel.Streams) {
		r.channel.mu.RUnlock()
		r.logger.Printf("Invalid stream index %d for channel: %s", index, r.channel.Name)
		return false
	}
	stream := r.channel.Streams[index]
	r.channel.mu.RUnlock()

	// Check if stream is blocked
	if atomic.LoadInt32(&stream.Blocked) == 1 {
		r.logger.Printf("Stream blocked for channel %s: %s", r.channel.Name, r.logURL(stream.URL))
		return false
	}

	// Rate limit the request
	r.rateLimiter.Take()

	r.logger.Printf("Connecting to upstream for channel %s: %s", r.channel.Name, r.logURL(stream.URL))

	// Create request
	req, err := http.NewRequest("GET", stream.URL, nil)
	if err != nil {
		r.logger.Printf("Error creating request for channel %s: %v", r.channel.Name, err)
		return false
	}

	// Use longer timeout for restreaming
	ctx, cancel := context.WithTimeout(r.ctx, 5*time.Minute)
	defer cancel()
	req = req.WithContext(ctx)

	// Make request using httpClient with proper headers
	resp, err := r.httpClient.Do(req)
	if err != nil {
		r.logger.Printf("Error connecting to upstream for channel %s: %v", r.channel.Name, err)
		streamErrors.WithLabelValues(r.channel.Name, "connection").Inc()
		return false
	}

	if resp.StatusCode != http.StatusOK {
		r.logger.Printf("HTTP %d from upstream for channel %s", resp.StatusCode, r.channel.Name)
		streamErrors.WithLabelValues(r.channel.Name, fmt.Sprintf("http_%d", resp.StatusCode)).Inc()
		resp.Body.Close()
		return false
	}
	defer resp.Body.Close()

	r.logger.Printf("Successfully connected to upstream for channel %s, starting stream", r.channel.Name)
	activeConnections.WithLabelValues(r.channel.Name).Set(1)

	// Stream data to all clients
	buffer := make([]byte, 32*1024) // 32KB chunks
	totalBytes := int64(0)
	lastLog := time.Now()

	for {
		select {
		case <-r.ctx.Done():
			r.logger.Printf("Restreamer context cancelled for channel: %s (transferred %d bytes)", r.channel.Name, totalBytes)
			return true
		default:
		}

		n, err := resp.Body.Read(buffer)
		if n > 0 {
			data := buffer[:n]
			r.buffer.Write(data)
			totalBytes += int64(n)
			bytesTransferred.WithLabelValues(r.channel.Name, "downstream").Add(float64(n))

			// Log progress every 10 seconds
			if time.Since(lastLog) > 10*time.Second {
				clientCount := 0
				r.clients.Range(func(_, _ interface{}) bool {
					clientCount++
					return true
				})
				r.logger.Printf("Channel %s streaming: %d bytes transferred, %d clients", r.channel.Name, totalBytes, clientCount)
				lastLog = time.Now()
			}

			// Send to all clients
			r.distributeToClients(data)
		}

		if err != nil {
			if err == io.EOF {
				r.logger.Printf("Stream ended normally for channel %s after %d bytes", r.channel.Name, totalBytes)
				return true // Normal end
			}
			r.logger.Printf("Error reading stream for channel %s after %d bytes: %v", r.channel.Name, totalBytes, err)
			streamErrors.WithLabelValues(r.channel.Name, "read").Inc()
			return false // Error
		}
	}
}

func (r *Restreamer) distributeToClients(data []byte) {
	r.clients.Range(func(key, value interface{}) bool {
		id := key.(string)
		client := value.(*RestreamClient)

		select {
		case <-client.done:
			// Client disconnected, remove it
			go r.RemoveClient(id)
			return true
		default:
			_, writeErr := client.writer.Write(data)
			if writeErr != nil {
				r.logger.Printf("Error writing to client %s: %v", id, writeErr)
				go r.RemoveClient(id)
			} else {
				client.flusher.Flush()
				client.lastSeen.Store(time.Now().Unix())
				bytesTransferred.WithLabelValues(r.channel.Name, "upstream").Add(float64(len(data)))
			}
		}
		return true
	})
}
