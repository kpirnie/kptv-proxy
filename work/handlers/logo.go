package handlers

import (
	"kptv-proxy/work/logger"
	"kptv-proxy/work/logos"
	"kptv-proxy/work/proxy"
	"net/http"

	"github.com/gorilla/mux"
)

// HandleLogo returns an HTTP handler that serves a stored channel logo by its
// hash. A hash with no file on disk is fetched from its recorded source URL on
// the spot, so exports stay valid across a cold cache or a restart.
func HandleLogo(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)

		account := findXCAccount(sp.Config, vars["username"], vars["password"])
		if account == nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		hash := vars["hash"]
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
				logger.Debug("{handlers/logo - HandleLogo} lazy fetch failed for %s: %v", hash, ferr)
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

		// content-hashed and url-keyed names never change meaning, so clients
		// can hold these for a long time; ServeContent adds ETag handling
		w.Header().Set("Cache-Control", "public, max-age=604800, immutable")

		http.ServeContent(w, r, hash, fi.ModTime(), f)
	}
}
