package toolutil

import (
	"errors"
	"fmt"
	"strings"
	"syscall"
	"testing"
)

// 空間不足時作業系統只回 "no space left on device"。在記憶體隔離專案裡，
// 使用者看到那句話不會聯想到「RAM SIZE 要調大」，只會以為工具壞了。
func TestDescribeWriteErrorExplainsFullRAMDisk(t *testing.T) {
	err := DescribeWriteError("/Volumes/NRIntern-RAM-6886-c38c5d52/out.txt",
		fmt.Errorf("write file: %w", syscall.ENOSPC))
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, expected := range []string{"RAM Disk 已滿", "建立專案時固定", "RAM SIZE"} {
		if !strings.Contains(err.Error(), expected) {
			t.Fatalf("訊息應說明如何處理，實際為：%s", err)
		}
	}
	// 原始錯誤要能被 errors.Is 找到，稽核與上層判斷才不會因為包裝而失效。
	if !errors.Is(err, syscall.ENOSPC) {
		t.Fatal("包裝後仍必須保留 ENOSPC")
	}
}

// 一般磁碟也該有看得懂的說明，但不能亂講成 RAM Disk。
func TestDescribeWriteErrorHandlesOrdinaryDisk(t *testing.T) {
	err := DescribeWriteError("/home/agent/proj/out.txt", syscall.ENOSPC)
	if !strings.Contains(err.Error(), "磁碟空間不足") {
		t.Fatalf("一般路徑應回報磁碟空間不足，實際為：%s", err)
	}
	if strings.Contains(err.Error(), "RAM Disk") {
		t.Fatalf("一般路徑不該被說成 RAM Disk：%s", err)
	}
}

// 其他錯誤必須原樣傳遞，不能被這層改寫掩蓋真正的原因。
func TestDescribeWriteErrorPassesOtherErrorsThrough(t *testing.T) {
	original := syscall.EACCES
	if got := DescribeWriteError("/Volumes/NRIntern-RAM-1/out.txt", original); !errors.Is(got, original) {
		t.Fatalf("非空間不足的錯誤應原樣傳遞，實際為：%v", got)
	}
	if DescribeWriteError("/x", nil) != nil {
		t.Fatal("nil 應維持 nil")
	}
}
