package users

import (
	"database/sql"
	"fmt"
	"kptv-proxy/work/db"
	"kptv-proxy/work/logger"
	"time"
)

// User mirrors a kp_users row.
type User struct {
	ID           int64
	Name         string
	Email        string
	Username     string
	PasswordHash string
	CreatedAt    int64
	LastLogin    int64
}

// APIToken mirrors a kp_api_tokens row.
type APIToken struct {
	ID          int64
	Name        string
	TokenHash   string
	Permissions int
}

// UserCount returns the number of users in the database.
func UserCount() (int, error) {
	var count int
	err := db.Get().QueryRow(`SELECT COUNT(*) FROM kp_users`).Scan(&count)
	if err != nil {
		logger.Error("{users/db - UserCount} %v", err)
		return 0, err
	}
	return count, nil
}

// CreateUser inserts a new user and returns the assigned ID.
func CreateUser(name, email, username, passwordHash string) (int64, error) {
	res, err := db.Get().Exec(`
		INSERT INTO kp_users (name, email, username, password_hash, created_at, last_login)
		VALUES (?, ?, ?, ?, ?, 0)`,
		name, email, username, passwordHash, time.Now().Unix(),
	)
	if err != nil {
		logger.Error("{users/db - CreateUser} %v", err)
		return 0, err
	}
	return res.LastInsertId()
}

// GetUserByUsername retrieves a user by username.
func GetUserByUsername(username string) (User, error) {
	return scanUser(db.Get().QueryRow(`
		SELECT id, name, email, username, password_hash, created_at, last_login
		FROM kp_users WHERE username = ?`, username))
}

// GetUserByEmail retrieves a user by email.
func GetUserByEmail(email string) (User, error) {
	return scanUser(db.Get().QueryRow(`
		SELECT id, name, email, username, password_hash, created_at, last_login
		FROM kp_users WHERE email = ?`, email))
}

// UpdateLastLogin updates the last_login timestamp for a user.
func UpdateLastLogin(id int64) error {
	_, err := db.Get().Exec(`UPDATE kp_users SET last_login = ? WHERE id = ?`,
		time.Now().Unix(), id)
	if err != nil {
		logger.Error("{users/db - UpdateLastLogin} id=%d: %v", id, err)
	}
	return err
}

// UpdatePassword updates the password hash for a user.
func UpdatePassword(id int64, passwordHash string) error {
	_, err := db.Get().Exec(`UPDATE kp_users SET password_hash = ? WHERE id = ?`,
		passwordHash, id)
	if err != nil {
		logger.Error("{users/db - UpdatePassword} id=%d: %v", id, err)
	}
	return err
}

// CreateToken inserts a new API token and returns the assigned ID.
func CreateToken(name, tokenHash string, permissions int) (int64, error) {
	res, err := db.Get().Exec(`
		INSERT INTO kp_api_tokens (name, token_hash, permissions)
		VALUES (?, ?, ?)`,
		name, tokenHash, permissions,
	)
	if err != nil {
		logger.Error("{users/db - CreateToken} %v", err)
		return 0, err
	}
	return res.LastInsertId()
}

// GetAllTokens returns all API tokens.
func GetAllTokens() ([]APIToken, error) {
	rows, err := db.Get().Query(`
		SELECT id, name, token_hash, permissions
		FROM kp_api_tokens ORDER BY id ASC`)
	if err != nil {
		logger.Error("{users/db - GetAllTokens} %v", err)
		return nil, err
	}
	defer rows.Close()
	return scanTokens(rows)
}

// GetTokenByHash retrieves an API token by its hash.
func GetTokenByHash(hash string) (APIToken, error) {
	var t APIToken
	err := db.Get().QueryRow(`
		SELECT id, name, token_hash, permissions
		FROM kp_api_tokens WHERE token_hash = ?`, hash).
		Scan(&t.ID, &t.Name, &t.TokenHash, &t.Permissions)
	if err != nil {
		logger.Error("{users/db - GetTokenByHash} %v", err)
	}
	return t, err
}

// DeleteToken removes an API token by ID.
func DeleteToken(id int64) error {
	_, err := db.Get().Exec(`DELETE FROM kp_api_tokens WHERE id = ?`, id)
	if err != nil {
		logger.Error("{users/db - DeleteToken} %v", err)
	}
	return err
}

// scanUser scans a single *sql.Row into a User.
func scanUser(row *sql.Row) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Name, &u.Email, &u.Username, &u.PasswordHash, &u.CreatedAt, &u.LastLogin)
	if err != nil {
		logger.Error("{users/db - scanUser} %v", err)
		return u, fmt.Errorf("user not found: %w", err)
	}
	return u, nil
}

// scanTokens iterates a *sql.Rows result into an APIToken slice.
func scanTokens(rows *sql.Rows) ([]APIToken, error) {
	var tokens []APIToken
	for rows.Next() {
		var t APIToken
		if err := rows.Scan(&t.ID, &t.Name, &t.TokenHash, &t.Permissions); err != nil {
			logger.Error("{users/db - scanTokens} %v", err)
			return nil, err
		}
		tokens = append(tokens, t)
	}
	return tokens, rows.Err()
}
