package admin

import (
	"encoding/json"
	"fmt"
	"kptv-proxy/work/proxy"
	"net/http"
	"time"
)

// restartChan provides a signaling mechanism for graceful application restart
// operations initiated through the admin interface, enabling coordinated shutdown
// and restart sequences without abrupt process termination.
var restartChan = make(chan bool, 1)

// GetRestartChan exposes the restart channel for consumption by main.go,
// allowing the admin package to signal restarts without direct coupling.
func GetRestartChan() chan bool {
	return restartChan
}

// handleRestart initiates a graceful application restart through coordination
// channel signaling, allowing the main application to handle the restart sequence
// cleanly without abrupt termination.
func handleRestart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	addLogEntry("info", "Restart requested via admin interface - triggering graceful restart")

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "restart_initiated",
		"message": "Restarting KPTV Proxy process...",
	})

	// Trigger restart signal after brief delay to allow response to flush
	go func() {
		time.Sleep(500 * time.Millisecond)
		restartChan <- true
	}()
}

// handleToggleWatcher processes requests to enable or disable the stream watcher system,
// persisting the configuration change to disk and starting or stopping the watcher
// infrastructure immediately without requiring application restart.
func handleToggleWatcher(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		var request struct {
			Enabled bool `json:"enabled"`
		}

		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		if err := persistWatcherConfig(request.Enabled); err != nil {
			addLogEntry("error", fmt.Sprintf("Failed to persist watcher config: %v", err))
			http.Error(w, "Failed to update config file", http.StatusInternalServerError)
			return
		}

		sp.Config.WatcherEnabled = request.Enabled

		if request.Enabled {
			sp.WatcherManager.Start()
			addLogEntry("info", "Stream watcher enabled via admin interface")
		} else {
			sp.WatcherManager.Stop()
			addLogEntry("info", "Stream watcher disabled via admin interface")
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":         "success",
			"watcherEnabled": request.Enabled,
		})
	}
}

// handleAdminInterface serves the main admin HTML page.
func handleAdminInterface(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "/static/admin.html")
}
