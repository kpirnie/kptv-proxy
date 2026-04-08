// work/streamorder/streamorder.go
package streamorder

import (
	"kptv-proxy/work/db"
	"kptv-proxy/work/logger"
)

// StreamOrderEntry pairs a positional index with the stream URL hash.
type StreamOrderEntry struct {
	Index int    `json:"index"`
	Hash  string `json:"hash"`
}

// GetChannelStreamOrder returns custom ordering entries for a channel from
// the persistent overrides table. Returns nil if no overrides exist.
func GetChannelStreamOrder(channelName string) ([]StreamOrderEntry, error) {
	overrides, err := db.GetChannelOverrides(channelName)
	if err != nil {
		logger.Error("{streamorder - GetChannelStreamOrder} channel=%s: %v", channelName, err)
		return nil, err
	}
	if len(overrides) == 0 {
		return nil, nil
	}

	// Only return entries that have a custom order set (s_order >= 0).
	entries := make([]StreamOrderEntry, 0, len(overrides))
	for hash, o := range overrides {
		if o.SOrder >= 0 {
			entries = append(entries, StreamOrderEntry{Index: o.SOrder, Hash: hash})
		}
	}
	if len(entries) == 0 {
		return nil, nil
	}
	return entries, nil
}

// SetChannelStreamOrder persists a new stream ordering via the overrides table.
func SetChannelStreamOrder(channelName string, order []StreamOrderEntry) error {
	for _, entry := range order {
		if err := db.SetStreamOrder(channelName, entry.Hash, entry.Index); err != nil {
			logger.Error("{streamorder - SetChannelStreamOrder} channel=%s hash=%s: %v", channelName, entry.Hash, err)
			return err
		}
	}
	logger.Debug("{streamorder - SetChannelStreamOrder} channel=%s updated (%d entries)", channelName, len(order))
	return nil
}

// DeleteChannelStreamOrder removes all order overrides for a channel.
func DeleteChannelStreamOrder(channelName string) error {
	overrides, err := db.GetChannelOverrides(channelName)
	if err != nil {
		return err
	}
	for hash := range overrides {
		if err := db.DeleteStreamOverride(channelName, hash); err != nil {
			logger.Error("{streamorder - DeleteChannelStreamOrder} channel=%s hash=%s: %v", channelName, hash, err)
			return err
		}
	}
	logger.Debug("{streamorder - DeleteChannelStreamOrder} channel=%s overrides cleared", channelName)
	return nil
}
