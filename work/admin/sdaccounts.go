package admin

import (
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"kptv-proxy/work/config"
	"kptv-proxy/work/db"
	"kptv-proxy/work/proxy"
	"net/http"
	"regexp"
	"strconv"

	"github.com/gorilla/mux"
)

// sdSHA1Pattern matches a stored Schedules Direct password that is already in
// the sha1-hex form the SD API expects.
var sdSHA1Pattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// hashSDPassword reduces a Schedules Direct password to the sha1 hex digest the
// SD token endpoint expects, which is the only form the account ever needs.
func hashSDPassword(password string) string {
	return fmt.Sprintf("%x", sha1.Sum([]byte(password)))
}

// handleGetSDAccounts returns all configured Schedules Direct accounts.
func handleGetSDAccounts(_ *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		accounts, err := db.GetAllSDAccountsWithLineups()
		if err != nil {
			addLogEntry("error", fmt.Sprintf("Failed to get SD accounts: %v", err))
			http.Error(w, "Failed to get SD accounts", http.StatusInternalServerError)
			return
		}

		type sdAccountOut struct {
			ID              int64    `json:"id"`
			Name            string   `json:"name"`
			Username        string   `json:"username"`
			Enabled         bool     `json:"enabled"`
			DaysToFetch     int      `json:"daysToFetch"`
			SelectedLineups []string `json:"selectedLineups"`
			Legacy          bool     `json:"legacy"`
		}

		out := make([]sdAccountOut, len(accounts))
		for i, a := range accounts {
			out[i] = sdAccountOut{
				ID:              a.ID,
				Name:            a.Name,
				Username:        a.Username,
				Enabled:         a.Enabled,
				DaysToFetch:     a.DaysToFetch,
				SelectedLineups: a.Lineups,
				Legacy:          !sdSHA1Pattern.MatchString(a.Password),
			}
		}

		json.NewEncoder(w).Encode(out)
	}
}

// handleCreateSDAccount creates a new Schedules Direct account.
func handleCreateSDAccount(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		var incoming config.SDAccount
		if err := json.NewDecoder(r.Body).Decode(&incoming); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		if incoming.Name == "" || incoming.Username == "" || incoming.Password == "" {
			http.Error(w, "Name, username, and password are required", http.StatusBadRequest)
			return
		}

		if incoming.DaysToFetch <= 0 {
			incoming.DaysToFetch = 7
		}

		id, err := db.InsertSDAccount(db.SDAccount{
			Name:        incoming.Name,
			Username:    incoming.Username,
			Password:    hashSDPassword(incoming.Password),
			Enabled:     incoming.Enabled,
			DaysToFetch: incoming.DaysToFetch,
			Lineups:     incoming.SelectedLineups,
		})
		if err != nil {
			addLogEntry("error", fmt.Sprintf("Failed to create SD account: %v", err))
			http.Error(w, "Failed to create SD account", http.StatusInternalServerError)
			return
		}

		reloadSDAccounts(sp)
		addLogEntry("info", fmt.Sprintf("SD account created: %s", incoming.Name))

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{"status": "success", "id": id})
	}
}

// handleUpdateSDAccount updates an existing Schedules Direct account.
func handleUpdateSDAccount(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		vars := mux.Vars(r)
		id, err := strconv.ParseInt(vars["id"], 10, 64)
		if err != nil {
			http.Error(w, "Invalid ID", http.StatusBadRequest)
			return
		}

		var incoming config.SDAccount
		if err := json.NewDecoder(r.Body).Decode(&incoming); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		if incoming.Name == "" || incoming.Username == "" {
			http.Error(w, "Name and username are required", http.StatusBadRequest)
			return
		}

		// A blank password on update keeps the stored digest
		stored := incoming.Password
		if stored == "" {
			existing, err := db.GetSDAccountWithLineups(id)
			if err != nil {
				http.Error(w, "Password is required", http.StatusBadRequest)
				return
			}
			stored = existing.Password
		} else {
			stored = hashSDPassword(stored)
		}

		if incoming.DaysToFetch <= 0 {
			incoming.DaysToFetch = 7
		}

		if err := db.UpdateSDAccount(db.SDAccount{
			ID:          id,
			Name:        incoming.Name,
			Username:    incoming.Username,
			Password:    stored,
			Enabled:     incoming.Enabled,
			DaysToFetch: incoming.DaysToFetch,
			Lineups:     incoming.SelectedLineups,
		}); err != nil {
			addLogEntry("error", fmt.Sprintf("Failed to update SD account: %v", err))
			http.Error(w, "Failed to update SD account", http.StatusInternalServerError)
			return
		}

		reloadSDAccounts(sp)
		addLogEntry("info", fmt.Sprintf("SD account updated: %s", incoming.Name))

		json.NewEncoder(w).Encode(map[string]string{"status": "success"})
	}
}

// handleDeleteSDAccount deletes a Schedules Direct account by ID.
func handleDeleteSDAccount(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		vars := mux.Vars(r)
		id, err := strconv.ParseInt(vars["id"], 10, 64)
		if err != nil {
			http.Error(w, "Invalid ID", http.StatusBadRequest)
			return
		}

		if err := db.DeleteSDAccount(id); err != nil {
			addLogEntry("error", fmt.Sprintf("Failed to delete SD account: %v", err))
			http.Error(w, "Failed to delete SD account", http.StatusInternalServerError)
			return
		}

		reloadSDAccounts(sp)
		addLogEntry("info", fmt.Sprintf("SD account deleted: %d", id))

		json.NewEncoder(w).Encode(map[string]string{"status": "success"})
	}
}

// reloadSDAccounts refreshes the in-memory SD accounts from the database.
func reloadSDAccounts(sp *proxy.StreamProxy) {
	accounts, err := db.GetAllSDAccountsWithLineups()
	if err != nil {
		addLogEntry("error", fmt.Sprintf("Failed to reload SD accounts: %v", err))
		return
	}

	sp.Config.SDAccounts = make([]config.SDAccount, len(accounts))
	for i, a := range accounts {
		sp.Config.SDAccounts[i] = config.SDAccount{
			Name:            a.Name,
			Username:        a.Username,
			Password:        a.Password,
			Enabled:         a.Enabled,
			DaysToFetch:     a.DaysToFetch,
			SelectedLineups: a.Lineups,
		}
	}
}
