// work/db/channels.go
package db

import (
	"database/sql"
	"kptv-proxy/work/logger"
)

// Channel mirrors a kp_channels row. M3UAttributes holds the raw JSON-encoded
// map of EXTINF key/value pairs (tvg-id, tvg-logo, group-title, etc.).
type Channel struct {
	ID            int64
	Name          string
	M3UAttributes string // JSON-encoded map[string]string
}

// GetAllChannels returns every channel row ordered by name ascending.
func GetAllChannels() ([]Channel, error) {
	rows, err := Get().Query(`
		SELECT id, name, m3u_attributes
		FROM kp_channels
		ORDER BY name ASC`)
	if err != nil {
		logger.Error("{db/channels - GetAllChannels} query failed: %v", err)
		return nil, err
	}
	defer rows.Close()
	return scanChannels(rows)
}

// GetChannel returns a single channel by primary key.
// Returns sql.ErrNoRows if the ID does not exist.
func GetChannel(id int64) (Channel, error) {
	row := Get().QueryRow(`
		SELECT id, name, m3u_attributes
		FROM kp_channels WHERE id = ?`, id)

	var c Channel
	if err := row.Scan(&c.ID, &c.Name, &c.M3UAttributes); err != nil {
		logger.Error("{db/channels - GetChannel} id=%d: %v", id, err)
		return c, err
	}
	return c, nil
}

// GetChannelByName returns a single channel by its unique name.
// Returns sql.ErrNoRows if no match exists.
func GetChannelByName(name string) (Channel, error) {
	row := Get().QueryRow(`
		SELECT id, name, m3u_attributes
		FROM kp_channels WHERE name = ?`, name)

	var c Channel
	if err := row.Scan(&c.ID, &c.Name, &c.M3UAttributes); err != nil {
		logger.Error("{db/channels - GetChannelByName} name=%s: %v", name, err)
		return c, err
	}
	return c, nil
}

// UpsertChannel inserts the channel if the name does not exist, or updates
// m3u_attributes if it does. Returns the row ID in either case.
func UpsertChannel(c Channel) (int64, error) {
	res, err := Get().Exec(`
		INSERT INTO kp_channels (name, m3u_attributes)
		VALUES (?, ?)
		ON CONFLICT(name) DO UPDATE SET m3u_attributes = excluded.m3u_attributes`,
		c.Name, c.M3UAttributes,
	)
	if err != nil {
		logger.Error("{db/channels - UpsertChannel} name=%s: %v", c.Name, err)
		return 0, err
	}

	// LastInsertId returns 0 on a no-op update in SQLite; fetch the real ID.
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if id == 0 {
		existing, err := GetChannelByName(c.Name)
		if err != nil {
			return 0, err
		}
		return existing.ID, nil
	}
	return id, nil
}

// DeleteChannel removes a channel row by ID. Cascades to kp_streams via FK.
func DeleteChannel(id int64) error {
	_, err := Get().Exec(`DELETE FROM kp_channels WHERE id = ?`, id)
	if err != nil {
		logger.Error("{db/channels - DeleteChannel} id=%d: %v", id, err)
	}
	return err
}

// DeleteAllChannels removes every channel row and their associated streams.
// Used to reset state before a full re-import.
func DeleteAllChannels() error {
	_, err := Get().Exec(`DELETE FROM kp_channels`)
	if err != nil {
		logger.Error("{db/channels - DeleteAllChannels} %v", err)
	}
	return err
}

// scanChannels iterates a *sql.Rows result into a Channel slice.
func scanChannels(rows *sql.Rows) ([]Channel, error) {
	var channels []Channel
	for rows.Next() {
		var c Channel
		if err := rows.Scan(&c.ID, &c.Name, &c.M3UAttributes); err != nil {
			logger.Error("{db/channels - scanChannels} scan failed: %v", err)
			return nil, err
		}
		channels = append(channels, c)
	}
	return channels, rows.Err()
}
