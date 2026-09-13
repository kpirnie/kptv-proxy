package handlers

import (
	"encoding/json"
	"fmt"
	"kptv-proxy/work/config"
	"kptv-proxy/work/db"
	"kptv-proxy/work/localscan"
	"kptv-proxy/work/logger"
	"kptv-proxy/work/logos"
	"kptv-proxy/work/proxy"
	"kptv-proxy/work/utils"
	"net/http"
	"sort"
	"strconv"
)

// writeXCM3UPlaylist writes a sorted M3U playlist filtered by account content settings.
func writeXCM3UPlaylist(w http.ResponseWriter, sp *proxy.StreamProxy, account *config.XCOutputAccount) {
	fmt.Fprintf(w, "#EXTM3U\n")

	// channel-name -> mapped epg_id; unmapped channels fall back to the dummy id
	epgMap := proxy.ChannelEPGMap()

	// live logos resolve through the proxy; vod and series keep provider art
	logoResolver := logos.NewResolver(fmt.Sprintf("%s/logo/%s/%s", sp.Config.BaseURL, account.Username, account.Password), epgMap)

	for _, item := range getSortedChannels(sp) {
		item.channel.Mu.RLock()

		if len(item.channel.Streams) == 0 {
			item.channel.Mu.RUnlock()
			continue
		}

		contentType := getChannelContentType(item.channel)
		stream := item.channel.Streams[0]
		attrs := stream.Attributes
		extension := utils.NormalizeContainerExtension(stream.ContainerExtension)
		item.channel.Mu.RUnlock()

		if contentType == "live" && !account.EnableLive {
			continue
		}
		if contentType == "vod" && !account.EnableVOD {
			continue
		}
		if contentType == "series" {
			if !account.EnableSeries {
				continue
			}
			writeSeriesEpisodeEntries(w, sp, account, item.name, attrs)
			continue
		}

		streamID := streamIDFromName(item.name)
		logo := attrs["tvg-logo"]
		if contentType == "live" {
			logo = logoResolver.For(item.name, logo)
		}
		group := groupTitleOf(attrs)
		tvgID := proxy.EPGIDForChannel(item.name, epgMap)

		// mapped channels advertise the raw mapped epg id on all three
		// guide-matching attributes; unmapped fall back to the dummy id
		epgAttrs := fmt.Sprintf(" tvg-id=\"%s\"", tvgID)
		if tvgID != proxy.DummyChannelID {
			epgAttrs = fmt.Sprintf(" tvg-id=\"%s\" tvg-epgid=\"%s\" tvc-guide-stationid=\"%s\"", tvgID, tvgID, tvgID)
		}

		displayName := utils.SanitizeM3UDisplayName(item.name)
		fmt.Fprintf(w, "#EXTINF:-1%s tvg-name=\"%s\" tvg-logo=\"%s\" group-title=\"%s\",%s\n",
			epgAttrs, utils.EscapeM3UAttribute(displayName), utils.EscapeM3UAttribute(logo), utils.EscapeM3UAttribute(group), displayName)
		fmt.Fprintln(w, buildXCStreamURL(sp.Config.BaseURL, contentType, account.Username, account.Password, streamID, extension))

	}

	for _, e := range localscan.ExportEntries() {
		contentType := localscan.ContentTypeOf(e.MediaType)
		if contentType == "vod" && !account.EnableVOD {
			continue
		}
		if contentType == "series" && !account.EnableSeries {
			continue
		}

		extension := utils.NormalizeContainerExtension(localscan.ContainerExtension(e))
		streamID := localscan.XCStreamID(e.Hash)

		logo := ""
		if e.Poster != "" {
			logo = fmt.Sprintf("%s/localart/%s/%s/%s/poster", sp.Config.BaseURL, account.Username, account.Password, e.Hash)
		}

		displayName := utils.SanitizeM3UDisplayName(e.Display)
		fmt.Fprintf(w, "#EXTINF:-1 tvg-name=\"%s\" tvg-logo=\"%s\" group-title=\"%s\",%s\n",
			utils.EscapeM3UAttribute(displayName), utils.EscapeM3UAttribute(logo),
			utils.EscapeM3UAttribute(e.GroupTitle), displayName)
		fmt.Fprintln(w, buildXCStreamURL(sp.Config.BaseURL, contentType, account.Username, account.Password, streamID, extension))
	}
}

// writeSeriesEpisodeEntries expands a series channel into one M3U line per
// episode, read from the stored get_series_info payload. M3U carries no episode
// tree of its own, so a bare series entry points at the provider's series
// container and cannot play. The payload is used at whatever age it has and is
// never fetched here — building a playlist must not trigger a provider call per
// series — so a series no XC client has opened yet contributes nothing.
func writeSeriesEpisodeEntries(w http.ResponseWriter, sp *proxy.StreamProxy, account *config.XCOutputAccount, channelName string, attrs map[string]string) {

	if _, ok := remoteSeriesOrigins(sp, streamIDFromName(channelName)); !ok {
		return
	}

	var payload struct {
		Episodes map[string][]cachedSeriesEpisode `json:"episodes"`
	}

	cached, _ := db.GetSeriesInfo(mergedSeriesCacheSource, strconv.Itoa(streamIDFromName(channelName)), 0)
	if cached != "" {
		if err := json.Unmarshal([]byte(cached), &payload); err != nil {
			logger.Debug("{handlers/xcoutput - writeSeriesEpisodeEntries} Unreadable cached tree for %s: %v", channelName, err)
		}
	}

	if len(payload.Episodes) == 0 {
		logger.Debug("{handlers/xcoutput - writeSeriesEpisodeEntries} No cached episode tree for %s, omitting from playlist", channelName)
		return
	}

	seasons := make([]string, 0, len(payload.Episodes))
	for season := range payload.Episodes {
		seasons = append(seasons, season)
	}
	sort.Slice(seasons, func(i, j int) bool {
		a, aErr := strconv.Atoi(seasons[i])
		b, bErr := strconv.Atoi(seasons[j])
		if aErr == nil && bErr == nil {
			return a < b
		}
		return seasons[i] < seasons[j]
	})

	logo := attrs["tvg-logo"]
	group := groupTitleOf(attrs)

	for _, season := range seasons {
		seasonNum, _ := strconv.Atoi(season)
		episodes := payload.Episodes[season]

		sort.SliceStable(episodes, func(i, j int) bool {
			a, aErr := strconv.Atoi(string(episodes[i].EpisodeNum))
			b, bErr := strconv.Atoi(string(episodes[j].EpisodeNum))
			if aErr == nil && bErr == nil {
				return a < b
			}
			return false
		})

		for _, ep := range episodes {
			episodeID, err := strconv.Atoi(ep.ID)
			if err != nil {
				continue
			}

			episodeNum, _ := strconv.Atoi(string(ep.EpisodeNum))
			extension := utils.NormalizeContainerExtension(ep.ContainerExtension)

			name := fmt.Sprintf("%s - S%02dE%02d", channelName, seasonNum, episodeNum)
			if ep.Title != "" {
				name += " - " + ep.Title
			}

			displayName := utils.SanitizeM3UDisplayName(name)
			fmt.Fprintf(w, "#EXTINF:-1 tvg-name=\"%s\" tvg-logo=\"%s\" group-title=\"%s\",%s\n",
				utils.EscapeM3UAttribute(displayName), utils.EscapeM3UAttribute(logo), utils.EscapeM3UAttribute(group), displayName)
			fmt.Fprintln(w, buildXCStreamURL(sp.Config.BaseURL, "series", account.Username, account.Password, episodeID, extension))
		}
	}
}
