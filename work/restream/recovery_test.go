package restream

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"kptv-proxy/work/constants"
	"kptv-proxy/work/deadstreams"
	"kptv-proxy/work/stream"
	"kptv-proxy/work/types"
)

// upstream counts requests and serves either a 404 or a TS body of the given size.
type upstream struct {
	*httptest.Server
	hits atomic.Int32
}

func newUpstream(t *testing.T, tsBytes int) *upstream {
	t.Helper()
	u := &upstream{}
	u.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.hits.Add(1)
		if tsBytes == 0 {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "video/mp2t")
		w.Write(make([]byte, tsBytes))
	}))
	t.Cleanup(u.Close)
	return u
}

// autoBlock drives the stream into the auto-blocked state through the same
// path the streaming loop uses.
func autoBlock(t *testing.T, r *Restream, s *types.Stream) {
	t.Helper()
	for i := 0; i < 5; i++ {
		stream.HandleStreamFailure(s, r.Config, r.Channel.Name, 0)
	}
	if atomic.LoadInt32(&s.Blocked) != 1 || !deadstreams.IsStreamDead(r.Channel.Name, s.URLHash) {
		t.Fatal("setup: stream was not auto-blocked")
	}
}

func setLastFail(s *types.Stream, at time.Time) {
	s.Mu.Lock()
	s.LastFail = at
	s.Mu.Unlock()
}

// An auto-blocked stream is skipped while its failure is recent, and probed
// again once the retry interval has passed.
func TestAutoBlockedStreamIsRetriedAfterInterval(t *testing.T) {
	u := newUpstream(t, 0)
	r, s := newSingleStreamRestreamer(t, u.URL)
	autoBlock(t, r, s)

	if r.streamEligible(0) {
		t.Error("recently blocked stream is eligible")
	}
	r.StreamFromSource(0)
	if n := u.hits.Load(); n != 0 {
		t.Fatalf("recently blocked stream was requested %d times, want 0", n)
	}

	setLastFail(s, time.Now().Add(-time.Hour))
	if !r.streamEligible(0) {
		t.Error("auto-blocked stream past its retry interval is still ineligible")
	}
	r.StreamFromSource(0)
	if n := u.hits.Load(); n != 1 {
		t.Fatalf("auto-blocked stream past its retry interval was requested %d times, want 1", n)
	}
}

// A stream an admin killed by hand is never retried automatically.
func TestManuallyKilledStreamIsNotRetried(t *testing.T) {
	u := newUpstream(t, 0)
	r, s := newSingleStreamRestreamer(t, u.URL)
	if err := deadstreams.MarkStreamDeadByHash(r.Channel.Name, s.URLHash, "manual"); err != nil {
		t.Fatal(err)
	}
	setLastFail(s, time.Now().Add(-24*time.Hour))

	r.StreamFromSource(0)
	if r.streamEligible(0) || u.hits.Load() != 0 {
		t.Fatalf("manually killed stream: eligible=%v requests=%d, want false and 0", r.streamEligible(0), u.hits.Load())
	}
}

// A probe that delivers real data leaves the stream unblocked and no longer
// recorded as dead.
func TestSuccessfulProbeClearsAutoBlock(t *testing.T) {
	u := newUpstream(t, 3<<20)
	r, s := newSingleStreamRestreamer(t, u.URL)
	autoBlock(t, r, s)
	setLastFail(s, time.Now().Add(-time.Hour))

	if outcome, n := r.runAttempt(0); outcome != outcomeSuccess {
		t.Fatalf("attempt outcome = %d (%d bytes), want success", outcome, n)
	}
	if atomic.LoadInt32(&s.Blocked) != 0 || deadstreams.IsStreamDead(r.Channel.Name, s.URLHash) {
		t.Errorf("stream still blocked after a successful probe")
	}
}

// After the fallback period the loop must go back to trying the real source
// rather than playing the loading clip for as long as the viewer stays.
func TestFallbackReturnsToRealSource(t *testing.T) {
	prev := constants.Internal.FallbackRetryInterval
	constants.Internal.FallbackRetryInterval = 300 * time.Millisecond
	t.Cleanup(func() { constants.Internal.FallbackRetryInterval = prev })

	r, _ := newSingleStreamRestreamer(t, "http://upstream.invalid")
	done := make(chan bool, 1)
	go func() { done <- r.runFallback() }()

	select {
	case retry := <-done:
		if !retry {
			t.Fatal("runFallback returned false with a viewer still connected")
		}
	case <-time.After(5 * time.Second):
		r.CancelStream()
		<-done // let the fallback goroutine exit before cleanup restores the interval
		t.Fatal("fallback did not hand back to the source retry loop within 5s")
	}
}

// Failure recording and retry gating touch the same stream from different
// goroutines; run them together under -race.
func TestAutoBlockRetryConcurrent(t *testing.T) {
	u := newUpstream(t, 0)
	r, s := newSingleStreamRestreamer(t, u.URL)
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				if g%2 == 0 {
					stream.HandleStreamFailure(s, r.Config, r.Channel.Name, 0)
				} else {
					setLastFail(s, time.Now().Add(-time.Hour))
					r.streamEligible(0)
					r.StreamFromSource(0)
				}
			}
		}(g)
	}
	wg.Wait()
}
