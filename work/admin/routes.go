package admin

import (
	"kptv-proxy/work/middleware"
	"kptv-proxy/work/proxy"
	"net/http"

	"github.com/gorilla/mux"
)

// SetupAdminRoutes configures all HTTP routes for the administrative web interface,
// including static file serving, API endpoints, and CORS middleware application.
// This is the primary entry point for the admin package, called from main.go
// during application initialization.
//
// Parameters:
//   - router: configured mux router for route registration
//   - proxyInstance: StreamProxy instance for API operations
func SetupAdminRoutes(router *mux.Router, sp *proxy.StreamProxy) {
	// Serve static admin assets from the container static directory
	router.PathPrefix("/static/").Handler(http.StripPrefix("/static/", http.FileServer(http.Dir("/static/"))))

	// Admin UI entry point
	router.HandleFunc("/", handleAdminInterface).Methods("GET")

	// Configuration endpoints
	router.HandleFunc("/api/config", corsMiddleware(middleware.GzipMiddleware(handleGetConfig(sp)))).Methods("GET", "OPTIONS")
	router.HandleFunc("/api/config", corsMiddleware(handleSetConfig(sp))).Methods("POST", "OPTIONS")

	// Stats endpoint
	router.HandleFunc("/api/stats", corsMiddleware(middleware.GzipMiddleware(handleGetStats(sp)))).Methods("GET", "OPTIONS")

	// Channel endpoints
	router.HandleFunc("/api/channels", corsMiddleware(middleware.GzipMiddleware(handleGetAllChannels(sp)))).Methods("GET", "OPTIONS")
	router.HandleFunc("/api/channels/active", corsMiddleware(middleware.GzipMiddleware(handleGetActiveChannels(sp)))).Methods("GET", "OPTIONS")
	router.HandleFunc("/api/channels/{channel}/streams", corsMiddleware(middleware.GzipMiddleware(handleGetChannelStreams(sp)))).Methods("GET", "OPTIONS")
	router.HandleFunc("/api/channels/{channel}/stats", corsMiddleware(middleware.GzipMiddleware(handleGetChannelStats(sp)))).Methods("GET", "OPTIONS")
	router.HandleFunc("/api/channels/{channel}/stream", corsMiddleware(handleSetChannelStream(sp))).Methods("POST", "OPTIONS")
	router.HandleFunc("/api/channels/{channel}/kill-stream", corsMiddleware(handleKillStream(sp))).Methods("POST", "OPTIONS")
	router.HandleFunc("/api/channels/{channel}/revive-stream", corsMiddleware(handleReviveStream(sp))).Methods("POST", "OPTIONS")
	router.HandleFunc("/api/channels/{channel}/order", corsMiddleware(handleSetChannelOrder(sp))).Methods("POST", "OPTIONS")

	// Log endpoints
	router.HandleFunc("/api/logs", corsMiddleware(middleware.GzipMiddleware(handleGetLogs))).Methods("GET", "OPTIONS")
	router.HandleFunc("/api/logs", corsMiddleware(handleClearLogs)).Methods("DELETE", "OPTIONS")

	// System endpoints
	router.HandleFunc("/api/restart", corsMiddleware(handleRestart)).Methods("POST", "OPTIONS")
	router.HandleFunc("/api/watcher/toggle", corsMiddleware(handleToggleWatcher(sp))).Methods("POST", "OPTIONS")

	// Schedules Direct endpoints
	router.HandleFunc("/api/sd/discover", corsMiddleware(handleSDDiscover(sp))).Methods("POST", "OPTIONS")

	addLogEntry("info", "Admin interface initialized")
}

// corsMiddleware provides Cross-Origin Resource Sharing (CORS) support for admin API
// endpoints, enabling web-based admin interfaces to access the API from different
// origins while maintaining security through appropriate header management and
// preflight request handling.
func corsMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		// Handle preflight OPTIONS requests immediately without passing to handler
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next(w, r)
	}
}
