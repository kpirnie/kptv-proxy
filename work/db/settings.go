// work/db/settings.go
package db

import "kptv-proxy/work/logger"

// GetSetting retrieves a single value by key from kp_settings.
// Returns an empty string and false if the key does not exist.
func GetSetting(key string) (string, bool) {
	var value string
	err := Get().QueryRow(
		`SELECT the_value FROM kp_settings WHERE the_key = ?`, key,
	).Scan(&value)
	if err != nil {
		logger.Debug("{db/settings - GetSetting} key=%s not found: %v", key, err)
		return "", false
	}
	return value, true
}

// SetSetting inserts or replaces a key/value pair in kp_settings.
func SetSetting(key, value string) error {
	_, err := Get().Exec(
		`INSERT INTO kp_settings (the_key, the_value) VALUES (?, ?)
		 ON CONFLICT(the_key) DO UPDATE SET the_value = excluded.the_value`,
		key, value,
	)
	if err != nil {
		logger.Error("{db/settings - SetSetting} key=%s: %v", key, err)
	}
	return err
}

// DeleteSetting removes a key from kp_settings. No-op if the key does not exist.
func DeleteSetting(key string) error {
	_, err := Get().Exec(`DELETE FROM kp_settings WHERE the_key = ?`, key)
	if err != nil {
		logger.Error("{db/settings - DeleteSetting} key=%s: %v", key, err)
	}
	return err
}

// AllSettings returns every key/value pair in kp_settings as a map.
func AllSettings() (map[string]string, error) {
	rows, err := Get().Query(`SELECT the_key, the_value FROM kp_settings`)
	if err != nil {
		logger.Error("{db/settings - AllSettings} query failed: %v", err)
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			logger.Error("{db/settings - AllSettings} scan failed: %v", err)
			return nil, err
		}
		result[k] = v
	}
	return result, rows.Err()
}
