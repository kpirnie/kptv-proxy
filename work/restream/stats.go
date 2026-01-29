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

		logger.Debug("[STATS] Channel %s: Container=%s, Video=%s@%s (%.2f fps), Audio=%s (%s), Bitrate=%d",
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

				logger.Debug("[STATS] Channel %s: Container=%s, Video=%s@%s (%.2f fps), Audio=%s (%s), Bitrate=%d",
					r.Channel.Name, stats.Container, stats.VideoCodec, stats.VideoResolution,
					stats.FPS, stats.AudioCodec, stats.AudioChannels, stats.Bitrate)

			}
		}
	}
}

// analyzeStreamStats executes FFprobe analysis on buffered stream data to extract
// comprehensive stream characteristics including container format, video codec,
// audio codec, resolution, frame rate, bitrate, and channel configuration, returning
// a StreamStats structure with validity flag indicating successful analysis completion.
func (r *Restream) analyzeStreamStats() types.StreamStats {
	stats := types.StreamStats{}

	if r.Buffer == nil || r.Buffer.IsDestroyed() {
		return stats
	}

	streamData := r.Buffer.PeekRecentData(3 * 1024 * 1024)
	if len(streamData) == 0 {
		return stats
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ffprobe",
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		"-analyzeduration", "2M",
		"-probesize", "2M",
		"-i", "pipe:0")

	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return stats
	}

	go func() {
		defer stdin.Close()
		maxData := 2 * 1024 * 1024
		if len(streamData) > maxData {
			stdin.Write(streamData[:maxData])
		} else {
			stdin.Write(streamData)
		}
	}()

	output, err := cmd.Output()
	if err != nil {
		if cmd.Process != nil {
			syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return stats
	}

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

	if err := json.Unmarshal(output, &result); err != nil {
		return stats
	}

	stats.Container = result.Format.FormatName
	if result.Format.BitRate != "" {
		if bitrate, err := strconv.ParseInt(result.Format.BitRate, 10, 64); err == nil {
			stats.Bitrate = bitrate
		}
	}

	for _, stream := range result.Streams {
		if stream.CodecType == "video" {
			stats.VideoCodec = stream.CodecName
			if stream.Width > 0 && stream.Height > 0 {
				stats.VideoResolution = strconv.Itoa(stream.Width) + "x" + strconv.Itoa(stream.Height)
			}

			fps := r.parseFrameRate(stream.AvgFrameRate)
			if fps == 0 {
				fps = r.parseFrameRate(stream.RFrameRate)
			}
			stats.FPS = fps
		} else if stream.CodecType == "audio" {
			stats.AudioCodec = stream.CodecName
			if stream.ChannelLayout != "" {
				stats.AudioChannels = stream.ChannelLayout
			} else if stream.Channels > 0 {
				stats.AudioChannels = strconv.Itoa(stream.Channels) + " channels"
			}
		}
	}

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

	stats.Valid = stats.VideoCodec != "" || stats.AudioCodec != ""
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
