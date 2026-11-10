package filestore

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"AgenticService/src/domain"
)

func BenchmarkFunctionMemoryMaintenance(b *testing.B) {
	for _, operation := range []string{"ForgetMissing", "ForgetAlready", "PurgeNoop"} {
		b.Run(operation, func(b *testing.B) {
			r := syntheticPerformanceMemories(10_000)
			value := r.items["synthetic-000000"]
			value.Status = domain.MemoryStatusForgotten
			r.items[value.ID] = value
			ctx := context.Background()
			b.ReportAllocs()
			for b.Loop() {
				switch operation {
				case "ForgetMissing":
					if _, err := r.Forget(ctx, "benchmark", "absent", "synthetic"); !errors.Is(err, domain.ErrNotFound) {
						b.Fatal(err)
					}
				case "ForgetAlready":
					if _, err := r.Forget(ctx, "benchmark", value.ID, "synthetic"); err != nil {
						b.Fatal(err)
					}
				case "PurgeNoop":
					if count, err := r.PurgeInactive(ctx, time.Unix(-1, 0)); err != nil || count != 0 {
						b.Fatalf("移除數量=%d err=%v", count, err)
					}
				}
			}
		})
	}
}

func BenchmarkFunctionRunRetention(b *testing.B) {
	for _, count := range []int{500, 1000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			r := &RunRepository{retention: 500, runs: make(map[string]domain.Run, count)}
			for i := range count {
				id := fmt.Sprintf("synthetic_%04d", i)
				r.runs[id] = domain.Run{ID: id, SessionID: "session_synthetic", Status: domain.RunStatusRunning, CreatedAt: time.Unix(int64(i), 0)}
			}
			b.ReportAllocs()
			for b.Loop() {
				if r.pruneLocked() {
					b.Fatal("執行中的 Run 不應被淘汰")
				}
			}
		})
	}
}

func BenchmarkFunctionRunPruneOverflow(b *testing.B) {
	for _, reference := range []bool{true, false} {
		name := "Current"
		if reference {
			name = "Reference"
		}
		b.Run(name, func(b *testing.B) {
			r := &RunRepository{retention: 500, runs: make(map[string]domain.Run, 501)}
			for i := range 501 {
				id := fmt.Sprintf("synthetic_%04d", i)
				status := domain.RunStatusCompleted
				if i%4 == 3 {
					status = domain.RunStatusRunning
				}
				r.runs[id] = domain.Run{ID: id, SessionID: "session_synthetic", Status: status, CreatedAt: time.Unix(int64(i), 0)}
			}
			oldest := r.runs["synthetic_0000"]
			b.ReportAllocs()
			for b.Loop() {
				if reference {
					referenceFunctionPrune(r)
				} else {
					r.pruneLocked()
				}
				if len(r.pruned) != 1 || r.pruned[0] != oldest.ID {
					b.Fatal("淘汰結果不同")
				}
				r.runs[oldest.ID] = oldest
				r.pruned = r.pruned[:0]
			}
		})
	}
}
