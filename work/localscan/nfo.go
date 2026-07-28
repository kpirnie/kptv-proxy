// work/localscan/nfo.go
package localscan

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// nfoActor mirrors an <actor> node from a Jellyfin/Kodi .nfo file.
type nfoActor struct {
	Name string `xml:"name"`
	Role string `xml:"role"`
	Type string `xml:"type"`
}

// nfoUniqueID mirrors a <uniqueid type="..."> node.
type nfoUniqueID struct {
	Type  string `xml:"type,attr"`
	Value string `xml:",chardata"`
}

// nfoData is a permissive union of the Jellyfin and Kodi .nfo schemas, covering
// the <movie>, <episodedetails>, <tvshow> and <album> root elements. All
// numeric values are decoded as strings and parsed separately: a single empty
// element such as <rating></rating> would otherwise fail the entire document.
type nfoData struct {
	Title         string `xml:"title"`
	OriginalTitle string `xml:"originaltitle"`
	SortTitle     string `xml:"sorttitle"`
	ShowTitle     string `xml:"showtitle"`
	Plot          string `xml:"plot"`
	Outline       string `xml:"outline"`
	Review        string `xml:"review"`
	Tagline       string `xml:"tagline"`

	Year        string `xml:"year"`
	Premiered   string `xml:"premiered"`
	ReleaseDate string `xml:"releasedate"`
	Aired       string `xml:"aired"`

	Rating       string `xml:"rating"`
	CriticRating string `xml:"criticrating"`
	MPAA         string `xml:"mpaa"`
	Runtime      string `xml:"runtime"`
	Country      string `xml:"country"`

	Season  string `xml:"season"`
	Episode string `xml:"episode"`

	Artist      string `xml:"artist"`
	AlbumArtist string `xml:"albumartist"`

	Genre    []string `xml:"genre"`
	Studio   []string `xml:"studio"`
	Label    []string `xml:"label"`
	Tag      []string `xml:"tag"`
	Director []string `xml:"director"`
	Writer   []string `xml:"writer"`
	Credits  []string `xml:"credits"`

	Actors []nfoActor `xml:"actor"`

	IMDBID string `xml:"imdbid"`
	TMDBID string `xml:"tmdbid"`
	TVDBID string `xml:"tvdbid"`

	UniqueIDs []nfoUniqueID `xml:"uniqueid"`

	Set struct {
		Name string `xml:"name"`
	} `xml:"set"`

	Art struct {
		Poster string `xml:"poster"`
		Fanart string `xml:"fanart"`
		Thumb  string `xml:"thumb"`
	} `xml:"art"`

	FileInfo struct {
		StreamDetails struct {
			Video struct {
				DurationInSeconds string `xml:"durationinseconds"`
			} `xml:"video"`
		} `xml:"streamdetails"`
	} `xml:"fileinfo"`
}

var (
	posterNames = []string{"poster.jpg", "poster.png", "folder.jpg", "folder.png",
		"cover.jpg", "cover.png", "movie.jpg", "default.jpg"}
	fanartNames = []string{"fanart.jpg", "fanart.png", "backdrop.jpg", "background.jpg"}
	posterExts  = []string{".jpg", ".jpeg", ".png", ".webp"}
)

// enrichNFO fills extended metadata on the entry from .nfo sidecars and sibling
// artwork files. Movies and shows read a sibling <basename>.nfo; shows also
// merge series-level values from tvshow.nfo; music reads an album-level
// album.nfo. Enrichment is additive — existing values are never overwritten.
//
// Silently no-ops when no sidecar is present or the XML is malformed.
func enrichNFO(e *MediaEntry) {
	switch e.MediaType {
	case "movies":
		if n, ok := readNFO(siblingNFO(e.Path)); ok {
			applyNFO(e, n)
		} else if n, ok := readNFO(filepath.Join(filepath.Dir(e.Path), "movie.nfo")); ok {
			applyNFO(e, n)
		}

	case "shows":
		if n, ok := readNFO(siblingNFO(e.Path)); ok {
			applyEpisodeNFO(e, n)
		}
		if n, ok := findSeriesNFO(e.Path); ok {
			applyNFO(e, n)
		}

	case "music":
		dir := filepath.Dir(e.Path)
		if n, ok := readNFO(filepath.Join(dir, "album.nfo")); ok {
			applyNFO(e, n)
		} else if n, ok := readNFO(filepath.Join(filepath.Dir(dir), "album.nfo")); ok {
			applyNFO(e, n)
		}
	}

	findArtwork(e)
}

// siblingNFO returns the <basename>.nfo path alongside a media file.
func siblingNFO(path string) string {
	return strings.TrimSuffix(path, filepath.Ext(path)) + ".nfo"
}

// readNFO returns a parsed sidecar, memoised for the current scan. A nil result
// is cached too — a missing sidecar is the common case and is otherwise probed
// once per file in the directory.
func readNFO(path string) (*nfoData, bool) {
	if n, ok := fsCache.getNFO(path); ok {
		return n, n != nil
	}

	n, ok := readNFOUncached(path)
	if !ok {
		n = nil
	}
	fsCache.putNFO(path, n)
	return n, n != nil
}

// readNFOUncached reads and unmarshals a sidecar directly from disk.
func readNFOUncached(path string) (*nfoData, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}

	var n nfoData
	if err := xml.Unmarshal(data, &n); err != nil {
		return nil, false
	}
	return &n, true
}

// findSeriesNFO walks up from the episode's directory looking for tvshow.nfo.
// The depth limit keeps a misconfigured scan root from walking to the
// filesystem root on every single episode.
func findSeriesNFO(path string) (*nfoData, bool) {
	dir := filepath.Dir(path)
	for i := 0; i < 4; i++ {
		if n, ok := readNFO(filepath.Join(dir, "tvshow.nfo")); ok {
			return n, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return nil, false
}

// applyEpisodeNFO applies episode-level values, then rebuilds the show display
// label if the episode title was newly discovered.
func applyEpisodeNFO(e *MediaEntry, n *nfoData) {
	if e.EpisodeTitle == "" && n.Title != "" {
		e.EpisodeTitle = strings.TrimSpace(n.Title)
		rebuildShowDisplay(e)
	}
	if e.Series == "" && n.ShowTitle != "" {
		e.Series = strings.TrimSpace(n.ShowTitle)
	}
	applyNFO(e, n)
}

// applyNFO copies values onto the entry, filling only fields that are unset.
func applyNFO(e *MediaEntry, n *nfoData) {
	setStr(&e.Title, n.Title, n.OriginalTitle)
	setStr(&e.SortTitle, n.SortTitle)
	setStr(&e.Plot, n.Plot, n.Review)
	setStr(&e.Tagline, n.Tagline, n.Outline)
	setStr(&e.MPAA, n.MPAA)
	setStr(&e.Country, n.Country)
	setStr(&e.Premiered, n.Premiered, n.ReleaseDate, n.Aired)
	setStr(&e.Collection, n.Set.Name)
	setStr(&e.Artist, n.AlbumArtist, n.Artist)

	if e.Year == "" {
		for _, v := range []string{n.Aired, n.Premiered, n.ReleaseDate, n.Year} {
			if len(v) >= 4 {
				e.Year = v[:4]
				break
			}
		}
	}

	if e.Rating == 0 {
		if f, err := strconv.ParseFloat(strings.TrimSpace(n.Rating), 64); err == nil {
			e.Rating = f
		}
	}
	if e.CriticRating == 0 {
		if i, err := strconv.Atoi(strings.TrimSpace(n.CriticRating)); err == nil {
			e.CriticRating = i
		}
	}

	if e.Duration <= 0 {
		if s, err := strconv.Atoi(strings.TrimSpace(n.FileInfo.StreamDetails.Video.DurationInSeconds)); err == nil && s > 0 {
			e.Duration = s
		} else if m, err := strconv.Atoi(strings.TrimSpace(n.Runtime)); err == nil && m > 0 {
			e.Duration = m * 60
		}
	}

	setSlice(&e.Genres, n.Genre)
	setSlice(&e.Studios, n.Studio, n.Label)
	setSlice(&e.Tags, n.Tag)
	setSlice(&e.Directors, n.Director)
	setSlice(&e.Writers, n.Writer, n.Credits)

	if len(e.Cast) == 0 {
		for _, a := range n.Actors {
			name := strings.TrimSpace(a.Name)
			if name == "" || !strings.EqualFold(a.Type, "Actor") {
				continue
			}
			e.Cast = append(e.Cast, Person{Name: name, Role: strings.TrimSpace(a.Role)})
		}
	}

	setStr(&e.IMDBID, n.IMDBID)
	setStr(&e.TMDBID, n.TMDBID)
	setStr(&e.TVDBID, n.TVDBID)

	for _, u := range n.UniqueIDs {
		v := strings.TrimSpace(u.Value)
		if v == "" {
			continue
		}
		switch strings.ToLower(u.Type) {
		case "imdb":
			setStr(&e.IMDBID, v)
		case "tmdb":
			setStr(&e.TMDBID, v)
		case "tvdb":
			setStr(&e.TVDBID, v)
		}
	}

	setStr(&e.Poster, resolveArtPath(e.Path, n.Art.Poster), resolveArtPath(e.Path, n.Art.Thumb))
	setStr(&e.Fanart, resolveArtPath(e.Path, n.Art.Fanart))
}

// resolveArtPath normalises an <art> value. Remote URLs pass through unchanged;
// local paths are made absolute and discarded when they do not exist on disk.
func resolveArtPath(mediaPath, art string) string {
	art = strings.TrimSpace(art)
	if art == "" {
		return ""
	}
	if isRemoteURL(art) {
		return art
	}
	if !filepath.IsAbs(art) {
		art = filepath.Join(filepath.Dir(mediaPath), art)
	}
	if fi, err := os.Stat(art); err != nil || fi.IsDir() {
		return ""
	}
	return art
}

// isRemoteURL reports whether the value is an http(s) URL rather than a path.
func isRemoteURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

// findArtwork probes for sibling poster and backdrop images when the .nfo did
// not supply usable art. Music looks one directory up as well, so tracks in a
// Disc N subfolder still resolve to the album cover.
func findArtwork(e *MediaEntry) {
	dir := filepath.Dir(e.Path)
	base := strings.TrimSuffix(filepath.Base(e.Path), filepath.Ext(e.Path))

	if e.Poster == "" {
		for _, ext := range posterExts {
			if p := existingFile(filepath.Join(dir, base+"-poster"+ext)); p != "" {
				e.Poster = p
				break
			}
			if p := existingFile(filepath.Join(dir, base+ext)); p != "" {
				e.Poster = p
				break
			}
		}
	}
	if e.Poster == "" {
		e.Poster = firstExisting(dir, posterNames)
	}
	if e.Poster == "" && e.MediaType == "music" {
		e.Poster = firstExisting(filepath.Dir(dir), posterNames)
	}

	if e.Fanart == "" {
		e.Fanart = firstExisting(dir, fanartNames)
	}
}

// firstExisting returns the first candidate name that resolves to a file in dir.
func firstExisting(dir string, names []string) string {
	for _, n := range names {
		if p := existingFile(filepath.Join(dir, n)); p != "" {
			return p
		}
	}
	return ""
}

// existingFile returns path when it resolves to a regular file, memoised for
// the current scan.
func existingFile(path string) string {
	if v, ok := fsCache.getFile(path); ok {
		return v
	}

	resolved := ""
	if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
		resolved = path
	}
	fsCache.putFile(path, resolved)
	return resolved
}

// setStr assigns the first non-empty candidate when the target is unset.
func setStr(dst *string, candidates ...string) {
	if *dst != "" {
		return
	}
	for _, c := range candidates {
		if c = strings.TrimSpace(c); c != "" {
			*dst = c
			return
		}
	}
}

// setSlice assigns the first non-empty candidate slice when the target is unset.
func setSlice(dst *[]string, candidates ...[]string) {
	if len(*dst) > 0 {
		return
	}
	for _, c := range candidates {
		out := make([]string, 0, len(c))
		for _, v := range c {
			if v = strings.TrimSpace(v); v != "" {
				out = append(out, v)
			}
		}
		if len(out) > 0 {
			*dst = out
			return
		}
	}
}

// rebuildShowDisplay reconstructs Display and TVGName after episode title enrichment.
func rebuildShowDisplay(e *MediaEntry) {
	epStr := ""
	if e.Season > 0 && e.Episode > 0 {
		epStr = fmt.Sprintf("S%02dE%02d", e.Season, e.Episode)
	}

	if epStr != "" && e.EpisodeTitle != "" {
		e.Display = fmt.Sprintf("%s - %s - %s", e.Series, epStr, e.EpisodeTitle)
	} else if epStr != "" {
		e.Display = fmt.Sprintf("%s - %s", e.Series, epStr)
	} else {
		e.Display = e.Series
	}
	e.TVGName = e.Display
}
