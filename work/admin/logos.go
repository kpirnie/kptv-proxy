package admin

import (
	"encoding/json"
	"fmt"
	"io"
	"kptv-proxy/work/constants"
	"kptv-proxy/work/db"
	"kptv-proxy/work/epgindex"
	"kptv-proxy/work/logger"
	"kptv-proxy/work/logos"
	"kptv-proxy/work/proxy"
	"kptv-proxy/work/utils"
	"net/http"
	"net/url"
	"strings"

	"github.com/gorilla/mux"
)

// handleGetLogo serves a stored channel logo to the admin UI, which has no XC
// credentials for the public /logo/ route. A hash with no file on disk is
// fetched from its recorded source URL on the spot.
func handleGetLogo(_ *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hash := mux.Vars(r)["hash"]
		if !logos.ValidHash(hash) {
			http.Error(w, "Not found", http.StatusNotFound)
			return
		}

		f, fi, err := logos.Open(hash)
		if err != nil {
			url, ok := logos.URLFor(hash)
			if !ok {
				http.Error(w, "Not found", http.StatusNotFound)
				return
			}

			if _, ferr := logos.EnsureCached(url); ferr != nil {
				logger.Debug("{admin/logos - handleGetLogo} lazy fetch failed for %s: %v", hash, ferr)
				http.Redirect(w, r, url, http.StatusFound)
				return
			}

			f, fi, err = logos.Open(hash)
			if err != nil {
				http.Error(w, "Not found", http.StatusNotFound)
				return
			}
		}
		defer f.Close()

		w.Header().Set("Cache-Control", "public, max-age=604800, immutable")

		http.ServeContent(w, r, hash, fi.ModTime(), f)
	}
}

// handleGetChannelLogo returns the saved logo assignment for a channel along
// with the URL the admin UI should display right now.
func handleGetChannelLogo(_ *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		channelName, err := url.PathUnescape(mux.Vars(r)["channel"])
		if err != nil {
			http.Error(w, "Invalid channel name", http.StatusBadRequest)
			return
		}

		kind := ""
		value := ""
		if l, ok := db.GetChannelLogo(channelName); ok {
			kind = l.Kind
			value = l.Value
		}

		utils.WriteJSON(w, map[string]string{
			"kind":  kind,
			"value": value,
		})
	}
}

// handleSetChannelLogo saves a manual override, an uploaded logo hash, or an
// EPG pull for a channel. Uploads arrive as multipart, everything else as JSON.
func handleSetChannelLogo(_ *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		channelName, err := url.PathUnescape(mux.Vars(r)["channel"])
		if err != nil {
			http.Error(w, "Invalid channel name", http.StatusBadRequest)
			return
		}

		kind := ""
		value := ""

		if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			kind, value, err = uploadedLogo(r)
		} else {
			kind, value, err = requestedLogo(r, channelName)
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if err := db.UpsertChannelLogo(channelName, kind, value); err != nil {
			http.Error(w, "Failed to save logo", http.StatusInternalServerError)
			return
		}

		addLogEntry("info", fmt.Sprintf("Logo set for channel %s (%s)", channelName, kind))
		utils.WriteJSON(w, map[string]string{"status": "success", "kind": kind, "value": value})
	}
}

// handleDeleteChannelLogo clears a channel's logo assignment, dropping it back
// to EPG/provider/default resolution.
func handleDeleteChannelLogo(_ *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		channelName, err := url.PathUnescape(mux.Vars(r)["channel"])
		if err != nil {
			http.Error(w, "Invalid channel name", http.StatusBadRequest)
			return
		}

		if err := db.DeleteChannelLogo(channelName); err != nil {
			http.Error(w, "Failed to clear logo", http.StatusInternalServerError)
			return
		}

		addLogEntry("info", fmt.Sprintf("Logo cleared for channel %s", channelName))
		utils.WriteJSON(w, map[string]string{"status": "success"})
	}
}

// handleGetLogoLibrary returns the hashes of every uploaded logo for the
// set-logo picker.
func handleGetLogoLibrary(_ *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		utils.WriteJSON(w, logos.Library())
	}
}

// handleBulkPullLogosFromEPG assigns the mapped EPG icon to every channel that
// has an EPG mapping and an icon behind it, leaving channels without one alone.
func handleBulkPullLogosFromEPG(_ *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		epgMap := proxy.ChannelEPGMap()
		updated := 0

		for channelName, epgID := range epgMap {
			icon := epgindex.IconFor(epgID)
			if icon == "" {
				continue
			}
			if err := db.UpsertChannelLogo(channelName, "epg", icon); err != nil {
				continue
			}
			updated++
		}

		addLogEntry("info", fmt.Sprintf("Pulled EPG logos for %d channels", updated))
		utils.WriteJSON(w, map[string]int{"updated": updated})
	}
}

// uploadedLogo stores a multipart logo upload and returns its content hash.
func uploadedLogo(r *http.Request) (string, string, error) {
	if err := r.ParseMultipartForm(constants.Internal.LogoMaxBytes); err != nil {
		return "", "", fmt.Errorf("Invalid upload")
	}

	file, _, err := r.FormFile("logo")
	if err != nil {
		return "", "", fmt.Errorf("Missing logo file")
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, constants.Internal.LogoMaxBytes+1))
	if err != nil {
		return "", "", fmt.Errorf("Failed to read upload")
	}

	hash, err := logos.StoreUpload(data)
	if err != nil {
		return "", "", err
	}

	return "upload", hash, nil
}

// requestedLogo reads a JSON logo assignment: a pasted URL, a library hash, or
// a pull from the channel's mapped EPG entry.
func requestedLogo(r *http.Request, channelName string) (string, string, error) {
	var req struct {
		Kind  string `json:"kind"`
		Value string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return "", "", fmt.Errorf("Invalid JSON")
	}

	switch req.Kind {
	case "override":
		value := strings.TrimSpace(req.Value)
		if !strings.HasPrefix(value, "http://") && !strings.HasPrefix(value, "https://") {
			return "", "", fmt.Errorf("Logo URL must be http or https")
		}
		return "override", value, nil

	case "upload":
		if !logos.ValidHash(req.Value) {
			return "", "", fmt.Errorf("Invalid logo hash")
		}
		return "upload", req.Value, nil

	case "epg":
		epgID, ok := proxy.ChannelEPGMap()[channelName]
		if !ok || epgID == "" {
			return "", "", fmt.Errorf("Channel has no EPG mapping")
		}
		icon := epgindex.IconFor(epgID)
		if icon == "" {
			return "", "", fmt.Errorf("Mapped EPG channel has no icon")
		}
		return "epg", icon, nil
	}

	return "", "", fmt.Errorf("Unknown logo kind")
}
