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
	"strings"
	"sync"
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
// reports throughput the same way a restreamed channel does, and marks the
// transfer's progress so displacement can tell a client that stopped reading
// from one still streaming.
type sessionWriter struct {
	http.ResponseWriter
	bytes    *atomic.Int64
	transfer *activeTransfer
}

func (sw *sessionWriter) Write(b []byte) (int, error) {
	n, err := sw.ResponseWriter.Write(b)
	if n > 0 {
		sw.bytes.Add(int64(n))
		metrics.TotalBytesTransferred.Add(int64(n))
		if sw.transfer != nil {
			sw.transfer.touch()
		}
	}
	return n, err
}

// serveSeriesEpisode plays a proxy episode ID, walking every provider carrying
// it in the series channel's own stream order. Each candidate is retried once
// against the same provider — episode URLs hand out a single-use CDN token, so
// a stale token fails on first contact and succeeds on the next — before the
// provider is marked dead and the next one is tried. A provider with no free
// connection is skipped, not marked dead. When every candidate is exhausted
// the offline video is served so the player shows something rather than
// erroring; when every candidate was merely busy the player is sent a 503 with
// Retry-After instead, so it comes back when a slot frees.
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

	logger.Debug("{handlers/remotefile - serveSeriesEpisode} Episode s%de%d of %s: client=%s, method=%s, range=%q, ua=%q",
		season, episode, channelName, r.RemoteAddr, r.Method, r.Header.Get("Range"), r.UserAgent())

	origins, ok := remoteSeriesOrigins(sp, streamIDFromName(channelName))
	if !ok {
		logger.Warn("{handlers/remotefile - serveSeriesEpisode} Episode %d has no live provider on %s", episodeID, channelName)
		serveFallbackVideo(w, r)
		return true
	}

	// capacity is tracked apart from failure: a playback arriving while the slot
	// is held is normal, and must not cost the provider its stream
	capacity := false

	for _, origin := range origins {
		upstreamID, extension, found := episodeOnSource(sp, origin, mappings, season, episode)
		if !found {
			logger.Debug("{handlers/remotefile - serveSeriesEpisode} Episode %d (s%de%d) not carried by %s", episodeID, season, episode, origin.Source.Name)
			continue
		}

		streamURL := upstreamEpisodeURL(origin.Source, upstreamID, extension)
		sessionID := fmt.Sprintf("%s-%d", r.RemoteAddr, time.Now().UnixNano())
		session := proxy.StartFileSession(sessionID, channelName, origin.Source.Name, origin.Attributes["tvg-logo"])

		outcome := RemoteDelivered
		for attempt := 0; attempt <= constants.Internal.RemoteFileRetries; attempt++ {
			outcome = AttemptRemoteFile(sp, w, r, streamURL, origin.Source, extension, session, sp.Config.StreamQueueWait)
			if outcome == RemoteDelivered {
				break
			}
			if r.Context().Err() != nil {
				outcome = RemoteDelivered
				break
			}
		}
		proxy.EndFileSession(sessionID)
		if outcome == RemoteDelivered {
			return true
		}

		if outcome == RemoteCapacity {
			capacity = true
			logger.Debug("{handlers/remotefile - serveSeriesEpisode} Episode %d has no free connection on %s", episodeID, origin.Source.Name)
			continue
		}

		if err := deadstreams.MarkStreamDeadByHash(channelName, origin.StreamHash, "series episode unplayable"); err != nil {
			logger.Error("{handlers/remotefile - serveSeriesEpisode} Failed to mark %s dead on %s: %v", origin.Source.Name, channelName, err)
		}
		logger.Warn("{handlers/remotefile - serveSeriesEpisode} Episode %d failed on %s, trying next provider", episodeID, origin.Source.Name)
	}

	if capacity {
		// the episode is playable the moment a slot frees, so send the player
		// away with a retry instead of the offline clip
		w.Header().Set("Retry-After", "2")
		http.Error(w, "Provider connection busy", http.StatusServiceUnavailable)
		return true
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

	for _, numbered := range numberProviderEpisodes(info) {
		epExtension := utils.NormalizeContainerExtension(numbered.ep.ContainerExtension)

		learned = append(learned, db.SeriesEpisode{
			EpisodeID:   episodeIDFromChannel(origin.ChannelName, numbered.season, numbered.episode),
			ChannelName: origin.ChannelName,
			Season:      numbered.season,
			Episode:     numbered.episode,
			SourceURL:   origin.Source.URL,
			SeriesID:    origin.UpstreamID,
			UpstreamID:  string(numbered.ep.ID),
			Extension:   epExtension,
		})

		if numbered.season == season && numbered.episode == episode {
			upstreamID = string(numbered.ep.ID)
			extension = epExtension
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

// remoteOutcome says why one passthrough attempt ended. Capacity has to be
// separable from failure: a source limited to one connection sees a player
// session as two requests for the same file — a probe, then the playback — and
// the second finds the slot taken by the first. The stream is healthy in that
// case, and a dead mark would make it unplayable for the life of the process,
// since the VOD path has no retry interval of its own. Exported so the test in
// testing/handlers can assert outcomes; the identifiers keep the Remote prefix
// to read as one family.
type RemoteOutcome int

const (
	RemoteDelivered       RemoteOutcome = iota // response completed, or the player left mid-transfer
	RemoteCapacity                             // source has no free connection slot
	RemoteUpstreamFailure                      // provider refused, answered badly, or stalled
)

// ReserveSourceSlot takes one of a source's connection slots, queueing up to
// wait for the current holder to finish. The slot is held for the whole
// transfer, so a caller that reserves must call the returned release — which
// is idempotent, so a displacement that releases the slot early and the
// transfer's own deferred release do not double-count. A wait of zero or less
// disables queueing: the request is refused at once, leaving the caller free
// to answer 503 instead of taking a connection the provider limits. Exported
// for the test in testing/handlers, which runs in an external package.
func ReserveSourceSlot(source *config.SourceConfig, ctx context.Context, wait time.Duration) (func(), bool) {
	take := func() (func(), bool) {
		if source.ActiveConns.Add(1) > int32(source.MaxConnections) {
			source.ActiveConns.Add(-1)
			return nil, false
		}
		var once sync.Once
		return func() { once.Do(func() { source.ActiveConns.Add(-1) }) }, true
	}

	if release, ok := take(); ok {
		return release, true
	}
	if wait <= 0 {
		return nil, false
	}

	deadline := time.After(wait)
	ticker := time.NewTicker(constants.Internal.RemoteFileCapacityPoll)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, false
		case <-deadline:
			return nil, false
		case <-ticker.C:
			if release, ok := take(); ok {
				return release, true
			}
		}
	}
}

// activeTransfer describes one running passthrough delivery that a newer
// request for the same content may displace. lastProgress records when the
// client last accepted bytes, so a transfer whose client has gone silent can
// be told apart from one still streaming; contentLength records the full size
// of the upstream file, so a tail fetch can recognize itself against it.
type activeTransfer struct {
	channel       string
	source        string
	startedAt     time.Time
	lastProgress  atomic.Int64 // unix nanos of the last byte the client accepted
	contentLength int64        // full upstream file size, 0 when unknown
	release       func()       // idempotent; frees the slot even if the copy is wedged on a silent client
	writer        http.ResponseWriter
	cancel        context.CancelFunc // aborts the upstream copy, which releases the slot
}

// touch records that the client just accepted bytes.
func (t *activeTransfer) touch() {
	t.lastProgress.Store(time.Now().UnixNano())
}

var (
	transferMutex   sync.Mutex
	activeTransfers = make(map[string]*activeTransfer) // session id -> transfer
)

// registerTransfer records a running transfer under its session id. The caller
// must unregister the same id when the transfer ends.
func registerTransfer(id string, t *activeTransfer) {
	transferMutex.Lock()
	activeTransfers[id] = t
	transferMutex.Unlock()
}

// unregisterTransfer drops the transfer record. Deleting an id that a
// displacement already removed is a harmless no-op.
func unregisterTransfer(id string) {
	transferMutex.Lock()
	delete(activeTransfers, id)
	transferMutex.Unlock()
}

// DisplaceTransfer cancels the oldest STALLED transfer of the same channel on
// the same source — one whose client stopped accepting bytes — freeing its
// provider slot for a newer request for the same content. Players probe, seek,
// and re-open the same file while leaving the older connection idle, so on a
// saturated source the stalled holder is almost always the requester's own
// abandoned predecessor. A transfer still streaming is never displaced: it is
// either the player's own metadata probe, which ends by itself in a moment and
// hands over the slot, or another viewer's playback, which has as much right
// to the connection as the newcomer does. Reports whether a transfer was found
// and cancelled. Exported for the test in testing/handlers, which runs in an
// external package.
func DisplaceTransfer(channelName string, source *config.SourceConfig) bool {
	transferMutex.Lock()
	defer transferMutex.Unlock()

	now := time.Now()
	var oldestID string
	var oldest *activeTransfer
	for id, t := range activeTransfers {
		if t.channel != channelName || t.source != source.Name {
			continue
		}
		stalled := now.Sub(time.Unix(0, t.lastProgress.Load())) >= constants.Internal.RemoteFileTransferStall
		if !stalled {
			continue
		}
		if oldest == nil || t.startedAt.Before(oldest.startedAt) {
			oldest, oldestID = t, id
		}
	}
	if oldest == nil {
		return false
	}

	delete(activeTransfers, oldestID)
	logger.Info("{handlers/remotefile - DisplaceTransfer} Displacing the stalled transfer of %s on %s (silent for %v, held for %v) to free the provider slot",
		channelName, source.Name, now.Sub(time.Unix(0, oldest.lastProgress.Load())).Truncate(time.Millisecond), now.Sub(oldest.startedAt).Truncate(time.Millisecond))
	// cancelling ends the upstream read, but a copy wedged writing to the
	// silent client would keep the slot held until that client closes; break
	// the write as well and free the slot from here, so the requester is not
	// hostage to a connection the player already abandoned
	oldest.cancel()
	if oldest.writer != nil {
		if rc := http.NewResponseController(oldest.writer); rc != nil {
			_ = rc.SetWriteDeadline(time.Now().Add(-time.Second))
		}
	}
	oldest.release()
	return true
}

// acquireRemoteSlot gets one of the source's connection slots for a transfer
// of channelName, waiting up to queueWait. The slot is held for the whole
// transfer, so the caller must call the returned release. A free slot is taken
// at once. When the source is full, a stalled transfer of the same content —
// the requester's own abandoned predecessor — is displaced and its slot taken
// after the takeover grace period; displacement is re-tried on every poll, so
// a holder that goes silent while the request is already queued is still
// caught without waiting out the whole queue wait. A holder still streaming to
// its client is left alone, and if the wait expires the caller answers 503 —
// that is a genuinely busy source, not a broken stream.
func acquireRemoteSlot(source *config.SourceConfig, channelName string, ctx context.Context, queueWait time.Duration) (func(), bool) {
	if release, ok := ReserveSourceSlot(source, ctx, 0); ok {
		return release, true
	}
	if ctx.Err() != nil {
		return nil, false
	}

	deadline := time.Now().Add(queueWait)
	for {
		if DisplaceTransfer(channelName, source) {
			if release, ok := ReserveSourceSlot(source, ctx, constants.Internal.RemoteFileTakeoverWait); ok {
				return release, true
			}
		}
		if ctx.Err() != nil || !time.Now().Before(deadline) {
			return nil, false
		}
		select {
		case <-ctx.Done():
			return nil, false
		case <-time.After(constants.Internal.RemoteFileCapacityPoll):
		}
	}
}

// upstreamTotalSize reports the full size of the upstream file from a
// response: the total after the slash of Content-Range for partial
// responses, the content length otherwise, 0 when neither is given.
func upstreamTotalSize(resp *http.Response) int64 {
	if cr := resp.Header.Get("Content-Range"); cr != "" {
		if idx := strings.LastIndexByte(cr, '/'); idx >= 0 {
			if n, err := strconv.ParseInt(cr[idx+1:], 10, 64); err == nil && n > 0 {
				return n
			}
		}
	}
	if resp.ContentLength > 0 {
		return resp.ContentLength
	}
	return 0
}

// rangeStartOffset parses the first-byte position of a Range header. A
// request without a usable start offset reports false.
func rangeStartOffset(rangeHeader string) (int64, bool) {
	if !strings.HasPrefix(rangeHeader, "bytes=") {
		return 0, false
	}
	spec := strings.TrimPrefix(rangeHeader, "bytes=")
	dash := strings.IndexByte(spec, '-')
	if dash <= 0 {
		return 0, false // suffix form ("bytes=-500") has no start offset
	}
	n, err := strconv.ParseInt(spec[:dash], 10, 64)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// registeredContentLength returns the full upstream file size recorded by any
// running transfer of the same channel on the same source.
func registeredContentLength(channelName string, source *config.SourceConfig) int64 {
	transferMutex.Lock()
	defer transferMutex.Unlock()
	for _, t := range activeTransfers {
		if t.channel == channelName && t.source == source.Name && t.contentLength > 0 {
			return t.contentLength
		}
	}
	return 0
}

// tailFetchCandidate reports whether a request is the player's tail metadata
// fetch — a ranged request starting within the last few megabytes of a file
// that a running transfer of the same content already sizes. Matroska cues
// and other container metadata live at the end, and players fetch them while
// keeping the main connection open. The initial open of a stream has no start
// offset or one near the file's beginning and never qualifies.
func tailFetchCandidate(channelName string, source *config.SourceConfig, r *http.Request) bool {
	start, ok := rangeStartOffset(r.Header.Get("Range"))
	if !ok {
		return false
	}
	total := registeredContentLength(channelName, source)
	return total > 0 && start >= total-constants.Internal.RemoteFileTailFetchWindow
}

// attemptTailFetch serves one ranged request on a direct upstream connection,
// without consuming a slot and without touching the running transfer that
// holds one. The header phase carries a short deadline so a provider that
// refuses a second connection fails fast and the caller falls back to the
// slot path.
func attemptTailFetch(sp *proxy.StreamProxy, w http.ResponseWriter, r *http.Request, streamURL string, source *config.SourceConfig, extension string, session *proxy.FileSession) RemoteOutcome {
	headerCtx, cancel := context.WithCancel(r.Context())
	defer cancel()
	headerTimer := time.AfterFunc(constants.Internal.RemoteFileTailFetchTimeout, cancel)

	req, err := http.NewRequestWithContext(headerCtx, r.Method, streamURL, nil)
	if err != nil {
		headerTimer.Stop()
		logger.Debug("{handlers/remotefile - attemptTailFetch} request build failed for %s: %v", utils.LogURL(sp.Config, streamURL), err)
		return RemoteCapacity
	}
	for _, header := range remoteFileRequestHeaders {
		if value := r.Header.Get(header); value != "" {
			req.Header.Set(header, value)
		}
	}

	start := time.Now()
	resp, err := sp.HttpClient.DoWithHeaders(req, source.UserAgent, source.ReqOrigin, source.ReqReferrer)
	if err != nil {
		headerTimer.Stop()
		logger.Debug("{handlers/remotefile - attemptTailFetch} direct request failed for %s after %v: %v", utils.LogURL(sp.Config, streamURL), time.Since(start), err)
		return RemoteCapacity
	}
	headerTimer.Stop()
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		logger.Debug("{handlers/remotefile - attemptTailFetch} direct request got HTTP %d for %s", resp.StatusCode, utils.LogURL(sp.Config, streamURL))
		return RemoteCapacity
	}

	logger.Debug("{handlers/remotefile - attemptTailFetch} Tail fetch for %s served on a direct connection: %d, length=%q (after %v)",
		utils.LogURL(sp.Config, streamURL), resp.StatusCode, resp.Header.Get("Content-Length"), time.Since(start))

	if err := deliverUpstream(sp, w, r, streamURL, extension, session, resp, nil); err != nil {
		logger.Debug("{handlers/remotefile - attemptTailFetch} tail copy ended for %s: %v", utils.LogURL(sp.Config, streamURL), err)
	}
	return RemoteDelivered
}

// deliverUpstream forwards an upstream response to the client, copying back
// the headers players need and streaming the body while counting bytes. The
// transfer, when not nil, records progress so displacement can tell a client
// that stopped reading from one still streaming.
func deliverUpstream(sp *proxy.StreamProxy, w http.ResponseWriter, r *http.Request, streamURL string, extension string, session *proxy.FileSession, resp *http.Response, transfer *activeTransfer) error {
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
		return nil
	}

	_, err := io.Copy(&sessionWriter{ResponseWriter: w, bytes: &session.Bytes, transfer: transfer}, resp.Body)
	return err
}

// AttemptRemoteFile proxies one upstream file to the client, forwarding range
// and conditional headers in both directions so seeking works. It reports why
// the attempt ended: only RemoteUpstreamFailure entitles the caller to treat
// the candidate as broken, and RemoteCapacity means the provider slot is busy
// with somebody else's transfer. A slot held by a STALLED transfer of the same
// channel on the same source is displaced, because that holder is the
// requester's own abandoned predecessor; a holder still streaming is left
// alone, and a small ranged fetch near the end of the file — the player's own
// container-metadata read — is served on a direct connection so the holder's
// connection survives the pause. The header phase carries its own deadline so
// a stalled provider is abandoned while the player is still waiting, rather
// than losing the race to the player's own timeout.
func AttemptRemoteFile(sp *proxy.StreamProxy, w http.ResponseWriter, r *http.Request, streamURL string, source *config.SourceConfig, extension string, session *proxy.FileSession, queueWait time.Duration) RemoteOutcome {
	// A ranged request near the end of the file while the source is full is
	// the player's own metadata fetch (Matroska cues live in the tail): serve
	// it on a direct connection without touching the transfer that holds the
	// slot. The provider itself serves a player's concurrent double-open, and
	// killing the holder would cut off the player's metadata parse mid-read
	// and force it to start the file over.
	if source.ActiveConns.Load() >= int32(source.MaxConnections) && tailFetchCandidate(session.ChannelName, source, r) {
		if outcome := attemptTailFetch(sp, w, r, streamURL, source, extension, session); outcome == RemoteDelivered {
			return RemoteDelivered
		}
		logger.Debug("{handlers/remotefile - attemptRemoteFile} Tail fetch for %s could not be served on a direct connection; falling back to the slot path", utils.LogURL(sp.Config, streamURL))
	}

	if limiter := sp.RateLimiterForSource(source); limiter != nil {
		// wait off the handler's goroutine so a player that gave up releases
		// the handler at once instead of sitting in the limiter queue
		taken := make(chan struct{})
		go func() {
			limiter.Take()
			close(taken)
		}()
		select {
		case <-taken:
		case <-r.Context().Done():
			return RemoteDelivered
		}
	}

	// Get a slot: a free one at once, otherwise — while the source is full —
	// displace the stalled transfer of this same channel on this same source
	// (whose client has moved on to this request) and take its slot; a holder
	// still streaming is left alone and the request queues up to queueWait
	// for it to end, answering 503 when the wait expires.
	start := time.Now()
	logger.Debug("{handlers/remotefile - attemptRemoteFile} Attempting %s on %s for %s: method=%s, range=%q, ua=%q, slots=%d/%d",
		utils.LogURL(sp.Config, streamURL), source.Name, r.RemoteAddr, r.Method, r.Header.Get("Range"), r.UserAgent(),
		source.ActiveConns.Load(), source.MaxConnections)

	release, ok := acquireRemoteSlot(source, session.ChannelName, r.Context(), queueWait)
	if !ok {
		if r.Context().Err() != nil {
			// the player gave up while waiting, so nothing is left to serve
			logger.Debug("{handlers/remotefile - attemptRemoteFile} Player left while queueing for %s after %v", utils.LogURL(sp.Config, streamURL), time.Since(start))
			return RemoteDelivered
		}
		logger.Debug("{handlers/remotefile - attemptRemoteFile} source at max connections (%d): %s (after %v)", source.MaxConnections, utils.LogURL(sp.Config, source.URL), time.Since(start))
		return RemoteCapacity
	}
	defer release()
	logger.Debug("{handlers/remotefile - attemptRemoteFile} Slot taken for %s after %v", utils.LogURL(sp.Config, streamURL), time.Since(start))

	headerCtx, cancel := context.WithCancel(r.Context())
	defer cancel()
	headerTimer := time.AfterFunc(constants.Internal.RemoteFileHeaderTimeout, cancel)

	req, err := http.NewRequestWithContext(headerCtx, r.Method, streamURL, nil)
	if err != nil {
		headerTimer.Stop()
		logger.Error("{handlers/remotefile - attemptRemoteFile} request build failed for %s: %v", utils.LogURL(sp.Config, streamURL), err)
		return RemoteUpstreamFailure
	}

	for _, header := range remoteFileRequestHeaders {
		if value := r.Header.Get(header); value != "" {
			req.Header.Set(header, value)
		}
	}

	resp, err := sp.HttpClient.DoWithHeaders(req, source.UserAgent, source.ReqOrigin, source.ReqReferrer)
	if err != nil {
		headerTimer.Stop()
		logger.Error("{handlers/remotefile - attemptRemoteFile} upstream request failed for %s: %v (after %v)", utils.LogURL(sp.Config, streamURL), err, time.Since(start))
		return RemoteUpstreamFailure
	}
	headerTimer.Stop()
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		logger.Error("{handlers/remotefile - attemptRemoteFile} upstream HTTP %d for %s (after %v)", resp.StatusCode, utils.LogURL(sp.Config, streamURL), time.Since(start))
		return RemoteUpstreamFailure
	}

	logger.Debug("{handlers/remotefile - attemptRemoteFile} Upstream %d for %s: length=%q, range=%q, acceptRanges=%q (after %v)",
		resp.StatusCode, utils.LogURL(sp.Config, streamURL),
		resp.Header.Get("Content-Length"), resp.Header.Get("Content-Range"), resp.Header.Get("Accept-Ranges"), time.Since(start))

	// from here on the transfer holds the provider slot, so a newer request
	// for the same content must be able to find and displace it when it goes
	// silent; the transfer carries the slot's own release so displacement can
	// free the slot even if the copy is wedged writing to the silent client,
	// and the file size so tail fetches can recognize themselves against it
	transfer := &activeTransfer{
		channel:   session.ChannelName,
		source:    source.Name,
		startedAt: time.Now(),
		release:   release,
		writer:    w,
		cancel:    cancel,
	}
	transfer.touch()
	transfer.contentLength = upstreamTotalSize(resp)
	registerTransfer(session.ID, transfer)
	defer unregisterTransfer(session.ID)

	if err := deliverUpstream(sp, w, r, streamURL, extension, session, resp, transfer); err != nil {
		logger.Debug("{handlers/remotefile - attemptRemoteFile} client copy ended for %s after %d bytes, %v total: %v",
			utils.LogURL(sp.Config, streamURL), session.Bytes.Load(), time.Since(start), err)
	}
	logger.Debug("{handlers/remotefile - attemptRemoteFile} Delivered %s after %v (%d bytes total)",
		utils.LogURL(sp.Config, streamURL), time.Since(start), session.Bytes.Load())
	return RemoteDelivered
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
// and kill controls govern which provider answers. Auto-blocked entries are
// probed again once AutoBlockRetryInterval has passed — the same recovery the
// live restreamer gets — because with a one-connection provider a playback can
// be marked dead while its provider was merely busy, and without this the film
// would never be playable again. Each candidate is retried once before being
// marked dead and the next one tried. VOD is a finite file and cannot go
// through the live restreamer, which serves no length and cannot seek.
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
		if deadstreams.BlockedUntilRetry(channel.Name, stream) {
			continue
		}
		if dead := deadstreams.IsStreamDead(channel.Name, stream.URLHash); dead || atomic.LoadInt32(&stream.Blocked) == 1 {
			// the retry interval has passed, so the entry is probed again and
			// the dead mark cleared — this is the only way a VOD stream marked
			// dead while its provider was busy can come back
			logger.Debug("{handlers/remotefile - serveVODChannel} Retrying auto-blocked %s on %s", channel.Name, stream.Source.Name)
			atomic.StoreInt32(&stream.Blocked, 0)
			if dead {
				if err := deadstreams.ReviveStream(channel.Name, stream.URLHash); err != nil {
					logger.Error("{handlers/remotefile - serveVODChannel} Failed to clear auto-block for %s on %s: %v", stream.Source.Name, channel.Name, err)
				}
			}
		}
		candidates = append(candidates, stream)
	}
	channel.Mu.RUnlock()

	// capacity is tracked apart from failure: a playback that arrives while the
	// slot is held (by this player's own probe, or by another viewer) is a
	// normal thing to happen, and the stream must not be marked dead for it.
	capacity := false

	for _, stream := range candidates {
		sessionID := fmt.Sprintf("%s-%d", r.RemoteAddr, time.Now().UnixNano())
		session := proxy.StartFileSession(sessionID, channel.Name, stream.Source.Name, stream.Attributes["tvg-logo"])

		outcome := RemoteDelivered
		for attempt := 0; attempt <= constants.Internal.RemoteFileRetries; attempt++ {
			outcome = AttemptRemoteFile(sp, w, r, stream.URL, stream.Source, stream.ContainerExtension, session, sp.Config.StreamQueueWait)
			if outcome == RemoteDelivered {
				break
			}
			if r.Context().Err() != nil {
				outcome = RemoteDelivered
				break
			}
		}
		proxy.EndFileSession(sessionID)
		if outcome == RemoteDelivered {
			return
		}

		if outcome == RemoteCapacity {
			// keep walking the list: another provider may have a free slot
			capacity = true
			logger.Debug("{handlers/remotefile - serveVODChannel} %s has no free connection on %s", channel.Name, stream.Source.Name)
			continue
		}

		if err := deadstreams.MarkStreamDeadByHash(channel.Name, stream.URLHash, "vod unplayable"); err != nil {
			logger.Error("{handlers/remotefile - serveVODChannel} Failed to mark %s dead on %s: %v", stream.Source.Name, channel.Name, err)
		}
		logger.Warn("{handlers/remotefile - serveVODChannel} %s failed on %s, trying next provider", channel.Name, stream.Source.Name)
	}

	if capacity {
		// the file is playable the moment a slot frees, so send the player away
		// with a retry instead of the offline clip
		w.Header().Set("Retry-After", "2")
		http.Error(w, "Provider connection busy", http.StatusServiceUnavailable)
		return
	}

	logger.Error("{handlers/remotefile - serveVODChannel} %s failed on every provider", channel.Name)
	serveFallbackVideo(w, r)
}
