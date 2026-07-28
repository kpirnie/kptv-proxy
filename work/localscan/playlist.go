// work/localscan/playlist.go
package localscan

import (
	"fmt"
	"kptv-proxy/work/utils"
	"strconv"
	"strings"
)

// maxAttrLen bounds any single attribute value in runes. Plot synopses are
// unbounded in a .nfo and some players truncate or reject very long #EXTINF
// lines outright.
const maxAttrLen = 600

// maxCast bounds how many credited actors are written to tvg-cast.
const maxCast = 8

// ContentTypeOf maps a local media type onto the proxy's export content
// classification. Music rides in the VOD bucket and is separated by category.
func ContentTypeOf(mediaType string) string {
	switch mediaType {
	case "shows":
		return "series"
	default:
		return "vod"
	}
}

// WritePlaylistEntries appends #EXTINF blocks for every stored local media
// entry onto the supplied builder. enableVOD and enableSeries mirror the
// requesting XC account's content toggles; groupFilter, when non-empty,
// restricts output to entries whose group title matches it.
//
// Returns the number of entries written.
func WritePlaylistEntries(sb *strings.Builder, baseURL, username, password, groupFilter, typeFilter string, enableVOD, enableSeries bool) int {

	if !enableVOD && !enableSeries {
		return 0
	}

	entries := ExportEntries()

	written := 0
	for _, e := range entries {
		contentType := ContentTypeOf(e.MediaType)

		if typeFilter != "" && contentType != typeFilter {
			continue
		}

		switch contentType {
		case "series":
			if !enableSeries {
				continue
			}
		default:
			if !enableVOD {
				continue
			}
		}

		if groupFilter != "" && !strings.EqualFold(e.GroupTitle, groupFilter) {
			continue
		}

		sb.WriteString(formatEntry(e, e.GroupTitle, baseURL, username, password))
		written++
	}

	return written
}

// formatEntry renders one entry as an #EXTINF line plus its proxied URL.
func formatEntry(e *MediaEntry, group, baseURL, username, password string) string {
	duration := e.Duration
	if duration == 0 {
		duration = -1
	}

	return fmt.Sprintf("#EXTINF:%d %s,%s\n%s\n",
		duration,
		buildAttrs(e, group, baseURL, username, password),
		sanitize(e.Display),
		streamURL(baseURL, username, password, e.Hash),
	)
}

// buildAttrs assembles the EXTINF attribute list for an entry. Standard
// attributes are emitted first so lenient parsers that stop at the first
// unrecognised key still receive the ones they actually render.
func buildAttrs(e *MediaEntry, group, baseURL, username, password string) string {
	type kv struct{ k, v string }
	var pairs []kv

	add := func(k, v string) {
		if v = sanitize(v); v != "" {
			pairs = append(pairs, kv{k, v})
		}
	}

	add("tvg-id", tvgID(e))
	add("tvg-name", e.TVGName)
	add("group-title", group)
	if e.Poster != "" {
		add("tvg-logo", artworkURL(baseURL, username, password, e.Hash, "poster"))
	}

	switch e.MediaType {
	case "music":
		add("tvg-artist", e.Artist)
		add("tvg-album", e.Album)
		if e.Disc > 0 {
			add("tvg-disc", strconv.Itoa(e.Disc))
		}
		if e.Track > 0 {
			add("tvg-track", strconv.Itoa(e.Track))
		}
	case "shows":
		add("tvg-show", e.Series)
		if e.Season > 0 {
			add("tvg-season", strconv.Itoa(e.Season))
		}
		if e.Episode > 0 {
			add("tvg-episode", strconv.Itoa(e.Episode))
		}
		add("tvg-episode-title", e.EpisodeTitle)
	}

	add("tvg-genre", strings.Join(e.Genres, ", "))
	add("tvg-year", e.Year)

	add("tvg-title", e.Title)
	add("tvg-tagline", e.Tagline)
	add("tvg-plot", e.Plot)
	if e.Rating > 0 {
		add("tvg-rating", strconv.FormatFloat(e.Rating, 'f', 1, 64))
	}
	if e.CriticRating > 0 {
		add("tvg-critic-rating", strconv.Itoa(e.CriticRating))
	}
	add("tvg-mpaa", e.MPAA)
	add("tvg-director", strings.Join(e.Directors, ", "))
	add("tvg-cast", castNames(e))
	add("tvg-studio", strings.Join(e.Studios, ", "))
	add("tvg-country", e.Country)
	add("tvg-premiered", e.Premiered)
	add("tvg-collection", e.Collection)
	if e.Fanart != "" {
		add("tvg-fanart", artworkURL(baseURL, username, password, e.Hash, "fanart"))
	}

	parts := make([]string, 0, len(pairs))
	for _, p := range pairs {
		parts = append(parts, fmt.Sprintf(`%s="%s"`, p.k, utils.EscapeM3UAttribute(p.v)))
	}
	return strings.Join(parts, " ")
}

// streamURL builds the proxied playback URL for a local media entry.
func streamURL(baseURL, username, password, hash string) string {
	return fmt.Sprintf("%s/local/%s/%s/%s", baseURL, username, password, hash)
}

// artworkURL builds the proxied artwork URL for a local media entry.
func artworkURL(baseURL, username, password, hash, kind string) string {
	return fmt.Sprintf("%s/localart/%s/%s/%s/%s", baseURL, username, password, hash, kind)
}

// tvgID returns a stable external identifier for the entry, or an empty string
// when none is known. Identifiers are never synthesised: a fabricated tvg-id
// can collide with a real EPG channel id in a merged playlist.
func tvgID(e *MediaEntry) string {
	switch {
	case e.IMDBID != "":
		return e.IMDBID
	case e.TMDBID != "":
		return "tmdb-" + e.TMDBID
	case e.TVDBID != "":
		return "tvdb-" + e.TVDBID
	}
	return ""
}

// castNames joins the leading credited actor names for tvg-cast.
func castNames(e *MediaEntry) string {
	if len(e.Cast) == 0 {
		return ""
	}
	n := len(e.Cast)
	if n > maxCast {
		n = maxCast
	}
	names := make([]string, 0, n)
	for _, p := range e.Cast[:n] {
		if p.Name != "" {
			names = append(names, p.Name)
		}
	}
	return strings.Join(names, ", ")
}

// sanitize collapses all whitespace to single spaces and bounds the result.
// An #EXTINF line is newline-terminated, so an embedded newline in a plot or
// tagline would truncate the attribute list and corrupt every entry that
// follows it in the playlist.
func sanitize(s string) string {
	if s == "" {
		return ""
	}

	s = strings.Join(strings.Fields(s), " ")

	r := []rune(s)
	if len(r) > maxAttrLen {
		s = strings.TrimSpace(string(r[:maxAttrLen])) + "…"
	}
	return s
}
