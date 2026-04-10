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

// handleGetEPGs returns all configured EPG sources.
func handleGetEPGs(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		epgs, err := db.GetAllEPGs()
		if err != nil {
			addLogEntry("error", fmt.Sprintf("Failed to get EPGs: %v", err))
			http.Error(w, "Failed to get EPGs", http.StatusInternalServerError)
			return
		}

		type epgOut struct {
			ID        int64  `json:"id"`
			Name      string `json:"name"`
			URL       string `json:"url"`
			SortOrder int    `json:"order"`
		}

		out := make([]epgOut, len(epgs))
		for i, e := range epgs {
			out[i] = epgOut{
				ID:        e.ID,
				Name:      e.Name,
				URL:       e.URL,
				SortOrder: e.SortOrder,
			}
		}

		json.NewEncoder(w).Encode(out)
	}
}

// handleCreateEPG creates a new EPG source.
func handleCreateEPG(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		var incoming config.EPGConfig
		if err := json.NewDecoder(r.Body).Decode(&incoming); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		if incoming.Name == "" || incoming.URL == "" {
			http.Error(w, "Name and URL are required", http.StatusBadRequest)
			return
		}

		if incoming.Order <= 0 {
			incoming.Order = 1
		}

		id, err := db.InsertEPG(db.EPG{
			Name:      incoming.Name,
			URL:       incoming.URL,
			SortOrder: incoming.Order,
		})
		if err != nil {
			addLogEntry("error", fmt.Sprintf("Failed to create EPG: %v", err))
			http.Error(w, "Failed to create EPG", http.StatusInternalServerError)
			return
		}

		reloadEPGs(sp)
		addLogEntry("info", fmt.Sprintf("EPG created: %s", incoming.Name))

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{"status": "success", "id": id})
	}
}

// handleUpdateEPG updates an existing EPG source.
func handleUpdateEPG(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		vars := mux.Vars(r)
		id, err := strconv.ParseInt(vars["id"], 10, 64)
		if err != nil {
			http.Error(w, "Invalid ID", http.StatusBadRequest)
			return
		}

		var incoming config.EPGConfig
		if err := json.NewDecoder(r.Body).Decode(&incoming); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		if incoming.Name == "" || incoming.URL == "" {
			http.Error(w, "Name and URL are required", http.StatusBadRequest)
			return
		}

		if incoming.Order <= 0 {
			incoming.Order = 1
		}

		if err := db.UpdateEPG(db.EPG{
			ID:        id,
			Name:      incoming.Name,
			URL:       incoming.URL,
			SortOrder: incoming.Order,
		}); err != nil {
			addLogEntry("error", fmt.Sprintf("Failed to update EPG: %v", err))
			http.Error(w, "Failed to update EPG", http.StatusInternalServerError)
			return
		}

		reloadEPGs(sp)
		addLogEntry("info", fmt.Sprintf("EPG updated: %s", incoming.Name))

		json.NewEncoder(w).Encode(map[string]string{"status": "success"})
	}
}

// handleDeleteEPG deletes an EPG source by ID.
func handleDeleteEPG(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		vars := mux.Vars(r)
		id, err := strconv.ParseInt(vars["id"], 10, 64)
		if err != nil {
			http.Error(w, "Invalid ID", http.StatusBadRequest)
			return
		}

		if err := db.DeleteEPG(id); err != nil {
			addLogEntry("error", fmt.Sprintf("Failed to delete EPG: %v", err))
			http.Error(w, "Failed to delete EPG", http.StatusInternalServerError)
			return
		}

		reloadEPGs(sp)
		addLogEntry("info", fmt.Sprintf("EPG deleted: %d", id))

		json.NewEncoder(w).Encode(map[string]string{"status": "success"})
	}
}

// reloadEPGs refreshes the in-memory EPG list from the database.
func reloadEPGs(sp *proxy.StreamProxy) {
	epgs, err := db.GetAllEPGs()
	if err != nil {
		addLogEntry("error", fmt.Sprintf("Failed to reload EPGs: %v", err))
		return
	}

	sp.Config.EPGs = make([]config.EPGConfig, len(epgs))
	for i, e := range epgs {
		sp.Config.EPGs[i] = config.EPGConfig{
			Name:  e.Name,
			URL:   e.URL,
			Order: e.SortOrder,
		}
	}
}
