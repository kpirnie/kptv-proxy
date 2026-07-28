package handlers

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"kptv-proxy/work/config"
	"kptv-proxy/work/epgindex"
	"kptv-proxy/work/localscan"
	"kptv-proxy/work/logger"
	"kptv-proxy/work/proxy"
	"kptv-proxy/work/types"
	"kptv-proxy/work/utils"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
)

// xcUserInfo represents the user_info block in XC API responses.
type xcUserInfo struct {
	Username             string   `json:"username"`
	Password             string   `json:"password"`
	Message              string   `json:"message"`
	Auth                 int      `json:"auth"`
	Status               string   `json:"status"`
	ExpDate              *string  `json:"exp_date"`
	IsTrial              string   `json:"is_trial"`
	ActiveCons           string   `json:"active_cons"`
	CreatedAt            string   `json:"created_at"`
	MaxConnections       string   `json:"max_connections"`
	AllowedOutputFormats []string `json:"allowed_output_formats"`
}

// xcServerInfo represents the server_info block in XC API responses.
type xcServerInfo struct {
	URL            string `json:"url"`
	Port           string `json:"port"`
	HTTPSPort      string `json:"https_port"`
	ServerProtocol string `json:"server_protocol"`
	RTMPPort       string `json:"rtmp_port"`
	Timezone       string `json:"timezone"`
	TimestampNow   int64  `json:"timestamp_now"`
	TimeNow        string `json:"time_now"`
}

// xcStream represents a stream entry (live, VOD, or series) in XC API output.
type xcStream struct {
	Num                int    `json:"num"`
	Name               string `json:"name"`
	StreamType         string `json:"stream_type"`
	StreamID           int    `json:"stream_id"`
	StreamIcon         string `json:"stream_icon"`
	EPGChannelID       string `json:"epg_channel_id"`
	Added              string `json:"added"`
	CategoryID         string `json:"category_id"`
	CustomSid          string `json:"custom_sid"`
	TVArchive          int    `json:"tv_archive"`
	DirectSource       string `json:"direct_source"`
	TVArchiveDuration  int    `json:"tv_archive_duration"`
	ContainerExtension string `json:"container_extension,omitempty"`
}

// xcCategory represents a category in XC API output.
type xcCategory struct {
	CategoryID   string `json:"category_id"`
	CategoryName string `json:"category_name"`
	ParentID     int    `json:"parent_id"`
}

// xcChannelBatch is a lightweight name+channel pair for sorted iteration.
type xcChannelBatch struct {
	name    string
	channel *types.Channel
}

// xcEPGListing is one programme entry in XC EPG API responses. Title and
// description are base64-encoded per the XC API convention.
type xcEPGListing struct {
	ID             string `json:"id"`
	EPGID          string `json:"epg_id"`
	Title          string `json:"title"`
	Lang           string `json:"lang"`
	Start          string `json:"start"`
	End            string `json:"end"`
	Description    string `json:"description"`
	ChannelID      string `json:"channel_id"`
	StartTimestamp string `json:"start_timestamp"`
	StopTimestamp  string `json:"stop_timestamp"`
	NowPlaying     int    `json:"now_playing,omitempty"`
	HasArchive     int    `json:"has_archive"`
}

// getSortedChannels snapshots the channel map and returns it sorted alphabetically
// by channel name. All XC output functions must use this instead of ranging the
// map directly to guarantee consistent ordering across every response.
func getSortedChannels(sp *proxy.StreamProxy) []xcChannelBatch {
	batch := make([]xcChannelBatch, 0, 1000)
	sp.Channels.Range(func(name string, ch *types.Channel) bool {
		batch = append(batch, xcChannelBatch{name, ch})
		return true
	})
	if sp.Config.SortField == "preserve-order" {
		sort.Slice(batch, func(i, j int) bool {
			return xcChannelOriginalOrderLess(batch[i], batch[j])
		})
	} else {
		sort.Slice(batch, func(i, j int) bool {
			return strings.ToLower(batch[i].name) < strings.ToLower(batch[j].name)
		})
	}
	return batch
}

func xcChannelOriginalOrderLess(a, b xcChannelBatch) bool {
	aSourceOrder, aImportOrder := xcChannelOriginalOrder(a.channel)
	bSourceOrder, bImportOrder := xcChannelOriginalOrder(b.channel)
	if aSourceOrder != bSourceOrder {
		return aSourceOrder < bSourceOrder
	}
	if aImportOrder != bImportOrder {
		return aImportOrder < bImportOrder
	}
	return strings.ToLower(a.name) < strings.ToLower(b.name)
}

func xcChannelOriginalOrder(ch *types.Channel) (int, int) {
	ch.Mu.RLock()
	defer ch.Mu.RUnlock()

	if len(ch.Streams) == 0 {
		return int(^uint(0) >> 1), int(^uint(0) >> 1)
	}

	sourceOrder := ch.Streams[0].Source.Order
	importOrder := ch.Streams[0].ImportOrder
	for _, stream := range ch.Streams[1:] {
		if stream.Source.Order < sourceOrder || (stream.Source.Order == sourceOrder && stream.ImportOrder < importOrder) {
			sourceOrder = stream.Source.Order
			importOrder = stream.ImportOrder
		}
	}
	return sourceOrder, importOrder
}

// streamIDFromName generates a stable positive integer stream ID from a channel name
// using FNV32a hashing to produce consistent IDs across restarts.
func streamIDFromName(name string) int {
	h := fnv.New32a()
	h.Write([]byte(name))
	id := int(h.Sum32() & 0x7FFFFFFF)
	if id == 0 {
		id = 1
	}
	return id
}

// categoryIDFromName generates a stable string category ID from a group name.
func categoryIDFromName(name string) string {
	h := fnv.New32a()
	h.Write([]byte(name))
	id := int(h.Sum32() & 0x7FFFFFFF)
	if id == 0 {
		id = 1
	}
	return fmt.Sprintf("%d", id)
}

// buildXCStreamURL constructs an XC direct-source URL for a content type, using the
// stream's real container extension rather than assuming MPEG-TS.
func buildXCStreamURL(baseURL, contentType, username, password string, streamID int, extension string) string {
	pathType := "live"
	suffix := "ts"
	switch contentType {
	case "vod":
		pathType = "movie"
		suffix = utils.NormalizeContainerExtension(extension)
	case "series":
		pathType = "series"
		suffix = utils.NormalizeContainerExtension(extension)
	}
	return fmt.Sprintf("%s/%s/%s/%s/%d.%s", baseURL, pathType, username, password, streamID, suffix)
}

// findXCAccount locates an XC output account by username and password.
func findXCAccount(cfg *config.Config, username, password string) *config.XCOutputAccount {
	for i := range cfg.XCOutputAccounts {
		acc := &cfg.XCOutputAccounts[i]
		if acc.Username == username && acc.Password == password {
			return acc
		}
	}
	return nil
}

// findChannelByStreamID locates a channel name by its hashed stream ID.
func findChannelByStreamID(sp *proxy.StreamProxy, id int) string {
	var found string
	sp.Channels.Range(func(name string, _ *types.Channel) bool {
		if streamIDFromName(name) == id {
			found = name
			return false
		}
		return true
	})
	return found
}

// getChannelContentType returns the content type for a channel.
// Caller must hold the channel read lock.
func getChannelContentType(ch *types.Channel) string {
	if len(ch.Streams) == 0 {
		return "live"
	}
	return string(utils.ContentTypeOfStream(ch.Streams[0]))
}

// buildXCServerInfo constructs the server_info block from the configured base URL.
func buildXCServerInfo(baseURL string) xcServerInfo {
	protocol := "http"
	host := baseURL
	port := "80"

	if strings.HasPrefix(baseURL, "https://") {
		protocol = "https"
		host = strings.TrimPrefix(baseURL, "https://")
		port = "443"
	} else {
		host = strings.TrimPrefix(host, "http://")
	}

	if idx := strings.LastIndex(host, ":"); idx != -1 {
		port = host[idx+1:]
		host = host[:idx]
	}

	return xcServerInfo{
		URL:            host,
		Port:           port,
		HTTPSPort:      "443",
		ServerProtocol: protocol,
		RTMPPort:       "1935",
		Timezone:       "UTC",
		TimestampNow:   time.Now().Unix(),
		TimeNow:        time.Now().Format("2006-01-02 15:04:05"),
	}
}

// buildXCUserInfo constructs the user_info block for an XC output account.
func buildXCUserInfo(account *config.XCOutputAccount) xcUserInfo {
	return xcUserInfo{
		Username:             account.Username,
		Password:             account.Password,
		Message:              "",
		Auth:                 1,
		Status:               "Active",
		ExpDate:              nil,
		IsTrial:              "0",
		ActiveCons:           fmt.Sprintf("%d", account.ActiveConns.Load()),
		CreatedAt:            "0",
		MaxConnections:       fmt.Sprintf("%d", account.MaxConnections),
		AllowedOutputFormats: []string{"ts", "m3u8"},
	}
}

// buildStreamList iterates sorted channels and builds the XC stream list for a
// given content type. Channels are always ordered alphabetically by name.
func buildStreamList(sp *proxy.StreamProxy, contentType, baseURL, username, password string) []xcStream {
	var streams []xcStream
	num := 1

	// channel-name -> mapped epg_id; unmapped channels fall back to the dummy id
	epgMap := proxy.ChannelEPGMap()

	for _, item := range getSortedChannels(sp) {
		item.channel.Mu.RLock()

		if len(item.channel.Streams) == 0 {
			item.channel.Mu.RUnlock()
			continue
		}

		chType := getChannelContentType(item.channel)
		if chType != contentType {
			item.channel.Mu.RUnlock()
			continue
		}

		stream := item.channel.Streams[0]
		attrs := stream.Attributes
		extension := utils.NormalizeContainerExtension(stream.ContainerExtension)
		item.channel.Mu.RUnlock()

		streamID := streamIDFromName(item.name)
		group := attrs["group-title"]
		logo := attrs["tvg-logo"]
		tvgID := proxy.EPGIDForChannel(item.name, epgMap)

		directURL := buildXCStreamURL(baseURL, contentType, username, password, streamID, extension)

		s := xcStream{
			Num:               num,
			Name:              item.name,
			StreamType:        contentType,
			StreamID:          streamID,
			StreamIcon:        logo,
			EPGChannelID:      tvgID,
			Added:             "0",
			CategoryID:        categoryIDFromName(group),
			CustomSid:         "",
			TVArchive:         0,
			DirectSource:      directURL,
			TVArchiveDuration: 0,
		}
		if contentType == "vod" || contentType == "series" {
			s.ContainerExtension = extension
		}

		streams = append(streams, s)
		num++
	}

	for _, e := range localscan.EntriesForContentType(contentType) {
		extension := utils.NormalizeContainerExtension(localscan.ContainerExtension(e))
		streamID := localscan.XCStreamID(e.Hash)

		logo := ""
		if e.Poster != "" {
			logo = fmt.Sprintf("%s/localart/%s/%s/%s/poster", baseURL, username, password, e.Hash)
		}

		streams = append(streams, xcStream{
			Num:                num,
			Name:               e.Display,
			StreamType:         contentType,
			StreamID:           streamID,
			StreamIcon:         logo,
			EPGChannelID:       "",
			Added:              "0",
			CategoryID:         categoryIDFromName(e.GroupTitle),
			CustomSid:          "",
			TVArchive:          0,
			DirectSource:       buildXCStreamURL(baseURL, contentType, username, password, streamID, extension),
			TVArchiveDuration:  0,
			ContainerExtension: extension,
		})
		num++
	}

	return streams
}

// buildCategoryList iterates sorted channels and returns unique categories for a
// given content type. Category order follows first-seen in alphabetical channel order.
func buildCategoryList(sp *proxy.StreamProxy, contentType string) []xcCategory {
	seen := make(map[string]bool)
	var categories []xcCategory

	for _, item := range getSortedChannels(sp) {
		item.channel.Mu.RLock()

		if len(item.channel.Streams) == 0 {
			item.channel.Mu.RUnlock()
			continue
		}

		chType := getChannelContentType(item.channel)
		group := item.channel.Streams[0].Attributes["group-title"]
		item.channel.Mu.RUnlock()

		if chType != contentType || group == "" || seen[group] {
			continue
		}

		seen[group] = true
		categories = append(categories, xcCategory{
			CategoryID:   categoryIDFromName(group),
			CategoryName: group,
			ParentID:     0,
		})
	}

	for _, e := range localscan.EntriesForContentType(contentType) {
		if e.GroupTitle == "" || seen[e.GroupTitle] {
			continue
		}
		seen[e.GroupTitle] = true
		categories = append(categories, xcCategory{
			CategoryID:   categoryIDFromName(e.GroupTitle),
			CategoryName: e.GroupTitle,
			ParentID:     0,
		})
	}

	return categories
}

// HandleXCPlayerAPI handles /player_api.php requests from Xtream Codes compatible clients.
func HandleXCPlayerAPI(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username := r.URL.Query().Get("username")
		password := r.URL.Query().Get("password")
		action := r.URL.Query().Get("action")

		w.Header().Set("Content-Type", "application/json")

		account := findXCAccount(sp.Config, username, password)
		if account == nil {
			logger.Debug("{handlers/xcoutput - HandleXCPlayerAPI} Invalid credentials for username: %s", username)
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]any{
				"user_info": xcUserInfo{Auth: 0, Message: "Invalid credentials"},
			})
			return
		}

		if action != "" {
			if account.ActiveConns.Load() >= int32(account.MaxConnections) {
				logger.Warn("{handlers/xcoutput - HandleXCPlayerAPI} Account %s at connection limit (%d)", account.Name, account.MaxConnections)
				http.Error(w, "Connection limit reached", http.StatusTooManyRequests)
				return
			}
			account.ActiveConns.Add(1)
			defer account.ActiveConns.Add(-1)
		}

		serverInfo := buildXCServerInfo(sp.Config.BaseURL)
		userInfo := buildXCUserInfo(account)

		switch action {
		case "get_live_categories":
			if !account.EnableLive {
				json.NewEncoder(w).Encode([]xcCategory{})
				return
			}
			json.NewEncoder(w).Encode(buildCategoryList(sp, "live"))

		case "get_live_streams":
			if !account.EnableLive {
				json.NewEncoder(w).Encode([]xcStream{})
				return
			}
			json.NewEncoder(w).Encode(buildStreamList(sp, "live", sp.Config.BaseURL, username, password))

		case "get_vod_categories":
			if !account.EnableVOD {
				json.NewEncoder(w).Encode([]xcCategory{})
				return
			}
			json.NewEncoder(w).Encode(buildCategoryList(sp, "vod"))

		case "get_vod_streams":
			if !account.EnableVOD {
				json.NewEncoder(w).Encode([]xcStream{})
				return
			}
			json.NewEncoder(w).Encode(buildStreamList(sp, "vod", sp.Config.BaseURL, username, password))

		case "get_series_categories":
			if !account.EnableSeries {
				json.NewEncoder(w).Encode([]xcCategory{})
				return
			}
			json.NewEncoder(w).Encode(buildCategoryList(sp, "series"))

		case "get_series":
			if !account.EnableSeries {
				json.NewEncoder(w).Encode([]xcStream{})
				return
			}
			json.NewEncoder(w).Encode(buildStreamList(sp, "series", sp.Config.BaseURL, username, password))

		case "get_short_epg":
			limit := 4
			if l, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && l > 0 {
				limit = l
			}
			json.NewEncoder(w).Encode(buildXCEPGListings(sp, r.URL.Query().Get("stream_id"), limit, false))

		case "get_simple_data_table":
			json.NewEncoder(w).Encode(buildXCEPGListings(sp, r.URL.Query().Get("stream_id"), 0, true))

		default:
			json.NewEncoder(w).Encode(map[string]any{
				"user_info":   userInfo,
				"server_info": serverInfo,
			})
		}

		logger.Debug("{handlers/xcoutput - HandleXCPlayerAPI} Handled action '%s' for account: %s", action, account.Name)
	}
}

// HandleXCGetPHP handles /get.php requests, returning a sorted M3U playlist.
func HandleXCGetPHP(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username := r.URL.Query().Get("username")
		password := r.URL.Query().Get("password")
		outputType := r.URL.Query().Get("type")

		account := findXCAccount(sp.Config, username, password)
		if account == nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		if outputType == "m3u_plus" || outputType == "m3u" {
			w.Header().Set("Content-Type", "application/x-mpegURL")
			w.Header().Set("Content-Disposition", "attachment; filename=\"playlist.m3u\"")
			writeXCM3UPlaylist(w, sp, account)
			return
		}

		http.Error(w, "Unsupported output type", http.StatusBadRequest)
	}
}

// HandleXCLiveStream handles live XC requests, canonicalizing a misleading .m3u8
// request to the .ts URL before the continuous MPEG-TS response begins.
func HandleXCLiveStream(sp *proxy.StreamProxy) http.HandlerFunc {
	return handleXCStream(sp, true)
}

// HandleXCStream handles direct VOD and series stream requests from XC clients.
func HandleXCStream(sp *proxy.StreamProxy) http.HandlerFunc {
	return handleXCStream(sp, false)
}

// handleXCStream resolves an XC stream ID to a channel and hands it to the restreamer.
func handleXCStream(sp *proxy.StreamProxy, redirectM3U8 bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		username := vars["username"]
		password := vars["password"]
		rawID := vars["id"]

		account := findXCAccount(sp.Config, username, password)
		if account == nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		id := rawID
		if dotIdx := strings.LastIndex(rawID, "."); dotIdx != -1 {
			id = rawID[:dotIdx]
		}

		streamID, err := strconv.Atoi(id)
		if err != nil {
			http.Error(w, "Invalid stream ID", http.StatusBadRequest)
			return
		}

		channelName := findChannelByStreamID(sp, streamID)
		if channelName == "" {
			// Local media is not a channel — resolve it and serve from disk.
			if entry := localscan.FindByXCStreamID(streamID); entry != nil {
				if !localscan.PathWithinSource(entry.LocalSourceID, entry.Path) {
					logger.Warn("{handlers/xcoutput - handleXCStream} entry path outside source root, refusing: %s", entry.Path)
					http.Error(w, "Stream not found", http.StatusNotFound)
					return
				}
				serveLocalFile(w, r, entry.Path)
				return
			}
			http.Error(w, "Stream not found", http.StatusNotFound)
			return
		}

		channel, exists := sp.Channels.Load(channelName)
		if !exists {
			http.Error(w, "Stream not found", http.StatusNotFound)
			return
		}

		if redirectM3U8 && strings.HasSuffix(strings.ToLower(rawID), ".m3u8") {
			location := id + ".ts"
			if r.URL.RawQuery != "" {
				location += "?" + r.URL.RawQuery
			}
			w.Header().Set("Location", location)
			w.WriteHeader(http.StatusTemporaryRedirect)
			return
		}

		logger.Debug("{handlers/xcoutput - handleXCStream} XC stream: account=%s, id=%d, channel=%s",
			account.Name, streamID, channelName)

		sp.HandleRestreamingClient(w, r, channel)
	}
}

// writeXCM3UPlaylist writes a sorted M3U playlist filtered by account content settings.
func writeXCM3UPlaylist(w http.ResponseWriter, sp *proxy.StreamProxy, account *config.XCOutputAccount) {
	fmt.Fprintf(w, "#EXTM3U\n")

	// channel-name -> mapped epg_id; unmapped channels fall back to the dummy id
	epgMap := proxy.ChannelEPGMap()

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
		if contentType == "series" && !account.EnableSeries {
			continue
		}

		streamID := streamIDFromName(item.name)
		logo := attrs["tvg-logo"]
		group := attrs["group-title"]
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

// HandleXCXMLTV handles /xmltv.php requests, delegating to the EPG handler.
func HandleXCXMLTV(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username := r.URL.Query().Get("username")
		password := r.URL.Query().Get("password")

		account := findXCAccount(sp.Config, username, password)
		if account == nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		logger.Debug("{handlers/xcoutput - HandleXCXMLTV} EPG request for account: %s", account.Name)
		serveEPG(sp)(w, r)
	}
}

// buildXCEPGListings resolves a stream_id to its mapped EPG channel and returns
// the XC epg_listings payload for get_short_epg / get_simple_data_table.
func buildXCEPGListings(sp *proxy.StreamProxy, streamIDStr string, limit int, markNowPlaying bool) map[string]any {
	empty := map[string]any{"epg_listings": []xcEPGListing{}}

	streamID, err := strconv.Atoi(streamIDStr)
	if err != nil {
		return empty
	}

	channelName := findChannelByStreamID(sp, streamID)
	if channelName == "" {
		return empty
	}

	tvgID := proxy.EPGIDForChannel(channelName, proxy.ChannelEPGMap())

	now := time.Now()
	progs := epgindex.Programmes(tvgID, now, limit)
	if len(progs) == 0 {
		return empty
	}

	listings := make([]xcEPGListing, 0, len(progs))
	for i, p := range progs {
		l := xcEPGListing{
			ID:             strconv.Itoa(i + 1),
			EPGID:          strconv.Itoa(streamID),
			Title:          base64.StdEncoding.EncodeToString([]byte(p.Title)),
			Lang:           "",
			Start:          p.Start.Format("2006-01-02 15:04:05"),
			End:            p.Stop.Format("2006-01-02 15:04:05"),
			Description:    base64.StdEncoding.EncodeToString([]byte(p.Desc)),
			ChannelID:      tvgID,
			StartTimestamp: strconv.FormatInt(p.Start.Unix(), 10),
			StopTimestamp:  strconv.FormatInt(p.Stop.Unix(), 10),
			HasArchive:     0,
		}
		if markNowPlaying && !p.Start.After(now) && p.Stop.After(now) {
			l.NowPlaying = 1
		}
		listings = append(listings, l)
	}

	return map[string]any{"epg_listings": listings}
}
