package watcher

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"kptv-proxy/work/buffer"
	"kptv-proxy/work/config"
	"kptv-proxy/work/constants"
	"kptv-proxy/work/types"

	"github.com/puzpuzpuz/xsync/v3"
)

// TestMain gives the dead-stream lookups a scratch database.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "kptv-watcher-test-")
	if err != nil {
		panic(err)
	}
	constants.Internal.DatabasePath = filepath.Join(dir, "kptv.db")
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// newHealthyWatcher returns a watcher over a running two-stream channel with
// one connected viewer, past its grace period.
func newHealthyWatcher(t *testing.T) (*StreamWatcher, *types.Restreamer) {
	t.Helper()
	src := &config.SourceConfig{Name: "test", URL: "http://upstream.invalid/", MaxConnections: 5}
	ch := &types.Channel{
		Name: "Dual",
		Streams: []*types.Stream{
			{URL: "http://upstream.invalid/1.m3u8", URLHash: "h1", Source: src},
			{URL: "http://upstream.invalid/2.m3u8", URLHash: "h2", Source: src},
		},
	}
	r := &types.Restreamer{
		Channel: ch,
		Clients: xsync.NewMapOf[string, *types.RestreamClient](),
		Config:  &config.Config{},
		Stats:   &types.StreamStats{},
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	r.SetContext(ctx, cancel)
	r.StoreBuffer(buffer.NewRingBuffer(1 << 20))
	r.Running.Store(true)
	r.SwitchTo.Store(-1)
	r.LastActivity.Store(time.Now().Unix())
	r.Clients.Store("viewer", &types.RestreamClient{Id: "viewer", Done: make(chan bool)})
	r.ClientCount.Store(1)
	ch.Restreamer = r

	wctx, wcancel := context.WithCancel(context.Background())
	t.Cleanup(wcancel)
	sw := &StreamWatcher{
		channelName:      ch.Name,
		restreamer:       r,
		ctx:              wctx,
		cancel:           wcancel,
		lastStreamStart:  time.Now().Add(-10 * time.Minute),
		lastFailureReset: time.Now(),
	}
	return sw, r
}

// The stream is delivering data and reporting activity; only the background
// ffprobe snapshot is old (a skipped or failed probe cycle). That is not a
// stream fault and must not make the watcher switch streams.
func TestStaleProbeStatsDoNotTriggerSwitch(t *testing.T) {
	sw, r := newHealthyWatcher(t)

	r.Stats.Mu.Lock()
	r.Stats.Valid = true
	r.Stats.LastUpdated = time.Now().Add(-6 * time.Minute).Unix()
	r.Stats.Mu.Unlock()

	chunk := make([]byte, 256*1024)
	for i := 0; i < 5; i++ {
		r.LoadBuffer().Write(chunk) // healthy throughput between checks
		r.LastActivity.Store(time.Now().Unix())
		sw.checkStreamHealth()
	}

	if got := r.SwitchTo.Load(); got != -1 {
		t.Fatalf("watcher requested a switch to stream %d on a healthy stream", got)
	}
}

// Control: a stream that really stops delivering still fails over.
func TestStalledStreamStillTriggersSwitch(t *testing.T) {
	sw, r := newHealthyWatcher(t)

	r.LoadBuffer().Write(make([]byte, 256*1024))
	for i := 0; i < 5; i++ {
		r.LastActivity.Store(time.Now().Unix())
		sw.checkStreamHealth() // no new bytes since the previous check
	}

	if got := r.SwitchTo.Load(); got != 1 {
		t.Fatalf("SwitchTo = %d, want 1 after a sustained stall", got)
	}
}
