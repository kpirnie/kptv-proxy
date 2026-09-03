// work/localscan/store.go
package localscan

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"kptv-proxy/work/db"
	"kptv-proxy/work/logger"
	"kptv-proxy/work/utils"
	"path/filepath"
	"strings"
)

const localMediaColumns = `
	id, local_source_id, s_hash, path, media_type, group_title, tvg_name,
	display, duration, year, artist, album, disc, track, series, season,
	episode, episode_title, title, sort_title, plot, tagline, poster,
	fanart, rating, critic_rating, mpaa, country, premiered, imdb_id,
	tmdb_id, tvdb_id, collection, genres, studios, tags, directors,
	writers, cast_json, sort_key, mod_time, file_size`

// EntryHash derives the stable stream identity hash for a local media file.
// The local source ID is folded in so the same path under two sources does
// not collide.
func EntryHash(localSourceID int64, path string) string {
	return utils.HashURL(fmt.Sprintf("%d|%s", localSourceID, path))
}

// LoadAllForSource returns every stored entry for a local source keyed by
// file path, used by the scanner to detect unchanged files.
func LoadAllForSource(localSourceID int64) (map[string]*MediaEntry, error) {
	rows, err := db.Get().Query(`SELECT `+localMediaColumns+`
		FROM kp_local_media WHERE local_source_id = ?`, localSourceID)
	if err != nil {
		logger.Error("{localscan/store - LoadAllForSource} id=%d: %v", localSourceID, err)
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]*MediaEntry)
	for rows.Next() {
		e, err := scanMediaRow(rows)
		if err != nil {
			return nil, err
		}
		out[e.Path] = e
	}
	return out, rows.Err()
}

// ListBySource returns every stored entry for a local source ordered by sort key.
func ListBySource(localSourceID int64) ([]*MediaEntry, error) {
	rows, err := db.Get().Query(`SELECT `+localMediaColumns+`
		FROM kp_local_media WHERE local_source_id = ? ORDER BY sort_key ASC`, localSourceID)
	if err != nil {
		logger.Error("{localscan/store - ListBySource} id=%d: %v", localSourceID, err)
		return nil, err
	}
	defer rows.Close()
	return scanMediaRows(rows)
}

// ListAll returns every stored local media entry across all sources,
// ordered by source sort order then entry sort key.
func ListAll() ([]*MediaEntry, error) {
	rows, err := db.Get().Query(`SELECT m.id, m.local_source_id, m.s_hash, m.path,
		m.media_type, m.group_title, m.tvg_name, m.display, m.duration, m.year,
		m.artist, m.album, m.disc, m.track, m.series, m.season, m.episode,
		m.episode_title, m.title, m.sort_title, m.plot, m.tagline, m.poster,
		m.fanart, m.rating, m.critic_rating, m.mpaa, m.country, m.premiered,
		m.imdb_id, m.tmdb_id, m.tvdb_id, m.collection, m.genres, m.studios,
		m.tags, m.directors, m.writers, m.cast_json, m.sort_key, m.mod_time,
		m.file_size
		FROM kp_local_media m
		JOIN kp_local_sources s ON s.id = m.local_source_id
		WHERE s.enabled = 1
		ORDER BY s.sort_order ASC, m.sort_key ASC`)
	if err != nil {
		logger.Error("{localscan/store - ListAll} %v", err)
		return nil, err
	}
	defer rows.Close()
	return scanMediaRows(rows)
}

// GetByHash returns a single entry by its stream identity hash.
// Returns sql.ErrNoRows if the hash is unknown.
func GetByHash(hash string) (*MediaEntry, error) {
	rows, err := db.Get().Query(`SELECT `+localMediaColumns+`
		FROM kp_local_media WHERE s_hash = ? LIMIT 1`, hash)
	if err != nil {
		logger.Error("{localscan/store - GetByHash} hash=%s: %v", hash, err)
		return nil, err
	}
	defer rows.Close()

	if !rows.Next() {
		return nil, sql.ErrNoRows
	}
	return scanMediaRow(rows)
}

// UpsertBatch writes new or changed entries for a local source in a single
// transaction, keyed on (local_source_id, path).
func UpsertBatch(localSourceID int64, entries []*MediaEntry) error {
	if len(entries) == 0 {
		return nil
	}

	tx, err := db.Get().Begin()
	if err != nil {
		logger.Error("{localscan/store - UpsertBatch} begin: %v", err)
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO kp_local_media
			(local_source_id, s_hash, path, media_type, group_title, tvg_name,
			 display, duration, year, artist, album, disc, track, series,
			 season, episode, episode_title, title, sort_title, plot, tagline,
			 poster, fanart, rating, critic_rating, mpaa, country, premiered,
			 imdb_id, tmdb_id, tvdb_id, collection, genres, studios, tags,
			 directors, writers, cast_json, sort_key, mod_time, file_size)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(local_source_id, path) DO UPDATE SET
			s_hash=excluded.s_hash, media_type=excluded.media_type,
			group_title=excluded.group_title, tvg_name=excluded.tvg_name,
			display=excluded.display, duration=excluded.duration,
			year=excluded.year, artist=excluded.artist, album=excluded.album,
			disc=excluded.disc, track=excluded.track, series=excluded.series,
			season=excluded.season, episode=excluded.episode,
			episode_title=excluded.episode_title, title=excluded.title,
			sort_title=excluded.sort_title, plot=excluded.plot,
			tagline=excluded.tagline, poster=excluded.poster,
			fanart=excluded.fanart, rating=excluded.rating,
			critic_rating=excluded.critic_rating, mpaa=excluded.mpaa,
			country=excluded.country, premiered=excluded.premiered,
			imdb_id=excluded.imdb_id, tmdb_id=excluded.tmdb_id,
			tvdb_id=excluded.tvdb_id, collection=excluded.collection,
			genres=excluded.genres, studios=excluded.studios,
			tags=excluded.tags, directors=excluded.directors,
			writers=excluded.writers, cast_json=excluded.cast_json,
			sort_key=excluded.sort_key, mod_time=excluded.mod_time,
			file_size=excluded.file_size`)
	if err != nil {
		logger.Error("{localscan/store - UpsertBatch} prepare: %v", err)
		return err
	}
	defer stmt.Close()

	for _, e := range entries {
		if _, err := stmt.Exec(
			localSourceID, EntryHash(localSourceID, e.Path), e.Path,
			MediaTypeToInt[e.MediaType], e.GroupTitle, e.TVGName, e.Display,
			e.Duration, e.Year, e.Artist, e.Album, e.Disc, e.Track,
			e.Series, e.Season, e.Episode, e.EpisodeTitle, e.Title,
			e.SortTitle, e.Plot, e.Tagline, e.Poster, e.Fanart, e.Rating,
			e.CriticRating, e.MPAA, e.Country, e.Premiered, e.IMDBID,
			e.TMDBID, e.TVDBID, e.Collection, encodeList(e.Genres),
			encodeList(e.Studios), encodeList(e.Tags), encodeList(e.Directors),
			encodeList(e.Writers), encodeCast(e.Cast), e.SortKey(),
			e.ModTime, e.FileSize,
		); err != nil {
			logger.Error("{localscan/store - UpsertBatch} exec %s: %v", e.Path, err)
			return err
		}
	}

	return tx.Commit()
}

// UpdateEntry rewrites a single stored entry, matched on its primary key.
// Used after a metadata edit re-enriches one file.
func UpdateEntry(e *MediaEntry) error {
	return UpsertBatch(e.LocalSourceID, []*MediaEntry{e})
}

// DeleteMissing removes stored entries for a local source whose paths were
// not seen during the scan that produced active.
func DeleteMissing(localSourceID int64, active map[string]struct{}) error {
	rows, err := db.Get().Query(`SELECT path FROM kp_local_media WHERE local_source_id = ?`, localSourceID)
	if err != nil {
		logger.Error("{localscan/store - DeleteMissing} query id=%d: %v", localSourceID, err)
		return err
	}

	var stale []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			rows.Close()
			return err
		}
		if _, ok := active[p]; !ok {
			stale = append(stale, p)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	if len(stale) == 0 {
		return nil
	}

	tx, err := db.Get().Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, p := range stale {
		if _, err := tx.Exec(`DELETE FROM kp_local_media WHERE local_source_id = ? AND path = ?`, localSourceID, p); err != nil {
			logger.Error("{localscan/store - DeleteMissing} delete %s: %v", p, err)
			return err
		}
	}

	logger.Debug("{localscan/store - DeleteMissing} removed %d stale entries for source %d", len(stale), localSourceID)
	return tx.Commit()
}

// DeleteAllForSource removes every stored entry belonging to a local source.
func DeleteAllForSource(localSourceID int64) error {
	_, err := db.Get().Exec(`DELETE FROM kp_local_media WHERE local_source_id = ?`, localSourceID)
	if err != nil {
		logger.Error("{localscan/store - DeleteAllForSource} id=%d: %v", localSourceID, err)
	}
	return err
}

// PathWithinSource reports whether path resolves inside the configured root of
// the given local source, after symlink resolution.
func PathWithinSource(localSourceID int64, path string) bool {
	src, err := db.GetLocalSource(localSourceID)
	if err != nil {
		return false
	}

	root, err := filepath.EvalSymlinks(src.Path)
	if err != nil {
		return false
	}
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}

	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// scanMediaRows iterates a *sql.Rows result into a MediaEntry slice.
func scanMediaRows(rows *sql.Rows) ([]*MediaEntry, error) {
	var out []*MediaEntry
	for rows.Next() {
		e, err := scanMediaRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// scanMediaRow scans the current row of a *sql.Rows into a MediaEntry.
func scanMediaRow(rows *sql.Rows) (*MediaEntry, error) {
	var (
		e                                         MediaEntry
		mediaType                                 int
		genres, studios, tags, directors, writers string
		castJSON, sortKey                         string
	)

	if err := rows.Scan(
		&e.ID, &e.LocalSourceID, &e.Hash, &e.Path, &mediaType, &e.GroupTitle,
		&e.TVGName, &e.Display, &e.Duration, &e.Year, &e.Artist, &e.Album,
		&e.Disc, &e.Track, &e.Series, &e.Season, &e.Episode, &e.EpisodeTitle,
		&e.Title, &e.SortTitle, &e.Plot, &e.Tagline, &e.Poster, &e.Fanart,
		&e.Rating, &e.CriticRating, &e.MPAA, &e.Country, &e.Premiered,
		&e.IMDBID, &e.TMDBID, &e.TVDBID, &e.Collection, &genres, &studios,
		&tags, &directors, &writers, &castJSON, &sortKey, &e.ModTime,
		&e.FileSize,
	); err != nil {
		logger.Error("{localscan/store - scanMediaRow} scan failed: %v", err)
		return nil, err
	}

	e.MediaType = MediaTypeFromInt[mediaType]
	e.Genres = decodeList(genres)
	e.Studios = decodeList(studios)
	e.Tags = decodeList(tags)
	e.Directors = decodeList(directors)
	e.Writers = decodeList(writers)
	e.Cast = decodeCast(castJSON)
	e.SetSortKey(sortKey)

	return &e, nil
}

// encodeList serialises a string slice for storage, returning an empty
// string for nil or empty input.
func encodeList(v []string) string {
	if len(v) == 0 {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

// decodeList deserialises a stored string slice, returning nil on blank
// or malformed input.
func decodeList(s string) []string {
	if s == "" {
		return nil
	}
	var v []string
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return nil
	}
	return v
}

// encodeCast serialises the cast list for storage.
func encodeCast(v []Person) string {
	if len(v) == 0 {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

// decodeCast deserialises a stored cast list, returning nil on blank
// or malformed input.
func decodeCast(s string) []Person {
	if s == "" {
		return nil
	}
	var v []Person
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return nil
	}
	return v
}
