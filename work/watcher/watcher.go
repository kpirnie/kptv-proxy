package watcher

import (
	"context"
	"kptv-proxy/work/restream"
	"kptv-proxy/work/types"
	"kptv-proxy/work/utils"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

type WatcherManager struct {
	watchers sync.Map // map[string]*StreamWatcher
	logger   *log.Logger
	enabled  atomic.Bool
	stopChan chan struct{}
}

type StreamWatcher struct {
	channelName         string
	restreamer          *types.Restreamer
	logger              *log.Logger
	ctx                 context.Context
	cancel              context.CancelFunc
	lastCheck           time.Time
	consecutiveFailures int32
	running             atomic.Bool
}

type RestreamWrapper struct {
	*types.Restreamer
}

func NewWatcherManager(logger *log.Logger) *WatcherManager {
	return &WatcherManager{
		logger:   logger,
		stopChan: make(chan struct{}),
	}
}

func (wm *WatcherManager) Start() {
	if !wm.enabled.CompareAndSwap(false, true) {
		return
	}

	wm.logger.Printf("[WATCHER] Stream Watcher Manager started")
	go wm.cleanupRoutine()
}

func (wm *WatcherManager) Stop() {
	if !wm.enabled.CompareAndSwap(true, false) {
		return
	}

	close(wm.stopChan)

	// Stop all active watchers
	wm.watchers.Range(func(key, value interface{}) bool {
		watcher := value.(*StreamWatcher)
		watcher.Stop()
		return true
	})

	wm.logger.Printf("[WATCHER] Stream Watcher Manager stopped")
}

func (wm *WatcherManager) StartWatching(channelName string, restreamer *types.Restreamer) {
	// Stop any existing watcher for this channel
	if existing, exists := wm.watchers.LoadAndDelete(channelName); exists {
		existingWatcher := existing.(*StreamWatcher)
		existingWatcher.Stop()
	}

	// Create new watcher
	watcher := &StreamWatcher{
		channelName: channelName,
		restreamer:  restreamer,
		logger:      wm.logger,
		lastCheck:   time.Now(),
	}

	watcher.ctx, watcher.cancel = context.WithCancel(context.Background())
	wm.watchers.Store(channelName, watcher)

	// Start watching
	go watcher.Watch()

	currentIdx := int(atomic.LoadInt32(&restreamer.CurrentIndex))
	restreamer.Channel.Mu.RLock()
	var streamURL string
	if currentIdx < len(restreamer.Channel.Streams) {
		streamURL = restreamer.Channel.Streams[currentIdx].URL
	}
	restreamer.Channel.Mu.RUnlock()

	wm.logger.Printf("[WATCHER] Started watching channel %s (stream %d): %s",
		channelName, currentIdx, utils.LogURL(restreamer.Config, streamURL))
}

func (wm *WatcherManager) StopWatching(channelName string) {
	if watcher, exists := wm.watchers.LoadAndDelete(channelName); exists {
		w := watcher.(*StreamWatcher)
		w.Stop()
		wm.logger.Printf("[WATCHER] Stopped watching channel %s", channelName)
	}
}

func (wm *WatcherManager) cleanupRoutine() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-wm.stopChan:
			return
		case <-ticker.C:
			wm.watchers.Range(func(key, value interface{}) bool {
				watcher := value.(*StreamWatcher)

				// Check if restreamer is still running
				if !watcher.restreamer.Running.Load() {
					watcher.Stop()
					wm.watchers.Delete(key)
				}

				return true
			})
		}
	}
}

func (sw *StreamWatcher) Watch() {
	if !sw.running.CompareAndSwap(false, true) {
		return
	}

	defer sw.running.Store(false)

	// Check every 10 seconds
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-sw.ctx.Done():
			return
		case <-ticker.C:
			if !sw.restreamer.Running.Load() {
				return
			}

			sw.checkStreamHealth()
		}
	}
}

func (sw *StreamWatcher) Stop() {
	if sw.cancel != nil {
		sw.cancel()
	}
}

func (sw *StreamWatcher) checkStreamHealth() {
	startTime := time.Now()

	// Use existing restreamer state - NO additional network requests
	hasIssues := sw.evaluateStreamHealthFromState()

	checkDuration := time.Since(startTime)

	if sw.restreamer.Config.Debug {
		sw.logger.Printf("[WATCHER] Channel %s health check took %v: Issues=%v",
			sw.channelName, checkDuration, hasIssues)
	}

	if hasIssues {
		failures := atomic.AddInt32(&sw.consecutiveFailures, 1)

		if sw.restreamer.Config.Debug {
			sw.logger.Printf("[WATCHER] Channel %s: Health issue detected (consecutive failures: %d)",
				sw.channelName, failures)
		}

		// Trigger failover after 5 consecutive failures
		if failures >= 5 {
			sw.triggerStreamSwitch("health_check_failed")
			atomic.StoreInt32(&sw.consecutiveFailures, 0)
		}
	} else {
		// Reset failure count on healthy stream
		atomic.StoreInt32(&sw.consecutiveFailures, 0)
	}

	sw.lastCheck = time.Now()
}

func (sw *StreamWatcher) evaluateStreamHealthFromState() bool {
	hasIssues := false

	// Check buffer health (no network requests)
	if sw.restreamer.Buffer == nil || sw.restreamer.Buffer.IsDestroyed() {
		if sw.restreamer.Config.Debug {
			sw.logger.Printf("[WATCHER] Channel %s: Buffer destroyed", sw.channelName)
		}
		hasIssues = true
	}

	// Check if there's been recent activity
	lastActivity := sw.restreamer.LastActivity.Load()
	timeSinceActivity := time.Now().Unix() - lastActivity

	if timeSinceActivity > 60 { // No activity for 60 seconds
		if sw.restreamer.Config.Debug {
			sw.logger.Printf("[WATCHER] Channel %s: No activity for %d seconds",
				sw.channelName, timeSinceActivity)
		}
		hasIssues = true
	}

	// Check if restreamer is still running but no clients (might indicate a problem)
	clientCount := 0
	sw.restreamer.Clients.Range(func(_, _ interface{}) bool {
		clientCount++
		return true
	})

	if sw.restreamer.Running.Load() && clientCount == 0 {
		if sw.restreamer.Config.Debug {
			sw.logger.Printf("[WATCHER] Channel %s: Restreamer running but no clients", sw.channelName)
		}
		// This might not be an issue, but worth noting
	}

	// Check if context is cancelled (indicates stream problems)
	select {
	case <-sw.restreamer.Ctx.Done():
		if sw.restreamer.Running.Load() {
			if sw.restreamer.Config.Debug {
				sw.logger.Printf("[WATCHER] Channel %s: Context cancelled but still marked as running",
					sw.channelName)
			}
			hasIssues = true
		}
	default:
	}

	return hasIssues
}

func (sw *StreamWatcher) triggerStreamSwitch(reason string) {
	if sw.restreamer.Config.Debug {
		sw.logger.Printf("[WATCHER] Triggering stream switch for channel %s due to: %s",
			sw.channelName, reason)
	}

	// Find next available stream (reuse existing logic)
	nextIndex := sw.findNextAvailableStream()
	if nextIndex == -1 {
		if sw.restreamer.Config.Debug {
			sw.logger.Printf("[WATCHER] No alternative streams found for channel %s",
				sw.channelName)
		}
		return
	}

	// Check if we have clients before switching
	clientCount := 0
	sw.restreamer.Clients.Range(func(_, _ interface{}) bool {
		clientCount++
		return true
	})

	if clientCount == 0 {
		if sw.restreamer.Config.Debug {
			sw.logger.Printf("[WATCHER] No clients connected for channel %s, skipping switch",
				sw.channelName)
		}
		return
	}

	if sw.restreamer.Config.Debug {
		sw.logger.Printf("[WATCHER] Switching channel %s to stream %d for %d clients",
			sw.channelName, nextIndex, clientCount)
	}

	// Use existing stream switching mechanism
	sw.forceStreamRestart(nextIndex)
}

func (sw *StreamWatcher) forceStreamRestart(newIndex int) {
	// Set preferred stream index
	atomic.StoreInt32(&sw.restreamer.Channel.PreferredStreamIndex, int32(newIndex))
	atomic.StoreInt32(&sw.restreamer.CurrentIndex, int32(newIndex))

	// Stop current streaming if running
	if sw.restreamer.Running.Load() {
		sw.restreamer.Running.Store(false)
		sw.restreamer.Cancel()

		// Brief pause to allow cleanup
		time.Sleep(200 * time.Millisecond)
	}

	// Create new context for the restreamer
	ctx, cancel := context.WithCancel(context.Background())
	sw.restreamer.Ctx = ctx
	sw.restreamer.Cancel = cancel

	// Reset buffer if it exists
	if sw.restreamer.Buffer != nil && !sw.restreamer.Buffer.IsDestroyed() {
		sw.restreamer.Buffer.Reset()
	}

	// Check if we still have clients
	clientCount := 0
	sw.restreamer.Clients.Range(func(_, _ interface{}) bool {
		clientCount++
		return true
	})

	if clientCount > 0 {
		// Start new streaming using existing Stream() method
		sw.restreamer.Running.Store(true)
		go sw.restartWithExistingLogic()
	}

	// Reset failure count for new stream
	atomic.StoreInt32(&sw.consecutiveFailures, 0)
}

func (sw *StreamWatcher) restartWithExistingLogic() {
	defer func() {
		if rec := recover(); rec != nil {
			if sw.restreamer.Config.Debug {
				sw.logger.Printf("[WATCHER_RESTART_PANIC] Channel %s: Recovered from panic: %v",
					sw.channelName, rec)
			}
		}
		sw.restreamer.Running.Store(false)
	}()

	if sw.restreamer.Config.Debug {
		currentIdx := int(atomic.LoadInt32(&sw.restreamer.CurrentIndex))
		sw.logger.Printf("[WATCHER_RESTART] Channel %s: Starting stream restart at index %d",
			sw.channelName, currentIdx)
	}

	// Call the existing Stream() method which handles all connection management,
	// source selection, master playlist processing, etc.
	r := NewRestreamWrapper(sw.restreamer)
	r.Stream()
}

func (sw *StreamWatcher) findNextAvailableStream() int {
	sw.restreamer.Channel.Mu.RLock()
	defer sw.restreamer.Channel.Mu.RUnlock()

	streamCount := len(sw.restreamer.Channel.Streams)
	if streamCount <= 1 {
		return -1
	}

	currentIdx := int(atomic.LoadInt32(&sw.restreamer.CurrentIndex))

	// Try next stream in order
	for i := 1; i < streamCount; i++ {
		nextIndex := (currentIdx + i) % streamCount
		stream := sw.restreamer.Channel.Streams[nextIndex]

		// Skip blocked streams
		if atomic.LoadInt32(&stream.Blocked) == 1 {
			continue
		}

		// Check if source has available connections (don't actually use them)
		currentConns := atomic.LoadInt32(&stream.Source.ActiveConns)
		if currentConns >= int32(stream.Source.MaxConnections) {
			if sw.restreamer.Config.Debug {
				sw.logger.Printf("[WATCHER] Source at connection limit (%d/%d), skipping: %s",
					currentConns, stream.Source.MaxConnections, stream.Source.Name)
			}
			continue
		}

		return nextIndex
	}

	return -1
}

// RestreamWrapper wraps the existing restreamer to call the Stream() method
func NewRestreamWrapper(restreamer *types.Restreamer) *RestreamWrapper {
	return &RestreamWrapper{
		Restreamer: restreamer,
	}
}

// Stream method that reuses the existing streaming logic
func (rw *RestreamWrapper) Stream() {
	// Use the existing complete Stream() method
	r := &restream.Restream{Restreamer: rw.Restreamer}
	r.WatcherStream()
}
