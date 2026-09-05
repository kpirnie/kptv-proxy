// work/db/db.go
package db

import (
	"database/sql"
	"fmt"
	"kptv-proxy/work/constants"
	"kptv-proxy/work/logger"
	"runtime"
	"sync"

	_ "github.com/ncruces/go-sqlite3/driver"
)

// dbPragmas is applied through the DSN rather than with Exec so that every
// pooled connection comes up configured; an Exec only configures whichever
// connection happens to serve it.
const dbPragmas = "_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)"

// maxReaderConns caps the read pool regardless of core count.
const maxReaderConns = 8

var (
	instance *sql.DB
	reader   *sql.DB
	once     sync.Once
)

// dsn builds the sqlite connection string for the configured database file.
func dsn() string {
	return fmt.Sprintf("file:%s?%s", constants.Internal.DatabasePath, dbPragmas)
}

// Get returns the singleton database connection, initializing it on first call.
// The database file is created at /settings/kptv.db if it does not exist.
// Foreign key enforcement is enabled at the connection level.
func Get() *sql.DB {
	once.Do(func() {
		var err error
		instance, err = sql.Open("sqlite3", dsn())
		if err != nil {
			logger.Error("{db - Get} Failed to open database: %v", err)
			panic(err)
		}

		// SQLite permits one writer at a time; the write pool stays capped at one.
		instance.SetMaxOpenConns(1)

		if err = initSchema(instance); err != nil {
			logger.Error("{db - Get} Failed to initialize schema: %v", err)
			panic(err)
		}

		reader, err = sql.Open("sqlite3", dsn())
		if err != nil {
			logger.Error("{db - Get} Failed to open reader pool: %v", err)
			panic(err)
		}

		// WAL allows concurrent readers alongside the single writer.
		readerConns := runtime.NumCPU()
		if readerConns > maxReaderConns {
			readerConns = maxReaderConns
		}
		if readerConns < 2 {
			readerConns = 2
		}
		reader.SetMaxOpenConns(readerConns)
		reader.SetMaxIdleConns(readerConns)

	})
	return instance
}

// GetReader returns the read-only connection pool, initializing the database on
// first call if necessary. Use it for every SELECT path; writes must go through
// Get so they stay serialized on the single write connection.
func GetReader() *sql.DB {
	Get()
	return reader
}

// Close shuts down the database connection. Should be called during application
// shutdown after all other components have stopped accessing the database.
func Close() {
	if reader != nil {
		if err := reader.Close(); err != nil {
			logger.Error("{db - Close} Error closing reader pool: %v", err)
		}
	}
	if instance != nil {
		if err := instance.Close(); err != nil {
			logger.Error("{db - Close} Error closing database: %v", err)
		}
	}
}

// initSchema applies PRAGMA settings and creates all tables and indexes
// if they do not already exist. Safe to call on every startup.
func initSchema(db *sql.DB) error {

	_, err := db.Exec(`
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

	CREATE TABLE IF NOT EXISTS kp_users (
		id            INTEGER PRIMARY KEY AUTOINCREMENT,
		name          TEXT    NOT NULL,
		email         TEXT    NOT NULL UNIQUE,
		username      TEXT    NOT NULL UNIQUE,
		password_hash TEXT    NOT NULL,
		created_at    INTEGER NOT NULL,
		last_login    INTEGER NOT NULL DEFAULT 0
	);

	CREATE TABLE IF NOT EXISTS kp_api_tokens (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		name        TEXT    NOT NULL,
		token_hash  TEXT    NOT NULL UNIQUE,
		permissions INTEGER NOT NULL DEFAULT 0
	);


	CREATE TABLE IF NOT EXISTS kp_channel_epg (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		channel     TEXT NOT NULL UNIQUE,
		epg_id      TEXT NOT NULL DEFAULT '',
		epg_name    TEXT NOT NULL DEFAULT ''
	);

	CREATE TABLE IF NOT EXISTS kp_stream_order (
		id      INTEGER PRIMARY KEY AUTOINCREMENT,
		channel TEXT    NOT NULL,
		s_hash  TEXT    NOT NULL,
		s_order INTEGER NOT NULL DEFAULT 0,
		UNIQUE(channel, s_hash)
	);

	CREATE TABLE IF NOT EXISTS kp_local_sources (
		id            INTEGER PRIMARY KEY AUTOINCREMENT,
		name          TEXT    NOT NULL,
		path          TEXT    NOT NULL,
		media_type    INTEGER NOT NULL DEFAULT 1,
		group_prefix  TEXT    NOT NULL DEFAULT '',
		sort_order    INTEGER NOT NULL DEFAULT 1,
		enabled       INTEGER NOT NULL DEFAULT 1,
		inc_regex     TEXT    NOT NULL DEFAULT '',
		exc_regex     TEXT    NOT NULL DEFAULT '',
		last_scan     INTEGER NOT NULL DEFAULT 0,
		entry_count   INTEGER NOT NULL DEFAULT 0,
		UNIQUE(path, media_type)
	);

	CREATE TABLE IF NOT EXISTS kp_local_media (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		local_source_id INTEGER NOT NULL REFERENCES kp_local_sources(id) ON DELETE CASCADE,
		s_hash          TEXT    NOT NULL,
		path            TEXT    NOT NULL,
		media_type      INTEGER NOT NULL DEFAULT 1,
		group_title     TEXT    NOT NULL DEFAULT '',
		tvg_name        TEXT    NOT NULL DEFAULT '',
		display         TEXT    NOT NULL DEFAULT '',
		duration        INTEGER NOT NULL DEFAULT 0,
		year            TEXT    NOT NULL DEFAULT '',
		artist          TEXT    NOT NULL DEFAULT '',
		album           TEXT    NOT NULL DEFAULT '',
		disc            INTEGER NOT NULL DEFAULT 0,
		track           INTEGER NOT NULL DEFAULT 0,
		series          TEXT    NOT NULL DEFAULT '',
		season          INTEGER NOT NULL DEFAULT 0,
		episode         INTEGER NOT NULL DEFAULT 0,
		episode_title   TEXT    NOT NULL DEFAULT '',
		title           TEXT    NOT NULL DEFAULT '',
		sort_title      TEXT    NOT NULL DEFAULT '',
		plot            TEXT    NOT NULL DEFAULT '',
		tagline         TEXT    NOT NULL DEFAULT '',
		poster          TEXT    NOT NULL DEFAULT '',
		fanart          TEXT    NOT NULL DEFAULT '',
		rating          REAL    NOT NULL DEFAULT 0,
		critic_rating   INTEGER NOT NULL DEFAULT 0,
		mpaa            TEXT    NOT NULL DEFAULT '',
		country         TEXT    NOT NULL DEFAULT '',
		premiered       TEXT    NOT NULL DEFAULT '',
		imdb_id         TEXT    NOT NULL DEFAULT '',
		tmdb_id         TEXT    NOT NULL DEFAULT '',
		tvdb_id         TEXT    NOT NULL DEFAULT '',
		collection      TEXT    NOT NULL DEFAULT '',
		genres          TEXT    NOT NULL DEFAULT '',
		studios         TEXT    NOT NULL DEFAULT '',
		tags            TEXT    NOT NULL DEFAULT '',
		directors       TEXT    NOT NULL DEFAULT '',
		writers         TEXT    NOT NULL DEFAULT '',
		cast_json       TEXT    NOT NULL DEFAULT '',
		sort_key        TEXT    NOT NULL DEFAULT '',
		mod_time        INTEGER NOT NULL DEFAULT 0,
		file_size       INTEGER NOT NULL DEFAULT 0,
		UNIQUE(local_source_id, path)
	);

	CREATE TABLE IF NOT EXISTS kp_series_info (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		source_url TEXT    NOT NULL,
		series_id  TEXT    NOT NULL,
		payload    TEXT    NOT NULL DEFAULT '',
		fetched_at INTEGER NOT NULL DEFAULT 0,
		UNIQUE(source_url, series_id)
	);

	CREATE TABLE IF NOT EXISTS kp_series_episodes (
		id           INTEGER PRIMARY KEY AUTOINCREMENT,
		episode_id   INTEGER NOT NULL,
		channel_name TEXT    NOT NULL DEFAULT '',
		season       INTEGER NOT NULL DEFAULT 0,
		episode      INTEGER NOT NULL DEFAULT 0,
		source_url   TEXT    NOT NULL,
		series_id    TEXT    NOT NULL,
		upstream_id  TEXT    NOT NULL,
		extension    TEXT    NOT NULL DEFAULT 'mp4',
		UNIQUE(episode_id, source_url)
	);

	CREATE INDEX IF NOT EXISTS idx_sd_lineups_account     ON kp_sd_lineups(sd_account_id);
	CREATE INDEX IF NOT EXISTS idx_overrides_channel_hash ON kp_stream_overrides(channel, s_hash);	
	CREATE INDEX IF NOT EXISTS idx_users_username ON kp_users(username);
	CREATE INDEX IF NOT EXISTS idx_users_email    ON kp_users(email);
	CREATE INDEX IF NOT EXISTS idx_tokens_hash    ON kp_api_tokens(token_hash);
	CREATE INDEX IF NOT EXISTS idx_stream_order_channel ON kp_stream_order(channel, s_order);
	CREATE INDEX IF NOT EXISTS idx_local_sources_order ON kp_local_sources(sort_order);
	CREATE INDEX IF NOT EXISTS idx_local_media_source  ON kp_local_media(local_source_id, sort_key);
	CREATE INDEX IF NOT EXISTS idx_local_media_hash    ON kp_local_media(s_hash);
	CREATE INDEX IF NOT EXISTS idx_series_info_lookup ON kp_series_info(source_url, series_id);
	CREATE INDEX IF NOT EXISTS idx_series_episodes_episode ON kp_series_episodes(episode_id);
	CREATE INDEX IF NOT EXISTS idx_series_episodes_series  ON kp_series_episodes(source_url, series_id);
	`)

	if err != nil {
		return err
	}

	return rebuildSeriesEpisodes(db)
}

// rebuildSeriesEpisodes drops a pre-rekey kp_series_episodes and recreates it in
// its current shape. Episode IDs were once minted per source and constrained
// UNIQUE, which cannot hold now that one ID maps to every provider carrying the
// episode. The table is a cache of get_series_info, so nothing is carried over.
func rebuildSeriesEpisodes(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(kp_series_episodes)`)
	if err != nil {
		return err
	}
	defer rows.Close()

	current := false
	for rows.Next() {
		var cid, notNull, pk int
		var name, colType string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			return err
		}
		if name == "season" {
			current = true
		}
	}
	if current {
		return nil
	}

	logger.Info("Rebuilding kp_series_episodes for source-independent episode IDs...")
	_, err = db.Exec(`
	DROP TABLE IF EXISTS kp_series_episodes;

	CREATE TABLE kp_series_episodes (
		id           INTEGER PRIMARY KEY AUTOINCREMENT,
		episode_id   INTEGER NOT NULL,
		channel_name TEXT    NOT NULL DEFAULT '',
		season       INTEGER NOT NULL DEFAULT 0,
		episode      INTEGER NOT NULL DEFAULT 0,
		source_url   TEXT    NOT NULL,
		series_id    TEXT    NOT NULL,
		upstream_id  TEXT    NOT NULL,
		extension    TEXT    NOT NULL DEFAULT 'mp4',
		UNIQUE(episode_id, source_url)
	);

	CREATE INDEX IF NOT EXISTS idx_series_episodes_episode ON kp_series_episodes(episode_id);
	CREATE INDEX IF NOT EXISTS idx_series_episodes_series  ON kp_series_episodes(source_url, series_id);
	`)
	return err
}
