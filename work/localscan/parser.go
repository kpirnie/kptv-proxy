// work/localscan/parser.go
package localscan

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/grafana/regexp"
)

var discFolderRE = regexp.MustCompile(`(?i)^(?:Dis[ck]|CD)\s*(\d+)$`)

var seasonFolderRE = regexp.MustCompile(`(?i)^(?:Season|Series|S)\s*(\d+)$`)

var trackRE = regexp.MustCompile(`^(\d+)\s*[-\.]\s*(.+)$`)

var discTrackRE = regexp.MustCompile(`^(\d+)-(\d+)\s+(.+)$`)

var episodeRE = regexp.MustCompile(`(?i)S(\d{1,2})E(\d{1,2})`)

var movieYearRE = regexp.MustCompile(`[\.\s\(](\d{4})[\.\s\)]`)

// ParseMusic parses a music file using the known path structure:
//
//	<musicRoot> / Albums / <Genre> / <Artist> / <Album> / [Disk N /] <file>
func ParseMusic(filePath, musicRoot string) *MediaEntry {
	rel, _ := filepath.Rel(musicRoot, filePath)
	parts := strings.Split(filepath.ToSlash(rel), "/")

	genre := safeGet(parts, 1, "Unknown Genre")
	artist := safeGet(parts, 2, "Unknown Artist")
	album := safeGet(parts, 3, "Unknown Album")

	disc := 0
	if len(parts) >= 6 {
		if m := discFolderRE.FindStringSubmatch(parts[4]); m != nil {
			disc, _ = strconv.Atoi(m[1])
		}
	}

	stem := strings.TrimSuffix(filepath.Base(filePath), filepath.Ext(filePath))
	track := 0
	title := stem

	if m := discTrackRE.FindStringSubmatch(stem); m != nil {
		if disc == 0 {
			disc, _ = strconv.Atoi(m[1])
		}
		track, _ = strconv.Atoi(m[2])
		title = strings.TrimSpace(m[3])
	} else if m := trackRE.FindStringSubmatch(stem); m != nil {
		track, _ = strconv.Atoi(m[1])
		title = strings.TrimSpace(m[2])
	}

	display := fmt.Sprintf("%s - %s", artist, title)

	return &MediaEntry{
		Path:       filePath,
		MediaType:  "music",
		GroupTitle: "Music/" + genre + "/" + artist,
		TVGName:    display,
		Display:    display,
		Duration:   -1,
		Artist:     artist,
		Genres:     nonEmpty(genre),
		Album:      album,
		Disc:       disc,
		Track:      track,
	}
}

// ParseShow parses a show file using the known path structure:
//
//	<showsRoot> / <Show Name> / <Season N> / <file>
func ParseShow(filePath, showsRoot string) *MediaEntry {
	rel, _ := filepath.Rel(showsRoot, filePath)
	parts := strings.Split(filepath.ToSlash(rel), "/")

	series := safeGet(parts, 0, "Unknown Show")
	season := 0
	if len(parts) > 1 {
		if m := seasonFolderRE.FindStringSubmatch(parts[1]); m != nil {
			season, _ = strconv.Atoi(m[1])
		}
	}

	stem := strings.TrimSuffix(filepath.Base(filePath), filepath.Ext(filePath))
	episode := 0
	if m := episodeRE.FindStringSubmatch(stem); m != nil {
		if season == 0 {
			season, _ = strconv.Atoi(m[1])
		}
		episode, _ = strconv.Atoi(m[2])
	}

	epStr := buildEpStr(season, episode, stem)
	display := fmt.Sprintf("%s - %s", series, epStr)

	return &MediaEntry{
		Path:       filePath,
		MediaType:  "shows",
		GroupTitle: "Shows/" + series,
		TVGName:    display,
		Display:    display,
		Duration:   -1,
		Series:     series,
		Season:     season,
		Episode:    episode,
	}
}

// ParseMovie parses a movie file using the filename only.
// Title and optional year are extracted from the filename stem.
func ParseMovie(filePath string) *MediaEntry {
	stem := strings.TrimSuffix(filepath.Base(filePath), filepath.Ext(filePath))

	year := ""
	if m := movieYearRE.FindStringSubmatch(stem); m != nil {
		year = m[1]
	}

	title := cleanMovieTitle(stem)
	display := title
	if year != "" {
		display = fmt.Sprintf("%s (%s)", title, year)
	}

	return &MediaEntry{
		Path:       filePath,
		MediaType:  "movies",
		GroupTitle: "Movies",
		TVGName:    display,
		Display:    display,
		Duration:   -1,
		Year:       year,
	}
}

// safeGet returns the path segment at idx, or the fallback when the segment
// is missing or blank.
func safeGet(parts []string, idx int, fallback string) string {
	if idx < len(parts) && parts[idx] != "" {
		return parts[idx]
	}
	return fallback
}

// buildEpStr formats a SxxExx label, falling back to the filename stem when
// either season or episode could not be determined.
func buildEpStr(season, episode int, fallbackStem string) string {
	if season > 0 && episode > 0 {
		return fmt.Sprintf("S%02dE%02d", season, episode)
	}
	return fallbackStem
}

// cleanMovieTitle converts dot/underscore-separated filenames into title case
// and strips the year and common release-group suffixes.
func cleanMovieTitle(stem string) string {
	cleaned := movieYearRE.ReplaceAllString(stem, " ")
	cleaned = strings.NewReplacer(".", " ", "_", " ").Replace(cleaned)
	fields := strings.Fields(cleaned)
	for i, f := range fields {
		if len(f) > 0 {
			fields[i] = strings.ToUpper(f[:1]) + f[1:]
		}
	}
	return strings.Join(fields, " ")
}

// nonEmpty returns a single-element slice, or nil when the value is blank.
func nonEmpty(s string) []string {
	if s == "" {
		return nil
	}
	return []string{s}
}
