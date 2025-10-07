-- Configuration table
CREATE TABLE IF NOT EXISTS config (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Sources table
CREATE TABLE IF NOT EXISTS sources (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    url TEXT NOT NULL UNIQUE,
    username TEXT,
    password TEXT,
    order_priority INTEGER DEFAULT 1,
    max_connections INTEGER DEFAULT 5,
    max_stream_timeout TEXT DEFAULT '30s',
    retry_delay TEXT DEFAULT '5s',
    max_retries INTEGER DEFAULT 3,
    max_failures_before_block INTEGER DEFAULT 5,
    min_data_size INTEGER DEFAULT 2,
    user_agent TEXT,
    req_origin TEXT,
    req_referrer TEXT,
    live_include_regex TEXT,
    live_exclude_regex TEXT,
    series_include_regex TEXT,
    series_exclude_regex TEXT,
    vod_include_regex TEXT,
    vod_exclude_regex TEXT,
    active BOOLEAN DEFAULT 1,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Channels table
CREATE TABLE IF NOT EXISTS channels (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    display_name TEXT NOT NULL,
    group_title TEXT,
    logo_url TEXT,
    preferred_stream_index INTEGER DEFAULT 0,
    active BOOLEAN DEFAULT 1,
    last_seen TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Streams table
CREATE TABLE IF NOT EXISTS streams (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    channel_id INTEGER NOT NULL,
    source_id INTEGER NOT NULL,
    url TEXT NOT NULL,
    stream_type TEXT DEFAULT 'live',
    resolution TEXT,
    bandwidth INTEGER,
    codecs TEXT,
    custom_order INTEGER,
    tvg_id TEXT,
    tvg_name TEXT,
    tvg_logo TEXT,
    epg_channel_id TEXT,
    attributes TEXT,
    failures INTEGER DEFAULT 0,
    last_failure TIMESTAMP,
    blocked BOOLEAN DEFAULT 0,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (channel_id) REFERENCES channels(id) ON DELETE CASCADE,
    FOREIGN KEY (source_id) REFERENCES sources(id) ON DELETE CASCADE
);

-- Dead streams table
CREATE TABLE IF NOT EXISTS dead_streams (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    channel_id INTEGER NOT NULL,
    stream_id INTEGER NOT NULL,
    url TEXT NOT NULL,
    source_name TEXT,
    reason TEXT,
    marked_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (channel_id) REFERENCES channels(id) ON DELETE CASCADE,
    FOREIGN KEY (stream_id) REFERENCES streams(id) ON DELETE CASCADE,
    UNIQUE(channel_id, stream_id)
);

-- Stream ordering table
CREATE TABLE IF NOT EXISTS stream_orders (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    channel_id INTEGER NOT NULL,
    stream_order TEXT NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (channel_id) REFERENCES channels(id) ON DELETE CASCADE,
    UNIQUE(channel_id)
);

-- Import history for tracking refresh operations
CREATE TABLE IF NOT EXISTS import_history (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source_id INTEGER NOT NULL,
    started_at TIMESTAMP NOT NULL,
    completed_at TIMESTAMP,
    status TEXT,
    streams_imported INTEGER DEFAULT 0,
    error_message TEXT,
    FOREIGN KEY (source_id) REFERENCES sources(id) ON DELETE CASCADE
);

-- Indexes for performance
CREATE INDEX IF NOT EXISTS idx_streams_channel ON streams(channel_id);
CREATE INDEX IF NOT EXISTS idx_streams_source ON streams(source_id);
CREATE INDEX IF NOT EXISTS idx_streams_blocked ON streams(blocked);
CREATE INDEX IF NOT EXISTS idx_channels_group ON channels(group_title);
CREATE INDEX IF NOT EXISTS idx_channels_active ON channels(active);
CREATE INDEX IF NOT EXISTS idx_dead_streams_channel ON dead_streams(channel_id);
CREATE INDEX IF NOT EXISTS idx_import_history_source ON import_history(source_id, started_at);
CREATE INDEX IF NOT EXISTS idx_sources_url ON sources(url);
CREATE INDEX IF NOT EXISTS idx_channels_name ON channels(name);
CREATE UNIQUE INDEX IF NOT EXISTS idx_streams_unique ON streams(channel_id, source_id, url);
