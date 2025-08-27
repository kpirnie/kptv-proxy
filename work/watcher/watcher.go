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

// WatcherManager coordinates multiple stream watchers across all active channels,
// providing centralized management for stream health monitoring, automatic failover,
// and resource cleanup. The manager implements a sophisticated monitoring system
// that tracks stream quality, detects degradation patterns, and triggers intelligent
// failover operations to maintain optimal user experience during stream issues.
//
// The manager operates independently of client connections and restreaming operations,
// continuously monitoring active streams through background processes that analyze
// stream characteristics, buffer health, and content quality. When problems are
// detected, the manager can trigger automatic stream switching without disrupting
// connected clients, providing seamless failover capabilities.
//
// Key responsibilities include:
//   - Lifecycle management for individual stream watchers
//   - Coordination of health monitoring across multiple channels
//   - Automatic cleanup of watchers for terminated streams
//   - Resource management and memory optimization for long-running operations
//   - Integration with restreaming infrastructure for seamless failover
type WatcherManager struct {
	watchers sync.Map      // Thread-safe map of channel name -> *StreamWatcher for concurrent access
	logger   *log.Logger   // Application logger for monitoring events and debugging information
	enabled  atomic.Bool   // Atomic flag indicating manager operational state (true=active, false=stopped)
	stopChan chan struct{} // Coordination channel for graceful manager shutdown and cleanup
}

// StreamWatcher implements comprehensive health monitoring for individual stream channels,
// analyzing stream quality, content characteristics, and infrastructure health through
// multiple monitoring techniques. The watcher operates continuously in the background,
// performing periodic health assessments and maintaining failure statistics to enable
// intelligent automatic failover decisions when stream quality degrades.
//
// The monitoring system employs multiple analysis techniques:
//   - Buffer state analysis for infrastructure health assessment
//   - Activity monitoring to detect stream stalls and interruptions
//   - Context state evaluation for restreamer lifecycle management
//   - FFprobe integration for deep content analysis and quality validation
//   - Statistical failure tracking with configurable threshold management
//
// Failover decisions are based on sophisticated algorithms that consider both
// immediate health indicators and historical failure patterns, preventing
// unnecessary stream switching while ensuring rapid response to persistent problems.
type StreamWatcher struct {
	channelName         string             // Channel identifier for logging and coordination
	restreamer          *types.Restreamer  // Reference to monitored restreamer instance
	logger              *log.Logger        // Application logger for debugging and event recording
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

// RestreamWrapper provides a bridge between the watcher system and the restreaming
// infrastructure, enabling watchers to trigger stream restarts and failover operations
// through the existing restreaming logic. The wrapper maintains compatibility with
// established streaming patterns while providing the additional control needed for
// automatic health management and quality-based stream switching.
//
// This wrapper pattern enables the watcher system to leverage the comprehensive
// streaming logic (master playlist handling, source selection, connection management)
// without duplicating complex implementation details or creating tight coupling
// between monitoring and streaming subsystems.
type RestreamWrapper struct {
	*types.Restreamer // Embedded restreamer for direct access to streaming operations
}

// NewWatcherManager creates and initializes a new WatcherManager instance ready for
// stream monitoring operations. The manager starts in disabled state and must be
// explicitly started to begin monitoring operations, allowing for controlled
// initialization and resource management during application startup.
//
// The manager is designed for singleton usage within the application, coordinating
// all stream monitoring activities through a centralized interface while maintaining
// thread-safe operations across multiple concurrent watchers and client operations.
//
// Parameters:
//   - logger: application logger for monitoring events, debugging, and operational reporting
//
// Returns:
//   - *WatcherManager: fully initialized manager ready for start() operation
func NewWatcherManager(logger *log.Logger) *WatcherManager {
	return &WatcherManager{
		logger:   logger,
		stopChan: make(chan struct{}),
	}
}

// Start activates the WatcherManager and begins background monitoring operations,
// including periodic cleanup routines and resource management tasks. The start
// operation is idempotent and thread-safe, ensuring that multiple calls will not
// create duplicate background processes or interfere with existing operations.
//
// The manager implements a comprehensive background maintenance system that
// periodically evaluates active watchers, removes watchers for terminated streams,
// and performs memory optimization to ensure long-term operational stability.
// These background processes continue until the manager is explicitly stopped.
func (wm *WatcherManager) Start() {

	// Use atomic compare-and-swap to ensure start operation executes only once
	if !wm.enabled.CompareAndSwap(false, true) {
		return
	}

	wm.logger.Printf("[WATCHER] Stream Watcher Manager started")

	// Launch background cleanup routine for resource management
	go wm.cleanupRoutine()
}

// Stop gracefully terminates the WatcherManager and all associated monitoring
// operations, ensuring proper cleanup of resources, termination of background
// processes, and coordinated shutdown of individual stream watchers. The stop
// operation implements comprehensive cleanup to prevent resource leaks and
// ensure clean application shutdown sequences.
//
// The shutdown process includes:
//   - Atomic state transition to prevent new watcher creation
//   - Background process termination through channel signaling
//   - Individual watcher cleanup and resource release
//   - Memory optimization through resource cleanup
func (wm *WatcherManager) Stop() {

	// Use atomic compare-and-swap to ensure stop operation executes only once
	if !wm.enabled.CompareAndSwap(true, false) {
		return
	}

	// Signal background processes to terminate gracefully
	close(wm.stopChan)

	// Terminate all active stream watchers with proper cleanup
	wm.watchers.Range(func(key, value interface{}) bool {
		watcher := value.(*StreamWatcher)
		watcher.Stop()
		return true
	})

	wm.logger.Printf("[WATCHER] Stream Watcher Manager stopped")
}

// StartWatching initiates health monitoring for a specific channel and restreamer,
// creating a dedicated StreamWatcher instance to continuously assess stream quality
// and trigger automatic failover when necessary. The method ensures that only one
// watcher exists per channel, terminating any existing watcher before creating
// a new one to prevent resource conflicts and monitoring duplication.
//
// The watcher initialization process establishes comprehensive monitoring coverage
// including failure tracking initialization, grace period configuration, and
// integration with the restreaming infrastructure for seamless failover operations.
// Debug logging provides operational visibility for troubleshooting and monitoring.
//
// Parameters:
//   - channelName: unique channel identifier for watcher coordination and logging
//   - restreamer: active restreamer instance to monitor for health and quality issues
func (wm *WatcherManager) StartWatching(channelName string, restreamer *types.Restreamer) {

	// Terminate any existing watcher for this channel to prevent duplication
	if existing, exists := wm.watchers.LoadAndDelete(channelName); exists {
		existingWatcher := existing.(*StreamWatcher)
		existingWatcher.Stop()
	}

	// Create new watcher instance with comprehensive monitoring configuration
	watcher := &StreamWatcher{
		channelName:      channelName,
		restreamer:       restreamer,
		logger:           wm.logger,
		lastCheck:        time.Now(),
		lastStreamStart:  time.Now(),
		lastFailureReset: time.Now(),
	}

	// Initialize cancellable context for coordinated watcher lifecycle management
	watcher.ctx, watcher.cancel = context.WithCancel(context.Background())
	wm.watchers.Store(channelName, watcher)

	// Launch monitoring goroutine for continuous health assessment
	go watcher.Watch()

	// Extract current stream information for logging and debugging
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

// StopWatching terminates health monitoring for a specific channel, performing
// proper cleanup of the associated StreamWatcher and removing it from the active
// watcher registry. The method ensures graceful termination of monitoring operations
// without disrupting the underlying streaming infrastructure or connected clients.
//
// The cleanup process includes context cancellation, resource release, and
// registry cleanup to prevent memory leaks and ensure clean operational state
// for potential future monitoring of the same channel.
//
// Parameters:
//   - channelName: unique channel identifier for the watcher to terminate
func (wm *WatcherManager) StopWatching(channelName string) {
	if watcher, exists := wm.watchers.LoadAndDelete(channelName); exists {
		w := watcher.(*StreamWatcher)
		w.Stop()
		wm.logger.Printf("[WATCHER] Stopped watching channel %s", channelName)
	}
}

// cleanupRoutine implements the background maintenance system for the WatcherManager,
// performing periodic evaluation of active watchers and removing watchers for
// terminated or inactive restreaming operations. The routine operates continuously
// until the manager is stopped, ensuring optimal resource usage and preventing
// accumulation of obsolete monitoring instances.
//
// The cleanup process evaluates each active watcher to determine if its associated
// restreamer is still operational, removing watchers for terminated streams to
// prevent resource leaks and maintain accurate monitoring state. The routine
// uses configurable intervals to balance resource efficiency with responsiveness.
func (wm *WatcherManager) cleanupRoutine() {

	// Create ticker for periodic cleanup operations (30-second intervals)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	// Continue cleanup operations until manager shutdown signal
	for {
		select {
		case <-wm.stopChan:
			return
		case <-ticker.C:

			// Evaluate all active watchers for cleanup opportunities
			wm.watchers.Range(func(key, value interface{}) bool {
				watcher := value.(*StreamWatcher)

				// Remove watchers for terminated restreamers to prevent resource leaks
				if !watcher.restreamer.Running.Load() {
					watcher.Stop()
					wm.watchers.Delete(key)
				}

				return true
			})
		}
	}
}

// Watch implements the main monitoring loop for individual stream health assessment,
// performing periodic quality checks and maintaining failure statistics to enable
// intelligent automatic failover decisions. The monitoring system operates continuously
// until explicitly stopped, providing comprehensive coverage of stream health
// indicators and infrastructure status.
//
// The monitoring process implements configurable check intervals with debug mode
// support for more frequent assessment during development and troubleshooting.
// The routine ensures that monitoring operations do not interfere with streaming
// performance while providing timely detection of quality degradation and failures.
func (sw *StreamWatcher) Watch() {

	// Ensure single execution using atomic compare-and-swap
	if !sw.running.CompareAndSwap(false, true) {
		return
	}

	// Ensure running flag is cleared on routine termination
	defer sw.running.Store(false)

	// Configure monitoring interval with debug mode optimization
	interval := 30 * time.Second // Standard interval for production monitoring

	// Use more frequent checks in debug mode for development and troubleshooting
	if sw.restreamer.Config.Debug {
		interval = 15 * time.Second
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	sw.logger.Printf("[WATCHER] Channel %s: Monitoring every %v", sw.channelName, interval)

	// Main monitoring loop continues until context cancellation
	for {
		select {
		case <-sw.ctx.Done():
			return
		case <-ticker.C:
			// Skip monitoring if restreamer has terminated
			if !sw.restreamer.Running.Load() {
				return
			}

			// Perform comprehensive stream health assessment
			sw.checkStreamHealth()
		}
	}
}

// Stop gracefully terminates the StreamWatcher by cancelling its context and
// triggering cleanup of monitoring operations. The stop operation ensures that
// background monitoring routines terminate cleanly without leaving orphaned
// goroutines or resource leaks.
//
// The termination process is coordinated through context cancellation, allowing
// the main monitoring loop to detect the stop signal and perform appropriate
// cleanup before terminating the monitoring goroutine.
func (sw *StreamWatcher) Stop() {
	if sw.cancel != nil {
		sw.cancel()
	}
}

// checkStreamHealth performs comprehensive stream quality assessment using multiple
// analysis techniques to evaluate infrastructure health, content quality, and
// streaming reliability. The health check implements sophisticated algorithms
// that consider both immediate indicators and historical patterns to make
// intelligent failover decisions without unnecessary stream switching.
//
// The assessment process includes:
//   - Infrastructure health evaluation (buffer state, activity monitoring)
//   - Context state analysis for restreamer lifecycle management
//   - Statistical failure tracking with configurable threshold management
//   - Deep content analysis through FFprobe integration (periodic)
//   - Grace period handling for newly started streams
//
// Results are logged comprehensively in debug mode and trigger failover operations
// when configurable thresholds are exceeded, ensuring optimal user experience
// through proactive quality management.
func (sw *StreamWatcher) checkStreamHealth() {
	startTime := time.Now()

	// Perform comprehensive health evaluation using existing restreamer state
	hasIssues := sw.evaluateStreamHealthFromState()

	checkDuration := time.Since(startTime)

	// Provide comprehensive debug logging for operational monitoring
	if sw.restreamer.Config.Debug {
		lastActivity := sw.restreamer.LastActivity.Load()
		timeSinceActivity := time.Now().Unix() - lastActivity

		// Count active clients for context
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

	// if the stream has issues
	if hasIssues {

		// Increment failure counters with atomic operations for thread safety
		consecutiveFailures := atomic.AddInt32(&sw.consecutiveFailures, 1)
		totalFailures := atomic.AddInt32(&sw.totalFailures, 1)

		sw.logger.Printf("[WATCHER] Channel %s: Health issue detected (consecutive: %d/5, total: %d/3)",
			sw.channelName, consecutiveFailures, totalFailures)

		// Trigger failover if EITHER condition is met for comprehensive problem handling:
		// 1. 3 consecutive failures (immediate serious issues requiring quick response)
		// 2. 3 total failures (pattern of instability indicating persistent problems)
		if consecutiveFailures >= 3 || totalFailures >= 3 {

			// Determine failover reason for logging and analysis
			reason := "consecutive_failures"
			if totalFailures >= 3 {
				reason = "total_failures"
			}
			sw.triggerStreamSwitch(reason)

			// Reset both failure counters after successful failover operation
			atomic.StoreInt32(&sw.consecutiveFailures, 0)
			atomic.StoreInt32(&sw.totalFailures, 0)
			sw.lastFailureReset = time.Now()
		}
	} else {

		// Handle healthy stream state with intelligent counter management
		oldConsecutiveFailures := atomic.SwapInt32(&sw.consecutiveFailures, 0)

		// Reset total failures after sustained health period (10 minutes of stability)
		if time.Since(sw.lastFailureReset) > 10*time.Minute {
			oldTotalFailures := atomic.SwapInt32(&sw.totalFailures, 0)
			sw.lastFailureReset = time.Now()

			// Log comprehensive recovery information for operational monitoring
			if (oldConsecutiveFailures > 0 || oldTotalFailures > 0) && sw.restreamer.Config.Debug {
				sw.logger.Printf("[WATCHER] Channel %s: Long-term health recovered, reset %d consecutive + %d total failures",
					sw.channelName, oldConsecutiveFailures, oldTotalFailures)
			}
		} else if oldConsecutiveFailures > 0 && sw.restreamer.Config.Debug {

			// Log partial recovery with remaining total failure count
			totalFails := atomic.LoadInt32(&sw.totalFailures)
			sw.logger.Printf("[WATCHER] Channel %s: Consecutive failures reset, but %d total failures remain",
				sw.channelName, totalFails)
		}
	}

	// Update last check timestamp for interval management
	sw.lastCheck = time.Now()
}

// evaluateStreamHealthFromState performs comprehensive infrastructure and content
// health assessment using existing restreamer state without additional network
// requests, ensuring efficient monitoring that doesn't impact streaming performance.
// The evaluation implements multiple health indicators with appropriate weighting
// and grace periods to prevent false positives during normal operation transitions.
//
// Health assessment criteria include:
//   - Grace period enforcement for newly started streams (30 seconds)
//   - Buffer integrity validation for infrastructure health
//   - Activity monitoring for stream stall detection (60 second threshold)
//   - Context state evaluation for lifecycle management
//   - Periodic FFprobe analysis for deep content validation
//
// The multi-layered approach provides comprehensive coverage while minimizing
// false positive failovers that could disrupt stable streaming operations.
//
// Returns:
//   - bool: true if health issues detected, false if stream appears healthy
func (sw *StreamWatcher) evaluateStreamHealthFromState() bool {

	// Implement grace period for newly started streams to prevent false positives
	// during normal startup and initialization phases
	if time.Since(sw.lastStreamStart) < 30*time.Second {
		if sw.restreamer.Config.Debug {
			sw.logger.Printf("[WATCHER] Channel %s: In grace period, skipping health check",
				sw.channelName)
		}
		return false
	}

	hasIssues := false

	// 1. Evaluate buffer health for infrastructure integrity assessment
	if sw.restreamer.Buffer != nil && sw.restreamer.Buffer.IsDestroyed() {
		if sw.restreamer.Config.Debug {
			sw.logger.Printf("[WATCHER] Channel %s: Buffer destroyed", sw.channelName)
		}
		hasIssues = true
	}

	// 2. Monitor for extended inactivity indicating stream stalls or interruptions
	lastActivity := sw.restreamer.LastActivity.Load()
	timeSinceActivity := time.Now().Unix() - lastActivity

	if timeSinceActivity > 60 { // 1 minute threshold for activity monitoring
		if sw.restreamer.Config.Debug {
			sw.logger.Printf("[WATCHER] Channel %s: No activity for %d seconds",
				sw.channelName, timeSinceActivity)
		}
		hasIssues = true
	}

	// 3. Evaluate context state for restreamer lifecycle management
	if sw.restreamer.Running.Load() {
		select {
		case <-sw.restreamer.Ctx.Done():

			// Implement grace period after context cancellation to handle normal restart sequences
			if time.Since(sw.lastCheck) > 60*time.Second {
				if sw.restreamer.Config.Debug {
					sw.logger.Printf("[WATCHER] Channel %s: Context cancelled and running for >60s",
						sw.channelName)
				}
				hasIssues = true
			}
		default:
			// Context is healthy - no issues from this check
		}
	}

	// 4. Perform periodic FFprobe analysis (every 2nd check to balance thoroughness with efficiency)
	if sw.shouldRunFFProbeCheck() {
		streamHealth := sw.analyzeStreamWithFFProbe()
		if sw.evaluateFFProbeResults(streamHealth) {
			hasIssues = true
		}
	}

	return hasIssues
}

// analyzeStreamWithFFProbe performs deep content analysis using FFprobe to assess
// video quality, audio presence, bitrate characteristics, and format validity.
// The analysis uses existing buffer data rather than opening new network connections,
// ensuring efficient monitoring that doesn't impact streaming performance or
// consume additional bandwidth from source servers.
//
// The FFprobe integration implements comprehensive error handling, timeout management,
// and structured JSON parsing to extract essential stream characteristics including
// video dimensions, codec information, audio track presence, and bitrate measurements.
// Results provide detailed quality metrics for intelligent failover decisions.
//
// Returns:
//   - types.StreamHealthData: comprehensive stream quality assessment with validity flag
func (sw *StreamWatcher) analyzeStreamWithFFProbe() types.StreamHealthData {
	health := types.StreamHealthData{}

	// Obtain stream data from existing buffer rather than creating new network connections
	streamData := sw.getStreamDataFromBuffer()
	if len(streamData) == 0 {
		if sw.restreamer.Config.Debug {
			sw.logger.Printf("[WATCHER] No stream data available in buffer for ffprobe analysis")
		}
		return health
	}

	// Configure FFprobe execution with reasonable timeout for analysis operations
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Configure FFprobe for comprehensive stream analysis using stdin input
	cmd := exec.CommandContext(ctx, "ffprobe",
		"-v", "quiet", // Minimize verbose output for cleaner parsing
		"-print_format", "json", // Request structured JSON output
		"-show_format",           // Include format information (bitrate, duration)
		"-show_streams",          // Include individual stream details (video, audio)
		"-analyzeduration", "3M", // Allow adequate analysis time for reliable results
		"-probesize", "3M", // Allocate sufficient buffer for content examination
		"-i", "pipe:0") // Read from stdin using buffered stream data

	// Create stdin pipe for providing stream data to FFprobe
	stdin, err := cmd.StdinPipe()
	if err != nil {
		if sw.restreamer.Config.Debug {
			sw.logger.Printf("[WATCHER] Failed to create stdin pipe for ffprobe: %v", err)
		}
		return health
	}

	// Start FFprobe process with configured parameters
	if err := cmd.Start(); err != nil {
		stdin.Close()
		if sw.restreamer.Config.Debug {
			sw.logger.Printf("[WATCHER] Failed to start ffprobe: %v", err)
		}
		return health
	}

	// Provide stream data to FFprobe through stdin in separate goroutine
	go func() {
		defer stdin.Close()
		_, writeErr := stdin.Write(streamData)
		if writeErr != nil && sw.restreamer.Config.Debug {
			sw.logger.Printf("[WATCHER] Error writing to ffprobe stdin: %v", writeErr)
		}
	}()

	// Capture FFprobe analysis output with timeout protection
	output, err := cmd.Output()
	if err != nil {
		if sw.restreamer.Config.Debug {
			sw.logger.Printf("[WATCHER] ffprobe analysis failed for %s: %v", sw.channelName, err)
		}
		return health
	}

	// Parse structured JSON output to extract stream characteristics
	var result struct {
		Format struct {
			BitRate string `json:"bit_rate"` // Overall bitrate for quality assessment
		} `json:"format"`
		Streams []struct {
			CodecType  string `json:"codec_type"`   // Stream type (video, audio, subtitle)
			CodecName  string `json:"codec_name"`   // Specific codec identifier
			Width      int    `json:"width"`        // Video width in pixels
			Height     int    `json:"height"`       // Video height in pixels
			RFrameRate string `json:"r_frame_rate"` // Frame rate specification
		} `json:"streams"`
	}

	if err := json.Unmarshal(output, &result); err != nil {
		return health
	}

	// Analyze individual streams to extract video and audio characteristics
	for _, stream := range result.Streams {
		switch stream.CodecType {
		case "video":
			// Validate video stream has proper dimensions and codec
			health.HasVideo = stream.Width > 0 && stream.Height > 0
			if health.HasVideo {
				health.Resolution = fmt.Sprintf("%dx%d", stream.Width, stream.Height)
				// Parse frame rate if available and valid
				if stream.RFrameRate != "" && stream.RFrameRate != "0/0" {
					health.FPS = sw.parseFrameRate(stream.RFrameRate)
				}
			}
		case "audio":
			// Validate audio stream has proper codec specification
			health.HasAudio = stream.CodecName != ""
		}
	}

	// Extract and parse overall bitrate information
	if result.Format.BitRate != "" {
		if bitrate, err := strconv.ParseInt(result.Format.BitRate, 10, 64); err == nil {
			health.Bitrate = bitrate
		}
	}

	// Mark analysis as successful for result validation
	health.Valid = true
	if sw.restreamer.Config.Debug {
		sw.logger.Printf("[WATCHER] ffprobe analysis using existing stream data: Video=%v, Audio=%v, Bitrate=%d",
			health.HasVideo, health.HasAudio, health.Bitrate)
	}

	return health
}

// parseFrameRate converts FFprobe frame rate strings (in "numerator/denominator" format)
// to decimal frame rate values for quality assessment and performance evaluation.
// The parsing handles various frame rate specifications including standard rates
// (29.970, 30.000, 60.000) and custom rates used by some streaming sources.
//
// Parameters:
//   - frameRate: frame rate string in "num/den" format from FFprobe output
//
// Returns:
//   - float64: decimal frame rate value, or 0 if parsing fails
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

// evaluateFFProbeResults analyzes FFprobe stream health data to identify quality
// issues that warrant automatic failover operations. The evaluation implements
// comprehensive quality thresholds for video presence, audio characteristics,
// and bitrate adequacy while providing detailed debug logging for troubleshooting
// and monitoring purposes.
//
// Quality assessment criteria:
//   - Video stream presence (critical for IPTV functionality)
//   - Bitrate adequacy (minimum 50 kbps threshold for usable content)
//   - Overall stream viability based on content characteristics
//
// Parameters:
//   - health: comprehensive stream health data from FFprobe analysis
//
// Returns:
//   - bool: true if quality issues warrant failover, false if stream is acceptable
func (sw *StreamWatcher) evaluateFFProbeResults(health types.StreamHealthData) bool {

	// Skip evaluation if FFprobe analysis failed to avoid false positives
	if !health.Valid {
		return false // Don't consider analysis failures as stream issues
	}

	hasIssues := false

	// Evaluate video stream presence (critical for IPTV functionality)
	if !health.HasVideo {
		if sw.restreamer.Config.Debug {
			sw.logger.Printf("[WATCHER] Channel %s: No video stream detected", sw.channelName)
		}
		hasIssues = true
	}

	// Evaluate bitrate characteristics for content viability
	if health.Bitrate == 0 {
		if sw.restreamer.Config.Debug {
			sw.logger.Printf("[WATCHER] Channel %s: No bitrate detected (stream may be problematic)",
				sw.channelName)
		}
		hasIssues = true
	} else if health.Bitrate < 50000 {

		// Minimum 50 kbps threshold for usable streaming content
		if sw.restreamer.Config.Debug {
			sw.logger.Printf("[WATCHER] Channel %s: Bitrate too low (%d bps)",
				sw.channelName, health.Bitrate)
		}
		hasIssues = true
	}

	// Provide comprehensive health logging for operational monitoring
	if !hasIssues && sw.restreamer.Config.Debug {
		sw.logger.Printf("[WATCHER] Channel %s: Stream healthy - Video=%v, Audio=%v, Bitrate=%d, Resolution=%s",
			sw.channelName, health.HasVideo, health.HasAudio, health.Bitrate, health.Resolution)
	}

	return hasIssues
}

// shouldRunFFProbeCheck implements frequency management for FFprobe analysis operations,
// balancing comprehensive monitoring with system resource efficiency. The function
// uses atomic counters to ensure thread-safe frequency management while distributing
// analysis load across multiple health check cycles.
//
// FFprobe analysis is performed every 2nd health check to provide adequate monitoring
// coverage while preventing excessive system resource usage during continuous operation.
//
// Returns:
//   - bool: true if FFprobe analysis should be performed in current health check cycle
func (sw *StreamWatcher) shouldRunFFProbeCheck() bool {

	// Perform FFprobe analysis every 2nd health check for balanced monitoring efficiency
	count := atomic.AddInt32(&sw.ffprobeCheckCount, 1)
	return count%2 == 0
}

// triggerStreamSwitch initiates automatic failover operations when stream health
// issues exceed configured thresholds, implementing intelligent stream selection
// and restart procedures to maintain optimal user experience. The switch operation
// preserves client connections while transitioning to alternative stream sources
// without service interruption.
//
// The failover process includes:
//   - Alternative stream identification and availability validation
//   - Client connection preservation during transition
//   - Graceful shutdown of problematic stream infrastructure
//   - Restart coordination with existing restreaming logic
//   - Failure counter reset for fresh monitoring state
//
// Parameters:
//   - reason: categorized reason for failover operation (for logging and analysis)
func (sw *StreamWatcher) triggerStreamSwitch(reason string) {
	if sw.restreamer.Config.Debug {
		sw.logger.Printf("[WATCHER] Triggering stream switch for channel %s due to: %s",
			sw.channelName, reason)
	}

	// Locate next available stream using intelligent selection algorithms
	nextIndex := sw.findNextAvailableStream()
	if nextIndex == -1 {
		if sw.restreamer.Config.Debug {
			sw.logger.Printf("[WATCHER] No alternative streams found for channel %s",
				sw.channelName)
		}
		return
	}

	// Verify client presence before performing potentially disruptive switch operation
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

	// Execute failover using established stream switching infrastructure
	sw.forceStreamRestart(nextIndex)
}

// forceStreamRestart implements comprehensive stream restart operations for failover
// scenarios, including preference update, infrastructure cleanup, and coordination
// with existing restreaming logic. The restart process maintains client connections
// while transitioning to alternative stream sources for seamless user experience.
//
// The restart sequence includes:
//   - Atomic preference index updates for consistent state
//   - Graceful shutdown of current streaming infrastructure
//   - Buffer reset and context recreation for fresh start
//   - Client connection validation and preservation
//   - Integration with existing streaming logic for restart coordination
//   - Comprehensive failure counter reset for monitoring state cleanup
//
// Parameters:
//   - newIndex: index of alternative stream for failover operation
func (sw *StreamWatcher) forceStreamRestart(newIndex int) {

	// Update preferred stream index atomically for consistent failover state
	atomic.StoreInt32(&sw.restreamer.Channel.PreferredStreamIndex, int32(newIndex))
	atomic.StoreInt32(&sw.restreamer.CurrentIndex, int32(newIndex))

	// Gracefully terminate current streaming operations if active
	if sw.restreamer.Running.Load() {
		sw.restreamer.Running.Store(false)
		sw.restreamer.Cancel()

		// Provide brief pause for cleanup completion
		time.Sleep(200 * time.Millisecond)
	}

	// Create fresh context for new streaming session
	ctx, cancel := context.WithCancel(context.Background())
	sw.restreamer.Ctx = ctx
	sw.restreamer.Cancel = cancel

	// Reset buffer state for clean restart
	if sw.restreamer.Buffer != nil && !sw.restreamer.Buffer.IsDestroyed() {
		sw.restreamer.Buffer.Reset()
	}

	// Verify client connections before restart operation
	clientCount := 0
	sw.restreamer.Clients.Range(func(_, _ interface{}) bool {
		clientCount++
		return true
	})

	if clientCount > 0 {

		// Initiate new streaming session using established restreaming infrastructure
		sw.restreamer.Running.Store(true)
		sw.lastStreamStart = time.Now() // Update stream start time for grace period
		go sw.restartWithExistingLogic()
	}

	// Reset failure tracking for fresh monitoring state after successful restart
	atomic.StoreInt32(&sw.consecutiveFailures, 0)
	atomic.StoreInt32(&sw.totalFailures, 0)
	sw.lastFailureReset = time.Now()
}

// restartWithExistingLogic provides integration between watcher-initiated failover
// operations and the established restreaming infrastructure, ensuring that stream
// restart operations leverage existing connection management, source selection,
// and master playlist processing logic without code duplication or inconsistencies.
//
// The restart wrapper implements comprehensive error handling with panic recovery
// to prevent watcher-initiated restarts from destabilizing the overall streaming
// system, while maintaining proper running state management for coordination
// with other system components.
func (sw *StreamWatcher) restartWithExistingLogic() {

	// Implement panic recovery to prevent restart failures from crashing the system
	defer func() {
		if rec := recover(); rec != nil {
			if sw.restreamer.Config.Debug {
				sw.logger.Printf("[WATCHER_RESTART_PANIC] Channel %s: Recovered from panic: %v",
					sw.channelName, rec)
			}
		}

		// Ensure running state is properly managed on restart completion
		sw.restreamer.Running.Store(false)
	}()

	if sw.restreamer.Config.Debug {
		currentIdx := int(atomic.LoadInt32(&sw.restreamer.CurrentIndex))
		sw.logger.Printf("[WATCHER_RESTART] Channel %s: Starting stream restart at index %d",
			sw.channelName, currentIdx)
	}

	// Leverage existing comprehensive streaming logic through wrapper pattern
	r := NewRestreamWrapper(sw.restreamer)
	r.Stream()
}

// findNextAvailableStream implements intelligent alternative stream selection for
// failover operations, evaluating available streams for suitability based on
// blocking status, source connection availability, and round-robin fairness.
// The selection algorithm prioritizes operational streams while avoiding known
// problematic sources that could result in immediate secondary failures.
//
// Stream selection criteria include:
//   - Exclusion of blocked streams from consideration
//   - Source connection limit validation to prevent overload
//   - Round-robin rotation for fair source utilization
//   - Comprehensive availability logging for operational monitoring
//
// Returns:
//   - int: index of next available stream for failover, or -1 if no alternatives available
func (sw *StreamWatcher) findNextAvailableStream() int {

	// Acquire read lock for safe access to channel stream collection
	sw.restreamer.Channel.Mu.RLock()
	defer sw.restreamer.Channel.Mu.RUnlock()

	streamCount := len(sw.restreamer.Channel.Streams)

	// Return failure indicator if insufficient streams for failover
	if streamCount <= 1 {
		return -1
	}

	currentIdx := int(atomic.LoadInt32(&sw.restreamer.CurrentIndex))

	// Evaluate alternative streams using round-robin selection with availability filtering
	for i := 1; i < streamCount; i++ {
		nextIndex := (currentIdx + i) % streamCount
		stream := sw.restreamer.Channel.Streams[nextIndex]

		// Skip streams that have been blocked due to previous failures
		if atomic.LoadInt32(&stream.Blocked) == 1 {
			continue
		}

		// Validate source connection availability without consuming connection slots
		currentConns := atomic.LoadInt32(&stream.Source.ActiveConns)
		if currentConns >= int32(stream.Source.MaxConnections) {
			if sw.restreamer.Config.Debug {
				sw.logger.Printf("[WATCHER] Source at connection limit (%d/%d), skipping: %s",
					currentConns, stream.Source.MaxConnections, stream.Source.Name)
			}
			continue
		}

		// Return first available alternative stream index
		return nextIndex
	}

	// Return failure indicator if no suitable alternatives found
	return -1
}

// NewRestreamWrapper creates a RestreamWrapper instance that bridges watcher
// failover operations with the established restreaming infrastructure, enabling
// seamless integration between monitoring-initiated restart operations and
// comprehensive streaming logic including master playlist handling, source
// selection, and connection management.
//
// Parameters:
//   - restreamer: existing restreamer instance to wrap for restart operations
//
// Returns:
//   - *RestreamWrapper: wrapper providing access to comprehensive streaming functionality
func NewRestreamWrapper(restreamer *types.Restreamer) *RestreamWrapper {
	return &RestreamWrapper{
		Restreamer: restreamer,
	}
}

// Stream leverages the complete established streaming infrastructure through the
// restreaming package, ensuring that watcher-initiated restarts benefit from
// comprehensive source selection algorithms, master playlist processing, connection
// management, and error handling without duplicating complex implementation logic.
//
// The method provides seamless integration between monitoring and streaming
// subsystems while maintaining consistency with manually initiated streaming
// operations and client-driven connection establishment.
func (rw *RestreamWrapper) Stream() {

	// Utilize comprehensive streaming logic through established restream infrastructure
	r := &restream.Restream{Restreamer: rw.Restreamer}
	r.WatcherStream()
}

// getStreamDataFromBuffer retrieves recent stream content from the restreamer's
// ring buffer for FFprobe analysis operations, providing efficient access to
// stream data without creating additional network connections or impacting
// streaming performance. The function implements safe buffer access with
// destruction state validation to prevent crashes during buffer lifecycle transitions.
//
// Buffer data extraction uses configurable size limits (3MB) to provide adequate
// content for comprehensive FFprobe analysis while maintaining memory efficiency
// and preventing excessive buffer read operations that could impact streaming clients.
//
// Returns:
//   - []byte: recent stream data suitable for FFprobe analysis, or nil if buffer unavailable
func (sw *StreamWatcher) getStreamDataFromBuffer() []byte {

	// Validate buffer existence and operational state
	if sw.restreamer.Buffer == nil || sw.restreamer.Buffer.IsDestroyed() {
		return nil
	}

	// Extract recent buffer content with size limit for efficient FFprobe analysis
	// 3MB provides adequate content for comprehensive stream analysis without excessive memory usage
	return sw.restreamer.Buffer.PeekRecentData(3 * 1024 * 1024)
}
