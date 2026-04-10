package admin

import (
	"encoding/json"
	"fmt"
	"kptv-proxy/work/config"
	"kptv-proxy/work/db"
	"kptv-proxy/work/proxy"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
)

// handleGetSDAccounts returns all configured Schedules Direct accounts.
func handleGetSDAccounts(sp *proxy.StreamProxy) http.HandlerFunc {
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
			Password        string   `json:"password"`
			Enabled         bool     `json:"enabled"`
			DaysToFetch     int      `json:"daysToFetch"`
			SelectedLineups []string `json:"selectedLineups"`
		}

		out := make([]sdAccountOut, len(accounts))
		for i, a := range accounts {
			out[i] = sdAccountOut{
				ID:              a.ID,
				Name:            a.Name,
				Username:        a.Username,
				Password:        a.Password,
				Enabled:         a.Enabled,
				DaysToFetch:     a.DaysToFetch,
				SelectedLineups: a.Lineups,
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
			Password:    incoming.Password,
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
		json.NewEncoder(w).Encode(map[string]interface{}{"status": "success", "id": id})
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

		if incoming.Name == "" || incoming.Username == "" || incoming.Password == "" {
			http.Error(w, "Name, username, and password are required", http.StatusBadRequest)
			return
		}

		if incoming.DaysToFetch <= 0 {
			incoming.DaysToFetch = 7
		}

		if err := db.UpdateSDAccount(db.SDAccount{
			ID:          id,
			Name:        incoming.Name,
			Username:    incoming.Username,
			Password:    incoming.Password,
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
