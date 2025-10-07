package database

import (
	"database/sql"
	"fmt"
	"kptv-proxy/work/config"
	"time"
)

// LoadSources loads all sources from the database
func (db *DB) LoadSources() ([]config.SourceConfig, error) {
	query := `
		SELECT id, name, url, username, password, order_priority, max_connections,
		       max_stream_timeout, retry_delay, max_retries, max_failures_before_block,
		       min_data_size, user_agent, req_origin, req_referrer,
		       live_include_regex, live_exclude_regex, series_include_regex, series_exclude_regex,
		       vod_include_regex, vod_exclude_regex
		FROM sources
		WHERE active = 1
		ORDER BY order_priority
	`

	rows, err := db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to load sources: %w", err)
	}
	defer rows.Close()

	var sources []config.SourceConfig
	for rows.Next() {
		var id int
		var src config.SourceConfig
		var maxStreamTimeout, retryDelay string

		err := rows.Scan(
			&id, &src.Name, &src.URL, &src.Username, &src.Password,
			&src.Order, &src.MaxConnections, &maxStreamTimeout, &retryDelay,
			&src.MaxRetries, &src.MaxFailuresBeforeBlock, &src.MinDataSize,
			&src.UserAgent, &src.ReqOrigin, &src.ReqReferrer,
			&src.LiveIncludeRegex, &src.LiveExcludeRegex,
			&src.SeriesIncludeRegex, &src.SeriesExcludeRegex,
			&src.VODIncludeRegex, &src.VODExcludeRegex,
		)
		if err != nil {
			continue
		}

		// Parse durations
		src.MaxStreamTimeout, _ = time.ParseDuration(maxStreamTimeout)
		src.RetryDelay, _ = time.ParseDuration(retryDelay)

		sources = append(sources, src)
	}

	return sources, nil
}

// SaveSource saves or updates a source
func (db *DB) SaveSource(src *config.SourceConfig) (int64, error) {
	query := `
		INSERT INTO sources (
			name, url, username, password, order_priority, max_connections,
			max_stream_timeout, retry_delay, max_retries, max_failures_before_block,
			min_data_size, user_agent, req_origin, req_referrer,
			live_include_regex, live_exclude_regex, series_include_regex, series_exclude_regex,
			vod_include_regex, vod_exclude_regex, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(url) DO UPDATE SET
			name = excluded.name,
			username = excluded.username,
			password = excluded.password,
			order_priority = excluded.order_priority,
			max_connections = excluded.max_connections,
			max_stream_timeout = excluded.max_stream_timeout,
			retry_delay = excluded.retry_delay,
			max_retries = excluded.max_retries,
			max_failures_before_block = excluded.max_failures_before_block,
			min_data_size = excluded.min_data_size,
			user_agent = excluded.user_agent,
			req_origin = excluded.req_origin,
			req_referrer = excluded.req_referrer,
			live_include_regex = excluded.live_include_regex,
			live_exclude_regex = excluded.live_exclude_regex,
			series_include_regex = excluded.series_include_regex,
			series_exclude_regex = excluded.series_exclude_regex,
			vod_include_regex = excluded.vod_include_regex,
			vod_exclude_regex = excluded.vod_exclude_regex,
			updated_at = CURRENT_TIMESTAMP
	`

	result, err := db.Exec(query,
		src.Name, src.URL, src.Username, src.Password, src.Order, src.MaxConnections,
		src.MaxStreamTimeout.String(), src.RetryDelay.String(), src.MaxRetries,
		src.MaxFailuresBeforeBlock, src.MinDataSize,
		src.UserAgent, src.ReqOrigin, src.ReqReferrer,
		src.LiveIncludeRegex, src.LiveExcludeRegex,
		src.SeriesIncludeRegex, src.SeriesExcludeRegex,
		src.VODIncludeRegex, src.VODExcludeRegex,
	)

	if err != nil {
		return 0, fmt.Errorf("failed to save source: %w", err)
	}

	return result.LastInsertId()
}

// DeleteSource marks a source as inactive
func (db *DB) DeleteSource(sourceID int64) error {
	_, err := db.Exec("UPDATE sources SET active = 0, updated_at = CURRENT_TIMESTAMP WHERE id = ?", sourceID)
	return err
}

// GetSourceByURL retrieves a source by its URL
func (db *DB) GetSourceByURL(url string) (*config.SourceConfig, error) {
	query := `
		SELECT id, name, url, username, password, order_priority, max_connections,
		       max_stream_timeout, retry_delay, max_retries, max_failures_before_block,
		       min_data_size, user_agent, req_origin, req_referrer,
		       live_include_regex, live_exclude_regex, series_include_regex, series_exclude_regex,
		       vod_include_regex, vod_exclude_regex
		FROM sources
		WHERE url = ? AND active = 1
	`

	var id int
	var src config.SourceConfig
	var maxStreamTimeout, retryDelay string

	err := db.QueryRow(query, url).Scan(
		&id, &src.Name, &src.URL, &src.Username, &src.Password,
		&src.Order, &src.MaxConnections, &maxStreamTimeout, &retryDelay,
		&src.MaxRetries, &src.MaxFailuresBeforeBlock, &src.MinDataSize,
		&src.UserAgent, &src.ReqOrigin, &src.ReqReferrer,
		&src.LiveIncludeRegex, &src.LiveExcludeRegex,
		&src.SeriesIncludeRegex, &src.SeriesExcludeRegex,
		&src.VODIncludeRegex, &src.VODExcludeRegex,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get source: %w", err)
	}

	src.MaxStreamTimeout, _ = time.ParseDuration(maxStreamTimeout)
	src.RetryDelay, _ = time.ParseDuration(retryDelay)

	return &src, nil
}
