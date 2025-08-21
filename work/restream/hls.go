// Create work/restream/hls.go

package restream

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"kptv-proxy/work/utils"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"time"
)

// test the stream with FFProbe
func (r *Restream) testStreamWithFFprobe(streamURL string) (string, error) {
	// Check if this looks like a tracking URL that might need special handling
	if strings.Contains(streamURL, "/beacon/") || strings.Contains(streamURL, "redirect_url") {
		if r.Config.Debug {
			r.Logger.Printf("[FFPROBE_TEST] Detected tracking URL, attempting to resolve: %s", utils.LogURL(r.Config, streamURL))
		}

		// Try to resolve redirect URL first
		if resolvedURL := r.resolveRedirectURL(streamURL); resolvedURL != "" {
			if r.Config.Debug {
				r.Logger.Printf("[FFPROBE_TEST] Testing resolved URL: %s", utils.LogURL(r.Config, resolvedURL))
			}

			streamURL = resolvedURL
		} else {
			if r.Config.Debug {
				r.Logger.Printf("[FFPROBE_TEST] Could not resolve tracking URL, skipping ffprobe validation")
			}

			// For tracking URLs we can't resolve, assume they might work with proxying
			return "error", fmt.Errorf("tracking URL requires proxying")
		}
	}

	// Test with ffprobe using configured timeout
	ctx, cancel := context.WithTimeout(context.Background(), r.Config.StreamTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=codec_type,codec_name,width,height",
		"-analyzeduration", "5M",
		"-probesize", "5M",
		"-of", "json",
		"-i", streamURL)

	if r.Config.Debug {
		r.Logger.Printf("[FFPROBE_TEST] Testing stream with %v timeout: %s", r.Config.StreamTimeout, utils.LogURL(r.Config, streamURL))
	}

	startTime := time.Now()
	output, err := cmd.Output()
	duration := time.Since(startTime)
	if r.Config.Debug {
		r.Logger.Printf("[FFPROBE_TEST] ffprobe completed in %v", duration)
	}

	if err != nil {
		// Check if it was a timeout (context deadline exceeded)
		if ctx.Err() == context.DeadlineExceeded {
			if r.Config.Debug {
				r.Logger.Printf("[FFPROBE_TEST] ffprobe timed out - stream likely invalid/hanging")
			}

			return "timeout", err
		}

		// Check the specific error output to distinguish between types of failures
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr := string(exitErr.Stderr)
			if r.Config.Debug {
				r.Logger.Printf("[FFPROBE_TEST] ffprobe stderr: %s", stderr)
			}

			// Check for specific error patterns that indicate fundamentally invalid streams
			if strings.Contains(stderr, "Invalid data found") ||
				strings.Contains(stderr, "Unable to find a suitable output format") ||
				strings.Contains(stderr, "not in allowed_segment_extensions") {
				if r.Config.Debug {
					r.Logger.Printf("[FFPROBE_TEST] Stream has fundamental format errors")
				}
				return "invalid_format", err
			}
			if r.Config.Debug {
				r.Logger.Printf("[FFPROBE_TEST] ffprobe failed with exit error: %v", err)
			}

			return "error", err
		}
		if r.Config.Debug {
			r.Logger.Printf("[FFPROBE_TEST] ffprobe failed with execution error: %v", err)
		}

		return "error", err
	}

	// Parse JSON output to get more detailed information
	var probeResult struct {
		Streams []struct {
			CodecType string `json:"codec_type"`
			CodecName string `json:"codec_name"`
			Width     int    `json:"width"`
			Height    int    `json:"height"`
		} `json:"streams"`
	}

	if err := json.Unmarshal(output, &probeResult); err != nil {
		if r.Config.Debug {
			r.Logger.Printf("[FFPROBE_TEST] Error parsing ffprobe JSON: %v", err)
		}

		return "no_video", fmt.Errorf("failed to parse ffprobe output")
	}

	outputStr := string(output)
	if r.Config.Debug {
		r.Logger.Printf("[FFPROBE_TEST] ffprobe output: %q", outputStr)
	}

	// Check if we found video streams with valid properties
	if len(probeResult.Streams) > 0 {
		stream := probeResult.Streams[0]
		if stream.CodecType == "video" && stream.Width > 0 && stream.Height > 0 {
			if r.Config.Debug {
				r.Logger.Printf("[FFPROBE_TEST] Stream contains valid video: %s %dx%d", stream.CodecName, stream.Width, stream.Height)
			}

			return "video", nil
		}
	}
	if r.Config.Debug {
		r.Logger.Printf("[FFPROBE_TEST] No valid video streams found")
	}

	return "no_video", fmt.Errorf("no video stream found")
}

func (r *Restream) streamHLSSegments(playlistURL string) (bool, int64) {
	if r.Config.Debug {
		r.Logger.Printf("[HLS_STREAM] Starting HLS segment streaming for: %s", utils.LogURL(r.Config, playlistURL))
	}

	totalBytes := int64(0)
	processedSegments := make(map[string]bool) // Track by URL instead of index

	for {
		select {
		case <-r.Ctx.Done():
			return totalBytes > 0, totalBytes
		default:
		}

		// Check if we still have clients
		clientCount := 0
		r.Clients.Range(func(_, _ interface{}) bool {
			clientCount++
			return true
		})

		if clientCount == 0 {
			if r.Config.Debug {
				r.Logger.Printf("[HLS_STREAM] No clients remaining")
			}

			return totalBytes > 0, totalBytes
		}

		segments, err := r.getHLSSegments(playlistURL)
		if err != nil {
			if r.Config.Debug {
				r.Logger.Printf("[HLS_STREAM_ERROR] Error getting segments: %v", err)
			}

			return false, totalBytes
		}
		if r.Config.Debug {
			r.Logger.Printf("[HLS_PLAYLIST_REFRESH] Found %d segments", len(segments))
		}

		// Stream new segments only
		newSegmentCount := 0
		for _, segmentURL := range segments {
			if processedSegments[segmentURL] {
				continue // Skip already processed segments
			}
			if r.Config.Debug {
				r.Logger.Printf("[HLS_SEGMENT_NEW] Processing new segment: %s", utils.LogURL(r.Config, segmentURL))
			}

			segmentBytes, err := r.streamSegment(segmentURL)
			if err != nil {
				if r.Config.Debug {
					r.Logger.Printf("[HLS_SEGMENT_ERROR] Error streaming segment: %v", err)
				}

				continue
			}

			processedSegments[segmentURL] = true
			totalBytes += segmentBytes
			newSegmentCount++
			if r.Config.Debug {
				r.Logger.Printf("[HLS_SEGMENT] Streamed segment: %d bytes", segmentBytes)
			}

		}

		if r.Config.Debug && newSegmentCount > 0 {
			r.Logger.Printf("[HLS_BATCH] Streamed %d new segments", newSegmentCount)
		}

		// Clean up old processed segments to prevent memory growth
		if len(processedSegments) > 20 {
			// Keep only the most recent segments
			newProcessed := make(map[string]bool)
			recentCount := 0
			for i := len(segments) - 10; i < len(segments) && i >= 0; i++ {
				newProcessed[segments[i]] = true
				recentCount++
			}
			processedSegments = newProcessed
			if r.Config.Debug {
				r.Logger.Printf("[HLS_CLEANUP] Cleaned segment cache, kept %d recent segments", recentCount)
			}
		}

		// Wait before next playlist refresh (shorter interval)
		select {
		case <-r.Ctx.Done():
			return totalBytes > 0, totalBytes
		case <-time.After(2 * time.Second):
			continue
		}
	}
}

// Enhanced getHLSSegments function to handle redirect URLs
func (r *Restream) getHLSSegments(playlistURL string) ([]string, error) {
	req, err := http.NewRequest("GET", playlistURL, nil)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(r.Ctx, 10*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	resp, err := r.HttpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var segments []string
	lines := strings.Split(string(body), "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			var segmentURL string

			if strings.HasPrefix(line, "http") {
				segmentURL = line
			} else {
				// Relative URL - resolve against playlist URL
				baseURL := playlistURL[:strings.LastIndex(playlistURL, "/")]
				segmentURL = baseURL + "/" + line
			}

			// Check if this is a tracking/beacon URL with redirect
			if resolvedURL := r.resolveRedirectURL(segmentURL); resolvedURL != "" {
				segmentURL = resolvedURL
				if r.Config.Debug {
					r.Logger.Printf("[HLS_REDIRECT] Resolved tracking URL to: %s", utils.LogURL(r.Config, segmentURL))
				}
			}

			segments = append(segments, segmentURL)
		}
	}

	return segments, nil
}

func (r *Restream) resolveRedirectURL(segmentURL string) string {
	// Parse the URL to extract redirect_url parameter
	if strings.Contains(segmentURL, "redirect_url=") {
		if parsedURL, err := url.Parse(segmentURL); err == nil {
			if redirectURL := parsedURL.Query().Get("redirect_url"); redirectURL != "" {
				if decodedURL, err := url.QueryUnescape(redirectURL); err == nil {
					return decodedURL
				}
			}
		}
	}

	// Check for other redirect patterns (beacon URLs, etc.)
	if strings.Contains(segmentURL, "/beacon/") && strings.Contains(segmentURL, "redirect_url") {
		if parsedURL, err := url.Parse(segmentURL); err == nil {
			if redirectURL := parsedURL.Query().Get("redirect_url"); redirectURL != "" {
				if decodedURL, err := url.QueryUnescape(redirectURL); err == nil {
					return decodedURL
				}
			}
		}
	}

	return ""
}

// Enhanced streamSegment to handle tracking URLs
func (r *Restream) streamSegment(segmentURL string) (int64, error) {
	// Resolve redirect URL if this is a tracking URL
	originalURL := segmentURL
	if resolvedURL := r.resolveRedirectURL(segmentURL); resolvedURL != "" {
		segmentURL = resolvedURL
		if r.Config.Debug {
			r.Logger.Printf("[HLS_SEGMENT_REDIRECT] Using resolved URL: %s", utils.LogURL(r.Config, segmentURL))
		}
	}

	req, err := http.NewRequest("GET", segmentURL, nil)
	if err != nil {
		return 0, err
	}

	// Add headers that might be needed for tracking URLs
	if originalURL != segmentURL {
		req.Header.Set("Referer", originalURL)
	}

	ctx, cancel := context.WithTimeout(r.Ctx, 30*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	resp, err := r.HttpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	buffer := make([]byte, (r.Config.BufferSizePerStream * 1024 * 1024))
	totalBytes := int64(0)

	for {
		n, err := resp.Body.Read(buffer)
		if n > 0 {
			data := buffer[:n]
			totalBytes += int64(n)

			r.Buffer.Write(data)
			activeClients := r.DistributeToClients(data)
			if activeClients == 0 {
				return totalBytes, fmt.Errorf("no active clients")
			}
		}

		if err != nil {
			if err == io.EOF {
				return totalBytes, nil
			}
			return totalBytes, err
		}
	}
}
