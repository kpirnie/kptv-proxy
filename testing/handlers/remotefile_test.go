package handlers_test

import (
	"context"
	"kptv-proxy/work/config"
	"kptv-proxy/work/constants"
	"kptv-proxy/work/handlers"
	"testing"
	"time"
)

// sourceWith returns a source with the given connection limit and that many
// slots already taken by somebody else's transfer.
func sourceWith(max int, taken int32) *config.SourceConfig {
	source := &config.SourceConfig{MaxConnections: max}
	source.ActiveConns.Add(taken)
	return source
}

// queueTimings shortens the poll interval so a test does not sit through the
// production 100ms between slot rechecks. Returns the function that restores
// the previous values.
func queueTimings(t *testing.T, poll time.Duration) func() {
	t.Helper()

	prevPoll := constants.Internal.RemoteFileCapacityPoll
	constants.Internal.RemoteFileCapacityPoll = poll

	return func() {
		constants.Internal.RemoteFileCapacityPoll = prevPoll
	}
}

// A source with a free slot hands it over at once, and releasing gives it back.
func TestFreeSlotIsTakenAtOnce(t *testing.T) {
	restore := queueTimings(t, 10*time.Millisecond)
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
	restore := queueTimings(t, 10*time.Millisecond)
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
	restore := queueTimings(t, 10*time.Millisecond)
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
	restore := queueTimings(t, 10*time.Millisecond)
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
	restore := queueTimings(t, 10*time.Millisecond)
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
