package admin

import (
	"encoding/json"
	"fmt"
	"kptv-proxy/work/config"
	"kptv-proxy/work/db"
	"kptv-proxy/work/proxy"
	"kptv-proxy/work/utils"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
)

// sourcePayload is the admin JSON shape for a single source. Duration fields are
// strings matching the stored convention, and the password is masked on read.
type sourcePayload struct {
	ID                     int64  `json:"id"`
	Name                   string `json:"name"`
	URL                    string `json:"url"`
	Order                  int    `json:"order"`
	MaxConnections         int    `json:"maxConnections"`
	MaxStreamTimeout       string `json:"maxStreamTimeout"`
	RetryDelay             string `json:"retryDelay"`
	MaxRetries             int    `json:"maxRetries"`
	MaxFailuresBeforeBlock int    `json:"maxFailuresBeforeBlock"`
	MinDataSize            int64  `json:"minDataSize"`
	UserAgent              string `json:"userAgent"`
	ReqOrigin              string `json:"reqOrigin"`
	ReqReferrer            string `json:"reqReferrer"`
	Username               string `json:"username"`
	Password               string `json:"password"`
	LiveIncludeRegex       string `json:"liveIncludeRegex"`
	LiveExcludeRegex       string `json:"liveExcludeRegex"`
	SeriesIncludeRegex     string `json:"seriesIncludeRegex"`
	SeriesExcludeRegex     string `json:"seriesExcludeRegex"`
	VODIncludeRegex        string `json:"vodIncludeRegex"`
	VODExcludeRegex        string `json:"vodExcludeRegex"`
}

// sourceFromDB converts a stored row into the admin payload, masking the password.
func sourceFromDB(s db.Source) sourcePayload {
	return sourcePayload{
		ID:                     s.ID,
		Name:                   s.Name,
		URL:                    s.URI,
		Order:                  s.SortOrder,
		MaxConnections:         s.MaxCnx,
		MaxStreamTimeout:       s.MaxStreamTo,
		RetryDelay:             s.RetryDelay,
		MaxRetries:             s.MaxRetries,
		MaxFailuresBeforeBlock: s.MaxFailures,
		MinDataSize:            int64(s.MinDataSize),
		UserAgent:              s.UserAgent,
		ReqOrigin:              s.ReqOrigin,
		ReqReferrer:            s.ReqReferer,
		Username:               s.Username,
		Password:               maskSecret(s.Password),
		LiveIncludeRegex:       s.LiveIncRegex,
		LiveExcludeRegex:       s.LiveExcRegex,
		SeriesIncludeRegex:     s.SeriesIncRegex,
		SeriesExcludeRegex:     s.SeriesExcRegex,
		VODIncludeRegex:        s.VODIncRegex,
		VODExcludeRegex:        s.VODExcRegex,
	}
}

// sourceToDB converts an admin payload into a storable row, applying defaults for
// the duration fields.
func sourceToDB(p sourcePayload) db.Source {
	if p.MaxStreamTimeout == "" {
		p.MaxStreamTimeout = "30s"
	}
	if p.RetryDelay == "" {
		p.RetryDelay = "5s"
	}
	if p.Order <= 0 {
		p.Order = 1
	}

	return db.Source{
		ID:             p.ID,
		Name:           p.Name,
		URI:            p.URL,
		Username:       p.Username,
		Password:       p.Password,
		SortOrder:      p.Order,
		MaxCnx:         p.MaxConnections,
		MaxStreamTo:    p.MaxStreamTimeout,
		RetryDelay:     p.RetryDelay,
		MaxRetries:     p.MaxRetries,
		MaxFailures:    p.MaxFailuresBeforeBlock,
		MinDataSize:    int(p.MinDataSize),
		UserAgent:      p.UserAgent,
		ReqOrigin:      p.ReqOrigin,
		ReqReferer:     p.ReqReferrer,
		LiveIncRegex:   p.LiveIncludeRegex,
		LiveExcRegex:   p.LiveExcludeRegex,
		SeriesIncRegex: p.SeriesIncludeRegex,
		SeriesExcRegex: p.SeriesExcludeRegex,
		VODIncRegex:    p.VODIncludeRegex,
		VODExcRegex:    p.VODExcludeRegex,
	}
}

// handleGetSources returns every configured source.
func handleGetSources(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		rows, err := db.GetAllSources()
		if err != nil {
			addLogEntry("error", fmt.Sprintf("Failed to load sources: %v", err))
			http.Error(w, "Failed to load sources", http.StatusInternalServerError)
			return
		}

		payload := make([]sourcePayload, len(rows))
		for i, row := range rows {
			payload[i] = sourceFromDB(row)
		}

		utils.WriteJSON(w, payload)
	}
}

// handleCreateSource adds a new source and re-imports in the background.
func handleCreateSource(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		var incoming sourcePayload
		if err := json.NewDecoder(r.Body).Decode(&incoming); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		if incoming.Name == "" || incoming.URL == "" {
			http.Error(w, "Name and URL are required", http.StatusBadRequest)
			return
		}

		if incoming.Password == maskedSecret {
			incoming.Password = ""
		}

		id, err := db.InsertSource(sourceToDB(incoming))
		if err != nil {
			addLogEntry("error", fmt.Sprintf("Failed to create source: %v", err))
			http.Error(w, "Failed to create source", http.StatusInternalServerError)
			return
		}

		reloadSources(sp)
		addLogEntry("info", fmt.Sprintf("Source created: %s", incoming.Name))

		w.WriteHeader(http.StatusOK)
		utils.WriteJSON(w, map[string]any{"status": "success", "id": id})
	}
}

// handleUpdateSource updates an existing source and re-imports in the background.
func handleUpdateSource(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		vars := mux.Vars(r)
		id, err := strconv.ParseInt(vars["id"], 10, 64)
		if err != nil {
			http.Error(w, "Invalid ID", http.StatusBadRequest)
			return
		}

		var incoming sourcePayload
		if err := json.NewDecoder(r.Body).Decode(&incoming); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		if incoming.Name == "" || incoming.URL == "" {
			http.Error(w, "Name and URL are required", http.StatusBadRequest)
			return
		}

		existing, err := db.GetSource(id)
		if err != nil {
			http.Error(w, "Source not found", http.StatusNotFound)
			return
		}

		// a password posted back unchanged means keep the stored secret
		if incoming.Password == maskedSecret {
			incoming.Password = existing.Password
		}

		incoming.ID = id
		if err := db.UpdateSource(sourceToDB(incoming)); err != nil {
			addLogEntry("error", fmt.Sprintf("Failed to update source: %v", err))
			http.Error(w, "Failed to update source", http.StatusInternalServerError)
			return
		}

		reloadSources(sp)
		addLogEntry("info", fmt.Sprintf("Source updated: %s", incoming.Name))

		w.WriteHeader(http.StatusOK)
		utils.WriteJSON(w, map[string]string{"status": "success"})
	}
}

// handleDeleteSource removes a source and re-imports in the background.
func handleDeleteSource(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		vars := mux.Vars(r)
		id, err := strconv.ParseInt(vars["id"], 10, 64)
		if err != nil {
			http.Error(w, "Invalid ID", http.StatusBadRequest)
			return
		}

		if err := db.DeleteSource(id); err != nil {
			addLogEntry("error", fmt.Sprintf("Failed to delete source: %v", err))
			http.Error(w, "Failed to delete source", http.StatusInternalServerError)
			return
		}

		reloadSources(sp)
		addLogEntry("info", fmt.Sprintf("Source deleted: %d", id))

		w.WriteHeader(http.StatusOK)
		utils.WriteJSON(w, map[string]string{"status": "success"})
	}
}

// reloadSources swaps the live source list for the stored one, carrying over the
// connection counters of sources that survived the change so in-flight streams
// keep their accounting, then re-imports in the background.
func reloadSources(sp *proxy.StreamProxy) {
	sources, err := config.LoadSources()
	if err != nil {
		addLogEntry("error", fmt.Sprintf("Failed to reload sources: %v", err))
		return
	}

	previous := make(map[string]*config.SourceConfig, len(sp.Config.Sources))
	for i := range sp.Config.Sources {
		key := sp.Config.Sources[i].Name + "|" + sp.Config.Sources[i].URL
		previous[key] = &sp.Config.Sources[i]
	}

	for i := range sources {
		if prior, ok := previous[sources[i].Name+"|"+sources[i].URL]; ok {
			sources[i].ActiveConns.Store(prior.ActiveConns.Load())
		}
	}

	sp.Config.Sources = sources
	sp.ReinitRateLimiters()

	go sp.ImportStreams()
}
