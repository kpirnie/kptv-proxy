// work/db/db.go
package db

import (
	"database/sql"
	"kptv-proxy/work/logger"
	"sync"

	_ "github.com/ncruces/go-sqlite3/driver"
)

const dbPath = "/settings/kptv.db"

var (
	instance *sql.DB
	once     sync.Once
)

// Get returns the singleton database connection, initializing it on first call.
// The database file is created at /settings/kptv.db if it does not exist.
// Foreign key enforcement is enabled at the connection level.
func Get() *sql.DB {
	once.Do(func() {
		var err error
		instance, err = sql.Open("sqlite3", dbPath)
		if err != nil {
			logger.Error("{db - Get} Failed to open database: %v", err)
			panic(err)
		}

		// SQLite performs best with a single writer; cap the pool accordingly.
		instance.SetMaxOpenConns(1)

		if err = initSchema(instance); err != nil {
			logger.Error("{db - Get} Failed to initialize schema: %v", err)
			panic(err)
		}

	})
	return instance
}

// Close shuts down the database connection. Should be called during application
// shutdown after all other components have stopped accessing the database.
func Close() {
	if instance != nil {
		if err := instance.Close(); err != nil {
			logger.Error("{db - Close} Error closing database: %v", err)
		}
	}
}

// initSchema applies PRAGMA settings and creates all tables and indexes
// if they do not already exist. Safe to call on every startup.
func initSchema(db *sql.DB) error {
	_, err := db.Exec(`PRAGMA foreign_keys = ON;`)
	if err != nil {
		return err
	}

	_, err = db.Exec(`
	CREATE TABLE IF NOT EXISTS kp_settings (
		id        INTEGER PRIMARY KEY AUTOINCREMENT,
		the_key   TEXT    NOT NULL UNIQUE,
		the_value TEXT    NOT NULL
	);

	CREATE TABLE IF NOT EXISTS kp_sources (
		id               INTEGER PRIMARY KEY AUTOINCREMENT,
		name             TEXT    NOT NULL,
		uri              TEXT    NOT NULL,
		uname            TEXT    NOT NULL DEFAULT '',
		pword            TEXT    NOT NULL DEFAULT '',
		sort_order       INTEGER NOT NULL DEFAULT 1,
		max_cnx          INTEGER NOT NULL DEFAULT 5,
		max_stream_to    TEXT    NOT NULL DEFAULT '30s',
		retry_delay      TEXT    NOT NULL DEFAULT '5s',
		max_retries      INTEGER NOT NULL DEFAULT 3,
		max_failures     INTEGER NOT NULL DEFAULT 5,
		min_data_size    INTEGER NOT NULL DEFAULT 2,
		user_agent       TEXT    NOT NULL DEFAULT '',
		req_origin       TEXT    NOT NULL DEFAULT '',
		req_referer      TEXT    NOT NULL DEFAULT '',
		live_inc_regex   TEXT    NOT NULL DEFAULT '',
		live_exc_regex   TEXT    NOT NULL DEFAULT '',
		series_inc_regex TEXT    NOT NULL DEFAULT '',
		series_exc_regex TEXT    NOT NULL DEFAULT '',
		vod_inc_regex    TEXT    NOT NULL DEFAULT '',
		vod_exc_regex    TEXT    NOT NULL DEFAULT ''
	);

	CREATE TABLE IF NOT EXISTS kp_epgs (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		name       TEXT    NOT NULL,
		url        TEXT    NOT NULL,
		sort_order INTEGER NOT NULL DEFAULT 1
	);

	CREATE TABLE IF NOT EXISTS kp_xc_accounts (
		id            INTEGER PRIMARY KEY AUTOINCREMENT,
		name          TEXT    NOT NULL,
		uname         TEXT    NOT NULL,
		pword         TEXT    NOT NULL,
		max_cnx       INTEGER NOT NULL DEFAULT 10,
		enable_live   INTEGER NOT NULL DEFAULT 1,
		enable_series INTEGER NOT NULL DEFAULT 0,
		enable_vod    INTEGER NOT NULL DEFAULT 0
	);

	CREATE TABLE IF NOT EXISTS kp_sd_accounts (
		id            INTEGER PRIMARY KEY AUTOINCREMENT,
		name          TEXT    NOT NULL,
		uname         TEXT    NOT NULL,
		pword         TEXT    NOT NULL,
		enabled       INTEGER NOT NULL DEFAULT 1,
		days_to_fetch INTEGER NOT NULL DEFAULT 7
	);

	CREATE TABLE IF NOT EXISTS kp_sd_lineups (
		id            INTEGER PRIMARY KEY AUTOINCREMENT,
		sd_account_id INTEGER NOT NULL REFERENCES kp_sd_accounts(id) ON DELETE CASCADE,
		lineup_id     TEXT    NOT NULL DEFAULT ''
	);

	CREATE TABLE IF NOT EXISTS kp_stream_overrides (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		channel     TEXT    NOT NULL,
		s_hash      TEXT    NOT NULL,
		s_status    INTEGER NOT NULL DEFAULT 0,
		s_order     INTEGER NOT NULL DEFAULT -1,
		dead_reason TEXT    NOT NULL DEFAULT '',
		UNIQUE(channel, s_hash)
	);

	CREATE INDEX IF NOT EXISTS idx_sd_lineups_account     ON kp_sd_lineups(sd_account_id);
	CREATE INDEX IF NOT EXISTS idx_overrides_channel_hash ON kp_stream_overrides(channel, s_hash);
	`)

	return err
}
