// work/localscan/ffprobe.go
package localscan

import (
	"context"
	"encoding/json"
	"fmt"
	"kptv-proxy/work/constants"
	"kptv-proxy/work/logger"
	"os/exec"
	"strconv"
	"sync"
)

var (
	ffprobeOnce      sync.Once
	ffprobeAvailable bool
	ffprobePath      string
)

// InitFFProbe resolves the ffprobe binary once via a PATH lookup. The binary is
// supplied by a volume mount, so absence is expected and non-fatal — duration
// then falls back to tag libraries and NFO runtime values.
func InitFFProbe() {
	ffprobeOnce.Do(func() {
		p, err := exec.LookPath("ffprobe")
		if err != nil {
			logger.Info("{localscan/ffprobe - InitFFProbe} ffprobe not found — duration falls back to tags and NFO")
			return
		}
		ffprobePath = p
		ffprobeAvailable = true
		logger.Info("{localscan/ffprobe - InitFFProbe} using %s", p)
	})
}

// FFProbeAvailable reports whether ffprobe was resolved and is executable.
func FFProbeAvailable() bool {
	InitFFProbe()
	return ffprobeAvailable
}

// DurationViaFFProbe returns the duration in seconds for the given file.
// Returns -1 and an error if ffprobe is unavailable or fails.
func DurationViaFFProbe(filePath string) (int, error) {
	if !FFProbeAvailable() {
		return -1, fmt.Errorf("ffprobe not available")
	}

	ctx, cancel := context.WithTimeout(context.Background(), constants.Internal.ScanFFprobeTimeout)
	defer cancel()

	out, err := exec.CommandContext(
		ctx,
		ffprobePath,
		"-v", "quiet",
		"-print_format", "json",
		"-show_entries", "format=duration",
		filePath,
	).Output()
	if err != nil {
		return -1, fmt.Errorf("ffprobe exec: %w", err)
	}

	var result struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return -1, fmt.Errorf("ffprobe parse: %w", err)
	}

	secs, err := strconv.ParseFloat(result.Format.Duration, 64)
	if err != nil {
		return -1, fmt.Errorf("ffprobe duration value: %w", err)
	}

	return int(secs), nil
}
