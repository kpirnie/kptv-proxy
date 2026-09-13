// work/logos/store.go
package logos

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"kptv-proxy/work/constants"
	"kptv-proxy/work/logger"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	mu       sync.RWMutex
	cacheTTL time.Duration
	fetchMu  sync.Map
	fetcher  = &http.Client{Timeout: constants.Internal.LogoFetchTimeout}
)

// Init creates the logo directories and sets the TTL applied to cached remote
// logos. It is safe to call again on a config reload to pick up a new TTL.
func Init(ttl time.Duration) error {
	if err := os.MkdirAll(constants.Internal.LogoPath, 0755); err != nil {
		logger.Error("{logos/store - Init} failed to create logo directory: %v", err)
		return err
	}
	if err := os.MkdirAll(constants.Internal.LogoCachePath, 0755); err != nil {
		logger.Error("{logos/store - Init} failed to create logo cache directory: %v", err)
		return err
	}

	mu.Lock()
	cacheTTL = ttl
	mu.Unlock()

	logger.Debug("{logos/store - Init} logo store ready, cache ttl %s", ttl)
	return nil
}

// HashBytes returns the content hash used as the on-disk name of an upload.
func HashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// HashURL returns the cache hash used as the on-disk name of a remote logo.
func HashURL(url string) string {
	sum := sha256.Sum256([]byte(url))
	return hex.EncodeToString(sum[:])
}

// ValidHash reports whether a client-supplied hash is a plain sha256 hex digest.
// Every serving path runs this before touching the filesystem — the hash lands
// in a file path, so anything else is a traversal attempt.
func ValidHash(hash string) bool {
	if len(hash) != 64 {
		return false
	}
	if _, err := hex.DecodeString(hash); err != nil {
		return false
	}
	return true
}

// StoreUpload writes an uploaded logo to the library directory and returns its
// content hash. Non-image payloads and oversized uploads are rejected.
func StoreUpload(data []byte) (string, error) {
	if int64(len(data)) > constants.Internal.LogoMaxBytes {
		return "", fmt.Errorf("logo exceeds %d bytes", constants.Internal.LogoMaxBytes)
	}
	if !strings.HasPrefix(http.DetectContentType(data), "image/") {
		return "", errors.New("payload is not an image")
	}

	hash := HashBytes(data)
	if err := writeFile(filepath.Join(constants.Internal.LogoPath, hash), data); err != nil {
		logger.Error("{logos/store - StoreUpload} write failed: %v", err)
		return "", err
	}

	logger.Debug("{logos/store - StoreUpload} stored upload %s", hash)
	return hash, nil
}

// EnsureCached returns the cache hash for a remote logo URL, fetching and
// writing it to disk when there is no fresh copy. A stale copy is preferred
// over a failed fetch, matching the series-info cache policy.
func EnsureCached(url string) (string, error) {
	hash := HashURL(url)
	target := filepath.Join(constants.Internal.LogoCachePath, hash)

	mu.RLock()
	ttl := cacheTTL
	mu.RUnlock()

	if info, err := os.Stat(target); err == nil && time.Since(info.ModTime()) <= ttl {
		return hash, nil
	}

	// one fetch per URL — a cold cache plus a full channel list would otherwise
	// hammer the provider CDN with duplicate requests for the same image
	lk, _ := fetchMu.LoadOrStore(hash, &sync.Mutex{})
	lock := lk.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()

	if info, err := os.Stat(target); err == nil && time.Since(info.ModTime()) <= ttl {
		return hash, nil
	}

	data, err := fetch(url)
	if err != nil {
		if _, serr := os.Stat(target); serr == nil {
			logger.Debug("{logos/store - EnsureCached} serving stale logo for %s: %v", url, err)
			return hash, nil
		}
		return "", err
	}

	if err := writeFile(target, data); err != nil {
		logger.Error("{logos/store - EnsureCached} write failed: %v", err)
		return "", err
	}

	logger.Debug("{logos/store - EnsureCached} cached logo %s", hash)
	return hash, nil
}

// Open returns an open handle to a stored logo, looking in the library
// directory first and the remote cache second.
func Open(hash string) (*os.File, os.FileInfo, error) {
	if !ValidHash(hash) {
		return nil, nil, errors.New("invalid logo hash")
	}

	for _, dir := range []string{constants.Internal.LogoPath, constants.Internal.LogoCachePath} {
		f, err := os.Open(filepath.Join(dir, hash))
		if err != nil {
			continue
		}

		fi, err := f.Stat()
		if err != nil || fi.IsDir() {
			f.Close()
			continue
		}
		return f, fi, nil
	}

	return nil, nil, errors.New("logo not found")
}

// Library returns the hashes of every uploaded logo, newest first, for the
// admin logo picker.
func Library() []string {
	entries, err := os.ReadDir(constants.Internal.LogoPath)
	if err != nil {
		logger.Error("{logos/store - Library} read dir failed: %v", err)
		return nil
	}

	type libEntry struct {
		hash string
		mod  time.Time
	}

	var items []libEntry
	for _, e := range entries {
		if e.IsDir() || !ValidHash(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		items = append(items, libEntry{hash: e.Name(), mod: info.ModTime()})
	}

	sort.Slice(items, func(i, j int) bool { return items[i].mod.After(items[j].mod) })

	hashes := make([]string, 0, len(items))
	for _, it := range items {
		hashes = append(hashes, it.hash)
	}
	return hashes
}

// fetch retrieves a remote logo, bounding the body read so a misbehaving
// provider cannot stream an unbounded payload into the cache directory.
func fetch(url string) ([]byte, error) {
	resp, err := fetcher.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("logo fetch returned %d", resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, constants.Internal.LogoMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > constants.Internal.LogoMaxBytes {
		return nil, fmt.Errorf("logo exceeds %d bytes", constants.Internal.LogoMaxBytes)
	}
	if !strings.HasPrefix(http.DetectContentType(data), "image/") {
		return nil, errors.New("fetched payload is not an image")
	}

	return data, nil
}

// writeFile writes logo bytes using atomic temp+rename so a reader never sees a
// partially written image.
func writeFile(target string, data []byte) error {
	dir := filepath.Dir(target)

	tmp, err := os.CreateTemp(dir, "logo-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}

	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}

	if err := os.Rename(tmpName, target); err != nil {
		os.Remove(tmpName)
		return err
	}

	return nil
}
