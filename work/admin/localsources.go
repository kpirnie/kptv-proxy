package admin

import (
	"encoding/json"
	"fmt"
	"kptv-proxy/work/db"
	"kptv-proxy/work/localscan"
	"kptv-proxy/work/proxy"
	"net/http"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
)

// localSourceIn is the request payload for creating or updating a local source.
type localSourceIn struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	MediaType   string `json:"mediaType"`
	GroupPrefix string `json:"groupPrefix"`
	Order       int    `json:"order"`
	Enabled     bool   `json:"enabled"`
	IncRegex    string `json:"incRegex"`
	ExcRegex    string `json:"excRegex"`
}

// localSourceOut is the response payload for a configured local source.
type localSourceOut struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Path        string `json:"path"`
	MediaType   string `json:"mediaType"`
	GroupPrefix string `json:"groupPrefix"`
	Order       int    `json:"order"`
	Enabled     bool   `json:"enabled"`
	IncRegex    string `json:"incRegex"`
	ExcRegex    string `json:"excRegex"`
	LastScan    int64  `json:"lastScan"`
	EntryCount  int    `json:"entryCount"`
}

// handleGetLocalSources returns all configured local media sources.
func handleGetLocalSources(_ *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		sources, err := db.GetAllLocalSources()
		if err != nil {
			addLogEntry("error", fmt.Sprintf("Failed to get local sources: %v", err))
			http.Error(w, "Failed to get local sources", http.StatusInternalServerError)
			return
		}

		out := make([]localSourceOut, len(sources))
		for i, s := range sources {
			out[i] = toLocalSourceOut(s)
		}

		json.NewEncoder(w).Encode(out)
	}
}

// handleCreateLocalSource creates a new local media source.
func handleCreateLocalSource(_ *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		incoming, err := decodeLocalSource(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		id, err := db.InsertLocalSource(incoming)
		if err != nil {
			addLogEntry("error", fmt.Sprintf("Failed to create local source: %v", err))
			http.Error(w, "Failed to create local source", http.StatusInternalServerError)
			return
		}

		addLogEntry("info", fmt.Sprintf("Local source created: %s", incoming.Name))

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{"status": "success", "id": id})
	}
}

// handleUpdateLocalSource updates an existing local media source.
func handleUpdateLocalSource(_ *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		id, err := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
		if err != nil {
			http.Error(w, "Invalid ID", http.StatusBadRequest)
			return
		}

		incoming, err := decodeLocalSource(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		existing, err := db.GetLocalSource(id)
		if err != nil {
			http.Error(w, "Local source not found", http.StatusNotFound)
			return
		}

		incoming.ID = id
		if err := db.UpdateLocalSource(incoming); err != nil {
			addLogEntry("error", fmt.Sprintf("Failed to update local source: %v", err))
			http.Error(w, "Failed to update local source", http.StatusInternalServerError)
			return
		}

		// A changed path or media type invalidates every stored entry for the
		// source — drop them so the next scan rebuilds from scratch.
		if existing.Path != incoming.Path || existing.MediaType != incoming.MediaType {
			if err := localscan.DeleteAllForSource(id); err != nil {
				addLogEntry("error", fmt.Sprintf("Failed to clear local media for source %d: %v", id, err))
			}
		}

		addLogEntry("info", fmt.Sprintf("Local source updated: %s", incoming.Name))

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{"status": "success"})
	}
}

// handleDeleteLocalSource removes a local media source and its stored entries.
func handleDeleteLocalSource(_ *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		id, err := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
		if err != nil {
			http.Error(w, "Invalid ID", http.StatusBadRequest)
			return
		}

		if err := db.DeleteLocalSource(id); err != nil {
			addLogEntry("error", fmt.Sprintf("Failed to delete local source: %v", err))
			http.Error(w, "Failed to delete local source", http.StatusInternalServerError)
			return
		}

		addLogEntry("info", fmt.Sprintf("Local source deleted: %d", id))

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{"status": "success"})
	}
}

// handleScanLocalSource runs a manual scan of a single local source.
func handleScanLocalSource(_ *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		id, err := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
		if err != nil {
			http.Error(w, "Invalid ID", http.StatusBadRequest)
			return
		}

		count, err := localscan.ScanSource(id, localscan.Enrich)
		if err != nil {
			addLogEntry("error", fmt.Sprintf("Local scan failed for source %d: %v", id, err))
			http.Error(w, fmt.Sprintf("Scan failed: %v", err), http.StatusInternalServerError)
			return
		}

		addLogEntry("info", fmt.Sprintf("Local scan complete for source %d: %d entries", id, count))

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{"status": "success", "count": count})
	}
}

// handleScanAllLocalSources runs a manual scan across every enabled local source.
func handleScanAllLocalSources(_ *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		count, err := localscan.ScanAll(localscan.Enrich)
		if err != nil {
			addLogEntry("error", fmt.Sprintf("Local scan-all failed: %v", err))
			http.Error(w, fmt.Sprintf("Scan failed: %v", err), http.StatusInternalServerError)
			return
		}

		addLogEntry("info", fmt.Sprintf("Local scan-all complete: %d entries", count))

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{"status": "success", "count": count})
	}
}

// handleGetLocalMedia returns stored local media entries, optionally filtered
// by source ID and a free-text query, with paging.
func handleGetLocalMedia(_ *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		q := r.URL.Query()

		var (
			entries []*localscan.MediaEntry
			err     error
		)

		if sid := q.Get("source"); sid != "" {
			id, parseErr := strconv.ParseInt(sid, 10, 64)
			if parseErr != nil {
				http.Error(w, "Invalid source ID", http.StatusBadRequest)
				return
			}
			entries, err = localscan.ListBySource(id)
		} else {
			entries, err = localscan.ListAll()
		}
		if err != nil {
			addLogEntry("error", fmt.Sprintf("Failed to list local media: %v", err))
			http.Error(w, "Failed to list local media", http.StatusInternalServerError)
			return
		}

		if term := strings.ToLower(strings.TrimSpace(q.Get("q"))); term != "" {
			filtered := entries[:0]
			for _, e := range entries {
				if strings.Contains(strings.ToLower(e.Display), term) ||
					strings.Contains(strings.ToLower(e.GroupTitle), term) {
					filtered = append(filtered, e)
				}
			}
			entries = filtered
		}

		total := len(entries)
		page := atoiDefault(q.Get("page"), 1)
		size := atoiDefault(q.Get("size"), 100)
		if page < 1 {
			page = 1
		}
		if size < 1 || size > 1000 {
			size = 100
		}

		start := (page - 1) * size
		if start > total {
			start = total
		}
		end := start + size
		if end > total {
			end = total
		}

		json.NewEncoder(w).Encode(map[string]any{
			"total":   total,
			"page":    page,
			"size":    size,
			"entries": entries[start:end],
		})
	}
}

// handleGetLocalMediaEntry returns a single stored local media entry by hash.
func handleGetLocalMediaEntry(_ *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		entry, err := localscan.GetByHash(mux.Vars(r)["hash"])
		if err != nil {
			http.Error(w, "Entry not found", http.StatusNotFound)
			return
		}

		json.NewEncoder(w).Encode(entry)
	}
}

// handleUpdateLocalMedia persists edited metadata for a single local media
// entry, writing the .nfo sidecar (and embedded tags for music) before
// re-scanning the file.
func handleUpdateLocalMedia(_ *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		hash := mux.Vars(r)["hash"]

		existing, err := localscan.GetByHash(hash)
		if err != nil {
			http.Error(w, "Entry not found", http.StatusNotFound)
			return
		}

		var incoming localscan.MediaEntry
		if err := json.NewDecoder(r.Body).Decode(&incoming); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		// Identity and location come from the stored row, never the payload —
		// a client must not be able to retarget an edit at another file.
		incoming.ID = existing.ID
		incoming.LocalSourceID = existing.LocalSourceID
		incoming.Hash = existing.Hash
		incoming.Path = existing.Path
		incoming.MediaType = existing.MediaType

		updated, err := localscan.ApplyEdit(&incoming)
		if err != nil {
			addLogEntry("error", fmt.Sprintf("Metadata edit failed for %s: %v", existing.Path, err))
			http.Error(w, fmt.Sprintf("Edit failed: %v", err), http.StatusInternalServerError)
			return
		}

		addLogEntry("info", fmt.Sprintf("Metadata updated: %s", existing.Display))

		json.NewEncoder(w).Encode(updated)
	}
}

// decodeLocalSource parses and validates a local source payload.
func decodeLocalSource(r *http.Request) (db.LocalSource, error) {
	var in localSourceIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		return db.LocalSource{}, fmt.Errorf("Invalid JSON")
	}

	in.Name = strings.TrimSpace(in.Name)
	in.Path = strings.TrimSpace(in.Path)

	if in.Name == "" || in.Path == "" {
		return db.LocalSource{}, fmt.Errorf("Name and path are required")
	}

	mt, ok := localscan.MediaTypeToInt[strings.ToLower(strings.TrimSpace(in.MediaType))]
	if !ok {
		return db.LocalSource{}, fmt.Errorf("Media type must be one of: music, movies, shows")
	}

	if in.Order <= 0 {
		in.Order = 1
	}

	return db.LocalSource{
		Name:        in.Name,
		Path:        in.Path,
		MediaType:   mt,
		GroupPrefix: strings.TrimSpace(in.GroupPrefix),
		SortOrder:   in.Order,
		Enabled:     in.Enabled,
		IncRegex:    in.IncRegex,
		ExcRegex:    in.ExcRegex,
	}, nil
}

// toLocalSourceOut converts a stored row into its API representation.
func toLocalSourceOut(s db.LocalSource) localSourceOut {
	return localSourceOut{
		ID:          s.ID,
		Name:        s.Name,
		Path:        s.Path,
		MediaType:   localscan.MediaTypeFromInt[s.MediaType],
		GroupPrefix: s.GroupPrefix,
		Order:       s.SortOrder,
		Enabled:     s.Enabled,
		IncRegex:    s.IncRegex,
		ExcRegex:    s.ExcRegex,
		LastScan:    s.LastScan,
		EntryCount:  s.EntryCount,
	}
}

// atoiDefault parses an integer query value, returning def when absent or invalid.
func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return v
}
