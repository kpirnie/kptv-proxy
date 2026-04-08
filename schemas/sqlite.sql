PRAGMA foreign_keys = ON;

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

CREATE TABLE IF NOT EXISTS kp_channels (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    name           TEXT    NOT NULL UNIQUE,
    m3u_attributes TEXT    NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS kp_streams (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    channel_id  INTEGER NOT NULL REFERENCES kp_channels(id) ON DELETE CASCADE,
    source_id   INTEGER NOT NULL REFERENCES kp_sources(id)  ON DELETE CASCADE,
    s_order     INTEGER NOT NULL DEFAULT 0,
    s_status    INTEGER NOT NULL DEFAULT 0,
    s_hash      TEXT    NOT NULL DEFAULT '',
    s_url       TEXT    NOT NULL DEFAULT '',
    dead_reason TEXT    NOT NULL DEFAULT ''
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

CREATE INDEX IF NOT EXISTS idx_streams_channel_id ON kp_streams(channel_id);
CREATE INDEX IF NOT EXISTS idx_streams_source_id  ON kp_streams(source_id);
CREATE INDEX IF NOT EXISTS idx_streams_s_hash     ON kp_streams(s_hash);
CREATE INDEX IF NOT EXISTS idx_sd_lineups_account ON kp_sd_lineups(sd_account_id);