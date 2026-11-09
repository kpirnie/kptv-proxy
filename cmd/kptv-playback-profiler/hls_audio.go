package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type audioRendition struct {
	GroupID  string
	URL      string
	Channels string
	Default  bool
}

// audioURLForVariant finds the EXT-X-MEDIA audio rendition associated
// with the selected child video playlist.
//
// Some masters contain duplicate video child URLs paired with different
// AUDIO groups. Since Plex output is stereo AC3, prefer a 2-channel
// rendition when several groups reference the same video variant.
func audioURLForVariant(
	baseURL string,
	body string,
	variantURL string,
) string {

	lines := strings.Split(body, "\n")

	audioGroups := make(map[string][]audioRendition)

	// First collect all EXT-X-MEDIA AUDIO renditions.
	for _, raw := range lines {
		line := strings.TrimSpace(raw)

		if !strings.HasPrefix(
			line,
			"#EXT-X-MEDIA:",
		) {
			continue
		}

		attrs := parseHLSAttrs(
			strings.TrimPrefix(
				line,
				"#EXT-X-MEDIA:",
			),
		)

		if strings.Trim(
			attrs["TYPE"],
			`"`,
		) != "AUDIO" {
			continue
		}

		groupID := strings.Trim(
			attrs["GROUP-ID"],
			`"`,
		)

		uri := strings.Trim(
			attrs["URI"],
			`"`,
		)

		if groupID == "" || uri == "" {
			continue
		}

		audioGroups[groupID] = append(
			audioGroups[groupID],
			audioRendition{
				GroupID: groupID,
				URL: resolveURL(
					baseURL,
					uri,
				),
				Channels: strings.Trim(
					attrs["CHANNELS"],
					`"`,
				),
				Default: strings.EqualFold(
					strings.Trim(
						attrs["DEFAULT"],
						`"`,
					),
					"YES",
				),
			},
		)
	}

	// Find every STREAM-INF entry whose child URL matches
	// our selected video playlist. There may be multiple
	// entries with the same video URL but different AUDIO groups.
	var candidateGroups []string

	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(
			lines[i],
		)

		if !strings.HasPrefix(
			line,
			"#EXT-X-STREAM-INF:",
		) {
			continue
		}

		attrs := parseHLSAttrs(
			strings.TrimPrefix(
				line,
				"#EXT-X-STREAM-INF:",
			),
		)

		audioGroup := strings.Trim(
			attrs["AUDIO"],
			`"`,
		)

		if audioGroup == "" {
			continue
		}

		var child string

		for j := i + 1; j < len(lines); j++ {
			child = strings.TrimSpace(
				lines[j],
			)

			if child == "" ||
				strings.HasPrefix(
					child,
					"#",
				) {
				continue
			}

			break
		}

		if child == "" {
			continue
		}

		resolved := resolveURL(
			baseURL,
			child,
		)

		if resolved == variantURL {
			candidateGroups = append(
				candidateGroups,
				audioGroup,
			)
		}
	}

	// Prefer stereo when available because our final Plex
	// output is AC3 stereo anyway.
	for _, groupID := range candidateGroups {
		for _, rendition := range audioGroups[groupID] {
			if rendition.Channels == "2" {
				return rendition.URL
			}
		}
	}

	// Otherwise prefer a DEFAULT rendition.
	for _, groupID := range candidateGroups {
		for _, rendition := range audioGroups[groupID] {
			if rendition.Default {
				return rendition.URL
			}
		}
	}

	// Finally use the first associated audio rendition.
	for _, groupID := range candidateGroups {
		if renditions := audioGroups[groupID]; len(renditions) > 0 {
			return renditions[0].URL
		}
	}

	return ""
}

func runFFmpegValidatedAV(
	ffmpegPath string,
	ffprobePath string,
	audioURL string,
	streamURL string,
	ua string,
	timeout time.Duration,
	seconds int,
) (int64, MediaInfo, error) {

	ctx, cancel := context.WithTimeout(
		context.Background(),
		timeout,
	)
	defer cancel()

	tmp, err := os.CreateTemp(
		"",
		"kptv-profiler-av-*.ts",
	)
	if err != nil {
		return 0, MediaInfo{}, err
	}

	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)

	args := []string{
		"-hide_banner",
		"-loglevel",
		"error",
		"-y",
	}

	if ua != "" {
		args = append(
			args,
			"-user_agent",
			ua,
		)
	}

	args = append(
		args,
		"-i",
		streamURL,
	)

	if audioURL != "" {
		if ua != "" {
			args = append(
				args,
				"-user_agent",
				ua,
			)
		}

		args = append(
			args,
			"-i",
			audioURL,

			"-map",
			"0:v:0?",

			"-map",
			"1:a:0?",
		)
	} else {
		args = append(
			args,
			"-map",
			"0:v:0?",

			"-map",
			"0:a:0?",
		)
	}

	args = append(
		args,
		"-c:v",
		"copy",

		"-c:a",
		"ac3",

		"-ac",
		"2",

		"-b:a",
		"192k",

		"-t",
		strconv.Itoa(seconds),

		"-f",
		"mpegts",

		tmpPath,
	)

	cmd := exec.CommandContext(
		ctx,
		ffmpegPath,
		args...,
	)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	runErr := cmd.Run()

	var bytesOut int64

	if info, statErr := os.Stat(tmpPath); statErr == nil {
		bytesOut = info.Size()
	}

	if ctx.Err() == context.DeadlineExceeded {
		return bytesOut,
			MediaInfo{},
			fmt.Errorf(
				"timeout after %s: %s",
				timeout,
				strings.TrimSpace(
					stderr.String(),
				),
			)
	}

	if runErr != nil {
		return bytesOut,
			MediaInfo{},
			fmt.Errorf(
				"%s",
				cleanErr(
					stderr.String(),
					runErr,
				),
			)
	}

	outputMedia, err := runFFprobe(
		ffprobePath,
		tmpPath,
		"",
		5*time.Second,
	)

	if err != nil {
		return bytesOut,
			MediaInfo{},
			fmt.Errorf(
				"output ffprobe: %w",
				err,
			)
	}

	return bytesOut, outputMedia, nil
}
