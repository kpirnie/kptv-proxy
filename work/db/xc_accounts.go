// work/db/xc_accounts.go
package db

import (
	"database/sql"
	"kptv-proxy/work/logger"
)

// XCAccount mirrors a kp_xc_accounts row.
type XCAccount struct {
	ID           int64
	Name         string
	Username     string
	Password     string
	MaxCnx       int
	EnableLive   bool
	EnableSeries bool
	EnableVOD    bool
}

// GetAllXCAccounts returns every XC output account row.
func GetAllXCAccounts() ([]XCAccount, error) {
	rows, err := Get().Query(`
		SELECT id, name, uname, pword, max_cnx,
		       enable_live, enable_series, enable_vod
		FROM kp_xc_accounts
		ORDER BY id ASC`)
	if err != nil {
		logger.Error("{db/xc_accounts - GetAllXCAccounts} query failed: %v", err)
		return nil, err
	}
	defer rows.Close()
	return scanXCAccounts(rows)
}

// GetXCAccount returns a single XC account by primary key.
// Returns sql.ErrNoRows if the ID does not exist.
func GetXCAccount(id int64) (XCAccount, error) {
	row := Get().QueryRow(`
		SELECT id, name, uname, pword, max_cnx,
		       enable_live, enable_series, enable_vod
		FROM kp_xc_accounts WHERE id = ?`, id)

	var a XCAccount
	err := scanXCAccount(row, &a)
	if err != nil {
		logger.Error("{db/xc_accounts - GetXCAccount} id=%d: %v", id, err)
	}
	return a, err
}

// GetXCAccountByCredentials returns the XC account matching the given
// username and password pair. Returns sql.ErrNoRows if not found.
func GetXCAccountByCredentials(username, password string) (XCAccount, error) {
	row := Get().QueryRow(`
		SELECT id, name, uname, pword, max_cnx,
		       enable_live, enable_series, enable_vod
		FROM kp_xc_accounts
		WHERE uname = ? AND pword = ?`, username, password)

	var a XCAccount
	err := scanXCAccount(row, &a)
	if err != nil {
		logger.Error("{db/xc_accounts - GetXCAccountByCredentials} user=%s: %v", username, err)
	}
	return a, err
}

// InsertXCAccount inserts a new XC account row and returns the assigned ID.
func InsertXCAccount(a XCAccount) (int64, error) {
	res, err := Get().Exec(`
		INSERT INTO kp_xc_accounts
			(name, uname, pword, max_cnx, enable_live, enable_series, enable_vod)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		a.Name, a.Username, a.Password, a.MaxCnx,
		boolToInt(a.EnableLive), boolToInt(a.EnableSeries), boolToInt(a.EnableVOD),
	)
	if err != nil {
		logger.Error("{db/xc_accounts - InsertXCAccount} %v", err)
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateXCAccount replaces all mutable columns for the given XC account ID.
func UpdateXCAccount(a XCAccount) error {
	_, err := Get().Exec(`
		UPDATE kp_xc_accounts SET
			name=?, uname=?, pword=?, max_cnx=?,
			enable_live=?, enable_series=?, enable_vod=?
		WHERE id=?`,
		a.Name, a.Username, a.Password, a.MaxCnx,
		boolToInt(a.EnableLive), boolToInt(a.EnableSeries), boolToInt(a.EnableVOD),
		a.ID,
	)
	if err != nil {
		logger.Error("{db/xc_accounts - UpdateXCAccount} id=%d: %v", a.ID, err)
	}
	return err
}

// DeleteXCAccount removes an XC account row by ID.
func DeleteXCAccount(id int64) error {
	_, err := Get().Exec(`DELETE FROM kp_xc_accounts WHERE id = ?`, id)
	if err != nil {
		logger.Error("{db/xc_accounts - DeleteXCAccount} id=%d: %v", id, err)
	}
	return err
}

// scanXCAccounts iterates a *sql.Rows result into an XCAccount slice.
func scanXCAccounts(rows *sql.Rows) ([]XCAccount, error) {
	var accounts []XCAccount
	for rows.Next() {
		var a XCAccount
		var live, series, vod int
		if err := rows.Scan(
			&a.ID, &a.Name, &a.Username, &a.Password, &a.MaxCnx,
			&live, &series, &vod,
		); err != nil {
			logger.Error("{db/xc_accounts - scanXCAccounts} scan failed: %v", err)
			return nil, err
		}
		a.EnableLive = live == 1
		a.EnableSeries = series == 1
		a.EnableVOD = vod == 1
		accounts = append(accounts, a)
	}
	return accounts, rows.Err()
}

// scanXCAccount scans a single *sql.Row into an XCAccount.
func scanXCAccount(row *sql.Row, a *XCAccount) error {
	var live, series, vod int
	if err := row.Scan(
		&a.ID, &a.Name, &a.Username, &a.Password, &a.MaxCnx,
		&live, &series, &vod,
	); err != nil {
		return err
	}
	a.EnableLive = live == 1
	a.EnableSeries = series == 1
	a.EnableVOD = vod == 1
	return nil
}

// boolToInt converts a bool to SQLite's integer representation (0 or 1).
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
