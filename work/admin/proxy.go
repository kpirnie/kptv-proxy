package admin

import (
	"encoding/json"
	"fmt"
	"kptv-proxy/work/config"
	"kptv-proxy/work/logger"
	"os"
)

// watcherManager defines the watcher lifecycle methods required by admin handlers.
type watcherManager interface {
	Start()
	Stop()
}

// filterManager defines the filter management methods required by admin handlers.
type filterManager interface {
	ClearFilters()
}

// channelMap defines the channel map methods required by admin handlers.
type channelMap interface {
	Range(f func(key string, value interface{}) bool)
	Load(key string) (interface{}, bool)
}

// persistWatcherConfig reads the config file from disk, updates the watcher
// enabled flag, and writes it back atomically using a temp file rename.
func persistWatcherConfig(enabled bool) error {
	configPath := "/settings/config.json"

	data, err := os.ReadFile(configPath)
	if err != nil {
		logger.Error("{admin/proxy - persistWatcherConfig} Failed to read config: %v", err)
		return err
	}

	var configFile config.ConfigFile
	if err := json.Unmarshal(data, &configFile); err != nil {
		logger.Error("{admin/proxy - persistWatcherConfig} Failed to parse config: %v", err)
		return err
	}

	configFile.WatcherEnabled = enabled

	newData, err := json.MarshalIndent(configFile, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	tempPath := configPath + ".tmp"
	if err := os.WriteFile(tempPath, newData, 0644); err != nil {
		return fmt.Errorf("failed to write temp config: %w", err)
	}

	return os.Rename(tempPath, configPath)
}
