package parser

import (
	"context"
	"encoding/json"
	"fmt"
	"kptv-proxy/work/client"
	"kptv-proxy/work/config"
	"kptv-proxy/work/logger"
	"kptv-proxy/work/utils"
	"net/http"
	"time"

	"go.uber.org/ratelimit"
)

// XCEpisode represents a single episode entry from an Xtreme Codes
// get_series_info response. Info is carried through verbatim so the proxy's
// own response matches what the provider's panel returns.
type XCEpisode struct {
	ID                 XCID            `json:"id"`
	EpisodeNum         XCID            `json:"episode_num"`
	Title              XCID            `json:"title"`
	ContainerExtension string          `json:"container_extension"`
	Season             XCID            `json:"season"`
	CustomSID          XCID            `json:"custom_sid"`
	Added              XCID            `json:"added"`
	Info               json.RawMessage `json:"info"`
}

// XCSeriesInfo represents an Xtreme Codes get_series_info response. Seasons and
// Info are carried through verbatim; only the episode tree is rewritten by the
// proxy, since its IDs and source URLs have to point back at us.
type XCSeriesInfo struct {
	Seasons  json.RawMessage        `json:"seasons"`
	Info     json.RawMessage        `json:"info"`
	Episodes map[string][]XCEpisode `json:"episodes"`
}

// UnmarshalJSON decodes the episode tree from either the season-keyed object
// most panels emit or the flat array a handful of them return instead.
func (s *XCSeriesInfo) UnmarshalJSON(data []byte) error {
	var aux struct {
		Seasons  json.RawMessage `json:"seasons"`
		Info     json.RawMessage `json:"info"`
		Episodes json.RawMessage `json:"episodes"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	s.Seasons = aux.Seasons
	s.Info = aux.Info
	s.Episodes = make(map[string][]XCEpisode)

	if len(aux.Episodes) == 0 {
		return nil
	}

	var keyed map[string][]XCEpisode
	if err := json.Unmarshal(aux.Episodes, &keyed); err == nil {
		s.Episodes = keyed
		return nil
	}

	var flat []XCEpisode
	if err := json.Unmarshal(aux.Episodes, &flat); err != nil {
		return err
	}
	for _, ep := range flat {
		season := string(ep.Season)
		if season == "" {
			season = "1"
		}
		s.Episodes[season] = append(s.Episodes[season], ep)
	}
	return nil
}

// FetchXCSeriesInfo retrieves the season and episode tree for a single series
// from a source's Xtreme Codes API, honoring that source's rate limiter.
// Callers are responsible for caching the result.
//
// Parameters:
//   - httpClient: configured HTTP client with custom header support for source authentication
//   - cfg: application configuration containing debug settings and URL obfuscation preferences
//   - source: source configuration with URL, credentials, and connection parameters
//   - rateLimiter: rate limiter for controlling API request frequency to prevent server overload
//   - seriesID: the provider's own series identifier
//
// Returns:
//   - *XCSeriesInfo: the decoded series tree, nil on failure
//   - error: request, status, or decode failure
func FetchXCSeriesInfo(httpClient *client.HeaderSettingClient, cfg *config.Config, source *config.SourceConfig, rateLimiter ratelimit.Limiter, seriesID string) (*XCSeriesInfo, error) {
	// a single short API call is not a stream slot, so it neither checks nor holds
	// one; gating it emptied the episode tree whenever the source was busy, and
	// holding one could refuse a concurrent episode playback on the same source
	if rateLimiter != nil {
		rateLimiter.Take()
		logger.Debug("{parser/seriesinfo - FetchXCSeriesInfo} Applied rate limit for XC series info request: %s", source.Name)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	url := fmt.Sprintf("%s/player_api.php?username=%s&password=%s&action=get_series_info&series_id=%s",
		source.URL, source.Username, source.Password, seriesID)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		logger.Error("{parser/seriesinfo - FetchXCSeriesInfo} Failed to create XC series info request: %v", err)
		return nil, err
	}
	req.Header.Set("Connection", "keep-alive")

	resp, err := httpClient.DoWithHeaders(req, source.UserAgent, source.ReqOrigin, source.ReqReferrer)
	if err != nil {
		logger.Error("{parser/seriesinfo - FetchXCSeriesInfo} XC series info request failed: %v", err)
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logger.Error("{parser/seriesinfo - FetchXCSeriesInfo} XC API returned HTTP %d for series %s from: %s",
			resp.StatusCode, seriesID, utils.LogURL(cfg, source.URL))
		return nil, fmt.Errorf("XC API returned HTTP %d", resp.StatusCode)
	}

	var info XCSeriesInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		logger.Error("{parser/seriesinfo - FetchXCSeriesInfo} Failed to parse XC series info for %s: %v", seriesID, err)
		return nil, err
	}

	total := 0
	for _, eps := range info.Episodes {
		total += len(eps)
	}
	logger.Debug("{parser/seriesinfo - FetchXCSeriesInfo} Fetched series %s from %s: %d seasons, %d episodes",
		seriesID, source.Name, len(info.Episodes), total)

	return &info, nil
}
