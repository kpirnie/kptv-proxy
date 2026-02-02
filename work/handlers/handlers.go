package handlers

import (
	"fmt"
	"kptv-proxy/work/logger"
	"kptv-proxy/work/middleware"
	"kptv-proxy/work/proxy"
	"net/http"
	"os"
	"strings"
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
			logger.Debug("{handlers - HandleEPG} Serving from disk cache (%d bytes, ttl=%ds)", size, remainingTTL)

			w.Header().Set("Content-Type", "application/xml")
			w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", remainingTTL))

			http.ServeContent(w, r, "epg.xml", modTime, f)
			return
		}

		// Cache miss — fetch and stream fresh EPG data
		logger.Debug("{handlers - HandleEPG} Cache miss, fetching and streaming fresh EPG")

		sources := sp.GetEPGSources()
		if len(sources) == 0 {
			logger.Warn("{handlers - HandleEPG} No EPG sources available")
			http.Error(w, "No EPG sources configured", http.StatusServiceUnavailable)
			return
		}

		w.Header().Set("Content-Type", "application/xml")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Header().Set("Transfer-Encoding", "chunked")

		flusher, ok := w.(http.Flusher)
		if !ok {
			logger.Error("{handlers - HandleEPG} Streaming not supported")
			http.Error(w, "Streaming not supported", http.StatusInternalServerError)
			return
		}

		// Write XML header
		w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>` + "\n"))
		w.Write([]byte(`<tv generator-info-name="KPTV-Proxy">` + "\n"))
		flusher.Flush()

		// Fetch EPG data from all sources
		logger.Debug("{handlers - HandleEPG} Fetching EPG data from %d sources", len(sources))
		channels, programmes := sp.FetchEPGData(sources)

		logger.Debug("{handlers - HandleEPG} Fetched %d channels, %d programmes", len(channels), len(programmes))

		// Stream channels
		for _, channelData := range channels {
			w.Write([]byte(channelData))
		}
		flusher.Flush()

		// Stream programmes
		for _, programmeData := range programmes {
			w.Write([]byte(programmeData))
		}
		flusher.Flush()

		// Close XML
		w.Write([]byte("</tv>"))
		flusher.Flush()

		logger.Debug("{handlers - HandleEPG} Finished streaming EPG to client")

		// After successfully streaming to client, save to cache in background
		go func() {
			var result strings.Builder
			result.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
			result.WriteString(`<tv generator-info-name="KPTV-Proxy">` + "\n")
			for _, ch := range channels {
				result.WriteString(ch)
			}
			for _, prog := range programmes {
				result.WriteString(prog)
			}
			result.WriteString("</tv>")

			sp.Cache.SetEPG("merged", result.String())
			logger.Debug("{handlers - HandleEPG} Cached EPG data in background")
		}()
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
