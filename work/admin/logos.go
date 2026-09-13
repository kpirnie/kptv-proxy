package admin

import (
	"kptv-proxy/work/logger"
	"kptv-proxy/work/logos"
	"kptv-proxy/work/proxy"
	"net/http"

	"github.com/gorilla/mux"
)

// handleGetLogo serves a stored channel logo to the admin UI, which has no XC
// credentials for the public /logo/ route. A hash with no file on disk is
// fetched from its recorded source URL on the spot.
func handleGetLogo(_ *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hash := mux.Vars(r)["hash"]
		if !logos.ValidHash(hash) {
			http.Error(w, "Not found", http.StatusNotFound)
			return
		}

		f, fi, err := logos.Open(hash)
		if err != nil {
			url, ok := logos.URLFor(hash)
			if !ok {
				http.Error(w, "Not found", http.StatusNotFound)
				return
			}

			if _, ferr := logos.EnsureCached(url); ferr != nil {
				logger.Debug("{admin/logos - handleGetLogo} lazy fetch failed for %s: %v", hash, ferr)
				http.Redirect(w, r, url, http.StatusFound)
				return
			}

			f, fi, err = logos.Open(hash)
			if err != nil {
				http.Error(w, "Not found", http.StatusNotFound)
				return
			}
		}
		defer f.Close()

		w.Header().Set("Cache-Control", "public, max-age=604800, immutable")

		http.ServeContent(w, r, hash, fi.ModTime(), f)
	}
}
