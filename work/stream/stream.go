package stream

import (
	"kptv-proxy/work/config"
	"kptv-proxy/work/types"
	"kptv-proxy/work/utils"
	"log"
	"sync/atomic"
	"time"
)

func HandleStreamFailure(stream *types.Stream, cfg *config.Config, logger *log.Logger) {
	failures := atomic.AddInt32(&stream.Failures, 1)

	stream.Mu.Lock()
	stream.LastFail = time.Now()
	stream.Mu.Unlock()

	if failures >= int32(cfg.MaxFailuresBeforeBlock) {
		atomic.StoreInt32(&stream.Blocked, 1)
		logger.Printf("Stream blocked due to excessive failures: %s", utils.LogURL(cfg, stream.URL))
	}
}
