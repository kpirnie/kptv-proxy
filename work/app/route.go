package app

import (
	"kptv-proxy/work/admin"
	"kptv-proxy/work/handlers"
	"kptv-proxy/work/proxy"
	"kptv-proxy/work/users"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// RegisterRoutes registers all HTTP routes on the provided router, including
// M3U8 playlists, stream proxying, Xtream Codes API, EPG, Prometheus metrics,
// the web admin interface, and HDHomeRun emulation endpoints.
func RegisterRoutes(router *mux.Router, sp *proxy.StreamProxy) {

	// User auth routes — public, must be first
	users.SetupUserRoutes(router)

	// we need a dummy route for the container health check, but it has to be defined before the auth middleware is applied to avoid requiring auth for the health check
	router.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("PONG"))
	}).Methods("GET")

	// M3U8 playlist endpoints
	router.HandleFunc("/pl/{username}/{password}", handlers.HandlePlaylist(sp)).Methods("GET", "HEAD")
	router.HandleFunc("/playlist/{username}/{password}", handlers.HandlePlaylist(sp)).Methods("GET", "HEAD")

	// Group-filtered M3U8 playlist endpoints
	router.HandleFunc("/pl/{username}/{password}/{group}", handlers.HandleGroupPlaylist(sp)).Methods("GET", "HEAD")
	router.HandleFunc("/playlist/{username}/{password}/{group}", handlers.HandleGroupPlaylist(sp)).Methods("GET", "HEAD")

	// Stream proxy endpoint
	router.HandleFunc("/s/{username}/{password}/{channel}", handlers.HandleStream(sp)).Methods("GET", "HEAD")

	// Xtream Codes endpoints — self-authenticating via XC accounts (handled later)
	router.HandleFunc("/player_api.php", handlers.HandleXCPlayerAPI(sp)).Methods("GET", "POST", "HEAD")
	router.HandleFunc("/get.php", handlers.HandleXCGetPHP(sp)).Methods("GET", "HEAD")
	router.HandleFunc("/xmltv.php", handlers.HandleXCXMLTV(sp)).Methods("GET", "HEAD")
	router.HandleFunc("/live/{username}/{password}/{id}", handlers.HandleXCStream(sp)).Methods("GET", "HEAD")
	router.HandleFunc("/movie/{username}/{password}/{id}", handlers.HandleXCStream(sp)).Methods("GET", "HEAD")
	router.HandleFunc("/series/{username}/{password}/{id}", handlers.HandleXCStream(sp)).Methods("GET", "HEAD")

	// EPG endpoints
	router.HandleFunc("/epg/{username}/{password}", handlers.HandleEPG(sp)).Methods("GET", "HEAD")
	router.HandleFunc("/epg.xml/{username}/{password}", handlers.HandleEPG(sp)).Methods("GET", "HEAD")

	// Prometheus metrics — admin auth protected
	router.Handle("/metrics", users.RequireAuth(promhttp.Handler().ServeHTTP)).Methods("GET")

	// Web admin interface and REST API — admin auth applied inside SetupAdminRoutes
	admin.SetupAdminRoutes(router, sp)

	// HDHomeRun emulation
	handlers.SetupHDHRRoutes(router, sp)
}
