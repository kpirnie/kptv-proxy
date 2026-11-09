package restream

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"kptv-proxy/work/config"
	"kptv-proxy/work/constants"
	"kptv-proxy/work/logger"
	"kptv-proxy/work/metrics"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

// sanitizeHeaderValue strips CR, LF and other control characters from a
// configured header value so it cannot inject additional request headers
// through the ffmpeg -headers / -user_agent arguments.
func sanitizeHeaderValue(v string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, v)
}

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
	return r.streamWithFFmpegAV(streamURL, "", nil)
}

// streamWithFFmpegAV runs FFmpeg on streamURL, adding audioURL as a second
// input when the selected HLS variant carries its audio in a separate
// EXT-X-MEDIA rendition. source supplies the request headers; when nil it is
// looked up from the channel's streams. A variant URL resolved from a master
// playlist often shares nothing with the source URL, so callers that have the
// source should pass it.
func (r *Restream) streamWithFFmpegAV(streamURL string, audioURL string, source *config.SourceConfig) (bool, int64) {
	if source == nil {
		r.Channel.Mu.RLock()
		for _, stream := range r.Channel.Streams {
			if stream.URL == streamURL || strings.Contains(streamURL, stream.Source.URL) {
				source = stream.Source
				break
			}
		}
		r.Channel.Mu.RUnlock()
	}

	logger.Debug("{restream/ffmpeg - streamWithFFmpeg} Starting FFmpeg for channel %s with URL: %s", r.Channel.Name, streamURL)

	// Build FFmpeg command arguments
	// Start with base args to suppress banner and set error-only logging
	// -progress reports the output position on stderr so a restart can
	// continue this session's timeline (see TimelineOffset below)
	args := []string{"-hide_banner", "-loglevel", "error",
		"-progress", "pipe:2", "-stats_period", fmt.Sprintf("%.3f", constants.Internal.FFmpegProgressPeriod.Seconds())}
	args = append(args, r.Config.FFmpegPreInput...)

	// Add custom User-Agent if configured for this source
	if source != nil && source.UserAgent != "" {
		ua := sanitizeHeaderValue(source.UserAgent)
		logger.Debug("{restream/ffmpeg - streamWithFFmpeg} Using custom User-Agent for channel %s: %s", r.Channel.Name, ua)
		args = append(args, "-user_agent", ua)
	}

	// Add Referer header if configured for this source
	if source != nil && source.ReqReferrer != "" {
		ref := sanitizeHeaderValue(source.ReqReferrer)
		logger.Debug("{restream/ffmpeg - streamWithFFmpeg} Using custom Referer for channel %s: %s", r.Channel.Name, ref)
		args = append(args, "-headers", fmt.Sprintf("Referer: %s\r\n", ref))
	}

	// Add input URL and output format args
	args = append(args, "-i", streamURL)

	// Separate audio rendition: same input options and headers as the video,
	// then map video from the first input and audio from the second
	if audioURL != "" {
		args = append(args, r.Config.FFmpegPreInput...)
		if source != nil && source.UserAgent != "" {
			args = append(args, "-user_agent", sanitizeHeaderValue(source.UserAgent))
		}
		if source != nil && source.ReqReferrer != "" {
			args = append(args, "-headers", fmt.Sprintf("Referer: %s\r\n", sanitizeHeaderValue(source.ReqReferrer)))
		}
		args = append(args, "-i", audioURL, "-map", "0:v:0?", "-map", "1:a:0?")
	}
	args = append(args, r.Config.FFmpegPreOutput...)

	// Each FFmpeg starts its output clock near zero. Within one client
	// session, start this one where the previous process left off; a clock
	// that jumps backwards mid-stream stalls players such as Plex.
	timelineOffset := time.Duration(r.TimelineOffset.Load()) * time.Microsecond
	args = append(args, "-output_ts_offset", fmt.Sprintf("%.3f", timelineOffset.Seconds()))
	args = append(args, "pipe:1")

	logger.Debug("{restream/ffmpeg - streamWithFFmpeg} Command args for channel %s: %v", r.Channel.Name, args)

	// Create cancellable context for FFmpeg process
	ctx, cancel := context.WithCancel(r.Context())
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
		logger.Error("{restream/ffmpeg - streamWithFFmpeg} Failed to create stderr pipe for channel %s: %v", r.Channel.Name, err)
		return false, 0
	}

	// Start the FFmpeg process
	if err := cmd.Start(); err != nil {
		logger.Error("{restream/ffmpeg - streamWithFFmpeg} Failed to start FFmpeg for channel %s: %v", r.Channel.Name, err)
		return false, 0
	}

	logger.Debug("{restream/ffmpeg - streamWithFFmpeg} Process started for channel %s with PID: %d", r.Channel.Name, cmd.Process.Pid)

	// Advance the session timeline past this process's output once it has
	// exited (deferred before the kill below, so it runs after it). The last
	// progress report can trail the real output by up to one period, which
	// FFmpegTimelineGap covers, so the next process never starts earlier.
	var lastOutUs atomic.Int64
	defer func() {
		if out := lastOutUs.Load(); out > 0 {
			r.TimelineOffset.Add(out + constants.Internal.FFmpegTimelineGap.Microseconds())
		}
	}()

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
			case <-time.After(constants.Internal.FFmpegGracefulTermTimeout):
				// Timeout - force kill, then wait on the SAME Wait() running in the
				// goroutine. A second cmd.Wait() here races the goroutine's call.
				syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
				<-done
			}
		}
	}()

	// Goroutine to capture and log FFmpeg stderr output
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()

			// -progress key=value lines: keep the output position, don't log them
			if key, value, ok := strings.Cut(line, "="); ok && !strings.Contains(key, " ") {
				if key == "out_time_us" {
					if us, err := strconv.ParseInt(value, 10, 64); err == nil && us > 0 {
						lastOutUs.Store(us)
					}
				}
				continue
			}

			// Log FFmpeg errors/warnings as debug since we already log major errors separately
			logger.Debug("{restream/ffmpeg - streamWithFFmpeg} Channel %s: %s", r.Channel.Name, line)
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
	maxConsecutiveErrors := constants.Internal.FFmpegMaxConsecutiveErrors

	// a stalled input leaves ffmpeg running with nothing on stdout, blocking
	// Read indefinitely — killing it on the idle window ends the read so the
	// attempt fails over instead of waiting on the watcher
	var idled atomic.Bool
	idleTimer := time.AfterFunc(constants.Internal.StreamReadIdleTimeout, func() {
		idled.Store(true)
		logger.Warn("{restream/ffmpeg - streamWithFFmpeg} Channel %s: No output for %v, stopping FFmpeg", r.Channel.Name, constants.Internal.StreamReadIdleTimeout)
		cancel()
	})
	defer idleTimer.Stop()

	logger.Debug("{restream/ffmpeg - streamWithFFmpeg} Starting stream loop for channel %s", r.Channel.Name)

	// Main stream processing loop
	for {
		// Check if context was cancelled (shutdown or manual switch)
		select {
		case <-r.Context().Done():
			logger.Debug("{restream/ffmpeg - streamWithFFmpeg} Context cancelled for channel %s (manual switch: %v, total bytes: %d)",
				r.Channel.Name, r.SwitchPending(), totalBytes)
			if r.SwitchPending() {
				return true, totalBytes
			}
			return totalBytes > constants.Internal.StreamMinViableBytes, totalBytes
		default:
		}

		// Count active clients - if none, stop streaming
		clientCount := int(r.ClientCount.Load())

		if clientCount == 0 {
			logger.Debug("{restream/ffmpeg - streamWithFFmpeg} No clients remaining for channel %s, stopping (total bytes: %d)",
				r.Channel.Name, totalBytes)
			return totalBytes > constants.Internal.StreamMinViableBytes, totalBytes
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
				return totalBytes > constants.Internal.StreamMinViableBytes, totalBytes
			}

			totalBytes += int64(n)
			metrics.TotalBytesTransferred.Add(int64(n))

			// Update activity timestamp every 5 seconds
			now := time.Now()
			if now.Sub(lastActivityUpdate) > constants.Internal.FFmpegActivityUpdateInterval {
				r.LastActivity.Store(now.Unix())
				lastActivityUpdate = now
				idleTimer.Reset(constants.Internal.StreamReadIdleTimeout)
			}

			// Update Prometheus metrics every 10 seconds
			if now.Sub(lastMetricUpdate) > constants.Internal.FFmpegMetricUpdateInterval {
				metrics.BytesTransferred.WithLabelValues(r.Channel.Name, "downstream").Add(float64(n))
				lastMetricUpdate = now
			}

			// Log progress every 20MB transferred
			if totalBytes%(constants.Internal.FFmpegLogProgressInterval) < int64(n) {
				logger.Debug("{restream/ffmpeg - streamWithFFmpeg} Channel %s: Streamed %d MB (clients: %d)",
					r.Channel.Name, totalBytes/(1024*1024), activeClients)
			}
		}

		// Handle read errors
		if err != nil {
			if err == io.EOF {
				// an idle kill after output had started is a stall: the
				// session stopped advancing, so the loop restarts the source
				if idled.Load() && totalBytes > 0 {
					r.AttemptStalled.Store(true)
				}

				// Stream ended normally
				success := totalBytes > constants.Internal.StreamMinViableBytes && !idled.Load()
				logger.Debug("{restream/ffmpeg - streamWithFFmpeg} Stream ended for channel %s: %d bytes transferred (success: %v)",
					r.Channel.Name, totalBytes, success)

				// EOF drain pause is handled once by the caller (Stream)
				// before reconnecting — sleeping here as well doubled the
				// gap to ~4s and drained player buffers on short segments.
				return success, totalBytes
			}

			// a failed read on ffmpeg's stdout pipe means the pipe is gone —
			// re-reading it only returns the same error, so fail straight away
			logger.Error("{restream/ffmpeg - streamWithFFmpeg} Read error for channel %s: %v", r.Channel.Name, err)
			return false, totalBytes
		}

		// Successful read with no errors - reset counter
		consecutiveErrors = 0
	}
}
