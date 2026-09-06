package handlers

import (
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"kptv-proxy/work/config"
	"kptv-proxy/work/db"
	"kptv-proxy/work/deadstreams"
	"kptv-proxy/work/epgindex"
	"kptv-proxy/work/localscan"
	"kptv-proxy/work/logger"
	"kptv-proxy/work/parser"
	"kptv-proxy/work/proxy"
	"kptv-proxy/work/types"
	"kptv-proxy/work/utils"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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
	SeriesID           int    `json:"series_id,omitempty"`
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

var (
	streamIDIndex    atomic.Pointer[map[int]string]
	streamIDIndexGen atomic.Uint64
	streamIDIndexMu  sync.Mutex
)

// mergedSeriesCacheSource is the synthetic source_url the merged get_series_info
// payload is stored under, since a tree assembled from several providers belongs
// to none of them.
const mergedSeriesCacheSource = "kptv://merged"

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

// groupTitleOf returns a channel's category label, falling back to the source's
// tvg-group and then to All so uncategorized channels still land in a real category.
func groupTitleOf(attrs map[string]string) string {
	if group := attrs["group-title"]; group != "" {
		return group
	}
	if group := attrs["tvg-group"]; group != "" {
		return group
	}
	return "All"
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

// findXCAccount locates an XC output account by username and password. Both
// fields compare in constant time and every account is checked, so neither the
// comparison nor the match position leaks timing information.
func findXCAccount(cfg *config.Config, username, password string) *config.XCOutputAccount {
	var found *config.XCOutputAccount
	for i := range cfg.XCOutputAccounts {
		acc := &cfg.XCOutputAccounts[i]
		u := subtle.ConstantTimeCompare([]byte(acc.Username), []byte(username))
		p := subtle.ConstantTimeCompare([]byte(acc.Password), []byte(password))
		if u&p == 1 {
			found = acc
		}
	}
	return found
}

// acquireXCConnection reserves a connection slot on an XC output account for
// the life of a playback request and returns the release func. The reservation
// is made with a single atomic add and rolled back when it exceeds the limit,
// so concurrent starts cannot both pass a check-then-increment.
func acquireXCConnection(w http.ResponseWriter, account *config.XCOutputAccount) (func(), bool) {
	if account.ActiveConns.Add(1) > int32(account.MaxConnections) {
		account.ActiveConns.Add(-1)
		logger.Warn("{handlers/xcoutput - acquireXCConnection} Account %s at connection limit (%d)", account.Name, account.MaxConnections)
		http.Error(w, "Connection limit reached", http.StatusTooManyRequests)
		return nil, false
	}
	return func() { account.ActiveConns.Add(-1) }, true
}

// findChannelByStreamID resolves an XC stream ID to a channel name through an
// index rebuilt whenever the import generation moves, rather than walking the
// full channel map on every stream request.
func findChannelByStreamID(sp *proxy.StreamProxy, id int) string {
	gen := sp.ImportGeneration()

	m := streamIDIndex.Load()
	if m == nil || streamIDIndexGen.Load() != gen {
		m = rebuildStreamIDIndex(sp, gen)
	}
	return (*m)[id]
}

// rebuildStreamIDIndex snapshots stream ID to channel name for the supplied
// import generation.
func rebuildStreamIDIndex(sp *proxy.StreamProxy, gen uint64) *map[int]string {
	streamIDIndexMu.Lock()
	defer streamIDIndexMu.Unlock()

	if m := streamIDIndex.Load(); m != nil && streamIDIndexGen.Load() == gen {
		return m
	}

	index := make(map[int]string)
	sp.Channels.Range(func(name string, _ *types.Channel) bool {
		index[streamIDFromName(name)] = name
		return true
	})

	streamIDIndex.Store(&index)
	streamIDIndexGen.Store(gen)
	return &index
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
		group := groupTitleOf(attrs)
		logo := attrs["tvg-logo"]
		tvgID := proxy.EPGIDForChannel(item.name, epgMap)

		directURL := buildXCStreamURL(baseURL, contentType, username, password, streamID, extension)

		s := xcStream{
			Num:               num,
			Name:              item.name,
			StreamType:        contentType,
			StreamID:          streamID,
			SeriesID:          seriesIDForType(contentType, streamID),
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

	if contentType == "series" {
		for _, e := range localscan.SeriesForExport() {
			seriesID := localscan.XCStreamID(e.Hash)

			name := e.Series
			if name == "" {
				name = e.Display
			}

			streams = append(streams, xcStream{
				Num:          num,
				Name:         name,
				StreamType:   "series",
				SeriesID:     seriesID,
				StreamIcon:   localArtURL(baseURL, username, password, e, "poster"),
				EPGChannelID: "",
				Added:        "0",
				CategoryID:   categoryIDFromName(localscan.SeriesCategory(e)),
				CustomSid:    "",
				DirectSource: "",
			})
			num++
		}
		return streams
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
		group := groupTitleOf(item.channel.Streams[0].Attributes)
		item.channel.Mu.RUnlock()

		if chType != contentType || seen[group] {
			continue
		}

		seen[group] = true
		categories = append(categories, xcCategory{
			CategoryID:   categoryIDFromName(group),
			CategoryName: group,
			ParentID:     0,
		})
	}

	if contentType == "series" {
		for _, e := range localscan.SeriesForExport() {
			group := localscan.SeriesCategory(e)
			if group == "" || seen[group] {
				continue
			}
			seen[group] = true
			categories = append(categories, xcCategory{
				CategoryID:   categoryIDFromName(group),
				CategoryName: group,
				ParentID:     0,
			})
		}
		return categories
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
			release, ok := acquireXCConnection(w, account)
			if !ok {
				return
			}
			defer release()
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

		case "get_vod_info":
			if !account.EnableVOD {
				json.NewEncoder(w).Encode(map[string]any{})
				return
			}
			vodID, err := strconv.Atoi(r.URL.Query().Get("vod_id"))
			if err != nil {
				json.NewEncoder(w).Encode(map[string]any{})
				return
			}
			if entry := localscan.FindByXCStreamID(vodID); entry != nil {
				json.NewEncoder(w).Encode(buildLocalVODInfo(entry, sp.Config.BaseURL, username, password))
				return
			}
			json.NewEncoder(w).Encode(map[string]any{
				"user_info":   userInfo,
				"server_info": serverInfo,
			})

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

		case "get_series_info":
			if !account.EnableSeries {
				json.NewEncoder(w).Encode(map[string]any{})
				return
			}
			seriesID, err := strconv.Atoi(r.URL.Query().Get("series_id"))
			if err != nil {
				json.NewEncoder(w).Encode(map[string]any{})
				return
			}
			if entry := localscan.FindByXCStreamID(seriesID); entry != nil && entry.MediaType == "shows" {
				json.NewEncoder(w).Encode(buildLocalSeriesInfo(entry, sp.Config.BaseURL, username, password))
				return
			}
			if payload, ok := buildRemoteSeriesInfo(sp, seriesID, sp.Config.BaseURL, username, password); ok {
				json.NewEncoder(w).Encode(payload)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{
				"user_info":   userInfo,
				"server_info": serverInfo,
			})

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

		// find the account
		account := findXCAccount(sp.Config, username, password)
		if account == nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		// get the connection
		release, ok := acquireXCConnection(w, account)
		if !ok {
			return
		}
		defer release()

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

			// Remote series episodes are not channels — resolve every provider
			// carrying the episode and stream from the first that answers.
			if serveSeriesEpisode(sp, w, r, streamID) {
				return
			}

			// Local media is not a channel — resolve it and serve from disk.
			if entry := localscan.FindByXCStreamID(streamID); entry != nil {
				if !localscan.PathWithinSource(entry.LocalSourceID, entry.Path) {
					logger.Warn("{handlers/xcoutput - handleXCStream} entry path outside source root, refusing: %s", entry.Path)
					http.Error(w, "Stream not found", http.StatusNotFound)
					return
				}
				release, ok := sp.AcquireClientSlot()
				if !ok {
					http.Error(w, "Server at capacity", http.StatusServiceUnavailable)
					return
				}
				defer release()
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

		channel.Mu.RLock()
		isVOD := getChannelContentType(channel) == "vod"
		channel.Mu.RUnlock()

		if isVOD {
			logger.Debug("{handlers/xcoutput - handleXCStream} XC VOD: account=%s, id=%d, channel=%s",
				account.Name, streamID, channelName)
			serveVODChannel(sp, w, r, channel)
			return
		}

		logger.Debug("{handlers/xcoutput - handleXCStream} XC stream: account=%s, id=%d, channel=%s",
			account.Name, streamID, channelName)

		sp.HandleRestreamingClient(w, r, channel)
	}
}

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
		if contentType == "series" {
			if !account.EnableSeries {
				continue
			}
			writeSeriesEpisodeEntries(w, sp, account, item.name, attrs)
			continue
		}

		streamID := streamIDFromName(item.name)
		logo := attrs["tvg-logo"]
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
