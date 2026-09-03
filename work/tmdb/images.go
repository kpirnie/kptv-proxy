// work/tmdb/images.go
package tmdb

import (
	"context"
	"fmt"
	"io"
	"kptv-proxy/work/constants"
	"net/http"
	"os"
)

// imageClient bounds image downloads; the package-level default client used
// previously had no timeout at any phase.
var imageClient = &http.Client{Timeout: constants.Internal.TMDBImageTimeout}

// ImageURL builds a fully-qualified TMDB image URL for a poster or backdrop
// path, using the configured size for the given kind. kind is "poster" or
// "backdrop". Returns "" when path is empty.
func ImageURL(path, kind string) string {
	if path == "" {
		return ""
	}
	size := constants.Internal.TMDBPosterSize
	if kind == "backdrop" {
		size = constants.Internal.TMDBBackdropSize
	}
	return fmt.Sprintf("%s/%s%s", constants.Internal.TMDBImageBaseUrl, size, path)
}

// DownloadImage saves the TMDB image at tmdbPath to destFile, skipping the
// request entirely when destFile already exists on disk. tmdbPath is the raw
// file_path value from a TMDB response (e.g. "/abc123.jpg"); kind is
// "poster" or "backdrop".
func DownloadImage(tmdbPath, kind, destFile string) error {
	if tmdbPath == "" {
		return fmt.Errorf("empty image path")
	}
	if _, err := os.Stat(destFile); err == nil {
		return nil
	}

	url := ImageURL(tmdbPath, kind)

	ctx, cancel := context.WithTimeout(context.Background(), constants.Internal.TMDBImageTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build request for %s: %w", url, err)
	}

	resp, err := imageClient.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s failed: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s returned HTTP %d", url, resp.StatusCode)
	}

	out, err := os.Create(destFile)
	if err != nil {
		return fmt.Errorf("create %s: %w", destFile, err)
	}
	defer out.Close()

	if _, err := io.Copy(out, resp.Body); err != nil {
		return fmt.Errorf("write %s: %w", destFile, err)
	}
	return nil
}
