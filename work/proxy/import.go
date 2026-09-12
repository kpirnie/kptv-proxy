package proxy

import (
	"context"
	"kptv-proxy/work/config"
	"kptv-proxy/work/constants"
	"kptv-proxy/work/db"
	"kptv-proxy/work/filter"
	"kptv-proxy/work/logger"
	"kptv-proxy/work/parser"
	"kptv-proxy/work/types"
	"kptv-proxy/work/utils"
	"mime"

	"sync"
	"sync/atomic"
	"time"
)

// ImportStreams performs comprehensive stream discovery and aggregation from all configured
// sources. Each source is fetched concurrently in its own goroutine, with connection
// tracking and rate limiting enforced per-source. Discovered streams are filtered,
// deduplicated by channel name, sorted according to configuration, and optionally
// reordered based on persisted custom stream order preferences.
//
// Import is bounded by a per-source timeout and a global ceiling, both propagated through
// context so a slow source is cancelled rather than orphaned. Sources that fail, time out,
// or return nothing keep their previous catalog instead of being dropped, and a run that
// produces no channels at all never overwrites existing state.
func (sp *StreamProxy) ImportStreams() {
	logger.Debug("{proxy/stream - ImportStreams} Starting stream import for %d configured sources", len(sp.Config.Sources))

	if len(sp.Config.Sources) == 0 {
		logger.Warn("{proxy/stream - ImportStreams} No sources configured, skipping import")
		return
	}

	// possible recover
	defer func() {
		if rec := recover(); rec != nil {
			logger.Error("{proxy/stream - ImportStreams} Recovered from panic: %v", rec)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), constants.Internal.ImportGlobalTimeout)
	defer cancel()

	var wg sync.WaitGroup
	sourceStreams := make([][]*types.Stream, len(sp.Config.Sources))
	sourceOK := make([]bool, len(sp.Config.Sources))

	importSemaphore := make(chan struct{}, sp.Config.WorkerThreads)
	for i := range sp.Config.Sources {
		wg.Add(1)
		go func(index int, src *config.SourceConfig) {
			defer wg.Done()

			// possibly recover
			defer func() {
				if rec := recover(); rec != nil {
					logger.Error("{proxy/stream - ImportStreams} Source %s: Recovered from panic: %v", src.Name, rec)
				}
			}()

			select {
			case importSemaphore <- struct{}{}:
			case <-ctx.Done():
				logger.Warn("{proxy/stream - ImportStreams} Import cancelled before start, keeping previous catalog: %s", src.Name)
				return
			}
			defer func() { <-importSemaphore }()

			currentConns := src.ActiveConns.Load()
			if currentConns >= int32(src.MaxConnections) {
				logger.Warn("{proxy/stream - ImportStreams} Cannot import from source (connection limit %d/%d): %s",
					currentConns, src.MaxConnections, utils.LogURL(sp.Config, src.URL))
				return
			}

			newConns := src.ActiveConns.Add(1)
			logger.Debug("{proxy/stream - ImportStreams} Acquired connection %d/%d for parsing: %s",
				newConns, src.MaxConnections, utils.LogURL(sp.Config, src.URL))

			defer func() {
				remainingConns := src.ActiveConns.Add(-1)
				logger.Debug("{proxy/stream - ImportStreams} Released parsing connection, remaining: %d/%d for: %s",
					remainingConns, src.MaxConnections, utils.LogURL(sp.Config, src.URL))
			}()

			srcCtx, srcCancel := context.WithTimeout(ctx, constants.Internal.ImportSourceTimeout)
			defer srcCancel()

			rateLimiter := sp.getRateLimiterForSource(src)

			var streams []*types.Stream
			if src.Username != "" && src.Password != "" {
				logger.Debug("{proxy/stream - ImportStreams} Parsing Xtreme Codes API source: %s", src.Name)
				streams = parser.ParseXtremeCodesAPI(srcCtx, sp.ImportClient, sp.Config, src, rateLimiter, sp.Cache)
			} else {
				logger.Debug("{proxy/stream - ImportStreams} Parsing M3U8 source: %s", src.Name)
				streams = parser.ParseM3U8(srcCtx, sp.ImportClient, sp.Config, src, rateLimiter, sp.Cache)
			}

			if srcCtx.Err() != nil {
				logger.Warn("{proxy/stream - ImportStreams} Source timed out or was cancelled, keeping previous catalog: %s", src.Name)
				return
			}

			if len(streams) == 0 {
				logger.Warn("{proxy/stream - ImportStreams} Source returned no streams, keeping previous catalog: %s", src.Name)
				return
			}

			if sp != nil && sp.FilterManager != nil {
				beforeFilter := len(streams)
				streams = filter.FilterStreams(streams, src, sp.FilterManager)
				if beforeFilter != len(streams) {
					logger.Debug("{proxy/stream - ImportStreams} Filtered %d streams down to %d for source: %s", beforeFilter, len(streams), src.Name)
				}
			}

			if src.Username != "" && src.Password != "" {
				logger.Debug("{proxy/stream - ImportStreams} Parsed %d streams from Xtreme Codes API: %s", len(streams), utils.LogURL(sp.Config, src.URL))
			} else {
				logger.Debug("{proxy/stream - ImportStreams} Parsed %d streams from M3U8 source: %s", len(streams), utils.LogURL(sp.Config, src.URL))
			}

			for importOrder, stream := range streams {
				stream.ImportOrder = importOrder
			}

			sourceStreams[index] = streams
			sourceOK[index] = true
		}(i, &sp.Config.Sources[i])
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		logger.Debug("{proxy/stream - ImportStreams} All source imports completed")
	case <-ctx.Done():
		logger.Warn("{proxy/stream - ImportStreams} Global timeout reached (%v), committing partial import", constants.Internal.ImportGlobalTimeout)
		<-done
	}

	// Zero out ActiveConns for all sources — import connections are
	// short-lived and must not bleed into the streaming phase
	for i := range sp.Config.Sources {
		sp.Config.Sources[i].ActiveConns.Store(0)
	}

	failedSources := make(map[string]*config.SourceConfig)
	for i := range sp.Config.Sources {
		if !sourceOK[i] {
			failedSources[sp.Config.Sources[i].URL] = &sp.Config.Sources[i]
		}
	}

	newChannels := make(map[string]*types.Channel)
	addStream := func(stream *types.Stream) {
		channel, exists := newChannels[stream.Name]
		if !exists {
			channel = &types.Channel{
				Name:                 stream.Name,
				Streams:              []*types.Stream{},
				PreferredStreamIndex: 0,
			}
			newChannels[stream.Name] = channel
		}
		channel.Streams = append(channel.Streams, stream)
	}

	for _, streams := range sourceStreams {
		for _, stream := range streams {
			addStream(stream)
		}
	}
	for _, stream := range sp.carryForwardStreams(failedSources) {
		addStream(stream)
	}

	if len(newChannels) == 0 {
		if existing := sp.ChannelCount(); existing > 0 {
			logger.Error("{proxy/stream - ImportStreams} Import produced no channels, keeping %d existing channels", existing)
			return
		}
		logger.Warn("{proxy/stream - ImportStreams} Import produced no channels")
		return
	}

	allOrders, err := db.GetAllChannelOrders()
	if err != nil {
		logger.Warn("{proxy/stream - ImportStreams} Failed to load stream orders: %v", err)
		allOrders = make(map[string]map[string]int)
	}

	for channelName, channel := range newChannels {
		// Hash URLs before sorting so SortStreams can dedupe and rank on them.
		for _, s := range channel.Streams {
			s.URLHash = utils.HashURL(s.URL)
		}

		// SortStreams handles global sort, dedupe, and custom ordering internally.
		channel.Streams = parser.SortStreams(channel.Streams, sp.Config, channelName, allOrders)

		// Preserve the existing preferred stream index across import cycles.
		if existingChannel, exists := sp.Channels.Load(channelName); exists {
			existingPreferred := atomic.LoadInt32(&existingChannel.PreferredStreamIndex)
			atomic.StoreInt32(&channel.PreferredStreamIndex, existingPreferred)
		}

		sp.Channels.Store(channelName, channel)
	}

	// Drop channels that no longer exist in any successful or carried-forward source
	sp.Channels.Range(func(name string, _ *types.Channel) bool {
		if _, exists := newChannels[name]; !exists {
			sp.Channels.Delete(name)
		}
		return true
	})

	// invalidate previously generated playlists so a partial or empty render
	// from an earlier import window is never served after a good commit
	sp.importGeneration.Add(1)
	sp.rebuildGroupIndex()
	sp.rebuildNameIndex()

	logger.Debug("{proxy/stream - ImportStreams} Import committed %d channels (%d sources carried forward)", len(newChannels), len(failedSources))
}

// carryForwardStreams collects the streams still held for sources that did not import
// successfully, re-pointing each at the current source config so a failed or timed-out
// source keeps its previous catalog rather than disappearing from the channel map.
func (sp *StreamProxy) carryForwardStreams(failedSources map[string]*config.SourceConfig) []*types.Stream {
	if len(failedSources) == 0 {
		return nil
	}

	var carried []*types.Stream
	sp.Channels.Range(func(name string, channel *types.Channel) bool {
		channel.Mu.Lock()
		for _, stream := range channel.Streams {
			if stream.Source == nil {
				continue
			}
			src, exists := failedSources[stream.Source.URL]
			if !exists {
				continue
			}
			stream.Source = src
			carried = append(carried, stream)
		}
		channel.Mu.Unlock()
		return true
	})

	logger.Debug("{proxy/stream - carryForwardStreams} Carried %d streams forward from %d unavailable sources", len(carried), len(failedSources))
	return carried
}

// streamContentType resolves a stream's content type via the shared resolver.
func streamContentType(stream *types.Stream) string {
	return string(utils.ContentTypeOfStream(stream))
}

// streamResponseContentType picks the response MIME type from the currently selected
// stream's container, falling back to MPEG-TS when the container is unknown.
func streamResponseContentType(channel *types.Channel) string {
	extension := ""

	channel.Mu.RLock()
	index := 0
	if channel.Restreamer != nil {
		index = int(atomic.LoadInt32(&channel.Restreamer.CurrentIndex))
	}
	if index >= 0 && index < len(channel.Streams) {
		extension = channel.Streams[index].ContainerExtension
	}
	channel.Mu.RUnlock()

	if extension == "" {
		return "video/mp2t"
	}
	if contentType := mime.TypeByExtension("." + utils.NormalizeContainerExtension(extension)); contentType != "" {
		return contentType
	}
	return "video/mp2t"
}

// StartImportRefresh initiates periodic background import refresh at the interval
// configured in ImportRefreshInterval. It runs in a blocking loop and should be
// launched in its own goroutine. The loop terminates gracefully when a signal is
// received on the import stop channel via StopImportRefresh.
func (sp *StreamProxy) StartImportRefresh() {
	logger.Debug("{proxy/stream - StartImportRefresh} Starting import refresh loop (interval: %s)", sp.Config.ImportRefreshInterval)

	ticker := time.NewTicker(sp.Config.ImportRefreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-sp.importStopChan:
			logger.Debug("{proxy/stream - StartImportRefresh} Import refresh loop stopped")
			return
		case <-ticker.C:
			logger.Debug("{proxy/stream - StartImportRefresh} Triggering scheduled import refresh")
			sp.ImportStreams()
			logger.Debug("{proxy/stream - StartImportRefresh} Scheduled import refresh complete")
		}
	}
}

// StopImportRefresh signals the periodic import refresh loop to terminate gracefully.
// It sends a non-blocking signal to the stop channel, ensuring the caller never blocks
// even if the refresh loop has already stopped or hasn't started yet.
func (sp *StreamProxy) StopImportRefresh() {
	logger.Debug("{proxy/stream - StopImportRefresh} Sending stop signal to import refresh loop")
	if sp.importStopChan != nil {
		select {
		case sp.importStopChan <- true:
			logger.Debug("{proxy/stream - StopImportRefresh} Stop signal sent successfully")
		default:
			logger.Warn("{proxy/stream - StopImportRefresh} Stop channel already full, refresh loop may have already stopped")
		}
	}
}
