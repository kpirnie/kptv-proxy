package restream

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"kptv-proxy/work/config"
	"kptv-proxy/work/metrics"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// streamWithFFmpeg uses ffmpeg to proxy the stream instead of Go-based restreaming
func (r *Restream) streamWithFFmpeg(streamURL string) (bool, int64) {
	var source *config.SourceConfig
	r.Channel.Mu.RLock()
	for _, stream := range r.Channel.Streams {
		if stream.URL == streamURL || strings.Contains(streamURL, stream.Source.URL) {
			source = stream.Source
			break
		}
	}
	r.Channel.Mu.RUnlock()

	if r.Config.Debug {
		r.Logger.Printf("[FFMPEG] Starting FFmpeg for channel %s", r.Channel.Name)
	}

	args := []string{"-hide_banner", "-loglevel", "error"}
	args = append(args, r.Config.FFmpegPreInput...)

	if source != nil && source.UserAgent != "" {
		args = append(args, "-user_agent", source.UserAgent)
	}

	if source != nil && source.ReqReferrer != "" {
		args = append(args, "-headers", fmt.Sprintf("Referer: %s\r\n", source.ReqReferrer))
	}

	args = append(args, "-i", streamURL)
	args = append(args, r.Config.FFmpegPreOutput...)
	args = append(args, "-f", "mpegts", "pipe:1")

	ctx, cancel := context.WithCancel(r.Ctx)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		if r.Config.Debug {
			r.Logger.Printf("[FFMPEG] Failed to create stdout pipe: %v", err)
		}
		return false, 0
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		if r.Config.Debug {
			r.Logger.Printf("[FFMPEG] Failed to create stderr pipe: %v", err)
		}
		return false, 0
	}

	if err := cmd.Start(); err != nil {
		if r.Config.Debug {
			r.Logger.Printf("[FFMPEG] Failed to start: %v", err)
		}
		return false, 0
	}

	defer func() {
		if cmd.Process != nil {
			syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			cmd.Wait()
		}
	}()

	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			if r.Config.Debug {
				r.Logger.Printf("[FFMPEG_ERROR] Channel %s: %s", r.Channel.Name, scanner.Text())
			}
		}
	}()

	var totalBytes int64
	buffer := make([]byte, 64*1024)
	lastActivityUpdate := time.Now()
	lastMetricUpdate := time.Now()
	consecutiveErrors := 0
	maxConsecutiveErrors := 10

	for {
		select {
		case <-r.Ctx.Done():
			if r.ManualSwitch.Load() {
				return true, totalBytes
			}
			return totalBytes > 1024*1024, totalBytes
		default:
		}

		clientCount := 0
		r.Clients.Range(func(_, _ interface{}) bool {
			clientCount++
			return true
		})

		if clientCount == 0 {
			if r.Config.Debug {
				r.Logger.Printf("[FFMPEG] No clients for channel %s", r.Channel.Name)
			}
			return totalBytes > 1024*1024, totalBytes
		}

		n, err := stdout.Read(buffer)
		if n > 0 {
			data := buffer[:n]

			if !r.SafeBufferWrite(data) {
				consecutiveErrors++
				if consecutiveErrors >= maxConsecutiveErrors {
					return false, totalBytes
				}
				time.Sleep(10 * time.Millisecond)
				continue
			}

			consecutiveErrors = 0
			activeClients := r.DistributeToClients(data)
			if activeClients == 0 {
				return totalBytes > 1024*1024, totalBytes
			}

			totalBytes += int64(n)

			now := time.Now()
			if now.Sub(lastActivityUpdate) > 5*time.Second {
				r.LastActivity.Store(now.Unix())
				lastActivityUpdate = now
			}

			if now.Sub(lastMetricUpdate) > 10*time.Second {
				metrics.BytesTransferred.WithLabelValues(r.Channel.Name, "downstream").Add(float64(n))
				lastMetricUpdate = now
			}

			if r.Config.Debug && totalBytes%(20*1024*1024) < int64(n) {
				r.Logger.Printf("[FFMPEG] Channel %s: Streamed %d MB", r.Channel.Name, totalBytes/(1024*1024))
			}
		}

		if err != nil {
			if err == io.EOF {
				success := totalBytes > 1024*1024
				if r.Config.Debug {
					r.Logger.Printf("[FFMPEG] Stream ended: %d bytes", totalBytes)
				}
				return success, totalBytes
			}

			consecutiveErrors++
			if consecutiveErrors >= maxConsecutiveErrors {
				if r.Config.Debug {
					r.Logger.Printf("[FFMPEG] Read error: %v (consecutive: %d)", err, consecutiveErrors)
				}
				return false, totalBytes
			}

			time.Sleep(100 * time.Millisecond)
			continue
		}

		consecutiveErrors = 0
	}
}
