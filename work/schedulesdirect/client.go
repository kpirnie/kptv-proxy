package schedulesdirect

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"kptv-proxy/work/logger"
	"net/http"
	"time"
)

// sdClient wraps HTTP operations for the Schedules Direct API,
// injecting the auth token and standard headers on every request.
type sdClient struct {
	token      string
	httpClient *http.Client
}

// newClient creates a new sdClient with a valid token for the given account.
func newClient(username, password string) (*sdClient, error) {
	token, err := GetToken(username, password)
	if err != nil {
		return nil, fmt.Errorf("could not obtain SD token for %s: %w", username, err)
	}

	return &sdClient{
		token: token,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}, nil
}

// get performs an authenticated GET request to the given SD API path
// and decodes the JSON response into dest.
func (c *sdClient) get(ctx context.Context, path string, dest interface{}) error {
	req, err := http.NewRequestWithContext(ctx, "GET", sdBaseURL+path, nil)
	if err != nil {
		return fmt.Errorf("create GET request for %s: %w", path, err)
	}

	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s failed: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s returned HTTP %d", path, resp.StatusCode)
	}

	if err := json.NewDecoder(resp.Body).Decode(dest); err != nil {
		return fmt.Errorf("decode GET response from %s: %w", path, err)
	}

	logger.Debug("{schedulesdirect/client - get} GET %s OK", path)
	return nil
}

// post performs an authenticated POST request to the given SD API path,
// sending payload as JSON and decoding the response into dest.
func (c *sdClient) post(ctx context.Context, path string, payload interface{}, dest interface{}) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal POST payload for %s: %w", path, err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", sdBaseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create POST request for %s: %w", path, err)
	}

	c.setHeaders(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("POST %s failed: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("POST %s returned HTTP %d", path, resp.StatusCode)
	}

	if err := json.NewDecoder(resp.Body).Decode(dest); err != nil {
		return fmt.Errorf("decode POST response from %s: %w", path, err)
	}

	logger.Debug("{schedulesdirect/client - post} POST %s OK", path)
	return nil
}

// setHeaders applies the standard SD API headers to every request.
func (c *sdClient) setHeaders(req *http.Request) {
	req.Header.Set("token", c.token)
	req.Header.Set("User-Agent", "kptv-proxy/1.0")
}

// SDStatus represents the response from the /status endpoint.
type SDStatus struct {
	Account struct {
		Expires string `json:"expires"`
		Status  string `json:"status"`
	} `json:"account"`
	Lineups []struct {
		ID        string `json:"ID"`
		Name      string `json:"name"`
		Uri       string `json:"uri"`
		IsDeleted bool   `json:"isDeleted"`
	} `json:"lineups"`
	SystemStatus []struct {
		Status  string `json:"status"`
		Message string `json:"message"`
	} `json:"systemStatus"`
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// SDLineup represents a single lineup entry from /lineups.
type SDLineup struct {
	LineupID  string `json:"lineup"`
	Name      string `json:"name"`
	Transport string `json:"transport"`
	Location  string `json:"location"`
	URI       string `json:"uri"`
}

// SDStation represents a station within a lineup.
type SDStation struct {
	StationID         string         `json:"stationID"`
	Name              string         `json:"name"`
	Callsign          string         `json:"callsign"`
	Affiliate         string         `json:"affiliate"`
	BroadcastLanguage []string       `json:"broadcastLanguage"`
	Logo              *SDStationLogo `json:"logo,omitempty"`
}

// SDStationLogo holds the logo URL and dimensions for a station.
type SDStationLogo struct {
	URL    string `json:"URL"`
	Height int    `json:"height"`
	Width  int    `json:"width"`
	MD5    string `json:"md5"`
}

// SDLineupMap is the response from /lineups/{lineupID}.
type SDLineupMap struct {
	Map []struct {
		StationID string `json:"stationID"`
		Channel   string `json:"channel"`
	} `json:"map"`
	Stations []SDStation `json:"stations"`
}

// SDScheduleRequest is a single entry in the schedule POST body.
type SDScheduleRequest struct {
	StationID string   `json:"stationID"`
	Date      []string `json:"date"`
}

// SDScheduleResponse is a single station's schedule response.
type SDScheduleResponse struct {
	StationID string     `json:"stationID"`
	Programs  []SDAiring `json:"programs"`
	Code      int        `json:"code"`
}

// SDAiring represents a single program airing in a schedule.
type SDAiring struct {
	ProgramID          string `json:"programID"`
	AirDateTime        string `json:"airDateTime"`
	Duration           int    `json:"duration"`
	MD5                string `json:"md5"`
	New                bool   `json:"new"`
	LiveTapeDelay      string `json:"liveTapeDelay,omitempty"`
	IsPremiereOrFinale string `json:"isPremiereOrFinale,omitempty"`
}

// SDProgram represents full program detail from the /programs endpoint.
type SDProgram struct {
	ProgramID       string            `json:"programID"`
	Titles          []SDTitle         `json:"titles"`
	EventDetails    *SDEventDetails   `json:"eventDetails,omitempty"`
	Descriptions    *SDDescriptions   `json:"descriptions,omitempty"`
	OriginalAirDate string            `json:"originalAirDate,omitempty"`
	Genres          []string          `json:"genres,omitempty"`
	EpisodeTitle    string            `json:"episodeTitle150,omitempty"`
	Metadata        []SDMetadata      `json:"metadata,omitempty"`
	Cast            []SDCastMember    `json:"cast,omitempty"`
	ContentRating   []SDContentRating `json:"contentRating,omitempty"`
	EntityType      string            `json:"entityType,omitempty"`
	ShowType        string            `json:"showType,omitempty"`
}

// SDTitle holds a program title at a specific max length.
type SDTitle struct {
	Title120 string `json:"title120"`
}

// SDEventDetails contains live event metadata.
type SDEventDetails struct {
	SubType string `json:"subType,omitempty"`
}

// SDDescriptions holds short and long descriptions for a program.
type SDDescriptions struct {
	Description100  []SDDescription `json:"description100,omitempty"`
	Description1000 []SDDescription `json:"description1000,omitempty"`
}

// SDDescription is a single description entry with language.
type SDDescription struct {
	DescriptionLanguage string `json:"descriptionLanguage"`
	Description         string `json:"description"`
}

// SDMetadata holds season/episode metadata for a program.
type SDMetadata struct {
	Gracenote *SDGracenoteMetadata `json:"Gracenote,omitempty"`
}

// SDGracenoteMetadata holds Gracenote-specific season and episode numbers.
type SDGracenoteMetadata struct {
	Season  int `json:"season"`
	Episode int `json:"episode"`
}

// SDCastMember represents a single cast or crew member.
type SDCastMember struct {
	PersonID      string `json:"personId,omitempty"`
	NameID        string `json:"nameId,omitempty"`
	Name          string `json:"name"`
	Role          string `json:"role"`
	CharacterName string `json:"characterName,omitempty"`
}

// SDContentRating holds content rating information.
type SDContentRating struct {
	Body    string `json:"body"`
	Code    string `json:"code"`
	Country string `json:"country,omitempty"`
}
