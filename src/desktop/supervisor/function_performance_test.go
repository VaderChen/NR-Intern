package supervisor

import (
	"bytes"
	"testing"
)

func BenchmarkFunctionLogBuffer(b *testing.B) {
	for _, config := range []struct {
		name  string
		chunk []byte
	}{
		{"SmallWrites", bytes.Repeat([]byte("synthetic log record\n"), 32)},
		{"OversizedWrite", bytes.Repeat([]byte("synthetic log record\n"), 1<<19)},
	} {
		b.Run(config.name, func(b *testing.B) {
			buffer := NewLogBuffer(512 << 10)
			_, _ = buffer.Write(bytes.Repeat([]byte("x"), 512<<10))
			b.ReportAllocs()
			for b.Loop() {
				_, _ = buffer.Write(config.chunk)
			}
		})
	}
	b.Run("String", func(b *testing.B) {
		buffer := NewLogBuffer(512 << 10)
		_, _ = buffer.Write(bytes.Repeat([]byte("x"), 512<<10))
		b.ReportAllocs()
		for b.Loop() {
			if len(buffer.String()) != 512<<10 {
				b.Fatal("日誌長度改變")
			}
		}
	})
}
