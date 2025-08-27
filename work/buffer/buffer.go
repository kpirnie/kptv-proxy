package buffer

import (
	"runtime"
	"sync"
	"sync/atomic"
)

// RingBuffer is a thread-safe circular buffer that supports multiple concurrent readers.
// Each reader (client) maintains its own read position, allowing independent consumption
// of data from the buffer. The buffer overwrites old data when full (circular behavior).
type RingBuffer struct {
	data      []byte       // underlying byte slice for storage
	size      int64        // fixed size of the buffer
	writePos  atomic.Int64 // atomic write position (global writer cursor)
	readPos   sync.Map     // per-client read positions (clientID -> position)
	destroyed atomic.Bool  // atomic flag indicating if buffer is destroyed
	mu        sync.RWMutex // read-write mutex for thread safety
}

// BufferPool is a thread-safe pool of byte slices that reuses buffers to reduce
// memory allocation overhead and provides secure zeroing of returned buffers.
type BufferPool struct {
	pool        sync.Pool   // underlying sync.Pool for buffer management
	bufferSize  int         // fixed size of each buffer in the pool
	initialized atomic.Bool // atomic flag indicating if pool is initialized
}

// NewRingBuffer creates and returns a new RingBuffer with the specified size.
// The buffer is initialized with zeroed storage and ready for use.
func NewRingBuffer(size int64) *RingBuffer {

	// Allocate underlying slice of fixed size
	rb := &RingBuffer{
		data: make([]byte, size),
		size: size,
	}

	// Mark buffer as not destroyed
	rb.destroyed.Store(false)
	return rb
}

// Write appends data to the ring buffer. If the buffer is destroyed or nil,
// the operation is silently ignored. Thread-safe with concurrent reads.
func (rb *RingBuffer) Write(data []byte) {

	// Skip writes if buffer destroyed
	if rb.destroyed.Load() {
		return
	}

	// Acquire read-lock (multiple readers can write safely due to atomic pos ops)
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	// Double-check destruction after acquiring lock
	if rb.destroyed.Load() || rb.data == nil {
		return
	}

	// Calculate length of incoming data
	dataLen := int64(len(data))

	// Capture current write position
	writePos := rb.writePos.Load()

	// Write each byte into the buffer in circular fashion
	for i := int64(0); i < dataLen; i++ {
		rb.data[(writePos+i)%rb.size] = data[i]
	}

	// Atomically advance write position by data length
	rb.writePos.Add(dataLen)
}

// GetClientPosition returns the current read position for a specific client.
// If the client doesn't exist, it initializes and returns position 0.
// Returns 0 if the buffer is destroyed.
func (rb *RingBuffer) GetClientPosition(clientID string) int64 {

	// Return default if buffer is destroyed
	if rb.destroyed.Load() {
		return 0
	}

	// Load or initialize client’s read position (defaults to 0)
	pos, _ := rb.readPos.LoadOrStore(clientID, int64(0))
	return pos.(int64)
}

// UpdateClientPosition updates the read position for a specific client.
// Does nothing if the buffer is destroyed.
func (rb *RingBuffer) UpdateClientPosition(clientID string, pos int64) {
	if rb.destroyed.Load() {
		return
	}

	// Store new read position for client
	rb.readPos.Store(clientID, pos)
}

// RemoveClient removes a client's read position from the buffer.
// Does nothing if the buffer is destroyed.
func (rb *RingBuffer) RemoveClient(clientID string) {
	if rb.destroyed.Load() {
		return
	}

	// Delete client entry from readPos map
	rb.readPos.Delete(clientID)
}

// Reset clears the buffer content and all client read positions.
// Thread-safe but will not reset if buffer is destroyed.
func (rb *RingBuffer) Reset() {

	// Skip reset if buffer already destroyed
	if rb.destroyed.Load() {
		return
	}

	// Acquire full write lock since we are mutating state
	rb.mu.Lock()
	defer rb.mu.Unlock()

	// Double-check after lock acquisition
	if rb.destroyed.Load() {
		return
	}

	// Reset write position to 0
	rb.writePos.Store(0)

	// Clear all stored client read positions
	rb.readPos.Range(func(key, value interface{}) bool {
		rb.readPos.Delete(key)
		return true
	})
}

// Destroy securely destroys the buffer by zeroing all data, clearing client positions,
// and making the buffer unusable. This operation is irreversible and thread-safe.
func (rb *RingBuffer) Destroy() {
	// Use CompareAndSwap to ensure destroy only executes once
	if !rb.destroyed.CompareAndSwap(false, true) {
		return
	}

	// Acquire write lock to protect destruction
	rb.mu.Lock()
	defer rb.mu.Unlock()

	// Delete all client positions
	rb.readPos.Range(func(key, value interface{}) bool {
		rb.readPos.Delete(key)
		return true
	})

	// Zero out buffer data for security, then release memory
	if rb.data != nil {
		for i := range rb.data {
			rb.data[i] = 0
		}
		rb.data = nil
	}

	// Reset write position to 0
	rb.writePos.Store(0)

	// Request garbage collection after secure destruction
	runtime.GC()
}

// IsDestroyed returns true if the buffer has been destroyed and is no longer usable.
func (rb *RingBuffer) IsDestroyed() bool {
	return rb.destroyed.Load()
}

// NewBufferPool creates a new BufferPool that manages byte slices of the specified size.
// The pool is lazily initialized on first use.
func NewBufferPool(bufferSize int64) *BufferPool {

	// Construct new buffer pool struct
	bp := &BufferPool{
		bufferSize: int(bufferSize),
	}

	// Mark as not yet initialized
	bp.initialized.Store(false)
	return bp
}

// initialize sets up the sync.Pool with the appropriate buffer creation function.
// This method is thread-safe and idempotent.
func (bp *BufferPool) initialize() {

	// Ensure only one initializer wins
	if !bp.initialized.CompareAndSwap(false, true) {
		return
	}

	// Set sync.Pool with buffer creation function
	bp.pool = sync.Pool{
		New: func() interface{} {
			b := make([]byte, bp.bufferSize)
			return &b
		},
	}
}

// Get retrieves a byte slice from the pool. If the pool is not initialized,
// it will be initialized automatically. The returned buffer is zeroed.
func (bp *BufferPool) Get() []byte {

	// Initialize pool if not ready
	if !bp.initialized.Load() {
		bp.initialize()
	}

	// Fetch from pool and dereference
	return *(bp.pool.Get().(*[]byte))
}

// Put returns a byte slice to the pool after securely zeroing its contents.
// If the buffer is nil, it is silently ignored.
func (bp *BufferPool) Put(buf []byte) {
	if buf == nil {
		return
	}

	// Overwrite buffer with zeroes for security
	for i := range buf {
		buf[i] = 0
	}

	// Return buffer pointer to pool
	bp.pool.Put(&buf)
}

// PeekRecentData returns the most recent data from the buffer up to maxBytes.
// Returns nil if the buffer is destroyed, empty, or contains no data.
// The returned data is a copy and safe for modification.
func (rb *RingBuffer) PeekRecentData(maxBytes int64) []byte {
	// Bail if buffer is destroyed or no data exists
	if rb.destroyed.Load() || rb.data == nil {
		return nil
	}

	// Acquire read lock while accessing buffer content
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	// Double-check after lock
	if rb.destroyed.Load() {
		return nil
	}

	// Load current global write position
	writePos := rb.writePos.Load()
	if writePos == 0 {
		return nil
	}

	// Determine how much data to copy
	dataSize := maxBytes
	if dataSize > writePos {
		dataSize = writePos
	}
	if dataSize > rb.size {
		dataSize = rb.size
	}

	// Allocate result slice
	result := make([]byte, dataSize)

	// Compute start position (circular wrap)
	startPos := (writePos - dataSize) % rb.size

	// Copy bytes from buffer into result
	for i := int64(0); i < dataSize; i++ {
		result[i] = rb.data[(startPos+i)%rb.size]
	}

	return result
}
