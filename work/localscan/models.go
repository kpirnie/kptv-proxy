// work/localscan/models.go
package localscan

import (
	"fmt"
	"strings"
)

// Media type integer enum — matches the media_type column on
// kp_local_sources and kp_local_media.
const (
	MediaTypeMusic  = 0
	MediaTypeMovies = 1
	MediaTypeShows  = 2
)

// MediaTypeToInt converts a string media type to its integer representation.
var MediaTypeToInt = map[string]int{
	"music":  MediaTypeMusic,
	"movies": MediaTypeMovies,
	"shows":  MediaTypeShows,
}

// MediaTypeFromInt converts an integer media type back to its string representation.
var MediaTypeFromInt = map[int]string{
	MediaTypeMusic:  "music",
	MediaTypeMovies: "movies",
	MediaTypeShows:  "shows",
}

// Person represents a credited individual attached to a media entry.
type Person struct {
	Name string `json:"name"`
	Role string `json:"role,omitempty"`
}

// MediaEntry holds all metadata for a single local media file, derived
// primarily from the folder structure and enriched from NFO sidecars,
// embedded tags, and ffprobe.
type MediaEntry struct {
	ID            int64  `json:"id"`
	LocalSourceID int64  `json:"local_source_id"`
	Hash          string `json:"hash"`

	Path       string `json:"path"`
	MediaType  string `json:"media_type"`
	GroupTitle string `json:"group_title"`
	TVGName    string `json:"tvg_name"`
	Display    string `json:"display"`

	Duration int    `json:"duration"`
	Year     string `json:"year"`

	Artist string `json:"artist"`
	Album  string `json:"album"`
	Disc   int    `json:"disc"`
	Track  int    `json:"track"`

	Series       string `json:"series"`
	Season       int    `json:"season"`
	Episode      int    `json:"episode"`
	EpisodeTitle string `json:"episode_title"`

	Title        string   `json:"title"`
	SortTitle    string   `json:"sort_title"`
	Plot         string   `json:"plot"`
	Tagline      string   `json:"tagline"`
	Poster       string   `json:"poster"`
	Fanart       string   `json:"fanart"`
	Rating       float64  `json:"rating"`
	CriticRating int      `json:"critic_rating"`
	MPAA         string   `json:"mpaa"`
	Country      string   `json:"country"`
	Premiered    string   `json:"premiered"`
	IMDBID       string   `json:"imdb_id"`
	TMDBID       string   `json:"tmdb_id"`
	TVDBID       string   `json:"tvdb_id"`
	Collection   string   `json:"collection"`
	Genres       []string `json:"genres"`
	Studios      []string `json:"studios"`
	Tags         []string `json:"tags"`
	Directors    []string `json:"directors"`
	Writers      []string `json:"writers"`
	Cast         []Person `json:"cast"`

	ModTime  int64 `json:"mod_time"`
	FileSize int64 `json:"file_size"`

	sortKey string
}

// SortKey returns a pre-computed string that produces correct ordering when
// entries are sorted lexicographically:
//
//	music  → group / artist / album / disc (zero-padded) / track (zero-padded)
//	shows  → group / series / season (zero-padded) / episode (zero-padded)
//	movies → group / display title
func (e *MediaEntry) SortKey() string {
	if e.sortKey != "" {
		return e.sortKey
	}
	switch e.MediaType {
	case "music":
		e.sortKey = strings.Join([]string{
			e.GroupTitle,
			strings.ToLower(e.Artist),
			strings.ToLower(e.Album),
			fmt.Sprintf("%04d", e.Disc),
			fmt.Sprintf("%04d", e.Track),
			strings.ToLower(e.Display),
		}, "\x00")
	case "shows":
		e.sortKey = strings.Join([]string{
			e.GroupTitle,
			strings.ToLower(e.Series),
			fmt.Sprintf("%04d", e.Season),
			fmt.Sprintf("%04d", e.Episode),
		}, "\x00")
	default:
		e.sortKey = strings.Join([]string{
			e.GroupTitle,
			strings.ToLower(e.Display),
		}, "\x00")
	}
	return e.sortKey
}

// SetSortKey overrides the cached sort key, used when rehydrating an entry
// from the database rather than recomputing it.
func (e *MediaEntry) SetSortKey(k string) {
	e.sortKey = k
}
