package filestore

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
)

// entryIndexRecord 是 transcript 裡一筆 entry 的位置。
//
// 只留定位需要的三個欄位：序號用來二分搜尋，型別讓 LatestEntryOfType 不必碰磁碟，
// 位移讓讀取可以直接 seek 過去。內容一律不進索引——工具輸出佔了 transcript 絕大
// 部分體積，把它們留在記憶體等於用一個問題換另一個問題。
type entryIndexRecord struct {
	sequence  int64
	entryType string
	offset    int64
}

// entryIndex 是單一 session transcript 的位置索引。
//
// 只存在於記憶體，不落地。落地要處理版本、損毀與跟 transcript 不同步三種狀況，
// 而重建一次只要掃一遍 header（21 MB 的 transcript 約 11 ms），一個程序週期內
// 每個 session 也只付一次。用 size 對照檔案大小判斷是否仍然有效。
type entryIndex struct {
	records []entryIndexRecord
	size    int64
}

// entryIndexFor 取得（必要時建立）指定 session 的索引。
//
// 建立索引要讀檔，因此不在鎖裡做：先在鎖外掃描，再回頭確認期間沒有別人先建好。
func (r *SessionRepository) entryIndexFor(ctx context.Context, sessionID, path string) (*entryIndex, error) {
	size, err := transcriptSize(path)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	cached, ok := r.indexes[sessionID]
	r.mu.Unlock()
	if ok && cached.size == size {
		return cached, nil
	}
	built, err := buildEntryIndex(ctx, path)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	// 重新檢查：掃描期間可能有 append 讓索引更新到更後面，那份才是對的。
	if current, exists := r.indexes[sessionID]; exists && current.size >= built.size {
		r.mu.Unlock()
		return current, nil
	}
	r.indexes[sessionID] = built
	r.mu.Unlock()
	return built, nil
}

// noteAppendedEntry 在 append 之後就地延長索引，省下一次重建。
//
// 呼叫端持有該 session 的寫入鎖，因此 size 一定是連續的；若對不上（外部改動過
// 檔案）就丟掉索引，下次讀取自然會重建。
func (r *SessionRepository) noteAppendedEntry(sessionID string, sequence int64, entryType string, offset int64, written int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	index, ok := r.indexes[sessionID]
	if !ok {
		return
	}
	if index.size != offset {
		delete(r.indexes, sessionID)
		return
	}
	index.records = append(index.records, entryIndexRecord{sequence: sequence, entryType: entryType, offset: offset})
	index.size = offset + int64(written)
}

// offsetAfter 回傳「序號大於 afterSequence 的第一筆」的位移，以及其後還有幾筆。
//
// 筆數直接由索引長度得到，不必讀檔——分頁的 has_more 因此不需要多掃一次。
func (index *entryIndex) offsetAfter(afterSequence int64) (offset int64, remaining int) {
	position := sort.Search(len(index.records), func(i int) bool {
		return index.records[i].sequence > afterSequence
	})
	if position >= len(index.records) {
		return index.size, 0
	}
	return index.records[position].offset, len(index.records) - position
}

// latestOffsetOfType 回傳指定型別中序號最大的那一筆的位移。
func (index *entryIndex) latestOffsetOfType(entryType string) (int64, bool) {
	for position := len(index.records) - 1; position >= 0; position-- {
		if index.records[position].entryType == entryType {
			return index.records[position].offset, true
		}
	}
	return 0, false
}

// lastSequence 回傳目前最大的序號，讓 append 不必為了取號重讀整個檔案。
func (index *entryIndex) lastSequence() int64 {
	if len(index.records) == 0 {
		return 0
	}
	return index.records[len(index.records)-1].sequence
}

func transcriptSize(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("stat session transcript: %w", err)
	}
	return info.Size(), nil
}

// buildEntryIndex 掃一遍 transcript 建立位置索引，只解析每行的 header。
func buildEntryIndex(ctx context.Context, path string) (*entryIndex, error) {
	index := &entryIndex{}
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return index, nil
		}
		return nil, fmt.Errorf("open session transcript: %w", err)
	}
	defer file.Close()
	if err := readJSONLinesAt(ctx, file, 0, func(offset int64, line []byte) error {
		header, err := decodeEntryHeader(line)
		if err != nil {
			return fmt.Errorf("decode session entry header: %w", err)
		}
		index.records = append(index.records, entryIndexRecord{sequence: header.Sequence, entryType: header.Type, offset: offset})
		return nil
	}); err != nil {
		return nil, fmt.Errorf("index session transcript: %w", err)
	}
	size, err := transcriptSize(path)
	if err != nil {
		return nil, err
	}
	index.size = size
	// append-only 的檔案本來就是序號遞增，但索引的二分搜尋依賴這件事，
	// 因此明確確認一次；不成立就當索引無效，退回全檔掃描。
	for position := 1; position < len(index.records); position++ {
		if index.records[position].sequence <= index.records[position-1].sequence {
			return nil, errUnsortedTranscript
		}
	}
	return index, nil
}

// errUnsortedTranscript 表示 transcript 的序號不是遞增的，索引無法使用。
var errUnsortedTranscript = errors.New("session transcript sequences are not ascending")

// readJSONLinesAt 從指定位移開始逐行讀取，並回報每行的起始位移。
func readJSONLinesAt(ctx context.Context, file *os.File, start int64, visit func(offset int64, line []byte) error) error {
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return fmt.Errorf("seek session transcript: %w", err)
	}
	buffered := bufio.NewReaderSize(file, 64*1024)
	offset := start
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		line, err := buffered.ReadBytes('\n')
		consumed := int64(len(line))
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) > 0 {
			if visitErr := visit(offset, trimmed); visitErr != nil {
				return visitErr
			}
		}
		offset += consumed
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
