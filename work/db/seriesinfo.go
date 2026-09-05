// work/db/seriesinfo.go
package db

import (
	"fmt"
	"kptv-proxy/work/logger"
	"strings"
	"time"
)

// SeriesEpisode maps a proxy episode ID to one provider's copy of that episode.
// The ID is minted from the series channel and the season/episode numbers rather
// than from any single provider, so every source carrying the episode resolves
// through the same ID and playback can fail over between them.
type SeriesEpisode struct {
	EpisodeID   int
	ChannelName string
	Season      int
	Episode     int
	SourceURL   string
	SeriesID    string
	UpstreamID  string
	Extension   string
}

// GetSeriesInfo returns the cached get_series_info payload for a source's series,
// along with whether it is still within the supplied TTL.
func GetSeriesInfo(sourceURL, seriesID string, ttl time.Duration) (string, bool) {
	row := GetReader().QueryRow(
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

// GetSeriesEpisode resolves a proxy episode ID to a single upstream origin,
// preferring the earliest recorded mapping.
func GetSeriesEpisode(episodeID int) (SeriesEpisode, bool) {
	row := GetReader().QueryRow(
		`SELECT episode_id, channel_name, season, episode, source_url, series_id, upstream_id, extension
		 FROM kp_series_episodes WHERE episode_id = ? ORDER BY id LIMIT 1`,
		episodeID,
	)

	var e SeriesEpisode
	if err := row.Scan(&e.EpisodeID, &e.ChannelName, &e.Season, &e.Episode, &e.SourceURL, &e.SeriesID, &e.UpstreamID, &e.Extension); err != nil {
		return SeriesEpisode{}, false
	}
	return e, true
}

// GetSeriesEpisodeSources returns every provider mapping recorded for a proxy
// episode ID, so playback can walk them in the channel's own stream order.
func GetSeriesEpisodeSources(episodeID int) []SeriesEpisode {
	rows, err := GetReader().Query(
		`SELECT episode_id, channel_name, season, episode, source_url, series_id, upstream_id, extension
		 FROM kp_series_episodes WHERE episode_id = ? ORDER BY id`,
		episodeID,
	)
	if err != nil {
		logger.Error("{db/seriesinfo - GetSeriesEpisodeSources} query episode=%d: %v", episodeID, err)
		return nil
	}
	defer rows.Close()

	var mappings []SeriesEpisode
	for rows.Next() {
		var e SeriesEpisode
		if err := rows.Scan(&e.EpisodeID, &e.ChannelName, &e.Season, &e.Episode, &e.SourceURL, &e.SeriesID, &e.UpstreamID, &e.Extension); err != nil {
			logger.Error("{db/seriesinfo - GetSeriesEpisodeSources} scan episode=%d: %v", episodeID, err)
			return mappings
		}
		mappings = append(mappings, e)
	}
	if err := rows.Err(); err != nil {
		logger.Error("{db/seriesinfo - GetSeriesEpisodeSources} rows episode=%d: %v", episodeID, err)
	}
	return mappings
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

	stmt, err := tx.Prepare(`INSERT INTO kp_series_episodes (episode_id, channel_name, season, episode, source_url, series_id, upstream_id, extension)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(episode_id, source_url) DO UPDATE SET channel_name = excluded.channel_name, season = excluded.season, episode = excluded.episode, series_id = excluded.series_id, upstream_id = excluded.upstream_id, extension = excluded.extension`)
	if err != nil {
		logger.Error("{db/seriesinfo - SetSeriesEpisodes} prepare source=%s series=%s: %v", sourceURL, seriesID, err)
		return err
	}
	defer stmt.Close()

	for _, e := range episodes {
		if e.EpisodeID == 0 || e.UpstreamID == "" {
			continue
		}
		if _, err := stmt.Exec(e.EpisodeID, e.ChannelName, e.Season, e.Episode, sourceURL, seriesID, e.UpstreamID, e.Extension); err != nil {
			logger.Error("{db/seriesinfo - SetSeriesEpisodes} insert episode=%d: %v", e.EpisodeID, err)
			return err
		}
	}

	return tx.Commit()
}

// PruneSeriesInfo removes cached series payloads and episode mappings belonging
// to sources that are no longer configured. Sources are replaced wholesale on
// every config save, so this reconciles by URL rather than by delete hook.
func PruneSeriesInfo(keepURLs []string) error {
	tx, err := Get().Begin()
	if err != nil {
		logger.Error("{db/seriesinfo - PruneSeriesInfo} begin: %v", err)
		return err
	}
	defer tx.Rollback()

	if len(keepURLs) == 0 {
		if _, err := tx.Exec(`DELETE FROM kp_series_info`); err != nil {
			logger.Error("{db/seriesinfo - PruneSeriesInfo} delete all info: %v", err)
			return err
		}
		if _, err := tx.Exec(`DELETE FROM kp_series_episodes`); err != nil {
			logger.Error("{db/seriesinfo - PruneSeriesInfo} delete all episodes: %v", err)
			return err
		}
		return tx.Commit()
	}

	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(keepURLs)), ",")
	args := make([]any, 0, len(keepURLs))
	for _, url := range keepURLs {
		args = append(args, url)
	}

	for _, table := range []string{"kp_series_info", "kp_series_episodes"} {
		query := fmt.Sprintf(`DELETE FROM %s WHERE source_url NOT IN (%s)`, table, placeholders)
		if _, err := tx.Exec(query, args...); err != nil {
			logger.Error("{db/seriesinfo - PruneSeriesInfo} prune %s: %v", table, err)
			return err
		}
	}

	return tx.Commit()
}
