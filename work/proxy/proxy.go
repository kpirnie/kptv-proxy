package proxy

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"kptv-proxy/work/buffer"
	"kptv-proxy/work/cache"
	"kptv-proxy/work/client"
	"kptv-proxy/work/config"

	"github.com/VictoriaMetrics/fastcache"
	"github.com/panjf2000/ants/v2"
	"go.uber.org/ratelimit"
)

// StreamProxy is the main application struct
type StreamProxy struct {
	config       *config.Config
	channels     sync.Map // Use sync.Map for concurrent access
	cache        *cache.Cache
	segmentCache *fastcache.Cache
	logger       *log.Logger
	bufferPool   *buffer.BufferPool
	httpClient   *client.HeaderSettingClient
	workerPool   *ants.Pool
	rateLimiter  ratelimit.Limiter
}

// Import streams using grafov/m3u8 for better parsing
func (sp *StreamProxy) ImportStreams() {
	sp.logger.Println("Starting stream import...")

	if len(sp.config.Sources) == 0 {
		sp.logger.Println("WARNING: No sources configured!")
		return
	}

	var wg sync.WaitGroup
	newChannels := sync.Map{}

	for i := range sp.config.Sources {
		wg.Add(1)
		source := &sp.config.Sources[i]

		err := sp.workerPool.Submit(func() {
			defer wg.Done()
			sp.rateLimiter.Take() // Rate limit imports

			streams := sp.parser.ParseM3U8(source)
			for _, stream := range streams {
				channelName := stream.Name
				actual, _ := newChannels.LoadOrStore(channelName, &Channel{
					Name:    channelName,
					Streams: []*Stream{},
				})
				channel := actual.(*Channel)
				channel.mu.Lock()
				channel.Streams = append(channel.Streams, stream)
				channel.mu.Unlock()
			}
		})

		if err != nil {
			sp.logger.Printf("Worker pool error: %v", err)
		}
	}

	wg.Wait()

	// Sort streams in each channel and migrate to main map
	count := 0
	newChannels.Range(func(key, value interface{}) bool {
		channel := value.(*Channel)
		sp.sortStreams(channel.Streams)
		sp.channels.Store(key, channel)
		count++
		return true
	})

	// Clear cache if needed
	if sp.config.CacheEnabled {
		sp.cache.clearIfNeeded()
	}

	sp.logger.Printf("Import complete. Found %d channels", count)
}

func (sp *StreamProxy) tryStreams(channel *Channel, startIndex int, w http.ResponseWriter, r *http.Request) bool {
	channel.mu.RLock()
	streams := channel.Streams
	channel.mu.RUnlock()

	ctx := r.Context()

	for i := 0; i < len(streams); i++ {
		select {
		case <-ctx.Done():
			sp.logger.Printf("Client disconnected, stopping stream attempts")
			return false
		default:
		}

		idx := (startIndex + i) % len(streams)
		stream := streams[idx]

		if sp.tryStream(stream, w, r) {
			return true
		}
	}

	return false
}

func (sp *StreamProxy) tryStream(stream *Stream, w http.ResponseWriter, r *http.Request) bool {
	if atomic.LoadInt32(&stream.Blocked) == 1 {
		sp.logger.Printf("Stream blocked: %s", sp.logURL(stream.URL))
		return false
	}

	// Check connection limit using atomic operations
	if atomic.LoadInt32(&stream.Source.ActiveConns) >= int32(stream.Source.MaxConnections) {
		sp.logger.Printf("Connection limit reached for source: %s", sp.logURL(stream.Source.URL))
		return false
	}

	atomic.AddInt32(&stream.Source.ActiveConns, 1)
	defer atomic.AddInt32(&stream.Source.ActiveConns, -1)

	sp.logger.Printf("Attempting to connect to stream: %s", sp.logURL(stream.URL))

	// Try to connect with retries
	var resp *http.Response
	var err error

	for retry := 0; retry <= sp.config.MaxRetries; retry++ {
		if retry > 0 {
			sp.logger.Printf("Retry %d/%d for stream: %s", retry, sp.config.MaxRetries, sp.logURL(stream.URL))
			time.Sleep(sp.config.RetryDelay)
		}

		// Rate limit
		sp.rateLimiter.Take()

		// Create request with proper timeout
		req, _ := http.NewRequest("GET", stream.URL, nil)
		ctx, cancel := context.WithTimeout(r.Context(), 1*time.Minute)
		defer cancel()
		req = req.WithContext(ctx)

		resp, err = sp.httpClient.Do(req)

		if err == nil && resp.StatusCode == http.StatusOK {
			sp.logger.Printf("Successfully connected to stream: %s", sp.logURL(stream.URL))
			break
		}

		if err != nil {
			sp.logger.Printf("Error connecting to stream: %v", err)
			streamErrors.WithLabelValues("direct", "connection").Inc()
		} else {
			sp.logger.Printf("HTTP %d response from stream: %s", resp.StatusCode, sp.logURL(stream.URL))
			streamErrors.WithLabelValues("direct", fmt.Sprintf("http_%d", resp.StatusCode)).Inc()
			switch resp.StatusCode {
			case 407:
				sp.logger.Printf("Stream requires proxy authentication: %s", sp.logURL(stream.URL))
			case 429:
				sp.logger.Printf("Rate limited (429) on stream: %s", sp.logURL(stream.URL))
				sp.handleStreamFailure(stream)
			}
		}

		if resp != nil {
			resp.Body.Close()
		}
	}

	if err != nil || resp == nil || resp.StatusCode != http.StatusOK {
		sp.handleStreamFailure(stream)
		return false
	}
	defer resp.Body.Close()

	// Check Content-Length to detect empty responses
	contentLength := resp.Header.Get("Content-Length")
	if contentLength == "0" {
		sp.logger.Printf("Stream returned Content-Length: 0, skipping: %s", sp.logURL(stream.URL))
		sp.handleStreamFailure(stream)
		return false
	}

	// Copy response headers
	headerWritten := false
	writeHeaders := func() {
		if !headerWritten {
			for key, values := range resp.Header {
				switch strings.ToLower(key) {
				case "connection", "transfer-encoding", "upgrade", "proxy-authenticate", "proxy-authorization", "te", "trailers":
					continue
				}
				for _, value := range values {
					w.Header().Add(key, value)
				}
			}

			contentType := resp.Header.Get("Content-Type")
			if contentType == "" || contentType == "application/octet-stream" {
				w.Header().Set("Content-Type", "video/mp2t")
			}
			headerWritten = true
		}
	}

	sp.logger.Printf("Starting to stream data from: %s", sp.logURL(stream.URL))
	activeConnections.WithLabelValues("direct").Inc()
	defer activeConnections.WithLabelValues("direct").Dec()

	// Get buffer from pool
	buffer := sp.bufferPool.Get()
	defer sp.bufferPool.Put(buffer)

	totalBytes := int64(0)
	ctx := r.Context()
	firstData := true
	dataTimer := time.NewTimer(5 * time.Second)
	defer dataTimer.Stop()

	for {
		select {
		case <-ctx.Done():
			sp.logger.Printf("Client disconnected, stopping stream")
			return true
		case <-dataTimer.C:
			if firstData && totalBytes == 0 {
				sp.logger.Printf("No data received in 5 seconds from: %s", sp.logURL(stream.URL))
				sp.handleStreamFailure(stream)
				return false
			}
		default:
		}

		n, err := resp.Body.Read(buffer)
		if n > 0 {
			if firstData {
				writeHeaders()
				firstData = false
				dataTimer.Stop()
			}

			select {
			case <-ctx.Done():
				sp.logger.Printf("Client disconnected during write")
				return true
			default:
			}

			_, writeErr := w.Write(buffer[:n])
			if writeErr != nil {
				sp.logger.Printf("Error writing to client: %v", writeErr)
				return true
			}
			totalBytes += int64(n)
			bytesTransferred.WithLabelValues("direct", "upstream").Add(float64(n))

			// Flush if possible
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			} else if crw, ok := w.(*CustomResponseWriter); ok {
				crw.Flush()
			}
		}

		if err != nil {
			if err == io.EOF {
				sp.logger.Printf("Stream ended, transferred %d bytes", totalBytes)
				if totalBytes >= sp.config.MinDataSize {
					return true
				}
				sp.logger.Printf("Stream ended but only transferred %d bytes (minimum: %d)", totalBytes, sp.config.MinDataSize)
			} else if strings.Contains(err.Error(), "context deadline exceeded") {
				sp.logger.Printf("Stream timeout: %v", err)
			} else {
				sp.logger.Printf("Error reading stream: %v", err)
			}

			if totalBytes == 0 {
				sp.handleStreamFailure(stream)
			}
			return false
		}

		if totalBytes >= sp.config.MaxBufferSize {
			sp.logger.Printf("Reached max buffer size (%d bytes)", totalBytes)
			return true
		}
	}
}

func (sp *StreamProxy) startImportRefresh() {
	ticker := time.NewTicker(sp.config.ImportRefreshInterval)
	defer ticker.Stop()

	for range ticker.C {
		sp.logger.Println("Refreshing imports...")
		sp.importStreams()
	}
}

func (sp *StreamProxy) restreamCleanup() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now().Unix()

		sp.channels.Range(func(key, value interface{}) bool {
			channel := value.(*Channel)
			channel.mu.Lock()
			if channel.restreamer != nil && !channel.restreamer.running.Load() {
				// Check if inactive for more than 10 seconds
				lastActivity := channel.restreamer.lastActivity.Load()
				if now-lastActivity > 10 {
					channel.restreamer.cancel()
					channel.restreamer = nil
					sp.logger.Printf("Cleaned up inactive restreamer for channel: %s", channel.Name)
				}
			}
			channel.mu.Unlock()
			return true
		})
	}
}

func (sp *StreamProxy) findChannelBySafeName(safeName string) string {
	// First try exact match after simple space replacement
	simpleName := strings.ReplaceAll(safeName, "_", " ")
	if _, exists := sp.channels.Load(simpleName); exists {
		return simpleName
	}

	// Then try to find by matching sanitized names
	var foundName string
	sp.channels.Range(func(key, _ interface{}) bool {
		name := key.(string)
		if sanitizeChannelName(name) == safeName {
			foundName = name
			return false // Stop iteration
		}
		return true
	})

	if foundName != "" {
		return foundName
	}
	return safeName
}
