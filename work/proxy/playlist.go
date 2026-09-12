package proxy

import (
	"fmt"
	"kptv-proxy/work/config"
	"kptv-proxy/work/localscan"
	"kptv-proxy/work/logger"
	"kptv-proxy/work/types"
	"kptv-proxy/work/utils"

	"net/http"
	"sort"
	"strings"
)

// channelBatch is a lightweight struct pairing a channel name with its channel pointer,
// used for efficient batch operations like sorting and playlist generation without
// needing to re-query the concurrent map during iteration.
type channelBatch struct {
	name        string         // channel name as stored in the map key
	channel     *types.Channel // pointer to the channel data
	sourceOrder int            // lowest source order across the channel's streams
	importOrder int            // lowest import order within that source
}

// getChannelBatch snapshots the current channel map into an ordered slice for batch
// processing. This avoids holding read locks on the concurrent map during potentially
// expensive operations like sorting and playlist rendering.
func (sp *StreamProxy) getChannelBatch() []channelBatch {
	batch := make([]channelBatch, 0, 1000)
	sp.Channels.Range(func(name string, ch *types.Channel) bool {
		batch = append(batch, channelBatch{name: name, channel: ch})
		return true
	})
	return batch
}

// snapshotOriginalOrder fills each entry's original-order keys under a single
// read lock per channel, so the sort comparator never takes a lock.
func snapshotOriginalOrder(batch []channelBatch) {
	for i := range batch {
		batch[i].sourceOrder, batch[i].importOrder = channelOriginalOrder(batch[i].channel)
	}
}

func channelOriginalOrderLess(a, b channelBatch) bool {
	if a.sourceOrder != b.sourceOrder {
		return a.sourceOrder < b.sourceOrder
	}
	if a.importOrder != b.importOrder {
		return a.importOrder < b.importOrder
	}
	return strings.ToLower(a.name) < strings.ToLower(b.name)
}

func channelOriginalOrder(channel *types.Channel) (int, int) {
	channel.Mu.RLock()
	defer channel.Mu.RUnlock()

	if len(channel.Streams) == 0 {
		return int(^uint(0) >> 1), int(^uint(0) >> 1)
	}

	sourceOrder := channel.Streams[0].Source.Order
	importOrder := channel.Streams[0].ImportOrder
	for _, stream := range channel.Streams[1:] {
		if stream.Source.Order < sourceOrder || (stream.Source.Order == sourceOrder && stream.ImportOrder < importOrder) {
			sourceOrder = stream.Source.Order
			importOrder = stream.ImportOrder
		}
	}
	return sourceOrder, importOrder
}

// getChannelSortValue extracts the sort value from a channel based on the configured
// sort field. It reads from the first stream's attributes using the field specified in
// Config.SortField, defaulting to "tvg-name" when no sort field is configured. All
// values are lowercased for case-insensitive sorting. Falls back to the channel name
// if the sort field attribute doesn't exist on the stream.
func (sp *StreamProxy) getChannelSortValue(ch channelBatch) string {
	ch.channel.Mu.RLock()
	defer ch.channel.Mu.RUnlock()

	if len(ch.channel.Streams) == 0 {
		return ""
	}

	sortField := sp.Config.SortField
	if sortField == "" {
		sortField = "tvg-name"
	}

	if value, exists := ch.channel.Streams[0].Attributes[sortField]; exists {
		return strings.ToLower(value)
	}

	return strings.ToLower(ch.name)
}

// GeneratePlaylist creates and serves a complete M3U8 playlist containing all discovered
// channels. When a group filter is provided, only channels matching that group are included.
// The generated playlist is cached when caching is enabled to avoid regeneration on
// subsequent requests within the cache TTL.
//
// Channels are sorted according to the configured sort field and direction before
// rendering. Each channel entry includes its stream attributes and a proxy URL pointing
// back to this server for transparent stream proxying.
func (sp *StreamProxy) GeneratePlaylist(w http.ResponseWriter, r *http.Request, groupFilter string, account *config.XCOutputAccount) {
	logger.Debug("{proxy/stream - GeneratePlaylist} Playlist request from: %s (%s)",
		r.RemoteAddr, r.Header.Get("User-Agent"))

	// a group segment naming a content type filters by content type rather
	// than by group title
	typeFilter := ""
	if t := strings.ToLower(groupFilter); t == "live" || t == "vod" || t == "series" {
		typeFilter = t
		groupFilter = ""
	}

	// reject unknown group names before they reach the cache key
	if groupFilter != "" && !sp.IsKnownGroup(groupFilter) {
		logger.Debug("{proxy/stream - GeneratePlaylist} Unknown group requested: %s", groupFilter)
		http.Error(w, "Group not found", http.StatusNotFound)
		return
	}

	// construct cache key per account and import generation, with optional group
	// or content-type suffix
	generation := sp.importGeneration.Load()
	cacheKey := fmt.Sprintf("playlist_%d_%s", generation, account.Username)
	if groupFilter != "" {
		cacheKey = fmt.Sprintf("playlist_%d_%s_%s", generation, account.Username, strings.ToLower(groupFilter))
	} else if typeFilter != "" {
		cacheKey = fmt.Sprintf("playlist_%d_%s_type_%s", generation, account.Username, typeFilter)
	}

	// one in-flight build per cache key; concurrent misses wait on that build
	// instead of each rendering the same playlist
	result, err, _ := sp.playlistBuilds.Do(cacheKey, func() (any, error) {
		if sp.Config.CacheEnabled {
			if cached, ok := sp.Cache.GetM3U8(cacheKey); ok {
				logger.Debug("{proxy/stream - GeneratePlaylist} Serving cached playlist (key: %s)", cacheKey)
				return cached, nil
			}
		}

		built := sp.buildPlaylist(groupFilter, typeFilter, account)

		if sp.Config.CacheEnabled {
			sp.Cache.SetM3U8(cacheKey, built)
			logger.Debug("{proxy/stream - GeneratePlaylist} Cached generated playlist (key: %s)", cacheKey)
		}

		return built, nil
	})

	if err != nil {
		logger.Error("{proxy/stream - GeneratePlaylist} Playlist build failed: %v", err)
		http.Error(w, "Failed to generate playlist", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/x-mpegURL")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write([]byte(result.(string)))
}

// buildPlaylist renders the M3U8 body for the given filters and account. Callers
// are responsible for cache lookup, storage, and response writing.
func (sp *StreamProxy) buildPlaylist(groupFilter, typeFilter string, account *config.XCOutputAccount) string {
	channels := sp.getChannelBatch()
	logger.Debug("{proxy/stream - buildPlaylist} Building playlist from %d channels", len(channels))

	if sp.Config.SortField == "preserve-order" {
		snapshotOriginalOrder(channels)
		sort.SliceStable(channels, func(i, j int) bool {
			return channelOriginalOrderLess(channels[i], channels[j])
		})
	} else {
		// sort channels alphabetically by channel name
		sort.SliceStable(channels, func(i, j int) bool {
			return strings.ToLower(channels[i].name) < strings.ToLower(channels[j].name)
		})
	}

	// pre-allocate the builder with a reasonable estimate
	estimatedSize := len(channels) * 250
	var playlist strings.Builder
	playlist.Grow(estimatedSize)
	playlist.WriteString("#EXTM3U\n")

	filteredCount := 0

	// channel-name -> mapped epg_id; unmapped channels fall back to the dummy id
	epgMap := ChannelEPGMap()

	for _, ch := range channels {
		ch.channel.Mu.RLock()
		if len(ch.channel.Streams) > 0 {
			stream := ch.channel.Streams[0]
			attrs := stream.Attributes

			// skip channels that don't match the group filter
			if groupFilter != "" {
				channelGroup := sp.GetChannelGroup(attrs)
				if !strings.EqualFold(channelGroup, groupFilter) {
					ch.channel.Mu.RUnlock()
					continue
				}
			}

			// determine content type from the importer's classification
			contentType := streamContentType(stream)

			// skip channels that don't match the requested content type
			if typeFilter != "" && contentType != typeFilter {
				ch.channel.Mu.RUnlock()
				continue
			}

			// skip channels that don't match the account content settings
			if contentType == "live" && !account.EnableLive {
				ch.channel.Mu.RUnlock()
				continue
			}
			if contentType == "vod" && !account.EnableVOD {
				ch.channel.Mu.RUnlock()
				continue
			}
			if contentType == "series" && !account.EnableSeries {
				ch.channel.Mu.RUnlock()
				continue
			}

			filteredCount++

			// write the EXTINF line with all stream attributes; tvg-id is forced
			// to the mapped EPG id (or dummy) so the playlist matches the export
			playlist.WriteString("#EXTINF:-1")

			// mapped channels advertise the raw mapped epg id on all three
			// guide-matching attributes; unmapped fall back to the dummy id
			epgID := EPGIDForChannel(ch.name, epgMap)
			if epgID != DummyChannelID {
				playlist.WriteString(fmt.Sprintf(" tvg-id=\"%s\" tvg-epgid=\"%s\" tvc-guide-stationid=\"%s\"", epgID, epgID, epgID))
			} else {
				playlist.WriteString(fmt.Sprintf(" tvg-id=\"%s\"", epgID))
			}

			// other EXTINF attributes...
			for key, value := range attrs {
				if key != "tvg-name" && key != "duration" && key != "tvg-id" {
					playlist.WriteString(fmt.Sprintf(" %s=\"%s\"", key, utils.EscapeM3UAttribute(value)))
				}
			}

			// write the channel name and proxy URL with XC credentials
			cleanName := utils.SanitizeM3UDisplayName(strings.Trim(ch.name, "\""))
			playlist.WriteString(fmt.Sprintf(",%s\n", cleanName))
			safeName := utils.SanitizeChannelName(ch.name)
			proxyURL := fmt.Sprintf("%s/s/%s/%s/%s", sp.Config.BaseURL, account.Username, account.Password, safeName)
			playlist.WriteString(proxyURL)
			playlist.WriteByte('\n')
		}
		ch.channel.Mu.RUnlock()
	}

	// Local media is export-only — entries carry direct /local/ URLs so the
	// client range-requests the file rather than going through the restreamer.
	localCount := localscan.WritePlaylistEntries(&playlist, sp.Config.BaseURL,
		account.Username, account.Password, groupFilter, typeFilter,
		account.EnableVOD, account.EnableSeries)
	if localCount > 0 {
		logger.Debug("{proxy/stream - buildPlaylist} Appended %d local media entries", localCount)
	}

	if groupFilter == "" {
		logger.Debug("{proxy/stream - buildPlaylist} Generated playlist with %d channels", len(channels))
	} else {
		logger.Debug("{proxy/stream - buildPlaylist} Generated playlist for group '%s' with %d channels (out of %d total)", groupFilter, filteredCount, len(channels))
	}

	return playlist.String()
}
