package handlers

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"hash/fnv"
	"kptv-proxy/work/logger"
	"kptv-proxy/work/proxy"
	"kptv-proxy/work/utils"
	"net/http"

	"github.com/gorilla/mux"
)

// HDHomeRun device constants. These are intentionally hardcoded — there is no
// need to expose tuner count or device name as user-configurable values.
const (
	hdhrTunerCount      = 4
	hdhrDeviceName      = "KPTV Proxy"
	hdhrManufacturer    = "Silicondust"
	hdhrModelNumber     = "HDHR3-US"
	hdhrFirmwareName    = "hdhomerun3_atsc"
	hdhrFirmwareVersion = "20190621"
)

// hdhrDiscoverResponse mirrors the JSON payload that real HDHomeRun devices
// return at /discover.json. Plex, Emby, Jellyfin, and Channels DVR all parse
// this to identify the device type and locate the lineup URL.
type hdhrDiscoverResponse struct {
	FriendlyName    string `json:"FriendlyName"`
	Manufacturer    string `json:"Manufacturer"`
	ModelNumber     string `json:"ModelNumber"`
	FirmwareName    string `json:"FirmwareName"`
	TunerCount      int    `json:"TunerCount"`
	FirmwareVersion string `json:"FirmwareVersion"`
	DeviceID        string `json:"DeviceID"`
	DeviceAuth      string `json:"DeviceAuth"`
	BaseURL         string `json:"BaseURL"`
	LineupURL       string `json:"LineupURL"`
}

// hdhrLineupStatus is returned at /lineup_status.json. Clients check
// ScanInProgress before trusting the lineup — we always report idle/ready.
type hdhrLineupStatus struct {
	ScanInProgress int      `json:"ScanInProgress"`
	ScanPossible   int      `json:"ScanPossible"`
	Source         string   `json:"Source"`
	SourceList     []string `json:"SourceList"`
}

// hdhrLineupEntry represents a single channel in the HDHomeRun lineup.
// GuideNumber is hash-derived from the channel name so it is fully stable
// across proxy restarts and import refresh cycles.
type hdhrLineupEntry struct {
	GuideNumber string `json:"GuideNumber"`
	GuideName   string `json:"GuideName"`
	URL         string `json:"URL"`
	HD          int    `json:"HD"`
	Favorite    int    `json:"Favorite"`
}

// hdhrDeviceXML is the UPnP/DLNA device descriptor served at /device.xml.
// Some clients validate this before accepting the lineup — Plex in particular
// checks it during manual IP entry.
type hdhrDeviceXML struct {
	XMLName     xml.Name        `xml:"root"`
	XMLNS       string          `xml:"xmlns,attr"`
	SpecVersion hdhrSpecVersion `xml:"specVersion"`
	URLBase     string          `xml:"URLBase"`
	Device      hdhrXMLDevice   `xml:"device"`
}

type hdhrSpecVersion struct {
	Major int `xml:"major"`
	Minor int `xml:"minor"`
}

type hdhrXMLDevice struct {
	DeviceType   string `xml:"deviceType"`
	FriendlyName string `xml:"friendlyName"`
	Manufacturer string `xml:"manufacturer"`
	ModelName    string `xml:"modelName"`
	ModelNumber  string `xml:"modelNumber"`
	SerialNumber string `xml:"serialNumber"`
	UDN          string `xml:"UDN"`
}

// hdhrDeviceID derives a stable 8-hex-char device identifier from the configured
// base URL using FNV32a. Consistent across restarts since it is deterministic.
func hdhrDeviceID(baseURL string) string {
	h := fnv.New32a()
	h.Write([]byte(baseURL))
	return fmt.Sprintf("%08X", h.Sum32())
}

// hdhrGuideNumber derives a stable 1-9999 channel number from a channel name
// using FNV32a. Because channel names are unique in the proxy channel map,
// there can be no collisions. Numbers are consistent across restarts.
func hdhrGuideNumber(channelName string) string {
	h := fnv.New32a()
	h.Write([]byte(channelName))
	n := (int(h.Sum32()&0x7FFFFFFF) % 9999) + 1
	return fmt.Sprintf("%d", n)
}

// SetupHDHRRoutes registers all HDHomeRun emulation endpoints on the provided
// router. No config toggle is needed — these routes are always active when the
// proxy is running. Clients point directly at the proxy IP and port.
func SetupHDHRRoutes(router *mux.Router, sp *proxy.StreamProxy) {
	router.HandleFunc("/discover.json", handleHDHRDiscover(sp)).Methods("GET")
	router.HandleFunc("/device.xml", handleHDHRDeviceXML(sp)).Methods("GET")
	router.HandleFunc("/lineup_status.json", handleHDHRLineupStatus()).Methods("GET")
	router.HandleFunc("/lineup.json", handleHDHRLineup(sp)).Methods("GET")
	router.HandleFunc("/lineup.post", handleHDHRLineupPost()).Methods("POST", "GET")

	logger.Info("HDHomeRun emulation active (deviceID: %s)", hdhrDeviceID(sp.Config.BaseURL))
}

// handleHDHRDiscover serves /discover.json — the primary device identification
// endpoint consumed by all HDHomeRun-compatible client applications.
func handleHDHRDiscover(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		json.NewEncoder(w).Encode(hdhrDiscoverResponse{
			FriendlyName:    hdhrDeviceName,
			Manufacturer:    hdhrManufacturer,
			ModelNumber:     hdhrModelNumber,
			FirmwareName:    hdhrFirmwareName,
			TunerCount:      hdhrTunerCount,
			FirmwareVersion: hdhrFirmwareVersion,
			DeviceID:        hdhrDeviceID(sp.Config.BaseURL),
			DeviceAuth:      "kptv1234",
			BaseURL:         sp.Config.BaseURL,
			LineupURL:       sp.Config.BaseURL + "/lineup.json",
		})

		logger.Debug("{handlers/hdhr - handleHDHRDiscover} served discover.json to %s", r.RemoteAddr)
	}
}

// handleHDHRDeviceXML serves /device.xml — the UPnP/DLNA descriptor used by
// clients that perform deeper device validation before accepting the lineup.
func handleHDHRDeviceXML(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")

		deviceID := hdhrDeviceID(sp.Config.BaseURL)

		doc := hdhrDeviceXML{
			XMLNS:   "urn:schemas-upnp-org:device-1-0",
			URLBase: sp.Config.BaseURL,
			SpecVersion: hdhrSpecVersion{
				Major: 1,
				Minor: 0,
			},
			Device: hdhrXMLDevice{
				DeviceType:   "urn:schemas-upnp-org:device:MediaServer:1",
				FriendlyName: hdhrDeviceName,
				Manufacturer: hdhrManufacturer,
				ModelName:    "HDHomeRun DUAL",
				ModelNumber:  hdhrModelNumber,
				SerialNumber: deviceID,
				// UDN must be globally unique and stable — derived from deviceID
				// so it never changes for a given base URL configuration.
				UDN: fmt.Sprintf("uuid:d7bce490-e7e6-4d2c-%s-%s", deviceID[:4], deviceID[4:]),
			},
		}

		w.Write([]byte(xml.Header))
		enc := xml.NewEncoder(w)
		enc.Indent("", "  ")
		enc.Encode(doc)

		logger.Debug("{handlers/hdhr - handleHDHRDeviceXML} served device.xml to %s", r.RemoteAddr)
	}
}

// handleHDHRLineupStatus serves /lineup_status.json. We always report idle with
// no scan in progress — the lineup is generated on demand from the channel map.
func handleHDHRLineupStatus() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(hdhrLineupStatus{
			ScanInProgress: 0,
			ScanPossible:   1,
			Source:         "Cable",
			SourceList:     []string{"Cable"},
		})
	}
}

// handleHDHRLineup serves /lineup.json — the full channel list consumed by
// client apps to populate their guide. Channels are sorted alphabetically by
// name (consistent with playlist and XC output) and assigned stable
// hash-derived guide numbers via hdhrGuideNumber.
func handleHDHRLineup(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		lineup := make([]hdhrLineupEntry, 0, 1000)

		// getSortedChannels is defined in xcoutput.go (same package) and
		// guarantees consistent alphabetical ordering across all output formats.
		for _, item := range getSortedChannels(sp) {
			item.channel.Mu.RLock()
			empty := len(item.channel.Streams) == 0
			item.channel.Mu.RUnlock()

			if empty {
				continue
			}

			lineup = append(lineup, hdhrLineupEntry{
				GuideNumber: hdhrGuideNumber(item.name),
				GuideName:   item.name,
				URL:         fmt.Sprintf("%s/s/%s", sp.Config.BaseURL, utils.SanitizeChannelName(item.name)),
				HD:          1,
				Favorite:    0,
			})
		}

		json.NewEncoder(w).Encode(lineup)
		logger.Debug("{handlers/hdhr - handleHDHRLineup} served %d channels to %s", len(lineup), r.RemoteAddr)
	}
}

// handleHDHRLineupPost is a no-op stub for /lineup.post. Client apps (Plex in
// particular) POST here to trigger a channel scan. We accept it and return 200
// immediately — our lineup is always current and requires no scan process.
func handleHDHRLineupPost() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		logger.Debug("{handlers/hdhr - handleHDHRLineupPost} scan request acknowledged from %s", r.RemoteAddr)
	}
}
