package app

import (
	"kptv-proxy/work/admin"
	"kptv-proxy/work/handlers"
	"kptv-proxy/work/proxy"

	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// RegisterRoutes registers all HTTP routes on the provided router, including
// M3U8 playlists, stream proxying, Xtream Codes API, EPG, Prometheus metrics,
// the web admin interface, and HDHomeRun emulation endpoints.
func RegisterRoutes(router *mux.Router, sp *proxy.StreamProxy) {

	// Standard M3U8 playlist endpoints — full channel list
	router.HandleFunc("/pl", handlers.HandlePlaylist(sp)).Methods("GET")
	router.HandleFunc("/playlist", handlers.HandlePlaylist(sp)).Methods("GET")

	// Group-filtered M3U8 playlist endpoints
	router.HandleFunc("/pl/{group}", handlers.HandleGroupPlaylist(sp)).Methods("GET")
	router.HandleFunc("/playlist/{group}", handlers.HandleGroupPlaylist(sp)).Methods("GET")

	// Individual channel stream proxy endpoint
	router.HandleFunc("/s/{channel}", handlers.HandleStream(sp)).Methods("GET")

	// Xtream Codes compatible API endpoints
	router.HandleFunc("/player_api.php", handlers.HandleXCPlayerAPI(sp)).Methods("GET", "POST")
	router.HandleFunc("/get.php", handlers.HandleXCGetPHP(sp)).Methods("GET")
	router.HandleFunc("/xmltv.php", handlers.HandleXCXMLTV(sp)).Methods("GET")

	// Xtream Codes stream delivery endpoints (live, VOD, series)
	router.HandleFunc("/live/{username}/{password}/{id}", handlers.HandleXCStream(sp)).Methods("GET")
	router.HandleFunc("/movie/{username}/{password}/{id}", handlers.HandleXCStream(sp)).Methods("GET")
	router.HandleFunc("/series/{username}/{password}/{id}", handlers.HandleXCStream(sp)).Methods("GET")

	// EPG/XMLTV endpoints
	router.HandleFunc("/epg", handlers.HandleEPG(sp)).Methods("GET")
	router.HandleFunc("/epg.xml", handlers.HandleEPG(sp)).Methods("GET")

	// Prometheus metrics endpoint
	router.Handle("/metrics", promhttp.Handler()).Methods("GET")

	// Web admin interface and REST API endpoints
	admin.SetupAdminRoutes(router, sp)

	// HDHomeRun device emulation endpoints for Plex, Emby, Jellyfin, and Channels DVR
	handlers.SetupHDHRRoutes(router, sp)
}
