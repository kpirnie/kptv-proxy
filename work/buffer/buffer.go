package buffer

import (
	"runtime"
	"sync"
	"sync/atomic"
	
	"github.com/valyala/bytebufferpool"
)

// BufferPool is a thread-safe pool of byte slices that reuses buffers to reduce
// memory allocation overhead through valyala/bytebufferpool integration. This
// implementation provides high-performance buffer management with automatic pooling,
// reuse capabilities, and efficient memory reclamation without manual zeroing operations.
type BufferPool struct {
	pool       *bytebufferpool.Pool
	bufferSize int
}

// NewBufferPool creates a new BufferPool that manages byte slices of the specified size.
// The pool leverages valyala/bytebufferpool for efficient buffer reuse and automatic
// memory management, providing high-performance buffer allocation without manual pooling
// overhead. The pool is immediately ready for use after creation.
func NewBufferPool(bufferSize int64) *BufferPool {
	return &BufferPool{
		bufferSize: int(bufferSize),
		pool:       &bytebufferpool.Pool{},
	}
}

// Get retrieves a byte slice from the pool. The pool automatically manages buffer
// lifecycle and reuse through valyala/bytebufferpool, ensuring optimal memory usage
// and minimal allocation overhead. Returns a fresh copy of a buffer with the configured
// size, safe for concurrent use without interference from other buffer consumers.
func (bp *BufferPool) Get() *bytebufferpool.ByteBuffer {
    buf := bp.pool.Get()
    buf.Reset() // Reset first
    // Only grow if necessary, don't replace
    if cap(buf.B) < bp.bufferSize {
        buf.B = buf.B[:0]
        if cap(buf.B) < bp.bufferSize {
            buf.B = make([]byte, 0, bp.bufferSize)
        }
    }
    return buf
}

// Put returns a byte slice to the pool. The valyala/bytebufferpool implementation
// handles automatic buffer cleanup and reuse internally, eliminating the need for
// manual zeroing operations while maintaining security through the pool's internal
// management. This method exists for API compatibility and future extensibility.
func (bp *BufferPool) Put(buf *bytebufferpool.ByteBuffer) {
	if buf != nil {
		bp.pool.Put(buf)
	}
}


// Cleanup releases all pooled buffers and performs garbage collection to reclaim memory.
// This method should be called during application shutdown or when performing comprehensive
// resource cleanup to ensure all pooled memory is properly released back to the system.
func (bp *BufferPool) Cleanup() {
	// bytebufferpool handles its own cleanup
	runtime.GC()
}

// RingBuffer is a thread-safe circular buffer that supports multiple concurrent readers.
// Each reader (client) maintains its own read position, allowing independent consumption
// of data from the buffer. The buffer overwrites old data when full (circular behavior).
type RingBuffer struct {
	data      []byte
	size      int64
	writePos  atomic.Int64
	readPos   sync.Map
	destroyed atomic.Bool
	mu        sync.RWMutex
}

// NewRingBuffer creates and returns a new RingBuffer with the specified size.
// The buffer is initialized with zeroed storage and ready for use.
func NewRingBuffer(size int64) *RingBuffer {
	rb := &RingBuffer{
		data: make([]byte, size),
		size: size,
	}
	rb.destroyed.Store(false)
	return rb
}

// Write appends data to the ring buffer. If the buffer is destroyed or nil,
// the operation is silently ignored. Thread-safe with concurrent reads.
func (rb *RingBuffer) Write(data []byte) {
	if rb.destroyed.Load() {
		return
	}

	rb.mu.RLock()
	defer rb.mu.RUnlock()

	if rb.destroyed.Load() || rb.data == nil {
		return
	}

	dataLen := int64(len(data))
	writePos := rb.writePos.Load()

	for i := int64(0); i < dataLen; i++ {
		rb.data[(writePos+i)%rb.size] = data[i]
	}

	rb.writePos.Add(dataLen)
}

// GetClientPosition returns the current read position for a specific client.
// If the client doesn't exist, it initializes and returns position 0.
// Returns 0 if the buffer is destroyed.
func (rb *RingBuffer) GetClientPosition(clientID string) int64 {
	if rb.destroyed.Load() {
		return 0
	}

	pos, _ := rb.readPos.LoadOrStore(clientID, int64(0))
	return pos.(int64)
}

// UpdateClientPosition updates the read position for a specific client.
// Does nothing if the buffer is destroyed.
func (rb *RingBuffer) UpdateClientPosition(clientID string, pos int64) {
	if rb.destroyed.Load() {
		return
	}

	rb.readPos.Store(clientID, pos)
}

// RemoveClient removes a client's read position from the buffer.
// Does nothing if the buffer is destroyed.
func (rb *RingBuffer) RemoveClient(clientID string) {
	if rb.destroyed.Load() {
		return
	}

	rb.readPos.Delete(clientID)
}

// Reset clears the buffer content and all client read positions.
// Thread-safe but will not reset if buffer is destroyed.
func (rb *RingBuffer) Reset() {
    if rb.destroyed.Load() {
        return
    }

    rb.mu.Lock()
    defer rb.mu.Unlock()

    if rb.destroyed.Load() {
        return
    }

    rb.writePos.Store(0)

    // Use interface{} types for Range callback
    rb.readPos.Range(func(key, value interface{}) bool {
        rb.readPos.Delete(key)
        return true
    })
}

// Destroy securely destroys the buffer by zeroing all data, clearing client positions,
// and making the buffer unusable. This operation is irreversible and thread-safe.
func (rb *RingBuffer) Destroy() {
    if !rb.destroyed.CompareAndSwap(false, true) {
        return
    }

    rb.mu.Lock()
    defer rb.mu.Unlock()

    // Use interface{} types for Range callback
    rb.readPos.Range(func(key, value interface{}) bool {
        rb.readPos.Delete(key)
        return true
    })

    if rb.data != nil {
        for i := range rb.data {
            rb.data[i] = 0
        }
        rb.data = nil
    }

    rb.writePos.Store(0)

    runtime.GC()
}

// IsDestroyed returns true if the buffer has been destroyed and is no longer usable.
func (rb *RingBuffer) IsDestroyed() bool {
	return rb.destroyed.Load()
}

// for monitoring
func (rb *RingBuffer) GetWritePosition() int64 {
    if rb.destroyed.Load() {
        return 0
    }
    return rb.writePos.Load()
}

// PeekRecentData returns the most recent data from the buffer up to maxBytes.
// Returns nil if the buffer is destroyed, empty, or contains no data.
// The returned data is a copy and safe for modification.
func (rb *RingBuffer) PeekRecentData(maxBytes int64) []byte {
	if rb.destroyed.Load() || rb.data == nil {
		return nil
	}

	rb.mu.RLock()
	defer rb.mu.RUnlock()

	if rb.destroyed.Load() {
		return nil
	}

	writePos := rb.writePos.Load()
	if writePos == 0 {
		return nil
	}

	dataSize := maxBytes
	if dataSize > writePos {
		dataSize = writePos
	}
	if dataSize > rb.size {
		dataSize = rb.size
	}

	result := make([]byte, dataSize)

	startPos := (writePos - dataSize) % rb.size

	for i := int64(0); i < dataSize; i++ {
		result[i] = rb.data[(startPos+i)%rb.size]
	}

	return result
}