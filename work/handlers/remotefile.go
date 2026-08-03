// work/handlers/remotefile.go
package handlers

import (
	"context"
	"io"
	"kptv-proxy/work/config"
	"kptv-proxy/work/constants"
	"kptv-proxy/work/db"
	"kptv-proxy/work/deadstreams"
	"kptv-proxy/work/logger"
	"kptv-proxy/work/parser"
	"kptv-proxy/work/proxy"
	"kptv-proxy/work/utils"
	"mime"
	"net/http"
	"os"
	"strconv"
	"time"
)

// remoteFileRequestHeaders lists the client headers forwarded upstream so the
// provider can answer partial and conditional requests directly.
var remoteFileRequestHeaders = []string{"Range", "If-Range", "If-Modified-Since", "If-None-Match"}

// remoteFileResponseHeaders lists the upstream response headers copied back
// verbatim so players receive the length, range, and container details they
// need to start playback and seek.
var remoteFileResponseHeaders = []string{
	"Content-Type",
	"Content-Length",
	"Content-Range",
	"Accept-Ranges",
	"Last-Modified",
	"ETag",
	"Content-Disposition",
}

// serveSeriesEpisode plays a proxy episode ID, walking every provider carrying
// it in the series channel's own stream order. Each candidate is retried once
// against the same provider — episode URLs hand out a single-use CDN token, so a
// stale token fails on first contact and succeeds on the next — before the
// provider is marked dead and the next one is tried. When every candidate is
// exhausted the offline video is served so the player shows something rather
// than erroring.
func serveSeriesEpisode(sp *proxy.StreamProxy, w http.ResponseWriter, r *http.Request, episodeID int) bool {
	mappings := db.GetSeriesEpisodeSources(episodeID)
	if len(mappings) == 0 {
		return false
	}

	channelName := mappings[0].ChannelName
	season := mappings[0].Season
	episode := mappings[0].Episode

	origins, ok := remoteSeriesOrigins(sp, streamIDFromName(channelName))
	if !ok {
		logger.Warn("{handlers/remotefile - serveSeriesEpisode} Episode %d has no live provider on %s", episodeID, channelName)
		serveFallbackVideo(w, r)
		return true
	}

	for _, origin := range origins {
		upstreamID, extension, found := episodeOnSource(sp, origin, mappings, season, episode)
		if !found {
			logger.Debug("{handlers/remotefile - serveSeriesEpisode} Episode %d (s%de%d) not carried by %s", episodeID, season, episode, origin.Source.Name)
			continue
		}

		streamURL := upstreamEpisodeURL(origin.Source, upstreamID, extension)
		for attempt := 0; attempt <= constants.Internal.RemoteFileRetries; attempt++ {
			if attemptRemoteFile(sp, w, r, streamURL, origin.Source, extension) {
				return true
			}
			if r.Context().Err() != nil {
				return true
			}
		}

		if err := deadstreams.MarkStreamDeadByHash(channelName, origin.StreamHash, "series episode unplayable"); err != nil {
			logger.Error("{handlers/remotefile - serveSeriesEpisode} Failed to mark %s dead on %s: %v", origin.Source.Name, channelName, err)
		}
		logger.Warn("{handlers/remotefile - serveSeriesEpisode} Episode %d failed on %s, trying next provider", episodeID, origin.Source.Name)
	}

	logger.Error("{handlers/remotefile - serveSeriesEpisode} Episode %d failed on every provider for %s", episodeID, channelName)
	serveFallbackVideo(w, r)
	return true
}

// episodeOnSource resolves the provider-side episode identifier for one source,
// preferring a stored mapping and falling back to that source's own episode tree
// when the series has not been opened on it yet.
func episodeOnSource(sp *proxy.StreamProxy, origin seriesOrigin, mappings []db.SeriesEpisode, season, episode int) (string, string, bool) {
	for _, mapping := range mappings {
		if mapping.SourceURL == origin.Source.URL && mapping.UpstreamID != "" {
			return mapping.UpstreamID, mapping.Extension, true
		}
	}

	info, err := parser.FetchXCSeriesInfo(sp.HttpClient, sp.Config, origin.Source, sp.RateLimiterForSource(origin.Source), origin.UpstreamID)
	if err != nil {
		logger.Debug("{handlers/remotefile - episodeOnSource} Series info fetch failed on %s: %v", origin.Source.Name, err)
		return "", "", false
	}

	for seasonKey, seasonEpisodes := range info.Episodes {
		seasonNum, convErr := strconv.Atoi(seasonKey)
		if convErr != nil {
			continue
		}
		if seasonNum != season {
			continue
		}
		for index, ep := range seasonEpisodes {
			episodeNum, convErr := strconv.Atoi(string(ep.EpisodeNum))
			if convErr != nil || episodeNum == 0 {
				episodeNum = index + 1
			}
			if episodeNum == episode && string(ep.ID) != "" {
				return string(ep.ID), utils.NormalizeContainerExtension(ep.ContainerExtension), true
			}
		}
	}

	return "", "", false
}

// upstreamEpisodeURL builds a provider's own episode URL from its credentials.
func upstreamEpisodeURL(source *config.SourceConfig, upstreamID, extension string) string {
	return source.URL + "/series/" + source.Username + "/" + source.Password + "/" + upstreamID + "." + extension
}

// attemptRemoteFile proxies one upstream file to the client, forwarding range
// and conditional headers in both directions so seeking works. It reports
// whether the response was committed; a false return means nothing was written
// and the caller may try another candidate. The header phase carries its own
// deadline so a stalled provider is abandoned while the player is still waiting,
// rather than losing the race to the player's own timeout.
func attemptRemoteFile(sp *proxy.StreamProxy, w http.ResponseWriter, r *http.Request, streamURL string, source *config.SourceConfig, extension string) bool {
	if limiter := sp.RateLimiterForSource(source); limiter != nil {
		limiter.Take()
	}

	if source.ActiveConns.Load() >= int32(source.MaxConnections) {
		logger.Debug("{handlers/remotefile - attemptRemoteFile} source at max connections (%d): %s", source.MaxConnections, utils.LogURL(sp.Config, source.URL))
		return false
	}
	source.ActiveConns.Add(1)
	defer source.ActiveConns.Add(-1)

	headerCtx, cancel := context.WithCancel(r.Context())
	defer cancel()
	headerTimer := time.AfterFunc(constants.Internal.RemoteFileHeaderTimeout, cancel)

	req, err := http.NewRequestWithContext(headerCtx, r.Method, streamURL, nil)
	if err != nil {
		headerTimer.Stop()
		logger.Error("{handlers/remotefile - attemptRemoteFile} request build failed for %s: %v", utils.LogURL(sp.Config, streamURL), err)
		return false
	}

	for _, header := range remoteFileRequestHeaders {
		if value := r.Header.Get(header); value != "" {
			req.Header.Set(header, value)
		}
	}

	resp, err := sp.HttpClient.DoWithHeaders(req, source.UserAgent, source.ReqOrigin, source.ReqReferrer)
	if err != nil {
		headerTimer.Stop()
		logger.Error("{handlers/remotefile - attemptRemoteFile} upstream request failed for %s: %v", utils.LogURL(sp.Config, streamURL), err)
		return false
	}
	headerTimer.Stop()
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		logger.Error("{handlers/remotefile - attemptRemoteFile} upstream HTTP %d for %s", resp.StatusCode, utils.LogURL(sp.Config, streamURL))
		return false
	}

	for _, header := range remoteFileResponseHeaders {
		if value := resp.Header.Get(header); value != "" {
			w.Header().Set(header, value)
		}
	}

	if w.Header().Get("Accept-Ranges") == "" {
		w.Header().Set("Accept-Ranges", "bytes")
	}

	if w.Header().Get("Content-Type") == "" {
		if contentType := mime.TypeByExtension("." + utils.NormalizeContainerExtension(extension)); contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
	}

	w.WriteHeader(resp.StatusCode)

	if r.Method == http.MethodHead {
		return true
	}

	if _, err := io.Copy(w, resp.Body); err != nil {
		logger.Debug("{handlers/remotefile - attemptRemoteFile} client copy ended for %s: %v", utils.LogURL(sp.Config, streamURL), err)
	}
	return true
}

// serveFallbackVideo loops the offline clip to the client when no provider can
// supply the requested file, matching what live channels show when every stream
// fails. No length is sent, since the loop ends only when the client leaves.
func serveFallbackVideo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "video/mp2t")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)

	if r.Method == http.MethodHead {
		return
	}

	for {
		if r.Context().Err() != nil {
			return
		}

		f, err := os.Open(constants.Internal.FallbackVideoPath)
		if err != nil {
			logger.Error("{handlers/remotefile - serveFallbackVideo} open failed for %s: %v", constants.Internal.FallbackVideoPath, err)
			return
		}

		_, copyErr := io.Copy(w, f)
		f.Close()
		if copyErr != nil {
			return
		}

		select {
		case <-r.Context().Done():
			return
		case <-time.After(constants.Internal.FallbackVideoLoopDelay):
		}
	}
}
