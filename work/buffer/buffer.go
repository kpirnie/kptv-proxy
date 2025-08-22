package buffer

import (
	"runtime"
	"sync"
	"sync/atomic"
)

type RingBuffer struct {
	data      []byte
	size      int64
	writePos  atomic.Int64
	readPos   sync.Map
	destroyed atomic.Bool
	mu        sync.RWMutex
}

type BufferPool struct {
	pool        sync.Pool
	bufferSize  int
	initialized atomic.Bool
}

func NewRingBuffer(size int64) *RingBuffer {
	rb := &RingBuffer{
		data: make([]byte, size),
		size: size,
	}
	rb.destroyed.Store(false)
	return rb
}

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

func (rb *RingBuffer) GetClientPosition(clientID string) int64 {
	if rb.destroyed.Load() {
		return 0
	}
	pos, _ := rb.readPos.LoadOrStore(clientID, int64(0))
	return pos.(int64)
}

func (rb *RingBuffer) UpdateClientPosition(clientID string, pos int64) {
	if rb.destroyed.Load() {
		return
	}
	rb.readPos.Store(clientID, pos)
}

func (rb *RingBuffer) RemoveClient(clientID string) {
	if rb.destroyed.Load() {
		return
	}
	rb.readPos.Delete(clientID)
}

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
	rb.readPos.Range(func(key, value interface{}) bool {
		rb.readPos.Delete(key)
		return true
	})
}

func (rb *RingBuffer) Destroy() {
	if !rb.destroyed.CompareAndSwap(false, true) {
		return
	}

	rb.mu.Lock()
	defer rb.mu.Unlock()

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

func (rb *RingBuffer) IsDestroyed() bool {
	return rb.destroyed.Load()
}

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
	for i := range buf {
		buf[i] = 0
	}
	bp.pool.Put(&buf)
}
