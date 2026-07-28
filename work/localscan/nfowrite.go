// work/localscan/nfowrite.go
package localscan

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type nfoCDATA struct {
	Value string `xml:",cdata"`
}

type nfoOutActor struct {
	Name string `xml:"name"`
	Role string `xml:"role,omitempty"`
	Type string `xml:"type"`
}

type nfoOutUniqueID struct {
	Type    string `xml:"type,attr"`
	Default bool   `xml:"default,attr,omitempty"`
	Value   string `xml:",chardata"`
}

type nfoOutSet struct {
	Name string `xml:"name"`
}

type nfoOutArt struct {
	Poster string `xml:"poster,omitempty"`
	Fanart string `xml:"fanart,omitempty"`
}

type nfoOut struct {
	XMLName xml.Name

	Title         string    `xml:"title,omitempty"`
	OriginalTitle string    `xml:"originaltitle,omitempty"`
	SortTitle     string    `xml:"sorttitle,omitempty"`
	ShowTitle     string    `xml:"showtitle,omitempty"`
	Plot          *nfoCDATA `xml:"plot,omitempty"`
	Outline       *nfoCDATA `xml:"outline,omitempty"`
	Tagline       string    `xml:"tagline,omitempty"`

	Year        string `xml:"year,omitempty"`
	Premiered   string `xml:"premiered,omitempty"`
	ReleaseDate string `xml:"releasedate,omitempty"`
	Aired       string `xml:"aired,omitempty"`

	Rating       string `xml:"rating,omitempty"`
	CriticRating string `xml:"criticrating,omitempty"`
	MPAA         string `xml:"mpaa,omitempty"`
	Runtime      string `xml:"runtime,omitempty"`
	Country      string `xml:"country,omitempty"`

	Season  string `xml:"season,omitempty"`
	Episode string `xml:"episode,omitempty"`

	Artist      string `xml:"artist,omitempty"`
	AlbumArtist string `xml:"albumartist,omitempty"`
	Album       string `xml:"album,omitempty"`

	Genre    []string `xml:"genre,omitempty"`
	Studio   []string `xml:"studio,omitempty"`
	Tag      []string `xml:"tag,omitempty"`
	Director []string `xml:"director,omitempty"`
	Writer   []string `xml:"writer,omitempty"`

	Actors []nfoOutActor `xml:"actor,omitempty"`

	Set *nfoOutSet `xml:"set,omitempty"`
	Art *nfoOutArt `xml:"art,omitempty"`

	UniqueIDs []nfoOutUniqueID `xml:"uniqueid,omitempty"`
}

const nfoHeader = `<?xml version="1.0" encoding="utf-8" standalone="yes"?>` + "\n"

// NFOPath returns the sidecar path that WriteNFO targets for the entry, or an
// empty string for media types that carry no sidecar.
func NFOPath(e *MediaEntry) string {
	switch e.MediaType {
	case "movies", "shows":
		return strings.TrimSuffix(e.Path, filepath.Ext(e.Path)) + ".nfo"
	case "music":
		return filepath.Join(filepath.Dir(e.Path), "album.nfo")
	}
	return ""
}

// WriteNFO serialises the entry's extended metadata to its .nfo sidecar and
// returns the path written. The write is atomic: content lands in a temporary
// file in the same directory and is renamed into place, so a reader never
// observes a partial document and a failed write cannot destroy the existing
// sidecar.
func WriteNFO(e *MediaEntry) (string, error) {
	dest := NFOPath(e)
	if dest == "" {
		return "", fmt.Errorf("no .nfo sidecar defined for media type %q", e.MediaType)
	}

	out := buildNFOOut(e)

	body, err := xml.MarshalIndent(out, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal nfo: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(dest), ".nfo-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create temp nfo: %w", err)
	}
	tmpName := tmp.Name()

	defer func() {
		if tmpName != "" {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.WriteString(nfoHeader); err != nil {
		tmp.Close()
		return "", fmt.Errorf("write nfo: %w", err)
	}
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return "", fmt.Errorf("write nfo: %w", err)
	}
	if _, err := tmp.WriteString("\n"); err != nil {
		tmp.Close()
		return "", fmt.Errorf("write nfo: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return "", fmt.Errorf("sync nfo: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close nfo: %w", err)
	}
	if err := os.Chmod(tmpName, 0644); err != nil {
		return "", fmt.Errorf("chmod nfo: %w", err)
	}
	if err := os.Rename(tmpName, dest); err != nil {
		return "", fmt.Errorf("rename nfo: %w", err)
	}

	tmpName = ""
	return dest, nil
}

// buildNFOOut assembles the sidecar document for an entry, picking the root
// element and field mapping appropriate to its media type.
func buildNFOOut(e *MediaEntry) *nfoOut {
	root := "movie"
	switch e.MediaType {
	case "shows":
		root = "episodedetails"
	case "music":
		root = "album"
	}

	out := &nfoOut{
		XMLName:   xml.Name{Local: root},
		Title:     firstNonEmpty(e.Title, e.Display),
		SortTitle: e.SortTitle,
		Tagline:   e.Tagline,
		Year:      e.Year,
		MPAA:      e.MPAA,
		Country:   e.Country,
		Genre:     e.Genres,
		Studio:    e.Studios,
		Tag:       e.Tags,
		Director:  e.Directors,
		Writer:    e.Writers,
	}

	if e.Plot != "" {
		out.Plot = &nfoCDATA{Value: e.Plot}
	}
	if e.Tagline != "" {
		out.Outline = &nfoCDATA{Value: e.Tagline}
	}
	if e.Rating > 0 {
		out.Rating = strconv.FormatFloat(e.Rating, 'f', 1, 64)
	}
	if e.CriticRating > 0 {
		out.CriticRating = strconv.Itoa(e.CriticRating)
	}
	if e.Duration > 0 {
		out.Runtime = strconv.Itoa(e.Duration / 60)
	}

	switch e.MediaType {
	case "movies":
		out.OriginalTitle = e.Title
		out.Premiered = e.Premiered
		out.ReleaseDate = e.Premiered
		if e.Collection != "" {
			out.Set = &nfoOutSet{Name: e.Collection}
		}
	case "shows":
		out.Title = firstNonEmpty(e.EpisodeTitle, e.Title, e.Display)
		out.ShowTitle = e.Series
		out.Aired = e.Premiered
		if e.Season > 0 {
			out.Season = strconv.Itoa(e.Season)
		}
		if e.Episode > 0 {
			out.Episode = strconv.Itoa(e.Episode)
		}
	case "music":
		out.Title = firstNonEmpty(e.Album, e.Title)
		out.Album = e.Album
		out.Artist = e.Artist
		out.AlbumArtist = e.Artist
		out.ReleaseDate = e.Premiered
	}

	for _, p := range e.Cast {
		if p.Name == "" {
			continue
		}
		out.Actors = append(out.Actors, nfoOutActor{Name: p.Name, Role: p.Role, Type: "Actor"})
	}

	if id := relativeArt(e.Path, e.Poster); id != "" {
		out.Art = &nfoOutArt{Poster: id}
	}
	if id := relativeArt(e.Path, e.Fanart); id != "" {
		if out.Art == nil {
			out.Art = &nfoOutArt{}
		}
		out.Art.Fanart = id
	}

	for _, u := range []struct{ t, v string }{
		{"imdb", e.IMDBID}, {"tmdb", e.TMDBID}, {"tvdb", e.TVDBID},
	} {
		if u.v != "" {
			out.UniqueIDs = append(out.UniqueIDs, nfoOutUniqueID{Type: u.t, Value: u.v})
		}
	}

	return out
}

// relativeArt shortens an artwork path to a bare filename when it sits beside
// the media file, keeping the sidecar portable across mount points. Remote URLs
// are written verbatim.
func relativeArt(mediaPath, art string) string {
	if art == "" || isRemoteURL(art) {
		return art
	}
	if filepath.Dir(art) == filepath.Dir(mediaPath) {
		return filepath.Base(art)
	}
	return art
}

// firstNonEmpty returns the first trimmed non-blank candidate.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}
