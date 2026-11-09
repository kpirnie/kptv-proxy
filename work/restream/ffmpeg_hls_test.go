package restream

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// In FFmpeg mode a master playlist behind a redirect is resolved by KPTV:
// relative URIs against the redirected location, the highest-quality variant
// first, its separate audio rendition as a second input, and the source's
// User-Agent on both inputs.
func TestFFmpegModeResolvesMasterPlaylist(t *testing.T) {
	runs := fakeFFmpeg(t, ffmpegHealthy)
	mux := http.NewServeMux()
	mux.HandleFunc("/r/channel.m3u8", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/cdn/live/master.m3u8", http.StatusFound)
	})
	mux.HandleFunc("/cdn/live/master.m3u8", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		fmt.Fprint(w, `#EXTM3U
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aud",NAME="English",DEFAULT=YES,URI="audio_en.m3u8"
#EXT-X-STREAM-INF:BANDWIDTH=9000000,RESOLUTION=1280x720,FRAME-RATE=50.000,CODECS="avc1.640020,mp4a.40.2",AUDIO="aud"
v720.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=8000000,RESOLUTION=1920x1080,FRAME-RATE=50.000,CODECS="avc1.64002A,mp4a.40.2",AUDIO="aud"
v1080.m3u8
`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	r, _ := newRestreamerForURL(t, srv.URL, srv.URL+"/r/channel.m3u8")
	r.Config.FFmpegMode = true
	r.StreamFromSource(0)

	got := runs()
	if len(got) != 1 {
		t.Fatalf("FFmpeg ran %d times, want 1: %q", len(got), got)
	}
	args := got[0]
	for _, want := range []string{
		"-i " + srv.URL + "/cdn/live/v1080.m3u8",    // best variant, resolved against the redirect
		"-i " + srv.URL + "/cdn/live/audio_en.m3u8", // separate audio rendition
		"-map 0:v:0? -map 1:a:0?",
	} {
		if !strings.Contains(args, want) {
			t.Errorf("FFmpeg args missing %q:\n%s", want, args)
		}
	}
	if n := strings.Count(args, "-user_agent kptv-test-agent"); n != 2 {
		t.Errorf("source User-Agent applied to %d inputs, want 2:\n%s", n, args)
	}
}

// Non-playlist URLs keep the existing FFmpeg-mode behaviour: handed straight
// to ffmpeg with no request from KPTV, so single-use URLs are fetched once.
func TestFFmpegModePassesOtherURLsStraightToFFmpeg(t *testing.T) {
	runs := fakeFFmpeg(t, ffmpegHealthy)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	t.Cleanup(srv.Close)

	r, _ := newSingleStreamRestreamer(t, srv.URL) // stream URL .../live.ts
	r.Config.FFmpegMode = true
	r.StreamFromSource(0)

	got := runs()
	if len(got) != 1 || !strings.Contains(got[0], "-i "+srv.URL+"/live.ts ") {
		t.Fatalf("FFmpeg runs %q, want one run on the stream URL", got)
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("KPTV requested the stream itself %d times, want 0", n)
	}
}
