// work/db/streams.go
package db

import (
	"database/sql"
	"kptv-proxy/work/logger"
)

// Stream mirrors a kp_streams row.
// SStatus values: 0 = alive, 1 = bad, 2 = dead (manual), 3 = dead (auto-blocked).
type Stream struct {
	ID         int64
	ChannelID  int64
	SourceID   int64
	SOrder     int
	SStatus    int
	SHash      string
	SURL       string
	DeadReason string
}

// GetStreamsByChannel returns all streams for a channel ordered by s_order ascending.
func GetStreamsByChannel(channelID int64) ([]Stream, error) {
	rows, err := Get().Query(`
		SELECT id, channel_id, source_id, s_order, s_status, s_hash, s_url, dead_reason
		FROM kp_streams
		WHERE channel_id = ?
		ORDER BY s_order ASC`, channelID)
	if err != nil {
		logger.Error("{db/streams - GetStreamsByChannel} channel_id=%d: %v", channelID, err)
		return nil, err
	}
	defer rows.Close()
	return scanStreams(rows)
}

// GetStream returns a single stream by primary key.
// Returns sql.ErrNoRows if the ID does not exist.
func GetStream(id int64) (Stream, error) {
	row := Get().QueryRow(`
		SELECT id, channel_id, source_id, s_order, s_status, s_hash, s_url, dead_reason
		FROM kp_streams WHERE id = ?`, id)

	var s Stream
	if err := scanStream(row, &s); err != nil {
		logger.Error("{db/streams - GetStream} id=%d: %v", id, err)
		return s, err
	}
	return s, nil
}

// GetStreamByHash returns the stream matching the given URL hash within a channel.
// Returns sql.ErrNoRows if no match exists.
func GetStreamByHash(channelID int64, hash string) (Stream, error) {
	row := Get().QueryRow(`
		SELECT id, channel_id, source_id, s_order, s_status, s_hash, s_url, dead_reason
		FROM kp_streams
		WHERE channel_id = ? AND s_hash = ?`, channelID, hash)

	var s Stream
	if err := scanStream(row, &s); err != nil {
		logger.Error("{db/streams - GetStreamByHash} channel_id=%d hash=%s: %v", channelID, hash, err)
		return s, err
	}
	return s, nil
}

// InsertStream inserts a new stream row and returns the assigned ID.
func InsertStream(s Stream) (int64, error) {
	res, err := Get().Exec(`
		INSERT INTO kp_streams
			(channel_id, source_id, s_order, s_status, s_hash, s_url, dead_reason)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		s.ChannelID, s.SourceID, s.SOrder, s.SStatus,
		s.SHash, s.SURL, s.DeadReason,
	)
	if err != nil {
		logger.Error("{db/streams - InsertStream} %v", err)
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateStreamStatus updates only the s_status and dead_reason columns for
// a stream. Used by dead-stream management without touching ordering.
func UpdateStreamStatus(id int64, status int, deadReason string) error {
	_, err := Get().Exec(`
		UPDATE kp_streams SET s_status=?, dead_reason=? WHERE id=?`,
		status, deadReason, id,
	)
	if err != nil {
		logger.Error("{db/streams - UpdateStreamStatus} id=%d: %v", id, err)
	}
	return err
}

// UpdateStreamOrder updates the s_order column for a stream.
func UpdateStreamOrder(id int64, order int) error {
	_, err := Get().Exec(
		`UPDATE kp_streams SET s_order=? WHERE id=?`, order, id,
	)
	if err != nil {
		logger.Error("{db/streams - UpdateStreamOrder} id=%d: %v", id, err)
	}
	return err
}

// DeleteStream removes a single stream row by ID.
func DeleteStream(id int64) error {
	_, err := Get().Exec(`DELETE FROM kp_streams WHERE id = ?`, id)
	if err != nil {
		logger.Error("{db/streams - DeleteStream} id=%d: %v", id, err)
	}
	return err
}

// DeleteStreamsByChannel removes all stream rows for a given channel.
// Used during re-import to clear stale entries before reinserting.
func DeleteStreamsByChannel(channelID int64) error {
	_, err := Get().Exec(`DELETE FROM kp_streams WHERE channel_id = ?`, channelID)
	if err != nil {
		logger.Error("{db/streams - DeleteStreamsByChannel} channel_id=%d: %v", channelID, err)
	}
	return err
}

// UpsertStream inserts a stream or updates s_order, s_status, and dead_reason
// if a row with the same channel_id and s_hash already exists.
func UpsertStream(s Stream) (int64, error) {
	res, err := Get().Exec(`
		INSERT INTO kp_streams
			(channel_id, source_id, s_order, s_status, s_hash, s_url, dead_reason)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(channel_id, s_hash) DO UPDATE SET
			s_order     = excluded.s_order,
			s_status    = excluded.s_status,
			dead_reason = excluded.dead_reason`,
		s.ChannelID, s.SourceID, s.SOrder, s.SStatus,
		s.SHash, s.SURL, s.DeadReason,
	)
	if err != nil {
		logger.Error("{db/streams - UpsertStream} %v", err)
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if id == 0 {
		existing, err := GetStreamByHash(s.ChannelID, s.SHash)
		if err != nil {
			return 0, err
		}
		return existing.ID, nil
	}
	return id, nil
}

// scanStreams iterates a *sql.Rows result into a Stream slice.
func scanStreams(rows *sql.Rows) ([]Stream, error) {
	var streams []Stream
	for rows.Next() {
		var s Stream
		if err := rows.Scan(
			&s.ID, &s.ChannelID, &s.SourceID, &s.SOrder,
			&s.SStatus, &s.SHash, &s.SURL, &s.DeadReason,
		); err != nil {
			logger.Error("{db/streams - scanStreams} scan failed: %v", err)
			return nil, err
		}
		streams = append(streams, s)
	}
	return streams, rows.Err()
}

// scanStream scans a single *sql.Row into a Stream.
func scanStream(row *sql.Row, s *Stream) error {
	return row.Scan(
		&s.ID, &s.ChannelID, &s.SourceID, &s.SOrder,
		&s.SStatus, &s.SHash, &s.SURL, &s.DeadReason,
	)
}

// Ensure sql import is used.
var _ = (*sql.Rows)(nil)
