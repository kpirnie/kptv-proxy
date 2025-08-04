package proxy

import (
	"context"
	"fmt"
	"io"
	"kptv-proxy/work/buffer"
	"kptv-proxy/work/cache"
	"kptv-proxy/work/client"
	"kptv-proxy/work/config"
	"kptv-proxy/work/metrics"
	"kptv-proxy/work/parser"
	"kptv-proxy/work/restream"
	streamAlias "kptv-proxy/work/stream"
	"kptv-proxy/work/types"
	"kptv-proxy/work/utils"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/VictoriaMetrics/fastcache"
	"github.com/panjf2000/ants/v2"
	"go.uber.org/ratelimit"
)

// StreamProxy is the main application struct
type StreamProxy struct {
	Config       *config.Config
	Channels     sync.Map // Use sync.Map for concurrent access
	Cache        *cache.Cache
	SegmentCache *fastcache.Cache
	Logger       *log.Logger
	BufferPool   *buffer.BufferPool
	HttpClient   *client.HeaderSettingClient
	WorkerPool   *ants.Pool
	RateLimiter  ratelimit.Limiter
}

func New(cfg *config.Config, logger *log.Logger, bufferPool *buffer.BufferPool, httpClient *client.HeaderSettingClient, workerPool *ants.Pool, rateLimiter ratelimit.Limiter, segmentCache *fastcache.Cache, cache *cache.Cache) *StreamProxy {
	return &StreamProxy{
		Config:       cfg,
		Channels:     sync.Map{}, // Initialize empty sync.Map
		Cache:        cache,
		SegmentCache: segmentCache,
		Logger:       logger,
		BufferPool:   bufferPool,
		HttpClient:   httpClient,
		WorkerPool:   workerPool,
		RateLimiter:  rateLimiter,
	}
}

func (sp *StreamProxy) ImportStreams() {
	sp.Logger.Println("Starting stream import...")

	if len(sp.Config.Sources) == 0 {
		sp.Logger.Println("WARNING: No sources configured!")
		return
	}

	var wg sync.WaitGroup
	newChannels := sync.Map{}

	for i := range sp.Config.Sources {
		wg.Add(1)
		source := &sp.Config.Sources[i]

		err := sp.WorkerPool.Submit(func() {
			defer wg.Done()
			sp.RateLimiter.Take() // Rate limit imports

			streams := parser.ParseM3U8(sp.HttpClient, sp.Logger, sp.Config, source)
			for _, stream := range streams {
				channelName := stream.Name
				actual, _ := newChannels.LoadOrStore(channelName, &types.Channel{
					Name:    channelName,
					Streams: []*types.Stream{},
				})
				channel := actual.(*types.Channel)
				channel.Mu.Lock()
				channel.Streams = append(channel.Streams, stream)
				channel.Mu.Unlock()
			}
		})

		if err != nil {
			sp.Logger.Printf("Worker pool error: %v", err)
		}
	}

	wg.Wait()

	// Sort streams in each channel and migrate to main map
	count := 0
	newChannels.Range(func(key, value interface{}) bool {
		channel := value.(*types.Channel)
		parser.SortStreams(channel.Streams, sp.Config)
		sp.Channels.Store(key, channel)
		count++
		return true
	})

	// Clear cache if needed
	if sp.Config.CacheEnabled {
		sp.Cache.ClearIfNeeded()
	}

	sp.Logger.Printf("Import complete. Found %d channels", count)

}
func (sp *StreamProxy) GeneratePlaylist(w http.ResponseWriter, r *http.Request, groupFilter string) {
	if groupFilter == "" {
		sp.Logger.Println("Handling playlist request (all groups)")
	} else {
		sp.Logger.Printf("Handling playlist request for group: %s", groupFilter)
	}

	// Rate limit
	sp.RateLimiter.Take()

	// Create cache key
	cacheKey := "playlist"
	if groupFilter != "" {
		cacheKey = "playlist_" + strings.ToLower(groupFilter)
	}

	// Check cache
	if sp.Config.CacheEnabled {
		if cached, ok := sp.Cache.GetM3U8(cacheKey); ok {
			w.Header().Set("Content-Type", "application/x-mpegURL")
			w.Header().Set("Cache-Control", "no-cache")
			w.Write([]byte(cached))
			return
		}
	}

	var playlist strings.Builder
	playlist.WriteString("#EXTM3U\n")

	filteredCount := 0
	totalCount := 0

	// Iterate through channels
	sp.Channels.Range(func(key, value interface{}) bool {
		channelName := key.(string)
		channel := value.(*types.Channel)
		totalCount++

		channel.Mu.RLock()
		if len(channel.Streams) > 0 {
			// Use attributes from first stream
			attrs := channel.Streams[0].Attributes

			// Apply group filter if specified
			if groupFilter != "" {
				channelGroup := sp.GetChannelGroup(attrs)
				if !strings.EqualFold(channelGroup, groupFilter) {
					channel.Mu.RUnlock()
					return true // Continue to next channel
				}
			}

			filteredCount++

			playlist.WriteString("#EXTINF:-1")
			for key, value := range attrs {
				if key != "tvg-name" && key != "duration" {
					// Ensure values are properly quoted if they contain special characters
					if strings.ContainsAny(value, ",\"") {
						value = fmt.Sprintf("%q", value)
					}
					playlist.WriteString(fmt.Sprintf(" %s=\"%s\"", key, value))
				}
			}

			// Clean channel name for display
			cleanName := strings.Trim(channelName, "\"")
			playlist.WriteString(fmt.Sprintf(",%s\n", cleanName))

			// Generate proxy URL
			safeName := utils.SanitizeChannelName(channelName)
			proxyURL := fmt.Sprintf("%s/stream/%s", sp.Config.BaseURL, safeName)
			playlist.WriteString(proxyURL + "\n")
		}
		channel.Mu.RUnlock()
		return true
	})

	result := playlist.String()

	// Cache result
	if sp.Config.CacheEnabled {
		sp.Cache.SetM3U8(cacheKey, result)
	}

	w.Header().Set("Content-Type", "application/x-mpegURL")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write([]byte(result))

	if groupFilter == "" {
		sp.Logger.Printf("Generated playlist with %d channels", totalCount)
	} else {
		sp.Logger.Printf("Generated playlist for group '%s' with %d channels (out of %d total)", groupFilter, filteredCount, totalCount)
	}

}
func (sp *StreamProxy) GetChannelGroup(attrs map[string]string) string {
	// Check for tvg-group first
	if group, exists := attrs["tvg-group"]; exists && group != "" {
		return group
	}

	// Fall back to group-title
	if group, exists := attrs["group-title"]; exists && group != "" {
		return group
	}

	return ""

}
func (sp *StreamProxy) TryStreams(channel *types.Channel, startIndex int, w http.ResponseWriter, r *http.Request) bool {
	channel.Mu.RLock()
	streams := channel.Streams
	channel.Mu.RUnlock()

	ctx := r.Context()

	for i := 0; i < len(streams); i++ {
		select {
		case <-ctx.Done():
			sp.Logger.Printf("Client disconnected, stopping stream attempts")
			return false
		default:
		}

		idx := (startIndex + i) % len(streams)
		stream := streams[idx]

		if sp.TryStream(stream, w, r) {
			return true
		}
	}

	return false

}
func (sp *StreamProxy) TryStream(stream *types.Stream, w http.ResponseWriter, r *http.Request) bool {
	if atomic.LoadInt32(&stream.Blocked) == 1 {
		sp.Logger.Printf("Stream blocked: %s", utils.LogURL(sp.Config, stream.URL))
		return false
	}

	// Check connection limit using atomic operations
	if atomic.LoadInt32(&stream.Source.ActiveConns) >= int32(stream.Source.MaxConnections) {
		sp.Logger.Printf("Connection limit reached for source: %s", utils.LogURL(sp.Config, stream.Source.URL))
		return false
	}

	atomic.AddInt32(&stream.Source.ActiveConns, 1)
	defer atomic.AddInt32(&stream.Source.ActiveConns, -1)

	sp.Logger.Printf("Attempting to connect to stream: %s", utils.LogURL(sp.Config, stream.URL))

	// Try to connect with retries
	var resp *http.Response
	var err error

	for retry := 0; retry <= sp.Config.MaxRetries; retry++ {
		if retry > 0 {
			sp.Logger.Printf("Retry %d/%d for stream: %s", retry, sp.Config.MaxRetries, utils.LogURL(sp.Config, stream.URL))
			time.Sleep(sp.Config.RetryDelay)
		}

		// Rate limit
		sp.RateLimiter.Take()

		// Create request with proper timeout
		req, _ := http.NewRequest("GET", stream.URL, nil)
		ctx, cancel := context.WithTimeout(r.Context(), 1*time.Minute)
		defer cancel()
		req = req.WithContext(ctx)

		resp, err = sp.HttpClient.Do(req)

		if err == nil && resp.StatusCode == http.StatusOK {
			sp.Logger.Printf("Successfully connected to stream: %s", utils.LogURL(sp.Config, stream.URL))
			break
		}

		if err != nil {
			sp.Logger.Printf("Error connecting to stream: %v", err)
			metrics.StreamErrors.WithLabelValues("direct", "connection").Inc()
		} else {
			sp.Logger.Printf("HTTP %d response from stream: %s", resp.StatusCode, utils.LogURL(sp.Config, stream.URL))
			metrics.StreamErrors.WithLabelValues("direct", fmt.Sprintf("http_%d", resp.StatusCode)).Inc()
			switch resp.StatusCode {
			case 407:
				sp.Logger.Printf("Stream requires proxy authentication: %s", utils.LogURL(sp.Config, stream.URL))
			case 429:
				sp.Logger.Printf("Rate limited (429) on stream: %s", utils.LogURL(sp.Config, stream.URL))
				streamAlias.HandleStreamFailure(stream, sp.Config, sp.Logger)
			}
		}

		if resp != nil {
			resp.Body.Close()
		}
	}

	if err != nil || resp == nil || resp.StatusCode != http.StatusOK {
		streamAlias.HandleStreamFailure(stream, sp.Config, sp.Logger)
		return false
	}
	defer resp.Body.Close()

	// Check Content-Length to detect empty responses
	contentLength := resp.Header.Get("Content-Length")
	if contentLength == "0" {
		sp.Logger.Printf("Stream returned Content-Length: 0, skipping: %s", utils.LogURL(sp.Config, stream.URL))
		streamAlias.HandleStreamFailure(stream, sp.Config, sp.Logger)
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

	sp.Logger.Printf("Starting to stream data from: %s", utils.LogURL(sp.Config, stream.URL))
	metrics.ActiveConnections.WithLabelValues("direct").Inc()
	defer metrics.ActiveConnections.WithLabelValues("direct").Dec()

	// Get buffer from pool
	buffer := sp.BufferPool.Get()
	defer sp.BufferPool.Put(buffer)

	totalBytes := int64(0)
	ctx := r.Context()
	firstData := true
	dataTimer := time.NewTimer(5 * time.Second)
	defer dataTimer.Stop()

	for {
		select {
		case <-ctx.Done():
			sp.Logger.Printf("Client disconnected, stopping stream")
			return true
		case <-dataTimer.C:
			if firstData && totalBytes == 0 {
				sp.Logger.Printf("No data received in 5 seconds from: %s", utils.LogURL(sp.Config, stream.URL))
				streamAlias.HandleStreamFailure(stream, sp.Config, sp.Logger)
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
				sp.Logger.Printf("Client disconnected during write")
				return true
			default:
			}

			_, writeErr := w.Write(buffer[:n])
			if writeErr != nil {
				sp.Logger.Printf("Error writing to client: %v", writeErr)
				return true
			}
			totalBytes += int64(n)
			metrics.BytesTransferred.WithLabelValues("direct", "upstream").Add(float64(n))

			// Flush if possible
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			} else if crw, ok := w.(*client.CustomResponseWriter); ok {
				crw.Flush()
			}
		}

		if err != nil {
			if err == io.EOF {
				sp.Logger.Printf("Stream ended, transferred %d bytes", totalBytes)
				if totalBytes >= sp.Config.MinDataSize {
					return true
				}
				sp.Logger.Printf("Stream ended but only transferred %d bytes (minimum: %d)", totalBytes, sp.Config.MinDataSize)
			} else if strings.Contains(err.Error(), "context deadline exceeded") {
				sp.Logger.Printf("Stream timeout: %v", err)
			} else {
				sp.Logger.Printf("Error reading stream: %v", err)
			}

			if totalBytes == 0 {
				streamAlias.HandleStreamFailure(stream, sp.Config, sp.Logger)
			}
			return false
		}

		if totalBytes >= sp.Config.MaxBufferSize {
			sp.Logger.Printf("Reached max buffer size (%d bytes)", totalBytes)
			return true
		}
	}

}
func (sp *StreamProxy) StartImportRefresh() {
	ticker := time.NewTicker(sp.Config.ImportRefreshInterval)
	defer ticker.Stop()

	for range ticker.C {
		sp.Logger.Println("Refreshing imports...")
		sp.ImportStreams()
	}

}
func (sp *StreamProxy) RestreamCleanup() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now().Unix()

		sp.Channels.Range(func(key, value interface{}) bool {
			channel := value.(*types.Channel)
			channel.Mu.Lock()
			if channel.Restreamer != nil && !channel.Restreamer.Running.Load() {
				// Check if inactive for more than 10 seconds
				lastActivity := channel.Restreamer.LastActivity.Load()
				if now-lastActivity > 10 {
					channel.Restreamer.Cancel()
					channel.Restreamer = nil
					sp.Logger.Printf("Cleaned up inactive restreamer for channel: %s", channel.Name)
				}
			}
			channel.Mu.Unlock()
			return true
		})
	}

}
func (sp *StreamProxy) FindChannelBySafeName(safeName string) string {
	// First try exact match after simple space replacement
	simpleName := strings.ReplaceAll(safeName, "_", " ")
	if _, exists := sp.Channels.Load(simpleName); exists {
		return simpleName
	}

	// Then try to find by matching sanitized names
	var foundName string
	sp.Channels.Range(func(key, _ interface{}) bool {
		name := key.(string)
		if utils.SanitizeChannelName(name) == safeName {
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
func (sp *StreamProxy) HandleRestreamingClient(w http.ResponseWriter, r *http.Request, channel *types.Channel) {
	sp.Logger.Printf("Starting restreaming client for channel: %s", channel.Name)

	channel.Mu.Lock()
	var restreamer *restream.Restream
	if channel.Restreamer == nil {
		sp.Logger.Printf("Creating new restreamer for channel: %s", channel.Name)
		restreamer = restream.NewRestreamer(channel, sp.Config.MaxBufferSize, sp.Logger, sp.HttpClient, sp.RateLimiter, sp.Config)
		channel.Restreamer = restreamer.Restreamer // Store the underlying Restreamer
	} else {
		// Wrap the existing Restreamer to access its methods
		restreamer = &restream.Restream{Restreamer: channel.Restreamer}
	}
	channel.Mu.Unlock()

	// Generate client ID
	clientID := fmt.Sprintf("%s-%d", r.RemoteAddr, time.Now().UnixNano())
	sp.Logger.Printf("New client connected: %s for channel: %s", clientID, channel.Name)

	// Set headers before checking for flusher
	w.Header().Set("Content-Type", "video/mp2t")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Get flusher - use the underlying ResponseWriter if it's a CustomResponseWriter
	var flusher http.Flusher
	var ok bool

	if crw, isCustom := w.(*client.CustomResponseWriter); isCustom {
		flusher, ok = crw.ResponseWriter.(http.Flusher)
	} else {
		flusher, ok = w.(http.Flusher)
	}

	if !ok {
		sp.Logger.Printf("Streaming not supported for client: %s", clientID)
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	// Write headers
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	restreamer.AddClient(clientID, w, flusher)
	defer restreamer.RemoveClient(clientID)

	sp.Logger.Printf("Client %s added to restreamer, waiting for disconnect", clientID)

	// Wait for client disconnect
	<-r.Context().Done()
	sp.Logger.Printf("Restreaming client disconnected: %s", clientID)

}
