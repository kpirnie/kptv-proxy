package restream

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kptv-proxy/work/client"
	"kptv-proxy/work/config"
	"kptv-proxy/work/constants"
	"kptv-proxy/work/types"

	"github.com/puzpuzpuz/xsync/v3"
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "kptv-restream-test-")
	if err != nil {
		panic(err)
	}
	constants.Internal.DatabasePath = filepath.Join(dir, "kptv.db")
	fallback := filepath.Join(dir, "loading.ts")
	if err := os.WriteFile(fallback, make([]byte, 188*2000), 0o644); err != nil {
		panic(err)
	}
	constants.Internal.FallbackVideoPath = fallback
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// newSingleStreamRestreamer returns a restreamer for a channel with one stream
// at url+"/live.ts" and one connected viewer whose queue is large enough that
// nothing is dropped during a test.
func newSingleStreamRestreamer(t *testing.T, url string) (*Restream, *types.Stream) {
	t.Helper()
	return newRestreamerForURL(t, url, url+"/live.ts")
}

func newRestreamerForURL(t *testing.T, sourceURL, streamURL string) (*Restream, *types.Stream) {
	t.Helper()
	src := &config.SourceConfig{Name: "test", URL: sourceURL, MaxConnections: 5, MaxFailuresBeforeBlock: 5, UserAgent: "kptv-test-agent"}
	s := &types.Stream{URL: streamURL, URLHash: "hash-" + t.Name(), Source: src}
	ch := &types.Channel{Name: "ch-" + t.Name(), Streams: []*types.Stream{s}}
	cfg := &config.Config{SlowClientBufferChunks: 1024, BufferSizePerStream: 1}
	r := NewRestreamer(ch, 1<<20, client.NewHeaderSettingClient(5*time.Second), cfg, nil)
	ch.Restreamer = r.Restreamer
	r.Clients = xsync.NewMapOf[string, *types.RestreamClient]()
	r.Clients.Store("viewer", &types.RestreamClient{
		Id:        "viewer",
		Done:      make(chan bool),
		WriteChan: make(chan *types.StreamChunk, 1024),
	})
	r.ClientCount.Store(1)
	t.Cleanup(r.CancelStream)
	return r, s
}

// fakeFFmpeg puts an "ffmpeg" on PATH that appends its arguments to a log,
// one line per run, then runs body (a POSIX shell snippet). It returns a
// function reporting the arguments of every run so far.
func fakeFFmpeg(t *testing.T, body string) (runs func() []string) {
	t.Helper()
	dir := t.TempDir()
	argsLog := filepath.Join(dir, "args")
	script := "#!/bin/sh\necho \"$*\" >> \"" + argsLog + "\"\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, "ffmpeg"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() []string {
		b, _ := os.ReadFile(argsLog)
		if s := strings.TrimSpace(string(b)); s != "" {
			return strings.Split(s, "\n")
		}
		return nil
	}
}

// Shell snippets for fakeFFmpeg.
const (
	ffmpegHealthy = "head -c 300000 /dev/zero"                                                          // delivers media, then ends
	ffmpegStall   = "head -c 200000 /dev/zero; exec sleep 3600"                                         // delivers media, then goes silent
	ffmpegSilent  = "exec sleep 3600"                                                                   // never produces output
	ffmpegSteady  = "i=0; while [ $i -lt 20 ]; do head -c 10240 /dev/zero; sleep 0.1; i=$((i+1)); done" // 2s of steady output
)
