package database

import (
	"database/sql"
	"fmt"
	"time"
)

// DeadStreamRow represents a dead stream record
type DeadStreamRow struct {
	ID          int64
	ChannelID   int64
	StreamID    int64
	URL         string
	SourceName  string
	Reason      string
	MarkedAt    time.Time
}

// MarkStreamDead marks a stream as dead in the database
func (db *DB) MarkStreamDead(channelID, streamID int64, url, sourceName, reason string) error {
	query := `
		INSERT INTO dead_streams (channel_id, stream_id, url, source_name, reason, marked_at)
		VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(channel_id, stream_id) DO UPDATE SET
			url = excluded.url,
			source_name = excluded.source_name,
			reason = excluded.reason,
			marked_at = CURRENT_TIMESTAMP
	`

	_, err := db.Exec(query, channelID, streamID, url, sourceName, reason)
	if err != nil {
		return fmt.Errorf("failed to mark stream dead: %w", err)
	}

	// Also block the stream in the streams table
	db.BlockStream(streamID)

	return nil
}

// ReviveStream removes a stream from the dead streams list
func (db *DB) ReviveStream(channelID, streamID int64) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Remove from dead streams
	_, err = tx.Exec("DELETE FROM dead_streams WHERE channel_id = ? AND stream_id = ?", channelID, streamID)
	if err != nil {
		return fmt.Errorf("failed to remove from dead streams: %w", err)
	}

	// Unblock the stream
	_, err = tx.Exec(`
		UPDATE streams 
		SET blocked = 0, failures = 0, last_failure = NULL, updated_at = CURRENT_TIMESTAMP 
		WHERE id = ?
	`, streamID)
	if err != nil {
		return fmt.Errorf("failed to unblock stream: %w", err)
	}

	return tx.Commit()
}

// IsStreamDead checks if a stream is marked as dead
func (db *DB) IsStreamDead(channelID, streamID int64) (bool, error) {
	var exists bool
	err := db.QueryRow(`
		SELECT EXISTS(SELECT 1 FROM dead_streams WHERE channel_id = ? AND stream_id = ?)
	`, channelID, streamID).Scan(&exists)
	
	if err != nil {
		return false, err
	}
	return exists, nil
}

// GetDeadStreamReason retrieves the reason a stream was marked dead
func (db *DB) GetDeadStreamReason(channelID, streamID int64) (string, error) {
	var reason string
	err := db.QueryRow(`
		SELECT reason FROM dead_streams WHERE channel_id = ? AND stream_id = ?
	`, channelID, streamID).Scan(&reason)
	
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return reason, nil
}

// LoadDeadStreams retrieves all dead streams
func (db *DB) LoadDeadStreams() ([]*DeadStreamRow, error) {
	query := `
		SELECT id, channel_id, stream_id, url, source_name, reason, marked_at
		FROM dead_streams
		ORDER BY marked_at DESC
	`

	rows, err := db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to load dead streams: %w", err)
	}
	defer rows.Close()

	var deadStreams []*DeadStreamRow
	for rows.Next() {
		var ds DeadStreamRow
		err := rows.Scan(
			&ds.ID, &ds.ChannelID, &ds.StreamID, &ds.URL, 
			&ds.SourceName, &ds.Reason, &ds.MarkedAt,
		)
		if err != nil {
			continue
		}
		deadStreams = append(deadStreams, &ds)
	}

	return deadStreams, nil
}

// GetDeadStreamsForChannel retrieves all dead streams for a specific channel
func (db *DB) GetDeadStreamsForChannel(channelID int64) ([]*DeadStreamRow, error) {
	query := `
		SELECT id, channel_id, stream_id, url, source_name, reason, marked_at
		FROM dead_streams
		WHERE channel_id = ?
		ORDER BY marked_at DESC
	`

	rows, err := db.Query(query, channelID)
	if err != nil {
		return nil, fmt.Errorf("failed to load dead streams: %w", err)
	}
	defer rows.Close()

	var deadStreams []*DeadStreamRow
	for rows.Next() {
		var ds DeadStreamRow
		err := rows.Scan(
			&ds.ID, &ds.ChannelID, &ds.StreamID, &ds.URL, 
			&ds.SourceName, &ds.Reason, &ds.MarkedAt,
		)
		if err != nil {
			continue
		}
		deadStreams = append(deadStreams, &ds)
	}

	return deadStreams, nil
}

// CleanupOldDeadStreams removes dead stream records older than the specified duration
func (db *DB) CleanupOldDeadStreams(olderThan time.Duration) (int64, error) {
	cutoff := time.Now().Add(-olderThan)
	result, err := db.Exec("DELETE FROM dead_streams WHERE marked_at < ?", cutoff)
	if err != nil {
		return 0, fmt.Errorf("failed to cleanup old dead streams: %w", err)
	}
	return result.RowsAffected()
}

// GetDeadStreamStats returns statistics about dead streams
func (db *DB) GetDeadStreamStats() (map[string]interface{}, error) {
	stats := make(map[string]interface{})

	// Total dead streams
	var total int
	err := db.QueryRow("SELECT COUNT(*) FROM dead_streams").Scan(&total)
	if err != nil {
		return nil, err
	}
	stats["total_dead_streams"] = total

	// Dead streams by reason
	rows, err := db.Query(`
		SELECT reason, COUNT(*) as count
		FROM dead_streams
		GROUP BY reason
		ORDER BY count DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	reasonCounts := make(map[string]int)
	for rows.Next() {
		var reason string
		var count int
		if err := rows.Scan(&reason, &count); err != nil {
			continue
		}
		reasonCounts[reason] = count
	}
	stats["dead_streams_by_reason"] = reasonCounts

	// Recent dead streams (last 24 hours)
	var recent int
	cutoff := time.Now().Add(-24 * time.Hour)
	err = db.QueryRow("SELECT COUNT(*) FROM dead_streams WHERE marked_at > ?", cutoff).Scan(&recent)
	if err != nil {
		return nil, err
	}
	stats["dead_streams_last_24h"] = recent

	return stats, nil
}