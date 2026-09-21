package bootstrap

import (
	"AgenticService/src/domain"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
)

// 以實際寫入量限制總解壓大小，不能只信任掃描時的 Size（來源檔可能持續增長）。
type backupCopyBudget struct {
	ctx       context.Context
	remaining int64
}

func (b *backupCopyBudget) copyFile(dst io.Writer, path string, expected os.FileInfo) error {
	if err := b.ctx.Err(); err != nil {
		return err
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err == nil && (!info.Mode().IsRegular() || !os.SameFile(expected, info)) {
		err = fmt.Errorf("backup source changed during inspection")
	}
	if err == nil {
		_, err = io.Copy(backupBudgetWriter{dst: dst, budget: b}, file)
	}
	return errors.Join(err, file.Close())
}

type backupBudgetWriter struct {
	dst    io.Writer
	budget *backupCopyBudget
}

func (w backupBudgetWriter) Write(p []byte) (int, error) {
	if err := w.budget.ctx.Err(); err != nil {
		return 0, err
	}
	if int64(len(p)) > w.budget.remaining {
		return 0, backupSizeLimitError()
	}
	n, err := w.dst.Write(p)
	w.budget.remaining -= int64(n)
	return n, err
}

func backupSizeLimitError() error {
	return fmt.Errorf("%w: 備份資料超過還原上限（%d MiB），已停止且未產生部分備份", domain.ErrConflict, maxRestoreExpandedBytes/(1024*1024))
}
