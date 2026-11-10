package filestore

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
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
// 而重建只需要掃一遍 header。最近使用的索引由有界 LRU 保留；遭淘汰或
// 檔案身分、大小、修改時間不同時才重建。
type entryIndex struct {
	records []entryIndexRecord
	size    int64
	info    os.FileInfo
}

// entryIndexFor 取得（必要時建立）指定 session 的索引。
//
// 呼叫端持有 sessionLock，讀取、重建與追加不會交錯；磁碟 I/O 不持有全域 mu。
func (r *SessionRepository) entryIndexFor(ctx context.Context, sessionID, path string) (*entryIndex, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := transcriptInfo(path)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	cached, ok := r.indexes.get(sessionID)
	r.mu.Unlock()
	if ok && sameTranscript(cached.info, info) {
		return cached, nil
	}
	built, err := buildEntryIndex(ctx, path)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.indexes.set(sessionID, built)
	r.mu.Unlock()
	return built, nil
}

// noteAppendedEntry 在 append 之後就地延長索引，省下一次重建。
//
// 呼叫端持有該 session 的寫入鎖，因此 size 一定是連續的；若對不上（外部改動過
// 檔案）就丟掉索引，下次讀取自然會重建。
func (r *SessionRepository) noteAppendedEntry(sessionID string, sequence int64, entryType string, before, after os.FileInfo, written, separator int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	index, ok := r.indexes.get(sessionID)
	if !ok {
		return
	}
	offset := before.Size()
	previousMatches := sameTranscript(index.info, before) || (index.info == nil && offset == 0)
	if !previousMatches || after == nil || !os.SameFile(before, after) || after.Size() != offset+int64(written+separator) {
		r.indexes.delete(sessionID)
		return
	}
	index.records = append(index.records, entryIndexRecord{sequence: sequence, entryType: entryType, offset: offset + int64(separator)})
	index.size = after.Size()
	index.info = after
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

// offsetOfRecentType 回傳「倒數第 count 個指定型別」的位移，
// 以及這個範圍是否已經涵蓋整份 transcript。
//
// 用途是「只要最近幾則訊息」的查詢：從這個位移正向讀到結尾就夠，
// 不必把整份 transcript 解碼一遍。complete 為真代表範圍就是全部，
// 呼叫端可以據此判斷結果與完整掃描等價。
func (index *entryIndex) offsetOfRecentType(entryType string, count int) (offset int64, complete bool) {
	if count <= 0 || len(index.records) == 0 {
		return index.size, len(index.records) == 0
	}
	seen := 0
	for position := len(index.records) - 1; position >= 0; position-- {
		if index.records[position].entryType != entryType {
			continue
		}
		seen++
		if seen < count {
			continue
		}
		// 從這一筆開始讀。它之前若還有同型別的紀錄，代表範圍不完整。
		for earlier := position - 1; earlier >= 0; earlier-- {
			if index.records[earlier].entryType == entryType {
				return index.records[position].offset, false
			}
		}
		return index.records[position].offset, true
	}
	// 同型別的紀錄不足 count 筆，整份都要讀。
	return 0, true
}

// lastSequence 回傳目前最大的序號，讓 append 不必為了取號重讀整個檔案。
func (index *entryIndex) lastSequence() int64 {
	if len(index.records) == 0 {
		return 0
	}
	return index.records[len(index.records)-1].sequence
}

func transcriptInfo(path string) (os.FileInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("stat session transcript: %w", err)
	}
	return info, nil
}

func sameTranscript(left, right os.FileInfo) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return os.SameFile(left, right) && left.Size() == right.Size() && left.ModTime().Equal(right.ModTime())
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
	before, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat opened session transcript: %w", err)
	}
	var tail []byte
	if err := readJSONLinesAt(ctx, file, 0, func(offset int64, line []byte) error {
		header, err := decodeEntryHeader(line)
		if err != nil {
			return fmt.Errorf("decode session entry header: %w", err)
		}
		index.records = append(index.records, entryIndexRecord{sequence: header.Sequence, entryType: header.Type, offset: offset})
		tail = line
		return nil
	}); err != nil {
		return nil, fmt.Errorf("index session transcript: %w", err)
	}
	// Header 可以完整但本文尚未寫完；不可在未知尾筆後繼續派送或重用其序號。
	if len(tail) > 0 && !json.Valid(tail) {
		return nil, fmt.Errorf("session transcript has an incomplete final entry")
	}
	after, err := file.Stat()
	if err != nil {
		return nil, err
	}
	current, err := transcriptInfo(path)
	if err != nil {
		return nil, err
	}
	if !sameTranscript(before, after) || !sameTranscript(after, current) {
		return nil, fmt.Errorf("session transcript changed while indexing")
	}
	index.size = after.Size()
	index.info = after
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
