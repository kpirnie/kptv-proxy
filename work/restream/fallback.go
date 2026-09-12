package restream

import (
	"io"
	bbuffer "kptv-proxy/work/buffer"
	"kptv-proxy/work/constants"
	"kptv-proxy/work/logger"
	"kptv-proxy/work/types"
	"os"
	"time"
)

// streamFallbackVideo streams the offline video in a loop when all streams fail
func (r *Restream) streamFallbackVideo() {
	// Local path inside container - copy loading.ts here
	fallbackPath := constants.Internal.FallbackVideoPath

	logger.Debug("{restream/restream - streamFallbackVideo} Channel %s: Starting fallback video loop", r.Channel.Name)

	// ensure buffer is valid before attempting fallback streaming —
	// it may have been destroyed by a prior ForceStreamSwitch
	if b := r.LoadBuffer(); b == nil || b.IsDestroyed() {
		bufferSize := r.Config.BufferSizePerStream * 1024 * 1024
		r.StoreBuffer(bbuffer.NewRingBuffer(bufferSize))
		logger.Debug("{restream/restream - streamFallbackVideo} Channel %s: Recreated destroyed buffer for fallback", r.Channel.Name)
	}

	for {
		select {
		case <-r.Context().Done():
			logger.Debug("{restream/restream - streamFallbackVideo} Channel %s: Context cancelled", r.Channel.Name)

			return
		default:
		}

		// Check if we still have clients
		clientCount := 0
		r.Clients.Range(func(key string, value *types.RestreamClient) bool {
			clientCount++
			return true
		})

		if clientCount == 0 {
			logger.Debug("{restream/restream - streamFallbackVideo} Channel %s: No clients remaining", r.Channel.Name)

			return
		}

		logger.Debug("{restream/restream - streamFallbackVideo} Channel %s: Starting fallback video playback for %d clients", r.Channel.Name, clientCount)

		// Stream the local fallback video
		r.streamLocalFallback(fallbackPath)

		// Brief pause before restarting loop
		select {
		case <-r.Context().Done():
			return
		case <-time.After(constants.Internal.FallbackVideoLoopDelay):
			continue
		}
	}
}

// streamLocalFallback streams a local .ts file in a loop
func (r *Restream) streamLocalFallback(filePath string) {
	logger.Debug("{restream/restream - streamLocalFallback} Channel %s: Starting local fallback from %s", r.Channel.Name, filePath)

	// Load fallback video into cache if not already loaded
	fallbackVideoCacheMu.RLock()
	needsLoad := fallbackVideoCachePath != filePath || len(fallbackVideoCache) == 0
	fallbackVideoCacheMu.RUnlock()

	if needsLoad {
		fallbackVideoCacheMu.Lock()
		// Double-check after acquiring write lock
		if fallbackVideoCachePath != filePath || len(fallbackVideoCache) == 0 {
			data, err := os.ReadFile(filePath)
			if err != nil {
				fallbackVideoCacheMu.Unlock()
				logger.Debug("{restream/restream - streamLocalFallback} Channel %s: Failed to load file: %v", r.Channel.Name, err)

				return
			}
			fallbackVideoCache = data
			fallbackVideoCachePath = filePath
			logger.Debug("{restream/restream - streamLocalFallback} Cached fallback video: %d bytes", len(data))

		}
		fallbackVideoCacheMu.Unlock()
	}

	fallbackVideoCacheMu.RLock()
	videoData := fallbackVideoCache
	fallbackVideoCacheMu.RUnlock()

	bufPtr := getStreamBuffer()
	buf := *bufPtr
	defer putStreamBuffer(bufPtr)

	lastActivityUpdate := time.Now()
	totalBytes := int64(0)
	offset := 0

	retryDeadline := time.Now().Add(constants.Internal.FallbackRetryInterval)

	for {
		if time.Now().After(retryDeadline) {
			logger.Debug("{restream/restream - streamFallbackVideo} Channel %s: Fallback period elapsed, returning to retry sources", r.Channel.Name)
			return
		}

		select {
		case <-r.Context().Done():
			logger.Debug("{restream/restream - streamLocalFallback} Channel %s: Context cancelled after %d bytes", r.Channel.Name, totalBytes)

			return
		default:
		}

		// Check if we still have clients
		clientCount := 0
		r.Clients.Range(func(key string, value *types.RestreamClient) bool {
			clientCount++
			return true
		})

		if clientCount == 0 {
			logger.Debug("{restream/restream - streamLocalFallback} Channel %s: No clients remaining", r.Channel.Name)
			return
		}

		// Read from cached memory
		remaining := len(videoData) - offset
		if remaining <= 0 {

			// Loop back to beginning
			offset = 0
			remaining = len(videoData)
			logger.Debug("{restream/restream - streamLocalFallback} Channel %s: Looping fallback video", r.Channel.Name)

		}

		n := copy(buf, videoData[offset:])
		offset += n
		var err error
		if offset >= len(videoData) {
			err = io.EOF
		}

		if n > 0 {
			totalBytes += int64(n)
			chunk := buf[:n]

			if !r.SafeBufferWrite(chunk) {
				logger.Debug("{restream/restream - streamLocalFallback} Channel %s: Buffer write failed", r.Channel.Name)
				return
			}

			activeClients := r.DistributeToClients(chunk)
			if activeClients == 0 {
				logger.Debug("{restream/restream - streamLocalFallback} Channel %s: No active clients after distribute", r.Channel.Name)
				return
			}

			// Update activity timestamp periodically
			now := time.Now()
			if now.Sub(lastActivityUpdate) > constants.Internal.StreamActivityUpdateInterval {
				r.LastActivity.Store(now.Unix())
				lastActivityUpdate = now
			}

			// Throttle to approximate real-time playback at the configured pace
			time.Sleep(time.Duration(n) * time.Second / time.Duration(constants.Internal.FallbackVideoPaceBytesPerSec))
		}

		if err != nil {
			if err == io.EOF {
				// Already handled by offset reset above
				continue
			}
		}
	}
}
