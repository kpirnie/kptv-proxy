// work/db/sd_accounts.go
package db

import (
	"database/sql"
	"kptv-proxy/work/logger"
)

// SDAccount mirrors a kp_sd_accounts row.
type SDAccount struct {
	ID          int64
	Name        string
	Username    string
	Password    string
	Enabled     bool
	DaysToFetch int
	// Lineups is populated by GetSDAccountWithLineups — not a column itself.
	Lineups []string
}

// GetAllSDAccounts returns every SD account row without lineup data.
func GetAllSDAccounts() ([]SDAccount, error) {
	rows, err := Get().Query(`
		SELECT id, name, uname, pword, enabled, days_to_fetch
		FROM kp_sd_accounts
		ORDER BY id ASC`)
	if err != nil {
		logger.Error("{db/sd_accounts - GetAllSDAccounts} query failed: %v", err)
		return nil, err
	}
	defer rows.Close()
	return scanSDAccounts(rows)
}

// GetSDAccountWithLineups returns a single SD account including its
// selected lineup IDs. Returns sql.ErrNoRows if the ID does not exist.
func GetSDAccountWithLineups(id int64) (SDAccount, error) {
	row := Get().QueryRow(`
		SELECT id, name, uname, pword, enabled, days_to_fetch
		FROM kp_sd_accounts WHERE id = ?`, id)

	var a SDAccount
	var enabled int
	if err := row.Scan(
		&a.ID, &a.Name, &a.Username, &a.Password, &enabled, &a.DaysToFetch,
	); err != nil {
		logger.Error("{db/sd_accounts - GetSDAccountWithLineups} id=%d: %v", id, err)
		return a, err
	}
	a.Enabled = enabled == 1

	lineups, err := GetSDLineups(a.ID)
	if err != nil {
		return a, err
	}
	a.Lineups = lineups
	return a, nil
}

// GetAllSDAccountsWithLineups returns every SD account including lineup data.
func GetAllSDAccountsWithLineups() ([]SDAccount, error) {
	accounts, err := GetAllSDAccounts()
	if err != nil {
		return nil, err
	}
	for i := range accounts {
		accounts[i].Lineups, err = GetSDLineups(accounts[i].ID)
		if err != nil {
			return nil, err
		}
	}
	return accounts, nil
}

// InsertSDAccount inserts a new SD account row and its lineup IDs,
// returning the assigned account ID.
func InsertSDAccount(a SDAccount) (int64, error) {
	res, err := Get().Exec(`
		INSERT INTO kp_sd_accounts (name, uname, pword, enabled, days_to_fetch)
		VALUES (?, ?, ?, ?, ?)`,
		a.Name, a.Username, a.Password, boolToInt(a.Enabled), a.DaysToFetch,
	)
	if err != nil {
		logger.Error("{db/sd_accounts - InsertSDAccount} %v", err)
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if err := replaceSDLineups(id, a.Lineups); err != nil {
		return id, err
	}
	return id, nil
}

// UpdateSDAccount replaces all mutable columns for the given SD account ID
// and synchronises its lineup rows.
func UpdateSDAccount(a SDAccount) error {
	_, err := Get().Exec(`
		UPDATE kp_sd_accounts SET
			name=?, uname=?, pword=?, enabled=?, days_to_fetch=?
		WHERE id=?`,
		a.Name, a.Username, a.Password, boolToInt(a.Enabled), a.DaysToFetch, a.ID,
	)
	if err != nil {
		logger.Error("{db/sd_accounts - UpdateSDAccount} id=%d: %v", a.ID, err)
		return err
	}
	return replaceSDLineups(a.ID, a.Lineups)
}

// DeleteSDAccount removes an SD account and its lineups (via FK cascade).
func DeleteSDAccount(id int64) error {
	_, err := Get().Exec(`DELETE FROM kp_sd_accounts WHERE id = ?`, id)
	if err != nil {
		logger.Error("{db/sd_accounts - DeleteSDAccount} id=%d: %v", id, err)
	}
	return err
}

// GetSDLineups returns the lineup_id strings associated with an SD account.
func GetSDLineups(accountID int64) ([]string, error) {
	rows, err := Get().Query(
		`SELECT lineup_id FROM kp_sd_lineups WHERE sd_account_id = ? ORDER BY id ASC`,
		accountID,
	)
	if err != nil {
		logger.Error("{db/sd_accounts - GetSDLineups} account_id=%d: %v", accountID, err)
		return nil, err
	}
	defer rows.Close()

	var lineups []string
	for rows.Next() {
		var l string
		if err := rows.Scan(&l); err != nil {
			logger.Error("{db/sd_accounts - GetSDLineups} scan failed: %v", err)
			return nil, err
		}
		lineups = append(lineups, l)
	}
	return lineups, rows.Err()
}

// replaceSDLineups deletes all existing lineup rows for an account and
// inserts the provided set. Called inside Insert and Update to keep
// lineups in sync without manual diff logic.
func replaceSDLineups(accountID int64, lineups []string) error {
	_, err := Get().Exec(
		`DELETE FROM kp_sd_lineups WHERE sd_account_id = ?`, accountID,
	)
	if err != nil {
		logger.Error("{db/sd_accounts - replaceSDLineups} delete failed account_id=%d: %v", accountID, err)
		return err
	}
	for _, l := range lineups {
		if _, err := Get().Exec(
			`INSERT INTO kp_sd_lineups (sd_account_id, lineup_id) VALUES (?, ?)`,
			accountID, l,
		); err != nil {
			logger.Error("{db/sd_accounts - replaceSDLineups} insert failed account_id=%d lineup=%s: %v", accountID, l, err)
			return err
		}
	}
	return nil
}

// scanSDAccounts iterates a *sql.Rows result into an SDAccount slice.
// Lineup data is NOT populated here — use GetAllSDAccountsWithLineups for that.
func scanSDAccounts(rows *sql.Rows) ([]SDAccount, error) {
	var accounts []SDAccount
	for rows.Next() {
		var a SDAccount
		var enabled int
		if err := rows.Scan(
			&a.ID, &a.Name, &a.Username, &a.Password, &enabled, &a.DaysToFetch,
		); err != nil {
			logger.Error("{db/sd_accounts - scanSDAccounts} scan failed: %v", err)
			return nil, err
		}
		a.Enabled = enabled == 1
		accounts = append(accounts, a)
	}
	return accounts, rows.Err()
}

// SDAccountExists returns true if any row with the given username already exists.
func SDAccountExists(username string) (bool, error) {
	var count int
	err := Get().QueryRow(
		`SELECT COUNT(*) FROM kp_sd_accounts WHERE uname = ?`, username,
	).Scan(&count)
	if err != nil {
		logger.Error("{db/sd_accounts - SDAccountExists} %v", err)
		return false, err
	}
	return count > 0, nil
}

// helper — reused from xc_accounts.go via same package, no redeclaration needed.
// boolToInt is declared in xc_accounts.go.

// intToBool is a local convenience used only within this file's scan helpers.
func intToBool(i int) bool { return i == 1 }

// Ensure sql import is used (scanSDAccounts uses sql.Rows indirectly via rows.Err).
var _ = (*sql.Rows)(nil)
