package watcher

import (
	"context"
	"kptv-proxy/work/constants"
	"kptv-proxy/work/deadstreams"
	"kptv-proxy/work/logger"
	"kptv-proxy/work/restream"
	"kptv-proxy/work/types"
	"kptv-proxy/work/utils"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/puzpuzpuz/xsync/v3"
)

// semaphore to limit concurrent watchers system-wide
var (
	watcherSemaphore = make(chan struct{}, constants.Internal.WatcherMaxConcurrent) // Max 100 concurrent watchers
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
	lastWritePos        int64              // tracks buffer write position between checks for throughput calculation
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

	// recreate so prior Stop() close doesn't kill new routine
	wm.stopChan = make(chan struct{})

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

	// Terminate existing watcher if present, releasing its semaphore slot
	// before acquiring a new one to prevent slot exhaustion on reconnects
	if existing, exists := wm.watchers.LoadAndDelete(channelName); exists {
		logger.Debug("{watcher - StartWatching} Stopping existing watcher for channel: %s", channelName)
		existing.Stop()
		// release the slot the old watcher was holding before we acquired a new one above;
		// without this every client reconnect to an active channel leaks one slot permanently
		<-watcherSemaphore
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

// IsWatching returns true if a watcher is already active for the given channel,
// preventing duplicate watchers from being spawned for each additional client
// connecting to an already-streaming channel.
func (wm *WatcherManager) IsWatching(channelName string) bool {
	_, exists := wm.watchers.Load(channelName)
	return exists
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
	ticker := time.NewTicker(constants.Internal.WatcherCleanupInterval)
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
					// release the slot so new watchers can be created
					<-watcherSemaphore
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
	interval := constants.Internal.WatcherCheckInterval // Standard interval for production monitoring

	// Use more frequent checks in debug mode for development and troubleshooting
	if sw.restreamer.Config.Debug {
		interval = constants.Internal.WatcherDebugCheckInterval
		logger.Debug("{watcher - Watch} Debug mode enabled, using %v interval for channel: %s", interval, sw.channelName)
	}

	// wait until the restreamer is actually running before starting the grace period clock
	for !sw.restreamer.Running.Load() {
		select {
		case <-sw.ctx.Done():
			return
		case <-time.After(constants.Internal.WatcherPollingInterval):
		}
	}
	sw.lastStreamStart = time.Now()

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

	// see if the stream has issues
	hasIssues := sw.evaluateStreamHealthFromState()
	if hasIssues {
		consecutiveFailures := atomic.AddInt32(&sw.consecutiveFailures, 1)
		totalFailures := atomic.AddInt32(&sw.totalFailures, 1)

		logger.Warn("{watcher - checkStreamHealth} Channel %s: Health issue detected (consecutive: %d/3, total: %d/3)",
			sw.channelName, consecutiveFailures, totalFailures)

		// trigger on total failures alone - consecutive resets too easily on bursty streams
		if totalFailures >= constants.Internal.WatcherFailureThreshold {
			logger.Warn("{watcher - checkStreamHealth} Channel %s: Failure threshold exceeded, triggering stream switch", sw.channelName)
			sw.triggerStreamSwitch("persistent_failures")
			atomic.StoreInt32(&sw.consecutiveFailures, 0)
			atomic.StoreInt32(&sw.totalFailures, 0)
			sw.lastFailureReset = time.Now()
		}
	} else {
		// require 2 consecutive passes before clearing failure history
		// prevents bursty streams from resetting the counter on a single good tick
		oldConsecutiveFailures := atomic.LoadInt32(&sw.consecutiveFailures)
		if oldConsecutiveFailures > 0 {
			newVal := atomic.AddInt32(&sw.consecutiveFailures, -1)
			if newVal == 0 {
				logger.Debug("{watcher - checkStreamHealth} Channel %s: Health recovered after 2 consecutive passes", sw.channelName)
			}
		}

		if time.Since(sw.lastFailureReset) > constants.Internal.WatcherFailureResetWindow {
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
	if time.Since(sw.lastStreamStart) < constants.Internal.WatcherGracePeriod {
		logger.Debug("{watcher - evaluateStreamHealthFromState} Channel %s: In grace period (%v elapsed), skipping health check",
			sw.channelName, time.Since(sw.lastStreamStart))
		return false
	}

	hasIssues := false

	// Capture buffer reference locally to prevent nil pointer race
	// between the nil check and subsequent calls if another goroutine
	// sets Buffer = nil during cleanup
	buf := sw.restreamer.Buffer
	if buf != nil && !buf.IsDestroyed() {
		currentWritePos := buf.GetWritePosition()

		if sw.lastWritePos == 0 {
			// first check after start or after buffer recreation —
			// just record position and skip throughput evaluation
			// to avoid a false stall detection on a fresh baseline
			sw.lastWritePos = currentWritePos
		} else if currentWritePos >= sw.lastWritePos {
			bytesSinceLastCheck := currentWritePos - sw.lastWritePos
			if bytesSinceLastCheck < constants.Internal.WatcherLowThroughputBytes {
				logger.Warn("{watcher - evaluateStreamHealthFromState} Channel %s: Throughput too low (%d KB since last check), stream likely stalled",
					sw.channelName, bytesSinceLastCheck/1024)
				hasIssues = true
			}
			sw.lastWritePos = currentWritePos
		} else {
			// write position went backwards — buffer was destroyed and recreated;
			// reset to zero so the next check establishes a clean baseline
			logger.Debug("{watcher - evaluateStreamHealthFromState} Channel %s: Buffer write position reset detected, re-baselining throughput check",
				sw.channelName)
			sw.lastWritePos = 0
		}
	} else {
		// buffer is gone — reset so we baseline cleanly when it comes back
		sw.lastWritePos = 0
	}

	// Check stream activity
	lastActivity := sw.restreamer.LastActivity.Load()
	timeSinceActivity := time.Now().Unix() - lastActivity

	if timeSinceActivity > constants.Internal.WatcherActivityTimeout {
		logger.Warn("{watcher - evaluateStreamHealthFromState} Channel %s: No activity for %d seconds (threshold: 120s)",
			sw.channelName, timeSinceActivity)
		hasIssues = true
	}

	// Only flag context cancellation as an issue if the restreamer has been stuck
	// in a cancelled state for an extended period - transient cancellations during
	// normal switching are expected and should not trigger failover
	if sw.restreamer.Running.Load() {
		select {
		case <-sw.restreamer.Ctx.Done():
			if time.Since(sw.lastStreamStart) > constants.Internal.WatcherContextStuckTimeout {
				logger.Warn("{watcher - evaluateStreamHealthFromState} Channel %s: Context cancelled and stream has been running for >300s",
					sw.channelName)
				hasIssues = true
			}
		default:
		}
	}

	// Reuse stats already collected by restream/stats instead of racing with a second FFprobe
	sw.restreamer.Stats.Mu.RLock()
	statsValid := sw.restreamer.Stats.Valid
	statsUpdated := sw.restreamer.Stats.LastUpdated
	sw.restreamer.Stats.Mu.RUnlock()

	// If stats exist but haven't been updated in 5 minutes, treat as stale
	if statsValid && time.Since(time.Unix(statsUpdated, 0)) > constants.Internal.WatcherStatsStaleTimeout {
		logger.Warn("{watcher - evaluateStreamHealthFromState} Channel %s: Stream stats stale for >5 minutes", sw.channelName)
		hasIssues = true
	}

	return hasIssues
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

	// validate the chosen stream is still usable at switch time, not just at find time
	sw.restreamer.Channel.Mu.RLock()
	streamStillValid := nextIndex < len(sw.restreamer.Channel.Streams) &&
		atomic.LoadInt32(&sw.restreamer.Channel.Streams[nextIndex].Blocked) == 0 &&
		sw.restreamer.Channel.Streams[nextIndex].Source.ActiveConns.Load() < int32(sw.restreamer.Channel.Streams[nextIndex].Source.MaxConnections)
	sw.restreamer.Channel.Mu.RUnlock()

	if !streamStillValid {
		logger.Warn("{watcher - triggerStreamSwitch} Channel %s: Stream %d became unavailable before switch could execute, aborting",
			sw.channelName, nextIndex)
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

	// mark a switch in progress so RemoveClient does not call stopStream()
	// and kill the new stream before it has a chance to start
	sw.restreamer.Switching.Store(true)

	// replace the notify channel and close the old one to unblock all
	// HandleRestreamingClient goroutines, forcing clients to reconnect
	// cleanly after the stream source changes
	oldNotify := sw.restreamer.SwitchNotify
	sw.restreamer.SwitchNotify = make(chan struct{})
	close(oldNotify)

	// Update preferred stream index atomically
	atomic.StoreInt32(&sw.restreamer.Channel.PreferredStreamIndex, int32(newIndex))
	atomic.StoreInt32(&sw.restreamer.CurrentIndex, int32(newIndex))

	logger.Debug("{watcher - forceStreamRestart} Channel %s: Updated stream indices to %d", sw.channelName, newIndex)

	// signal that this is a controlled switch so the Stream() loop
	// does not misclassify the context cancellation as a failure
	sw.restreamer.ManualSwitch.Store(true)

	// capture old cancel before reassignment so the running goroutine
	// receives the stop signal during the deadline-wait below
	oldCancel := sw.restreamer.Cancel

	// Gracefully terminate current streaming operations
	deadline := time.Now().Add(constants.Internal.WatcherRestartDeadline)
	for time.Now().Before(deadline) {
		if !sw.restreamer.Running.Load() {
			break
		}
		// signal the old goroutine to exit on first iteration, then poll
		oldCancel()
		time.Sleep(constants.Internal.WatcherRestartPollInterval)
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

	defer func() {
		if rec := recover(); rec != nil {
			logger.Error("{watcher - restartWithExistingLogic} Channel %s: Recovered from panic: %v",
				sw.channelName, rec)
		}
		// clear the switching flag now that the new stream goroutine has finished —
		// keeping it true for the full duration prevents RemoveClient from calling
		// stopStream() while the new stream is starting up
		sw.restreamer.Switching.Store(false)
		sw.restreamer.Running.Store(false)
		logger.Debug("{watcher - restartWithExistingLogic} Channel %s: Restart completed, running=false", sw.channelName)
	}()

	currentIdx := int(atomic.LoadInt32(&sw.restreamer.CurrentIndex))
	logger.Debug("{watcher - restartWithExistingLogic} Channel %s: Starting stream restart at index %d",
		sw.channelName, currentIdx)

	r := &restream.Restream{Restreamer: sw.restreamer}

	// These are normally started by AddClient but are not running after a watcher-triggered switch
	r.RestartMonitors()
	r.WatcherStream()

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

		// Skip manually dead streams — these must never be selected for failover
		if deadstreams.IsStreamDead(sw.channelName, stream.URLHash) {
			logger.Debug("{watcher - findNextAvailableStream} Channel %s: Stream %d is dead, skipping",
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
