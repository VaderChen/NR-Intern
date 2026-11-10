package supervisor

import (
	"bytes"
	"strings"
	"sync"
)

type LogBuffer struct {
	mu       sync.Mutex
	data     []byte
	maxBytes int
	start    int
	size     int
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
	written := len(value)
	if written == 0 || b.maxBytes <= 0 {
		return written, nil
	}
	overflow := b.size > b.maxBytes-written
	if written >= b.maxBytes {
		// 過大的單次寫入也只複製最後的有效尾段，不先配置整份輸入。
		value = value[written-b.maxBytes:]
		if overflow {
			if newline := bytes.IndexByte(value, '\n'); newline >= 0 {
				value = value[newline+1:]
			}
		}
		b.start, b.size = 0, 0
	} else if overflow {
		b.discard(b.size - (b.maxBytes - written))
	}
	b.grow(b.size + len(value))
	if len(value) > 0 {
		end := (b.start + b.size) % len(b.data)
		copied := copy(b.data[end:], value)
		copy(b.data, value[copied:])
		b.size += len(value)
	}
	if overflow && written < b.maxBytes {
		// 保留舊有規則：超出容量時，截至保留尾段中的第一個換行之後。
		first := min(b.size, len(b.data)-b.start)
		if newline := bytes.IndexByte(b.data[b.start:b.start+first], '\n'); newline >= 0 {
			b.discard(newline + 1)
		} else if newline := bytes.IndexByte(b.data[:b.size-first], '\n'); newline >= 0 {
			b.discard(first + newline + 1)
		}
	}
	return written, nil
}

// grow 與 discard 只在持有 mu 時呼叫；環狀緩衝容量不超過 maxBytes。
func (b *LogBuffer) grow(needed int) {
	if needed <= len(b.data) {
		return
	}
	capacity := b.maxBytes
	if len(b.data) <= b.maxBytes/2 {
		capacity = min(b.maxBytes, max(needed, max(256, len(b.data)*2)))
	}
	data := make([]byte, capacity)
	first := min(b.size, len(b.data)-b.start)
	copy(data, b.data[b.start:b.start+first])
	copy(data[first:], b.data[:b.size-first])
	b.data, b.start = data, 0
}

func (b *LogBuffer) discard(count int) {
	b.start = (b.start + count) % len(b.data)
	b.size -= count
	if b.size == 0 {
		b.start = 0
	}
}

func (b *LogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var result strings.Builder
	result.Grow(b.size)
	first := min(b.size, len(b.data)-b.start)
	result.Write(b.data[b.start : b.start+first])
	result.Write(b.data[:b.size-first])
	return result.String()
}
