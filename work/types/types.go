package types

import (
	"context"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"kptv-proxy/work/buffer"
	"kptv-proxy/work/client"
	"kptv-proxy/work/config"

	"go.uber.org/ratelimit"
)

// Stream represents a single stream
type Stream struct {
	URL        string
	Name       string
	Attributes map[string]string
	Source     *config.SourceConfig
	Failures   int32
	LastFail   time.Time
	Blocked    int32
	Mu         sync.Mutex
}

// Channel represents a group of streams
type Channel struct {
	Name       string
	Streams    []*Stream
	Mu         sync.RWMutex
	Restreamer *Restreamer
}

// Restreamer handles single connection to upstream and multiple clients
type Restreamer struct {
	Channel      *Channel
	Clients      sync.Map // Use sync.Map for better concurrent access
	Buffer       *buffer.RingBuffer
	Running      atomic.Bool
	Ctx          context.Context
	Cancel       context.CancelFunc
	CurrentIndex int32
	LastActivity atomic.Int64 // Unix timestamp
	Logger       *log.Logger
	HttpClient   *client.HeaderSettingClient
	RateLimiter  ratelimit.Limiter
	Config       *config.Config // Add config reference for URL obfuscation
}

// RestreamClient represents a connected client
type RestreamClient struct {
	Id       string
	Writer   http.ResponseWriter
	Flusher  http.Flusher
	Done     chan bool
	LastSeen atomic.Int64
}
