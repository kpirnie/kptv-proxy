// Command kptv-playback-profiler is stage 2: for each scanned channel it ranks
// the variants (resolution, then frame rate, then average/peak bandwidth),
// resolves any separate EXT-X-MEDIA audio rendition, and actually plays the
// top candidates through ffmpeg, then checks the final MPEG-TS with ffprobe
// so a channel only passes when it really yields usable video and audio.
//
//	go run ./cmd/kptv-playback-profiler -input profiler-output/channel-scan.json -out profiler-output/playback-profiles.json
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type ScanResult struct {
	Name      string `json:"name"`
	TVGID     string `json:"tvgId"`
	Group     string `json:"group"`
	SourceURL string `json:"sourceUrl"`
	UserAgent string `json:"userAgent,omitempty"`
}

type Variant struct {
	URL              string  `json:"url"`
	Bandwidth        int64   `json:"bandwidth,omitempty"`
	AverageBandwidth int64   `json:"averageBandwidth,omitempty"`
	Resolution       string  `json:"resolution,omitempty"`
	FrameRate        float64 `json:"frameRate,omitempty"`
	Codecs           string  `json:"codecs,omitempty"`
}

type MediaInfo struct {
	VideoCodec string  `json:"videoCodec,omitempty"`
	Width      int     `json:"width,omitempty"`
	Height     int     `json:"height,omitempty"`
	FPS        float64 `json:"fps,omitempty"`
	AudioCodec string  `json:"audioCodec,omitempty"`
	Channels   int     `json:"channels,omitempty"`
}

type Attempt struct {
	Rank          int       `json:"rank"`
	URL           string    `json:"url"`
	Advertised    Variant   `json:"advertised"`
	Encrypted     bool      `json:"encrypted"`
	ProbeOK       bool      `json:"probeOk"`
	ProbeMS       int64     `json:"probeMs"`
	ProbeError    string    `json:"probeError,omitempty"`
	Media         MediaInfo `json:"media"`
	OutputMedia   MediaInfo `json:"outputMedia"`
	PlaybackOK    bool      `json:"playbackOk"`
	PlaybackMS    int64     `json:"playbackMs"`
	PlaybackBytes int64     `json:"playbackBytes"`
	PlaybackError string    `json:"playbackError,omitempty"`
}

type PlaybackProfile struct {
	Name           string    `json:"name"`
	TVGID          string    `json:"tvgId"`
	SourceURL      string    `json:"sourceUrl"`
	UserAgent      string    `json:"userAgent,omitempty"`
	RootType       string    `json:"rootType"`
	CandidateCount int       `json:"candidateCount"`
	Attempts       []Attempt `json:"attempts"`
	SelectedRank   int       `json:"selectedRank,omitempty"`
	Selected       *Attempt  `json:"selected,omitempty"`
	Success        bool      `json:"success"`
	Error          string    `json:"error,omitempty"`
}

type ffprobeOutput struct {
	Streams []struct {
		CodecType    string `json:"codec_type"`
		CodecName    string `json:"codec_name"`
		Width        int    `json:"width"`
		Height       int    `json:"height"`
		RFrameRate   string `json:"r_frame_rate"`
		AvgFrameRate string `json:"avg_frame_rate"`
		Channels     int    `json:"channels"`
	} `json:"streams"`
}

func main() {
	input := flag.String(
		"input",
		"profiler-output/channel-scan.json",
		"Stage 2 scan JSON",
	)

	output := flag.String(
		"out",
		"profiler-output/playback-profiles.json",
		"playback profile JSON",
	)

	match := flag.String(
		"match",
		"",
		"optional case-insensitive regexp matching channel name or tvg-id",
	)

	ffprobePath := flag.String(
		"ffprobe",
		"ffprobe",
		"ffprobe path",
	)

	ffmpegPath := flag.String(
		"ffmpeg",
		"ffmpeg",
		"ffmpeg path",
	)

	probeTimeout := flag.Duration(
		"probe-timeout",
		10*time.Second,
		"ffprobe timeout per candidate",
	)

	playbackTimeout := flag.Duration(
		"playback-timeout",
		15*time.Second,
		"ffmpeg timeout per candidate",
	)

	playbackSeconds := flag.Int(
		"seconds",
		4,
		"seconds of playback to validate",
	)

	maxVariants := flag.Int(
		"max-variants",
		6,
		"maximum ranked variants to try per channel",
	)

	flag.Parse()

	raw, err := os.ReadFile(*input)
	if err != nil {
		fatal(err)
	}

	var scan []ScanResult
	if err := json.Unmarshal(raw, &scan); err != nil {
		fatal(err)
	}

	var re *regexp.Regexp

	if *match != "" {
		re, err = regexp.Compile("(?i)" + *match)
		if err != nil {
			fatal(fmt.Errorf("invalid -match regexp: %w", err))
		}
	}

	var selected []ScanResult

	for _, ch := range scan {
		if re != nil &&
			!re.MatchString(ch.Name) &&
			!re.MatchString(ch.TVGID) {
			continue
		}

		selected = append(selected, ch)
	}

	if len(selected) == 0 {
		fatal(fmt.Errorf("no channels matched"))
	}

	fmt.Printf("Channels selected: %d\n\n", len(selected))

	var profiles []PlaybackProfile

	for i, ch := range selected {
		fmt.Printf("[%d/%d] %s\n", i+1, len(selected), ch.Name)

		profile := profileChannel(
			ch,
			*ffprobePath,
			*ffmpegPath,
			*probeTimeout,
			*playbackTimeout,
			*playbackSeconds,
			*maxVariants,
		)

		profiles = append(profiles, profile)

		if profile.Success && profile.Selected != nil {
			a := profile.Selected

			fmt.Printf(
				"  PASS rank=%d %dx%d %.2ffps input=%s/%s output=%s/%s bytes=%d\n\n",
				a.Rank,
				a.Media.Width,
				a.Media.Height,
				a.Media.FPS,
				a.Media.VideoCodec,
				a.Media.AudioCodec,
				a.OutputMedia.VideoCodec,
				a.OutputMedia.AudioCodec,
				a.PlaybackBytes,
			)
		} else {
			fmt.Printf("  FAIL %s\n\n", profile.Error)
		}
	}

	if err := os.MkdirAll("profiler-output", 0755); err != nil {
		fatal(err)
	}

	out, err := json.MarshalIndent(profiles, "", "  ")
	if err != nil {
		fatal(err)
	}

	if err := os.WriteFile(*output, out, 0644); err != nil {
		fatal(err)
	}

	ok := 0

	for _, profile := range profiles {
		if profile.Success {
			ok++
		}
	}

	fmt.Printf("Complete: %d/%d working\n", ok, len(profiles))
	fmt.Printf("Report: %s\n", *output)
}

func profileChannel(
	ch ScanResult,
	ffprobePath string,
	ffmpegPath string,
	probeTimeout time.Duration,
	playbackTimeout time.Duration,
	seconds int,
	maxVariants int,
) PlaybackProfile {

	p := PlaybackProfile{
		Name:      ch.Name,
		TVGID:     ch.TVGID,
		SourceURL: ch.SourceURL,
		UserAgent: ch.UserAgent,
	}

	rootBody, finalURL, err := fetchText(
		ch.SourceURL,
		ch.UserAgent,
		5*1024*1024,
	)

	if err != nil {
		p.Error = "root fetch: " + err.Error()
		return p
	}

	var candidates []Variant

	switch {
	case strings.Contains(rootBody, "#EXT-X-STREAM-INF:"):
		p.RootType = "master"
		candidates = parseMaster(finalURL, rootBody)
		rankVariants(candidates)

	case strings.Contains(rootBody, "#EXTM3U"):
		p.RootType = "media"
		candidates = []Variant{
			{URL: finalURL},
		}

	default:
		p.RootType = "direct"
		candidates = []Variant{
			{URL: finalURL},
		}
	}

	p.CandidateCount = len(candidates)

	if len(candidates) == 0 {
		p.Error = "no playable candidates found"
		return p
	}

	if maxVariants > 0 && len(candidates) > maxVariants {
		candidates = candidates[:maxVariants]
	}

	for i, candidate := range candidates {
		a := Attempt{
			Rank:       i + 1,
			URL:        candidate.URL,
			Advertised: candidate,
		}

		if body, _, err := fetchText(
			candidate.URL,
			ch.UserAgent,
			2*1024*1024,
		); err == nil {

			a.Encrypted =
				strings.Contains(body, "#EXT-X-KEY:") ||
					strings.Contains(body, "#EXT-X-SESSION-KEY:")
		}

		probeStart := time.Now()

		media, probeErr := runFFprobe(
			ffprobePath,
			candidate.URL,
			ch.UserAgent,
			probeTimeout,
		)

		a.ProbeMS = time.Since(probeStart).Milliseconds()

		if probeErr != nil {
			a.ProbeError = probeErr.Error()
			p.Attempts = append(p.Attempts, a)

			fmt.Printf(
				"  rank %d probe FAIL: %s\n",
				a.Rank,
				shortErr(a.ProbeError),
			)

			continue
		}

		a.ProbeOK = true
		a.Media = media

		playbackStart := time.Now()

		bytesOut, outputMedia, playbackErr := runFFmpegValidatedAV(
			ffmpegPath,
			ffprobePath,
			audioURLForVariant(finalURL, rootBody, candidate.URL),
			candidate.URL,
			ch.UserAgent,
			playbackTimeout,
			seconds,
		)

		a.PlaybackMS = time.Since(playbackStart).Milliseconds()
		a.PlaybackBytes = bytesOut
		a.OutputMedia = outputMedia

		if playbackErr != nil {
			a.PlaybackError = playbackErr.Error()
			p.Attempts = append(p.Attempts, a)

			fmt.Printf(
				"  rank %d %dx%d %.2ffps playback FAIL: %s\n",
				a.Rank,
				a.Media.Width,
				a.Media.Height,
				a.Media.FPS,
				shortErr(a.PlaybackError),
			)

			continue
		}

		if bytesOut < 256*1024 {
			a.PlaybackError =
				fmt.Sprintf("only %d bytes produced", bytesOut)

			p.Attempts = append(p.Attempts, a)

			fmt.Printf(
				"  rank %d playback FAIL: %s\n",
				a.Rank,
				a.PlaybackError,
			)

			continue
		}

		a.PlaybackOK = true
		p.Attempts = append(p.Attempts, a)

		p.SelectedRank = a.Rank

		selected := a
		p.Selected = &selected
		p.Success = true

		return p
	}

	p.Error = "all tested candidates failed"
	return p
}

func runFFprobe(
	path string,
	streamURL string,
	ua string,
	timeout time.Duration,
) (MediaInfo, error) {

	ctx, cancel := context.WithTimeout(
		context.Background(),
		timeout,
	)
	defer cancel()

	args := []string{
		"-v",
		"error",
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
		"-show_entries",
		"stream=codec_type,codec_name,width,height,r_frame_rate,avg_frame_rate,channels",
		"-of",
		"json",
		streamURL,
	)

	cmd := exec.CommandContext(
		ctx,
		path,
		args...,
	)

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	if ctx.Err() == context.DeadlineExceeded {
		return MediaInfo{},
			fmt.Errorf("timeout after %s", timeout)
	}

	if err != nil {
		return MediaInfo{},
			fmt.Errorf("%s", cleanErr(stderr.String(), err))
	}

	var parsed ffprobeOutput

	if err := json.Unmarshal(
		stdout.Bytes(),
		&parsed,
	); err != nil {
		return MediaInfo{},
			fmt.Errorf("invalid ffprobe JSON: %w", err)
	}

	var info MediaInfo

	for _, stream := range parsed.Streams {
		switch stream.CodecType {
		case "video":
			if info.VideoCodec == "" {
				info.VideoCodec = stream.CodecName
				info.Width = stream.Width
				info.Height = stream.Height

				info.FPS = parseFPS(
					stream.AvgFrameRate,
				)

				if info.FPS == 0 {
					info.FPS = parseFPS(
						stream.RFrameRate,
					)
				}
			}

		case "audio":
			if info.AudioCodec == "" {
				info.AudioCodec = stream.CodecName
				info.Channels = stream.Channels
			}
		}
	}

	if info.VideoCodec == "" &&
		info.AudioCodec == "" {
		return MediaInfo{},
			fmt.Errorf(
				"ffprobe found no audio or video streams",
			)
	}

	return info, nil
}

func runFFmpeg(
	path string,
	streamURL string,
	ua string,
	timeout time.Duration,
	seconds int,
) (int64, error) {

	ctx, cancel := context.WithTimeout(
		context.Background(),
		timeout,
	)
	defer cancel()

	args := []string{
		"-hide_banner",
		"-loglevel",
		"error",
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

		"-map",
		"0:v:0?",

		"-map",
		"0:a:0?",

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

		"pipe:1",
	)

	cmd := exec.CommandContext(
		ctx,
		path,
		args...,
	)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0, err
	}

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return 0, err
	}

	n, copyErr := io.Copy(
		io.Discard,
		stdout,
	)

	waitErr := cmd.Wait()

	if ctx.Err() == context.DeadlineExceeded {
		return n,
			fmt.Errorf(
				"timeout after %s: %s",
				timeout,
				strings.TrimSpace(stderr.String()),
			)
	}

	if copyErr != nil {
		return n,
			fmt.Errorf(
				"reading ffmpeg output: %w",
				copyErr,
			)
	}

	if waitErr != nil {
		return n,
			fmt.Errorf(
				"%s",
				cleanErr(
					stderr.String(),
					waitErr,
				),
			)
	}

	return n, nil
}

func fetchText(
	rawURL string,
	ua string,
	maxBytes int64,
) (string, string, error) {

	client := &http.Client{
		Timeout: 12 * time.Second,
	}

	req, err := http.NewRequest(
		"GET",
		rawURL,
		nil,
	)

	if err != nil {
		return "", "", err
	}

	if ua != "" {
		req.Header.Set(
			"User-Agent",
			ua,
		)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "",
			"",
			fmt.Errorf(
				"HTTP %d",
				resp.StatusCode,
			)
	}

	body, err := io.ReadAll(
		io.LimitReader(
			resp.Body,
			maxBytes,
		),
	)

	if err != nil {
		return "", "", err
	}

	finalURL := rawURL

	if resp.Request != nil &&
		resp.Request.URL != nil {
		finalURL =
			resp.Request.URL.String()
	}

	return string(body), finalURL, nil
}

func parseMaster(
	baseURL string,
	body string,
) []Variant {

	lines := strings.Split(
		body,
		"\n",
	)

	var variants []Variant

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

		v := Variant{
			Resolution: strings.Trim(
				attrs["RESOLUTION"],
				`"`,
			),

			Codecs: strings.Trim(
				attrs["CODECS"],
				`"`,
			),
		}

		v.Bandwidth, _ =
			strconv.ParseInt(
				attrs["BANDWIDTH"],
				10,
				64,
			)

		v.AverageBandwidth, _ =
			strconv.ParseInt(
				attrs["AVERAGE-BANDWIDTH"],
				10,
				64,
			)

		v.FrameRate, _ =
			strconv.ParseFloat(
				attrs["FRAME-RATE"],
				64,
			)

		for j := i + 1; j < len(lines); j++ {
			child := strings.TrimSpace(
				lines[j],
			)

			if child == "" ||
				strings.HasPrefix(
					child,
					"#",
				) {
				continue
			}

			v.URL = resolveURL(
				baseURL,
				child,
			)

			i = j

			variants = append(
				variants,
				v,
			)

			break
		}
	}

	return variants
}

func parseHLSAttrs(
	s string,
) map[string]string {

	result :=
		make(map[string]string)

	var current strings.Builder
	var parts []string

	quoted := false

	for _, r := range s {
		switch r {
		case '"':
			quoted = !quoted
			current.WriteRune(r)

		case ',':
			if quoted {
				current.WriteRune(r)
			} else {
				parts = append(
					parts,
					current.String(),
				)

				current.Reset()
			}

		default:
			current.WriteRune(r)
		}
	}

	if current.Len() > 0 {
		parts = append(
			parts,
			current.String(),
		)
	}

	for _, part := range parts {
		key, value, ok :=
			strings.Cut(
				part,
				"=",
			)

		if !ok {
			continue
		}

		result[strings.TrimSpace(key)] =
			strings.TrimSpace(value)
	}

	return result
}

func rankVariants(
	variants []Variant,
) {

	sort.SliceStable(
		variants,
		func(i, j int) bool {

			pi :=
				pixels(
					variants[i].Resolution,
				)

			pj :=
				pixels(
					variants[j].Resolution,
				)

			if pi != pj {
				return pi > pj
			}

			if variants[i].FrameRate !=
				variants[j].FrameRate {

				return variants[i].FrameRate >
					variants[j].FrameRate
			}

			bi :=
				variants[i].AverageBandwidth

			if bi == 0 {
				bi =
					variants[i].Bandwidth
			}

			bj :=
				variants[j].AverageBandwidth

			if bj == 0 {
				bj =
					variants[j].Bandwidth
			}

			return bi > bj
		},
	)
}

func pixels(
	resolution string,
) int64 {

	parts :=
		strings.Split(
			strings.ToLower(
				strings.TrimSpace(
					resolution,
				),
			),
			"x",
		)

	if len(parts) != 2 {
		return 0
	}

	w, _ :=
		strconv.ParseInt(
			parts[0],
			10,
			64,
		)

	h, _ :=
		strconv.ParseInt(
			parts[1],
			10,
			64,
		)

	return w * h
}

func resolveURL(
	baseURL string,
	child string,
) string {

	childURL, err :=
		url.Parse(child)

	if err != nil {
		return child
	}

	if childURL.IsAbs() {
		return childURL.String()
	}

	base, err :=
		url.Parse(baseURL)

	if err != nil {
		return child
	}

	return base.ResolveReference(
		childURL,
	).String()
}

func parseFPS(
	value string,
) float64 {

	if value == "" ||
		value == "0/0" {
		return 0
	}

	if strings.Contains(
		value,
		"/",
	) {
		parts :=
			strings.SplitN(
				value,
				"/",
				2,
			)

		n, _ :=
			strconv.ParseFloat(
				parts[0],
				64,
			)

		d, _ :=
			strconv.ParseFloat(
				parts[1],
				64,
			)

		if d != 0 {
			return n / d
		}

		return 0
	}

	f, _ :=
		strconv.ParseFloat(
			value,
			64,
		)

	return f
}

func cleanErr(
	stderr string,
	fallback error,
) string {

	s := strings.TrimSpace(
		stderr,
	)

	if s == "" {
		return fallback.Error()
	}

	if len(s) > 900 {
		s = s[len(s)-900:]
	}

	return s
}

func shortErr(
	s string,
) string {

	s =
		strings.ReplaceAll(
			strings.TrimSpace(s),
			"\n",
			" | ",
		)

	if len(s) > 220 {
		return s[:220] + "..."
	}

	return s
}

func fatal(err error) {
	fmt.Fprintln(
		os.Stderr,
		"ERROR:",
		err,
	)

	os.Exit(1)
}

func runFFmpegValidated(
	ffmpegPath string,
	ffprobePath string,
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
		"kptv-profiler-*.ts",
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

		"-map",
		"0:v:0?",

		"-map",
		"0:a:0?",

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
				strings.TrimSpace(stderr.String()),
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
