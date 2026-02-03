package watcher

import (
	"context"
	"encoding/json"
	"fmt"
	"kptv-proxy/work/logger"
	"kptv-proxy/work/restream"
	"kptv-proxy/work/types"
	"kptv-proxy/work/utils"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/puzpuzpuz/xsync/v3"
)

// semaphore to limit concurrent watchers system-wide
var (
	watcherSemaphore = make(chan struct{}, 100) // Max 100 concurrent watchers
)

/**
 * WatcherManager coordinates multiple stream watchers across all active channels,
 * providing centralized management for stream health monitoring, automatic failover,
 * and resource cleanup.
 *
 * The manager implements a sophisticated monitoring system that tracks stream quality,
 * detects degradation patterns, and triggers intelligent failover operations to maintain
 * optimal user experience during stream issues.
 *
 * Key responsibilities:
 *   - Lifecycle management for individual stream watchers
 *   - Coordination of health monitoring across multiple channels
 *   - Automatic cleanup of watchers for terminated streams
 *   - Resource management and memory optimization for long-running operations
 *   - Integration with restreaming infrastructure for seamless failover
 */
type WatcherManager struct {
	watchers *xsync.MapOf[string, *StreamWatcher] // Thread-safe map of channel name -> *StreamWatcher for concurrent access
	enabled  atomic.Bool                          // Atomic flag indicating manager operational state (true=active, false=stopped)
	stopChan chan struct{}                        // Coordination channel for graceful manager shutdown and cleanup
}

/**
 * StreamWatcher implements comprehensive health monitoring for individual stream channels,
 * analyzing stream quality, content characteristics, and infrastructure health through
 * multiple monitoring techniques.
 *
 * The monitoring system employs multiple analysis techniques:
 *   - Buffer state analysis for infrastructure health assessment
 *   - Activity monitoring to detect stream stalls and interruptions
 *   - Context state evaluation for restreamer lifecycle management
 *   - FFprobe integration for deep content analysis and quality validation
 *   - Statistical failure tracking with configurable threshold management
 */
type StreamWatcher struct {
	channelName         string             // Channel identifier for logging and coordination
	restreamer          *types.Restreamer  // Reference to monitored restreamer instance
	ctx                 context.Context    // Cancellable context for coordinated watcher shutdown
	cancel              context.CancelFunc // Context cancellation function for cleanup operations
	lastCheck           time.Time          // Timestamp of most recent health assessment
	lastStreamStart     time.Time          // Timestamp when current stream began for grace period calculation
	consecutiveFailures int32              // Atomic counter of sequential health check failures
	totalFailures       int32              // Atomic counter of total failures within evaluation window
	lastFailureReset    time.Time          // Timestamp of most recent failure counter reset
	running             atomic.Bool        // Atomic flag indicating watcher operational state
	ffprobeCheckCount   int32              // Atomic counter for FFprobe check frequency management
}

/**
 * RestreamWrapper provides a bridge between the watcher system and the restreaming
 * infrastructure, enabling watchers to trigger stream restarts and failover operations
 * through the existing restreaming logic.
 */
type RestreamWrapper struct {
	*types.Restreamer // Embedded restreamer for direct access to streaming operations
}

/**
 * NewWatcherManager creates and initializes a new WatcherManager instance ready for
 * stream monitoring operations.
 *
 * The manager starts in disabled state and must be explicitly started to begin
 * monitoring operations, allowing for controlled initialization and resource
 * management during application startup.
 *
 * @return *WatcherManager - fully initialized manager ready for start() operation
 */
func NewWatcherManager() *WatcherManager {
	logger.Debug("{watcher - NewWatcherManager} Creating new watcher manager")

	return &WatcherManager{
		watchers: xsync.NewMapOf[string, *StreamWatcher](),
		stopChan: make(chan struct{}),
	}
}

/**
 * Start activates the WatcherManager and begins background monitoring operations.
 *
 * The start operation is idempotent and thread-safe, ensuring that multiple calls
 * will not create duplicate background processes or interfere with existing operations.
 */
func (wm *WatcherManager) Start() {
	logger.Debug("{watcher - Start} Attempting to start watcher manager")

	// Use atomic compare-and-swap to ensure start operation executes only once
	if !wm.enabled.CompareAndSwap(false, true) {
		logger.Debug("{watcher - Start} Already started, skipping")
		return
	}

	logger.Debug("{watcher - Start} Watcher manager started successfully")

	// Launch background cleanup routine for resource management
	go wm.cleanupRoutine()
}

/**
 * Stop gracefully terminates the WatcherManager and all associated monitoring
 * operations.
 *
 * The shutdown process includes:
 *   - Atomic state transition to prevent new watcher creation
 *   - Background process termination through channel signaling
 *   - Individual watcher cleanup and resource release
 *   - Memory optimization through resource cleanup
 */
func (wm *WatcherManager) Stop() {
	logger.Debug("{watcher - Stop} Attempting to stop watcher manager")

	// Use atomic compare-and-swap to ensure stop operation executes only once
	if !wm.enabled.CompareAndSwap(true, false) {
		logger.Debug("{watcher - Stop} Already stopped, skipping")
		return
	}

	logger.Debug("{watcher - Stop} Stopping watcher manager, terminating all watchers")

	// Signal background processes to terminate gracefully
	close(wm.stopChan)

	// Terminate all active stream watchers with proper cleanup
	watcherCount := 0
	wm.watchers.Range(func(key string, watcher *StreamWatcher) bool {
		logger.Debug("{watcher - Stop} Stopping watcher for channel: %s", key)
		watcher.Stop()
		watcherCount++
		return true
	})

	logger.Debug("{watcher - Stop} Stopped %d watchers", watcherCount)
}

/**
 * StartWatching initiates health monitoring for a specific channel and restreamer.
 *
 * Creates a dedicated StreamWatcher instance to continuously assess stream quality
 * and trigger automatic failover when necessary. Ensures that only one watcher
 * exists per channel by terminating any existing watcher before creating a new one.
 *
 * @param channelName Unique channel identifier for watcher coordination and logging
 * @param restreamer Active restreamer instance to monitor for health and quality issues
 */
func (wm *WatcherManager) StartWatching(channelName string, restreamer *types.Restreamer) {
	select {
	case watcherSemaphore <- struct{}{}:
	default:
		logger.Debug("{watcher - StartWatching} Max watchers reached, skipping channel %s", channelName)
		return
	}

	logger.Debug("{watcher - StartWatching} Starting watcher for channel: %s", channelName)

	// Terminate existing watcher if present
	if existing, exists := wm.watchers.LoadAndDelete(channelName); exists {
		logger.Debug("{watcher - StartWatching} Stopping existing watcher for channel: %s", channelName)
		existing.Stop()
	}

	// Create new watcher instance
	watcher := &StreamWatcher{
		channelName:      channelName,
		restreamer:       restreamer,
		lastCheck:        time.Now(),
		lastStreamStart:  time.Now(),
		lastFailureReset: time.Now(),
	}

	watcher.ctx, watcher.cancel = context.WithCancel(context.Background())
	wm.watchers.Store(channelName, watcher)

	logger.Debug("{watcher - StartWatching} Created watcher for channel: %s", channelName)

	// Start monitoring goroutine
	go watcher.Watch()

	// Log current stream information
	currentIdx := int(atomic.LoadInt32(&restreamer.CurrentIndex))
	restreamer.Channel.Mu.RLock()
	var streamURL string
	if currentIdx < len(restreamer.Channel.Streams) {
		streamURL = restreamer.Channel.Streams[currentIdx].URL
	}
	restreamer.Channel.Mu.RUnlock()

	logger.Debug("{watcher - StartWatching} Started watching channel %s (stream %d): %s",
		channelName, currentIdx, utils.LogURL(restreamer.Config, streamURL))
}

/**
 * StopWatching terminates health monitoring for a specific channel.
 *
 * Performs proper cleanup of the associated StreamWatcher and removes it from
 * the active watcher registry.
 *
 * @param channelName Unique channel identifier for the watcher to terminate
 */
func (wm *WatcherManager) StopWatching(channelName string) {
	logger.Debug("{watcher - StopWatching} Stopping watcher for channel: %s", channelName)

	if watcher, exists := wm.watchers.LoadAndDelete(channelName); exists {
		watcher.Stop()
		logger.Debug("{watcher - StopWatching} Stopped watching channel %s", channelName)
		// Release semaphore
		<-watcherSemaphore
	} else {
		logger.Debug("{watcher - StopWatching} No watcher found for channel: %s", channelName)
	}
}

/**
 * cleanupRoutine implements the background maintenance system for the WatcherManager.
 *
 * Performs periodic evaluation of active watchers and removes watchers for
 * terminated or inactive restreaming operations.
 */
func (wm *WatcherManager) cleanupRoutine() {
	logger.Debug("{watcher - cleanupRoutine} Starting cleanup routine")

	// Create ticker for periodic cleanup operations (30-second intervals)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	// Continue cleanup operations until manager shutdown signal
	for {
		select {
		case <-wm.stopChan:
			logger.Debug("{watcher - cleanupRoutine} Cleanup routine stopping")
			return
		case <-ticker.C:

			// Evaluate all active watchers for cleanup opportunities
			cleanedCount := 0
			wm.watchers.Range(func(key string, watcher *StreamWatcher) bool {
				if !watcher.restreamer.Running.Load() {
					logger.Debug("{watcher - cleanupRoutine} Removing watcher for stopped channel: %s", key)
					watcher.Stop()
					wm.watchers.Delete(key)
					cleanedCount++
				}
				return true
			})

			if cleanedCount > 0 {
				logger.Debug("{watcher - cleanupRoutine} Cleaned up %d inactive watchers", cleanedCount)
			}
		}
	}
}

/**
 * Watch implements the main monitoring loop for individual stream health assessment.
 *
 * Performs periodic quality checks and maintains failure statistics to enable
 * intelligent automatic failover decisions.
 */
func (sw *StreamWatcher) Watch() {
	logger.Debug("{watcher - Watch} Starting watch loop for channel: %s", sw.channelName)

	// Ensure single execution using atomic compare-and-swap
	if !sw.running.CompareAndSwap(false, true) {
		logger.Warn("{watcher - Watch} Watch already running for channel: %s", sw.channelName)
		return
	}

	// Ensure running flag is cleared on routine termination
	defer func() {
		sw.running.Store(false)
		logger.Debug("{watcher - Watch} Watch loop ended for channel: %s", sw.channelName)
	}()

	// Configure monitoring interval with debug mode optimization
	interval := 30 * time.Second // Standard interval for production monitoring

	// Use more frequent checks in debug mode for development and troubleshooting
	if sw.restreamer.Config.Debug {
		interval = 15 * time.Second
		logger.Debug("{watcher - Watch} Debug mode enabled, using %v interval for channel: %s", interval, sw.channelName)
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	logger.Debug("{watcher - Watch} Channel %s: Monitoring every %v", sw.channelName, interval)

	// Main monitoring loop continues until context cancellation
	for {
		select {
		case <-sw.ctx.Done():
			logger.Debug("{watcher - Watch} Context cancelled for channel: %s", sw.channelName)
			return
		case <-ticker.C:
			// Skip monitoring if restreamer has terminated
			if !sw.restreamer.Running.Load() {
				logger.Debug("{watcher - Watch} Restreamer not running for channel: %s, stopping watcher", sw.channelName)
				return
			}

			// Perform comprehensive stream health assessment
			sw.checkStreamHealth()
		}
	}
}

/**
 * Stop gracefully terminates the StreamWatcher.
 *
 * Cancels the watcher's context and triggers cleanup of monitoring operations.
 */
func (sw *StreamWatcher) Stop() {
	logger.Debug("{watcher - Stop} Stopping watcher for channel: %s", sw.channelName)

	if sw.cancel != nil {
		sw.cancel()
	}
}

/**
 * checkStreamHealth performs comprehensive stream quality assessment.
 *
 * Uses multiple analysis techniques to evaluate infrastructure health, content
 * quality, and streaming reliability.
 */
func (sw *StreamWatcher) checkStreamHealth() {
	logger.Debug("{watcher - checkStreamHealth} Starting health check for channel: %s", sw.channelName)

	startTime := time.Now()
	hasIssues := sw.evaluateStreamHealthFromState()
	checkDuration := time.Since(startTime)

	if sw.restreamer.Config.Debug {
		lastActivity := sw.restreamer.LastActivity.Load()
		timeSinceActivity := time.Now().Unix() - lastActivity

		clientCount := 0
		sw.restreamer.Clients.Range(func(key string, value *types.RestreamClient) bool {
			clientCount++
			return true
		})

		totalFails := atomic.LoadInt32(&sw.totalFailures)
		consecFails := atomic.LoadInt32(&sw.consecutiveFailures)

		logger.Debug("{watcher - checkStreamHealth} Channel %s: Health=%v, Activity=%ds ago, Clients=%d, TotalFails=%d, ConsecFails=%d, Check=%v",
			sw.channelName, !hasIssues, timeSinceActivity, clientCount, totalFails, consecFails, checkDuration)
	}

	if hasIssues {
		consecutiveFailures := atomic.AddInt32(&sw.consecutiveFailures, 1)
		totalFailures := atomic.AddInt32(&sw.totalFailures, 1)

		logger.Warn("{watcher - checkStreamHealth} Channel %s: Health issue detected (consecutive: %d/3, total: %d/3)",
			sw.channelName, consecutiveFailures, totalFailures)

		// Require both conditions and higher thresholds before triggering failover
		if consecutiveFailures >= 3 && totalFailures >= 3 {
			logger.Warn("{watcher - checkStreamHealth} Channel %s: Failure thresholds exceeded, triggering stream switch", sw.channelName)

			reason := "persistent_failures"
			sw.triggerStreamSwitch(reason)

			// Reset counters after triggering switch
			atomic.StoreInt32(&sw.consecutiveFailures, 0)
			atomic.StoreInt32(&sw.totalFailures, 0)
			sw.lastFailureReset = time.Now()

			logger.Debug("{watcher - checkStreamHealth} Channel %s: Failure counters reset after stream switch", sw.channelName)
		}
	} else {
		oldConsecutiveFailures := atomic.SwapInt32(&sw.consecutiveFailures, 0)

		if oldConsecutiveFailures > 0 {
			logger.Debug("{watcher - checkStreamHealth} Channel %s: Health recovered, reset %d consecutive failures",
				sw.channelName, oldConsecutiveFailures)
		}

		if time.Since(sw.lastFailureReset) > 15*time.Minute {
			oldTotalFailures := atomic.SwapInt32(&sw.totalFailures, 0)
			sw.lastFailureReset = time.Now()

			if oldTotalFailures > 0 {
				logger.Debug("{watcher - checkStreamHealth} Channel %s: Long-term health recovered, reset %d total failures",
					sw.channelName, oldTotalFailures)
			}
		}
	}

	sw.lastCheck = time.Now()
}

/**
 * evaluateStreamHealthFromState performs comprehensive infrastructure and content
 * health assessment using existing restreamer state.
 *
 * Health assessment criteria include:
 *   - Grace period enforcement for newly started streams (30 seconds)
 *   - Buffer integrity validation for infrastructure health
 *   - Activity monitoring for stream stall detection (30 second threshold)
 *   - Context state evaluation for lifecycle management
 *   - Periodic FFprobe analysis for deep content validation
 *
 * @return bool - true if health issues detected, false if stream appears healthy
 */
func (sw *StreamWatcher) evaluateStreamHealthFromState() bool {
	// Grace period for newly started streams
	if time.Since(sw.lastStreamStart) < 30*time.Second {
		logger.Debug("{watcher - evaluateStreamHealthFromState} Channel %s: In grace period (%v elapsed), skipping health check",
			sw.channelName, time.Since(sw.lastStreamStart))
		return false
	}

	hasIssues := false

	// Check buffer state
	if sw.restreamer.Buffer != nil && sw.restreamer.Buffer.IsDestroyed() {
		logger.Warn("{watcher - evaluateStreamHealthFromState} Channel %s: Buffer destroyed", sw.channelName)
		hasIssues = true
	}

	// Check stream activity
	lastActivity := sw.restreamer.LastActivity.Load()
	timeSinceActivity := time.Now().Unix() - lastActivity

	if timeSinceActivity > 30 {
		logger.Warn("{watcher - evaluateStreamHealthFromState} Channel %s: No activity for %d seconds (threshold: 30s)",
			sw.channelName, timeSinceActivity)
		hasIssues = true
	}

	// Check context state
	if sw.restreamer.Running.Load() {
		select {
		case <-sw.restreamer.Ctx.Done():
			if time.Since(sw.lastCheck) > 300*time.Second {
				logger.Warn("{watcher - evaluateStreamHealthFromState} Channel %s: Context cancelled and running for >300s", sw.channelName)
				hasIssues = true
			}
		default:
		}
	}

	// Periodic FFprobe check
	if sw.shouldRunFFProbeCheck() {
		logger.Debug("{watcher - evaluateStreamHealthFromState} Channel %s: Running FFprobe check", sw.channelName)
		streamHealth := sw.analyzeStreamWithFFProbe()
		if sw.evaluateFFProbeResults(streamHealth) {
			logger.Warn("{watcher - evaluateStreamHealthFromState} Channel %s: FFprobe check detected issues", sw.channelName)
			hasIssues = true
		}
	}

	return hasIssues
}

/**
 * analyzeStreamWithFFProbe performs deep content analysis using FFprobe.
 *
 * Uses existing buffer data rather than opening new network connections to assess
 * video quality, audio presence, bitrate characteristics, and format validity.
 *
 * @return types.StreamHealthData - comprehensive stream quality assessment with validity flag
 */
func (sw *StreamWatcher) analyzeStreamWithFFProbe() types.StreamHealthData {
	logger.Debug("{watcher - analyzeStreamWithFFProbe} Starting FFprobe analysis for channel: %s", sw.channelName)

	health := types.StreamHealthData{}

	streamData := sw.getStreamDataFromBuffer()
	if len(streamData) == 0 {
		logger.Warn("{watcher - analyzeStreamWithFFProbe} Channel %s: No stream data available for FFprobe", sw.channelName)
		return health
	}

	logger.Debug("{watcher - analyzeStreamWithFFProbe} Channel %s: Analyzing %d bytes of stream data", sw.channelName, len(streamData))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ffprobe",
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		"-analyzeduration", "2M",
		"-probesize", "2M",
		"-fflags", "nobuffer+discardcorrupt",
		"-thread_queue_size", "1024",
		"-i", "pipe:0")

	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		logger.Error("{watcher - analyzeStreamWithFFProbe} Channel %s: Failed to create stdin pipe: %v", sw.channelName, err)
		return health
	}

	go func() {
		defer stdin.Close()
		maxData := 2 * 1024 * 1024
		if len(streamData) > maxData {
			logger.Debug("{watcher - analyzeStreamWithFFProbe} Channel %s: Writing %d bytes (truncated) to FFprobe", sw.channelName, maxData)
			stdin.Write(streamData[:maxData])
		} else {
			logger.Debug("{watcher - analyzeStreamWithFFProbe} Channel %s: Writing %d bytes to FFprobe", sw.channelName, len(streamData))
			stdin.Write(streamData)
		}
	}()

	output, err := cmd.Output()
	if err != nil {
		if cmd.Process != nil {
			logger.Debug("{watcher - analyzeStreamWithFFProbe} Channel %s: Killing FFprobe process", sw.channelName)
			syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		logger.Warn("{watcher - analyzeStreamWithFFProbe} Channel %s: FFprobe failed (non-fatal): %v", sw.channelName, err)
		return health
	}

	logger.Debug("{watcher - analyzeStreamWithFFProbe} Channel %s: FFprobe completed successfully, parsing output", sw.channelName)

	var result struct {
		Format struct {
			BitRate string `json:"bit_rate"`
		} `json:"format"`
		Streams []struct {
			CodecType string `json:"codec_type"`
			CodecName string `json:"codec_name"`
			Width     int    `json:"width"`
			Height    int    `json:"height"`
		} `json:"streams"`
	}

	if err := json.Unmarshal(output, &result); err != nil {
		logger.Error("{watcher - analyzeStreamWithFFProbe} Channel %s: Failed to parse FFprobe output: %v", sw.channelName, err)
		return health
	}

	// Parse stream information
	for _, stream := range result.Streams {
		if stream.CodecType == "video" {
			health.HasVideo = stream.Width > 0 && stream.Height > 0
			if health.HasVideo {
				health.Resolution = fmt.Sprintf("%dx%d", stream.Width, stream.Height)
			}
		} else if stream.CodecType == "audio" {
			health.HasAudio = stream.CodecName != ""
		}
	}

	if result.Format.BitRate != "" {
		if bitrate, err := strconv.ParseInt(result.Format.BitRate, 10, 64); err == nil {
			health.Bitrate = bitrate
		}
	}

	health.Valid = true

	logger.Debug("{watcher - analyzeStreamWithFFProbe} Channel %s: Analysis complete - Video=%v, Audio=%v, Bitrate=%d, Resolution=%s",
		sw.channelName, health.HasVideo, health.HasAudio, health.Bitrate, health.Resolution)

	return health
}

/**
 * parseFrameRate converts FFprobe frame rate strings to decimal values.
 *
 * @param frameRate Frame rate string in "num/den" format from FFprobe output
 * @return float64 - decimal frame rate value, or 0 if parsing fails
 */
func (sw *StreamWatcher) parseFrameRate(frameRate string) float64 {
	// Split frame rate specification into numerator and denominator
	parts := strings.Split(frameRate, "/")
	if len(parts) != 2 {
		return 0
	}

	// Parse both components as floating point numbers
	num, err1 := strconv.ParseFloat(parts[0], 64)
	den, err2 := strconv.ParseFloat(parts[1], 64)

	// Validate parsing success and prevent division by zero
	if err1 != nil || err2 != nil || den == 0 {
		return 0
	}

	// Calculate decimal frame rate
	return num / den
}

/**
 * evaluateFFProbeResults analyzes FFprobe stream health data to identify quality issues.
 *
 * Quality assessment criteria:
 *   - Video stream presence (critical for IPTV functionality)
 *   - Bitrate adequacy (minimum 10 kbps threshold for usable content)
 *   - Overall stream viability based on content characteristics
 *
 * @param health Comprehensive stream health data from FFprobe analysis
 * @return bool - true if quality issues warrant failover, false if stream is acceptable
 */
func (sw *StreamWatcher) evaluateFFProbeResults(health types.StreamHealthData) bool {
	// If FFprobe didn't return valid data, don't fail
	if !health.Valid {
		logger.Debug("{watcher - evaluateFFProbeResults} Channel %s: FFprobe data not valid, skipping evaluation", sw.channelName)
		return false
	}

	hasIssues := false

	// Critical: no video AND no bitrate
	if !health.HasVideo && health.Bitrate == 0 {
		logger.Warn("{watcher - evaluateFFProbeResults} Channel %s: CRITICAL - No video stream AND no bitrate detected", sw.channelName)
		hasIssues = true
	}

	// Very low bitrate threshold (10 kbps)
	if health.Bitrate > 0 && health.Bitrate < 10000 {
		logger.Warn("{watcher - evaluateFFProbeResults} Channel %s: CRITICAL - Bitrate too low (%d bps)", sw.channelName, health.Bitrate)
		hasIssues = true
	}

	// Log healthy streams in debug mode
	if !hasIssues {
		logger.Debug("{watcher - evaluateFFProbeResults} Channel %s: FFprobe OK - Video=%v, Audio=%v, Bitrate=%d, Resolution=%s",
			sw.channelName, health.HasVideo, health.HasAudio, health.Bitrate, health.Resolution)
	}

	return hasIssues
}

/**
 * shouldRunFFProbeCheck implements frequency management for FFprobe analysis operations.
 *
 * FFprobe analysis is performed every 10th health check to provide adequate monitoring
 * coverage while preventing excessive system resource usage.
 *
 * @return bool - true if FFprobe analysis should be performed in current health check cycle
 */
func (sw *StreamWatcher) shouldRunFFProbeCheck() bool {
	// Perform FFprobe analysis every 10th health check
	count := atomic.AddInt32(&sw.ffprobeCheckCount, 1)
	shouldRun := count%10 == 0

	if shouldRun {
		logger.Debug("{watcher - shouldRunFFProbeCheck} Channel %s: FFprobe check #%d, running analysis", sw.channelName, count)
	}

	return shouldRun
}

/**
 * triggerStreamSwitch initiates automatic failover operations when stream health
 * issues exceed configured thresholds.
 *
 * @param reason Categorized reason for failover operation (for logging and analysis)
 */
func (sw *StreamWatcher) triggerStreamSwitch(reason string) {
	logger.Warn("{watcher - triggerStreamSwitch} Triggering stream switch for channel %s due to: %s",
		sw.channelName, reason)

	// Locate next available stream
	nextIndex := sw.findNextAvailableStream()
	if nextIndex == -1 {
		logger.Error("{watcher - triggerStreamSwitch} Channel %s: No alternative streams available for failover",
			sw.channelName)
		return
	}

	logger.Debug("{watcher - triggerStreamSwitch} Channel %s: Found alternative stream at index %d",
		sw.channelName, nextIndex)

	// Verify client presence before switching
	clientCount := 0
	sw.restreamer.Clients.Range(func(key string, value *types.RestreamClient) bool {
		clientCount++
		return true
	})

	if clientCount == 0 {
		logger.Debug("{watcher - triggerStreamSwitch} Channel %s: No clients connected, skipping switch",
			sw.channelName)
		return
	}

	logger.Warn("{watcher - triggerStreamSwitch} Channel %s: Switching to stream %d for %d clients",
		sw.channelName, nextIndex, clientCount)

	// Execute failover
	sw.forceStreamRestart(nextIndex)
}

/**
 * forceStreamRestart implements comprehensive stream restart operations for failover.
 *
 * The restart sequence includes:
 *   - Atomic preference index updates for consistent state
 *   - Graceful shutdown of current streaming infrastructure
 *   - Buffer reset and context recreation for fresh start
 *   - Client connection validation and preservation
 *   - Integration with existing streaming logic for restart coordination
 *
 * @param newIndex Index of alternative stream for failover operation
 */
func (sw *StreamWatcher) forceStreamRestart(newIndex int) {
	logger.Debug("{watcher - forceStreamRestart} Channel %s: Starting forced restart to stream index %d",
		sw.channelName, newIndex)

	// Update preferred stream index atomically
	atomic.StoreInt32(&sw.restreamer.Channel.PreferredStreamIndex, int32(newIndex))
	atomic.StoreInt32(&sw.restreamer.CurrentIndex, int32(newIndex))

	logger.Debug("{watcher - forceStreamRestart} Channel %s: Updated stream indices to %d", sw.channelName, newIndex)

	// Gracefully terminate current streaming operations
	if sw.restreamer.Running.Load() {
		logger.Debug("{watcher - forceStreamRestart} Channel %s: Stopping current stream", sw.channelName)
		sw.restreamer.Running.Store(false)
		sw.restreamer.Cancel()

		// Provide brief pause for cleanup completion
		time.Sleep(200 * time.Millisecond)
	}

	// Create fresh context for new streaming session
	ctx, cancel := context.WithCancel(context.Background())
	sw.restreamer.Ctx = ctx
	sw.restreamer.Cancel = cancel

	logger.Debug("{watcher - forceStreamRestart} Channel %s: Created new streaming context", sw.channelName)

	// Reset buffer state for clean restart
	if sw.restreamer.Buffer != nil && !sw.restreamer.Buffer.IsDestroyed() {
		sw.restreamer.Buffer.Reset()
		logger.Debug("{watcher - forceStreamRestart} Channel %s: Reset buffer", sw.channelName)
	}

	// Verify client connections before restart
	clientCount := 0
	sw.restreamer.Clients.Range(func(key string, value *types.RestreamClient) bool {
		clientCount++
		return true
	})

	logger.Debug("{watcher - forceStreamRestart} Channel %s: Verified %d clients connected", sw.channelName, clientCount)

	if clientCount > 0 {
		// Initiate new streaming session
		sw.restreamer.Running.Store(true)
		sw.lastStreamStart = time.Now()

		logger.Debug("{watcher - forceStreamRestart} Channel %s: Starting new stream session", sw.channelName)
		go sw.restartWithExistingLogic()
	} else {
		logger.Warn("{watcher - forceStreamRestart} Channel %s: No clients connected, skipping restart", sw.channelName)
	}

	// Reset failure tracking
	atomic.StoreInt32(&sw.consecutiveFailures, 0)
	atomic.StoreInt32(&sw.totalFailures, 0)
	sw.lastFailureReset = time.Now()

	logger.Debug("{watcher - forceStreamRestart} Channel %s: Reset failure counters", sw.channelName)
}

/**
 * restartWithExistingLogic provides integration between watcher-initiated failover
 * and established restreaming infrastructure.
 */
func (sw *StreamWatcher) restartWithExistingLogic() {
	logger.Debug("{watcher - restartWithExistingLogic} Channel %s: Entering restart logic", sw.channelName)

	// Implement panic recovery
	defer func() {
		if rec := recover(); rec != nil {
			logger.Error("{watcher - restartWithExistingLogic} Channel %s: Recovered from panic: %v",
				sw.channelName, rec)
		}

		// Ensure running state is properly managed
		sw.restreamer.Running.Store(false)
		logger.Debug("{watcher - restartWithExistingLogic} Channel %s: Restart completed, running=false", sw.channelName)
	}()

	currentIdx := int(atomic.LoadInt32(&sw.restreamer.CurrentIndex))
	logger.Debug("{watcher - restartWithExistingLogic} Channel %s: Starting stream restart at index %d",
		sw.channelName, currentIdx)

	// Leverage existing streaming logic
	r := NewRestreamWrapper(sw.restreamer)
	r.Stream()

	logger.Debug("{watcher - restartWithExistingLogic} Channel %s: Stream restart logic completed", sw.channelName)
}

/**
 * findNextAvailableStream implements intelligent alternative stream selection for failover.
 *
 * Stream selection criteria:
 *   - Exclusion of blocked streams
 *   - Source connection limit validation
 *   - Round-robin rotation for fair source utilization
 *
 * @return int - index of next available stream for failover, or -1 if no alternatives available
 */
func (sw *StreamWatcher) findNextAvailableStream() int {
	logger.Debug("{watcher - findNextAvailableStream} Channel %s: Searching for next available stream", sw.channelName)

	// Acquire read lock for safe access
	sw.restreamer.Channel.Mu.RLock()
	defer sw.restreamer.Channel.Mu.RUnlock()

	streamCount := len(sw.restreamer.Channel.Streams)

	if streamCount <= 1 {
		logger.Warn("{watcher - findNextAvailableStream} Channel %s: Only %d stream(s) available, no alternatives",
			sw.channelName, streamCount)
		return -1
	}

	currentIdx := int(atomic.LoadInt32(&sw.restreamer.CurrentIndex))
	logger.Debug("{watcher - findNextAvailableStream} Channel %s: Current index=%d, total streams=%d",
		sw.channelName, currentIdx, streamCount)

	// Evaluate alternative streams
	for i := 1; i < streamCount; i++ {
		nextIndex := (currentIdx + i) % streamCount
		stream := sw.restreamer.Channel.Streams[nextIndex]

		logger.Debug("{watcher - findNextAvailableStream} Channel %s: Evaluating stream %d", sw.channelName, nextIndex)

		// Skip blocked streams
		if atomic.LoadInt32(&stream.Blocked) == 1 {
			logger.Debug("{watcher - findNextAvailableStream} Channel %s: Stream %d is blocked, skipping",
				sw.channelName, nextIndex)
			continue
		}

		// Validate source connection availability
		currentConns := stream.Source.ActiveConns.Load()
		if currentConns >= int32(stream.Source.MaxConnections) {
			logger.Debug("{watcher - findNextAvailableStream} Channel %s: Stream %d source at connection limit (%d/%d), skipping: %s",
				sw.channelName, nextIndex, currentConns, stream.Source.MaxConnections, stream.Source.Name)
			continue
		}

		// Found available stream
		logger.Debug("{watcher - findNextAvailableStream} Channel %s: Selected stream %d as next alternative",
			sw.channelName, nextIndex)
		return nextIndex
	}

	// No suitable alternatives found
	logger.Warn("{watcher - findNextAvailableStream} Channel %s: No suitable alternative streams found after checking %d streams",
		sw.channelName, streamCount-1)
	return -1
}

/**
 * NewRestreamWrapper creates a RestreamWrapper instance that bridges watcher
 * failover operations with the established restreaming infrastructure.
 *
 * @param restreamer Existing restreamer instance to wrap for restart operations
 * @return *RestreamWrapper - wrapper providing access to streaming functionality
 */
func NewRestreamWrapper(restreamer *types.Restreamer) *RestreamWrapper {
	return &RestreamWrapper{
		Restreamer: restreamer,
	}
}

/**
 * Stream leverages the complete established streaming infrastructure through the
 * restreaming package.
 */
func (rw *RestreamWrapper) Stream() {
	logger.Debug("{watcher - Stream} Starting stream through wrapper")

	// Utilize comprehensive streaming logic
	r := &restream.Restream{Restreamer: rw.Restreamer}
	r.WatcherStream()

	logger.Debug("{watcher - Stream} Stream completed")
}

/**
 * getStreamDataFromBuffer retrieves recent stream content from the restreamer's
 * ring buffer for FFprobe analysis.
 *
 * @return []byte - recent stream data suitable for FFprobe analysis, or nil if buffer unavailable
 */
func (sw *StreamWatcher) getStreamDataFromBuffer() []byte {
	// Validate buffer existence and operational state
	if sw.restreamer.Buffer == nil || sw.restreamer.Buffer.IsDestroyed() {
		logger.Debug("{watcher - getStreamDataFromBuffer} Channel %s: Buffer unavailable or destroyed", sw.channelName)
		return nil
	}

	// Extract recent buffer content
	data := sw.restreamer.Buffer.PeekRecentData(3 * 1024 * 1024)
	logger.Debug("{watcher - getStreamDataFromBuffer} Channel %s: Retrieved %d bytes from buffer", sw.channelName, len(data))

	return data
}
