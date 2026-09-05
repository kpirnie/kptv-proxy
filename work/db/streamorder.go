// work/db/streamorder.go
package db

import (
	"kptv-proxy/work/logger"
)

// GetChannelOrder returns the custom stream ordering for a channel as a map of
// URL hash to position. An empty map means the channel has no custom order.
func GetChannelOrder(channelName string) (map[string]int, error) {
	rows, err := GetReader().Query(`
		SELECT s_hash, s_order
		FROM kp_stream_order
		WHERE channel = ?
		ORDER BY s_order`, channelName)
	if err != nil {
		logger.Error("{db/streamorder - GetChannelOrder} channel=%s: %v", channelName, err)
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]int)
	for rows.Next() {
		var hash string
		var order int
		if err := rows.Scan(&hash, &order); err != nil {
			logger.Error("{db/streamorder - GetChannelOrder} scan channel=%s: %v", channelName, err)
			return nil, err
		}
		result[hash] = order
	}
	return result, rows.Err()
}

// GetAllChannelOrders returns every custom stream ordering grouped by channel
// name, each inner map keyed by URL hash with the stored position as the value.
func GetAllChannelOrders() (map[string]map[string]int, error) {
	rows, err := GetReader().Query(`SELECT channel, s_hash, s_order FROM kp_stream_order`)
	if err != nil {
		logger.Error("{db/streamorder - GetAllChannelOrders} %v", err)
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]map[string]int)
	for rows.Next() {
		var channel, hash string
		var order int
		if err := rows.Scan(&channel, &hash, &order); err != nil {
			logger.Error("{db/streamorder - GetAllChannelOrders} scan: %v", err)
			return nil, err
		}
		if result[channel] == nil {
			result[channel] = make(map[string]int)
		}
		result[channel][hash] = order
	}
	return result, rows.Err()
}

// SetChannelOrder replaces the stored ordering for a channel with the supplied
// hashes, which must arrive in the desired display order. Rows for hashes absent
// from the slice are removed, so membership and ordering stay reconciled rather
// than accumulating stale entries. Empty and repeated hashes are skipped.
func SetChannelOrder(channelName string, hashes []string) error {
	tx, err := Get().Begin()
	if err != nil {
		logger.Error("{db/streamorder - SetChannelOrder} begin channel=%s: %v", channelName, err)
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM kp_stream_order WHERE channel = ?`, channelName); err != nil {
		logger.Error("{db/streamorder - SetChannelOrder} clear channel=%s: %v", channelName, err)
		return err
	}

	stmt, err := tx.Prepare(`INSERT INTO kp_stream_order (channel, s_hash, s_order) VALUES (?, ?, ?)`)
	if err != nil {
		logger.Error("{db/streamorder - SetChannelOrder} prepare channel=%s: %v", channelName, err)
		return err
	}
	defer stmt.Close()

	seen := make(map[string]bool, len(hashes))
	order := 0
	for _, hash := range hashes {
		if hash == "" || seen[hash] {
			continue
		}
		seen[hash] = true
		if _, err := stmt.Exec(channelName, hash, order); err != nil {
			logger.Error("{db/streamorder - SetChannelOrder} channel=%s hash=%s: %v", channelName, hash, err)
			return err
		}
		order++
	}

	if err := tx.Commit(); err != nil {
		logger.Error("{db/streamorder - SetChannelOrder} commit channel=%s: %v", channelName, err)
		return err
	}
	return nil
}

// ClearChannelOrder removes any custom ordering for a channel, returning it to
// the globally configured sort.
func ClearChannelOrder(channelName string) error {
	_, err := Get().Exec(`DELETE FROM kp_stream_order WHERE channel = ?`, channelName)
	if err != nil {
		logger.Error("{db/streamorder - ClearChannelOrder} channel=%s: %v", channelName, err)
	}
	return err
}
