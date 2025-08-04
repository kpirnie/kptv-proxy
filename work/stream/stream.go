package stream

import (
	"sync"
	"sync/atomic"
	"time"
)

// Stream represents a single stream
type Stream struct {
	URL        string
	Name       string
	Attributes map[string]string
	Source     *SourceConfig
	Failures   int32
	LastFail   time.Time
	Blocked    int32
	mu         sync.Mutex
}

// Channel represents a group of streams
type Channel struct {
	Name       string
	Streams    []*Stream
	mu         sync.RWMutex
	restreamer *Restreamer
}

func (sp *StreamProxy) HandleStreamFailure(stream *Stream) {
	failures := atomic.AddInt32(&stream.Failures, 1)

	stream.mu.Lock()
	stream.LastFail = time.Now()
	stream.mu.Unlock()

	if failures >= int32(sp.config.MaxFailuresBeforeBlock) {
		atomic.StoreInt32(&stream.Blocked, 1)
		sp.logger.Printf("Stream blocked due to excessive failures: %s", sp.logURL(stream.URL))
	}
}
