package stream

import (
	"kptv-proxy/work/config"
	"kptv-proxy/work/deadstreams"
	"kptv-proxy/work/types"
	"kptv-proxy/work/utils"
	"log"
	"sync/atomic"
	"time"
)

// HandleStreamFailure processes stream failure events by incrementing failure counters,
// updating failure timestamps, and implementing automatic stream blocking when failure
// thresholds are exceeded. The function serves as the central failure management system
// for the IPTV proxy, ensuring that persistently problematic streams are automatically
// removed from active rotation to prevent degraded user experience.
//
// The failure handling process implements a sophisticated threshold-based blocking system
// that considers both the total number of failures and source-specific configuration
// parameters. When streams exceed their configured failure limits, they are automatically
// blocked from future use and recorded in the persistent dead streams database for
// administrative review and monitoring.
//
// The function performs several critical operations:
//  1. Atomic increment of stream failure counter for thread-safe tracking
//  2. Timestamp recording of the most recent failure for debugging and analysis
//  3. Source-specific failure threshold evaluation with configurable limits
//  4. Automatic stream blocking when thresholds are exceeded
//  5. Dead stream database integration for persistent failure tracking
//  6. Comprehensive debug logging for operational monitoring
//
// Thread Safety: This function is fully thread-safe and can be called concurrently
// from multiple goroutines without risk of race conditions or data corruption.
//
// Parameters:
//   - stream: pointer to the Stream object that experienced the failure
//   - cfg: application configuration containing source definitions and failure thresholds
//   - logger: application logger for debug output and operational event recording
//   - channelName: name of the channel containing the failed stream for identification
//   - streamIndex: zero-based index of the failed stream within the channel's stream list
func HandleStreamFailure(stream *types.Stream, cfg *config.Config, logger *log.Logger, channelName string, streamIndex int) {

	// Atomically increment failure counter to ensure thread-safe access across goroutines
	// The returned value reflects the new total failure count after increment
	failures := atomic.AddInt32(&stream.Failures, 1)

	// Update failure timestamp with mutex protection for thread-safe write access
	// This timestamp is used for debugging, monitoring, and failure pattern analysis
	stream.Mu.Lock()
	stream.LastFail = time.Now()
	stream.Mu.Unlock()

	// Determine failure threshold based on source-specific configuration
	// Each source can have different reliability expectations and failure tolerances
	source := cfg.GetSourceByURL(stream.Source.URL)
	maxFailures := int32(5) // Conservative default threshold for unknown sources
	if source != nil {

		// Use source-specific threshold allowing per-provider customization
		maxFailures = int32(source.MaxFailuresBeforeBlock)
	}

	// Evaluate whether failure count has reached the blocking threshold
	if failures >= maxFailures {

		// Atomically mark stream as blocked to prevent future usage attempts
		// The blocked flag is checked during stream selection to skip problematic streams
		atomic.StoreInt32(&stream.Blocked, 1)

		// Log blocking event with failure statistics for operational monitoring
		if cfg.Debug {
			logger.Printf("Stream blocked due to excessive failures (%d/%d): %s",
				failures, maxFailures, utils.LogURL(cfg, stream.URL))
		}

		// Record stream as dead in persistent database for administrative tracking
		// This enables monitoring of problematic streams and provider quality analysis
		err := deadstreams.MarkStreamDead(
			channelName,        // Channel identifier for stream location
			streamIndex,        // Stream index within channel for precise identification
			stream.URL,         // Complete stream URL for debugging and verification
			stream.Source.Name, // Source name for provider tracking and analysis
			"auto_blocked",     // Reason categorization for failure analysis
		)

		// Handle dead stream recording errors gracefully without disrupting operation
		if err != nil {
			if cfg.Debug {
				logger.Printf("Failed to mark auto-blocked stream as dead in file: %v", err)
			}
		} else {

			// Confirm successful dead stream recording for audit trail
			if cfg.Debug {
				logger.Printf("Auto-blocked stream added to dead streams file: %s (channel: %s, index: %d)",
					utils.LogURL(cfg, stream.URL), channelName, streamIndex)
			}
		}
	}
}
