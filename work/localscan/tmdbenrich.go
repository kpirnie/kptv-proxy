// work/localscan/tmdbenrich.go
package localscan

import (
	"kptv-proxy/work/config"
	"kptv-proxy/work/logger"
	"kptv-proxy/work/tmdb"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// tmdbSeriesCache memoises series-level TMDB lookups for the duration of a
// single scan, keyed by lowercased series name + year. A nil entry records a
// failed search so it is not retried for every episode in that series.
type tmdbSeriesCache struct {
	mu      sync.Mutex
	entries map[string]*tmdb.TVDetails
}

var seriesCache = &tmdbSeriesCache{entries: make(map[string]*tmdb.TVDetails)}

// resetTMDBSeriesCache discards memoised series lookups. Called alongside
// ResetFSCache at the start of every scan.
func resetTMDBSeriesCache() {
	seriesCache.mu.Lock()
	seriesCache.entries = make(map[string]*tmdb.TVDetails)
	seriesCache.mu.Unlock()
}

// enrichRemote fills still-empty metadata and downloads missing poster/fanart
// art from TMDB when a global API key is configured. No-ops entirely when
// TMDB is disabled or unkeyed, and never overwrites a field already set by
// path parsing, embedded tags, or an .nfo sidecar.
func enrichRemote(e *MediaEntry) {
	cfg := config.LoadConfig()
	if !cfg.TMDBEnabled || cfg.TMDBAPIKey == "" {
		return
	}

	client := tmdb.NewClient(cfg.TMDBAPIKey)

	switch e.MediaType {
	case "movies":
		enrichMovieRemote(client, e)
	case "shows":
		enrichShowRemote(client, e)
	}
}

// enrichMovieRemote resolves the entry's own TMDB movie and applies its
// details, including poster/fanart art.
func enrichMovieRemote(client *tmdb.Client, e *MediaEntry) {
	if e.TMDBID == "" {
		result, err := tmdb.SearchMovie(client, e.Title, e.Year)
		if err != nil || result == nil {
			logger.Debug("{localscan/tmdbenrich - enrichMovieRemote} no match for %s (%s): %v", e.Title, e.Year, err)
			return
		}
		e.TMDBID = strconv.Itoa(result.ID)
	}

	id, err := strconv.Atoi(e.TMDBID)
	if err != nil {
		return
	}

	details, err := tmdb.GetMovieDetails(client, id)
	if err != nil {
		logger.Debug("{localscan/tmdbenrich - enrichMovieRemote} details failed for %d: %v", id, err)
		return
	}

	setStr(&e.IMDBID, details.IMDBID)
	setStr(&e.Plot, details.Overview)
	setStr(&e.Tagline, details.Tagline)
	if e.Rating == 0 {
		e.Rating = details.VoteAverage
	}
	if e.Duration <= 0 && details.Runtime > 0 {
		e.Duration = details.Runtime * 60
	}
	setSlice(&e.Genres, genreNames(details.Genres))
	if len(e.Cast) == 0 {
		e.Cast = castFrom(details.Credits.Cast)
	}

	downloadArt(e, details.PosterPath, details.BackdropPath)
}

// enrichShowRemote resolves the entry's series (once per series per scan,
// via seriesCache) and applies series-level details. Episode-level fields
// are left to NFO/filename parsing.
func enrichShowRemote(client *tmdb.Client, e *MediaEntry) {
	if e.Series == "" {
		return
	}

	details := lookupSeries(client, e.Series, e.Year)
	if details == nil {
		return
	}

	setStr(&e.TMDBID, strconv.Itoa(details.ID))
	setStr(&e.IMDBID, details.ExternalIDs.IMDBID)
	if details.ExternalIDs.TVDBID > 0 {
		setStr(&e.TVDBID, strconv.Itoa(details.ExternalIDs.TVDBID))
	}
	setStr(&e.Plot, details.Overview)
	if e.Rating == 0 {
		e.Rating = details.VoteAverage
	}
	setSlice(&e.Genres, genreNames(details.Genres))
	if len(e.Cast) == 0 {
		e.Cast = castFrom(details.Credits.Cast)
	}

	downloadArt(e, details.PosterPath, details.BackdropPath)
}

// lookupSeries returns cached series details for name+year, searching and
// fetching from TMDB on a cache miss.
func lookupSeries(client *tmdb.Client, name, year string) *tmdb.TVDetails {
	key := strings.ToLower(strings.TrimSpace(name)) + "|" + year

	seriesCache.mu.Lock()
	if details, ok := seriesCache.entries[key]; ok {
		seriesCache.mu.Unlock()
		return details
	}
	seriesCache.mu.Unlock()

	details := fetchSeries(client, name, year)

	seriesCache.mu.Lock()
	seriesCache.entries[key] = details
	seriesCache.mu.Unlock()

	return details
}

// fetchSeries performs the actual TMDB search and details calls for a series.
func fetchSeries(client *tmdb.Client, name, year string) *tmdb.TVDetails {
	result, err := tmdb.SearchTV(client, name, year)
	if err != nil || result == nil {
		logger.Debug("{localscan/tmdbenrich - fetchSeries} no match for %s (%s): %v", name, year, err)
		return nil
	}

	details, err := tmdb.GetTVDetails(client, result.ID)
	if err != nil {
		logger.Debug("{localscan/tmdbenrich - fetchSeries} details failed for %d: %v", result.ID, err)
		return nil
	}
	return details
}

// genreNames flattens TMDB genre references to their names.
func genreNames(genres []tmdb.Genre) []string {
	names := make([]string, 0, len(genres))
	for _, g := range genres {
		names = append(names, g.Name)
	}
	return names
}

// castFrom converts TMDB cast credits to the entry's Person representation.
func castFrom(cast []tmdb.CastMember) []Person {
	people := make([]Person, 0, len(cast))
	for _, c := range cast {
		people = append(people, Person{Name: c.Name, Role: c.Character})
	}
	return people
}

// downloadArt saves TMDB poster/backdrop images to the same directory the
// scanner already looks for local art in, using the filenames it already
// recognizes (poster.jpg/fanart.jpg) so a re-scan picks them up as local
// art. tmdb.DownloadImage itself skips the request when the file already
// exists on disk.
func downloadArt(e *MediaEntry, posterPath, backdropPath string) {
	dir := artDir(e)
	if dir == "" {
		return
	}

	if e.Poster == "" && posterPath != "" {
		dest := filepath.Join(dir, "poster.jpg")
		if err := tmdb.DownloadImage(posterPath, "poster", dest); err != nil {
			logger.Debug("{localscan/tmdbenrich - downloadArt} poster download failed for %s: %v", e.Path, err)
		} else {
			e.Poster = dest
		}
	}

	if e.Fanart == "" && backdropPath != "" {
		dest := filepath.Join(dir, "fanart.jpg")
		if err := tmdb.DownloadImage(backdropPath, "backdrop", dest); err != nil {
			logger.Debug("{localscan/tmdbenrich - downloadArt} fanart download failed for %s: %v", e.Path, err)
		} else {
			e.Fanart = dest
		}
	}
}

// artDir returns the directory a downloaded poster/fanart should live in:
// the series folder for shows, the movie's own folder otherwise.
func artDir(e *MediaEntry) string {
	if e.MediaType == "shows" {
		return findSeriesDir(e.Path)
	}
	return filepath.Dir(e.Path)
}
