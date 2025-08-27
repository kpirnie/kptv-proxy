package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// ActiveConnections tracks the current number of active connections per channel.
// This metric is a gauge, meaning it can go up and down as clients connect and disconnect.
var ActiveConnections = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "iptv_proxy_active_connections",
	Help: "Number of active connections",
}, []string{"channel"})

// BytesTransferred tracks the total number of bytes transferred per channel.
// The "direction" label can be used to distinguish between upstream (to the source)
// and downstream (to clients) traffic. This metric is a counter and only increases.
var BytesTransferred = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "iptv_proxy_bytes_transferred",
	Help: "Total bytes transferred",
}, []string{"channel", "direction"})

// StreamErrors counts the number of stream-related errors per channel.
// The "error_type" label allows categorization of different error types (e.g., timeout, decode, etc.).
// This metric is a counter and only increases.
var StreamErrors = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "iptv_proxy_stream_errors",
	Help: "Number of stream errors",
}, []string{"channel", "error_type"})

// ClientsConnected tracks the number of clients currently connected per channel.
// Similar to ActiveConnections, this is a gauge that increases and decreases in real-time.
var ClientsConnected = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "iptv_proxy_clients_connected",
	Help: "Number of clients connected",
}, []string{"channel"})

// StreamSwitches tracks the number of times streams are switched per channel.
// The "reason" label indicates why the switch occurred (e.g., "health_check", "manual", "failure").
var StreamSwitches = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "iptv_proxy_stream_switches_total",
	Help: "Total number of stream switches",
}, []string{"channel", "reason"})

// SourceConnections tracks the current number of active connections per source.
var SourceConnections = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "iptv_proxy_source_connections",
	Help: "Number of active connections per source",
}, []string{"source_name"})

// SourceFailures tracks the total number of failures per source.
var SourceFailures = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "iptv_proxy_source_failures_total",
	Help: "Total number of source failures",
}, []string{"source_name", "failure_type"})

// BufferUsage tracks buffer memory usage across all channels.
var BufferUsage = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "iptv_proxy_buffer_usage_bytes",
	Help: "Buffer memory usage in bytes",
}, []string{"channel"})

// ImportDuration tracks how long import operations take.
var ImportDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
	Name:    "iptv_proxy_import_duration_seconds",
	Help:    "Duration of import operations",
	Buckets: prometheus.DefBuckets,
}, []string{"source_name"})

// ImportSuccess tracks successful vs failed imports per source.
var ImportSuccess = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "iptv_proxy_imports_total",
	Help: "Total number of import attempts",
}, []string{"source_name", "status"})

// CacheOperations tracks cache hit/miss rates.
var CacheOperations = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "iptv_proxy_cache_operations_total",
	Help: "Total cache operations",
}, []string{"operation", "result"})

// DeadStreams tracks the number of streams marked as dead.
var DeadStreams = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "iptv_proxy_dead_streams",
	Help: "Number of streams marked as dead",
}, []string{"channel", "reason"})

// HLSSegments tracks HLS segment processing.
var HLSSegments = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "iptv_proxy_hls_segments_total",
	Help: "Total HLS segments processed",
}, []string{"channel", "status"})

// StreamHealthChecks tracks watcher health check results.
var StreamHealthChecks = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "iptv_proxy_health_checks_total",
	Help: "Total stream health checks performed",
}, []string{"channel", "result"})

// APIRequestDuration tracks API endpoint response times.
var APIRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
	Name:    "iptv_proxy_api_request_duration_seconds",
	Help:    "API request duration",
	Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
}, []string{"endpoint", "method"})

// MasterPlaylistVariants tracks variant selection from master playlists.
var MasterPlaylistVariants = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "iptv_proxy_master_playlist_variants_total",
	Help: "Total master playlist variants processed",
}, []string{"channel", "selected"})
