package schedulesdirect

import (
	"context"
	"fmt"
	"kptv-proxy/work/config"
	"kptv-proxy/work/constants"
	"kptv-proxy/work/logger"
	"time"
)

const (
	batchSize = 1000
)

// AccountData holds all fetched data for a single SD account,
// ready for XMLTV conversion.
type AccountData struct {
	AccountName string
	Stations    []SDStation
	// maps stationID -> channel number from the lineup map
	ChannelMap map[string]string
	// maps stationID -> []SDAiring for the 7-day window
	Schedule map[string][]SDAiring
	// maps programID -> SDProgram full detail
	Programs map[string]SDProgram
}

// FetchAccount runs the full SD data pipeline for a single account and
// returns all data needed for XMLTV generation.
func FetchAccount(ctx context.Context, account config.SDAccount) (*AccountData, error) {
	logger.Debug("{schedulesdirect/fetch - FetchAccount} Starting fetch for account: %s", account.Name)

	client, err := newClient(account.Username, account.Password)
	if err != nil {
		return nil, fmt.Errorf("auth failed for account %s: %w", account.Name, err)
	}

	// Step 1 — status check
	if err := checkStatus(ctx, client, account.Name); err != nil {
		return nil, err
	}

	// Step 2 — get subscribed lineups, filtered by selectedLineups if set
	lineups, err := fetchLineups(ctx, client, account)
	if err != nil {
		return nil, fmt.Errorf("fetch lineups for %s: %w", account.Name, err)
	}
	logger.Debug("{schedulesdirect/fetch - FetchAccount} Account %s: found %d lineup(s) to process", account.Name, len(lineups))

	// Step 3 — get stations from each lineup, deduplicate within this account
	stations, channelMap, err := fetchStations(ctx, client, lineups)
	if err != nil {
		return nil, fmt.Errorf("fetch stations for %s: %w", account.Name, err)
	}
	logger.Debug("{schedulesdirect/fetch - FetchAccount} Account %s: %d unique stations", account.Name, len(stations))

	// Step 4 — fetch configured schedules for all stations
	days := account.DaysToFetch
	if days <= 0 {
		days = constants.Internal.SDDefaultDaysToFetch
	}
	schedule, err := fetchSchedules(ctx, client, stations, days)

	// Step 5 — collect unique program IDs and fetch full detail
	programs, err := fetchPrograms(ctx, client, schedule)
	if err != nil {
		return nil, fmt.Errorf("fetch programs for %s: %w", account.Name, err)
	}

	logger.Debug("{schedulesdirect/fetch - FetchAccount} Account %s: fetch complete — %d stations, %d programs",
		account.Name, len(stations), len(programs))

	return &AccountData{
		AccountName: account.Name,
		Stations:    stations,
		ChannelMap:  channelMap,
		Schedule:    schedule,
		Programs:    programs,
	}, nil
}

// checkStatus verifies the account is active and the SD system is operational.
func checkStatus(ctx context.Context, client *sdClient, accountName string) error {
	var status SDStatus
	if err := client.get(ctx, "/status", &status); err != nil {
		return fmt.Errorf("status check failed: %w", err)
	}

	// Check system status
	for _, sys := range status.SystemStatus {
		if sys.Status != "Online" {
			logger.Warn("{schedulesdirect/fetch - checkStatus} SD system status for account %s: %s — %s",
				accountName, sys.Status, sys.Message)
		}
	}

	// Check account status
	if status.Code != 0 {
		return fmt.Errorf("SD account %s returned error code %d: %s",
			accountName, status.Code, status.Message)
	}

	if status.Account.Status != "" && status.Account.Status != "Active" {
		return fmt.Errorf("SD account %s is not active (status: %s, expires: %s)",
			accountName, status.Account.Status, status.Account.Expires)
	}

	logger.Debug("{schedulesdirect/fetch - checkStatus} Account %s status OK (expires: %s)",
		accountName, status.Account.Expires)
	return nil
}

// fetchLineups retrieves the lineups subscribed to the account,
// filtering against selectedLineups if that list is non-empty.
func fetchLineups(ctx context.Context, client *sdClient, account config.SDAccount) ([]SDLineup, error) {
	var response struct {
		Lineups []SDLineup `json:"lineups"`
	}
	if err := client.get(ctx, "/lineups", &response); err != nil {
		return nil, err
	}

	if len(account.SelectedLineups) == 0 {
		logger.Debug("{schedulesdirect/fetch - fetchLineups} No lineup filter set, using all %d lineups", len(response.Lineups))
		return response.Lineups, nil
	}

	// Build a set for O(1) lookup
	selected := make(map[string]bool, len(account.SelectedLineups))
	for _, id := range account.SelectedLineups {
		selected[id] = true
	}

	var filtered []SDLineup
	for _, l := range response.Lineups {
		if selected[l.LineupID] {
			filtered = append(filtered, l)
		}
	}

	logger.Debug("{schedulesdirect/fetch - fetchLineups} Filtered to %d of %d lineups", len(filtered), len(response.Lineups))
	return filtered, nil
}

// fetchStations retrieves stations for each lineup and deduplicates by stationID
// within this account. Returns the station list and a stationID->channel map.
func fetchStations(ctx context.Context, client *sdClient, lineups []SDLineup) ([]SDStation, map[string]string, error) {
	stationMap := make(map[string]SDStation)
	channelMap := make(map[string]string)

	for _, lineup := range lineups {
		var lineupMap SDLineupMap
		path := fmt.Sprintf("/lineups/%s", lineup.LineupID)
		if err := client.get(ctx, path, &lineupMap); err != nil {
			logger.Warn("{schedulesdirect/fetch - fetchStations} Could not fetch lineup %s: %v", lineup.LineupID, err)
			continue
		}

		// Build channel number map from the lineup map entries
		for _, entry := range lineupMap.Map {
			if entry.Channel != "" {
				channelMap[entry.StationID] = entry.Channel
			}
		}

		// Deduplicate stations by stationID
		for _, station := range lineupMap.Stations {
			if _, exists := stationMap[station.StationID]; !exists {
				stationMap[station.StationID] = station
			}
		}

		logger.Debug("{schedulesdirect/fetch - fetchStations} Lineup %s: %d stations",
			lineup.LineupID, len(lineupMap.Stations))
	}

	stations := make([]SDStation, 0, len(stationMap))
	for _, s := range stationMap {
		stations = append(stations, s)
	}

	return stations, channelMap, nil
}

// fetchSchedules retrieves 7-day schedule data for all stations in batches of 1000.
func fetchSchedules(ctx context.Context, client *sdClient, stations []SDStation, daysToFetch int) (map[string][]SDAiring, error) {
	// Build date strings for the next 7 days
	dates := make([]string, daysToFetch)
	now := time.Now()
	for i := 0; i < daysToFetch; i++ {
		dates[i] = now.AddDate(0, 0, i).Format("2006-01-02")
	}

	// Build the full request list — one entry per station
	requests := make([]SDScheduleRequest, 0, len(stations))
	for _, s := range stations {
		requests = append(requests, SDScheduleRequest{
			StationID: s.StationID,
			Date:      dates,
		})
	}

	schedule := make(map[string][]SDAiring)

	// Process in batches of 1000
	for start := 0; start < len(requests); start += batchSize {
		end := start + batchSize
		if end > len(requests) {
			end = len(requests)
		}
		batch := requests[start:end]

		logger.Debug("{schedulesdirect/fetch - fetchSchedules} Fetching schedule batch %d-%d of %d",
			start+1, end, len(requests))

		var responses []SDScheduleResponse
		if err := client.post(ctx, "/schedules", batch, &responses); err != nil {
			return nil, fmt.Errorf("schedule batch %d-%d failed: %w", start, end, err)
		}

		for _, r := range responses {
			if r.Code != 0 {
				logger.Warn("{schedulesdirect/fetch - fetchSchedules} Station %s returned code %d, skipping",
					r.StationID, r.Code)
				continue
			}
			schedule[r.StationID] = append(schedule[r.StationID], r.Programs...)
		}
	}

	totalAirings := 0
	for _, airings := range schedule {
		totalAirings += len(airings)
	}
	logger.Debug("{schedulesdirect/fetch - fetchSchedules} Fetched %d total airings across %d stations",
		totalAirings, len(schedule))

	return schedule, nil
}

// fetchPrograms collects all unique programIDs from the schedule and
// fetches full program detail in batches of 1000.
func fetchPrograms(ctx context.Context, client *sdClient, schedule map[string][]SDAiring) (map[string]SDProgram, error) {
	// Collect unique program IDs
	seen := make(map[string]bool)
	var programIDs []string
	for _, airings := range schedule {
		for _, a := range airings {
			if !seen[a.ProgramID] {
				seen[a.ProgramID] = true
				programIDs = append(programIDs, a.ProgramID)
			}
		}
	}

	logger.Debug("{schedulesdirect/fetch - fetchPrograms} Fetching detail for %d unique programs", len(programIDs))

	programs := make(map[string]SDProgram, len(programIDs))

	// Process in batches of 1000
	for start := 0; start < len(programIDs); start += batchSize {
		end := start + batchSize
		if end > len(programIDs) {
			end = len(programIDs)
		}
		batch := programIDs[start:end]

		logger.Debug("{schedulesdirect/fetch - fetchPrograms} Fetching program batch %d-%d of %d",
			start+1, end, len(programIDs))

		var results []SDProgram
		if err := client.post(ctx, "/programs", batch, &results); err != nil {
			return nil, fmt.Errorf("program batch %d-%d failed: %w", start, end, err)
		}

		for _, p := range results {
			programs[p.ProgramID] = p
		}
	}

	return programs, nil
}

// DiscoverLineups authenticates and returns the available lineups for an account
// without saving anything. Used by the admin UI discovery flow.
func DiscoverLineups(username, password string) ([]SDLineup, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := newClient(username, password)
	if err != nil {
		return nil, fmt.Errorf("authentication failed: %w", err)
	}

	var response struct {
		Lineups []SDLineup `json:"lineups"`
	}
	if err := client.get(ctx, "/lineups", &response); err != nil {
		return nil, fmt.Errorf("could not retrieve lineups: %w", err)
	}

	logger.Debug("{schedulesdirect/fetch - DiscoverLineups} Found %d lineups for %s", len(response.Lineups), username)
	return response.Lineups, nil
}
