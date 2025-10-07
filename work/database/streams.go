package database

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"kptv-proxy/work/types"
	"time"
)

// StreamRow represents a stream record from the database
type StreamRow struct {
	ID            int64
	ChannelID     int64
	SourceID      int64
	URL           string
	StreamType    string
	Resolution    string
	Bandwidth     int
	Codecs        string
	CustomOrder   int
	TvgID         string
	TvgName       string
	TvgLogo       string
	EpgChannelID  string
	Attributes    map[string]string
	Failures      int
	LastFailure   *time.Time
	Blocked       bool
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// SaveStream saves or updates a stream
func (db *DB) SaveStream(channelID, sourceID int64, stream *types.Stream) (int64, error) {
	// Serialize attributes to JSON
	attributesJSON, err := json.Marshal(stream.Attributes)
	if err != nil {
		return 0, fmt.Errorf("failed to marshal attributes: %w", err)
	}

	// Extract specific attributes
	tvgID := stream.Attributes["tvg-id"]
	tvgName := stream.Attributes["tvg-name"]
	tvgLogo := stream.Attributes["tvg-logo"]
	epgChannelID := stream.Attributes["epg_channel_id"]
	resolution := stream.Attributes["resolution"]
	streamType := stream.Attributes["group-title"]
	if streamType == "" {
		streamType = "live"
	}

	query := `
		INSERT INTO streams (
			channel_id, source_id, url, stream_type, resolution, bandwidth, codecs,
			custom_order, tvg_id, tvg_name, tvg_logo, epg_channel_id, attributes,
			failures, blocked, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(channel_id, source_id, url) DO UPDATE SET
			stream_type = excluded.stream_type,
			resolution = excluded.resolution,
			bandwidth = excluded.bandwidth,
			codecs = excluded.codecs,
			tvg_id = excluded.tvg_id,
			tvg_name = excluded.tvg_name,
			tvg_logo = excluded.tvg_logo,
			epg_channel_id = excluded.epg_channel_id,
			attributes = excluded.attributes,
			updated_at = CURRENT_TIMESTAMP
	`

	// Note: We need a unique constraint for this. Let's add it to the migration
	result, err := db.Exec(query,
		channelID, sourceID, stream.URL, streamType, resolution, 0, "",
		stream.CustomOrder, tvgID, tvgName, tvgLogo, epgChannelID, string(attributesJSON),
		0, false,
	)

	if err != nil {
		return 0, fmt.Errorf("failed to save stream: %w", err)
	}

	return result.LastInsertId()
}

// LoadStreamsForChannel loads all streams for a specific channel
func (db *DB) LoadStreamsForChannel(channelID int64) ([]*StreamRow, error) {
	query := `
		SELECT id, channel_id, source_id, url, stream_type, resolution, bandwidth, codecs,
		       custom_order, tvg_id, tvg_name, tvg_logo, epg_channel_id, attributes,
		       failures, last_failure, blocked, created_at, updated_at
		FROM streams
		WHERE channel_id = ? AND blocked = 0
		ORDER BY custom_order, id
	`

	rows, err := db.Query(query, channelID)
	if err != nil {
		return nil, fmt.Errorf("failed to load streams: %w", err)
	}
	defer rows.Close()

	var streams []*StreamRow
	for rows.Next() {
		var s StreamRow
		var attributesJSON string
		var lastFailure sql.NullTime

		err := rows.Scan(
			&s.ID, &s.ChannelID, &s.SourceID, &s.URL, &s.StreamType, &s.Resolution,
			&s.Bandwidth, &s.Codecs, &s.CustomOrder, &s.TvgID, &s.TvgName, &s.TvgLogo,
			&s.EpgChannelID, &attributesJSON, &s.Failures, &lastFailure, &s.Blocked,
			&s.CreatedAt, &s.UpdatedAt,
		)
		if err != nil {
			continue
		}

		if lastFailure.Valid {
			s.LastFailure = &lastFailure.Time
		}

		// Deserialize attributes
		if err := json.Unmarshal([]byte(attributesJSON), &s.Attributes); err != nil {
			s.Attributes = make(map[string]string)
		}

		streams = append(streams, &s)
	}

	return streams, nil
}

// UpdateStreamFailure increments the failure count for a stream
func (db *DB) UpdateStreamFailure(streamID int64) error {
	_, err := db.Exec(`
		UPDATE streams 
		SET failures = failures + 1, 
		    last_failure = CURRENT_TIMESTAMP, 
		    updated_at = CURRENT_TIMESTAMP 
		WHERE id = ?
	`, streamID)
	return err
}

// BlockStream marks a stream as blocked
func (db *DB) BlockStream(streamID int64) error {
	_, err := db.Exec(`
		UPDATE streams 
		SET blocked = 1, 
		    updated_at = CURRENT_TIMESTAMP 
		WHERE id = ?
	`, streamID)
	return err
}

// UnblockStream unblocks a stream and resets its failure count
func (db *DB) UnblockStream(streamID int64) error {
	_, err := db.Exec(`
		UPDATE streams 
		SET blocked = 0, 
		    failures = 0,
		    last_failure = NULL,
		    updated_at = CURRENT_TIMESTAMP 
		WHERE id = ?
	`, streamID)
	return err
}

// GetStreamByID retrieves a stream by its ID
func (db *DB) GetStreamByID(streamID int64) (*StreamRow, error) {
	query := `
		SELECT id, channel_id, source_id, url, stream_type, resolution, bandwidth, codecs,
		       custom_order, tvg_id, tvg_name, tvg_logo, epg_channel_id, attributes,
		       failures, last_failure, blocked, created_at, updated_at
		FROM streams
		WHERE id = ?
	`

	var s StreamRow
	var attributesJSON string
	var lastFailure sql.NullTime

	err := db.QueryRow(query, streamID).Scan(
		&s.ID, &s.ChannelID, &s.SourceID, &s.URL, &s.StreamType, &s.Resolution,
		&s.Bandwidth, &s.Codecs, &s.CustomOrder, &s.TvgID, &s.TvgName, &s.TvgLogo,
		&s.EpgChannelID, &attributesJSON, &s.Failures, &lastFailure, &s.Blocked,
		&s.CreatedAt, &s.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get stream: %w", err)
	}

	if lastFailure.Valid {
		s.LastFailure = &lastFailure.Time
	}

	// Deserialize attributes
	if err := json.Unmarshal([]byte(attributesJSON), &s.Attributes); err != nil {
		s.Attributes = make(map[string]string)
	}

	return &s, nil
}

// DeleteStreamsForChannel deletes all streams for a channel (used during refresh)
func (db *DB) DeleteStreamsForChannel(channelID int64) error {
	_, err := db.Exec("DELETE FROM streams WHERE channel_id = ?", channelID)
	return err
}

// GetBlockedStreams retrieves all blocked streams
func (db *DB) GetBlockedStreams() ([]*StreamRow, error) {
	query := `
		SELECT s.id, s.channel_id, s.source_id, s.url, s.stream_type, s.resolution, 
		       s.bandwidth, s.codecs, s.custom_order, s.tvg_id, s.tvg_name, s.tvg_logo,
		       s.epg_channel_id, s.attributes, s.failures, s.last_failure, s.blocked, 
		       s.created_at, s.updated_at
		FROM streams s
		WHERE s.blocked = 1
		ORDER BY s.last_failure DESC
	`

	rows, err := db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to load blocked streams: %w", err)
	}
	defer rows.Close()

	var streams []*StreamRow
	for rows.Next() {
		var s StreamRow
		var attributesJSON string
		var lastFailure sql.NullTime

		err := rows.Scan(
			&s.ID, &s.ChannelID, &s.SourceID, &s.URL, &s.StreamType, &s.Resolution,
			&s.Bandwidth, &s.Codecs, &s.CustomOrder, &s.TvgID, &s.TvgName, &s.TvgLogo,
			&s.EpgChannelID, &attributesJSON, &s.Failures, &lastFailure, &s.Blocked,
			&s.CreatedAt, &s.UpdatedAt,
		)
		if err != nil {
			continue
		}

		if lastFailure.Valid {
			s.LastFailure = &lastFailure.Time
		}

		if err := json.Unmarshal([]byte(attributesJSON), &s.Attributes); err != nil {
			s.Attributes = make(map[string]string)
		}

		streams = append(streams, &s)
	}

	return streams, nil
}

// GetStreamStats returns statistics about streams
func (db *DB) GetStreamStats() (map[string]interface{}, error) {
	stats := make(map[string]interface{})

	// Total streams
	var total int
	err := db.QueryRow("SELECT COUNT(*) FROM streams").Scan(&total)
	if err != nil {
		return nil, err
	}
	stats["total_streams"] = total

	// Blocked streams
	var blocked int
	err = db.QueryRow("SELECT COUNT(*) FROM streams WHERE blocked = 1").Scan(&blocked)
	if err != nil {
		return nil, err
	}
	stats["blocked_streams"] = blocked

	// Streams with failures
	var withFailures int
	err = db.QueryRow("SELECT COUNT(*) FROM streams WHERE failures > 0").Scan(&withFailures)
	if err != nil {
		return nil, err
	}
	stats["streams_with_failures"] = withFailures

	// Streams by type
	rows, err := db.Query(`
		SELECT stream_type, COUNT(*) as count
		FROM streams
		GROUP BY stream_type
		ORDER BY count DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	typeCounts := make(map[string]int)
	for rows.Next() {
		var streamType string
		var count int
		if err := rows.Scan(&streamType, &count); err != nil {
			continue
		}
		typeCounts[streamType] = count
	}
	stats["streams_by_type"] = typeCounts

	return stats, nil
}
