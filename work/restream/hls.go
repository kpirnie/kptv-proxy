// Create work/restream/hls.go

package restream

import (
	"context"
	"fmt"
	"io"
	"kptv-proxy/work/types"
	"kptv-proxy/work/utils"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

func (r *Restream) testStreamWithFFprobe(streamURL string) (string, error) {
	// Test with ffprobe using configured timeout
	ctx, cancel := context.WithTimeout(context.Background(), r.Config.StreamTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ffprobe",
		"-v", "quiet",
		"-select_streams", "v:0",
		"-show_entries", "stream=codec_type",
		"-of", "csv=p=0",
		streamURL)

	r.Logger.Printf("[FFPROBE_TEST] Testing stream with %v timeout: %s", r.Config.StreamTimeout, utils.LogURL(r.Config, streamURL))

	startTime := time.Now()
	output, err := cmd.Output()
	duration := time.Since(startTime)

	r.Logger.Printf("[FFPROBE_TEST] ffprobe completed in %v", duration)

	if err != nil {
		// Check if it was a timeout (context deadline exceeded)
		if ctx.Err() == context.DeadlineExceeded {
			r.Logger.Printf("[FFPROBE_TEST] ffprobe timed out - stream likely invalid/hanging")
			return "timeout", err
		}

		r.Logger.Printf("[FFPROBE_TEST] ffprobe failed with error: %v", err)
		return "error", err
	}

	outputStr := strings.TrimSpace(string(output))
	r.Logger.Printf("[FFPROBE_TEST] ffprobe output: %q", outputStr)

	if strings.Contains(outputStr, "video") {
		r.Logger.Printf("[FFPROBE_TEST] Stream contains valid video")
		return "video", nil
	}

	return "no_video", fmt.Errorf("no video stream found")
}

func (r *Restream) streamHLSSegments(playlistURL string) (bool, int64) {
	r.Logger.Printf("[HLS_STREAM] Starting HLS segment streaming for: %s", utils.LogURL(r.Config, playlistURL))

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
			r.Logger.Printf("[HLS_STREAM] No clients remaining")
			return totalBytes > 0, totalBytes
		}

		segments, err := r.getHLSSegments(playlistURL)
		if err != nil {
			r.Logger.Printf("[HLS_STREAM_ERROR] Error getting segments: %v", err)
			return false, totalBytes
		}

		r.Logger.Printf("[HLS_PLAYLIST_REFRESH] Found %d segments", len(segments))

		// Stream new segments only
		newSegmentCount := 0
		for _, segmentURL := range segments {
			if processedSegments[segmentURL] {
				continue // Skip already processed segments
			}

			r.Logger.Printf("[HLS_SEGMENT_NEW] Processing new segment: %s", utils.LogURL(r.Config, segmentURL))

			segmentBytes, err := r.streamSegment(segmentURL)
			if err != nil {
				r.Logger.Printf("[HLS_SEGMENT_ERROR] Error streaming segment: %v", err)
				continue
			}

			processedSegments[segmentURL] = true
			totalBytes += segmentBytes
			newSegmentCount++
			r.Logger.Printf("[HLS_SEGMENT] Streamed segment: %d bytes", segmentBytes)
		}

		if newSegmentCount > 0 {
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
			r.Logger.Printf("[HLS_CLEANUP] Cleaned segment cache, kept %d recent segments", recentCount)
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
			if strings.HasPrefix(line, "http") {
				segments = append(segments, line)
			} else {
				// Relative URL - resolve against playlist URL
				baseURL := playlistURL[:strings.LastIndex(playlistURL, "/")]
				segmentURL := baseURL + "/" + line
				segments = append(segments, segmentURL)
			}
		}
	}

	return segments, nil
}

func (r *Restream) streamSegment(segmentURL string) (int64, error) {
	req, err := http.NewRequest("GET", segmentURL, nil)
	if err != nil {
		return 0, err
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

	buffer := make([]byte, r.Config.BufferSizePerStream)
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

// Test if HLS stream works for direct playback
func (r *Restream) testHLSDirectPlayback(playlistURL, content string) bool {
	r.Logger.Printf("[HLS_DIRECT_TEST] Testing HLS direct playback: %s", utils.LogURL(r.Config, playlistURL))

	// Parse the first segment URL from playlist content
	lines := strings.Split(content, "\n")
	var firstSegmentURL string

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			if strings.HasPrefix(line, "http") {
				firstSegmentURL = line
			} else {
				// Relative URL - resolve against playlist URL
				baseURL := playlistURL[:strings.LastIndex(playlistURL, "/")]
				firstSegmentURL = baseURL + "/" + line
			}
			break
		}
	}

	if firstSegmentURL == "" {
		r.Logger.Printf("[HLS_DIRECT_TEST] No segments found in playlist")
		return false
	}

	r.Logger.Printf("[HLS_DIRECT_TEST] Testing segment accessibility: %s", utils.LogURL(r.Config, firstSegmentURL))

	// Test if segment is accessible without special headers
	req, err := http.NewRequest("HEAD", firstSegmentURL, nil)
	if err != nil {
		r.Logger.Printf("[HLS_DIRECT_TEST] Error creating segment test request: %v", err)
		return false
	}

	// Use a basic client without custom headers to simulate VLC
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		r.Logger.Printf("[HLS_DIRECT_TEST] Segment not accessible: %v", err)
		return false
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		r.Logger.Printf("[HLS_DIRECT_TEST] Segment returned HTTP %d", resp.StatusCode)
		return false
	}

	r.Logger.Printf("[HLS_DIRECT_TEST] HLS stream is valid for direct playback")
	return true
}

// Mark stream as valid HLS for direct serving
func (r *Restream) markStreamAsValidHLS(hlsURL string) {
	r.Channel.Mu.Lock()
	defer r.Channel.Mu.Unlock()

	for _, stream := range r.Channel.Streams {
		if stream.URL == hlsURL || strings.Contains(hlsURL, stream.URL) {
			stream.StreamType = types.StreamTypeHLS
			stream.ResolvedURL = hlsURL
			stream.LastChecked = time.Now()
			r.Logger.Printf("[HLS_MARKED] Marked stream as valid HLS: %s", stream.Name)
			break
		}
	}
}
