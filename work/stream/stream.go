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

// handle the stream failures
func HandleStreamFailure(stream *types.Stream, cfg *config.Config, logger *log.Logger, channelName string, streamIndex int) {
	failures := atomic.AddInt32(&stream.Failures, 1)

	stream.Mu.Lock()
	stream.LastFail = time.Now()
	stream.Mu.Unlock()

	// Get the source-specific failure threshold
	source := cfg.GetSourceByURL(stream.Source.URL)
	maxFailures := int32(5) // default
	if source != nil {
		maxFailures = int32(source.MaxFailuresBeforeBlock)
	}

	if failures >= maxFailures {
		atomic.StoreInt32(&stream.Blocked, 1)

		if cfg.Debug {
			logger.Printf("Stream blocked due to excessive failures (%d/%d): %s",
				failures, maxFailures, utils.LogURL(cfg, stream.URL))
		}

		// Add to dead streams file
		err := deadstreams.MarkStreamDead(
			channelName,
			streamIndex,
			stream.URL,
			stream.Source.Name,
			"auto_blocked",
		)

		if err != nil {
			if cfg.Debug {
				logger.Printf("Failed to mark auto-blocked stream as dead in file: %v", err)
			}
		} else {
			if cfg.Debug {
				logger.Printf("Auto-blocked stream added to dead streams file: %s (channel: %s, index: %d)",
					utils.LogURL(cfg, stream.URL), channelName, streamIndex)
			}
		}
	}
}
