package protocol

import (
	"bytes"
	"math"
	"sync"
)

var buffers = NewBufferPool()

// BufferPool holds different size of buffers and returns the best match size
type BufferPool struct {
	poolMap map[int]*sync.Pool

	mu sync.Mutex
}

func NewBufferPool() *BufferPool {
	return &BufferPool{
		poolMap: make(map[int]*sync.Pool),
	}
}

func (bp *BufferPool) Get(size int) *bytes.Buffer {
	bp.mu.Lock()
	defer bp.mu.Unlock()

	var lastDiff int = math.MaxInt
	var nearestSize int
	for bufSize := range bp.poolMap {
		if bufSize >= size && (bufSize-size) < lastDiff {
			lastDiff = bufSize - size
			nearestSize = bufSize
		}
	}

	if nearestSize == 0 {
		pool := &sync.Pool{
			New: func() interface{} {
				return bytes.NewBuffer(make([]byte, 0, size))
			},
		}
		bp.poolMap[size] = pool
		return pool.Get().(*bytes.Buffer)
	}

	return bp.poolMap[nearestSize].Get().(*bytes.Buffer)
}

func (bp *BufferPool) Put(buf *bytes.Buffer) {
	bp.mu.Lock()
	defer bp.mu.Unlock()

	pool, found := bp.poolMap[buf.Cap()]
	if found {
		buf.Reset()
		pool.Put(buf)
	}
}
