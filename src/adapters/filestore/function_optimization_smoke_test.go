package filestore

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"AgenticService/src/domain"
)

func TestMemoryDeltaRollbackSmoke(t *testing.T) {
	for _, operation := range []string{"merge", "supersede", "forget", "purge", "miskeyed-forget", "miskeyed-supersede"} {
		t.Run(operation, func(t *testing.T) {
			root := t.TempDir()
			r, err := NewMemoryRepository(root)
			if err != nil {
				t.Fatal(err)
			}
			for i := range 5 {
				id := fmt.Sprintf("synthetic_%d", i)
				r.items[id] = domain.Memory{ID: id, Scope: "synthetic", Kind: domain.MemoryKindFact, Content: fmt.Sprintf("合成記憶 %d", i), Status: domain.MemoryStatusActive, UpdatedAt: time.Unix(1, 0), Tags: []string{"old"}, Metadata: map[string]any{"nested": map[string]any{"value": "old"}}}
			}
			if operation == "purge" {
				value := r.items["synthetic_0"]
				value.Status = domain.MemoryStatusForgotten
				r.items[value.ID] = value
			}
			if operation == "miskeyed-forget" || operation == "miskeyed-supersede" {
				value := r.items["synthetic_0"]
				value.ID = "missing_key"
				r.items["synthetic_0"] = value
			}
			if err := r.persistLocked(); err != nil {
				t.Fatal(err)
			}
			originalPath := r.filePath
			originalFile, err := os.ReadFile(originalPath)
			if err != nil {
				t.Fatal(err)
			}
			original := make(map[string]domain.Memory, len(r.items))
			for id, value := range r.items {
				original[id] = cloneMemory(value)
			}
			// 故障只影響測試暫存目錄；原檔及記憶狀態都必須保持提交前版本。
			r.filePath = filepath.Join(root, "missing", "memory.json")
			ctx := context.Background()
			switch operation {
			case "merge":
				_, err = r.Remember(ctx, domain.RememberMemoryInput{Scope: "synthetic", Kind: domain.MemoryKindFact, Content: "合成記憶 0", Tags: []string{"new"}, Confidence: 1, Metadata: map[string]any{"new": []string{"value"}}})
			case "supersede", "miskeyed-supersede":
				_, err = r.Remember(ctx, domain.RememberMemoryInput{Scope: "synthetic", Content: "全新的合成記憶", Supersedes: []string{"synthetic_0", "synthetic_1", "synthetic_1", "missing"}})
			case "forget", "miskeyed-forget":
				_, err = r.Forget(ctx, "synthetic", "synthetic_0", "合成原因")
			case "purge":
				_, err = r.PurgeInactive(ctx, time.Unix(2, 0))
			}
			if err == nil {
				t.Fatal("預期寫入失敗")
			}
			if !reflect.DeepEqual(r.items, original) {
				t.Fatal("儲存失敗後未完整回復受影響項目")
			}
			gotFile, err := os.ReadFile(originalPath)
			if err != nil || string(gotFile) != string(originalFile) {
				t.Fatal("失敗改動已提交的檔案", err)
			}
		})
	}
}

func referenceFunctionPrune(r *RunRepository) {
	if r.retention <= 0 {
		return
	}
	for _, volatile := range []bool{false, true} {
		ordered := make([]domain.Run, 0, len(r.runs))
		for _, run := range r.runs {
			if volatileRun(run) == volatile {
				ordered = append(ordered, run)
			}
		}
		if len(ordered) <= r.retention {
			continue
		}
		sort.Slice(ordered, func(i, j int) bool {
			if ordered[i].CreatedAt.Equal(ordered[j].CreatedAt) {
				return ordered[i].ID < ordered[j].ID
			}
			return ordered[i].CreatedAt.Before(ordered[j].CreatedAt)
		})
		excess := len(ordered) - r.retention
		for _, run := range ordered {
			if excess <= 0 {
				break
			}
			if !terminalRunStatus(run.Status) || r.protected[run.ID] {
				continue
			}
			delete(r.runs, run.ID)
			r.pruned = append(r.pruned, run.ID)
			excess--
		}
	}
}

func TestRunRetentionFunctionCompatibilitySmoke(t *testing.T) {
	rng := rand.New(rand.NewSource(101))
	statuses := []domain.RunStatus{domain.RunStatusCompleted, domain.RunStatusCanceled, domain.RunStatusFailed, domain.RunStatusRunning, domain.RunStatusPaused, domain.RunStatusWaitingApproval, domain.RunStatusQueued}
	for sample := range 200 {
		retention := []int{-1, 0, 1, 5, 20}[sample%5]
		got := &RunRepository{runs: map[string]domain.Run{}, protected: map[string]bool{}, retention: retention, pruned: []string{"previous"}}
		want := &RunRepository{runs: map[string]domain.Run{}, protected: got.protected, retention: retention, pruned: []string{"previous"}}
		for i := range rng.Intn(100) {
			id := fmt.Sprintf("synthetic_%03d", i)
			sessionID := "session_synthetic"
			if rng.Intn(2) == 0 {
				sessionID = "session_vabcdef_012345"
			}
			run := domain.Run{ID: id, SessionID: sessionID, Status: statuses[rng.Intn(len(statuses))], CreatedAt: time.Unix(int64(rng.Intn(5)), 0)}
			got.runs[id], want.runs[id] = run, run
			got.protected[id] = rng.Intn(4) == 0
		}
		referenceFunctionPrune(want)
		changed := got.pruneLocked()
		if !reflect.DeepEqual(got.runs, want.runs) || !reflect.DeepEqual(got.pruned, want.pruned) || changed != (len(want.pruned) > 1) {
			t.Fatalf("第 %d 組留存結果／順序不同", sample)
		}
	}
}
