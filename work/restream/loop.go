package restream

import (
	"context"
	"kptv-proxy/work/constants"
	"kptv-proxy/work/logger"
	"kptv-proxy/work/metrics"
	"kptv-proxy/work/stream"
	"sync/atomic"
	"time"
)

// Stream is the main streaming loop for the restreamer. It owns the current
// stream index and runs one attempt at a time, reacting to the outcome of each:
// a stop ends the loop, a switch request adopts a new index, a success
// reconnects to the same index, and a failure rotates and retries until the
// attempt budget is spent, at which point the fallback video plays before the
// budget resets.
func (r *Restream) Stream() {

	// Ensure panic recovery to avoid crashing the whole process
	defer func() {

		if rec := recover(); rec != nil {
			logger.Debug("{restream/restream - Stream} Channel %s: Recovered from panic: %v", r.Channel.Name, rec)
		}

		// Mark restreamer as no longer running
		r.Running.Store(false)
		r.ClearAttemptContext()

		// Reset active connections metric
		metrics.ActiveConnections.WithLabelValues(r.Channel.Name).Set(0)
	}()

	logger.Debug("{restream/restream - Stream} Channel %s: Starting streaming loop", r.Channel.Name)

	// Lock channel to get stream count
	r.Channel.Mu.RLock()
	streamCount := len(r.Channel.Streams)
	r.Channel.Mu.RUnlock()

	// Bail out if no streams exist
	if streamCount == 0 {
		return
	}

	// Set the current index immediately so other components can read it correctly
	atomic.StoreInt32(&r.CurrentIndex, int32(r.startingIndex(streamCount)))

	// Retry configuration
	maxTotalAttempts := streamCount * constants.Internal.StreamMaxAttemptsMultiplier // maximum attempts across streams
	totalAttempts := 0                                                               // attempts counter
	consecutiveFailures := make(map[int]int)                                         // map of stream index → consecutive failures

	for {

		// deliberate cancellation (stopStream or shutdown) always ends the loop
		if r.LifetimeContext().Err() != nil {
			logger.Debug("{restream/restream - Stream} Channel %s: Session cancelled, exiting", r.Channel.Name)
			return
		}

		// Bail if no clients
		if r.ClientCount.Load() == 0 {
			logger.Debug("{restream/restream - Stream} Channel %s: No clients remaining", r.Channel.Name)
			r.LastStreamFailed.Store(true)
			return
		}

		// Adopt any pending switch target before the next attempt
		if target := r.TakeSwitch(); target >= 0 && target < streamCount {
			logger.Debug("{restream/restream - Stream} Channel %s: Adopting switch to stream %d", r.Channel.Name, target)
			atomic.StoreInt32(&r.CurrentIndex, int32(target))
			totalAttempts = 0
			consecutiveFailures = make(map[int]int)
		}

		// Attempt budget spent — play the fallback video, then start over
		if totalAttempts >= maxTotalAttempts {
			logger.Debug("{restream/restream - Stream} Channel %s: All streams failed after %d attempts", r.Channel.Name, totalAttempts)
			if !r.runFallback() {
				return
			}
			totalAttempts = 0
			consecutiveFailures = make(map[int]int)
			continue
		}

		// Get current index and increment attempts
		currentIdx := int(atomic.LoadInt32(&r.CurrentIndex))
		totalAttempts++

		outcome, bytesTransferred := r.runAttempt(currentIdx)
		logger.Debug("{restream/restream - Stream} Channel %s: Stream %d outcome %d (%d bytes)", r.Channel.Name, currentIdx, outcome, bytesTransferred)

		switch outcome {

		case outcomeStopped:
			r.LastStreamFailed.Store(false)
			return

		case outcomeSwitch:
			continue

		case outcomeSuccess:
			consecutiveFailures[currentIdx] = 0
			totalAttempts = 0
			r.LastStreamFailed.Store(false)

			// Pause before reconnecting to prevent rapid cycling on short .ts
			// segments — gives the client's WriteChan time to drain fully
			select {
			case <-r.LifetimeContext().Done():
				return
			case <-time.After(constants.Internal.EOFRestartDelay):
			}
			continue

		case outcomeFailure:
			consecutiveFailures[currentIdx]++
			logger.Debug("{restream/restream - Stream} Channel %s: Stream %d failed (consecutive failures: %d)",
				r.Channel.Name, currentIdx, consecutiveFailures[currentIdx])
			r.recordStreamFailure(currentIdx, consecutiveFailures[currentIdx])

			// If multiple streams, rotate index
			if streamCount > 1 {
				newIdx := (currentIdx + 1) % streamCount
				atomic.StoreInt32(&r.CurrentIndex, int32(newIdx))
				logger.Debug("{restream/restream - Stream} Channel %s: Switching from stream %d to stream %d", r.Channel.Name, currentIdx, newIdx)
			}

			// Add jitter to prevent thundering herd when multiple channels fail simultaneously
			jitter := constants.Internal.StreamJitterMinMs + time.Duration(time.Now().UnixNano())%constants.Internal.StreamJitterRangeMs

			select {
			case <-r.LifetimeContext().Done():
				return
			case <-time.After(jitter):
			}
		}
	}
}

// startingIndex resolves the index the loop begins at, preferring a manually
// set current index, then the channel's preferred index, then zero.
func (r *Restream) startingIndex(streamCount int) int {
	currentIndex := int(atomic.LoadInt32(&r.CurrentIndex))
	preferredIndex := int(atomic.LoadInt32(&r.Channel.PreferredStreamIndex))

	if currentIndex >= 0 && currentIndex < streamCount && currentIndex == preferredIndex {
		logger.Debug("{restream/restream - startingIndex} Channel %s: Using manually set stream index %d", r.Channel.Name, currentIndex)
		return currentIndex
	}

	if preferredIndex >= 0 && preferredIndex < streamCount {
		logger.Debug("{restream/restream - startingIndex} Channel %s: Starting with preferred stream index %d", r.Channel.Name, preferredIndex)
		return preferredIndex
	}

	logger.Debug("{restream/restream - startingIndex} Channel %s: Starting with default stream index 0", r.Channel.Name)
	return 0
}

// runAttempt runs one upstream attempt under its own cancellable context and
// classifies the result. Cancelling that context interrupts only the attempt,
// leaving the session context intact.
//
// Returns:
//   - streamOutcome: classification of the attempt
//   - int64: bytes transferred during the attempt
func (r *Restream) runAttempt(index int) (streamOutcome, int64) {

	ctx, cancel := context.WithCancel(r.LifetimeContext())
	r.SetAttemptContext(ctx, cancel)
	defer func() {
		cancel()
		r.ClearAttemptContext()
	}()

	r.resetBufferSafely()

	logger.Debug("{restream/restream - runAttempt} Channel %s: Attempting stream %d", r.Channel.Name, index)
	success, bytesTransferred := r.StreamFromSource(index)

	if r.SwitchPending() {
		return outcomeSwitch, bytesTransferred
	}

	if r.LifetimeContext().Err() != nil || r.ClientCount.Load() == 0 {
		return outcomeStopped, bytesTransferred
	}

	// a very brief success is a failure in disguise
	if success && bytesTransferred >= constants.Internal.BriefSuccessThreshold {
		return outcomeSuccess, bytesTransferred
	}

	return outcomeFailure, bytesTransferred
}

// recordStreamFailure tracks a failing stream for potential auto-blocking once
// it has failed enough consecutive times.
func (r *Restream) recordStreamFailure(index int, failures int) {

	if failures < constants.Internal.StreamConsecutiveFailureThreshold {
		return
	}

	r.Channel.Mu.RLock()
	if index >= len(r.Channel.Streams) {
		r.Channel.Mu.RUnlock()
		return
	}
	currentStream := r.Channel.Streams[index]
	r.Channel.Mu.RUnlock()

	stream.HandleStreamFailure(currentStream, r.Config, r.Channel.Name, index)

	logger.Debug("{restream/restream - recordStreamFailure} Channel %s: Stream %d failed %d consecutive times, tracked for potential auto-blocking",
		r.Channel.Name, index, failures)
}

// runFallback plays the offline video for its configured period so a transient
// provider outage does not strand clients, then reports whether the loop should
// carry on retrying real sources.
func (r *Restream) runFallback() bool {

	if r.ClientCount.Load() == 0 {
		return false
	}

	ctx, cancel := context.WithCancel(r.LifetimeContext())
	r.SetAttemptContext(ctx, cancel)
	r.streamFallbackVideo()
	cancel()
	r.ClearAttemptContext()

	if r.ClientCount.Load() == 0 || r.LifetimeContext().Err() != nil {
		return false
	}

	logger.Debug("{restream/restream - runFallback} Channel %s: Retrying real sources after fallback period", r.Channel.Name)
	return true
}

// stopStream forces the restreamer to stop streaming immediately.
// It cancels the context, destroys the buffer, resets state, and runs GC.
func (r *Restream) stopStream() {

	// serialize against AddClient's start block, and re-check the client
	// count under the lock — a client may have connected between the
	// caller's zero-count observation and now, in which case stopping
	// would tear down a stream that just gained a viewer
	r.Lifecycle.Lock()
	defer r.Lifecycle.Unlock()

	// setup the client counter
	clientCount := int(r.ClientCount.Load())
	if clientCount > 0 {
		logger.Debug("{restream/restream - stopStream} Channel %s: Client connected during stop, aborting", r.Channel.Name)
		return
	}

	// Only proceed if running state changes from true → false
	if r.Running.CompareAndSwap(true, false) {
		logger.Debug("{restream/restream - stopStream} Stopping stream for channel %s", r.Channel.Name)

		// Cancel the current streaming context
		r.CancelStream()
		logger.Debug("{restream/restream - stopStream} Context cancelled for channel %s", r.Channel.Name)

		// Destroy buffer if valid
		if b := r.LoadBuffer(); b != nil && !b.IsDestroyed() {
			b.Destroy()
			logger.Debug("{restream/restream - stopStream} Buffer destroyed for channel %s", r.Channel.Name)
		}
		r.StoreBuffer(nil)

		// the context is NOT recreated here — AddClient installs a fresh one
		// when the next session starts, so a running Stream() goroutine never
		// has its context swapped out from under it
	}
}

// WatcherStream provides an external entry point for observers
// to run the full Stream loop directly.
func (r *Restream) WatcherStream() {
	r.Stream()
}

// WatcherStreamFromSource provides an external entry point
// for observers/watchers to call StreamFromSource.
// This is useful for testing or monitoring streams.
func (r *Restream) WatcherStreamFromSource(index int) (bool, int64) {
	return r.StreamFromSource(index)
}

// ForceStreamSwitch requests a switch to a specific stream index while
// preserving clients. The running loop adopts the request on its next
// iteration; only the in-flight attempt is cancelled, never the session.
func (r *Restream) ForceStreamSwitch(newIndex int) {
	logger.Debug("{restream/restream - ForceStreamSwitch} Channel %s: Switching to stream %d", r.Channel.Name, newIndex)

	// Update preferred stream index on the channel
	atomic.StoreInt32(&r.Channel.PreferredStreamIndex, int32(newIndex))

	// If not running, just update index
	if !r.Running.Load() {
		atomic.StoreInt32(&r.CurrentIndex, int32(newIndex))
		return
	}

	// Clients stay on the same HTTP connection — VLC treats a closed connection
	// as end-of-stream and will not re-request, so we keep it open and let the
	// loop resume into it on the new index.
	r.RequestSwitch(newIndex)
	r.CancelAttempt()
}
