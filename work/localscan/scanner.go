// work/localscan/scanner.go
package localscan

import (
	"fmt"
	"io/fs"
	"kptv-proxy/work/db"
	"kptv-proxy/work/logger"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/grafana/regexp"
)

// Enricher is the optional metadata enrichment hook applied to every new or
// changed entry during a scan. Left nil, scanning is path-parse only.
type Enricher func(e *MediaEntry)

// Scanner performs recursive media file discovery for a single local source.
// The source's configured path is treated as the root for its media type.
type Scanner struct {
	Source db.LocalSource
	Enrich Enricher
}

// NewScanner constructs a Scanner for the given local source.
func NewScanner(src db.LocalSource, enrich Enricher) *Scanner {
	return &Scanner{Source: src, Enrich: enrich}
}

// Scan walks the source path, upserts new and changed files, evicts entries
// for files that no longer exist, and records the scan on the source row.
// Returns the total number of entries now stored for the source.
func (s *Scanner) Scan() (int, error) {

	// reset the filesystem scanner cache
	ResetFSCache()
	resetTMDBSeriesCache()

	mediaType := MediaTypeFromInt[s.Source.MediaType]
	if mediaType == "" {
		return 0, fmt.Errorf("unknown media type %d on source %d", s.Source.MediaType, s.Source.ID)
	}

	root, err := filepath.Abs(s.Source.Path)
	if err != nil {
		return 0, err
	}
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		return 0, fmt.Errorf("local source path is not a readable directory: %s", root)
	}

	incRE, excRE, err := compileFilters(s.Source.IncRegex, s.Source.ExcRegex)
	if err != nil {
		return 0, err
	}

	logger.Info("{localscan/scanner - Scan} scanning %s (%s) → %s", s.Source.Name, mediaType, root)

	cached, err := LoadAllForSource(s.Source.ID)
	if err != nil {
		logger.Error("{localscan/scanner - Scan} cache load failed, proceeding without: %v", err)
		cached = make(map[string]*MediaEntry)
	}

	files := s.collect(root, mediaType, incRE, excRE)

	active := make(map[string]struct{}, len(files))
	var changed []string
	total := 0

	for _, path := range files {
		active[path] = struct{}{}
		mtime, size, err := statFile(path)
		if err != nil {
			logger.Debug("{localscan/scanner - Scan} stat error %s: %v", path, err)
			continue
		}
		total++
		prev := cached[path]
		if prev == nil || prev.ModTime != mtime || prev.FileSize != size {
			changed = append(changed, path)
		}
	}

	logger.Debug("{localscan/scanner - Scan} %s: %d total, %d new/changed", s.Source.Name, total, len(changed))

	entries := s.process(changed, mediaType, root)

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].SortKey() < entries[j].SortKey()
	})

	if err := UpsertBatch(s.Source.ID, entries); err != nil {
		return 0, err
	}
	if err := DeleteMissing(s.Source.ID, active); err != nil {
		logger.Error("{localscan/scanner - Scan} eviction failed: %v", err)
	}
	if err := db.TouchLocalSourceScan(s.Source.ID, time.Now().Unix(), total); err != nil {
		logger.Error("{localscan/scanner - Scan} bookkeeping failed: %v", err)
	}

	logger.Info("{localscan/scanner - Scan} %s complete: %d entries", s.Source.Name, total)
	return total, nil
}

// ScanFile re-parses and re-enriches a single file already belonging to this
// source, writing the rebuilt entry back to storage.
func (s *Scanner) ScanFile(path string) (*MediaEntry, error) {

	// reset the filesystem scanner cache
	ResetFSCache()
	resetTMDBSeriesCache()

	mediaType := MediaTypeFromInt[s.Source.MediaType]
	root, err := filepath.Abs(s.Source.Path)
	if err != nil {
		return nil, err
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return nil, fmt.Errorf("path is not under local source %d: %s", s.Source.ID, abs)
	}

	entry := s.parseFile(abs, mediaType, root)
	if entry == nil {
		return nil, fmt.Errorf("could not parse %s as %s", abs, mediaType)
	}
	s.enrichSafe(entry)

	entry.LocalSourceID = s.Source.ID
	entry.Hash = EntryHash(s.Source.ID, abs)
	entry.ModTime, entry.FileSize, _ = statFile(abs)

	if err := UpsertBatch(s.Source.ID, []*MediaEntry{entry}); err != nil {
		return nil, err
	}
	return entry, nil
}

// collect walks the root and returns every file matching the media type's
// extension set and the source's include/exclude filters.
func (s *Scanner) collect(root, mediaType string, incRE, excRE *regexp.Regexp) []string {
	exts := ExtensionsByType[mediaType]

	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			logger.Debug("{localscan/scanner - collect} walk error at %s: %v", path, err)
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !exts[strings.ToLower(filepath.Ext(path))] {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		if incRE != nil && !incRE.MatchString(rel) {
			return nil
		}
		if excRE != nil && excRE.MatchString(rel) {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		logger.Error("{localscan/scanner - collect} walk failed for %s: %v", root, err)
	}
	return files
}

// process parses and enriches the given paths concurrently.
func (s *Scanner) process(paths []string, mediaType, root string) []*MediaEntry {
	if len(paths) == 0 {
		return nil
	}

	workers := runtime.NumCPU() * 2
	jobs := make(chan string, workers)
	results := make(chan *MediaEntry, workers)

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range jobs {
				entry := s.parseFile(path, mediaType, root)
				if entry == nil {
					continue
				}
				s.enrichSafe(entry)
				entry.LocalSourceID = s.Source.ID
				entry.Hash = EntryHash(s.Source.ID, path)
				entry.ModTime, entry.FileSize, _ = statFile(path)
				results <- entry
			}
		}()
	}

	go func() {
		for _, p := range paths {
			jobs <- p
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()

	out := make([]*MediaEntry, 0, len(paths))
	for e := range results {
		out = append(out, e)
	}
	return out
}

// parseFile dispatches to the media-type specific path parser, recovering
// from any panic so one malformed path cannot abort a scan.
func (s *Scanner) parseFile(path, mediaType, root string) (entry *MediaEntry) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("{localscan/scanner - parseFile} panic parsing %s: %v", path, r)
			entry = nil
		}
	}()

	switch mediaType {
	case "music":
		return ParseMusic(path, root)
	case "shows":
		return ParseShow(path, root)
	case "movies":
		return ParseMovie(path)
	}
	return nil
}

// compileFilters compiles the source's include and exclude patterns,
// returning nil for either when the pattern is blank.
func compileFilters(inc, exc string) (*regexp.Regexp, *regexp.Regexp, error) {
	var incRE, excRE *regexp.Regexp
	var err error

	if inc != "" {
		if incRE, err = regexp.Compile(inc); err != nil {
			return nil, nil, fmt.Errorf("invalid include regex: %w", err)
		}
	}
	if exc != "" {
		if excRE, err = regexp.Compile(exc); err != nil {
			return nil, nil, fmt.Errorf("invalid exclude regex: %w", err)
		}
	}
	return incRE, excRE, nil
}

// statFile returns the modification time and size of a file.
func statFile(path string) (int64, int64, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, 0, err
	}
	return fi.ModTime().Unix(), fi.Size(), nil
}

// ScanSource runs a scan for a single local source ID.
func ScanSource(id int64, enrich Enricher) (int, error) {
	src, err := db.GetLocalSource(id)
	if err != nil {
		return 0, err
	}
	return NewScanner(src, enrich).Scan()
}

// ScanAll runs a scan across every enabled local source, returning the total
// entry count. Individual source failures are logged and skipped.
func ScanAll(enrich Enricher) (int, error) {
	sources, err := db.GetEnabledLocalSources()
	if err != nil {
		return 0, err
	}

	total := 0
	for _, src := range sources {
		n, err := NewScanner(src, enrich).Scan()
		if err != nil {
			logger.Error("{localscan/scanner - ScanAll} source %s failed: %v", src.Name, err)
			continue
		}
		total += n
	}
	return total, nil
}

// enrichSafe applies the enrichment hook, recovering from any panic so one
// malformed sidecar, tag block, or image header cannot abort a scan.
func (s *Scanner) enrichSafe(e *MediaEntry) {
	if s.Enrich == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			logger.Error("{localscan/scanner - enrichSafe} panic enriching %s: %v", e.Path, r)
		}
	}()
	s.Enrich(e)
}
