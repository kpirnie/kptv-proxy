package database

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

// SaveStreamOrder saves the custom stream order for a channel
func (db *DB) SaveStreamOrder(channelID int64, streamOrder []int) error {
	orderJSON, err := json.Marshal(streamOrder)
	if err != nil {
		return fmt.Errorf("failed to marshal stream order: %w", err)
	}

	query := `
		INSERT INTO stream_orders (channel_id, stream_order, updated_at)
		VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(channel_id) DO UPDATE SET
			stream_order = excluded.stream_order,
			updated_at = CURRENT_TIMESTAMP
	`

	_, err = db.Exec(query, channelID, string(orderJSON))
	if err != nil {
		return fmt.Errorf("failed to save stream order: %w", err)
	}

	return nil
}

// LoadStreamOrder loads the custom stream order for a channel
func (db *DB) LoadStreamOrder(channelID int64) ([]int, error) {
	var orderJSON string
	err := db.QueryRow("SELECT stream_order FROM stream_orders WHERE channel_id = ?", channelID).Scan(&orderJSON)
	
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to load stream order: %w", err)
	}

	var order []int
	if err := json.Unmarshal([]byte(orderJSON), &order); err != nil {
		return nil, fmt.Errorf("failed to unmarshal stream order: %w", err)
	}

	return order, nil
}

// LoadStreamOrderByName loads the custom stream order for a channel by name
func (db *DB) LoadStreamOrderByName(channelName string) ([]int, error) {
	var orderJSON string
	err := db.QueryRow(`
		SELECT so.stream_order 
		FROM stream_orders so
		JOIN channels c ON c.id = so.channel_id
		WHERE c.name = ?
	`, channelName).Scan(&orderJSON)
	
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to load stream order: %w", err)
	}

	var order []int
	if err := json.Unmarshal([]byte(orderJSON), &order); err != nil {
		return nil, fmt.Errorf("failed to unmarshal stream order: %w", err)
	}

	return order, nil
}

// DeleteStreamOrder removes the custom stream order for a channel
func (db *DB) DeleteStreamOrder(channelID int64) error {
	_, err := db.Exec("DELETE FROM stream_orders WHERE channel_id = ?", channelID)
	return err
}

// DeleteStreamOrderByName removes the custom stream order for a channel by name
func (db *DB) DeleteStreamOrderByName(channelName string) error {
	_, err := db.Exec(`
		DELETE FROM stream_orders 
		WHERE channel_id = (SELECT id FROM channels WHERE name = ?)
	`, channelName)
	return err
}
