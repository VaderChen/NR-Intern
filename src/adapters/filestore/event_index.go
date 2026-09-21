package filestore

import (
	"AgenticService/src/domain"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// 索引只存已驗證事件的結尾位移，不快取本文；每筆額外 8 bytes。
// 檔案身分、長度或修改時間改變就重建，仍保留原本損壞隔離與尾筆修復。
type eventOffsetIndex struct {
	info os.FileInfo
	ends []int64
}

// 呼叫端持有該 Run 的 runLock，因此索引不會在讀取期間被 Append 修改。
func (r *RunEventRepository) validEventIndex(runID, path string) *eventOffsetIndex {
	r.mu.Lock()
	index := r.indexes[runID]
	r.mu.Unlock()
	if index == nil || index.info == nil {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil || !os.SameFile(index.info, info) || info.Size() != index.info.Size() || !info.ModTime().Equal(index.info.ModTime()) {
		return nil
	}
	return index
}

func readIndexedEvents(ctx context.Context, path, runID string, after int64, index *eventOffsetIndex) ([]domain.Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if after < 0 {
		after = 0
	}
	last := int64(len(index.ends) - 1)
	values := []domain.Event{}
	if after >= last {
		return values, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if _, err := file.Seek(index.ends[after], io.SeekStart); err != nil {
		return nil, err
	}
	reader := bufio.NewReader(file)
	for sequence := after + 1; sequence <= last; {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return nil, fmt.Errorf("read indexed run event: %w", err)
		}
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var event domain.Event
		if err := json.Unmarshal(line, &event); err != nil {
			return nil, err
		}
		if event.RunID != runID || event.Sequence != sequence || event.ID == "" || event.Type == "" {
			return nil, fmt.Errorf("indexed event identity or sequence changed")
		}
		values = append(values, event)
		sequence++
	}
	return values, nil
}
