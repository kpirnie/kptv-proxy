package handlers

import (
	"encoding/json"
	"fmt"
	"kptv-proxy/work/config"
	"kptv-proxy/work/db"
	"kptv-proxy/work/deadstreams"
	"kptv-proxy/work/localscan"
	"kptv-proxy/work/logger"
	"kptv-proxy/work/parser"
	"kptv-proxy/work/proxy"
	"kptv-proxy/work/utils"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
)

// seriesOrigin is one provider's copy of a series, paired with the channel it
// belongs to and the stream backing it so playback can mark it dead.
type seriesOrigin struct {
	Source      *config.SourceConfig
	UpstreamID  string
	Attributes  map[string]string
	ChannelName string
	StreamHash  string
}

// cachedSeriesEpisode is the subset of a cached get_series_info episode entry
// needed to write an M3U line for it.
type cachedSeriesEpisode struct {
	ID                 string      `json:"id"`
	Title              string      `json:"title"`
	EpisodeNum         parser.XCID `json:"episode_num"`
	ContainerExtension string      `json:"container_extension"`
}

// mergedSeriesEpisode pairs a rendered episode entry with its parsed episode
// number so a season merged from several providers can be ordered without
// re-parsing the entries.
type mergedSeriesEpisode struct {
	num   int
	entry map[string]any
}

// mergedSeriesCacheSource is the synthetic source_url the merged get_series_info
// payload is stored under, since a tree assembled from several providers belongs
// to none of them.
const mergedSeriesCacheSource = "kptv://merged"

// buildLocalVODInfo assembles the get_vod_info response for a local media
// entry. Unknown IDs are left to the caller — remote channels keep their
// existing default response.
func buildLocalVODInfo(e *localscan.MediaEntry, baseURL, username, password string) map[string]any {
	extension := utils.NormalizeContainerExtension(localscan.ContainerExtension(e))
	streamID := localscan.XCStreamID(e.Hash)

	poster := ""
	if e.Poster != "" {
		poster = fmt.Sprintf("%s/localart/%s/%s/%s/poster", baseURL, username, password, e.Hash)
	}

	backdrops := []string{}
	if e.Fanart != "" {
		backdrops = append(backdrops, fmt.Sprintf("%s/localart/%s/%s/%s/fanart", baseURL, username, password, e.Hash))
	}

	cast := make([]string, 0, len(e.Cast))
	for _, p := range e.Cast {
		if p.Name != "" {
			cast = append(cast, p.Name)
		}
	}

	name := e.Title
	if name == "" {
		name = e.Display
	}

	duration := e.Duration
	if duration < 0 {
		duration = 0
	}

	return map[string]any{
		"info": map[string]any{
			"name":            name,
			"o_name":          e.Display,
			"movie_image":     poster,
			"cover_big":       poster,
			"backdrop_path":   backdrops,
			"releasedate":     e.Premiered,
			"director":        strings.Join(e.Directors, ", "),
			"actors":          strings.Join(cast, ", "),
			"cast":            strings.Join(cast, ", "),
			"description":     e.Plot,
			"plot":            e.Plot,
			"genre":           strings.Join(e.Genres, ", "),
			"country":         e.Country,
			"age":             e.MPAA,
			"mpaa_rating":     e.MPAA,
			"rating":          fmt.Sprintf("%.1f", e.Rating),
			"duration_secs":   duration,
			"duration":        formatXCDuration(duration),
			"youtube_trailer": "",
			"tmdb_id":         e.TMDBID,
			"video":           []any{},
			"audio":           []any{},
			"bitrate":         0,
		},
		"movie_data": map[string]any{
			"stream_id":           streamID,
			"name":                e.Display,
			"added":               "0",
			"category_id":         categoryIDFromName(e.GroupTitle),
			"container_extension": extension,
			"custom_sid":          "",
			"direct_source":       buildXCStreamURL(baseURL, "vod", username, password, streamID, extension),
		},
	}
}

// formatXCDuration renders a second count as the HH:MM:SS string XC clients expect.
func formatXCDuration(secs int) string {
	if secs <= 0 {
		return "00:00:00"
	}
	return fmt.Sprintf("%02d:%02d:%02d", secs/3600, (secs%3600)/60, secs%60)
}

// buildLocalSeriesInfo assembles the get_series_info response for a local show,
// expanding the entry the client drilled into to the full season and episode
// tree for its series. Remote series keep their existing default response.
func buildLocalSeriesInfo(e *localscan.MediaEntry, baseURL, username, password string) map[string]any {
	episodes := localscan.EpisodesForSeries(e)
	if len(episodes) == 0 {
		episodes = []*localscan.MediaEntry{e}
	}

	bySeason := make(map[string][]map[string]any)
	seasonCounts := make(map[int]int)
	var seasonOrder []int

	for _, ep := range episodes {
		key := strconv.Itoa(ep.Season)
		if _, seen := seasonCounts[ep.Season]; !seen {
			seasonOrder = append(seasonOrder, ep.Season)
		}
		seasonCounts[ep.Season]++

		extension := utils.NormalizeContainerExtension(localscan.ContainerExtension(ep))
		streamID := localscan.XCStreamID(ep.Hash)

		title := ep.EpisodeTitle
		if title == "" {
			title = ep.Display
		}

		duration := ep.Duration
		if duration < 0 {
			duration = 0
		}

		bySeason[key] = append(bySeason[key], map[string]any{
			"id":                  strconv.Itoa(streamID),
			"episode_num":         ep.Episode,
			"title":               title,
			"container_extension": extension,
			"season":              ep.Season,
			"custom_sid":          "",
			"added":               "0",
			"direct_source":       buildXCStreamURL(baseURL, "series", username, password, streamID, extension),
			"info": map[string]any{
				"movie_image":   localArtURL(baseURL, username, password, ep, "poster"),
				"plot":          ep.Plot,
				"releasedate":   ep.Premiered,
				"rating":        fmt.Sprintf("%.1f", ep.Rating),
				"season":        ep.Season,
				"tmdb_id":       ep.TMDBID,
				"duration_secs": duration,
				"duration":      formatXCDuration(duration),
				"bitrate":       0,
				"video":         []any{},
				"audio":         []any{},
			},
		})
	}

	seasons := make([]map[string]any, 0, len(seasonOrder))
	for _, num := range seasonOrder {
		seasons = append(seasons, map[string]any{
			"id":            num,
			"season_number": num,
			"name":          fmt.Sprintf("Season %d", num),
			"episode_count": seasonCounts[num],
			"overview":      "",
			"air_date":      "",
			"cover":         localArtURL(baseURL, username, password, e, "poster"),
			"cover_big":     localArtURL(baseURL, username, password, e, "poster"),
		})
	}

	cast := make([]string, 0, len(e.Cast))
	for _, p := range e.Cast {
		if p.Name != "" {
			cast = append(cast, p.Name)
		}
	}

	name := e.Series
	if name == "" {
		name = e.Display
	}

	backdrops := []string{}
	if url := localArtURL(baseURL, username, password, e, "fanart"); url != "" {
		backdrops = append(backdrops, url)
	}

	return map[string]any{
		"seasons":  seasons,
		"episodes": bySeason,
		"info": map[string]any{
			"name":             name,
			"cover":            localArtURL(baseURL, username, password, e, "poster"),
			"plot":             e.Plot,
			"cast":             strings.Join(cast, ", "),
			"director":         strings.Join(e.Directors, ", "),
			"genre":            strings.Join(e.Genres, ", "),
			"releaseDate":      e.Premiered,
			"last_modified":    "0",
			"rating":           fmt.Sprintf("%.1f", e.Rating),
			"rating_5based":    e.Rating / 2,
			"backdrop_path":    backdrops,
			"youtube_trailer":  "",
			"episode_run_time": 0,
			"category_id":      categoryIDFromName(e.GroupTitle),
		},
	}
}

// seriesIDForType returns the series_id an XC client needs to drill into a
// series entry, leaving every other content type's value omitted.
func seriesIDForType(contentType string, streamID int) int {
	if contentType == "series" {
		return streamID
	}
	return 0
}

// episodeIDFromChannel mints a stable proxy episode ID from the series channel
// and the episode's season and number. The ID is deliberately independent of any
// provider, so every source carrying the episode resolves through it and
// playback can move between them on failure.
func episodeIDFromChannel(channelName string, season, episode int) int {
	return streamIDFromName(fmt.Sprintf("%s|episode|s%de%d", channelName, season, episode))
}

// remoteSeriesOrigins resolves a proxy series ID to every Xtreme Codes provider
// carrying it, walking the channel's streams from its preferred index and
// skipping dead and blocked entries so the admin's ordering and kill controls
// govern which provider answers first. M3U sources have no info endpoint to ask
// and are omitted.
func remoteSeriesOrigins(sp *proxy.StreamProxy, seriesID int) ([]seriesOrigin, bool) {
	channelName := findChannelByStreamID(sp, seriesID)
	if channelName == "" {
		return nil, false
	}

	channel, exists := sp.Channels.Load(channelName)
	if !exists {
		return nil, false
	}

	channel.Mu.RLock()
	defer channel.Mu.RUnlock()

	if len(channel.Streams) == 0 || getChannelContentType(channel) != "series" {
		return nil, false
	}

	total := len(channel.Streams)
	preferred := int(atomic.LoadInt32(&channel.PreferredStreamIndex))
	if preferred < 0 || preferred >= total {
		preferred = 0
	}

	origins := make([]seriesOrigin, 0, total)
	for i := 0; i < total; i++ {
		stream := channel.Streams[(preferred+i)%total]

		if stream.Source == nil || stream.Source.Username == "" || stream.Source.Password == "" {
			continue
		}
		if deadstreams.IsStreamDead(channelName, stream.URLHash) || atomic.LoadInt32(&stream.Blocked) == 1 {
			continue
		}

		upstreamID := stream.Attributes["tvg-id"]
		if upstreamID == "" {
			continue
		}

		origins = append(origins, seriesOrigin{
			Source:      stream.Source,
			UpstreamID:  upstreamID,
			Attributes:  stream.Attributes,
			ChannelName: channelName,
			StreamHash:  stream.URLHash,
		})
	}

	if len(origins) == 0 {
		return nil, false
	}
	return origins, true
}

// buildRemoteSeriesInfo assembles the get_series_info response for a series
// carried by upstream Xtreme Codes sources. Every provider carrying the series
// is queried and their episode trees are merged, first provider to carry a
// season and episode winning, so a provider with a partial listing no longer
// truncates the tree. Episode IDs and source URLs point back at this proxy, and
// each provider's mappings are persisted under its own key so playback can fail
// over between them. The merged payload is cached for the configured cache
// duration, and a stale payload is preferred over an error when no provider
// answers.
func buildRemoteSeriesInfo(sp *proxy.StreamProxy, seriesID int, baseURL, username, password string) (map[string]any, bool) {
	origins, ok := remoteSeriesOrigins(sp, seriesID)
	if !ok {
		return nil, false
	}
	channelName := origins[0].ChannelName
	cacheKey := strconv.Itoa(seriesID)

	ttl := sp.Config.CacheDuration
	if !sp.Config.CacheEnabled {
		ttl = 0
	}

	cached, fresh := db.GetSeriesInfo(mergedSeriesCacheSource, cacheKey, ttl)
	if fresh && cached != "" {
		var payload map[string]any
		if err := json.Unmarshal([]byte(cached), &payload); err == nil {
			logger.Debug("{handlers/xcoutput - buildRemoteSeriesInfo} Serving cached series info for %s", channelName)
			return payload, true
		}
	}

	merged := make(map[string][]mergedSeriesEpisode)
	seen := make(map[string]bool)
	var seasonsBlock, infoBlock json.RawMessage
	bestCount := 0
	answered := 0

	for _, origin := range origins {
		info, err := parser.FetchXCSeriesInfo(sp.HttpClient, sp.Config, origin.Source, sp.RateLimiterForSource(origin.Source), origin.UpstreamID)
		if err != nil {
			logger.Warn("{handlers/xcoutput - buildRemoteSeriesInfo} Series info fetch failed for %s on %s: %v", channelName, origin.Source.Name, err)
			continue
		}
		answered++

		mappings := make([]db.SeriesEpisode, 0, 64)
		counts := make(map[string]int)

		for season, seasonEpisodes := range info.Episodes {
			for _, ep := range seasonEpisodes {
				if string(ep.ID) == "" {
					continue
				}

				seasonNum, convErr := strconv.Atoi(season)
				if convErr != nil {
					seasonNum, _ = strconv.Atoi(string(ep.Season))
				}

				episodeNum, convErr := strconv.Atoi(string(ep.EpisodeNum))
				if convErr != nil || episodeNum == 0 {
					episodeNum = counts[season] + 1
				}
				counts[season]++

				extension := utils.NormalizeContainerExtension(ep.ContainerExtension)
				episodeID := episodeIDFromChannel(channelName, seasonNum, episodeNum)

				mappings = append(mappings, db.SeriesEpisode{
					EpisodeID:   episodeID,
					ChannelName: channelName,
					Season:      seasonNum,
					Episode:     episodeNum,
					SourceURL:   origin.Source.URL,
					SeriesID:    origin.UpstreamID,
					UpstreamID:  string(ep.ID),
					Extension:   extension,
				})

				key := fmt.Sprintf("%d|%d", seasonNum, episodeNum)
				if seen[key] {
					continue
				}
				seen[key] = true

				seasonKey := strconv.Itoa(seasonNum)
				entry := map[string]any{
					"id":                  strconv.Itoa(episodeID),
					"episode_num":         strconv.Itoa(episodeNum),
					"title":               ep.Title,
					"container_extension": extension,
					"season":              seasonKey,
					"custom_sid":          ep.CustomSID,
					"added":               ep.Added,
					"direct_source":       buildXCStreamURL(baseURL, "series", username, password, episodeID, extension),
				}
				if len(ep.Info) > 0 {
					var epInfo any
					if err := json.Unmarshal(ep.Info, &epInfo); err == nil {
						entry["info"] = epInfo
					}
				}

				merged[seasonKey] = append(merged[seasonKey], mergedSeriesEpisode{num: episodeNum, entry: entry})
			}
		}

		if len(mappings) > 0 {
			if err := db.SetSeriesEpisodes(origin.Source.URL, origin.UpstreamID, mappings); err != nil {
				logger.Warn("{handlers/xcoutput - buildRemoteSeriesInfo} Failed to persist episode mappings for %s on %s: %v", channelName, origin.Source.Name, err)
			}
		}

		if len(mappings) > bestCount {
			bestCount = len(mappings)
			seasonsBlock = info.Seasons
			infoBlock = info.Info
		}
	}

	if len(merged) == 0 {
		if cached != "" {
			var payload map[string]any
			if err := json.Unmarshal([]byte(cached), &payload); err == nil {
				logger.Warn("{handlers/xcoutput - buildRemoteSeriesInfo} No provider returned a tree for %s, serving stale cache", channelName)
				return payload, true
			}
		}
		logger.Error("{handlers/xcoutput - buildRemoteSeriesInfo} No series info available for %s (%d of %d providers answered)", channelName, answered, len(origins))
		return nil, false
	}

	episodes := make(map[string][]map[string]any, len(merged))
	total := 0
	for season, list := range merged {
		sort.SliceStable(list, func(i, j int) bool {
			return list[i].num < list[j].num
		})
		rendered := make([]map[string]any, 0, len(list))
		for _, m := range list {
			rendered = append(rendered, m.entry)
		}
		episodes[season] = rendered
		total += len(rendered)
	}

	payload := map[string]any{
		"seasons":  json.RawMessage("[]"),
		"info":     json.RawMessage("{}"),
		"episodes": episodes,
	}
	if len(seasonsBlock) > 0 {
		payload["seasons"] = seasonsBlock
	}
	if len(infoBlock) > 0 {
		payload["info"] = infoBlock
	}

	if cover := origins[0].Attributes["tvg-logo"]; cover != "" {
		block := map[string]any{}
		if raw, isRaw := payload["info"].(json.RawMessage); isRaw && len(raw) > 0 {
			if err := json.Unmarshal(raw, &block); err != nil {
				block = map[string]any{}
			}
		}
		if block["cover"] == nil {
			block["cover"] = cover
			payload["info"] = block
		}
	}

	if encoded, err := json.Marshal(payload); err == nil {
		if err := db.SetSeriesInfo(mergedSeriesCacheSource, cacheKey, string(encoded)); err != nil {
			logger.Warn("{handlers/xcoutput - buildRemoteSeriesInfo} Failed to cache series info for %s: %v", channelName, err)
		}
	}

	logger.Debug("{handlers/xcoutput - buildRemoteSeriesInfo} Built series info for %s from %d/%d providers: %d seasons, %d episodes", channelName, answered, len(origins), len(episodes), total)
	return payload, true
}

// localArtURL builds the proxied artwork URL for a local entry, or an empty
// string when the entry carries no artwork of that kind.
func localArtURL(baseURL, username, password string, e *localscan.MediaEntry, kind string) string {
	switch kind {
	case "poster":
		if e.Poster == "" {
			return ""
		}
	case "fanart":
		if e.Fanart == "" {
			return ""
		}
	default:
		return ""
	}
	return fmt.Sprintf("%s/localart/%s/%s/%s/%s", baseURL, username, password, e.Hash, kind)
}
