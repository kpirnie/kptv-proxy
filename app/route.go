package app

import (
	"kptv-proxy/work/admin"
	"kptv-proxy/work/handlers"
	"kptv-proxy/work/proxy"
	"kptv-proxy/work/users"

	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// RegisterRoutes registers all HTTP routes on the provided router, including
// M3U8 playlists, stream proxying, Xtream Codes API, EPG, Prometheus metrics,
// the web admin interface, and HDHomeRun emulation endpoints.
func RegisterRoutes(router *mux.Router, sp *proxy.StreamProxy) {

	// User auth routes — public, must be first
	users.SetupUserRoutes(router)

	// M3U8 playlist endpoints
	router.HandleFunc("/pl/{username}/{password}", handlers.HandlePlaylist(sp)).Methods("GET")
	router.HandleFunc("/playlist/{username}/{password}", handlers.HandlePlaylist(sp)).Methods("GET")

	// Group-filtered M3U8 playlist endpoints
	router.HandleFunc("/pl/{username}/{password}/{group}", handlers.HandleGroupPlaylist(sp)).Methods("GET")
	router.HandleFunc("/playlist/{username}/{password}/{group}", handlers.HandleGroupPlaylist(sp)).Methods("GET")

	// Stream proxy endpoint — XC auth (handled later)
	router.HandleFunc("/s/{username}/{password}/{channel}", handlers.HandleStream(sp)).Methods("GET")

	// Xtream Codes endpoints — self-authenticating via XC accounts (handled later)
	router.HandleFunc("/player_api.php", handlers.HandleXCPlayerAPI(sp)).Methods("GET", "POST")
	router.HandleFunc("/get.php", handlers.HandleXCGetPHP(sp)).Methods("GET")
	router.HandleFunc("/xmltv.php", handlers.HandleXCXMLTV(sp)).Methods("GET")
	router.HandleFunc("/live/{username}/{password}/{id}", handlers.HandleXCStream(sp)).Methods("GET")
	router.HandleFunc("/movie/{username}/{password}/{id}", handlers.HandleXCStream(sp)).Methods("GET")
	router.HandleFunc("/series/{username}/{password}/{id}", handlers.HandleXCStream(sp)).Methods("GET")

	// EPG endpoints — XC auth (handled later)
	router.HandleFunc("/epg/{username}/{password}", handlers.HandleEPG(sp)).Methods("GET")
	router.HandleFunc("/epg.xml/{username}/{password}", handlers.HandleEPG(sp)).Methods("GET")

	// Prometheus metrics — admin auth protected
	router.Handle("/metrics", users.RequireAuth(promhttp.Handler().ServeHTTP)).Methods("GET")

	// Web admin interface and REST API — admin auth applied inside SetupAdminRoutes
	admin.SetupAdminRoutes(router, sp)

	// HDHomeRun emulation — XC auth (handled later)
	handlers.SetupHDHRRoutes(router, sp)
}
