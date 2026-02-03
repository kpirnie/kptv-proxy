package restream

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"kptv-proxy/work/config"
	"kptv-proxy/work/logger"
	"kptv-proxy/work/metrics"
	"kptv-proxy/work/types"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

/**
 * streamWithFFmpeg uses ffmpeg to proxy the stream instead of Go-based restreaming
 *
 * This method handles the entire lifecycle of an FFmpeg-based stream proxy:
 * - Configures FFmpeg with appropriate headers and options from source config
 * - Manages process lifecycle with context cancellation and cleanup
 * - Reads stream data from FFmpeg stdout and distributes to connected clients
 * - Tracks metrics and activity timestamps
 * - Handles errors with exponential backoff and retry logic
 *
 * @param streamURL The source stream URL to proxy
 * @return (success bool, totalBytes int64) - success indicates if stream was healthy, totalBytes is data transferred
 */
func (r *Restream) streamWithFFmpeg(streamURL string) (bool, int64) {
	// Find the source config for this stream URL to get custom headers/user-agent
	var source *config.SourceConfig
	r.Channel.Mu.RLock()
	for _, stream := range r.Channel.Streams {
		if stream.URL == streamURL || strings.Contains(streamURL, stream.Source.URL) {
			source = stream.Source
			break
		}
	}
	r.Channel.Mu.RUnlock()

	logger.Debug("{restream/ffmpeg - streamWithFFmpeg} Starting FFmpeg for channel %s with URL: %s", r.Channel.Name, streamURL)

	// Build FFmpeg command arguments
	// Start with base args to suppress banner and set error-only logging
	args := []string{"-hide_banner", "-loglevel", "error"}
	args = append(args, r.Config.FFmpegPreInput...)

	// Add custom User-Agent if configured for this source
	if source != nil && source.UserAgent != "" {
		logger.Debug("{restream/ffmpeg - streamWithFFmpeg} Using custom User-Agent for channel %s: %s", r.Channel.Name, source.UserAgent)
		args = append(args, "-user_agent", source.UserAgent)
	}

	// Add Referer header if configured for this source
	if source != nil && source.ReqReferrer != "" {
		logger.Debug("{restream/ffmpeg - streamWithFFmpeg} Using custom Referer for channel %s: %s", r.Channel.Name, source.ReqReferrer)
		args = append(args, "-headers", fmt.Sprintf("Referer: %s\r\n", source.ReqReferrer))
	}

	// Add input URL and output format args
	args = append(args, "-i", streamURL)
	args = append(args, r.Config.FFmpegPreOutput...)
	args = append(args, "-f", "mpegts", "pipe:1")

	logger.Debug("{restream/ffmpeg - streamWithFFmpeg} Command args for channel %s: %v", r.Channel.Name, args)

	// Create cancellable context for FFmpeg process
	ctx, cancel := context.WithCancel(r.Ctx)
	defer cancel()

	// Setup FFmpeg command with process group for clean termination
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Create stdout pipe to read stream data
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		logger.Error("{restream/ffmpeg - streamWithFFmpeg} Failed to create stdout pipe for channel %s: %v", r.Channel.Name, err)
		return false, 0
	}

	// Create stderr pipe to capture FFmpeg errors/warnings
	stderr, err := cmd.StderrPipe()
	if err != nil {
		logger.Error("[FFMPEG] Failed to create stderr pipe for channel %s: %v", r.Channel.Name, err)
		return false, 0
	}

	// Start the FFmpeg process
	if err := cmd.Start(); err != nil {
		logger.Error("{restream/ffmpeg - streamWithFFmpeg} Failed to start FFmpeg for channel %s: %v", r.Channel.Name, err)
		return false, 0
	}

	logger.Debug("{restream/ffmpeg - streamWithFFmpeg} Process started for channel %s with PID: %d", r.Channel.Name, cmd.Process.Pid)

	// Ensure FFmpeg process is killed on exit (even if goroutine panics)
	// Using negative PID kills the entire process group
	defer func() {
		if cmd.Process != nil {
			logger.Debug("{restream/ffmpeg - streamWithFFmpeg} Killing process group for channel %s (PID: %d)", r.Channel.Name, cmd.Process.Pid)
			// Try graceful termination first
			syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
			// Wait up to 3 seconds for graceful exit
			done := make(chan error, 1)
			go func() {
				done <- cmd.Wait()
			}()
			select {
			case <-done:
				// Process exited gracefully
			case <-time.After(3 * time.Second):
				// Timeout - force kill
				syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
				cmd.Wait()
			}
		}
	}()

	// Goroutine to capture and log FFmpeg stderr output
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			// Log FFmpeg errors/warnings as debug since we already log major errors separately
			logger.Debug("{restream/ffmpeg - streamWithFFmpeg} Channel %s: %s", r.Channel.Name, scanner.Text())
		}
		if err := scanner.Err(); err != nil {
			logger.Error("{restream/ffmpeg - streamWithFFmpeg} reading stderr for channel %s: %v", r.Channel.Name, err)
		}
	}()

	// Initialize stream processing variables
	var totalBytes int64
	bufPtr := getStreamBuffer()
	buf := *bufPtr
	defer putStreamBuffer(bufPtr)
	lastActivityUpdate := time.Now()
	lastMetricUpdate := time.Now()
	consecutiveErrors := 0
	maxConsecutiveErrors := 10

	logger.Debug("{restream/ffmpeg - streamWithFFmpeg} Starting stream loop for channel %s", r.Channel.Name)

	// Main stream processing loop
	for {
		// Check if context was cancelled (shutdown or manual switch)
		select {
		case <-r.Ctx.Done():
			logger.Debug("{restream/ffmpeg - streamWithFFmpeg} Context cancelled for channel %s (manual switch: %v, total bytes: %d)",
				r.Channel.Name, r.ManualSwitch.Load(), totalBytes)
			if r.ManualSwitch.Load() {
				return true, totalBytes
			}
			return totalBytes > 1024*1024, totalBytes
		default:
		}

		// Count active clients - if none, stop streaming
		clientCount := 0
		r.Clients.Range(func(key string, value *types.RestreamClient) bool {
			clientCount++
			return true
		})

		if clientCount == 0 {
			logger.Debug("{restream/ffmpeg - streamWithFFmpeg} No clients remaining for channel %s, stopping (total bytes: %d)",
				r.Channel.Name, totalBytes)
			return totalBytes > 1024*1024, totalBytes
		}

		// Read data from FFmpeg stdout
		n, err := stdout.Read(buf)
		if n > 0 {
			data := buf[:n]

			// Attempt to write data to ring buffer
			if !r.SafeBufferWrite(data) {
				consecutiveErrors++
				logger.Warn("{restream/ffmpeg - streamWithFFmpeg} Buffer write failed for channel %s (consecutive errors: %d)",
					r.Channel.Name, consecutiveErrors)
				if consecutiveErrors >= maxConsecutiveErrors {
					logger.Error("{restream/ffmpeg - streamWithFFmpeg} Max consecutive buffer errors reached for channel %s, stopping",
						r.Channel.Name)
					return false, totalBytes
				}
				time.Sleep(10 * time.Millisecond)
				continue
			}

			// Reset error counter on successful write
			consecutiveErrors = 0

			// Distribute data to all connected clients
			activeClients := r.DistributeToClients(data)
			if activeClients == 0 {
				logger.Debug("{restream/ffmpeg - streamWithFFmpeg} No active clients after distribution for channel %s", r.Channel.Name)
				return totalBytes > 1024*1024, totalBytes
			}

			totalBytes += int64(n)

			// Update activity timestamp every 5 seconds
			now := time.Now()
			if now.Sub(lastActivityUpdate) > 5*time.Second {
				r.LastActivity.Store(now.Unix())
				lastActivityUpdate = now
			}

			// Update Prometheus metrics every 10 seconds
			if now.Sub(lastMetricUpdate) > 10*time.Second {
				metrics.BytesTransferred.WithLabelValues(r.Channel.Name, "downstream").Add(float64(n))
				lastMetricUpdate = now
			}

			// Log progress every 20MB transferred
			if totalBytes%(20*1024*1024) < int64(n) {
				logger.Debug("{restream/ffmpeg - streamWithFFmpeg} Channel %s: Streamed %d MB (clients: %d)",
					r.Channel.Name, totalBytes/(1024*1024), activeClients)
			}
		}

		// Handle read errors
		if err != nil {
			if err == io.EOF {
				// Stream ended normally
				success := totalBytes > 1024*1024
				logger.Debug("{restream/ffmpeg - streamWithFFmpeg} Stream ended for channel %s: %d bytes transferred (success: %v)",
					r.Channel.Name, totalBytes, success)
				return success, totalBytes
			}

			// Transient error - retry with backoff
			consecutiveErrors++
			if consecutiveErrors >= maxConsecutiveErrors {
				logger.Error("{restream/ffmpeg - streamWithFFmpeg} Max consecutive read errors for channel %s: %v (count: %d)",
					r.Channel.Name, err, consecutiveErrors)
				return false, totalBytes
			}

			logger.Warn("{restream/ffmpeg - streamWithFFmpeg} Read error for channel %s: %v (consecutive: %d, retrying...)",
				r.Channel.Name, err, consecutiveErrors)
			time.Sleep(100 * time.Millisecond)
			continue
		}

		// Successful read with no errors - reset counter
		consecutiveErrors = 0
	}
}
