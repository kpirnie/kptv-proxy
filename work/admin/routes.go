package admin

import (
	"kptv-proxy/work/middleware"
	"kptv-proxy/work/proxy"
	"kptv-proxy/work/users"
	"net/http"

	"github.com/gorilla/mux"
)

// SetupAdminRoutes configures all HTTP routes for the administrative web interface.
func SetupAdminRoutes(router *mux.Router, sp *proxy.StreamProxy) {

	// Serve static admin assets — public so login/register pages can load CSS
	router.PathPrefix("/static/").Handler(http.StripPrefix("/static/", http.FileServer(http.Dir("/static/"))))

	// Admin UI entry point
	router.HandleFunc("/", users.RequireAuth(handleAdminInterface)).Methods("GET")

	// Configuration endpoints
	router.HandleFunc("/api/config", authCORS(users.PermRead, middleware.GzipMiddleware(handleGetConfig(sp)))).Methods("GET", "OPTIONS")
	router.HandleFunc("/api/config", authCORS(users.PermConfigWrite, handleSetConfig(sp))).Methods("POST", "OPTIONS")

	// Stats endpoint
	router.HandleFunc("/api/stats", authCORS(users.PermRead, middleware.GzipMiddleware(handleGetStats(sp)))).Methods("GET", "OPTIONS")

	// Channel endpoints
	router.HandleFunc("/api/channels", authCORS(users.PermRead, middleware.GzipMiddleware(handleGetAllChannels(sp)))).Methods("GET", "OPTIONS")
	router.HandleFunc("/api/channels/active", authCORS(users.PermRead, middleware.GzipMiddleware(handleGetActiveChannels(sp)))).Methods("GET", "OPTIONS")
	router.HandleFunc("/api/channels/{channel}/streams", authCORS(users.PermRead, middleware.GzipMiddleware(handleGetChannelStreams(sp)))).Methods("GET", "OPTIONS")
	router.HandleFunc("/api/channels/{channel}/stats", authCORS(users.PermRead, middleware.GzipMiddleware(handleGetChannelStats(sp)))).Methods("GET", "OPTIONS")
	router.HandleFunc("/api/channels/{channel}/stream", authCORS(users.PermStreams, handleSetChannelStream(sp))).Methods("POST", "OPTIONS")
	router.HandleFunc("/api/channels/{channel}/kill-stream", authCORS(users.PermStreams, handleKillStream(sp))).Methods("POST", "OPTIONS")
	router.HandleFunc("/api/channels/{channel}/revive-stream", authCORS(users.PermStreams, handleReviveStream(sp))).Methods("POST", "OPTIONS")
	router.HandleFunc("/api/channels/{channel}/order", authCORS(users.PermStreams, handleSetChannelOrder(sp))).Methods("POST", "OPTIONS")
	router.HandleFunc("/api/channels/{channel}/order", authCORS(users.PermStreams, handleResetChannelOrder(sp))).Methods("DELETE")

	// Log endpoints
	router.HandleFunc("/api/logs", authCORS(users.PermLogs, middleware.GzipMiddleware(handleGetLogs))).Methods("GET", "OPTIONS")
	router.HandleFunc("/api/logs", authCORS(users.PermLogs, handleClearLogs)).Methods("DELETE", "OPTIONS")

	// System endpoints
	router.HandleFunc("/api/restart", authCORS(users.PermRestart, handleRestart)).Methods("POST", "OPTIONS")
	router.HandleFunc("/api/watcher/toggle", authCORS(users.PermStreams, handleToggleWatcher(sp))).Methods("POST", "OPTIONS")

	// XC Account endpoints
	router.HandleFunc("/api/xc-accounts", authCORS(users.PermXCAccounts, middleware.GzipMiddleware(handleGetXCAccounts(sp)))).Methods("GET", "OPTIONS")
	router.HandleFunc("/api/xc-accounts", authCORS(users.PermXCAccounts, handleCreateXCAccount(sp))).Methods("POST", "OPTIONS")
	router.HandleFunc("/api/xc-accounts/{id}", authCORS(users.PermXCAccounts, handleUpdateXCAccount(sp))).Methods("PUT", "OPTIONS")
	router.HandleFunc("/api/xc-accounts/{id}", authCORS(users.PermXCAccounts, handleDeleteXCAccount(sp))).Methods("DELETE", "OPTIONS")

	// EPG endpoints
	router.HandleFunc("/api/epgs", authCORS(users.PermEPGs, middleware.GzipMiddleware(handleGetEPGs(sp)))).Methods("GET", "OPTIONS")
	router.HandleFunc("/api/epgs", authCORS(users.PermEPGs, handleCreateEPG(sp))).Methods("POST", "OPTIONS")
	router.HandleFunc("/api/epgs/{id}", authCORS(users.PermEPGs, handleUpdateEPG(sp))).Methods("PUT", "OPTIONS")
	router.HandleFunc("/api/epgs/{id}", authCORS(users.PermEPGs, handleDeleteEPG(sp))).Methods("DELETE", "OPTIONS")

	// EPG channel mapping endpoints
	router.HandleFunc("/api/channels/{channel}/epg", authCORS(users.PermRead, middleware.GzipMiddleware(handleGetChannelEPG(sp)))).Methods("GET", "OPTIONS")
	router.HandleFunc("/api/channels/{channel}/epg", authCORS(users.PermEPGs, handleSetChannelEPG(sp))).Methods("POST", "OPTIONS")
	router.HandleFunc("/api/channels/{channel}/epg", authCORS(users.PermEPGs, handleDeleteChannelEPG(sp))).Methods("DELETE", "OPTIONS")
	router.HandleFunc("/api/epg/search", authCORS(users.PermRead, middleware.GzipMiddleware(handleSearchEPGChannels(sp)))).Methods("GET", "OPTIONS")
	router.HandleFunc("/api/channels/epg-mappings", authCORS(users.PermRead, middleware.GzipMiddleware(handleGetAllChannelEPGs(sp)))).Methods("GET", "OPTIONS")
	router.HandleFunc("/api/epgs/refresh", authCORS(users.PermEPGs, handleRefreshEPG(sp))).Methods("POST", "OPTIONS")

	// Local source endpoints
	router.HandleFunc("/api/local-sources", authCORS(users.PermRead, middleware.GzipMiddleware(handleGetLocalSources(sp)))).Methods("GET", "OPTIONS")
	router.HandleFunc("/api/local-sources", authCORS(users.PermConfigWrite, handleCreateLocalSource(sp))).Methods("POST", "OPTIONS")
	router.HandleFunc("/api/local-sources/scan", authCORS(users.PermConfigWrite, handleScanAllLocalSources(sp))).Methods("POST", "OPTIONS")
	router.HandleFunc("/api/local-sources/{id}", authCORS(users.PermConfigWrite, handleUpdateLocalSource(sp))).Methods("PUT", "OPTIONS")
	router.HandleFunc("/api/local-sources/{id}", authCORS(users.PermConfigWrite, handleDeleteLocalSource(sp))).Methods("DELETE", "OPTIONS")
	router.HandleFunc("/api/local-sources/{id}/scan", authCORS(users.PermConfigWrite, handleScanLocalSource(sp))).Methods("POST", "OPTIONS")

	// Local media endpoints
	router.HandleFunc("/api/local-media", authCORS(users.PermRead, middleware.GzipMiddleware(handleGetLocalMedia(sp)))).Methods("GET", "OPTIONS")
	router.HandleFunc("/api/local-media/{hash}", authCORS(users.PermRead, middleware.GzipMiddleware(handleGetLocalMediaEntry(sp)))).Methods("GET", "OPTIONS")
	router.HandleFunc("/api/local-media/{hash}", authCORS(users.PermConfigWrite, handleUpdateLocalMedia(sp))).Methods("PUT", "OPTIONS")
	router.HandleFunc("/api/local-media/{hash}/art/{kind}", authCORS(users.PermRead, handleGetLocalMediaArt(sp))).Methods("GET", "OPTIONS")

	// Schedules Direct endpoints
	router.HandleFunc("/api/sd-accounts", authCORS(users.PermSD, middleware.GzipMiddleware(handleGetSDAccounts(sp)))).Methods("GET", "OPTIONS")
	router.HandleFunc("/api/sd-accounts", authCORS(users.PermSD, handleCreateSDAccount(sp))).Methods("POST", "OPTIONS")
	router.HandleFunc("/api/sd-accounts/{id}", authCORS(users.PermSD, handleUpdateSDAccount(sp))).Methods("PUT", "OPTIONS")
	router.HandleFunc("/api/sd-accounts/{id}", authCORS(users.PermSD, handleDeleteSDAccount(sp))).Methods("DELETE", "OPTIONS")
	router.HandleFunc("/api/sd/discover", authCORS(users.PermSD, handleSDDiscover(sp))).Methods("POST", "OPTIONS")

	// API reference docs
	router.HandleFunc("/api-docs", users.RequireAuth(func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "/static/api-docs.html")
	})).Methods("GET")

	addLogEntry("info", "Admin interface initialized")
}

// corsMiddleware provides CORS support for admin API endpoints.
func corsMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next(w, r)
	}
}

// authCORS wraps a handler so CORS headers and preflight are handled before the
// auth check. Registering OPTIONS inside the auth wrapper 401s every preflight
// and defeats the CORS layer entirely.
func authCORS(perm int, next http.HandlerFunc) http.HandlerFunc {
	return corsMiddleware(users.RequireAuthWithPerm(perm, next))
}
