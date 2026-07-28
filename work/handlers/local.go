// work/handlers/local.go
package handlers

import (
	"database/sql"
	"kptv-proxy/work/db"
	"kptv-proxy/work/localscan"
	"kptv-proxy/work/logger"
	"kptv-proxy/work/proxy"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gorilla/mux"
)

// HandleLocalStream returns an HTTP handler that serves a local media file by
// its stream identity hash. Range requests, conditional requests, and content
// type are handled by http.ServeContent, so seeking works in every player.
func HandleLocalStream(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)

		if findXCAccount(sp.Config, vars["username"], vars["password"]) == nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		entry, ok := resolveLocalEntry(vars["hash"])
		if !ok {
			http.Error(w, "Not found", http.StatusNotFound)
			return
		}

		serveLocalFile(w, r, entry.Path)
	}
}

// HandleLocalArtwork returns an HTTP handler that serves the poster or fanart
// image associated with a local media entry, used for tvg-logo in exports.
func HandleLocalArtwork(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)

		if findXCAccount(sp.Config, vars["username"], vars["password"]) == nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		entry, ok := resolveLocalEntry(vars["hash"])
		if !ok {
			http.Error(w, "Not found", http.StatusNotFound)
			return
		}

		var art string
		switch vars["kind"] {
		case "poster":
			art = entry.Poster
		case "fanart":
			art = entry.Fanart
		default:
			http.Error(w, "Not found", http.StatusNotFound)
			return
		}

		if art == "" {
			http.Error(w, "Not found", http.StatusNotFound)
			return
		}

		// Remote art was stored verbatim from the sidecar — redirect rather
		// than proxy it.
		if strings.HasPrefix(art, "http://") || strings.HasPrefix(art, "https://") {
			http.Redirect(w, r, art, http.StatusFound)
			return
		}

		if !pathWithinSource(entry.LocalSourceID, art) {
			logger.Warn("{handlers/local - HandleLocalArtwork} artwork outside source root, refusing: %s", art)
			http.Error(w, "Not found", http.StatusNotFound)
			return
		}

		serveLocalFile(w, r, art)
	}
}

// resolveLocalEntry loads a local media entry by hash and verifies that its
// stored path still resolves inside its source's configured root. A row whose
// path has drifted outside the root is refused rather than served.
func resolveLocalEntry(hash string) (*localscan.MediaEntry, bool) {
	if hash == "" {
		return nil, false
	}

	entry, err := localscan.GetByHash(hash)
	if err != nil {
		if err != sql.ErrNoRows {
			logger.Error("{handlers/local - resolveLocalEntry} lookup failed for %s: %v", hash, err)
		}
		return nil, false
	}

	if !pathWithinSource(entry.LocalSourceID, entry.Path) {
		logger.Warn("{handlers/local - resolveLocalEntry} entry path outside source root, refusing: %s", entry.Path)
		return nil, false
	}

	return entry, true
}

// pathWithinSource reports whether path resolves inside the configured root of
// the given local source, after symlink resolution.
func pathWithinSource(localSourceID int64, path string) bool {
	src, err := db.GetLocalSource(localSourceID)
	if err != nil {
		return false
	}

	root, err := filepath.EvalSymlinks(src.Path)
	if err != nil {
		return false
	}
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}

	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// serveLocalFile streams a file from disk with range and conditional request
// support.
func serveLocalFile(w http.ResponseWriter, r *http.Request, path string) {
	f, err := os.Open(path)
	if err != nil {
		logger.Debug("{handlers/local - serveLocalFile} open failed for %s: %v", path, err)
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil || fi.IsDir() {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Accept-Ranges", "bytes")
	http.ServeContent(w, r, filepath.Base(path), fi.ModTime(), f)
}
