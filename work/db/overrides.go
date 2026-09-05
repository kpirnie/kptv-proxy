// work/db/overrides.go
package db

import (
	"database/sql"
	"kptv-proxy/work/logger"
)

// StreamOverride holds persistent state for a single stream identified by
// its URL hash. Only dead status and custom sort order are stored.
type StreamOverride struct {
	Hash       string
	SStatus    int
	SOrder     int
	DeadReason string
}

// OverrideKey builds the composite map key used by the in-memory override
// snapshot. The NUL separator cannot appear in a channel name or a hash.
func OverrideKey(channelName, hash string) string {
	return channelName + "\x00" + hash
}

// GetAllStreamOverrides returns every persisted override keyed by OverrideKey,
// for callers that check many streams in a single pass.
func GetAllStreamOverrides() (map[string]StreamOverride, error) {
	rows, err := GetReader().Query(`
		SELECT channel, s_hash, s_status, s_order, dead_reason
		FROM kp_stream_overrides`)
	if err != nil {
		logger.Error("{db/overrides - GetAllStreamOverrides} query: %v", err)
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]StreamOverride)
	for rows.Next() {
		var channel string
		var o StreamOverride
		if err := rows.Scan(&channel, &o.Hash, &o.SStatus, &o.SOrder, &o.DeadReason); err != nil {
			logger.Error("{db/overrides - GetAllStreamOverrides} scan: %v", err)
			return nil, err
		}
		out[OverrideKey(channel, o.Hash)] = o
	}
	return out, rows.Err()
}

// GetStreamOverride returns the persisted override for a channel/hash pair.
// Returns false if no row exists.
func GetStreamOverride(channelName, hash string) (StreamOverride, bool) {
	row := GetReader().QueryRow(`
		SELECT s_status, s_order, dead_reason
		FROM kp_stream_overrides
		WHERE channel = ? AND s_hash = ?`, channelName, hash)

	var o StreamOverride
	o.Hash = hash
	if err := row.Scan(&o.SStatus, &o.SOrder, &o.DeadReason); err != nil {
		if err != sql.ErrNoRows {
			logger.Error("{db/overrides - GetStreamOverride} channel=%s hash=%s: %v", channelName, hash, err)
		}
		return StreamOverride{Hash: hash, SStatus: 0, SOrder: -1}, false
	}
	return o, true
}

// SetStreamDead upserts a dead status for a stream.
func SetStreamDead(channelName, hash, reason string, status int) error {
	_, err := Get().Exec(`
		INSERT INTO kp_stream_overrides (channel, s_hash, s_status, dead_reason)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(channel, s_hash) DO UPDATE SET
			s_status    = excluded.s_status,
			dead_reason = excluded.dead_reason`,
		channelName, hash, status, reason)
	if err != nil {
		logger.Error("{db/overrides - SetStreamDead} channel=%s hash=%s: %v", channelName, hash, err)
	}
	return err
}

// SetStreamAlive clears dead status for a stream. Removes the row entirely
// if no custom order is set, keeping the table sparse.
func SetStreamAlive(channelName, hash string) error {
	_, err := Get().Exec(`
		DELETE FROM kp_stream_overrides
		WHERE channel = ? AND s_hash = ? AND s_order = -1`,
		channelName, hash)
	if err != nil {
		logger.Error("{db/overrides - SetStreamAlive} channel=%s hash=%s: %v", channelName, hash, err)
		return err
	}
	// If a custom order exists, just clear the dead status.
	_, err = Get().Exec(`
		UPDATE kp_stream_overrides SET s_status=0, dead_reason=''
		WHERE channel = ? AND s_hash = ?`,
		channelName, hash)
	if err != nil {
		logger.Error("{db/overrides - SetStreamAlive} update channel=%s hash=%s: %v", channelName, hash, err)
	}
	return err
}
