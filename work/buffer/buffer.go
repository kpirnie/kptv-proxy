package buffer

import (
	"runtime"
	"sync"
	"sync/atomic"
)

// ByteBuffer represents a reusable byte buffer with a public byte slice field for direct
// access and manipulation. This type provides API compatibility with valyala/bytebufferpool's
// ByteBuffer interface while eliminating the external dependency through a lightweight
// stdlib-backed implementation using sync.Pool for efficient memory management.
//
// The buffer exposes its underlying byte slice through the B field, enabling zero-copy
// operations and direct slice manipulation for high-performance streaming workloads.
// Consumers should use Reset() to clear buffer contents before reuse and access data
// through the B field for both read and write operations.
type ByteBuffer struct {
	B []byte // Underlying byte slice for direct access and manipulation
}

// Reset clears the buffer contents by resetting the byte slice length to zero while
// preserving the allocated capacity for reuse. This avoids unnecessary memory allocation
// when the buffer is recycled through the pool, maintaining optimal performance for
// high-frequency buffer operations in streaming contexts.
func (b *ByteBuffer) Reset() {
	b.B = b.B[:0]
}

// Len returns the current length of data stored in the buffer. This reflects the number
// of bytes actively in use, not the total allocated capacity of the underlying slice.
func (b *ByteBuffer) Len() int {
	return len(b.B)
}

// Write appends data to the buffer by extending the underlying byte slice. This method
// implements the io.Writer interface pattern, enabling seamless integration with standard
// library functions that expect writer semantics. Returns the number of bytes written
// and a nil error, as in-memory writes cannot fail under normal conditions.
//
// Parameters:
//   - p: byte slice containing data to append to the buffer
//
// Returns:
//   - int: number of bytes written (always equal to len(p))
//   - error: always nil for in-memory buffer writes
func (b *ByteBuffer) Write(p []byte) (int, error) {
	b.B = append(b.B, p...)
	return len(p), nil
}

// BufferPool is a thread-safe pool of ByteBuffer instances that reuses buffers to reduce
// memory allocation overhead through sync.Pool integration. This implementation provides
// high-performance buffer management with automatic pooling, reuse capabilities, and
// efficient memory reclamation without manual zeroing operations.
//
// The pool enforces a configurable buffer size and includes an oversized buffer guard
// that prevents excessively large buffers from being returned to the pool, maintaining
// predictable memory usage patterns across long-running streaming sessions.
type BufferPool struct {
	pool       sync.Pool
	bufferSize int
}

// NewBufferPool creates a new BufferPool that manages ByteBuffer instances of the specified
// size. The pool leverages sync.Pool for efficient buffer reuse and automatic memory
// management, providing high-performance buffer allocation without external dependency
// overhead. The pool is immediately ready for use after creation.
//
// Parameters:
//   - bufferSize: target capacity in bytes for each pooled buffer
//
// Returns:
//   - *BufferPool: initialized pool ready for concurrent Get/Put operations
func NewBufferPool(bufferSize int64) *BufferPool {
	size := int(bufferSize)
	bp := &BufferPool{
		bufferSize: size,
	}

	// Configure sync.Pool with factory function that pre-allocates buffers at the target capacity
	bp.pool = sync.Pool{
		New: func() any {
			return &ByteBuffer{
				B: make([]byte, 0, size),
			}
		},
	}

	// return the buffer pool
	return bp
}

// Get retrieves a ByteBuffer from the pool with the configured capacity. The buffer is
// reset to zero length before return, ensuring clean state for the caller. If the retrieved
// buffer's capacity is below the configured size, a new backing slice is allocated to
// guarantee the caller receives adequate capacity for their operations.
//
// Returns:
//   - *ByteBuffer: ready-to-use buffer with at minimum the configured capacity
func (bp *BufferPool) Get() *ByteBuffer {
	// Retrieve buffer from pool (may be recycled or newly allocated)
	buf := bp.pool.Get().(*ByteBuffer)

	// Reset to zero length while preserving allocated capacity
	buf.Reset()

	// Ensure buffer meets minimum capacity requirements
	if cap(buf.B) < bp.bufferSize {
		buf.B = make([]byte, 0, bp.bufferSize)
	}

	// return the buffer
	return buf
}

// Put returns a ByteBuffer to the pool for reuse. Buffers that have grown beyond 4x the
// configured pool size are discarded rather than pooled to prevent memory bloat from
// one-off large allocations accumulating in the pool over time. This guard ensures
// predictable memory usage patterns during long-running streaming sessions.
//
// Parameters:
//   - buf: ByteBuffer to return to the pool, nil values are safely ignored
func (bp *BufferPool) Put(buf *ByteBuffer) {
	if buf == nil {
		return
	}

	// Discard oversized buffers to prevent memory bloat from accumulating
	// in the pool during long-running streaming sessions
	if cap(buf.B) > bp.bufferSize*4 {
		return
	}

	// Reset length and return to pool for reuse
	buf.B = buf.B[:0]
	bp.pool.Put(buf)
}

// Cleanup releases all pooled buffers by allowing the garbage collector to reclaim
// idle pool entries on its next cycle. This method should be called during application
// shutdown or when performing comprehensive resource cleanup to ensure all pooled
// memory is properly released back to the system.
func (bp *BufferPool) Cleanup() {

	// sync.Pool entries are automatically cleared between GC cycles;
	// trigger collection to reclaim idle pool entries immediately
	runtime.GC()
}

// RingBuffer is a thread-safe circular buffer that supports multiple concurrent readers.
// Each reader (client) maintains its own read position, allowing independent consumption
// of data from the buffer. The buffer overwrites old data when full (circular behavior).
//
// The ring buffer uses a combination of atomic operations for the write position and
// a sync.Map for per-client read positions to minimize lock contention during high-throughput
// streaming operations. A read-write mutex protects the underlying data slice during
// write and peek operations while allowing concurrent client position management.
type RingBuffer struct {
	data      []byte       // Circular buffer storage
	size      int64        // Total buffer capacity in bytes
	writePos  atomic.Int64 // Current write position (monotonically increasing, modulo size for indexing)
	readPos   sync.Map     // Per-client read positions (clientID string -> int64 position)
	destroyed atomic.Bool  // Destruction flag preventing operations on invalidated buffers
	mu        sync.RWMutex // Read-write mutex protecting data slice access
}

// NewRingBuffer creates and returns a new RingBuffer with the specified size.
// The buffer is initialized with zeroed storage and ready for concurrent use
// by multiple readers and a single writer.
//
// Parameters:
//   - size: capacity of the ring buffer in bytes
//
// Returns:
//   - *RingBuffer: initialized circular buffer ready for read/write operations
func NewRingBuffer(size int64) *RingBuffer {

	// create the ring buffer
	rb := &RingBuffer{
		data: make([]byte, size),
		size: size,
	}
	rb.destroyed.Store(false)

	// return the ring buffer
	return rb
}

// Write appends data to the ring buffer at the current write position using optimized
// bulk copy operations. If the write spans the buffer boundary, two copy operations
// handle the wrap-around efficiently using Go's built-in copy() which compiles down
// to memmove for maximum throughput. Thread-safe with concurrent reads through
// read-lock acquisition.
//
// Parameters:
//   - data: byte slice to write into the circular buffer
func (rb *RingBuffer) Write(data []byte) {

	// Early return if buffer has been destroyed
	if rb.destroyed.Load() {
		return
	}

	// Acquire read lock to prevent destruction during write
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	// Double-check destruction state after acquiring lock
	if rb.destroyed.Load() || rb.data == nil {
		return
	}

	dataLen := int64(len(data))
	writePos := rb.writePos.Load() % rb.size

	// First chunk: from current write position to end of buffer (or end of data)
	n := copy(rb.data[writePos:], data)

	// Second chunk: wrap around to beginning if data extends past buffer boundary
	if int64(n) < dataLen {
		copy(rb.data[:], data[n:])
	}

	// Advance write position atomically (monotonically increasing)
	rb.writePos.Add(dataLen)
}

// GetClientPosition returns the current read position for a specific client.
// If the client doesn't exist, it initializes and returns position 0.
// Returns 0 if the buffer is destroyed.
//
// Parameters:
//   - clientID: unique identifier for the client reader
//
// Returns:
//   - int64: current read position for the specified client
func (rb *RingBuffer) GetClientPosition(clientID string) int64 {
	if rb.destroyed.Load() {
		return 0
	}

	// Load existing position or atomically store initial position of 0
	pos, _ := rb.readPos.LoadOrStore(clientID, int64(0))
	return pos.(int64)
}

// UpdateClientPosition updates the read position for a specific client.
// Does nothing if the buffer is destroyed.
//
// Parameters:
//   - clientID: unique identifier for the client reader
//   - pos: new read position to set for the client
func (rb *RingBuffer) UpdateClientPosition(clientID string, pos int64) {
	if rb.destroyed.Load() {
		return
	}

	rb.readPos.Store(clientID, pos)
}

// RemoveClient removes a client's read position from the buffer, freeing the
// associated tracking resources. Does nothing if the buffer is destroyed.
//
// Parameters:
//   - clientID: unique identifier for the client reader to remove
func (rb *RingBuffer) RemoveClient(clientID string) {
	if rb.destroyed.Load() {
		return
	}

	rb.readPos.Delete(clientID)
}

// Reset clears the buffer write position and removes all client read positions,
// effectively resetting the buffer to its initial empty state. Thread-safe through
// exclusive lock acquisition, but will not reset if the buffer has been destroyed.
func (rb *RingBuffer) Reset() {
	if rb.destroyed.Load() {
		return
	}

	rb.mu.Lock()
	defer rb.mu.Unlock()

	// Double-check destruction state after acquiring exclusive lock
	if rb.destroyed.Load() {
		return
	}

	// Reset write position to beginning
	rb.writePos.Store(0)

	// Remove all client read positions
	rb.readPos.Range(func(key, value interface{}) bool {
		rb.readPos.Delete(key)
		return true
	})
}

// Destroy permanently invalidates the buffer by clearing all client positions,
// zeroing the data slice, and releasing the underlying storage. This operation
// is irreversible and thread-safe through atomic compare-and-swap on the destruction
// flag, ensuring exactly-once execution even under concurrent invocation.
func (rb *RingBuffer) Destroy() {
	// Atomic CAS ensures exactly-once destruction semantics
	if !rb.destroyed.CompareAndSwap(false, true) {
		return
	}

	rb.mu.Lock()
	defer rb.mu.Unlock()

	// Remove all client read positions
	rb.readPos.Range(func(key, value interface{}) bool {
		rb.readPos.Delete(key)
		return true
	})

	// Release data slice for garbage collection
	if rb.data != nil {
		rb.data = nil
	}

	// Reset write position
	rb.writePos.Store(0)

	// Trigger garbage collection to reclaim released buffer memory immediately
	runtime.GC()
}

// IsDestroyed returns true if the buffer has been destroyed and is no longer usable.
// This check is lock-free through atomic load operations and safe for use as a
// pre-condition guard in concurrent contexts.
//
// Returns:
//   - bool: true if Destroy() has been called, false otherwise
func (rb *RingBuffer) IsDestroyed() bool {

	// return if the ring buffer actually has been destroyed
	return rb.destroyed.Load()
}

// GetWritePosition returns the current monotonically increasing write position for
// monitoring and diagnostics purposes. Returns 0 if the buffer has been destroyed.
//
// Returns:
//   - int64: current write position (total bytes written, not modulo buffer size)
func (rb *RingBuffer) GetWritePosition() int64 {
	if rb.destroyed.Load() {
		return 0
	}

	// return the ring buffers position
	return rb.writePos.Load()
}

// PeekRecentData returns the most recent data from the buffer up to maxBytes without
// advancing any read positions. The method uses optimized bulk copy operations to extract
// data efficiently, handling buffer wrap-around with at most two copy() calls that
// compile down to memmove. Returns nil if the buffer is destroyed, empty, or contains
// no data. The returned data is a copy and safe for modification without affecting
// the buffer contents.
//
// Parameters:
//   - maxBytes: maximum number of bytes to retrieve from the most recent data
//
// Returns:
//   - []byte: copy of the most recent buffer data, or nil if unavailable
func (rb *RingBuffer) PeekRecentData(maxBytes int64) []byte {
	if rb.destroyed.Load() || rb.data == nil {
		return nil
	}

	rb.mu.RLock()
	defer rb.mu.RUnlock()

	// Double-check destruction state after acquiring lock
	if rb.destroyed.Load() {
		return nil
	}

	writePos := rb.writePos.Load()
	if writePos == 0 {
		return nil
	}

	// Clamp data size to available content and buffer capacity
	dataSize := maxBytes
	if dataSize > writePos {
		dataSize = writePos
	}
	if dataSize > rb.size {
		dataSize = rb.size
	}

	result := make([]byte, dataSize)
	startPos := (writePos - dataSize) % rb.size

	// First chunk: from start position to end of buffer (or end of needed data)
	firstChunk := rb.size - startPos
	if firstChunk > dataSize {
		firstChunk = dataSize
	}
	copy(result[:firstChunk], rb.data[startPos:startPos+firstChunk])

	// Second chunk: wrap around from beginning if data spans the boundary
	if firstChunk < dataSize {
		copy(result[firstChunk:], rb.data[:dataSize-firstChunk])
	}

	// return the recent buffer data copy
	return result
}
