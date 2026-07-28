// work/db/localsources.go
package db

import (
	"database/sql"
	"kptv-proxy/work/logger"
)

// LocalSource mirrors the kp_local_sources row for transport between the
// database and the rest of the application. MediaType uses the integer enum
// shared with kp_local_media (0=music, 1=movies, 2=shows, 3=images).
type LocalSource struct {
	ID          int64
	Name        string
	Path        string
	MediaType   int
	GroupPrefix string
	SortOrder   int
	Enabled     bool
	IncRegex    string
	ExcRegex    string
	LastScan    int64
	EntryCount  int
}

// GetAllLocalSources returns every local source row ordered by sort_order ascending.
func GetAllLocalSources() ([]LocalSource, error) {
	rows, err := Get().Query(`
		SELECT id, name, path, media_type, group_prefix, sort_order,
		       enabled, inc_regex, exc_regex, last_scan, entry_count
		FROM kp_local_sources
		ORDER BY sort_order ASC`)
	if err != nil {
		logger.Error("{db/localsources - GetAllLocalSources} query failed: %v", err)
		return nil, err
	}
	defer rows.Close()
	return scanLocalSources(rows)
}

// GetEnabledLocalSources returns only the enabled local source rows ordered by
// sort_order ascending. Used by the parser when building the stream set.
func GetEnabledLocalSources() ([]LocalSource, error) {
	rows, err := Get().Query(`
		SELECT id, name, path, media_type, group_prefix, sort_order,
		       enabled, inc_regex, exc_regex, last_scan, entry_count
		FROM kp_local_sources
		WHERE enabled = 1
		ORDER BY sort_order ASC`)
	if err != nil {
		logger.Error("{db/localsources - GetEnabledLocalSources} query failed: %v", err)
		return nil, err
	}
	defer rows.Close()
	return scanLocalSources(rows)
}

// GetLocalSource returns a single local source by its primary key.
// Returns sql.ErrNoRows if the ID does not exist.
func GetLocalSource(id int64) (LocalSource, error) {
	row := Get().QueryRow(`
		SELECT id, name, path, media_type, group_prefix, sort_order,
		       enabled, inc_regex, exc_regex, last_scan, entry_count
		FROM kp_local_sources WHERE id = ?`, id)

	var s LocalSource
	err := scanLocalSource(row, &s)
	if err != nil {
		logger.Error("{db/localsources - GetLocalSource} id=%d: %v", id, err)
	}
	return s, err
}

// InsertLocalSource inserts a new local source row and returns the assigned ID.
func InsertLocalSource(s LocalSource) (int64, error) {
	res, err := Get().Exec(`
		INSERT INTO kp_local_sources
			(name, path, media_type, group_prefix, sort_order,
			 enabled, inc_regex, exc_regex)
		VALUES (?,?,?,?,?,?,?,?)`,
		s.Name, s.Path, s.MediaType, s.GroupPrefix, s.SortOrder,
		s.Enabled, s.IncRegex, s.ExcRegex,
	)
	if err != nil {
		logger.Error("{db/localsources - InsertLocalSource} %v", err)
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateLocalSource replaces every user-editable column for the given local
// source ID. Scan bookkeeping columns are left untouched.
func UpdateLocalSource(s LocalSource) error {
	_, err := Get().Exec(`
		UPDATE kp_local_sources SET
			name=?, path=?, media_type=?, group_prefix=?, sort_order=?,
			enabled=?, inc_regex=?, exc_regex=?
		WHERE id=?`,
		s.Name, s.Path, s.MediaType, s.GroupPrefix, s.SortOrder,
		s.Enabled, s.IncRegex, s.ExcRegex, s.ID,
	)
	if err != nil {
		logger.Error("{db/localsources - UpdateLocalSource} id=%d: %v", s.ID, err)
	}
	return err
}

// DeleteLocalSource removes a local source row by ID. Cascades to
// kp_local_media via FK.
func DeleteLocalSource(id int64) error {
	_, err := Get().Exec(`DELETE FROM kp_local_sources WHERE id = ?`, id)
	if err != nil {
		logger.Error("{db/localsources - DeleteLocalSource} id=%d: %v", id, err)
	}
	return err
}

// TouchLocalSourceScan records the completion time and resulting entry count
// for a finished scan of the given local source.
func TouchLocalSourceScan(id int64, when int64, count int) error {
	_, err := Get().Exec(`
		UPDATE kp_local_sources SET last_scan=?, entry_count=? WHERE id=?`,
		when, count, id)
	if err != nil {
		logger.Error("{db/localsources - TouchLocalSourceScan} id=%d: %v", id, err)
	}
	return err
}

// scanLocalSources iterates a *sql.Rows result into a LocalSource slice.
func scanLocalSources(rows *sql.Rows) ([]LocalSource, error) {
	var sources []LocalSource
	for rows.Next() {
		var s LocalSource
		if err := rows.Scan(
			&s.ID, &s.Name, &s.Path, &s.MediaType, &s.GroupPrefix,
			&s.SortOrder, &s.Enabled, &s.IncRegex, &s.ExcRegex,
			&s.LastScan, &s.EntryCount,
		); err != nil {
			logger.Error("{db/localsources - scanLocalSources} scan failed: %v", err)
			return nil, err
		}
		sources = append(sources, s)
	}
	return sources, rows.Err()
}

// scanLocalSource scans a single *sql.Row into a LocalSource.
func scanLocalSource(row *sql.Row, s *LocalSource) error {
	return row.Scan(
		&s.ID, &s.Name, &s.Path, &s.MediaType, &s.GroupPrefix,
		&s.SortOrder, &s.Enabled, &s.IncRegex, &s.ExcRegex,
		&s.LastScan, &s.EntryCount,
	)
}
