package restream

import (
	"context"
	"encoding/json"
	"kptv-proxy/work/types"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func (r *Restream) StartStatsCollection() {
	go r.collectStreamStats()
}

func (r *Restream) collectStreamStats() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

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

				if r.Config.Debug {
					r.Logger.Printf("[STATS] Channel %s: Container=%s, Video=%s@%s (%.2f fps), Audio=%s (%s), Bitrate=%d",
						r.Channel.Name, stats.Container, stats.VideoCodec, stats.VideoResolution,
						stats.FPS, stats.AudioCodec, stats.AudioChannels, stats.Bitrate)
				}
			}
		}
	}
}

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
			CodecType      string `json:"codec_type"`
			CodecName      string `json:"codec_name"`
			Width          int    `json:"width"`
			Height         int    `json:"height"`
			RFrameRate     string `json:"r_frame_rate"`
			AvgFrameRate   string `json:"avg_frame_rate"`
			Channels       int    `json:"channels"`
			ChannelLayout  string `json:"channel_layout"`
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
