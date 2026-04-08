// work/db/epgs.go
package db

import (
	"database/sql"
	"kptv-proxy/work/logger"
)

// EPG mirrors a kp_epgs row.
type EPG struct {
	ID        int64
	Name      string
	URL       string
	SortOrder int
}

// GetAllEPGs returns every EPG row ordered by sort_order ascending.
func GetAllEPGs() ([]EPG, error) {
	rows, err := Get().Query(`
		SELECT id, name, url, sort_order
		FROM kp_epgs
		ORDER BY sort_order ASC`)
	if err != nil {
		logger.Error("{db/epgs - GetAllEPGs} query failed: %v", err)
		return nil, err
	}
	defer rows.Close()
	return scanEPGs(rows)
}

// GetEPG returns a single EPG row by primary key.
// Returns sql.ErrNoRows if the ID does not exist.
func GetEPG(id int64) (EPG, error) {
	row := Get().QueryRow(`
		SELECT id, name, url, sort_order
		FROM kp_epgs WHERE id = ?`, id)

	var e EPG
	err := row.Scan(&e.ID, &e.Name, &e.URL, &e.SortOrder)
	if err != nil {
		logger.Error("{db/epgs - GetEPG} id=%d: %v", id, err)
	}
	return e, err
}

// InsertEPG inserts a new EPG row and returns the assigned ID.
func InsertEPG(e EPG) (int64, error) {
	res, err := Get().Exec(`
		INSERT INTO kp_epgs (name, url, sort_order)
		VALUES (?, ?, ?)`,
		e.Name, e.URL, e.SortOrder,
	)
	if err != nil {
		logger.Error("{db/epgs - InsertEPG} %v", err)
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateEPG replaces all mutable columns for the given EPG ID.
func UpdateEPG(e EPG) error {
	_, err := Get().Exec(`
		UPDATE kp_epgs SET name=?, url=?, sort_order=?
		WHERE id=?`,
		e.Name, e.URL, e.SortOrder, e.ID,
	)
	if err != nil {
		logger.Error("{db/epgs - UpdateEPG} id=%d: %v", e.ID, err)
	}
	return err
}

// DeleteEPG removes an EPG row by ID.
func DeleteEPG(id int64) error {
	_, err := Get().Exec(`DELETE FROM kp_epgs WHERE id = ?`, id)
	if err != nil {
		logger.Error("{db/epgs - DeleteEPG} id=%d: %v", id, err)
	}
	return err
}

// scanEPGs iterates a *sql.Rows result into an EPG slice.
func scanEPGs(rows *sql.Rows) ([]EPG, error) {
	var epgs []EPG
	for rows.Next() {
		var e EPG
		if err := rows.Scan(&e.ID, &e.Name, &e.URL, &e.SortOrder); err != nil {
			logger.Error("{db/epgs - scanEPGs} scan failed: %v", err)
			return nil, err
		}
		epgs = append(epgs, e)
	}
	return epgs, rows.Err()
}

// EPGExists returns true if any row with the given URL already exists.
// Useful for deduplication during import.
func EPGExists(url string) (bool, error) {
	var count int
	err := Get().QueryRow(
		`SELECT COUNT(*) FROM kp_epgs WHERE url = ?`, url,
	).Scan(&count)
	if err != nil {
		logger.Error("{db/epgs - EPGExists} %v", err)
		return false, err
	}
	return count > 0, nil
}
