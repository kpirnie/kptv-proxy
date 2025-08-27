package watcher

import (
	"context"
	"encoding/json"
	"fmt"
	"kptv-proxy/work/restream"
	"kptv-proxy/work/types"
	"kptv-proxy/work/utils"
	"log"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type WatcherManager struct {
	watchers sync.Map // map[string]*StreamWatcher
	logger   *log.Logger
	enabled  atomic.Bool
	stopChan chan struct{}
}

type StreamWatcher struct {
	channelName         string
	restreamer          *types.Restreamer
	logger              *log.Logger
	ctx                 context.Context
	cancel              context.CancelFunc
	lastCheck           time.Time
	lastStreamStart     time.Time
	consecutiveFailures int32
	totalFailures       int32
	lastFailureReset    time.Time
	running             atomic.Bool
	ffprobeCheckCount   int32
}

type RestreamWrapper struct {
	*types.Restreamer
}

func NewWatcherManager(logger *log.Logger) *WatcherManager {
	return &WatcherManager{
		logger:   logger,
		stopChan: make(chan struct{}),
	}
}

func (wm *WatcherManager) Start() {
	if !wm.enabled.CompareAndSwap(false, true) {
		return
	}

	wm.logger.Printf("[WATCHER] Stream Watcher Manager started")
	go wm.cleanupRoutine()
}

func (wm *WatcherManager) Stop() {
	if !wm.enabled.CompareAndSwap(true, false) {
		return
	}

	close(wm.stopChan)

	// Stop all active watchers
	wm.watchers.Range(func(key, value interface{}) bool {
		watcher := value.(*StreamWatcher)
		watcher.Stop()
		return true
	})

	wm.logger.Printf("[WATCHER] Stream Watcher Manager stopped")
}

func (wm *WatcherManager) StartWatching(channelName string, restreamer *types.Restreamer) {
	// Stop any existing watcher for this channel
	if existing, exists := wm.watchers.LoadAndDelete(channelName); exists {
		existingWatcher := existing.(*StreamWatcher)
		existingWatcher.Stop()
	}

	// Create new watcher
	watcher := &StreamWatcher{
		channelName:      channelName,
		restreamer:       restreamer,
		logger:           wm.logger,
		lastCheck:        time.Now(),
		lastStreamStart:  time.Now(),
		lastFailureReset: time.Now(),
	}

	watcher.ctx, watcher.cancel = context.WithCancel(context.Background())
	wm.watchers.Store(channelName, watcher)

	// Start watching
	go watcher.Watch()

	currentIdx := int(atomic.LoadInt32(&restreamer.CurrentIndex))
	restreamer.Channel.Mu.RLock()
	var streamURL string
	if currentIdx < len(restreamer.Channel.Streams) {
		streamURL = restreamer.Channel.Streams[currentIdx].URL
	}
	restreamer.Channel.Mu.RUnlock()

	wm.logger.Printf("[WATCHER] Started watching channel %s (stream %d): %s",
		channelName, currentIdx, utils.LogURL(restreamer.Config, streamURL))
}

func (wm *WatcherManager) StopWatching(channelName string) {
	if watcher, exists := wm.watchers.LoadAndDelete(channelName); exists {
		w := watcher.(*StreamWatcher)
		w.Stop()
		wm.logger.Printf("[WATCHER] Stopped watching channel %s", channelName)
	}
}

func (wm *WatcherManager) cleanupRoutine() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-wm.stopChan:
			return
		case <-ticker.C:
			wm.watchers.Range(func(key, value interface{}) bool {
				watcher := value.(*StreamWatcher)

				// Check if restreamer is still running
				if !watcher.restreamer.Running.Load() {
					watcher.Stop()
					wm.watchers.Delete(key)
				}

				return true
			})
		}
	}
}

func (sw *StreamWatcher) Watch() {
	if !sw.running.CompareAndSwap(false, true) {
		return
	}

	defer sw.running.Store(false)

	// Increase default interval to 60 seconds for less aggressive monitoring
	interval := 30 * time.Second

	// Use debug mode for more frequent checks if needed
	if sw.restreamer.Config.Debug {
		interval = 15 * time.Second
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	sw.logger.Printf("[WATCHER] Channel %s: Monitoring every %v", sw.channelName, interval)

	for {
		select {
		case <-sw.ctx.Done():
			return
		case <-ticker.C:
			if !sw.restreamer.Running.Load() {
				return
			}

			sw.checkStreamHealth()
		}
	}
}

func (sw *StreamWatcher) Stop() {
	if sw.cancel != nil {
		sw.cancel()
	}
}

func (sw *StreamWatcher) checkStreamHealth() {
	startTime := time.Now()

	// Use existing restreamer state - NO additional network requests
	hasIssues := sw.evaluateStreamHealthFromState()

	checkDuration := time.Since(startTime)

	// Always log health check results when debug is enabled
	if sw.restreamer.Config.Debug {
		lastActivity := sw.restreamer.LastActivity.Load()
		timeSinceActivity := time.Now().Unix() - lastActivity

		clientCount := 0
		sw.restreamer.Clients.Range(func(_, _ interface{}) bool {
			clientCount++
			return true
		})

		totalFails := atomic.LoadInt32(&sw.totalFailures)
		consecFails := atomic.LoadInt32(&sw.consecutiveFailures)

		sw.logger.Printf("[WATCHER] Channel %s: Health=%v, Activity=%ds ago, Clients=%d, TotalFails=%d, ConsecFails=%d, Check=%v",
			sw.channelName, !hasIssues, timeSinceActivity, clientCount, totalFails, consecFails, checkDuration)
	}

	if hasIssues {
		consecutiveFailures := atomic.AddInt32(&sw.consecutiveFailures, 1)
		totalFailures := atomic.AddInt32(&sw.totalFailures, 1)

		sw.logger.Printf("[WATCHER] Channel %s: Health issue detected (consecutive: %d/5, total: %d/3)",
			sw.channelName, consecutiveFailures, totalFailures)

		// Trigger failover if EITHER condition is met:
		// 1. 5 consecutive failures (immediate serious issues)
		// 2. 3 total failures (pattern of instability)
		if consecutiveFailures >= 3 || totalFailures >= 3 {
			reason := "consecutive_failures"
			if totalFailures >= 3 {
				reason = "total_failures"
			}
			sw.triggerStreamSwitch(reason)

			// Reset both counters after switching
			atomic.StoreInt32(&sw.consecutiveFailures, 0)
			atomic.StoreInt32(&sw.totalFailures, 0)
			sw.lastFailureReset = time.Now()
		}
	} else {
		// Reset consecutive failures on healthy stream
		oldConsecutiveFailures := atomic.SwapInt32(&sw.consecutiveFailures, 0)

		// Reset total failures if we've had a good period (10 minutes of health)
		if time.Since(sw.lastFailureReset) > 10*time.Minute {
			oldTotalFailures := atomic.SwapInt32(&sw.totalFailures, 0)
			sw.lastFailureReset = time.Now()

			if (oldConsecutiveFailures > 0 || oldTotalFailures > 0) && sw.restreamer.Config.Debug {
				sw.logger.Printf("[WATCHER] Channel %s: Long-term health recovered, reset %d consecutive + %d total failures",
					sw.channelName, oldConsecutiveFailures, oldTotalFailures)
			}
		} else if oldConsecutiveFailures > 0 && sw.restreamer.Config.Debug {
			totalFails := atomic.LoadInt32(&sw.totalFailures)
			sw.logger.Printf("[WATCHER] Channel %s: Consecutive failures reset, but %d total failures remain",
				sw.channelName, totalFails)
		}
	}

	sw.lastCheck = time.Now()
}

func (sw *StreamWatcher) evaluateStreamHealthFromState() bool {

	// Don't check health for first 30 seconds after stream start
	if time.Since(sw.lastStreamStart) < 30*time.Second {
		if sw.restreamer.Config.Debug {
			sw.logger.Printf("[WATCHER] Channel %s: In grace period, skipping health check",
				sw.channelName)
		}
		return false
	}

	hasIssues := false

	// 1. Check buffer health first (no network requests)
	if sw.restreamer.Buffer != nil && sw.restreamer.Buffer.IsDestroyed() {
		if sw.restreamer.Config.Debug {
			sw.logger.Printf("[WATCHER] Channel %s: Buffer destroyed", sw.channelName)
		}
		hasIssues = true
	}

	// 2. Check for extended inactivity
	lastActivity := sw.restreamer.LastActivity.Load()
	timeSinceActivity := time.Now().Unix() - lastActivity

	if timeSinceActivity > 60 { // 1 minute
		if sw.restreamer.Config.Debug {
			sw.logger.Printf("[WATCHER] Channel %s: No activity for %d seconds",
				sw.channelName, timeSinceActivity)
		}
		hasIssues = true
	}

	// 3. Check context state (no network requests)
	if sw.restreamer.Running.Load() {
		select {
		case <-sw.restreamer.Ctx.Done():
			// Give some grace time after context cancellation
			if time.Since(sw.lastCheck) > 60*time.Second {
				if sw.restreamer.Config.Debug {
					sw.logger.Printf("[WATCHER] Channel %s: Context cancelled and running for >60s",
						sw.channelName)
				}
				hasIssues = true
			}
		default:
		}
	}

	// 4. Perform ffprobe analysis (only every 3rd check to avoid being too aggressive)
	if sw.shouldRunFFProbeCheck() {
		streamHealth := sw.analyzeStreamWithFFProbe()
		if sw.evaluateFFProbeResults(streamHealth) {
			hasIssues = true
		}
	}

	return hasIssues
}

func (sw *StreamWatcher) analyzeStreamWithFFProbe() types.StreamHealthData {
	health := types.StreamHealthData{}

	// Get some data from the existing stream buffer instead of opening new connection
	streamData := sw.getStreamDataFromBuffer()
	if len(streamData) == 0 {
		if sw.restreamer.Config.Debug {
			sw.logger.Printf("[WATCHER] No stream data available in buffer for ffprobe analysis")
		}
		return health
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Use ffprobe with stdin instead of URL to reuse existing connection data
	cmd := exec.CommandContext(ctx, "ffprobe",
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		"-analyzeduration", "3M",
		"-probesize", "3M",
		"-i", "pipe:0") // Read from stdin instead of URL

	// Create stdin pipe
	stdin, err := cmd.StdinPipe()
	if err != nil {
		if sw.restreamer.Config.Debug {
			sw.logger.Printf("[WATCHER] Failed to create stdin pipe for ffprobe: %v", err)
		}
		return health
	}

	// Start the command
	if err := cmd.Start(); err != nil {
		stdin.Close()
		if sw.restreamer.Config.Debug {
			sw.logger.Printf("[WATCHER] Failed to start ffprobe: %v", err)
		}
		return health
	}

	// Write stream data to ffprobe stdin
	go func() {
		defer stdin.Close()
		_, writeErr := stdin.Write(streamData)
		if writeErr != nil && sw.restreamer.Config.Debug {
			sw.logger.Printf("[WATCHER] Error writing to ffprobe stdin: %v", writeErr)
		}
	}()

	// Get the output
	output, err := cmd.Output()
	if err != nil {
		if sw.restreamer.Config.Debug {
			sw.logger.Printf("[WATCHER] ffprobe analysis failed for %s: %v", sw.channelName, err)
		}
		return health
	}

	// Parse the same way as before...
	var result struct {
		Format struct {
			BitRate string `json:"bit_rate"`
		} `json:"format"`
		Streams []struct {
			CodecType  string `json:"codec_type"`
			CodecName  string `json:"codec_name"`
			Width      int    `json:"width"`
			Height     int    `json:"height"`
			RFrameRate string `json:"r_frame_rate"`
		} `json:"streams"`
	}

	if err := json.Unmarshal(output, &result); err != nil {
		return health
	}

	// Analyze streams (same as before)
	for _, stream := range result.Streams {
		switch stream.CodecType {
		case "video":
			health.HasVideo = stream.Width > 0 && stream.Height > 0
			if health.HasVideo {
				health.Resolution = fmt.Sprintf("%dx%d", stream.Width, stream.Height)
				if stream.RFrameRate != "" && stream.RFrameRate != "0/0" {
					health.FPS = sw.parseFrameRate(stream.RFrameRate)
				}
			}
		case "audio":
			health.HasAudio = stream.CodecName != ""
		}
	}

	// Parse bitrate
	if result.Format.BitRate != "" {
		if bitrate, err := strconv.ParseInt(result.Format.BitRate, 10, 64); err == nil {
			health.Bitrate = bitrate
		}
	}

	health.Valid = true
	if sw.restreamer.Config.Debug {
		sw.logger.Printf("[WATCHER] ffprobe analysis using existing stream data: Video=%v, Audio=%v, Bitrate=%d",
			health.HasVideo, health.HasAudio, health.Bitrate)
	}

	return health
}

func (sw *StreamWatcher) parseFrameRate(frameRate string) float64 {
	parts := strings.Split(frameRate, "/")
	if len(parts) != 2 {
		return 0
	}

	num, err1 := strconv.ParseFloat(parts[0], 64)
	den, err2 := strconv.ParseFloat(parts[1], 64)

	if err1 != nil || err2 != nil || den == 0 {
		return 0
	}

	return num / den
}

func (sw *StreamWatcher) evaluateFFProbeResults(health types.StreamHealthData) bool {
	if !health.Valid {
		return false // Don't consider ffprobe failures as issues
	}

	hasIssues := false

	// Check for video loss (most critical)
	if !health.HasVideo {
		if sw.restreamer.Config.Debug {
			sw.logger.Printf("[WATCHER] Channel %s: No video stream detected", sw.channelName)
		}
		hasIssues = true
	}

	// Check for bitrate issues
	if health.Bitrate == 0 {
		if sw.restreamer.Config.Debug {
			sw.logger.Printf("[WATCHER] Channel %s: No bitrate detected (stream may be problematic)",
				sw.channelName)
		}
		hasIssues = true
	} else if health.Bitrate < 50000 {
		if sw.restreamer.Config.Debug {
			sw.logger.Printf("[WATCHER] Channel %s: Bitrate too low (%d bps)",
				sw.channelName, health.Bitrate)
		}
		hasIssues = true
	}

	// Log healthy stream info in debug mode
	if !hasIssues && sw.restreamer.Config.Debug {
		sw.logger.Printf("[WATCHER] Channel %s: Stream healthy - Video=%v, Audio=%v, Bitrate=%d, Resolution=%s",
			sw.channelName, health.HasVideo, health.HasAudio, health.Bitrate, health.Resolution)
	}

	return hasIssues
}

func (sw *StreamWatcher) shouldRunFFProbeCheck() bool {

	// Run ffprobe every 3rd health check to avoid being too aggressive
	count := atomic.AddInt32(&sw.ffprobeCheckCount, 1)
	return count%2 == 0
}

func (sw *StreamWatcher) triggerStreamSwitch(reason string) {
	if sw.restreamer.Config.Debug {
		sw.logger.Printf("[WATCHER] Triggering stream switch for channel %s due to: %s",
			sw.channelName, reason)
	}

	// Find next available stream (reuse existing logic)
	nextIndex := sw.findNextAvailableStream()
	if nextIndex == -1 {
		if sw.restreamer.Config.Debug {
			sw.logger.Printf("[WATCHER] No alternative streams found for channel %s",
				sw.channelName)
		}
		return
	}

	// Check if we have clients before switching
	clientCount := 0
	sw.restreamer.Clients.Range(func(_, _ interface{}) bool {
		clientCount++
		return true
	})

	if clientCount == 0 {
		if sw.restreamer.Config.Debug {
			sw.logger.Printf("[WATCHER] No clients connected for channel %s, skipping switch",
				sw.channelName)
		}
		return
	}

	if sw.restreamer.Config.Debug {
		sw.logger.Printf("[WATCHER] Switching channel %s to stream %d for %d clients",
			sw.channelName, nextIndex, clientCount)
	}

	// Use existing stream switching mechanism
	sw.forceStreamRestart(nextIndex)
}

func (sw *StreamWatcher) forceStreamRestart(newIndex int) {

	// Set preferred stream index
	atomic.StoreInt32(&sw.restreamer.Channel.PreferredStreamIndex, int32(newIndex))
	atomic.StoreInt32(&sw.restreamer.CurrentIndex, int32(newIndex))

	// Stop current streaming if running
	if sw.restreamer.Running.Load() {
		sw.restreamer.Running.Store(false)
		sw.restreamer.Cancel()

		// Brief pause to allow cleanup
		time.Sleep(200 * time.Millisecond)
	}

	// Create new context for the restreamer
	ctx, cancel := context.WithCancel(context.Background())
	sw.restreamer.Ctx = ctx
	sw.restreamer.Cancel = cancel

	// Reset buffer if it exists
	if sw.restreamer.Buffer != nil && !sw.restreamer.Buffer.IsDestroyed() {
		sw.restreamer.Buffer.Reset()
	}

	// Check if we still have clients
	clientCount := 0
	sw.restreamer.Clients.Range(func(_, _ interface{}) bool {
		clientCount++
		return true
	})

	if clientCount > 0 {

		// Start new streaming using existing Stream() method
		sw.restreamer.Running.Store(true)
		sw.lastStreamStart = time.Now() // Add this line
		go sw.restartWithExistingLogic()
	}

	// Reset failure count for new stream
	atomic.StoreInt32(&sw.consecutiveFailures, 0)
	atomic.StoreInt32(&sw.totalFailures, 0)
	sw.lastFailureReset = time.Now()
}

func (sw *StreamWatcher) restartWithExistingLogic() {
	defer func() {
		if rec := recover(); rec != nil {
			if sw.restreamer.Config.Debug {
				sw.logger.Printf("[WATCHER_RESTART_PANIC] Channel %s: Recovered from panic: %v",
					sw.channelName, rec)
			}
		}
		sw.restreamer.Running.Store(false)
	}()

	if sw.restreamer.Config.Debug {
		currentIdx := int(atomic.LoadInt32(&sw.restreamer.CurrentIndex))
		sw.logger.Printf("[WATCHER_RESTART] Channel %s: Starting stream restart at index %d",
			sw.channelName, currentIdx)
	}

	// Call the existing Stream() method which handles all connection management,
	// source selection, master playlist processing, etc.
	r := NewRestreamWrapper(sw.restreamer)
	r.Stream()
}

func (sw *StreamWatcher) findNextAvailableStream() int {
	sw.restreamer.Channel.Mu.RLock()
	defer sw.restreamer.Channel.Mu.RUnlock()

	streamCount := len(sw.restreamer.Channel.Streams)
	if streamCount <= 1 {
		return -1
	}

	currentIdx := int(atomic.LoadInt32(&sw.restreamer.CurrentIndex))

	// Try next stream in order
	for i := 1; i < streamCount; i++ {
		nextIndex := (currentIdx + i) % streamCount
		stream := sw.restreamer.Channel.Streams[nextIndex]

		// Skip blocked streams
		if atomic.LoadInt32(&stream.Blocked) == 1 {
			continue
		}

		// Check if source has available connections (don't actually use them)
		currentConns := atomic.LoadInt32(&stream.Source.ActiveConns)
		if currentConns >= int32(stream.Source.MaxConnections) {
			if sw.restreamer.Config.Debug {
				sw.logger.Printf("[WATCHER] Source at connection limit (%d/%d), skipping: %s",
					currentConns, stream.Source.MaxConnections, stream.Source.Name)
			}
			continue
		}

		return nextIndex
	}

	return -1
}

// RestreamWrapper wraps the existing restreamer to call the Stream() method
func NewRestreamWrapper(restreamer *types.Restreamer) *RestreamWrapper {
	return &RestreamWrapper{
		Restreamer: restreamer,
	}
}

// Stream method that reuses the existing streaming logic
func (rw *RestreamWrapper) Stream() {
	// Use the existing complete Stream() method
	r := &restream.Restream{Restreamer: rw.Restreamer}
	r.WatcherStream()
}

func (sw *StreamWatcher) getStreamDataFromBuffer() []byte {
	if sw.restreamer.Buffer == nil || sw.restreamer.Buffer.IsDestroyed() {
		return nil
	}

	// Get 3MB of recent data for ffprobe analysis
	return sw.restreamer.Buffer.PeekRecentData(3 * 1024 * 1024)
}
