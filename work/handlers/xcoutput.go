package handlers

import (
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"kptv-proxy/work/config"
	"kptv-proxy/work/constants"
	"kptv-proxy/work/epgindex"
	"kptv-proxy/work/localscan"
	"kptv-proxy/work/logger"
	"kptv-proxy/work/proxy"
	"kptv-proxy/work/utils"
	"net/http"
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

// writeCachedXCList serves a player_api list response from the XC data cache,
// building and caching it on a miss. The key carries the import generation and
// the account, since rendered stream URLs embed that account's credentials.
func writeCachedXCList(sp *proxy.StreamProxy, w http.ResponseWriter, username, kind string, build func() any) {
	if !sp.Config.CacheEnabled {
		utils.WriteJSON(w, build())
		return
	}

	key := fmt.Sprintf("xclist_%d_%s_%s", sp.ImportGeneration(), username, kind)
	if cached, ok := sp.Cache.GetXCData(key); ok {
		logger.Debug("{handlers/xcoutput - writeCachedXCList} Serving cached %s for account: %s", kind, username)
		if _, err := w.Write([]byte(cached)); err != nil {
			logger.Error("{handlers/xcoutput - writeCachedXCList} Failed to write cached %s: %v", kind, err)
		}
		return
	}

	payload, err := json.Marshal(build())
	if err != nil {
		logger.Error("{handlers/xcoutput - writeCachedXCList} Failed to encode %s: %v", kind, err)
		return
	}

	sp.Cache.SetXCData(key, string(payload))
	if _, err := w.Write(payload); err != nil {
		logger.Error("{handlers/xcoutput - writeCachedXCList} Failed to write %s: %v", kind, err)
	}
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
			utils.WriteJSON(w, map[string]any{
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
				utils.WriteJSON(w, []xcCategory{})
				return
			}
			writeCachedXCList(sp, w, username, "live_categories", func() any { return buildCategoryList(sp, "live") })

		case "get_live_streams":
			if !account.EnableLive {
				utils.WriteJSON(w, []xcStream{})
				return
			}
			writeCachedXCList(sp, w, username, "live_streams", func() any {
				return buildStreamList(sp, "live", sp.Config.BaseURL, username, password)
			})

		case "get_vod_categories":
			if !account.EnableVOD {
				utils.WriteJSON(w, []xcCategory{})
				return
			}
			writeCachedXCList(sp, w, username, "vod_categories", func() any { return buildCategoryList(sp, "vod") })

		case "get_vod_streams":
			if !account.EnableVOD {
				utils.WriteJSON(w, []xcStream{})
				return
			}
			writeCachedXCList(sp, w, username, "vod_streams", func() any {
				return buildStreamList(sp, "vod", sp.Config.BaseURL, username, password)
			})

		case "get_vod_info":
			if !account.EnableVOD {
				utils.WriteJSON(w, map[string]any{})
				return
			}
			vodID, err := strconv.Atoi(r.URL.Query().Get("vod_id"))
			if err != nil {
				utils.WriteJSON(w, map[string]any{})
				return
			}
			if entry := localscan.FindByXCStreamID(vodID); entry != nil {
				utils.WriteJSON(w, buildLocalVODInfo(entry, sp.Config.BaseURL, username, password))
				return
			}
			utils.WriteJSON(w, map[string]any{
				"user_info":   userInfo,
				"server_info": serverInfo,
			})

		case "get_series_categories":
			if !account.EnableSeries {
				utils.WriteJSON(w, []xcCategory{})
				return
			}
			writeCachedXCList(sp, w, username, "series_categories", func() any { return buildCategoryList(sp, "series") })

		case "get_series":
			if !account.EnableSeries {
				utils.WriteJSON(w, []xcStream{})
				return
			}
			writeCachedXCList(sp, w, username, "series", func() any {
				return buildStreamList(sp, "series", sp.Config.BaseURL, username, password)
			})

		case "get_series_info":
			if !account.EnableSeries {
				utils.WriteJSON(w, map[string]any{})
				return
			}
			seriesID, err := strconv.Atoi(r.URL.Query().Get("series_id"))
			if err != nil {
				utils.WriteJSON(w, map[string]any{})
				return
			}
			if entry := localscan.FindByXCStreamID(seriesID); entry != nil && entry.MediaType == "shows" {
				utils.WriteJSON(w, buildLocalSeriesInfo(entry, sp.Config.BaseURL, username, password))
				return
			}
			if payload, ok := buildRemoteSeriesInfo(sp, seriesID, sp.Config.BaseURL, username, password); ok {
				utils.WriteJSON(w, payload)
				return
			}
			utils.WriteJSON(w, map[string]any{
				"user_info":   userInfo,
				"server_info": serverInfo,
			})

		case "get_short_epg":
			limit := 4
			if l, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && l > 0 {
				limit = min(l, constants.Internal.XCShortEPGMaxLimit)
			}
			utils.WriteJSON(w, buildXCEPGListings(sp, r.URL.Query().Get("stream_id"), limit, false))

		case "get_simple_data_table":
			utils.WriteJSON(w, buildXCEPGListings(sp, r.URL.Query().Get("stream_id"), 0, true))

		default:
			utils.WriteJSON(w, map[string]any{
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
