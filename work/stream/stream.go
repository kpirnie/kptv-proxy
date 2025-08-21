package stream

import (
	"kptv-proxy/work/config"
	"kptv-proxy/work/types"
	"kptv-proxy/work/utils"
	"log"
	"sync/atomic"
	"time"
)

// handle the stream failers
func HandleStreamFailure(stream *types.Stream, cfg *config.Config, logger *log.Logger) {
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
	}
}
