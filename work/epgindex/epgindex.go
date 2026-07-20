package epgindex

import (
	"encoding/xml"
	"regexp"
	"strings"
	"sync"
	"time"

	"kptv-proxy/work/logger"
)

// EPGChannel holds the parsed identity fields from a single XMLTV <channel> element.
type EPGChannel struct {
	ID           string   // the id attribute from <channel id="...">
	DisplayNames []string // all <display-name> values
}

// EPGProgramme holds one parsed <programme> entry for XC guide responses.
type EPGProgramme struct {
	Start time.Time // programme start
	Stop  time.Time // programme stop
	Title string    // decoded <title>
	Desc  string    // decoded <desc>
}

var (
	mu               sync.RWMutex
	index            []EPGChannel
	reChannelBlock   = regexp.MustCompile(`(?s)<channel\s[^>]*id="([^"]*)"[^>]*>(.*?)</channel>`)
	reDisplayName    = regexp.MustCompile(`<display-name[^>]*>([^<]*)</display-name>`)
	progIndex        map[string][]EPGProgramme
	reProgrammeBlock = regexp.MustCompile(`(?s)<programme\s([^>]*)>(.*?)</programme>`)
	reAttrStart      = regexp.MustCompile(`start="([^"]*)"`)
	reAttrStop       = regexp.MustCompile(`stop="([^"]*)"`)
	reAttrChannel    = regexp.MustCompile(`channel="([^"]*)"`)
	reTitle          = regexp.MustCompile(`(?s)<title[^>]*>(.*?)</title>`)
	reDesc           = regexp.MustCompile(`(?s)<desc[^>]*>(.*?)</desc>`)
)

// Rebuild parses the merged XMLTV string and replaces the in-memory index.
// Called after each EPG cache refresh.
func Rebuild(xmltv string) {
	matches := reChannelBlock.FindAllStringSubmatch(xmltv, -1)

	fresh := make([]EPGChannel, 0, len(matches))
	for _, m := range matches {
		// skip channels with no usable id; they cannot be mapped and render
		// as titleless rows in the mapping picker
		if strings.TrimSpace(m[1]) == "" {
			continue
		}
		ch := EPGChannel{ID: m[1]}
		for _, dn := range reDisplayName.FindAllStringSubmatch(m[2], -1) {
			name := strings.TrimSpace(dn[1])
			if name != "" {
				ch.DisplayNames = append(ch.DisplayNames, name)
			}
		}
		fresh = append(fresh, ch)
	}

	// index programmes by channel id for XC guide lookups — the merged doc
	// only contains mapped channels' programmes, so this stays bounded
	freshProgs := make(map[string][]EPGProgramme)
	for _, pm := range reProgrammeBlock.FindAllStringSubmatch(xmltv, -1) {
		attrs, body := pm[1], pm[2]

		chm := reAttrChannel.FindStringSubmatch(attrs)
		if chm == nil || strings.TrimSpace(chm[1]) == "" {
			continue
		}
		sm := reAttrStart.FindStringSubmatch(attrs)
		em := reAttrStop.FindStringSubmatch(attrs)
		if sm == nil || em == nil {
			continue
		}
		start, ok1 := parseXMLTVTime(sm[1])
		stop, ok2 := parseXMLTVTime(em[1])
		if !ok1 || !ok2 {
			continue
		}

		p := EPGProgramme{Start: start, Stop: stop}
		if tm := reTitle.FindStringSubmatch(body); tm != nil {
			p.Title = decodeXMLText(strings.TrimSpace(tm[1]))
		}
		if dm := reDesc.FindStringSubmatch(body); dm != nil {
			p.Desc = decodeXMLText(strings.TrimSpace(dm[1]))
		}
		freshProgs[chm[1]] = append(freshProgs[chm[1]], p)
	}
	for id := range freshProgs {
		ps := freshProgs[id]
		for i := 0; i < len(ps)-1; i++ {
			for j := i + 1; j < len(ps); j++ {
				if ps[i].Start.After(ps[j].Start) {
					ps[i], ps[j] = ps[j], ps[i]
				}
			}
		}
	}

	mu.Lock()
	progIndex = freshProgs
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

// RebuildFromSlices parses raw XMLTV <channel> element strings and replaces
// the in-memory index. Used to index the unfiltered merged EPG without
// materializing the full document string.
func RebuildFromSlices(channelElements []string) {
	fresh := make([]EPGChannel, 0, len(channelElements))
	for _, el := range channelElements {
		m := reChannelBlock.FindStringSubmatch(el)
		if m == nil || strings.TrimSpace(m[1]) == "" {
			continue
		}
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

	logger.Debug("{epgindex - RebuildFromSlices} Index rebuilt with %d channels", len(fresh))
}

// RebuildProgrammesFromSlices parses raw XMLTV <programme> element strings and
// replaces the in-memory programme index. Used by the warmup/refresh pipeline
// to index only the mapped export copies (plus the dummy entry) without
// materializing the full merged document.
func RebuildProgrammesFromSlices(programmeElements []string) {
	fresh := make(map[string][]EPGProgramme)
	for _, el := range programmeElements {
		pm := reProgrammeBlock.FindStringSubmatch(el)
		if pm == nil {
			continue
		}
		attrs, body := pm[1], pm[2]

		chm := reAttrChannel.FindStringSubmatch(attrs)
		if chm == nil || strings.TrimSpace(chm[1]) == "" {
			continue
		}
		sm := reAttrStart.FindStringSubmatch(attrs)
		em := reAttrStop.FindStringSubmatch(attrs)
		if sm == nil || em == nil {
			continue
		}
		start, ok1 := parseXMLTVTime(sm[1])
		stop, ok2 := parseXMLTVTime(em[1])
		if !ok1 || !ok2 {
			continue
		}

		p := EPGProgramme{Start: start, Stop: stop}
		if tm := reTitle.FindStringSubmatch(body); tm != nil {
			p.Title = decodeXMLText(strings.TrimSpace(tm[1]))
		}
		if dm := reDesc.FindStringSubmatch(body); dm != nil {
			p.Desc = decodeXMLText(strings.TrimSpace(dm[1]))
		}
		fresh[chm[1]] = append(fresh[chm[1]], p)
	}

	// keep each channel's programmes in start order for windowed lookups
	for id := range fresh {
		ps := fresh[id]
		for i := 0; i < len(ps)-1; i++ {
			for j := i + 1; j < len(ps); j++ {
				if ps[i].Start.After(ps[j].Start) {
					ps[i], ps[j] = ps[j], ps[i]
				}
			}
		}
	}

	mu.Lock()
	progIndex = fresh
	mu.Unlock()

	logger.Debug("{epgindex - RebuildProgrammesFromSlices} Programme index rebuilt with %d channels", len(fresh))
}

// Programmes returns up to limit programmes for the given channel id that end
// after the given time, in start order. limit <= 0 returns everything current
// and future.
func Programmes(channelID string, after time.Time, limit int) []EPGProgramme {
	mu.RLock()
	defer mu.RUnlock()

	ps := progIndex[channelID]
	var out []EPGProgramme
	for _, p := range ps {
		if p.Stop.Before(after) {
			continue
		}
		out = append(out, p)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

// parseXMLTVTime handles the standard XMLTV timestamp with or without a zone.
func parseXMLTVTime(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if t, err := time.Parse("20060102150405 -0700", s); err == nil {
		return t, true
	}
	if t, err := time.Parse("20060102150405", s); err == nil {
		return t, true
	}
	return time.Time{}, false
}

// decodeXMLText resolves entities in element text (&amp; etc.)
func decodeXMLText(s string) string {
	var out string
	if err := xml.Unmarshal([]byte("<x>"+s+"</x>"), &out); err != nil {
		return s
	}
	return out
}
