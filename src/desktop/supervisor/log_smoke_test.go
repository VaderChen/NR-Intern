package supervisor

import (
	"bytes"
	"math/rand"
	"strings"
	"sync"
	"testing"
)

func referenceLogWrite(previous, value []byte, capacity int) []byte {
	result := append(append([]byte(nil), previous...), value...)
	if len(result) > capacity {
		cut := len(result) - capacity
		if newline := bytes.IndexByte(result[cut:], '\n'); newline >= 0 {
			cut += newline + 1
		}
		result = append([]byte(nil), result[cut:]...)
	}
	return result
}

func TestLogBufferCompatibilitySmoke(t *testing.T) {
	rng := rand.New(rand.NewSource(93))
	for _, capacity := range []int{1, 2, 17, 1024} {
		buffer := NewLogBuffer(capacity)
		var reference []byte
		chunks := [][]byte{nil, []byte("\n"), bytes.Repeat([]byte("x"), capacity), []byte("甲乙\n"), bytes.Repeat([]byte("y"), capacity*8), []byte("\nend")}
		for range 500 {
			chunk := make([]byte, rng.Intn(capacity*3+1))
			for i := range chunk {
				chunk[i] = byte(rng.Intn(256))
			}
			chunks = append(chunks, chunk)
		}
		for index, chunk := range chunks {
			previous := buffer.String()
			before := string(reference)
			count, err := buffer.Write(chunk)
			reference = referenceLogWrite(reference, chunk, capacity)
			if count != len(chunk) || err != nil {
				t.Fatalf("寫入長度錯誤：%d %v", count, err)
			}
			if got := buffer.String(); got != string(reference) {
				t.Fatalf("容量=%d 第%d段結果不同：%q != %q", capacity, index, got, reference)
			}
			if previous != before {
				t.Fatal("後續寫入修改已回傳的字串")
			}
			if cap(buffer.data) > capacity || buffer.size > capacity {
				t.Fatal("配置超過日誌容量")
			}
		}
	}
	buffer := NewLogBuffer(32)
	_, _ = buffer.Write([]byte(strings.Repeat("z", 1<<20)))
	if cap(buffer.data) > 32 || buffer.String() != strings.Repeat("z", 32) {
		t.Fatal("過大寫入未限制配置")
	}
	source := []byte("source")
	buffer = NewLogBuffer(32)
	_, _ = buffer.Write(source)
	source[0] = 'X'
	if buffer.String() != "source" {
		t.Fatal("緩衝共用呼叫端輸入")
	}
}

func TestLogBufferConcurrentSmoke(t *testing.T) {
	buffer := NewLogBuffer(1024)
	var group sync.WaitGroup
	for range 8 {
		group.Go(func() {
			for range 200 {
				_, _ = buffer.Write([]byte("合成並行日誌\n"))
				if len(buffer.String()) > 1024 {
					t.Error("並行緩衝超過上限")
				}
			}
		})
	}
	group.Wait()
}
