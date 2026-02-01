package stream

import (
	"kptv-proxy/work/config"
	"kptv-proxy/work/deadstreams"
	"kptv-proxy/work/logger"
	"kptv-proxy/work/types"
	"kptv-proxy/work/utils"
	"sync/atomic"
	"time"
)

/**
 * HandleStreamFailure processes stream failure events by incrementing failure counters,
 * updating failure timestamps, and implementing automatic stream blocking when failure
 * thresholds are exceeded.
 *
 * This function serves as the central failure management system for the IPTV proxy,
 * ensuring that persistently problematic streams are automatically removed from active
 * rotation to prevent degraded user experience.
 *
 * The failure handling process implements a sophisticated threshold-based blocking system
 * that considers both the total number of failures and source-specific configuration
 * parameters. When streams exceed their configured failure limits, they are automatically
 * blocked from future use and recorded in the persistent dead streams database for
 * administrative review and monitoring.
 *
 * Key operations:
 *  1. Atomic increment of stream failure counter for thread-safe tracking
 *  2. Timestamp recording of the most recent failure for debugging and analysis
 *  3. Source-specific failure threshold evaluation with configurable limits
 *  4. Automatic stream blocking when thresholds are exceeded
 *  5. Dead stream database integration for persistent failure tracking
 *  6. Comprehensive debug logging for operational monitoring
 *
 * Thread Safety: This function is fully thread-safe and can be called concurrently
 * from multiple goroutines without risk of race conditions or data corruption.
 *
 * @param stream Pointer to the Stream object that experienced the failure
 * @param cfg Application configuration containing source definitions and failure thresholds
 * @param channelName Name of the channel containing the failed stream for identification
 * @param streamIndex Zero-based index of the failed stream within the channel's stream list
 */
func HandleStreamFailure(stream *types.Stream, cfg *config.Config, channelName string, streamIndex int) {
	logger.Debug("{stream/stream - HandleStreamFailure} Processing failure for channel %s, stream index %d: %s",
		channelName, streamIndex, utils.LogURL(cfg, stream.URL))

	// Atomically increment failure counter to ensure thread-safe access across goroutines
	// The returned value reflects the new total failure count after increment
	failures := atomic.AddInt32(&stream.Failures, 1)
	logger.Debug("{stream/stream - HandleStreamFailure} Failure count for channel %s, stream %d: %d (incremented)",
		channelName, streamIndex, failures)

	// Update failure timestamp with mutex protection for thread-safe write access
	// This timestamp is used for debugging, monitoring, and failure pattern analysis
	stream.Mu.Lock()
	stream.LastFail = time.Now()
	logger.Debug("{stream/stream - HandleStreamFailure} Updated last failure timestamp for channel %s, stream %d: %s",
		channelName, streamIndex, stream.LastFail.Format(time.RFC3339))
	stream.Mu.Unlock()

	// Determine failure threshold based on source-specific configuration
	// Each source can have different reliability expectations and failure tolerances
	source := cfg.GetSourceByURL(stream.Source.URL)
	maxFailures := int32(5) // Conservative default threshold for unknown sources
	if source != nil {
		logger.Debug("{stream/stream - HandleStreamFailure} Found source config for channel %s, stream %d: %s (max failures: %d)",
			channelName, streamIndex, source.Name, source.MaxFailuresBeforeBlock)
		// Use source-specific threshold allowing per-provider customization
		maxFailures = int32(source.MaxFailuresBeforeBlock)
	} else {
		logger.Debug("{stream/stream - HandleStreamFailure} No source config found for channel %s, stream %d, using default max failures: %d",
			channelName, streamIndex, maxFailures)
	}

	logger.Debug("{stream/stream - HandleStreamFailure} Threshold check for channel %s, stream %d: failures=%d, max=%d",
		channelName, streamIndex, failures, maxFailures)

	// Evaluate whether failure count has reached the blocking threshold
	if failures >= maxFailures {
		logger.Warn("{stream/stream - HandleStreamFailure} Threshold exceeded for channel %s, stream %d: %d >= %d failures",
			channelName, streamIndex, failures, maxFailures)

		// Atomically mark stream as blocked to prevent future usage attempts
		// The blocked flag is checked during stream selection to skip problematic streams
		atomic.StoreInt32(&stream.Blocked, 1)
		logger.Debug("{stream/stream - HandleStreamFailure} Stream marked as blocked for channel %s, stream %d",
			channelName, streamIndex)

		// Log blocking event with failure statistics for operational monitoring
		logger.Warn("{stream/stream - HandleStreamFailure} Stream auto-blocked due to excessive failures (%d/%d) for channel %s, stream %d: %s",
			failures, maxFailures, channelName, streamIndex, utils.LogURL(cfg, stream.URL))

		// Record stream as dead in persistent database for administrative tracking
		// This enables monitoring of problematic streams and provider quality analysis
		logger.Debug("{stream/stream - HandleStreamFailure} Marking stream as dead in database for channel %s, stream %d",
			channelName, streamIndex)

		err := deadstreams.MarkStreamDead(
			channelName,        // Channel identifier for stream location
			streamIndex,        // Stream index within channel for precise identification
			stream.URL,         // Complete stream URL for debugging and verification
			stream.Source.Name, // Source name for provider tracking and analysis
			"auto_blocked",     // Reason categorization for failure analysis
		)

		// Handle dead stream recording errors gracefully without disrupting operation
		if err != nil {
			logger.Error("{stream/stream - HandleStreamFailure} Failed to mark auto-blocked stream as dead in database for channel %s, stream %d: %v",
				channelName, streamIndex, err)
		} else {
			// Confirm successful dead stream recording for audit trail
			logger.Debug("{stream/stream - HandleStreamFailure} Successfully marked stream as dead in database for channel %s, stream %d: %s",
				channelName, streamIndex, utils.LogURL(cfg, stream.URL))
		}
	} else {
		logger.Debug("{stream/stream - HandleStreamFailure} Stream failure recorded but threshold not exceeded for channel %s, stream %d: %d/%d failures",
			channelName, streamIndex, failures, maxFailures)
	}
}
