package admin

import (
	"encoding/json"
	"kptv-proxy/work/logger"
	"kptv-proxy/work/proxy"
	"kptv-proxy/work/schedulesdirect"
	"net/http"
)

// handleSDDiscover authenticates to Schedules Direct and returns the available
// lineups for the provided credentials. Nothing is saved to config at this stage,
// allowing the admin UI to present lineup options before committing to storage.
func handleSDDiscover(_ *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		var request struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}

		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		if request.Username == "" || request.Password == "" {
			http.Error(w, "Username and password are required", http.StatusBadRequest)
			return
		}

		logger.Debug("{admin/schedulesdirect - handleSDDiscover} Discovering lineups for SD user: %s", request.Username)

		lineups, err := schedulesdirect.DiscoverLineups(request.Username, request.Password)
		if err != nil {
			logger.Error("{admin/schedulesdirect - handleSDDiscover} Discovery failed for %s: %v", request.Username, err)
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"error":   err.Error(),
			})
			return
		}

		logger.Debug("{admin/schedulesdirect - handleSDDiscover} Found %d lineups for %s", len(lineups), request.Username)

		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"lineups": lineups,
		})
	}
}
