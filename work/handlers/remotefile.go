// work/handlers/remotefile.go
package handlers

import (
	"context"
	"fmt"
	"io"
	"kptv-proxy/work/config"
	"kptv-proxy/work/constants"
	"kptv-proxy/work/db"
	"kptv-proxy/work/deadstreams"
	"kptv-proxy/work/logger"
	"kptv-proxy/work/metrics"
	"kptv-proxy/work/parser"
	"kptv-proxy/work/proxy"
	"kptv-proxy/work/types"
	"kptv-proxy/work/utils"
	"mime"
	"net/http"
	"os"
	"strconv"
	"sync/atomic"
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

// sessionWriter counts bytes delivered to the client so a passthrough session
// reports throughput the same way a restreamed channel does.
type sessionWriter struct {
	http.ResponseWriter
	bytes *atomic.Int64
}

func (sw *sessionWriter) Write(b []byte) (int, error) {
	n, err := sw.ResponseWriter.Write(b)
	if n > 0 {
		sw.bytes.Add(int64(n))
		metrics.TotalBytesTransferred.Add(int64(n))
	}
	return n, err
}

// serveSeriesEpisode plays a proxy episode ID, walking every provider carrying
// it in the series channel's own stream order. Each candidate is retried once
// against the same provider — episode URLs hand out a single-use CDN token, so a
// stale token fails on first contact and succeeds on the next — before the
// provider is marked dead and the next one is tried. When every candidate is
// exhausted the offline video is served so the player shows something rather
// than erroring.
func serveSeriesEpisode(sp *proxy.StreamProxy, w http.ResponseWriter, r *http.Request, episodeID int) bool {

	// make sure we aren't overloading the server with too many concurrent clients
	release, ok := sp.AcquireClientSlot()
	if !ok {
		http.Error(w, "Server at capacity", http.StatusServiceUnavailable)
		return true
	}
	defer release()

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
		sessionID := fmt.Sprintf("%s-%d", r.RemoteAddr, time.Now().UnixNano())
		session := proxy.StartFileSession(sessionID, channelName, origin.Source.Name, origin.Attributes["tvg-logo"])

		delivered := false
		for attempt := 0; attempt <= constants.Internal.RemoteFileRetries; attempt++ {
			if attemptRemoteFile(sp, w, r, streamURL, origin.Source, extension, session) {
				delivered = true
				break
			}
			if r.Context().Err() != nil {
				delivered = true
				break
			}
		}
		proxy.EndFileSession(sessionID)
		if delivered {
			return true
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
// when the series has not been opened on it yet. A tree fetched here is persisted
// in full, so failing over to this provider costs one provider call per series
// rather than one per playback.
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

	learned := make([]db.SeriesEpisode, 0, 64)
	upstreamID := ""
	extension := ""

	for seasonKey, seasonEpisodes := range info.Episodes {
		count := 0
		for _, ep := range seasonEpisodes {
			if string(ep.ID) == "" {
				continue
			}

			seasonNum, convErr := strconv.Atoi(seasonKey)
			if convErr != nil {
				seasonNum, _ = strconv.Atoi(string(ep.Season))
			}

			episodeNum, convErr := strconv.Atoi(string(ep.EpisodeNum))
			if convErr != nil || episodeNum == 0 {
				episodeNum = count + 1
			}
			count++

			epExtension := utils.NormalizeContainerExtension(ep.ContainerExtension)

			learned = append(learned, db.SeriesEpisode{
				EpisodeID:   episodeIDFromChannel(origin.ChannelName, seasonNum, episodeNum),
				ChannelName: origin.ChannelName,
				Season:      seasonNum,
				Episode:     episodeNum,
				SourceURL:   origin.Source.URL,
				SeriesID:    origin.UpstreamID,
				UpstreamID:  string(ep.ID),
				Extension:   epExtension,
			})

			if seasonNum == season && episodeNum == episode {
				upstreamID = string(ep.ID)
				extension = epExtension
			}
		}
	}

	if len(learned) > 0 {
		if err := db.SetSeriesEpisodes(origin.Source.URL, origin.UpstreamID, learned); err != nil {
			logger.Warn("{handlers/remotefile - episodeOnSource} Failed to persist episode mappings for %s on %s: %v", origin.ChannelName, origin.Source.Name, err)
		}
	}

	if upstreamID == "" {
		return "", "", false
	}
	return upstreamID, extension, true
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
func attemptRemoteFile(sp *proxy.StreamProxy, w http.ResponseWriter, r *http.Request, streamURL string, source *config.SourceConfig, extension string, session *proxy.FileSession) bool {
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

	if _, err := io.Copy(&sessionWriter{ResponseWriter: w, bytes: &session.Bytes}, resp.Body); err != nil {
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

// serveVODChannel plays a movie channel by walking its streams from the
// preferred index, skipping dead and blocked entries, so the admin's ordering
// and kill controls govern which provider answers. Each candidate is retried
// once before being marked dead and the next one tried; when all are exhausted
// the offline video is served. VOD is a finite file and cannot go through the
// live restreamer, which serves no length and cannot seek.
func serveVODChannel(sp *proxy.StreamProxy, w http.ResponseWriter, r *http.Request, channel *types.Channel) {

	// make sure we aren't overloading the server with too many concurrent clients
	release, ok := sp.AcquireClientSlot()
	if !ok {
		http.Error(w, "Server at capacity", http.StatusServiceUnavailable)
		return
	}
	defer release()

	channel.Mu.RLock()
	total := len(channel.Streams)
	preferred := int(atomic.LoadInt32(&channel.PreferredStreamIndex))
	if preferred < 0 || preferred >= total {
		preferred = 0
	}

	candidates := make([]*types.Stream, 0, total)
	for i := 0; i < total; i++ {
		stream := channel.Streams[(preferred+i)%total]
		if stream.Source == nil {
			continue
		}
		if deadstreams.IsStreamDead(channel.Name, stream.URLHash) || atomic.LoadInt32(&stream.Blocked) == 1 {
			continue
		}
		candidates = append(candidates, stream)
	}
	channel.Mu.RUnlock()

	for _, stream := range candidates {
		sessionID := fmt.Sprintf("%s-%d", r.RemoteAddr, time.Now().UnixNano())
		session := proxy.StartFileSession(sessionID, channel.Name, stream.Source.Name, stream.Attributes["tvg-logo"])

		delivered := false
		for attempt := 0; attempt <= constants.Internal.RemoteFileRetries; attempt++ {
			if attemptRemoteFile(sp, w, r, stream.URL, stream.Source, stream.ContainerExtension, session) {
				delivered = true
				break
			}
			if r.Context().Err() != nil {
				delivered = true
				break
			}
		}
		proxy.EndFileSession(sessionID)
		if delivered {
			return
		}

		if err := deadstreams.MarkStreamDeadByHash(channel.Name, stream.URLHash, "vod unplayable"); err != nil {
			logger.Error("{handlers/remotefile - serveVODChannel} Failed to mark %s dead on %s: %v", stream.Source.Name, channel.Name, err)
		}
		logger.Warn("{handlers/remotefile - serveVODChannel} %s failed on %s, trying next provider", channel.Name, stream.Source.Name)
	}

	logger.Error("{handlers/remotefile - serveVODChannel} %s failed on every provider", channel.Name)
	serveFallbackVideo(w, r)
}
