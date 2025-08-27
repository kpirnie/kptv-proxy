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
