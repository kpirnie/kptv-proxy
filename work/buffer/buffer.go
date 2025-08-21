package buffer

import (
	"sync"
	"sync/atomic"
)

// RingBuffer - Lock-free ring buffer for streaming
type RingBuffer struct {
	data     []byte
	size     int64
	writePos atomic.Int64
	readPos  sync.Map // Per-client read positions
}

// BufferPool manages reusable buffers
type BufferPool struct {
	pool        sync.Pool
	bufferSize  int
	initialized atomic.Bool
}

// RingBuffer - Lock-free ring buffer implementation
func NewRingBuffer(size int64) *RingBuffer {
	return &RingBuffer{
		data: make([]byte, size),
		size: size,
	}
}

func (rb *RingBuffer) Write(data []byte) {
	dataLen := int64(len(data))
	writePos := rb.writePos.Load()

	// Copy data to buffer (may wrap around)
	for i := int64(0); i < dataLen; i++ {
		rb.data[(writePos+i)%rb.size] = data[i]
	}

	// Update write position
	rb.writePos.Add(dataLen)
}

func (rb *RingBuffer) GetClientPosition(clientID string) int64 {
	pos, _ := rb.readPos.LoadOrStore(clientID, int64(0))
	return pos.(int64)
}

func (rb *RingBuffer) UpdateClientPosition(clientID string, pos int64) {
	rb.readPos.Store(clientID, pos)
}

func (rb *RingBuffer) RemoveClient(clientID string) {
	rb.readPos.Delete(clientID)
}

// BufferPool implementation
func NewBufferPool(bufferSize int64) *BufferPool {
	bp := &BufferPool{
		bufferSize: int(bufferSize),
	}
	bp.initialized.Store(false)
	return bp
}

func (bp *BufferPool) initialize() {
	if !bp.initialized.CompareAndSwap(false, true) {
		return
	}

	bp.pool = sync.Pool{
		New: func() interface{} {
			b := make([]byte, bp.bufferSize)
			return &b
		},
	}
}

func (bp *BufferPool) Get() []byte {
	if !bp.initialized.Load() {
		bp.initialize()
	}
	return *(bp.pool.Get().(*[]byte))
}

func (bp *BufferPool) Put(buf []byte) {
	if buf == nil {
		return
	}
	// Zero out buffer for security
	for i := range buf {
		buf[i] = 0
	}
	bp.pool.Put(&buf)
}

func (rb *RingBuffer) Reset() {
	rb.writePos.Store(0)
	rb.readPos.Range(func(key, value interface{}) bool {
		rb.readPos.Delete(key)
		return true
	})
}
