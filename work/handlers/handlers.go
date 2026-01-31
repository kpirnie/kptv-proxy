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
		logger.Debug("{handlers - HandlePlaylist} present the playlist")
		middleware.GzipMiddleware(func(w http.ResponseWriter, r *http.Request) {
			sp.GeneratePlaylist(w, r, "")
		})(w, r)
	}
}

// HandleGroupPlaylist returns an HTTP handler function that generates a filtered M3U8 playlist
// containing only channels belonging to a specific group.
func HandleGroupPlaylist(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logger.Debug("{handlers - HandleGroupPlaylist} present the grouped playlist")
		middleware.GzipMiddleware(func(w http.ResponseWriter, r *http.Request) {
			vars := mux.Vars(r)
			group := vars["group"]
			sp.GeneratePlaylist(w, r, group)
		})(w, r)
	}
}

// HandleStream returns an HTTP handler function that initiates streaming of a specific channel
// to the requesting client.
func HandleStream(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		safeName := vars["channel"]
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

		// Try to serve from disk cache via streaming
		if f, size, ok := sp.Cache.GetEPGFile("merged"); ok {
			defer f.Close()

			remainingTTL := sp.Cache.EPGRemainingTTL("merged")
			if remainingTTL <= 0 {
				remainingTTL = 3600
			}

			modTime := epgModTime(f)
			logger.Debug("{handlers - HandleEPG} from disk cache (%d bytes, ttl=%ds)", size, remainingTTL)

			w.Header().Set("Content-Type", "application/xml")
			w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", remainingTTL))

			// http.ServeContent handles Content-Length, Range requests, and
			// Last-Modified/If-Modified-Since automatically.
			http.ServeContent(w, r, "epg.xml", modTime, f)
			return
		}

		// Cache miss — trigger background warmup
		logger.Debug("{handlers - HandleEPG} Cache miss, triggering background warmup")
		sp.Cache.WarmUpEPG(func() string {
			return sp.FetchAndMergeEPG()
		})

		// Serve fresh data inline for this request
		sources := sp.GetEPGSources()
		if len(sources) == 0 {
			logger.Warn("{handlers - HandleEPG} No EPG sources available")
			return
		}

		logger.Debug("{handlers - HandleEPG} Streaming fresh EPG to client")
		w.Header().Set("Content-Type", "application/xml")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Header().Set("Transfer-Encoding", "chunked")

		flusher, ok := w.(http.Flusher)
		if !ok {
			logger.Error("{handlers - HandleEPG} streaming not supported")
			http.Error(w, "Streaming not supported", http.StatusInternalServerError)
			return
		}

		w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>` + "\n"))
		w.Write([]byte(`<tv generator-info-name="KPTV-Proxy">` + "\n"))
		flusher.Flush()

		channels, programmes := sp.FetchEPGData(sources)

		// write the data and flush the output
		for _, channelData := range channels {
			w.Write([]byte(channelData))
			flusher.Flush()
		}
		for _, programmeData := range programmes {
			w.Write([]byte(programmeData))
			flusher.Flush()
		}

		// write the end of the EPG and flush the response
		w.Write([]byte("</tv>"))
		flusher.Flush()
		logger.Debug("{handlers - HandleEPG} flushed the stream")
	}
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
