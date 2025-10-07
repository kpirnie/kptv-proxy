package database

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"kptv-proxy/work/types"
	"time"
)

// ChannelRow represents a channel record from the database
type ChannelRow struct {
	ID                   int64
	Name                 string
	DisplayName          string
	GroupTitle           string
	LogoURL              string
	PreferredStreamIndex int
	Active               bool
	LastSeen             time.Time
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

// LoadChannels loads all active channels from the database
func (db *DB) LoadChannels() (map[string]*ChannelRow, error) {
	query := `
		SELECT id, name, display_name, group_title, logo_url, 
		       preferred_stream_index, active, last_seen, created_at, updated_at
		FROM channels
		WHERE active = 1
		ORDER BY name
	`

	rows, err := db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to load channels: %w", err)
	}
	defer rows.Close()

	channels := make(map[string]*ChannelRow)
	for rows.Next() {
		var ch ChannelRow
		err := rows.Scan(
			&ch.ID, &ch.Name, &ch.DisplayName, &ch.GroupTitle, &ch.LogoURL,
			&ch.PreferredStreamIndex, &ch.Active, &ch.LastSeen, &ch.CreatedAt, &ch.UpdatedAt,
		)
		if err != nil {
			continue
		}
		channels[ch.Name] = &ch
	}

	return channels, nil
}

// SaveChannel saves or updates a channel
func (db *DB) SaveChannel(name, displayName, groupTitle, logoURL string) (int64, error) {
	query := `
		INSERT INTO channels (name, display_name, group_title, logo_url, last_seen, updated_at)
		VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(name) DO UPDATE SET
			display_name = excluded.display_name,
			group_title = excluded.group_title,
			logo_url = excluded.logo_url,
			last_seen = CURRENT_TIMESTAMP,
			updated_at = CURRENT_TIMESTAMP
	`

	result, err := db.Exec(query, name, displayName, groupTitle, logoURL)
	if err != nil {
		return 0, fmt.Errorf("failed to save channel: %w", err)
	}

	// Try to get the ID
	id, err := result.LastInsertId()
	if err != nil || id == 0 {
		// If LastInsertId fails (conflict case), query for the ID
		err = db.QueryRow("SELECT id FROM channels WHERE name = ?", name).Scan(&id)
		if err != nil {
			return 0, fmt.Errorf("failed to get channel ID: %w", err)
		}
	}

	return id, nil
}

// GetChannelByName retrieves a channel by its name
func (db *DB) GetChannelByName(name string) (*ChannelRow, error) {
	query := `
		SELECT id, name, display_name, group_title, logo_url, 
		       preferred_stream_index, active, last_seen, created_at, updated_at
		FROM channels
		WHERE name = ? AND active = 1
	`

	var ch ChannelRow
	err := db.QueryRow(query, name).Scan(
		&ch.ID, &ch.Name, &ch.DisplayName, &ch.GroupTitle, &ch.LogoURL,
		&ch.PreferredStreamIndex, &ch.Active, &ch.LastSeen, &ch.CreatedAt, &ch.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get channel: %w", err)
	}

	return &ch, nil
}

// UpdateChannelPreferredStream updates the preferred stream index for a channel
func (db *DB) UpdateChannelPreferredStream(channelID int64, streamIndex int) error {
	_, err := db.Exec(
		"UPDATE channels SET preferred_stream_index = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?",
		streamIndex, channelID,
	)
	return err
}

// UpdateChannelPreferredStreamByName updates preferred stream by channel name
func (db *DB) UpdateChannelPreferredStreamByName(channelName string, streamIndex int) error {
	_, err := db.Exec(
		"UPDATE channels SET preferred_stream_index = ?, updated_at = CURRENT_TIMESTAMP WHERE name = ?",
		streamIndex, channelName,
	)
	return err
}

// CleanupStaleChannels marks channels as inactive if not seen recently
func (db *DB) CleanupStaleChannels(olderThan time.Duration) error {
	cutoff := time.Now().Add(-olderThan)
	_, err := db.Exec(
		"UPDATE channels SET active = 0, updated_at = CURRENT_TIMESTAMP WHERE last_seen < ? AND active = 1",
		cutoff,
	)
	return err
}

// GetChannelsByGroup retrieves all channels in a specific group
func (db *DB) GetChannelsByGroup(groupTitle string) ([]*ChannelRow, error) {
	query := `
		SELECT id, name, display_name, group_title, logo_url, 
		       preferred_stream_index, active, last_seen, created_at, updated_at
		FROM channels
		WHERE group_title = ? AND active = 1
		ORDER BY name
	`

	rows, err := db.Query(query, groupTitle)
	if err != nil {
		return nil, fmt.Errorf("failed to load channels by group: %w", err)
	}
	defer rows.Close()

	var channels []*ChannelRow
	for rows.Next() {
		var ch ChannelRow
		err := rows.Scan(
			&ch.ID, &ch.Name, &ch.DisplayName, &ch.GroupTitle, &ch.LogoURL,
			&ch.PreferredStreamIndex, &ch.Active, &ch.LastSeen, &ch.CreatedAt, &ch.UpdatedAt,
		)
		if err != nil {
			continue
		}
		channels = append(channels, &ch)
	}

	return channels, nil
}

// GetAllGroups retrieves all unique group titles
func (db *DB) GetAllGroups() ([]string, error) {
	rows, err := db.Query(`
		SELECT DISTINCT group_title 
		FROM channels 
		WHERE active = 1 AND group_title != ''
		ORDER BY group_title
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to load groups: %w", err)
	}
	defer rows.Close()

	var groups []string
	for rows.Next() {
		var group string
		if err := rows.Scan(&group); err != nil {
			continue
		}
		groups = append(groups, group)
	}

	return groups, nil
}

// GetChannelStats returns statistics about channels
func (db *DB) GetChannelStats() (map[string]interface{}, error) {
	stats := make(map[string]interface{})

	// Total channels
	var total int
	err := db.QueryRow("SELECT COUNT(*) FROM channels WHERE active = 1").Scan(&total)
	if err != nil {
		return nil, err
	}
	stats["total_channels"] = total

	// Channels by group
	rows, err := db.Query(`
		SELECT group_title, COUNT(*) as count
		FROM channels
		WHERE active = 1
		GROUP BY group_title
		ORDER BY count DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	groupCounts := make(map[string]int)
	for rows.Next() {
		var group string
		var count int
		if err := rows.Scan(&group, &count); err != nil {
			continue
		}
		groupCounts[group] = count
	}
	stats["channels_by_group"] = groupCounts

	return stats, nil
}
