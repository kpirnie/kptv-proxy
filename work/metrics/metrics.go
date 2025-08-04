package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics
var (
	ActiveConnections = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "iptv_proxy_active_connections",
		Help: "Number of active connections",
	}, []string{"channel"})

	BytesTransferred = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "iptv_proxy_bytes_transferred",
		Help: "Total bytes transferred",
	}, []string{"channel", "direction"})

	StreamErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "iptv_proxy_stream_errors",
		Help: "Number of stream errors",
	}, []string{"channel", "error_type"})

	ClientsConnected = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "iptv_proxy_clients_connected",
		Help: "Number of clients connected",
	}, []string{"channel"})
)
