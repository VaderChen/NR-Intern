package systeminfo

import "testing"

func TestTotalMemoryBytesReturnsPhysicalCapacity(t *testing.T) {
	value, err := TotalMemoryBytes()
	if err != nil {
		t.Fatalf("TotalMemoryBytes: %v", err)
	}
	if value < 256*1024*1024 {
		t.Fatalf("total memory = %d bytes", value)
	}
}
