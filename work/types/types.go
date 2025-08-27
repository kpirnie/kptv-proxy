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

// StreamType represents the different categories of stream content that the proxy can handle,
// enabling specialized processing logic for different streaming protocols and formats.
// The type system allows the proxy to adapt its behavior based on content characteristics
// while maintaining a unified interface for stream management operations.
type StreamType int

// Stream type constants define the supported streaming protocols and content formats.
// Each type may require different handling strategies for optimal performance and compatibility.
const (
	StreamTypeDirect StreamType = iota // Direct streaming for simple HTTP streams and basic content
	StreamTypeHLS                      // HTTP Live Streaming (HLS) with adaptive bitrate and segment management
)

// Stream represents a single streamable content source with comprehensive metadata,
// reliability tracking, and format-specific configuration. Each stream corresponds to
// a specific quality/variant of a channel and maintains its own failure statistics,
// blocking state, and format-specific properties for optimal proxy handling.
//
// The Stream struct serves as the fundamental unit of content in the proxy system,
// containing all necessary information for source selection, quality assessment,
// failure tracking, and format-specific streaming operations. Thread-safe access
// is ensured through atomic operations for counters and mutex protection for
// complex state updates.
type Stream struct {
	URL         string               // Complete URL of the streamable content source
	Name        string               // Human-readable display name for the stream/channel
	Attributes  map[string]string    // Key-value pairs from M3U8 EXTINF metadata (tvg-id, group-title, etc.)
	Source      *config.SourceConfig // Reference to source configuration for authentication and limits
	Failures    int32                // Atomic counter of consecutive failures for reliability tracking
	LastFail    time.Time            // Timestamp of most recent failure for debugging and analysis
	Blocked     int32                // Atomic flag (0=active, 1=blocked) indicating stream availability
	Mu          sync.Mutex           // Mutex for thread-safe access to non-atomic fields (LastFail, ResolvedURL)
	StreamType  StreamType           // Content type classification for specialized processing logic
	ResolvedURL string               // For HLS master playlists, contains the selected variant URL
	LastChecked time.Time            // Timestamp of most recent stream validation or health check
}

// Channel represents a logical grouping of streams that provide the same content
// at different quality levels or from different sources. Channels implement the
// failover and load balancing logic that enables seamless switching between
// stream variants when failures occur or manual quality selection is requested.
//
// The Channel struct maintains thread-safe access to its stream collection while
// supporting concurrent operations from multiple clients and background maintenance
// tasks. The preferred stream index allows for manual quality selection that
// persists across application restarts and import refresh operations.
type Channel struct {
	Name                 string       // Unique channel identifier used for client requests and internal references
	Streams              []*Stream    // Ordered collection of stream variants for this channel content
	Mu                   sync.RWMutex // Read-write mutex for thread-safe access to streams and restreamer
	Restreamer           *Restreamer  // Active restreaming instance for efficient one-to-many distribution
	PreferredStreamIndex int32        // Atomic storage of user/admin selected preferred stream index
}

// Restreamer manages the complex one-to-many streaming infrastructure that enables
// a single upstream connection to serve multiple concurrent clients efficiently.
// The restreamer handles stream lifecycle management, client registration, buffer
// coordination, and automatic failover between stream sources when quality issues
// or failures are detected.
//
// The Restreamer operates as a sophisticated proxy layer that abstracts the complexity
// of multi-client streaming while providing comprehensive monitoring, health checking,
// and resource management capabilities. All operations are designed for high concurrency
// with minimal contention between client operations and background maintenance tasks.
type Restreamer struct {
	Channel      *Channel                    // Reference to parent channel for stream access and metadata
	Clients      sync.Map                    // Thread-safe map of client ID -> *RestreamClient for concurrent access
	Buffer       *buffer.RingBuffer          // Shared ring buffer for efficient data distribution to multiple clients
	Running      atomic.Bool                 // Atomic flag indicating active streaming state (true=streaming, false=stopped)
	Ctx          context.Context             // Cancellable context for coordinated shutdown and timeout management
	Cancel       context.CancelFunc          // Context cancellation function for graceful streaming termination
	CurrentIndex int32                       // Atomic storage of currently active stream index within channel
	LastActivity atomic.Int64                // Atomic Unix timestamp of most recent streaming activity for cleanup logic
	Logger       *log.Logger                 // Application logger for debugging and operational event recording
	HttpClient   *client.HeaderSettingClient // HTTP client with custom header support for source authentication
	Config       *config.Config              // Application configuration reference for URL obfuscation and operational parameters
	RateLimiter  ratelimit.Limiter
}

// RestreamClient represents an individual client connection receiving streamed content
// from a restreamer instance. Each client maintains its own connection state, activity
// tracking, and completion signaling while sharing the same upstream data stream
// with other concurrent clients for optimal bandwidth utilization.
//
// The RestreamClient struct encapsulates all client-specific state and communication
// channels while providing thread-safe access to activity timestamps and completion
// status. The design enables efficient client management and cleanup without
// disrupting other active client connections.
type RestreamClient struct {
	Id       string              // Unique client identifier for tracking and debugging purposes
	Writer   http.ResponseWriter // HTTP response writer for sending TS/HLS data to the client
	Flusher  http.Flusher        // HTTP flusher interface for real-time data streaming without buffering delays
	Done     chan bool           // Completion signal channel for coordinated client disconnection and cleanup
	LastSeen atomic.Int64        // Atomic Unix timestamp of most recent client activity for inactivity detection
}

// StreamHealthData contains comprehensive stream quality and format information
// gathered through analysis tools like FFprobe for stream monitoring and automatic
// failover decisions. This data enables the proxy to make intelligent choices about
// stream viability and quality characteristics during health monitoring operations.
//
// The health data structure provides essential metrics for stream watcher components
// that need to evaluate stream quality, detect degradation, and trigger failover
// operations when streams become problematic or lose essential characteristics.
type StreamHealthData struct {
	HasVideo   bool    // Indicates presence of valid video stream with proper codec and dimensions
	HasAudio   bool    // Indicates presence of valid audio stream for complete media experience
	Bitrate    int64   // Stream bitrate in bits per second for quality assessment and bandwidth planning
	FPS        float64 // Video frame rate for performance evaluation and playback quality assessment
	Resolution string  // Video resolution in "WIDTHxHEIGHT" format for display capability matching
	Valid      bool    // Overall validity flag indicating successful analysis and usable stream characteristics
}
