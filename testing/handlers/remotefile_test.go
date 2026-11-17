package handlers_test

import (
	"context"
	"fmt"
	"kptv-proxy/work/client"
	"kptv-proxy/work/config"
	"kptv-proxy/work/constants"
	"kptv-proxy/work/handlers"
	"kptv-proxy/work/proxy"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/ratelimit"
)

// sourceWith returns a source with the given connection limit and that many
// slots already taken by somebody else's transfer.
func sourceWith(max int, taken int32) *config.SourceConfig {
	source := &config.SourceConfig{MaxConnections: max}
	source.ActiveConns.Add(taken)
	return source
}

// streamTimings shortens the poll interval, the takeover grace, and the stall
// threshold so a test does not sit through the production values between slot
// rechecks. Returns the function that restores the previous values.
func streamTimings(t *testing.T, poll, takeover, stall time.Duration) func() {
	t.Helper()

	prevPoll := constants.Internal.RemoteFileCapacityPoll
	prevTakeover := constants.Internal.RemoteFileTakeoverWait
	prevStall := constants.Internal.RemoteFileTransferStall
	constants.Internal.RemoteFileCapacityPoll = poll
	constants.Internal.RemoteFileTakeoverWait = takeover
	constants.Internal.RemoteFileTransferStall = stall

	return func() {
		constants.Internal.RemoteFileCapacityPoll = prevPoll
		constants.Internal.RemoteFileTakeoverWait = prevTakeover
		constants.Internal.RemoteFileTransferStall = prevStall
	}
}

// A source with a free slot hands it over at once, and releasing gives it back.
func TestFreeSlotIsTakenAtOnce(t *testing.T) {
	restore := streamTimings(t, 10*time.Millisecond, 10*time.Millisecond, 10*time.Millisecond)
	defer restore()

	source := sourceWith(1, 0)

	release, ok := handlers.ReserveSourceSlot(source, context.Background(), 5*time.Second)
	if !ok {
		t.Fatal("setup: slot was refused while one was free")
	}
	if source.ActiveConns.Load() != 1 {
		t.Fatalf("ActiveConns = %d after taking the slot, want 1", source.ActiveConns.Load())
	}

	release()
	if source.ActiveConns.Load() != 0 {
		t.Fatalf("ActiveConns = %d after releasing, want 0", source.ActiveConns.Load())
	}
}

// A request that arrives while the slot is held queues and gets the slot as
// soon as the current transfer ends — the probe-then-play order a Jellyfin
// playback produces on a one-connection provider.
func TestSlotIsQueuedUntilTheHolderReleases(t *testing.T) {
	restore := streamTimings(t, 10*time.Millisecond, 10*time.Millisecond, 10*time.Millisecond)
	defer restore()

	source := sourceWith(1, 1)
	go func() {
		time.Sleep(150 * time.Millisecond)
		source.ActiveConns.Add(-1)
	}()

	start := time.Now()
	release, ok := handlers.ReserveSourceSlot(source, context.Background(), 5*time.Second)
	if !ok {
		t.Fatal("queue gave up while the slot was going to free up")
	}
	if elapsed := time.Since(start); elapsed < 150*time.Millisecond {
		t.Fatalf("queue returned after %v, want the wait for the holder", elapsed)
	}

	release()
	if source.ActiveConns.Load() != 0 {
		t.Fatalf("ActiveConns = %d after releasing, want 0", source.ActiveConns.Load())
	}
}

// A player that leaves while queued stops the queue, and the queue must leave
// the slot count as it found it.
func TestQueueStopsWhenThePlayerLeaves(t *testing.T) {
	restore := streamTimings(t, 10*time.Millisecond, 10*time.Millisecond, 10*time.Millisecond)
	defer restore()

	source := sourceWith(1, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	release, ok := handlers.ReserveSourceSlot(source, ctx, 5*time.Second)
	if ok {
		t.Fatal("queue reported a slot for a request that is no longer wanted")
	}
	if release != nil {
		t.Fatal("queue returned a release for a slot it did not hand over")
	}
	if source.ActiveConns.Load() != 1 {
		t.Fatalf("ActiveConns = %d after the queue gave up, want the holder's 1", source.ActiveConns.Load())
	}
}

// The queue is bounded: a slot that never frees ends with no slot, and the
// caller is left to answer 503, not to take a connection the provider limits.
func TestQueueGivesUpWhenTheWaitExpires(t *testing.T) {
	restore := streamTimings(t, 10*time.Millisecond, 10*time.Millisecond, 10*time.Millisecond)
	defer restore()

	source := sourceWith(1, 1)

	release, ok := handlers.ReserveSourceSlot(source, context.Background(), 200*time.Millisecond)
	if ok {
		t.Fatal("queue reported a slot on a full source")
	}
	if release != nil {
		t.Fatal("queue returned a release for a slot it did not hand over")
	}
	if source.ActiveConns.Load() != 1 {
		t.Fatalf("ActiveConns = %d after the queue gave up, want the holder's 1", source.ActiveConns.Load())
	}
}

// A wait of zero or less disables queueing: the request is refused at once so
// the caller can answer 503 immediately, which is the user-facing off switch
// for the streamQueueWait setting.
func TestZeroWaitRefusesImmediately(t *testing.T) {
	restore := streamTimings(t, 10*time.Millisecond, 10*time.Millisecond, 10*time.Millisecond)
	defer restore()

	source := sourceWith(1, 1)

	start := time.Now()
	release, ok := handlers.ReserveSourceSlot(source, context.Background(), 0)
	if ok {
		t.Fatal("zero wait must not report a slot on a full source")
	}
	if release != nil {
		t.Fatal("zero wait must not return a release")
	}
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("zero wait took %v, want an immediate refusal", elapsed)
	}
	if source.ActiveConns.Load() != 1 {
		t.Fatalf("ActiveConns = %d after the refusal, want the holder's 1", source.ActiveConns.Load())
	}
}

// slowUpstream serves a stream that drips chunks slowly enough that a
// passthrough of it is still running when the test needs it to be. The
// counter reports how many connections arrived.
func slowUpstream(chunks int, delay time.Duration) (*httptest.Server, *atomic.Int64) {
	connections := &atomic.Int64{}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connections.Add(1)
		w.Header().Set("Content-Length", strconv.Itoa(chunks*chunkSize))
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		flusher.Flush()
		chunk := make([]byte, chunkSize)
		for i := 0; i < chunks; i++ {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(delay):
			}
			if _, err := w.Write(chunk); err != nil {
				return
			}
			flusher.Flush()
		}
	})), connections
}

// tailUpstream serves a file in the shape of an MKV: the range from the start
// drips slowly (playback data), a request inside the last `tail` bytes
// answers at once with the small remainder (the cues). Content-Range always
// reports the full size, which is how a running transfer learns it.
func tailUpstream(total, tail int64) (*httptest.Server, *atomic.Int64) {
	connections := &atomic.Int64{}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connections.Add(1)
		start := int64(0)
		if spec := r.Header.Get("Range"); strings.HasPrefix(spec, "bytes=") {
			// "bytes=198000-" or "bytes=198000-199999"; the suffix form
			// ("bytes=-500") has no start offset
			spec = strings.TrimSuffix(strings.TrimPrefix(spec, "bytes="), "-")
			if n, err := strconv.ParseInt(spec, 10, 64); err == nil && n >= 0 {
				start = n
			}
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, total-1, total))
		w.Header().Set("Content-Length", strconv.FormatInt(total-start, 10))
		w.WriteHeader(http.StatusPartialContent)

		if start >= total-tail {
			w.Write(make([]byte, total-start))
			return
		}

		flusher := w.(http.Flusher)
		flusher.Flush()
		chunk := make([]byte, chunkSize)
		for i := start; i < total; i += chunkSize {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(20 * time.Millisecond):
			}
			if _, err := w.Write(chunk); err != nil {
				return
			}
			flusher.Flush()
		}
	})), connections
}

const chunkSize = 1024

// newPassthroughProxy builds the minimal StreamProxy AttemptRemoteFile needs.
// The rate limiter is seeded under the source's own URL — the key the proxy
// looks up — with a rate far above the test's needs, since the proxy would
// otherwise pace one request per second: the rate equals the source's
// MaxConnections.
func newPassthroughProxy(headerTimeout time.Duration, sourceURL string) *proxy.StreamProxy {
	return &proxy.StreamProxy{
		Config:             &config.Config{},
		HttpClient:         client.NewHeaderSettingClient(headerTimeout),
		SourceRateLimiters: map[string]ratelimit.Limiter{sourceURL: ratelimit.New(10000)},
	}
}

// waitForSlot polls until the source reports the given connection count, or
// fails the test after a deadline.
func waitForSlot(t *testing.T, source *config.SourceConfig, want int32) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if source.ActiveConns.Load() == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("source never reached %d connections (now %d)", want, source.ActiveConns.Load())
}

// blockingWriter is a client that opened the stream and then stopped reading:
// the first Write blocks until the test releases it, which is exactly how the
// abandoned connection of a player's double-open looks to the proxy.
type blockingWriter struct {
	unblock chan struct{}
	header  http.Header
}

func newBlockingWriter() *blockingWriter {
	return &blockingWriter{unblock: make(chan struct{}), header: make(http.Header)}
}

func (w *blockingWriter) Header() http.Header { return w.header }

func (w *blockingWriter) WriteHeader(int) {}

func (w *blockingWriter) Write(p []byte) (int, error) {
	<-w.unblock
	return len(p), nil
}

// A newer request for the same channel on a full source displaces the STALLED
// older transfer instead of queueing behind it. This is the VLC shape: the
// player opens the URL twice at once, leaves the first connection idle, and
// expects the second one to start playing without waiting for the first to
// end. The holder here blocks on its first write — a client that stopped
// reading — so the displacement may take its slot.
func TestNewerRequestDisplacesTheOlderTransfer(t *testing.T) {
	restore := streamTimings(t, 10*time.Millisecond, 300*time.Millisecond, 40*time.Millisecond)
	defer restore()

	upstream, connections := slowUpstream(150, 20*time.Millisecond) // ~3s of streaming
	defer upstream.Close()

	source := &config.SourceConfig{Name: "testsrc", URL: upstream.URL, MaxConnections: 1}
	sp := newPassthroughProxy(30*time.Second, upstream.URL)

	movie := "The Farm (2019)"
	sess1 := proxy.StartFileSession("t1", movie, source.Name, "")
	defer proxy.EndFileSession("t1")
	sess2 := proxy.StartFileSession("t2", movie, source.Name, "")
	defer proxy.EndFileSession("t2")

	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()

	// the holder's client reads nothing, so its copy wedges on the first chunk
	holder := newBlockingWriter()
	req1 := httptest.NewRequest("GET", upstream.URL+"/f.mp4", nil).WithContext(ctx1)
	done1 := make(chan handlers.RemoteOutcome, 1)
	go func() {
		done1 <- handlers.AttemptRemoteFile(sp, holder, req1, upstream.URL+"/f.mp4", source, "mp4", sess1, 0)
	}()

	waitForSlot(t, source, 1)
	// let the transfer register and go past the stall threshold: the client
	// accepted nothing, so the transfer is stalled well before this returns
	time.Sleep(200 * time.Millisecond)

	// the second request runs like a real handler would: streaming until its
	// client walks away, so the test watches the upstream instead of the clock
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	req2 := httptest.NewRequest("GET", upstream.URL+"/f.mp4", nil).WithContext(ctx2)
	rec2 := httptest.NewRecorder()
	done2 := make(chan handlers.RemoteOutcome, 1)
	go func() {
		done2 <- handlers.AttemptRemoteFile(sp, rec2, req2, upstream.URL+"/f.mp4", source, "mp4", sess2, 10*time.Second)
	}()
	start := time.Now()

	// displacement speed: the second connection must reach the upstream within
	// about the takeover grace period, not after the 10s queue wait it would
	// suffer if the stalled holder were not displaced
	deadline := start.Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if connections.Load() >= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if n := connections.Load(); n < 2 {
		t.Fatalf("upstream saw %d connections %v after request 2 started, want 2 by now (displacement never fired)", n, time.Since(start))
	}

	// the wedged holder was freed by the displacement itself, even though its
	// copy is still blocked on the silent client: release it now, as the
	// player closing the abandoned connection would
	holder.unblock <- struct{}{}
	select {
	case <-done1:
	case <-time.After(5 * time.Second):
		t.Fatal("displaced transfer did not return")
	}

	// the second transfer streams like a real playback would; give it a
	// moment to carry a few chunks (the upstream drips one every 20ms) before
	// the test ends it
	time.Sleep(300 * time.Millisecond)

	// end the second request like the player would, and wait for its handler
	// before touching its recorder
	cancel2()
	select {
	case <-done2:
	case <-time.After(5 * time.Second):
		t.Fatal("second transfer did not return after its client left")
	}

	if rec2.Body.Len() == 0 {
		t.Fatal("request 2 received no data")
	}
}

// The player's tail metadata fetch — a ranged request near the end of the
// file, issued while the main transfer holds the slot — is served on a direct
// connection: the holder keeps its slot and its connection, so the player's
// parse of the container is never interrupted.
func TestTailFetchIsServedWithoutDisplacingTheHolder(t *testing.T) {
	restore := streamTimings(t, 10*time.Millisecond, 300*time.Millisecond, 40*time.Millisecond)
	defer restore()

	total := int64(200000)
	tail := int64(2000)
	upstream, connections := tailUpstream(total, tail)
	defer upstream.Close()

	source := &config.SourceConfig{Name: "testsrc", URL: upstream.URL, MaxConnections: 1}
	sp := newPassthroughProxy(30*time.Second, upstream.URL)

	movie := "The Farm (2019)"
	sess1 := proxy.StartFileSession("t7", movie, source.Name, "")
	defer proxy.EndFileSession("t7")
	sess2 := proxy.StartFileSession("t8", movie, source.Name, "")
	defer proxy.EndFileSession("t8")

	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()

	// the main transfer, reading eagerly like a player's open connection
	req1 := httptest.NewRequest("GET", upstream.URL+"/f.mkv", nil).WithContext(ctx1)
	rec1 := httptest.NewRecorder()
	done1 := make(chan handlers.RemoteOutcome, 1)
	go func() {
		done1 <- handlers.AttemptRemoteFile(sp, rec1, req1, upstream.URL+"/f.mkv", source, "mkv", sess1, 0)
	}()

	waitForSlot(t, source, 1)
	time.Sleep(200 * time.Millisecond) // registered, with the file size known

	// the cues fetch, arriving while the main transfer holds the slot
	req2 := httptest.NewRequest("GET", upstream.URL+"/f.mkv", nil)
	req2.Header.Set("Range", fmt.Sprintf("bytes=%d-", total-tail))
	rec2 := httptest.NewRecorder()
	start := time.Now()
	outcome := handlers.AttemptRemoteFile(sp, rec2, req2, upstream.URL+"/f.mkv", source, "mkv", sess2, 5*time.Second)

	if outcome != handlers.RemoteDelivered {
		t.Fatalf("tail fetch outcome = %d, want delivered", outcome)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("tail fetch took %v, want a direct serve", elapsed)
	}
	if rec2.Body.Len() != int(tail) {
		t.Fatalf("tail fetch delivered %d bytes, want %d", rec2.Body.Len(), tail)
	}
	if source.ActiveConns.Load() != 1 {
		t.Fatalf("ActiveConns = %d, want the holder's 1 (the tail fetch must not take a slot)", source.ActiveConns.Load())
	}
	select {
	case <-done1:
		t.Fatal("the holder was disturbed by the tail fetch")
	default:
	}
	if n := connections.Load(); n != 2 {
		t.Fatalf("upstream saw %d connections, want 2 (main transfer + tail fetch)", n)
	}

	// end the holder like the player would
	cancel1()
	select {
	case <-done1:
	case <-time.After(5 * time.Second):
		t.Fatal("holder did not return")
	}
}

// A transfer still streaming to its client is never displaced, even by another
// request for the same content: it may be the player's own metadata probe —
// which ends by itself and hands over the slot — or another viewer's playback.
// A newcomer queues and is refused with RemoteCapacity when the wait expires.
func TestActiveTransferIsNeverDisplaced(t *testing.T) {
	restore := streamTimings(t, 10*time.Millisecond, 300*time.Millisecond, 40*time.Millisecond)
	defer restore()

	upstream, connections := slowUpstream(150, 20*time.Millisecond)
	defer upstream.Close()

	source := &config.SourceConfig{Name: "testsrc", URL: upstream.URL, MaxConnections: 1}
	sp := newPassthroughProxy(30*time.Second, upstream.URL)

	sess1 := proxy.StartFileSession("t5", "The Farm (2019)", source.Name, "")
	defer proxy.EndFileSession("t5")
	sess2 := proxy.StartFileSession("t6", "The Farm (2019)", source.Name, "")
	defer proxy.EndFileSession("t6")

	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()

	// the holder's client reads eagerly (a recorder accepts every write), so
	// the transfer keeps making progress and never looks stalled
	req1 := httptest.NewRequest("GET", upstream.URL+"/f.mp4", nil).WithContext(ctx1)
	rec1 := httptest.NewRecorder()
	done1 := make(chan handlers.RemoteOutcome, 1)
	go func() {
		done1 <- handlers.AttemptRemoteFile(sp, rec1, req1, upstream.URL+"/f.mp4", source, "mp4", sess1, 0)
	}()

	waitForSlot(t, source, 1)
	time.Sleep(200 * time.Millisecond)

	// the newcomer waits out its queue with no displacement to break it loose
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	req2 := httptest.NewRequest("GET", upstream.URL+"/f.mp4", nil).WithContext(ctx2)
	rec2 := httptest.NewRecorder()
	done2 := make(chan handlers.RemoteOutcome, 1)
	go func() {
		done2 <- handlers.AttemptRemoteFile(sp, rec2, req2, upstream.URL+"/f.mp4", source, "mp4", sess2, 300*time.Millisecond)
	}()

	// halfway through the queue wait the holder must still be untouched
	time.Sleep(150 * time.Millisecond)
	if n := connections.Load(); n != 1 {
		t.Fatalf("upstream saw %d connections mid-queue, want the holder's 1 (an active transfer was displaced)", n)
	}

	select {
	case outcome := <-done2:
		if outcome != handlers.RemoteCapacity {
			t.Fatalf("outcome = %d, want capacity after the queue expired", outcome)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("second request did not return")
	}

	if n := connections.Load(); n != 1 {
		t.Fatalf("upstream saw %d connections after the refusal, want 1", n)
	}
	if source.ActiveConns.Load() != 1 {
		t.Fatalf("ActiveConns = %d, want the untouched holder's 1", source.ActiveConns.Load())
	}

	// end the holder like the player would
	cancel1()
	select {
	case <-done1:
	case <-time.After(5 * time.Second):
		t.Fatal("holder transfer did not return")
	}
}

// A request for a different channel finds nothing to displace: the full
// source is refused at once (the caller answers 503) and the running
// transfer of the other channel is left alone.
func TestFullSourceWithoutAMatchingTransferIsRefused(t *testing.T) {
	upstream, _ := slowUpstream(150, 20*time.Millisecond)
	defer upstream.Close()

	source := &config.SourceConfig{Name: "testsrc", URL: upstream.URL, MaxConnections: 1}
	sp := newPassthroughProxy(30*time.Second, upstream.URL)

	sess1 := proxy.StartFileSession("t3", "Channel One", source.Name, "")
	defer proxy.EndFileSession("t3")
	sess2 := proxy.StartFileSession("t4", "Channel Two", source.Name, "")
	defer proxy.EndFileSession("t4")

	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()

	req1 := httptest.NewRequest("GET", upstream.URL+"/f.mp4", nil).WithContext(ctx1)
	rec1 := httptest.NewRecorder()
	done1 := make(chan handlers.RemoteOutcome, 1)
	go func() {
		done1 <- handlers.AttemptRemoteFile(sp, rec1, req1, upstream.URL+"/f.mp4", source, "mp4", sess1, 0)
	}()

	waitForSlot(t, source, 1)
	time.Sleep(200 * time.Millisecond)

	start := time.Now()
	req2 := httptest.NewRequest("GET", upstream.URL+"/f.mp4", nil)
	rec2 := httptest.NewRecorder()
	outcome := handlers.AttemptRemoteFile(sp, rec2, req2, upstream.URL+"/f.mp4", source, "mp4", sess2, 0)

	if outcome != handlers.RemoteCapacity {
		t.Fatalf("outcome = %d, want capacity", outcome)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("refusal took %v, want an immediate answer", elapsed)
	}
	if source.ActiveConns.Load() != 1 {
		t.Fatalf("ActiveConns = %d, want the untouched holder's 1", source.ActiveConns.Load())
	}

	cancel1()
	select {
	case <-done1:
	case <-time.After(5 * time.Second):
		t.Fatal("holder transfer did not return")
	}
}
