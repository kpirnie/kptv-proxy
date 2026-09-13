// work/db/logos.go
package db

import (
	"time"

	"kptv-proxy/work/logger"
)

// ChannelLogo holds the resolved logo assignment for a proxy channel. Kind is
// one of "override" (manual URL), "upload" (content-hashed local file), or
// "epg" (icon pulled from the mapped EPG channel); Value carries the URL or
// the on-disk hash depending on kind.
type ChannelLogo struct {
	ID        int64
	Channel   string
	Kind      string
	Value     string
	UpdatedAt int64
}

// GetChannelLogo returns the stored logo assignment for a channel name, or a
// zero-value struct with ok=false when the channel has none.
func GetChannelLogo(channel string) (ChannelLogo, bool) {
	row := GetReader().QueryRow(
		`SELECT id, channel, kind, value, updated_at FROM kp_channel_logo WHERE channel = ?`,
		channel,
	)

	var l ChannelLogo
	if err := row.Scan(&l.ID, &l.Channel, &l.Kind, &l.Value, &l.UpdatedAt); err != nil {
		return ChannelLogo{}, false
	}
	return l, true
}

// GetAllChannelLogos returns a map of channel name -> logo assignment for every
// channel that has one, for resolver use on the export paths.
func GetAllChannelLogos() (map[string]ChannelLogo, error) {
	rows, err := GetReader().Query(`SELECT id, channel, kind, value, updated_at FROM kp_channel_logo WHERE value != ''`)
	if err != nil {
		logger.Error("{db/logos - GetAllChannelLogos} Failed to query channel logos: %v", err)
		return nil, err
	}
	defer rows.Close()

	m := make(map[string]ChannelLogo)
	for rows.Next() {
		var l ChannelLogo
		if err := rows.Scan(&l.ID, &l.Channel, &l.Kind, &l.Value, &l.UpdatedAt); err != nil {
			continue
		}
		m[l.Channel] = l
	}
	if err := rows.Err(); err != nil {
		logger.Error("{db/logos - GetAllChannelLogos} Failed to iterate channel logos: %v", err)
		return nil, err
	}
	return m, nil
}

// UpsertChannelLogo inserts or replaces the logo assignment for a channel.
func UpsertChannelLogo(channel, kind, value string) error {
	_, err := Get().Exec(
		`INSERT INTO kp_channel_logo (channel, kind, value, updated_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(channel) DO UPDATE SET kind = excluded.kind, value = excluded.value, updated_at = excluded.updated_at`,
		channel, kind, value, time.Now().Unix(),
	)
	if err != nil {
		logger.Error("{db/logos - UpsertChannelLogo} Failed to upsert channel logo for %s: %v", channel, err)
	}
	return err
}

// DeleteChannelLogo removes the logo assignment for a channel, dropping it back
// to provider/default resolution.
func DeleteChannelLogo(channel string) error {
	_, err := Get().Exec(
		`DELETE FROM kp_channel_logo WHERE channel = ?`,
		channel,
	)
	if err != nil {
		logger.Error("{db/logos - DeleteChannelLogo} Failed to delete channel logo for %s: %v", channel, err)
	}
	return err
}

// UpsertLogoURL records the source URL behind a cache hash so the serving path
// can fetch it lazily on a cold cache, including after a restart.
func UpsertLogoURL(hash, url string) error {
	_, err := Get().Exec(
		`INSERT INTO kp_logo_url (hash, url)
		 VALUES (?, ?)
		 ON CONFLICT(hash) DO UPDATE SET url = excluded.url`,
		hash, url,
	)
	if err != nil {
		logger.Error("{db/logos - UpsertLogoURL} Failed to upsert logo url for %s: %v", hash, err)
	}
	return err
}

// GetLogoURL returns the source URL recorded for a cache hash.
func GetLogoURL(hash string) (string, bool) {
	row := GetReader().QueryRow(`SELECT url FROM kp_logo_url WHERE hash = ?`, hash)

	var url string
	if err := row.Scan(&url); err != nil {
		return "", false
	}
	return url, true
}

// AllLogoURLs returns every recorded hash -> source URL pair, for the
// post-import cache warm pass.
func AllLogoURLs() (map[string]string, error) {
	rows, err := GetReader().Query(`SELECT hash, url FROM kp_logo_url`)
	if err != nil {
		logger.Error("{db/logos - AllLogoURLs} Failed to query logo urls: %v", err)
		return nil, err
	}
	defer rows.Close()

	m := make(map[string]string)
	for rows.Next() {
		var hash, url string
		if err := rows.Scan(&hash, &url); err != nil {
			continue
		}
		m[hash] = url
	}
	if err := rows.Err(); err != nil {
		logger.Error("{db/logos - AllLogoURLs} Failed to iterate logo urls: %v", err)
		return nil, err
	}
	return m, nil
}
