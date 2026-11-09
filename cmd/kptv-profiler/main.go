// Command kptv-profiler is stage 1 of a channel survey for HLS sources. It
// reads an M3U playlist, fetches every channel's playlist with the declared
// User-Agent (following redirects), and records the final URL, playlist
// type, encryption and each master variant's resolution, frame rate,
// bandwidth and codecs as JSON for kptv-playback-profiler.
//
//	go run ./cmd/kptv-profiler -source <m3u url> -out profiler-output/channel-scan.json
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Channel struct {
	Name              string
	TVGID             string
	Group             string
	URL               string
	DeclaredUserAgent string
	DeclaredReferrer  string
}

type Variant struct {
	URL              string  `json:"url"`
	Bandwidth        int64   `json:"bandwidth,omitempty"`
	AverageBandwidth int64   `json:"averageBandwidth,omitempty"`
	Resolution       string  `json:"resolution,omitempty"`
	FrameRate        float64 `json:"frameRate,omitempty"`
	Codecs           string  `json:"codecs,omitempty"`
}

type ScanResult struct {
	Name         string    `json:"name"`
	TVGID        string    `json:"tvgId"`
	Group        string    `json:"group"`
	SourceURL    string    `json:"sourceUrl"`
	FinalURL     string    `json:"finalUrl,omitempty"`
	UserAgent    string    `json:"userAgent,omitempty"`
	Status       int       `json:"status"`
	ContentType  string    `json:"contentType,omitempty"`
	PlaylistType string    `json:"playlistType"`
	Encrypted    bool      `json:"encrypted"`
	Variants     []Variant `json:"variants,omitempty"`
	Error        string    `json:"error,omitempty"`
	DurationMS   int64     `json:"durationMs"`
}

var attrRE = regexp.MustCompile(`([A-Za-z0-9_-]+)="([^"]*)"`)

func main() {
	sourceURL := flag.String(
		"source",
		"https://i.mjh.nz/au/Brisbane/raw-tv.m3u8",
		"M3U playlist URL",
	)

	outPath := flag.String(
		"out",
		"profiler-output/channel-scan.json",
		"JSON output path",
	)

	workers := flag.Int(
		"workers",
		3,
		"concurrent channel requests",
	)

	fallbackUA := flag.String(
		"user-agent",
		"otg/1.5.1 (AppleTv Apple TV 4; tvOS16.0; appletv.client) libcurl/7.58.0 OpenSSL/1.0.2o zlib/1.2.11 clib/1.8.56",
		"User-Agent used to fetch source M3U",
	)

	flag.Parse()

	channels, err := fetchSource(*sourceURL, *fallbackUA)
	if err != nil {
		fatal(err)
	}

	fmt.Printf("Channels discovered: %d\n", len(channels))
	fmt.Printf("Workers: %d\n\n", *workers)

	results := make([]ScanResult, len(channels))

	type job struct {
		Index   int
		Channel Channel
	}

	jobs := make(chan job)
	var wg sync.WaitGroup
	var printMu sync.Mutex
	completed := 0

	for w := 0; w < *workers; w++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for j := range jobs {
				result := scanChannel(j.Channel)
				results[j.Index] = result

				printMu.Lock()
				completed++

				status := "OK"
				if result.Error != "" {
					status = "FAIL"
				}

				fmt.Printf(
					"[%3d/%3d] %-4s %-28s %-6s variants=%d %dms\n",
					completed,
					len(channels),
					status,
					j.Channel.Name,
					result.PlaylistType,
					len(result.Variants),
					result.DurationMS,
				)

				if result.Error != "" {
					fmt.Printf("          %s\n", result.Error)
				}

				printMu.Unlock()
			}
		}()
	}

	for i, ch := range channels {
		jobs <- job{
			Index:   i,
			Channel: ch,
		}
	}

	close(jobs)
	wg.Wait()

	if err := os.MkdirAll("profiler-output", 0755); err != nil {
		fatal(err)
	}

	data, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		fatal(err)
	}

	if err := os.WriteFile(*outPath, data, 0644); err != nil {
		fatal(err)
	}

	var masters, media, failures, encrypted int

	for _, r := range results {
		switch r.PlaylistType {
		case "master":
			masters++
		case "media":
			media++
		}

		if r.Error != "" {
			failures++
		}

		if r.Encrypted {
			encrypted++
		}
	}

	fmt.Println()
	fmt.Println("Scan complete")
	fmt.Printf("  Master playlists: %d\n", masters)
	fmt.Printf("  Media playlists:  %d\n", media)
	fmt.Printf("  Encrypted:         %d\n", encrypted)
	fmt.Printf("  Failed:            %d\n", failures)
	fmt.Printf("  Report:            %s\n", *outPath)
}

func fetchSource(sourceURL, ua string) ([]Channel, error) {
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	req, err := http.NewRequest("GET", sourceURL, nil)
	if err != nil {
		return nil, err
	}

	if ua != "" {
		req.Header.Set("User-Agent", ua)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("source playlist returned HTTP %d", resp.StatusCode)
	}

	return parseM3U(resp.Body)
}

func parseM3U(r io.Reader) ([]Channel, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)

	var channels []Channel
	var pending *Channel

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "#EXTINF:") {
			ch := Channel{}

			for _, match := range attrRE.FindAllStringSubmatch(line, -1) {
				if len(match) != 3 {
					continue
				}

				switch strings.ToLower(match[1]) {
				case "tvg-id", "channel-id":
					if ch.TVGID == "" {
						ch.TVGID = match[2]
					}
				case "tvg-name":
					ch.Name = match[2]
				case "group-title":
					ch.Group = match[2]
				}
			}

			if comma := strings.LastIndex(line, ","); comma >= 0 && comma+1 < len(line) {
				displayName := strings.TrimSpace(line[comma+1:])
				if ch.Name == "" {
					ch.Name = displayName
				}
			}

			pending = &ch
			continue
		}

		if pending == nil {
			continue
		}

		if strings.HasPrefix(line, "#EXTVLCOPT:http-user-agent=") {
			pending.DeclaredUserAgent =
				strings.TrimPrefix(line, "#EXTVLCOPT:http-user-agent=")
			continue
		}

		if strings.HasPrefix(line, "#EXTVLCOPT:http-referrer=") {
			pending.DeclaredReferrer =
				strings.TrimPrefix(line, "#EXTVLCOPT:http-referrer=")
			continue
		}

		if strings.HasPrefix(line, "#") {
			continue
		}

		pending.URL = line
		channels = append(channels, *pending)
		pending = nil
	}

	return channels, scanner.Err()
}

func scanChannel(ch Channel) ScanResult {
	start := time.Now()

	result := ScanResult{
		Name:         ch.Name,
		TVGID:        ch.TVGID,
		Group:        ch.Group,
		SourceURL:    ch.URL,
		UserAgent:    ch.DeclaredUserAgent,
		PlaylistType: "unknown",
	}

	defer func() {
		result.DurationMS = time.Since(start).Milliseconds()
	}()

	client := &http.Client{
		Timeout: 15 * time.Second,
	}

	req, err := http.NewRequest("GET", ch.URL, nil)
	if err != nil {
		result.Error = err.Error()
		return result
	}

	if ch.DeclaredUserAgent != "" {
		req.Header.Set("User-Agent", ch.DeclaredUserAgent)
	}

	if ch.DeclaredReferrer != "" {
		req.Header.Set("Referer", ch.DeclaredReferrer)
	}

	resp, err := client.Do(req)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer resp.Body.Close()

	result.Status = resp.StatusCode
	result.ContentType = resp.Header.Get("Content-Type")

	if resp.Request != nil && resp.Request.URL != nil {
		result.FinalURL = resp.Request.URL.String()
	}

	if resp.StatusCode != http.StatusOK {
		result.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
		return result
	}

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		result.Error = err.Error()
		return result
	}

	body := string(bodyBytes)

	result.Encrypted =
		strings.Contains(body, "#EXT-X-KEY:") ||
			strings.Contains(body, "#EXT-X-SESSION-KEY:")

	if strings.Contains(body, "#EXT-X-STREAM-INF:") {
		result.PlaylistType = "master"
		result.Variants = parseMaster(result.FinalURL, body)
		return result
	}

	if strings.Contains(body, "#EXTM3U") {
		result.PlaylistType = "media"
		return result
	}

	result.PlaylistType = "other"
	return result
}

func parseMaster(baseURL, body string) []Variant {
	scanner := bufio.NewScanner(strings.NewReader(body))
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)

	var variants []Variant
	var pending *Variant

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if strings.HasPrefix(line, "#EXT-X-STREAM-INF:") {
			attrs := parseHLSAttrs(
				strings.TrimPrefix(line, "#EXT-X-STREAM-INF:"),
			)

			v := Variant{
				Resolution: attrs["RESOLUTION"],
				Codecs:     trimQuotes(attrs["CODECS"]),
			}

			v.Bandwidth, _ = strconv.ParseInt(
				attrs["BANDWIDTH"],
				10,
				64,
			)

			v.AverageBandwidth, _ = strconv.ParseInt(
				attrs["AVERAGE-BANDWIDTH"],
				10,
				64,
			)

			v.FrameRate, _ = strconv.ParseFloat(
				attrs["FRAME-RATE"],
				64,
			)

			pending = &v
			continue
		}

		if pending == nil || line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		pending.URL = resolveURL(baseURL, line)
		variants = append(variants, *pending)
		pending = nil
	}

	return variants
}

func parseHLSAttrs(s string) map[string]string {
	result := make(map[string]string)

	var current strings.Builder
	inQuotes := false
	var parts []string

	for _, r := range s {
		switch r {
		case '"':
			inQuotes = !inQuotes
			current.WriteRune(r)

		case ',':
			if inQuotes {
				current.WriteRune(r)
			} else {
				parts = append(parts, current.String())
				current.Reset()
			}

		default:
			current.WriteRune(r)
		}
	}

	if current.Len() > 0 {
		parts = append(parts, current.String())
	}

	for _, part := range parts {
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}

		result[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}

	return result
}

func resolveURL(baseURL, child string) string {
	childURL, err := url.Parse(child)
	if err != nil {
		return child
	}

	if childURL.IsAbs() {
		return childURL.String()
	}

	base, err := url.Parse(baseURL)
	if err != nil {
		return child
	}

	return base.ResolveReference(childURL).String()
}

func trimQuotes(s string) string {
	return strings.Trim(s, `"`)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "ERROR:", err)
	os.Exit(1)
}
