package proxy

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"kptv-proxy/work/cache"
	"kptv-proxy/work/client"
	"kptv-proxy/work/config"
	"kptv-proxy/work/constants"
	"kptv-proxy/work/types"
	"kptv-proxy/work/utils"
)

// TestMain points every on-disk path at a scratch directory so imports can run
// without a /settings volume.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "kptv-proxy-test-")
	if err != nil {
		panic(err)
	}
	constants.Internal.DatabasePath = filepath.Join(dir, "kptv.db")
	constants.Internal.CatalogCachePath = filepath.Join(dir, "catalog")
	constants.Internal.EPGCachePath = filepath.Join(dir, "epg")
	constants.Internal.LogoPath = filepath.Join(dir, "logos")
	constants.Internal.LogoCachePath = filepath.Join(dir, "logos", "cache")
	utils.InitContentRegexes() // main() does this before the first import
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

const testPlaylist = `#EXTM3U
#EXTINF:-1 tvg-id="a" tvg-name="Alpha" group-title="Test",Alpha
http://upstream.invalid/alpha.m3u8
#EXTINF:-1 tvg-id="b" tvg-name="Bravo" group-title="Test",Bravo
http://upstream.invalid/bravo.m3u8
`

// newImportProxy builds a StreamProxy with one M3U source served by a local
// test server. cacheDuration controls whether a repeat import is served from
// the on-disk catalog cache (long) or refetched (short).
func newImportProxy(t *testing.T, cacheDuration time.Duration) (*StreamProxy, *config.SourceConfig) {
	sp, src, _ := newImportProxyCounting(t, cacheDuration)
	return sp, src
}

func newImportProxyCounting(t *testing.T, cacheDuration time.Duration) (*StreamProxy, *config.SourceConfig, *atomic.Int32) {
	t.Helper()
	os.RemoveAll(constants.Internal.CatalogCachePath)

	fetches := &atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetches.Add(1)
		fmt.Fprint(w, testPlaylist)
	}))
	t.Cleanup(srv.Close)

	cfg := &config.Config{
		CacheDuration:       cacheDuration,
		WorkerThreads:       2,
		MaxConnectionsToApp: 10,
		SortField:           "tvg-name",
		SortDirection:       "asc",
		Sources:             make([]config.SourceConfig, 1),
	}
	src := &cfg.Sources[0]
	src.Name = "test"
	src.URL = srv.URL + "/playlist.m3u"
	src.MaxConnections = 2
	src.MaxFailuresBeforeBlock = 5

	c, err := cache.NewCache(cacheDuration)
	if err != nil {
		t.Fatalf("cache: %v", err)
	}
	sp := New(cfg, nil, client.NewHeaderSettingClient(5*time.Second), nil, c)
	return sp, src, fetches
}

func loadChannel(t *testing.T, sp *StreamProxy, name string) *types.Channel {
	t.Helper()
	ch, ok := sp.Channels.Load(name)
	if !ok {
		var names []string
		sp.Channels.Range(func(n string, _ *types.Channel) bool { names = append(names, n); return true })
		t.Fatalf("channel %q missing after import; have %q", name, names)
	}
	return ch
}

// A stream that is playing holds a slot in its source's ActiveConns. A
// scheduled import must not wipe that slot, or the source limit stops holding.
func TestImportKeepsLiveConnectionCount(t *testing.T) {
	sp, src := newImportProxy(t, time.Millisecond)
	sp.ImportStreams()

	src.ActiveConns.Add(1) // a live stream, as StreamFromSource does
	time.Sleep(5 * time.Millisecond)
	sp.ImportStreams()

	if got := src.ActiveConns.Load(); got != 1 {
		t.Fatalf("ActiveConns after import = %d, want 1 (the live stream's slot)", got)
	}
	src.ActiveConns.Add(-1) // the stream ends
	if got := src.ActiveConns.Load(); got != 0 {
		t.Fatalf("ActiveConns after stream ended = %d, want 0", got)
	}
}

// Streams imported from the catalog cache must count against the configured
// source, not against a private copy decoded from JSON.
func TestCachedImportSharesSourceConfig(t *testing.T) {
	sp, src, fetches := newImportProxyCounting(t, time.Hour)
	sp.ImportStreams() // fetch, writes catalog
	sp.ImportStreams() // served from catalog cache

	if n := fetches.Load(); n != 1 {
		t.Fatalf("source fetched %d times, want 1 (second import from catalog cache); test setup is wrong", n)
	}
	for _, name := range []string{"Alpha", "Bravo"} {
		ch := loadChannel(t, sp, name)
		ch.Mu.RLock()
		got := ch.Streams[0].Source
		ch.Mu.RUnlock()
		if got != src {
			t.Errorf("%s: stream.Source = %p, want the configured source %p", name, got, src)
		}
	}
}

// A channel that is playing when an import commits must keep its restreamer,
// so the next viewer joins the existing session instead of opening a second
// upstream connection, and cleanup and the watcher can still reach it.
func TestImportKeepsRunningRestreamer(t *testing.T) {
	sp, _ := newImportProxy(t, time.Millisecond)
	sp.ImportStreams()

	live := &types.Restreamer{}
	live.Running.Store(true)
	ch := loadChannel(t, sp, "Alpha")
	ch.Mu.Lock()
	ch.Restreamer = live
	ch.Mu.Unlock()

	time.Sleep(5 * time.Millisecond)
	sp.ImportStreams()

	ch = loadChannel(t, sp, "Alpha")
	ch.Mu.RLock()
	got := ch.Restreamer
	ch.Mu.RUnlock()
	if got != live {
		t.Fatalf("Alpha.Restreamer after import = %p, want the running restreamer %p", got, live)
	}
	if b := loadChannel(t, sp, "Bravo"); b.Restreamer != nil {
		t.Fatalf("Bravo gained a restreamer it never had")
	}
}

// Imports race with viewers tuning in; run them together under -race.
func TestImportConcurrentWithTuning(t *testing.T) {
	sp, _ := newImportProxy(t, time.Millisecond)
	sp.ImportStreams()

	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			ch, ok := sp.Channels.Load("Alpha")
			if !ok {
				continue
			}
			ch.Mu.Lock()
			if ch.Restreamer == nil {
				ch.Restreamer = &types.Restreamer{}
			}
			_ = len(ch.Streams)
			ch.Mu.Unlock()
		}
	}()
	for i := 0; i < 20; i++ {
		time.Sleep(2 * time.Millisecond)
		sp.ImportStreams()
	}
	close(stop)
	wg.Wait()
}
