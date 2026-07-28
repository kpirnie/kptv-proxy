// work/localscan/tagwrite.go
package localscan

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"go.senan.xyz/taglib"
)

// coverNames lists the sibling artwork filenames probed when embedding cover
// art, in preference order.
var coverNames = []string{"cover.jpg", "cover.png", "folder.jpg", "folder.png", "poster.jpg"}

// maxCoverBytes bounds an embedded cover image. Oversized artwork bloats every
// track in an album and some players refuse to decode it.
const maxCoverBytes = 5 << 20

// WriteAudioTags writes the entry's metadata into the audio file's own tag
// container, so players that read embedded tags rather than the playlist see
// the same values. Only fields with a meaningful tag mapping are written;
// existing tags not covered here are preserved.
//
// Returns an error for any media type other than music.
func WriteAudioTags(e *MediaEntry) error {
	if e.MediaType != "music" {
		return fmt.Errorf("embedded tags are only supported for music, got %q", e.MediaType)
	}

	tags := map[string][]string{}

	setTag(tags, taglib.Title, firstNonEmpty(e.Title, e.Display))
	setTag(tags, taglib.Artist, e.Artist)
	setTag(tags, taglib.AlbumArtist, e.Artist)
	setTag(tags, taglib.Album, e.Album)
	setTag(tags, taglib.Date, firstNonEmpty(e.Premiered, e.Year))
	setTag(tags, taglib.AlbumSort, e.SortTitle)
	setTag(tags, taglib.Comment, e.Plot)

	if len(e.Genres) > 0 {
		tags[taglib.Genre] = e.Genres
	}
	if len(e.Studios) > 0 {
		tags[taglib.Label] = e.Studios
	}
	if e.Disc > 0 {
		tags[taglib.DiscNumber] = []string{strconv.Itoa(e.Disc)}
	}
	if e.Track > 0 {
		tags[taglib.TrackNumber] = []string{strconv.Itoa(e.Track)}
	}

	if len(tags) == 0 {
		return fmt.Errorf("nothing to write: no mappable metadata on %s", e.Path)
	}

	// Merge rather than Clear — the entry carries only the fields this project
	// models, and a clear would discard MusicBrainz ids, ReplayGain and other
	// tags written by the user's tagger.
	if err := taglib.WriteTags(e.Path, tags, 0); err != nil {
		return fmt.Errorf("write tags: %w", err)
	}

	return nil
}

// WriteAudioCover embeds the entry's resolved poster into the audio file. It is
// a no-op returning nil when no local cover image is available.
func WriteAudioCover(e *MediaEntry) error {
	if e.MediaType != "music" {
		return fmt.Errorf("embedded cover art is only supported for music, got %q", e.MediaType)
	}

	cover := e.Poster
	if cover == "" {
		dir := filepath.Dir(e.Path)
		cover = firstExisting(dir, coverNames)
		if cover == "" {
			cover = firstExisting(filepath.Dir(dir), coverNames)
		}
	}
	if cover == "" {
		return nil
	}

	fi, err := os.Stat(cover)
	if err != nil {
		return fmt.Errorf("stat cover: %w", err)
	}
	if fi.Size() > maxCoverBytes {
		return fmt.Errorf("cover %s is %d bytes, over the %d byte limit", cover, fi.Size(), maxCoverBytes)
	}

	data, err := os.ReadFile(cover)
	if err != nil {
		return fmt.Errorf("read cover: %w", err)
	}

	if err := taglib.WriteImage(e.Path, data); err != nil {
		return fmt.Errorf("write cover: %w", err)
	}

	return nil
}

// setTag assigns a single-valued tag when the value is non-blank.
func setTag(tags map[string][]string, key, value string) {
	if value = strings.TrimSpace(value); value != "" {
		tags[key] = []string{value}
	}
}
