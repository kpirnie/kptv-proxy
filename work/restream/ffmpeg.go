package restream

import (
	"context"
	"fmt"
	"io"
	"kptv-proxy/work/config"
	"os/exec"
	"strings"
	"syscall"
	"time"
)
// streamWithFFmpeg uses ffmpeg to proxy the stream instead of Go-based restreaming
func (r *Restream) streamWithFFmpeg(streamURL string) (bool, int64) {
	// Find the source for this URL
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
		r.Logger.Printf("[FFMPEG] Starting FFmpeg proxy for channel %s", 
			r.Channel.Name)
	}

	// Build ffmpeg command
	args := []string{}
	
	// Add pre-input arguments
	args = append(args, r.Config.FFmpegPreInput...)
	
	// Add user agent if source has one
	if source != nil && source.UserAgent != "" {
		args = append(args, "-user_agent", source.UserAgent)
	}
	
	// Add headers if source has them
	if source != nil && source.ReqReferrer != "" {
		args = append(args, "-headers", fmt.Sprintf("Referer: %s\r\n", source.ReqReferrer))
	}
	
	// Add input
	args = append(args, "-i", streamURL)
	
	// Add pre-output arguments
	args = append(args, r.Config.FFmpegPreOutput...)
	
	// Add output format and pipe
	args = append(args, "-f", "mpegts", "-")

	// Create ffmpeg process with context
	ctx, cancel := context.WithCancel(r.Ctx)
	defer cancel()
	
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}
	
	// Get stdout pipe for reading stream data
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		if r.Config.Debug {
			r.Logger.Printf("[FFMPEG] Failed to create stdout pipe: %v", err)
		}
		return false, 0
	}
	
	// Start ffmpeg process
	if err := cmd.Start(); err != nil {
		if r.Config.Debug {
			r.Logger.Printf("[FFMPEG] Failed to start ffmpeg: %v", err)
		}
		return false, 0
	}
	
	// Ensure process cleanup
	defer func() {
		if cmd.Process != nil {
			syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			cmd.Wait()
		}
	}()
	
	// Stream data from ffmpeg to clients using the configured buffer
	var totalBytes int64
	buffer := make([]byte, 32*1024) // 32KB read buffer
	lastActivityUpdate := time.Now()
	
	for {
		select {
		case <-r.Ctx.Done():
			if r.Config.Debug {
				r.Logger.Printf("[FFMPEG] Context cancelled for channel %s", r.Channel.Name)
			}
			return totalBytes > 0, totalBytes
		default:
		}
		
		// Check for clients
		clientCount := 0
		r.Clients.Range(func(_, _ interface{}) bool {
			clientCount++
			return true
		})
		
		if clientCount == 0 {
			if r.Config.Debug {
				r.Logger.Printf("[FFMPEG] No clients for channel %s", r.Channel.Name)
			}
			return totalBytes > 0, totalBytes
		}
		
		// Read from ffmpeg stdout
		n, err := stdout.Read(buffer)
		if n > 0 {
			data := buffer[:n]
			
			// Write to the configured ring buffer
			if !r.SafeBufferWrite(data) {
				if r.Config.Debug {
					r.Logger.Printf("[FFMPEG] Buffer write failed for channel %s", r.Channel.Name)
				}
				return false, totalBytes
			}
			
			// Distribute to clients from the buffer
			activeClients := r.DistributeToClients(data)
			if activeClients == 0 {
				if r.Config.Debug {
					r.Logger.Printf("[FFMPEG] No active clients for channel %s", r.Channel.Name)
				}
				return false, totalBytes
			}
			
			totalBytes += int64(n)
			
			// Update activity timestamp periodically
			now := time.Now()
			if now.Sub(lastActivityUpdate) > 5*time.Second {
				r.LastActivity.Store(now.Unix())
				lastActivityUpdate = now
			}
			
			// Log progress periodically
			if r.Config.Debug && totalBytes%(10*1024*1024) < int64(n) {
				r.Logger.Printf("[FFMPEG] Channel %s: Streamed %d MB", 
					r.Channel.Name, totalBytes/(1024*1024))
			}
		}
		
		if err != nil {
			if err == io.EOF {
				if r.Config.Debug {
					r.Logger.Printf("[FFMPEG] Stream ended for channel %s", r.Channel.Name)
				}
				return totalBytes > 0, totalBytes
			}
			
			if r.Config.Debug {
				r.Logger.Printf("[FFMPEG] Read error for channel %s: %v", r.Channel.Name, err)
			}
			return false, totalBytes
		}
	}
}