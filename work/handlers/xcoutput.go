package handlers

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"kptv-proxy/work/config"
	"kptv-proxy/work/logger"
	"kptv-proxy/work/proxy"
	"kptv-proxy/work/types"
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
	StreamIcon         string `json:"stream_icon"`
	EPGChannelID       string `json:"epg_channel_id"`
	Added              string `json:"added"`
	CategoryID         string `json:"category_id"`
	CustomSid          string `json:"custom_sid"`
	TVArchive          int    `json:"tv_archive"`
	DirectSource       string `json:"direct_source"`
	TVArchiveDuration  int    `json:"tv_archive_duration"`
	ContainerExtension string `json:"container_extension,omitempty"` // VOD/series only
}

// xcCategory represents a category in XC API output.
type xcCategory struct {
	CategoryID   string `json:"category_id"`
	CategoryName string `json:"category_name"`
	ParentID     int    `json:"parent_id"`
}

// streamIDFromName generates a stable positive integer stream ID from a channel name
// using FNV32a hashing to produce consistent IDs across restarts.
func streamIDFromName(name string) int {
	h := fnv.New32a()
	h.Write([]byte(name))
	id := int(h.Sum32() & 0x7FFFFFFF) // ensure positive
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
// Returns empty string if no matching channel is found.
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

// getChannelContentType returns the content type ("live", "vod", or "series")
// for a channel based on its group-title attribute. The caller must hold the channel read lock.
func getChannelContentType(ch *types.Channel) string {
	if len(ch.Streams) == 0 {
		return "live"
	}
	group := strings.ToLower(ch.Streams[0].Attributes["group-title"])
	if group == "series" || strings.Contains(group, "series") {
		return "series"
	}
	if group == "vod" || strings.Contains(group, "vod") || strings.Contains(group, "movie") {
		return "vod"
	}
	return "live"
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

	// Split host:port if port is included in the base URL
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

// buildStreamList iterates channels and builds the XC stream list for a given content type.
func buildStreamList(sp *proxy.StreamProxy, contentType, baseURL, username, password string) []xcStream {
	var streams []xcStream
	num := 1

	sp.Channels.Range(func(name string, ch *types.Channel) bool {
		ch.Mu.RLock()

		if len(ch.Streams) == 0 {
			ch.Mu.RUnlock()
			return true
		}

		chType := getChannelContentType(ch)
		if chType != contentType {
			ch.Mu.RUnlock()
			return true
		}

		attrs := ch.Streams[0].Attributes
		ch.Mu.RUnlock()

		streamID := streamIDFromName(name)
		group := attrs["group-title"]
		logo := attrs["tvg-logo"]
		tvgID := attrs["tvg-id"]

		// Construct direct stream URL pointing back to our XC stream endpoint
		var directURL string
		switch contentType {
		case "vod":
			directURL = fmt.Sprintf("%s/movie/%s/%s/%d.ts", baseURL, username, password, streamID)
		case "series":
			directURL = fmt.Sprintf("%s/series/%s/%s/%d.ts", baseURL, username, password, streamID)
		default:
			directURL = fmt.Sprintf("%s/live/%s/%s/%d.ts", baseURL, username, password, streamID)
		}

		s := xcStream{
			Num:               num,
			Name:              name,
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
			s.ContainerExtension = "ts"
		}

		streams = append(streams, s)
		num++
		return true
	})

	return streams
}

// buildCategoryList iterates channels and returns unique categories for a given content type.
func buildCategoryList(sp *proxy.StreamProxy, contentType string) []xcCategory {
	seen := make(map[string]bool)
	var categories []xcCategory

	sp.Channels.Range(func(name string, ch *types.Channel) bool {
		ch.Mu.RLock()

		if len(ch.Streams) == 0 {
			ch.Mu.RUnlock()
			return true
		}

		chType := getChannelContentType(ch)
		group := ch.Streams[0].Attributes["group-title"]
		ch.Mu.RUnlock()

		if chType != contentType || group == "" || seen[group] {
			return true
		}

		seen[group] = true
		categories = append(categories, xcCategory{
			CategoryID:   categoryIDFromName(group),
			CategoryName: group,
			ParentID:     0,
		})
		return true
	})

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
			json.NewEncoder(w).Encode(map[string]interface{}{
				"user_info": xcUserInfo{Auth: 0, Message: "Invalid credentials"},
			})
			return
		}

		// Enforce connection limit for data-fetching actions
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

		default:
			// Default covers user_info action and bare auth check (no action param)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"user_info":   userInfo,
				"server_info": serverInfo,
			})
		}

		logger.Debug("{handlers/xcoutput - HandleXCPlayerAPI} Handled action '%s' for account: %s", action, account.Name)
	}
}

// HandleXCGetPHP handles /get.php requests from Xtream Codes compatible clients,
// returning a filtered M3U playlist based on account content type settings.
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

// HandleXCStream handles direct stream requests from XC clients via the routes:
//
//	/live/{username}/{password}/{id}
//	/movie/{username}/{password}/{id}
//	/series/{username}/{password}/{id}
//
// The {id} path variable may include a file extension (e.g. 12345.ts) which is stripped
// before looking up the channel by its hashed stream ID.
func HandleXCStream(sp *proxy.StreamProxy) http.HandlerFunc {
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

		// Strip file extension (.ts, .m3u8, .mp4, etc.)
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
			http.Error(w, "Stream not found", http.StatusNotFound)
			return
		}

		channel, exists := sp.Channels.Load(channelName)
		if !exists {
			http.Error(w, "Stream not found", http.StatusNotFound)
			return
		}

		logger.Debug("{handlers/xcoutput - HandleXCStream} XC stream: account=%s, id=%d, channel=%s",
			account.Name, streamID, channelName)

		sp.HandleRestreamingClient(w, r, channel)
	}
}

// writeXCM3UPlaylist writes an M3U playlist to w, filtered by the account's content settings.
func writeXCM3UPlaylist(w http.ResponseWriter, sp *proxy.StreamProxy, account *config.XCOutputAccount) {
	fmt.Fprintf(w, "#EXTM3U\n")

	sp.Channels.Range(func(name string, ch *types.Channel) bool {
		ch.Mu.RLock()

		if len(ch.Streams) == 0 {
			ch.Mu.RUnlock()
			return true
		}

		contentType := getChannelContentType(ch)
		attrs := ch.Streams[0].Attributes
		ch.Mu.RUnlock()

		// Apply per-account content type filters
		if contentType == "live" && !account.EnableLive {
			return true
		}
		if contentType == "vod" && !account.EnableVOD {
			return true
		}
		if contentType == "series" && !account.EnableSeries {
			return true
		}

		streamID := streamIDFromName(name)
		logo := attrs["tvg-logo"]
		group := attrs["group-title"]
		tvgID := attrs["tvg-id"]

		fmt.Fprintf(w, "#EXTINF:-1 tvg-id=\"%s\" tvg-name=\"%s\" tvg-logo=\"%s\" group-title=\"%s\",%s\n",
			tvgID, name, logo, group, name)
		fmt.Fprintf(w, "%s/live/%s/%s/%d.ts\n",
			sp.Config.BaseURL, account.Username, account.Password, streamID)

		return true
	})
}
