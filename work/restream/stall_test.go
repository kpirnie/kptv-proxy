package restream

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"kptv-proxy/work/constants"
)

// withIdleTimeout shortens the FFmpeg idle window (and how often streaming
// re-arms it) so stall handling runs in test time.
func withIdleTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	prevIdle := constants.Internal.StreamReadIdleTimeout
	prevActivity := constants.Internal.FFmpegActivityUpdateInterval
	prevDelay := constants.Internal.EOFRestartDelay
	constants.Internal.StreamReadIdleTimeout = d
	constants.Internal.FFmpegActivityUpdateInterval = d / 5
	constants.Internal.EOFRestartDelay = 10 * time.Millisecond
	t.Cleanup(func() {
		constants.Internal.StreamReadIdleTimeout = prevIdle
		constants.Internal.FFmpegActivityUpdateInterval = prevActivity
		constants.Internal.EOFRestartDelay = prevDelay
	})
}

// masterServer serves a master playlist with a 1080p and a 360p variant.
func masterServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		fmt.Fprint(w, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=6000000,RESOLUTION=1920x1080\nbest.m3u8\n#EXT-X-STREAM-INF:BANDWIDTH=1000000,RESOLUTION=640x360\nlow.m3u8\n")
	}))
	t.Cleanup(srv.Close)
	return srv
}

// runFFmpegAttempt runs one FFmpeg attempt, failing the test if it does not
// return within limit.
func runFFmpegAttempt(t *testing.T, r *Restream, limit time.Duration) (bool, int64) {
	t.Helper()
	type result struct {
		ok    bool
		bytes int64
	}
	done := make(chan result, 1)
	go func() {
		ok, n := r.streamWithFFmpeg("http://upstream.invalid/v.m3u8")
		done <- result{ok, n}
	}()
	select {
	case res := <-done:
		return res.ok, res.bytes
	case <-time.After(limit):
		r.CancelStream()
		<-done
		t.Fatalf("FFmpeg attempt still running after %v", limit)
		return false, 0
	}
}

// Output that starts and then stops while FFmpeg stays alive is a stall.
func TestFFmpegGoingQuietIsAStall(t *testing.T) {
	fakeFFmpeg(t, ffmpegStall)
	withIdleTimeout(t, 500*time.Millisecond)
	r, _ := newSingleStreamRestreamer(t, "http://upstream.invalid")

	ok, n := runFFmpegAttempt(t, r, 5*time.Second)
	if ok || n < 200000 || !r.AttemptStalled.Load() {
		t.Fatalf("got ok=%v bytes=%d stalled=%v, want ok=false bytes>=200000 stalled=true", ok, n, r.AttemptStalled.Load())
	}
}

// No output at all is an ordinary failed attempt, not a stall.
func TestFFmpegWithNoOutputIsNotAStall(t *testing.T) {
	fakeFFmpeg(t, ffmpegSilent)
	withIdleTimeout(t, 500*time.Millisecond)
	r, _ := newSingleStreamRestreamer(t, "http://upstream.invalid")

	ok, n := runFFmpegAttempt(t, r, 5*time.Second)
	if ok || n != 0 || r.AttemptStalled.Load() {
		t.Fatalf("got ok=%v bytes=%d stalled=%v, want a plain failure", ok, n, r.AttemptStalled.Load())
	}
}

// Control: steady output is never treated as a stall.
func TestSteadyFFmpegIsNotAStall(t *testing.T) {
	fakeFFmpeg(t, ffmpegSteady)
	withIdleTimeout(t, 500*time.Millisecond)
	r, _ := newSingleStreamRestreamer(t, "http://upstream.invalid")

	ok, n := runFFmpegAttempt(t, r, 10*time.Second)
	if !ok || r.AttemptStalled.Load() {
		t.Fatalf("got ok=%v bytes=%d stalled=%v, want a healthy run", ok, n, r.AttemptStalled.Load())
	}
}

// A stall restarts the source from a fresh master (new SSAI session, best
// variant); it must not fall through to the next, lower-quality variant of
// the session that just stalled.
func TestStallDoesNotFallBackToLowerVariant(t *testing.T) {
	runs := fakeFFmpeg(t, ffmpegStall)
	withIdleTimeout(t, 300*time.Millisecond)
	srv := masterServer(t)

	r, _ := newRestreamerForURL(t, srv.URL, srv.URL+"/master.m3u8")
	r.Config.FFmpegMode = true
	r.StreamFromSource(0)

	got := runs()
	if len(got) != 1 || !strings.Contains(got[0], "best.m3u8") || !r.AttemptStalled.Load() {
		t.Fatalf("after a stall FFmpeg ran %d time(s) %q, stalled=%v; want one run on best.m3u8 reported as stalled",
			len(got), got, r.AttemptStalled.Load())
	}
}

// A single-source channel whose source stalls every time is restarted
// StreamMaxStallRestarts times, then handled as failing: one more attempt
// uses up the budget and the loop rests in the fallback period. No tight loop.
func TestStallRestartsAreBounded(t *testing.T) {
	runs := fakeFFmpeg(t, ffmpegStall)
	withIdleTimeout(t, 300*time.Millisecond)
	prevFallback := constants.Internal.FallbackRetryInterval
	constants.Internal.FallbackRetryInterval = time.Minute
	t.Cleanup(func() { constants.Internal.FallbackRetryInterval = prevFallback })
	srv := masterServer(t)

	r, _ := newRestreamerForURL(t, srv.URL, srv.URL+"/master.m3u8")
	r.Config.FFmpegMode = true
	done := make(chan struct{})
	go func() { r.Stream(); close(done) }()

	// first run, restarts, then one more failed attempt fills the budget of 2
	want := 1 + constants.Internal.StreamMaxStallRestarts + 1
	time.Sleep(8 * time.Second)
	got := len(runs())
	r.CancelStream()
	<-done

	if got != want {
		t.Fatalf("FFmpeg started %d times in 8s, want %d (restarts then fallback)", got, want)
	}
}

// A restarted FFmpeg must continue the previous process's output timeline.
// Each new process otherwise starts its timestamps near zero, and players
// (Plex) stall on a clock that jumps backwards mid-stream.
func TestRestartContinuesTimeline(t *testing.T) {
	runs := fakeFFmpeg(t, `echo "out_time_us=12000000" >&2; echo "progress=continue" >&2; `+ffmpegStall)
	withIdleTimeout(t, 300*time.Millisecond)
	r, _ := newSingleStreamRestreamer(t, "http://upstream.invalid")

	for i := 0; i < 2; i++ {
		runFFmpegAttempt(t, r, 5*time.Second)
	}

	got := runs()
	if len(got) != 2 {
		t.Fatalf("FFmpeg started %d times, want 2", len(got))
	}
	offset := func(args string) string {
		f := strings.Fields(args)
		for i := range f[:len(f)-1] {
			if f[i] == "-output_ts_offset" {
				return f[i+1]
			}
		}
		return "none"
	}
	// first run starts the session timeline at 0; the restart picks up after
	// the 12s the first run reported, plus the safety gap
	want := fmt.Sprintf("%.3f", (12*time.Second + constants.Internal.FFmpegTimelineGap).Seconds())
	if o := offset(got[0]); o != "0.000" {
		t.Errorf("first run -output_ts_offset = %s, want 0.000", o)
	}
	if o := offset(got[1]); o != want {
		t.Errorf("restart -output_ts_offset = %s, want %s", o, want)
	}
}
