package handlers

import (
	"fmt"
	"kptv-proxy/work/logger"
	"kptv-proxy/work/middleware"
	"kptv-proxy/work/proxy"
	"net/http"
	"os"
	"time"

	"github.com/gorilla/mux"
)

// HandlePlaylist returns an HTTP handler function that generates a complete M3U8 playlist
// containing all available channels from all configured sources.
func HandlePlaylist(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		username := vars["username"]
		password := vars["password"]

		account := findXCAccount(sp.Config, username, password)
		if account == nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		logger.Debug("{handlers - HandlePlaylist} present the playlist")
		middleware.GzipMiddleware(func(w http.ResponseWriter, r *http.Request) {
			sp.GeneratePlaylist(w, r, "", account)
		})(w, r)
	}
}

// HandleGroupPlaylist returns an HTTP handler function that generates a filtered M3U8 playlist
// containing only channels belonging to a specific group.
func HandleGroupPlaylist(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		username := vars["username"]
		password := vars["password"]
		group := vars["group"]

		account := findXCAccount(sp.Config, username, password)
		if account == nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		logger.Debug("{handlers - HandleGroupPlaylist} present the grouped playlist")
		middleware.GzipMiddleware(func(w http.ResponseWriter, r *http.Request) {
			sp.GeneratePlaylist(w, r, group, account)
		})(w, r)
	}
}

// HandleStream returns an HTTP handler function that initiates streaming of a specific channel
// to the requesting client.
func HandleStream(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		username := vars["username"]
		password := vars["password"]
		safeName := vars["channel"]

		if findXCAccount(sp.Config, username, password) == nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		channelName := sp.FindChannelBySafeName(safeName)
		channel, exists := sp.Channels.Load(channelName)
		if !exists {
			logger.Error("{handlers - HandleStream} Channel not found: %s", channelName)
			http.Error(w, "Channel not found", http.StatusNotFound)
			return
		}

		logger.Debug("{handlers - HandleStream} handling stream for channel: %s", channelName)
		sp.HandleRestreamingClient(w, r, channel)
	}
}

// HandleEPG serves combined EPG data from disk cache, streaming directly to the
// client via http.ServeContent. Sets Cache-Control max-age to the remaining TTL
// so downstream players (Emby/Plex/Channels) cache appropriately.
func HandleEPG(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		username := vars["username"]
		password := vars["password"]

		if findXCAccount(sp.Config, username, password) == nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		serveEPG(sp)(w, r)
	}
}

func serveEPG(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {

		// Try to serve from disk cache via streaming
		if serveEPGFromCache(sp, w, r) {
			return
		}

		// Cache miss — build the merged EPG straight to disk, then serve it
		logger.Debug("{handlers - HandleEPG} Cache miss, refreshing EPG cache")

		committed, err := sp.Cache.RefreshEPG(sp.FetchAndMergeEPG)
		if err != nil || !committed {
			logger.Warn("{handlers - HandleEPG} No EPG data available")
			http.Error(w, "No EPG data available", http.StatusServiceUnavailable)
			return
		}

		if !serveEPGFromCache(sp, w, r) {
			http.Error(w, "No EPG data available", http.StatusServiceUnavailable)
		}
	}
}

// serveEPGFromCache streams the cached merged EPG file to the client if a
// valid cached copy exists. Returns whether the response was served.
func serveEPGFromCache(sp *proxy.StreamProxy, w http.ResponseWriter, r *http.Request) bool {
	f, size, ok := sp.Cache.GetEPGFile("merged")
	if !ok {
		return false
	}
	defer f.Close()

	remainingTTL := sp.Cache.EPGRemainingTTL("merged")
	if remainingTTL <= 0 {
		remainingTTL = 3600
	}

	modTime := epgModTime(f)
	logger.Debug("{handlers - HandleEPG} Serving from disk cache (%d bytes, ttl=%ds)", size, remainingTTL)

	w.Header().Set("Content-Type", "application/xml")
	w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", remainingTTL))

	http.ServeContent(w, r, "epg.xml", modTime, f)
	return true
}

// epgModTime returns the modification time of the given file for use with
// http.ServeContent. Returns zero time on error.
func epgModTime(f *os.File) time.Time {
	info, err := f.Stat()
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}
