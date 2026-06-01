package db

import "kptv-proxy/work/logger"

// ChannelEPG holds the manual EPG channel mapping for a proxy channel.
type ChannelEPG struct {
	ID      int64
	Channel string
	EPGID   string
	EPGName string
}

// GetChannelEPG returns the EPG mapping for a given channel name, or a
// zero-value struct with ok=false if no mapping exists.
func GetChannelEPG(channel string) (ChannelEPG, bool) {
	row := Get().QueryRow(
		`SELECT id, channel, epg_id, epg_name FROM kp_channel_epg WHERE channel = ?`,
		channel,
	)

	var e ChannelEPG
	if err := row.Scan(&e.ID, &e.Channel, &e.EPGID, &e.EPGName); err != nil {
		return ChannelEPG{}, false
	}
	return e, true
}

// UpsertChannelEPG inserts or replaces the EPG mapping for a channel.
func UpsertChannelEPG(channel, epgID, epgName string) error {
	_, err := Get().Exec(
		`INSERT INTO kp_channel_epg (channel, epg_id, epg_name)
		 VALUES (?, ?, ?)
		 ON CONFLICT(channel) DO UPDATE SET epg_id = excluded.epg_id, epg_name = excluded.epg_name`,
		channel, epgID, epgName,
	)
	if err != nil {
		logger.Error("{db/channelepg - UpsertChannelEPG} Failed to upsert channel EPG mapping for %s: %v", channel, err)
	}
	return err
}

// DeleteChannelEPG removes the EPG mapping for a channel.
func DeleteChannelEPG(channel string) error {
	_, err := Get().Exec(
		`DELETE FROM kp_channel_epg WHERE channel = ?`,
		channel,
	)
	if err != nil {
		logger.Error("{db/channelepg - DeleteChannelEPG} Failed to delete channel EPG mapping for %s: %v", channel, err)
	}
	return err
}
