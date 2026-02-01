package restream

import (
	"context"
	"encoding/json"
	"kptv-proxy/work/logger"
	"kptv-proxy/work/types"
	"math/rand"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// StartStatsCollection initiates background statistics gathering for the active stream,
// launching a goroutine that performs periodic FFprobe analysis to collect codec information,
// resolution, bitrate, and other stream characteristics for monitoring and display purposes.
func (r *Restream) StartStatsCollection() {
	go r.collectStreamStats()
}

// collectStreamStats performs immediate initial stream analysis followed by periodic
// monitoring at configured intervals, using FFprobe to gather comprehensive stream
// metadata including container format, video/audio codecs, resolution, frame rate,
// and bitrate information for operational monitoring and quality assessment.
func (r *Restream) collectStreamStats() {
	// Run initial stats collection immediately
	stats := r.analyzeStreamStats()
	if stats.Valid {
		r.Restreamer.Stats.Mu.Lock()
		r.Restreamer.Stats.Container = stats.Container
		r.Restreamer.Stats.VideoCodec = stats.VideoCodec
		r.Restreamer.Stats.AudioCodec = stats.AudioCodec
		r.Restreamer.Stats.VideoResolution = stats.VideoResolution
		r.Restreamer.Stats.FPS = stats.FPS
		r.Restreamer.Stats.AudioChannels = stats.AudioChannels
		r.Restreamer.Stats.Bitrate = stats.Bitrate
		r.Restreamer.Stats.StreamType = stats.StreamType
		r.Restreamer.Stats.Valid = true
		r.Restreamer.Stats.LastUpdated = time.Now().Unix()
		r.Restreamer.Stats.Mu.Unlock()

		logger.Debug("{restream/stats - collectStreamStats} Channel %s: Container=%s, Video=%s@%s (%.2f fps), Audio=%s (%s), Bitrate=%d",
			r.Channel.Name, stats.Container, stats.VideoCodec, stats.VideoResolution,
			stats.FPS, stats.AudioCodec, stats.AudioChannels, stats.Bitrate)

	}

	interval := 5 * time.Minute // Default to 5 minutes
	if r.Config.Debug {
		interval = 1 * time.Minute // Only 1 min in debug
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Add jitter to prevent thundering herd
	jitter := time.Duration(rand.Intn(30)) * time.Second
	time.Sleep(jitter)

	for {
		select {
		case <-r.Ctx.Done():
			return
		case <-ticker.C:
			if !r.Running.Load() {
				return
			}

			stats := r.analyzeStreamStats()
			if stats.Valid {
				r.Restreamer.Stats.Mu.Lock()
				r.Restreamer.Stats.Container = stats.Container
				r.Restreamer.Stats.VideoCodec = stats.VideoCodec
				r.Restreamer.Stats.AudioCodec = stats.AudioCodec
				r.Restreamer.Stats.VideoResolution = stats.VideoResolution
				r.Restreamer.Stats.FPS = stats.FPS
				r.Restreamer.Stats.AudioChannels = stats.AudioChannels
				r.Restreamer.Stats.Bitrate = stats.Bitrate
				r.Restreamer.Stats.StreamType = stats.StreamType
				r.Restreamer.Stats.Valid = true
				r.Restreamer.Stats.LastUpdated = time.Now().Unix()
				r.Restreamer.Stats.Mu.Unlock()

				logger.Debug("{restream/stats - collectStreamStats} Channel %s: Container=%s, Video=%s@%s (%.2f fps), Audio=%s (%s), Bitrate=%d",
					r.Channel.Name, stats.Container, stats.VideoCodec, stats.VideoResolution,
					stats.FPS, stats.AudioCodec, stats.AudioChannels, stats.Bitrate)

			}
		}
	}
}

/**
 * analyzeStreamStats executes FFprobe analysis on buffered stream data to extract
 * comprehensive stream characteristics including container format, video codec,
 * audio codec, resolution, frame rate, bitrate, and channel configuration.
 *
 * This function performs deep inspection of stream data using FFprobe to gather
 * technical metadata that can be used for monitoring, debugging, and display purposes.
 * The analysis is performed on a recent sample of buffered data (up to 3MB) to avoid
 * processing the entire stream.
 *
 * Analysis process:
 * - Extracts recent buffer data (3MB maximum)
 * - Pipes data to FFprobe with appropriate probe size limits
 * - Parses JSON output for format and stream information
 * - Extracts video properties (codec, resolution, framerate)
 * - Extracts audio properties (codec, channels)
 * - Determines stream type from container format
 *
 * Error handling:
 * - Returns empty stats if buffer unavailable or destroyed
 * - Returns empty stats if FFprobe fails or times out
 * - Handles JSON parsing errors gracefully
 * - Sets Valid flag only if meaningful data extracted
 *
 * @return StreamStats structure with validity flag indicating successful analysis
 */
func (r *Restream) analyzeStreamStats() types.StreamStats {
	logger.Debug("{restream/stats - analyzeStreamStats} Starting stream analysis for channel %s", r.Channel.Name)

	stats := types.StreamStats{}

	// Verify buffer is available and valid
	if r.Buffer == nil || r.Buffer.IsDestroyed() {
		logger.Warn("{restream/stats - analyzeStreamStats} Buffer unavailable or destroyed for channel %s", r.Channel.Name)
		return stats
	}

	// Extract recent stream data for analysis (3MB sample)
	streamData := r.Buffer.PeekRecentData(3 * 1024 * 1024)
	if len(streamData) == 0 {
		logger.Warn("{restream/stats - analyzeStreamStats} No stream data available in buffer for channel %s", r.Channel.Name)
		return stats
	}

	logger.Debug("{restream/stats - analyzeStreamStats} Extracted %d bytes from buffer for channel %s", len(streamData), r.Channel.Name)

	// Create timeout context to prevent FFprobe from hanging
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Build FFprobe command with JSON output and optimal probe settings
	cmd := exec.CommandContext(ctx, "ffprobe",
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		"-analyzeduration", "2M",
		"-probesize", "2M",
		"-i", "pipe:0")

	// Set process group for clean termination
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	logger.Debug("{restream/stats - analyzeStreamStats} Starting FFprobe process for channel %s", r.Channel.Name)

	// Create stdin pipe to send stream data to FFprobe
	stdin, err := cmd.StdinPipe()
	if err != nil {
		logger.Error("{restream/stats - analyzeStreamStats} Failed to create stdin pipe for channel %s: %v", r.Channel.Name, err)
		return stats
	}

	// Goroutine to write stream data to FFprobe stdin
	go func() {
		defer stdin.Close()
		maxData := 2 * 1024 * 1024 // Limit to 2MB for faster analysis
		if len(streamData) > maxData {
			logger.Debug("{restream/stats - analyzeStreamStats} Writing %d bytes (truncated) to FFprobe for channel %s", maxData, r.Channel.Name)
			stdin.Write(streamData[:maxData])
		} else {
			logger.Debug("{restream/stats - analyzeStreamStats} Writing %d bytes to FFprobe for channel %s", len(streamData), r.Channel.Name)
			stdin.Write(streamData)
		}
	}()

	// Execute FFprobe and capture JSON output
	output, err := cmd.Output()
	if err != nil {
		logger.Error("{restream/stats - analyzeStreamStats} FFprobe execution failed for channel %s: %v", r.Channel.Name, err)
		// Ensure process is killed on error
		if cmd.Process != nil {
			logger.Debug("{restream/stats - analyzeStreamStats} Killing FFprobe process for channel %s", r.Channel.Name)
			syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return stats
	}

	logger.Debug("{restream/stats - analyzeStreamStats} FFprobe completed successfully for channel %s, parsing output (%d bytes)", r.Channel.Name, len(output))

	// Define structure to parse FFprobe JSON output
	var result struct {
		Format struct {
			FormatName string `json:"format_name"`
			BitRate    string `json:"bit_rate"`
		} `json:"format"`
		Streams []struct {
			CodecType     string `json:"codec_type"`
			CodecName     string `json:"codec_name"`
			Width         int    `json:"width"`
			Height        int    `json:"height"`
			RFrameRate    string `json:"r_frame_rate"`
			AvgFrameRate  string `json:"avg_frame_rate"`
			Channels      int    `json:"channels"`
			ChannelLayout string `json:"channel_layout"`
		} `json:"streams"`
	}

	// Parse JSON output from FFprobe
	if err := json.Unmarshal(output, &result); err != nil {
		logger.Error("{restream/stats - analyzeStreamStats} Failed to parse FFprobe JSON for channel %s: %v", r.Channel.Name, err)
		return stats
	}

	logger.Debug("{restream/stats - analyzeStreamStats} Parsed FFprobe output for channel %s: format=%s, streams=%d",
		r.Channel.Name, result.Format.FormatName, len(result.Streams))

	// Extract container format
	stats.Container = result.Format.FormatName
	logger.Debug("{restream/stats - analyzeStreamStats} Container format for channel %s: %s", r.Channel.Name, stats.Container)

	// Parse bitrate if available
	if result.Format.BitRate != "" {
		if bitrate, err := strconv.ParseInt(result.Format.BitRate, 10, 64); err == nil {
			stats.Bitrate = bitrate
			logger.Debug("{restream/stats - analyzeStreamStats} Bitrate for channel %s: %d bps (%d kbps)",
				r.Channel.Name, bitrate, bitrate/1000)
		} else {
			logger.Warn("[STATS_ANALYZE] Failed to parse bitrate for channel %s: %v", r.Channel.Name, err)
		}
	}

	// Process each stream (video/audio) in the container
	for i, stream := range result.Streams {
		if stream.CodecType == "video" {
			logger.Debug("{restream/stats - analyzeStreamStats} Processing video stream %d for channel %s", i, r.Channel.Name)

			stats.VideoCodec = stream.CodecName
			logger.Debug("{restream/stats - analyzeStreamStats} Video codec for channel %s: %s", r.Channel.Name, stats.VideoCodec)

			// Extract resolution if available
			if stream.Width > 0 && stream.Height > 0 {
				stats.VideoResolution = strconv.Itoa(stream.Width) + "x" + strconv.Itoa(stream.Height)
				logger.Debug("{restream/stats - analyzeStreamStats} Video resolution for channel %s: %s", r.Channel.Name, stats.VideoResolution)
			}

			// Parse frame rate from avg_frame_rate or r_frame_rate
			fps := r.parseFrameRate(stream.AvgFrameRate)
			if fps == 0 {
				fps = r.parseFrameRate(stream.RFrameRate)
			}
			stats.FPS = fps
			if fps > 0 {
				logger.Debug("{restream/stats - analyzeStreamStats} Frame rate for channel %s: %.2f fps", r.Channel.Name, fps)
			}
		} else if stream.CodecType == "audio" {
			logger.Debug("{restream/stats - analyzeStreamStats} Processing audio stream %d for channel %s", i, r.Channel.Name)

			stats.AudioCodec = stream.CodecName
			logger.Debug("{restream/stats - analyzeStreamStats} Audio codec for channel %s: %s", r.Channel.Name, stats.AudioCodec)

			// Extract channel configuration
			if stream.ChannelLayout != "" {
				stats.AudioChannels = stream.ChannelLayout
				logger.Debug("{restream/stats - analyzeStreamStats} Audio channels for channel %s: %s", r.Channel.Name, stats.AudioChannels)
			} else if stream.Channels > 0 {
				stats.AudioChannels = strconv.Itoa(stream.Channels) + " channels"
				logger.Debug("{restream/stats - analyzeStreamStats} Audio channels for channel %s: %d channels", r.Channel.Name, stream.Channels)
			}
		}
	}

	// Determine human-readable stream type from container format
	if strings.Contains(stats.Container, "mpegts") {
		stats.StreamType = "MPEG-TS"
	} else if strings.Contains(stats.Container, "hls") {
		stats.StreamType = "HLS"
	} else if strings.Contains(stats.Container, "mp4") {
		stats.StreamType = "MP4"
	} else if strings.Contains(stats.Container, "matroska") {
		stats.StreamType = "MKV"
	} else {
		stats.StreamType = stats.Container
	}

	logger.Debug("{restream/stats - analyzeStreamStats} Stream type for channel %s: %s", r.Channel.Name, stats.StreamType)

	// Mark stats as valid if we extracted at least codec information
	stats.Valid = stats.VideoCodec != "" || stats.AudioCodec != ""

	if stats.Valid {
		logger.Debug("{restream/stats - analyzeStreamStats} Analysis complete for channel %s: type=%s, video=%s, audio=%s, resolution=%s, fps=%.2f",
			r.Channel.Name, stats.StreamType, stats.VideoCodec, stats.AudioCodec, stats.VideoResolution, stats.FPS)
	} else {
		logger.Warn("{restream/stats - analyzeStreamStats} Analysis incomplete for channel %s: no codec information extracted", r.Channel.Name)
	}

	return stats
}

// parseFrameRate converts FFprobe frame rate strings in "numerator/denominator" format
// to decimal floating-point values for accurate frame rate representation, handling
// various standard and non-standard frame rate specifications while returning zero
// for invalid or unparseable frame rate strings.
func (r *Restream) parseFrameRate(frameRate string) float64 {
	parts := strings.Split(frameRate, "/")
	if len(parts) != 2 {
		return 0
	}

	num, err1 := strconv.ParseFloat(parts[0], 64)
	den, err2 := strconv.ParseFloat(parts[1], 64)

	if err1 != nil || err2 != nil || den == 0 {
		return 0
	}

	return num / den
}
