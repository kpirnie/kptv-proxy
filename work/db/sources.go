// work/db/sources.go
package db

import (
	"database/sql"
	"kptv-proxy/work/logger"
)

// Source mirrors the kp_sources row for transport between the database
// and the rest of the application. Duration fields are stored as strings
// (e.g. "30s", "5s") matching the existing config file convention.
type Source struct {
	ID             int64
	Name           string
	URI            string
	Username       string
	Password       string
	SortOrder      int
	MaxCnx         int
	MaxStreamTo    string
	RetryDelay     string
	MaxRetries     int
	MaxFailures    int
	MinDataSize    int
	UserAgent      string
	ReqOrigin      string
	ReqReferer     string
	LiveIncRegex   string
	LiveExcRegex   string
	SeriesIncRegex string
	SeriesExcRegex string
	VODIncRegex    string
	VODExcRegex    string
}

// GetAllSources returns every source row ordered by sort_order ascending.
func GetAllSources() ([]Source, error) {
	rows, err := Get().Query(`
		SELECT id, name, uri, uname, pword, sort_order, max_cnx,
		       max_stream_to, retry_delay, max_retries, max_failures,
		       min_data_size, user_agent, req_origin, req_referer,
		       live_inc_regex, live_exc_regex, series_inc_regex,
		       series_exc_regex, vod_inc_regex, vod_exc_regex
		FROM kp_sources
		ORDER BY sort_order ASC`)
	if err != nil {
		logger.Error("{db/sources - GetAllSources} query failed: %v", err)
		return nil, err
	}
	defer rows.Close()
	return scanSources(rows)
}

// GetSource returns a single source by its primary key.
// Returns sql.ErrNoRows if the ID does not exist.
func GetSource(id int64) (Source, error) {
	row := Get().QueryRow(`
		SELECT id, name, uri, uname, pword, sort_order, max_cnx,
		       max_stream_to, retry_delay, max_retries, max_failures,
		       min_data_size, user_agent, req_origin, req_referer,
		       live_inc_regex, live_exc_regex, series_inc_regex,
		       series_exc_regex, vod_inc_regex, vod_exc_regex
		FROM kp_sources WHERE id = ?`, id)

	var s Source
	err := scanSource(row, &s)
	if err != nil {
		logger.Error("{db/sources - GetSource} id=%d: %v", id, err)
	}
	return s, err
}

// InsertSource inserts a new source row and returns the assigned ID.
func InsertSource(s Source) (int64, error) {
	res, err := Get().Exec(`
		INSERT INTO kp_sources
			(name, uri, uname, pword, sort_order, max_cnx, max_stream_to,
			 retry_delay, max_retries, max_failures, min_data_size, user_agent,
			 req_origin, req_referer, live_inc_regex, live_exc_regex,
			 series_inc_regex, series_exc_regex, vod_inc_regex, vod_exc_regex)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		s.Name, s.URI, s.Username, s.Password, s.SortOrder, s.MaxCnx,
		s.MaxStreamTo, s.RetryDelay, s.MaxRetries, s.MaxFailures,
		s.MinDataSize, s.UserAgent, s.ReqOrigin, s.ReqReferer,
		s.LiveIncRegex, s.LiveExcRegex, s.SeriesIncRegex, s.SeriesExcRegex,
		s.VODIncRegex, s.VODExcRegex,
	)
	if err != nil {
		logger.Error("{db/sources - InsertSource} %v", err)
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateSource replaces every mutable column for the given source ID.
func UpdateSource(s Source) error {
	_, err := Get().Exec(`
		UPDATE kp_sources SET
			name=?, uri=?, uname=?, pword=?, sort_order=?, max_cnx=?,
			max_stream_to=?, retry_delay=?, max_retries=?, max_failures=?,
			min_data_size=?, user_agent=?, req_origin=?, req_referer=?,
			live_inc_regex=?, live_exc_regex=?, series_inc_regex=?,
			series_exc_regex=?, vod_inc_regex=?, vod_exc_regex=?
		WHERE id=?`,
		s.Name, s.URI, s.Username, s.Password, s.SortOrder, s.MaxCnx,
		s.MaxStreamTo, s.RetryDelay, s.MaxRetries, s.MaxFailures,
		s.MinDataSize, s.UserAgent, s.ReqOrigin, s.ReqReferer,
		s.LiveIncRegex, s.LiveExcRegex, s.SeriesIncRegex, s.SeriesExcRegex,
		s.VODIncRegex, s.VODExcRegex, s.ID,
	)
	if err != nil {
		logger.Error("{db/sources - UpdateSource} id=%d: %v", s.ID, err)
	}
	return err
}

// DeleteSource removes a source row by ID. Cascades to kp_streams via FK.
func DeleteSource(id int64) error {
	_, err := Get().Exec(`DELETE FROM kp_sources WHERE id = ?`, id)
	if err != nil {
		logger.Error("{db/sources - DeleteSource} id=%d: %v", id, err)
	}
	return err
}

// scanSources iterates a *sql.Rows result into a Source slice.
func scanSources(rows *sql.Rows) ([]Source, error) {
	var sources []Source
	for rows.Next() {
		var s Source
		if err := rows.Scan(
			&s.ID, &s.Name, &s.URI, &s.Username, &s.Password,
			&s.SortOrder, &s.MaxCnx, &s.MaxStreamTo, &s.RetryDelay,
			&s.MaxRetries, &s.MaxFailures, &s.MinDataSize, &s.UserAgent,
			&s.ReqOrigin, &s.ReqReferer, &s.LiveIncRegex, &s.LiveExcRegex,
			&s.SeriesIncRegex, &s.SeriesExcRegex, &s.VODIncRegex, &s.VODExcRegex,
		); err != nil {
			logger.Error("{db/sources - scanSources} scan failed: %v", err)
			return nil, err
		}
		sources = append(sources, s)
	}
	return sources, rows.Err()
}

// scanSource scans a single *sql.Row into a Source.
func scanSource(row *sql.Row, s *Source) error {
	return row.Scan(
		&s.ID, &s.Name, &s.URI, &s.Username, &s.Password,
		&s.SortOrder, &s.MaxCnx, &s.MaxStreamTo, &s.RetryDelay,
		&s.MaxRetries, &s.MaxFailures, &s.MinDataSize, &s.UserAgent,
		&s.ReqOrigin, &s.ReqReferer, &s.LiveIncRegex, &s.LiveExcRegex,
		&s.SeriesIncRegex, &s.SeriesExcRegex, &s.VODIncRegex, &s.VODExcRegex,
	)
}
