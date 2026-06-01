package epgindex

import (
	"regexp"
	"strings"
	"sync"

	"kptv-proxy/work/logger"
)

// EPGChannel holds the parsed identity fields from a single XMLTV <channel> element.
type EPGChannel struct {
	ID           string   // the id attribute from <channel id="...">
	DisplayNames []string // all <display-name> values
}

var (
	mu    sync.RWMutex
	index []EPGChannel

	reChannelBlock = regexp.MustCompile(`(?s)<channel\s[^>]*id="([^"]*)"[^>]*>(.*?)</channel>`)
	reDisplayName  = regexp.MustCompile(`<display-name[^>]*>([^<]*)</display-name>`)
)

// Rebuild parses the merged XMLTV string and replaces the in-memory index.
// Called after each EPG cache refresh.
func Rebuild(xmltv string) {
	matches := reChannelBlock.FindAllStringSubmatch(xmltv, -1)

	fresh := make([]EPGChannel, 0, len(matches))
	for _, m := range matches {
		ch := EPGChannel{ID: m[1]}
		for _, dn := range reDisplayName.FindAllStringSubmatch(m[2], -1) {
			name := strings.TrimSpace(dn[1])
			if name != "" {
				ch.DisplayNames = append(ch.DisplayNames, name)
			}
		}
		fresh = append(fresh, ch)
	}

	mu.Lock()
	index = fresh
	mu.Unlock()

	logger.Debug("{epgindex - Rebuild} Index rebuilt with %d channels", len(fresh))
}

// Search returns EPGChannel entries whose ID or any display-name contains
// the query string (case-insensitive). Returns up to limit results.
func Search(query string, limit int) []EPGChannel {
	if query == "" || limit <= 0 {
		return nil
	}

	q := strings.ToLower(query)

	mu.RLock()
	defer mu.RUnlock()

	results := make([]EPGChannel, 0, limit)
	for _, ch := range index {
		if len(results) >= limit {
			break
		}
		if strings.Contains(strings.ToLower(ch.ID), q) {
			results = append(results, ch)
			continue
		}
		for _, dn := range ch.DisplayNames {
			if strings.Contains(strings.ToLower(dn), q) {
				results = append(results, ch)
				break
			}
		}
	}

	return results
}

// Size returns the number of channels currently in the index.
func Size() int {
	mu.RLock()
	defer mu.RUnlock()
	return len(index)
}
