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

// handleGetXCAccounts returns all XC output accounts.
func handleGetXCAccounts(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		accounts, err := db.GetAllXCAccounts()
		if err != nil {
			addLogEntry("error", fmt.Sprintf("Failed to get XC accounts: %v", err))
			http.Error(w, "Failed to get XC accounts", http.StatusInternalServerError)
			return
		}

		type xcAccountOut struct {
			ID             int64  `json:"id"`
			Name           string `json:"name"`
			Username       string `json:"username"`
			Password       string `json:"password"`
			MaxConnections int    `json:"maxConnections"`
			EnableLive     bool   `json:"enableLive"`
			EnableSeries   bool   `json:"enableSeries"`
			EnableVOD      bool   `json:"enableVOD"`
		}

		out := make([]xcAccountOut, len(accounts))
		for i, a := range accounts {
			out[i] = xcAccountOut{
				ID:             a.ID,
				Name:           a.Name,
				Username:       a.Username,
				Password:       a.Password,
				MaxConnections: a.MaxCnx,
				EnableLive:     a.EnableLive,
				EnableSeries:   a.EnableSeries,
				EnableVOD:      a.EnableVOD,
			}
		}

		json.NewEncoder(w).Encode(out)
	}
}

// handleCreateXCAccount creates a new XC output account.
func handleCreateXCAccount(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		var incoming config.XCOutputAccount
		if err := json.NewDecoder(r.Body).Decode(&incoming); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		if incoming.Name == "" || incoming.Username == "" || incoming.Password == "" {
			http.Error(w, "Name, username, and password are required", http.StatusBadRequest)
			return
		}

		if incoming.MaxConnections <= 0 {
			incoming.MaxConnections = 10
		}

		id, err := db.InsertXCAccount(db.XCAccount{
			Name:         incoming.Name,
			Username:     incoming.Username,
			Password:     incoming.Password,
			MaxCnx:       incoming.MaxConnections,
			EnableLive:   incoming.EnableLive,
			EnableSeries: incoming.EnableSeries,
			EnableVOD:    incoming.EnableVOD,
		})
		if err != nil {
			addLogEntry("error", fmt.Sprintf("Failed to create XC account: %v", err))
			http.Error(w, "Failed to create XC account", http.StatusInternalServerError)
			return
		}

		reloadXCAccounts(sp)
		addLogEntry("info", fmt.Sprintf("XC account created: %s", incoming.Name))

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{"status": "success", "id": id})
	}
}

// handleUpdateXCAccount updates an existing XC output account.
func handleUpdateXCAccount(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		vars := mux.Vars(r)
		id, err := strconv.ParseInt(vars["id"], 10, 64)
		if err != nil {
			http.Error(w, "Invalid ID", http.StatusBadRequest)
			return
		}

		var incoming config.XCOutputAccount
		if err := json.NewDecoder(r.Body).Decode(&incoming); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		if incoming.MaxConnections <= 0 {
			incoming.MaxConnections = 10
		}

		if err := db.UpdateXCAccount(db.XCAccount{
			ID:           id,
			Name:         incoming.Name,
			Username:     incoming.Username,
			Password:     incoming.Password,
			MaxCnx:       incoming.MaxConnections,
			EnableLive:   incoming.EnableLive,
			EnableSeries: incoming.EnableSeries,
			EnableVOD:    incoming.EnableVOD,
		}); err != nil {
			addLogEntry("error", fmt.Sprintf("Failed to update XC account: %v", err))
			http.Error(w, "Failed to update XC account", http.StatusInternalServerError)
			return
		}

		reloadXCAccounts(sp)
		addLogEntry("info", fmt.Sprintf("XC account updated: %s", incoming.Name))

		json.NewEncoder(w).Encode(map[string]string{"status": "success"})
	}
}

// handleDeleteXCAccount deletes an XC output account by ID.
func handleDeleteXCAccount(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		vars := mux.Vars(r)
		id, err := strconv.ParseInt(vars["id"], 10, 64)
		if err != nil {
			http.Error(w, "Invalid ID", http.StatusBadRequest)
			return
		}

		if err := db.DeleteXCAccount(id); err != nil {
			addLogEntry("error", fmt.Sprintf("Failed to delete XC account: %v", err))
			http.Error(w, "Failed to delete XC account", http.StatusInternalServerError)
			return
		}

		reloadXCAccounts(sp)
		addLogEntry("info", fmt.Sprintf("XC account deleted: %d", id))

		json.NewEncoder(w).Encode(map[string]string{"status": "success"})
	}
}

// reloadXCAccounts refreshes the in-memory XC accounts from the database.
func reloadXCAccounts(sp *proxy.StreamProxy) {
	accounts, err := db.GetAllXCAccounts()
	if err != nil {
		addLogEntry("error", fmt.Sprintf("Failed to reload XC accounts: %v", err))
		return
	}

	sp.Config.XCOutputAccounts = make([]config.XCOutputAccount, len(accounts))
	for i, a := range accounts {
		sp.Config.XCOutputAccounts[i] = config.XCOutputAccount{
			Name:           a.Name,
			Username:       a.Username,
			Password:       a.Password,
			MaxConnections: a.MaxCnx,
			EnableLive:     a.EnableLive,
			EnableSeries:   a.EnableSeries,
			EnableVOD:      a.EnableVOD,
		}
	}
}
