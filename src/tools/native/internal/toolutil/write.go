package toolutil

import (
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// 有界的程序內路徑鎖，跨 Session 共用；不宣稱能鎖住外部編輯器或 Shell。
var fileWriteLocks [128]sync.Mutex

func fileWriteLock(path string) *sync.Mutex {
	if absolute, err := filepath.Abs(path); err == nil {
		path = absolute
	}
	if resolved, _, err := resolveExistingPath(path); err == nil {
		path = resolved
	}
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(alignPathCase(path)))
	return &fileWriteLocks[hash.Sum32()%uint32(len(fileWriteLocks))]
}

// AtomicWriteFile 在同一目錄建立暫存檔、sync 後原子替換目標。
func AtomicWriteFile(path string, data []byte, mode os.FileMode, overwrite bool) error {
	lock := fileWriteLock(path)
	lock.Lock()
	defer lock.Unlock()
	return atomicWriteFile(path, data, mode, overwrite)
}

// AtomicUpdateFile 將讀取、前置條件、轉換及寫回放在同一個程序內交易。
func AtomicUpdateFile(path string, limit int, transform func([]byte) ([]byte, error)) ([]byte, []byte, error) {
	lock := fileWriteLock(path)
	lock.Lock()
	defer lock.Unlock()
	info, err := os.Stat(path)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("path is not a regular file")
	}
	if limit < 0 || info.Size() > int64(limit) {
		return nil, nil, fmt.Errorf("file exceeds %d bytes", limit)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("path is not a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil {
		return nil, nil, err
	}
	if len(data) > limit {
		return nil, nil, fmt.Errorf("file exceeds %d bytes", limit)
	}
	if err := file.Close(); err != nil {
		return nil, nil, err
	}
	updated, err := transform(data)
	if err != nil {
		return nil, nil, err
	}
	if len(updated) > limit {
		return nil, nil, fmt.Errorf("edited file would exceed %d bytes", limit)
	}
	if err := atomicWriteFile(path, updated, info.Mode().Perm(), true); err != nil {
		return nil, nil, err
	}
	return data, updated, nil
}

func atomicWriteFile(path string, data []byte, mode os.FileMode, overwrite bool) error {
	if mode == 0 {
		mode = 0o640
	}

	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, "."+filepath.Base(path)+"-*")
	if err != nil {
		// 暫存檔就建不出來時，最常見的原因正是空間不足。
		return DescribeWriteError(path, fmt.Errorf("create temporary file: %w", err))
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	cleanup := func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}
	if err := temporary.Chmod(mode); err != nil {
		cleanup()
		return fmt.Errorf("set temporary file mode: %w", err)
	}
	if err := writeAndSync(temporary, data); err != nil {
		cleanup()
		return DescribeWriteError(path, err)
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return fmt.Errorf("close temporary file: %w", err)
	}
	publish := replacePath
	if !overwrite {
		publish = publishNewPath
	}
	if err := publish(temporaryPath, path); err != nil {
		_ = os.Remove(temporaryPath)
		return fmt.Errorf("replace file: %w", err)
	}
	return nil
}

func writeAndSync(file *os.File, data []byte) error {
	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("write file: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync file: %w", err)
	}
	return nil
}
