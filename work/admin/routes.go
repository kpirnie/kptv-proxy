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
	router.HandleFunc("/api/config", users.RequireAuthWithPerm(users.PermRead, corsMiddleware(middleware.GzipMiddleware(handleGetConfig(sp))))).Methods("GET", "OPTIONS")
	router.HandleFunc("/api/config", users.RequireAuthWithPerm(users.PermConfigWrite, corsMiddleware(handleSetConfig(sp)))).Methods("POST", "OPTIONS")

	// Stats endpoint
	router.HandleFunc("/api/stats", users.RequireAuthWithPerm(users.PermRead, corsMiddleware(middleware.GzipMiddleware(handleGetStats(sp))))).Methods("GET", "OPTIONS")

	// Channel endpoints
	router.HandleFunc("/api/channels", users.RequireAuthWithPerm(users.PermRead, corsMiddleware(middleware.GzipMiddleware(handleGetAllChannels(sp))))).Methods("GET", "OPTIONS")
	router.HandleFunc("/api/channels/active", users.RequireAuthWithPerm(users.PermRead, corsMiddleware(middleware.GzipMiddleware(handleGetActiveChannels(sp))))).Methods("GET", "OPTIONS")
	router.HandleFunc("/api/channels/{channel}/streams", users.RequireAuthWithPerm(users.PermRead, corsMiddleware(middleware.GzipMiddleware(handleGetChannelStreams(sp))))).Methods("GET", "OPTIONS")
	router.HandleFunc("/api/channels/{channel}/stats", users.RequireAuthWithPerm(users.PermRead, corsMiddleware(middleware.GzipMiddleware(handleGetChannelStats(sp))))).Methods("GET", "OPTIONS")
	router.HandleFunc("/api/channels/{channel}/stream", users.RequireAuthWithPerm(users.PermStreams, corsMiddleware(handleSetChannelStream(sp)))).Methods("POST", "OPTIONS")
	router.HandleFunc("/api/channels/{channel}/kill-stream", users.RequireAuthWithPerm(users.PermStreams, corsMiddleware(handleKillStream(sp)))).Methods("POST", "OPTIONS")
	router.HandleFunc("/api/channels/{channel}/revive-stream", users.RequireAuthWithPerm(users.PermStreams, corsMiddleware(handleReviveStream(sp)))).Methods("POST", "OPTIONS")
	router.HandleFunc("/api/channels/{channel}/order", users.RequireAuthWithPerm(users.PermStreams, corsMiddleware(handleSetChannelOrder(sp)))).Methods("POST", "OPTIONS")

	// Log endpoints
	router.HandleFunc("/api/logs", users.RequireAuthWithPerm(users.PermLogs, corsMiddleware(middleware.GzipMiddleware(handleGetLogs)))).Methods("GET", "OPTIONS")
	router.HandleFunc("/api/logs", users.RequireAuthWithPerm(users.PermLogs, corsMiddleware(handleClearLogs))).Methods("DELETE", "OPTIONS")

	// System endpoints
	router.HandleFunc("/api/restart", users.RequireAuthWithPerm(users.PermRestart, corsMiddleware(handleRestart))).Methods("POST", "OPTIONS")
	router.HandleFunc("/api/watcher/toggle", users.RequireAuthWithPerm(users.PermStreams, corsMiddleware(handleToggleWatcher(sp)))).Methods("POST", "OPTIONS")

	// XC Account endpoints
	router.HandleFunc("/api/xc-accounts", users.RequireAuthWithPerm(users.PermXCAccounts, corsMiddleware(middleware.GzipMiddleware(handleGetXCAccounts(sp))))).Methods("GET", "OPTIONS")
	router.HandleFunc("/api/xc-accounts", users.RequireAuthWithPerm(users.PermXCAccounts, corsMiddleware(handleCreateXCAccount(sp)))).Methods("POST", "OPTIONS")
	router.HandleFunc("/api/xc-accounts/{id}", users.RequireAuthWithPerm(users.PermXCAccounts, corsMiddleware(handleUpdateXCAccount(sp)))).Methods("PUT", "OPTIONS")
	router.HandleFunc("/api/xc-accounts/{id}", users.RequireAuthWithPerm(users.PermXCAccounts, corsMiddleware(handleDeleteXCAccount(sp)))).Methods("DELETE", "OPTIONS")

	// EPG endpoints
	router.HandleFunc("/api/epgs", users.RequireAuthWithPerm(users.PermEPGs, corsMiddleware(middleware.GzipMiddleware(handleGetEPGs(sp))))).Methods("GET", "OPTIONS")
	router.HandleFunc("/api/epgs", users.RequireAuthWithPerm(users.PermEPGs, corsMiddleware(handleCreateEPG(sp)))).Methods("POST", "OPTIONS")
	router.HandleFunc("/api/epgs/{id}", users.RequireAuthWithPerm(users.PermEPGs, corsMiddleware(handleUpdateEPG(sp)))).Methods("PUT", "OPTIONS")
	router.HandleFunc("/api/epgs/{id}", users.RequireAuthWithPerm(users.PermEPGs, corsMiddleware(handleDeleteEPG(sp)))).Methods("DELETE", "OPTIONS")

	// Schedules Direct endpoints
	router.HandleFunc("/api/sd-accounts", users.RequireAuthWithPerm(users.PermSD, corsMiddleware(middleware.GzipMiddleware(handleGetSDAccounts(sp))))).Methods("GET", "OPTIONS")
	router.HandleFunc("/api/sd-accounts", users.RequireAuthWithPerm(users.PermSD, corsMiddleware(handleCreateSDAccount(sp)))).Methods("POST", "OPTIONS")
	router.HandleFunc("/api/sd-accounts/{id}", users.RequireAuthWithPerm(users.PermSD, corsMiddleware(handleUpdateSDAccount(sp)))).Methods("PUT", "OPTIONS")
	router.HandleFunc("/api/sd-accounts/{id}", users.RequireAuthWithPerm(users.PermSD, corsMiddleware(handleDeleteSDAccount(sp)))).Methods("DELETE", "OPTIONS")
	router.HandleFunc("/api/sd/discover", users.RequireAuthWithPerm(users.PermSD, corsMiddleware(handleSDDiscover(sp)))).Methods("POST", "OPTIONS")

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
