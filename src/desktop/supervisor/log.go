package supervisor

import (
	"bytes"
	"sync"
)

type LogBuffer struct {
	mu       sync.Mutex
	data     []byte
	maxBytes int
}

func NewLogBuffer(maxBytes int) *LogBuffer {
	if maxBytes <= 0 {
		maxBytes = 512 * 1024
	}
	return &LogBuffer{maxBytes: maxBytes}
}

func (b *LogBuffer) Write(value []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data = append(b.data, value...)
	if len(b.data) > b.maxBytes {
		cut := len(b.data) - b.maxBytes
		if newline := bytes.IndexByte(b.data[cut:], '\n'); newline >= 0 {
			cut += newline + 1
		}
		b.data = append([]byte(nil), b.data[cut:]...)
	}
	return len(value), nil
}

func (b *LogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(append([]byte(nil), b.data...))
}
