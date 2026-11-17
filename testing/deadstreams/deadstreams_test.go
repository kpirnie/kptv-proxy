package deadstreams_test

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"kptv-proxy/work/constants"
	"kptv-proxy/work/db"
	"kptv-proxy/work/deadstreams"
	"kptv-proxy/work/types"
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "kptv-deadstreams-test-")
	if err != nil {
		panic(err)
	}
	constants.Internal.DatabasePath = filepath.Join(dir, "kptv.db")
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// newStream returns a stream whose failure record carries the given age. The
// mutex must be held while LastFail is written, exactly as the failure paths do.
func newStream(channel, hash string, failureAge time.Duration) *types.Stream {
	s := &types.Stream{URLHash: hash}
	s.Mu.Lock()
	s.LastFail = time.Now().Add(-failureAge)
	s.Mu.Unlock()
	return s
}

// A manually killed stream is an admin decision and never recovers on its own,
// no matter how long ago the (absent) failure was.
func TestManualKillNeverRecovers(t *testing.T) {
	channel, hash := "ch-manual", "hash-manual"
	if err := deadstreams.MarkStreamDeadByHash(channel, hash, "manual"); err != nil {
		t.Fatalf("setup: mark dead failed: %v", err)
	}

	s := newStream(channel, hash, 10*time.Hour)
	if !deadstreams.BlockedUntilRetry(channel, s) {
		t.Fatal("manual kill must stay skipped even long after any failure")
	}
}

// An auto-dead stream becomes eligible once the retry interval has passed.
func TestAutoDeadRecoversAfterInterval(t *testing.T) {
	channel, hash := "ch-auto", "hash-auto"
	if err := deadstreams.MarkStreamDeadByHash(channel, hash, "vod unplayable"); err != nil {
		t.Fatalf("setup: mark dead failed: %v", err)
	}

	s := newStream(channel, hash, time.Minute)
	if !deadstreams.BlockedUntilRetry(channel, s) {
		t.Fatal("auto-dead stream within the retry interval must stay skipped")
	}

	s = newStream(channel, hash, constants.Internal.AutoBlockRetryInterval+time.Minute)
	if deadstreams.BlockedUntilRetry(channel, s) {
		t.Fatal("auto-dead stream past the retry interval must be eligible again")
	}
}

// A blocked stream without a dead mark follows the same interval rule.
func TestBlockedFlagFollowsTheInterval(t *testing.T) {
	channel, hash := "ch-blocked", "hash-blocked"
	s := &types.Stream{URLHash: hash, Blocked: 1}
	s.Mu.Lock()
	s.LastFail = time.Now().Add(-time.Minute)
	s.Mu.Unlock()

	if !deadstreams.BlockedUntilRetry(channel, s) {
		t.Fatal("recently blocked stream must stay skipped")
	}

	s.Mu.Lock()
	s.LastFail = time.Now().Add(-constants.Internal.AutoBlockRetryInterval - time.Minute)
	s.Mu.Unlock()
	if deadstreams.BlockedUntilRetry(channel, s) {
		t.Fatal("blocked stream past the retry interval must be eligible again")
	}
}

// A dead mark with no failure timestamp on record — the state right after a
// restart, when the block survives in the database but the memory does not —
// is retried straight away, and a failed probe re-blocks through the usual path.
func TestDeadWithoutAFailureRecordIsEligibleAtOnce(t *testing.T) {
	channel, hash := "ch-nofail", "hash-nofail"
	if err := deadstreams.MarkStreamDeadByHash(channel, hash, "vod unplayable"); err != nil {
		t.Fatalf("setup: mark dead failed: %v", err)
	}

	s := &types.Stream{URLHash: hash}
	if deadstreams.BlockedUntilRetry(channel, s) {
		t.Fatal("dead stream with no failure on record must be eligible at once")
	}
}

// A healthy stream is never skipped.
func TestHealthyStreamIsEligible(t *testing.T) {
	channel, hash := "ch-healthy", "hash-healthy"
	s := newStream(channel, hash, 0)
	if deadstreams.BlockedUntilRetry(channel, s) {
		t.Fatal("healthy stream must be eligible")
	}
}

// The dead mark lives in the database, so it survives a fresh snapshot load,
// and reviving clears it both places.
func TestDeadMarkPersistsAndReviveClearsIt(t *testing.T) {
	channel, hash := "ch-persist", "hash-persist"
	if err := deadstreams.MarkStreamDeadByHash(channel, hash, "vod unplayable"); err != nil {
		t.Fatalf("setup: mark dead failed: %v", err)
	}
	if _, err := db.Get().Exec(`SELECT 1`); err != nil {
		t.Fatalf("setup: database open failed: %v", err)
	}

	if !deadstreams.IsStreamDead(channel, hash) {
		t.Fatal("stream should be dead right after marking")
	}

	// drop the snapshot so the next read comes from the database
	deadstreams.ReloadOverrides()

	if !deadstreams.IsStreamDead(channel, hash) {
		t.Fatal("dead mark must survive a reload from the database")
	}

	if err := deadstreams.ReviveStream(channel, hash); err != nil {
		t.Fatalf("revive failed: %v", err)
	}
	if deadstreams.IsStreamDead(channel, hash) {
		t.Fatal("revived stream must not be dead")
	}
}

// Two concurrent reloads must not corrupt the snapshot: the last one wins and
// every read either sees the old or the new complete map.
func TestReloadOverridesIsSafe(t *testing.T) {
	channel, hash := "ch-racesafe", "hash-racesafe"
	if err := deadstreams.MarkStreamDeadByHash(channel, hash, "vod unplayable"); err != nil {
		t.Fatalf("setup: mark dead failed: %v", err)
	}

	done := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			deadstreams.ReloadOverrides()
		}()
	}
	go func() {
		wg.Wait()
		close(done)
	}()
	<-done

	if !deadstreams.IsStreamDead(channel, hash) {
		t.Fatal("dead mark must survive concurrent reloads")
	}
}
