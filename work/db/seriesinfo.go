// work/db/seriesinfo.go
package db

import (
	"kptv-proxy/work/logger"
	"time"
)

// SeriesEpisode maps a proxy episode ID back to the upstream series episode it
// was minted from, so playback can be resolved long after the get_series_info
// call that created it.
type SeriesEpisode struct {
	EpisodeID  int
	SourceURL  string
	SeriesID   string
	UpstreamID string
	Extension  string
}

// GetSeriesInfo returns the cached get_series_info payload for a source's series,
// along with whether it is still within the supplied TTL.
func GetSeriesInfo(sourceURL, seriesID string, ttl time.Duration) (string, bool) {
	row := Get().QueryRow(
		`SELECT payload, fetched_at FROM kp_series_info WHERE source_url = ? AND series_id = ?`,
		sourceURL, seriesID,
	)

	var payload string
	var fetchedAt int64
	if err := row.Scan(&payload, &fetchedAt); err != nil {
		return "", false
	}
	if payload == "" {
		return "", false
	}
	if ttl > 0 && time.Since(time.Unix(fetchedAt, 0)) > ttl {
		return payload, false
	}
	return payload, true
}

// SetSeriesInfo stores the rendered get_series_info payload for a source's series,
// stamping it with the current time for TTL evaluation on later reads.
func SetSeriesInfo(sourceURL, seriesID, payload string) error {
	_, err := Get().Exec(
		`INSERT INTO kp_series_info (source_url, series_id, payload, fetched_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(source_url, series_id) DO UPDATE SET payload = excluded.payload, fetched_at = excluded.fetched_at`,
		sourceURL, seriesID, payload, time.Now().Unix(),
	)
	if err != nil {
		logger.Error("{db/seriesinfo - SetSeriesInfo} source=%s series=%s: %v", sourceURL, seriesID, err)
	}
	return err
}

// GetSeriesEpisode resolves a proxy episode ID to its upstream origin.
func GetSeriesEpisode(episodeID int) (SeriesEpisode, bool) {
	row := Get().QueryRow(
		`SELECT episode_id, source_url, series_id, upstream_id, extension
		 FROM kp_series_episodes WHERE episode_id = ?`,
		episodeID,
	)

	var e SeriesEpisode
	if err := row.Scan(&e.EpisodeID, &e.SourceURL, &e.SeriesID, &e.UpstreamID, &e.Extension); err != nil {
		return SeriesEpisode{}, false
	}
	return e, true
}

// SetSeriesEpisodes replaces the stored episode mappings for a source's series
// with the supplied set, so episodes the provider has dropped stop resolving
// rather than accumulating as stale rows.
func SetSeriesEpisodes(sourceURL, seriesID string, episodes []SeriesEpisode) error {
	tx, err := Get().Begin()
	if err != nil {
		logger.Error("{db/seriesinfo - SetSeriesEpisodes} begin source=%s series=%s: %v", sourceURL, seriesID, err)
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM kp_series_episodes WHERE source_url = ? AND series_id = ?`, sourceURL, seriesID); err != nil {
		logger.Error("{db/seriesinfo - SetSeriesEpisodes} delete source=%s series=%s: %v", sourceURL, seriesID, err)
		return err
	}

	stmt, err := tx.Prepare(`INSERT INTO kp_series_episodes (episode_id, source_url, series_id, upstream_id, extension)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(episode_id) DO UPDATE SET source_url = excluded.source_url, series_id = excluded.series_id, upstream_id = excluded.upstream_id, extension = excluded.extension`)
	if err != nil {
		logger.Error("{db/seriesinfo - SetSeriesEpisodes} prepare source=%s series=%s: %v", sourceURL, seriesID, err)
		return err
	}
	defer stmt.Close()

	for _, e := range episodes {
		if e.EpisodeID == 0 || e.UpstreamID == "" {
			continue
		}
		if _, err := stmt.Exec(e.EpisodeID, sourceURL, seriesID, e.UpstreamID, e.Extension); err != nil {
			logger.Error("{db/seriesinfo - SetSeriesEpisodes} insert episode=%d: %v", e.EpisodeID, err)
			return err
		}
	}

	return tx.Commit()
}

// DeleteSeriesInfoForSource clears every cached series payload and episode
// mapping belonging to a source, for use when that source is removed or edited.
func DeleteSeriesInfoForSource(sourceURL string) error {
	tx, err := Get().Begin()
	if err != nil {
		logger.Error("{db/seriesinfo - DeleteSeriesInfoForSource} begin source=%s: %v", sourceURL, err)
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM kp_series_info WHERE source_url = ?`, sourceURL); err != nil {
		logger.Error("{db/seriesinfo - DeleteSeriesInfoForSource} delete info source=%s: %v", sourceURL, err)
		return err
	}
	if _, err := tx.Exec(`DELETE FROM kp_series_episodes WHERE source_url = ?`, sourceURL); err != nil {
		logger.Error("{db/seriesinfo - DeleteSeriesInfoForSource} delete episodes source=%s: %v", sourceURL, err)
		return err
	}

	return tx.Commit()
}
