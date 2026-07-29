// work/localscan/xc.go
package localscan

import (
	"fmt"
	"hash/fnv"
	"kptv-proxy/work/db"
	"kptv-proxy/work/logger"
	"path/filepath"
	"sort"
	"strings"
)

// XCStreamID generates a stable positive integer stream ID from an entry hash,
// matching the FNV32a scheme the XC output uses for channel names.
func XCStreamID(hash string) int {
	h := fnv.New32a()
	h.Write([]byte(hash))
	id := int(h.Sum32() & 0x7FFFFFFF)
	if id == 0 {
		id = 1
	}
	return id
}

// ExportEntries returns every stored entry belonging to an enabled local
// source, with its source's group prefix already applied to GroupTitle.
func ExportEntries() []*MediaEntry {
	entries, err := ListAll()
	if err != nil {
		logger.Error("{localscan/xc - ExportEntries} failed to load local media: %v", err)
		return nil
	}

	prefixes := groupPrefixes()
	for _, e := range entries {
		e.GroupTitle = applyGroupPrefix(prefixes[e.LocalSourceID], e.GroupTitle)
	}
	return entries
}

// EntriesForContentType returns export entries whose content classification
// matches contentType ("vod" or "series").
func EntriesForContentType(contentType string) []*MediaEntry {
	all := ExportEntries()

	out := make([]*MediaEntry, 0, len(all))
	for _, e := range all {
		if ContentTypeOf(e.MediaType) == contentType {
			out = append(out, e)
		}
	}
	return out
}

// FindByXCStreamID resolves an XC stream ID back to its local media entry,
// returning nil when no entry matches.
func FindByXCStreamID(id int) *MediaEntry {
	entries, err := ListAll()
	if err != nil {
		logger.Error("{localscan/xc - FindByXCStreamID} failed to load local media: %v", err)
		return nil
	}

	for _, e := range entries {
		if XCStreamID(e.Hash) == id {
			return e
		}
	}
	return nil
}

// ContainerExtension returns the entry's file extension without its leading
// dot, used for XC direct-source URLs.
func ContainerExtension(e *MediaEntry) string {
	return strings.TrimPrefix(strings.ToLower(filepath.Ext(e.Path)), ".")
}

// groupPrefixes returns the configured group prefix for every enabled local
// source, keyed by source ID.
func groupPrefixes() map[int64]string {
	out := make(map[int64]string)

	sources, err := db.GetEnabledLocalSources()
	if err != nil {
		logger.Error("{localscan/xc - groupPrefixes} failed to load local sources: %v", err)
		return out
	}

	for _, s := range sources {
		out[s.ID] = s.GroupPrefix
	}
	return out
}

// applyGroupPrefix prepends the source's configured prefix to an entry's group.
func applyGroupPrefix(prefix, group string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return group
	}
	if group == "" {
		return prefix
	}
	return prefix + "/" + group
}

// EpisodesForSeries returns every stored episode belonging to the same series
// and local source as the supplied entry, in season then episode order.
func EpisodesForSeries(e *MediaEntry) []*MediaEntry {
	if e.MediaType != "shows" {
		return nil
	}

	all := ExportEntries()

	out := make([]*MediaEntry, 0, 32)
	for _, c := range all {
		if c.MediaType == "shows" && c.LocalSourceID == e.LocalSourceID && c.Series == e.Series {
			out = append(out, c)
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Season != out[j].Season {
			return out[i].Season < out[j].Season
		}
		return out[i].Episode < out[j].Episode
	})

	return out
}

// SeriesCategory returns the category a local show belongs to, which is its
// group title with the trailing series name removed. Without this every show
// becomes its own category in the XC output.
func SeriesCategory(e *MediaEntry) string {
	if idx := strings.LastIndex(e.GroupTitle, "/"); idx > 0 {
		return e.GroupTitle[:idx]
	}
	return e.GroupTitle
}

// SeriesForExport returns one representative entry per local series, used to
// build the XC series list. The representative is the first episode in season
// then episode order, and its hash supplies the series ID that get_series_info
// resolves back to the full tree.
func SeriesForExport() []*MediaEntry {
	all := ExportEntries()

	seen := make(map[string]bool)
	out := make([]*MediaEntry, 0, 32)

	for _, e := range all {
		if e.MediaType != "shows" {
			continue
		}
		key := fmt.Sprintf("%d|%s", e.LocalSourceID, e.Series)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, e)
	}

	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Series) < strings.ToLower(out[j].Series)
	})

	return out
}
