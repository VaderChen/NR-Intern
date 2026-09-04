package toolutil

import (
	"errors"
	"fmt"
	"strings"
	"syscall"
)

// ramDiskNameMarker 與 bootstrap 的 RAM disk 命名前綴一致。
//
// 這裡刻意用字串比對而不是 import bootstrap：toolutil 屬於工具內部套件，
// 反向依賴啟動流程會造成循環，而且工具本來就只看得到路徑。
const ramDiskNameMarker = "NRIntern-RAM-"

// DescribeWriteError 把寫入失敗轉成使用者看得懂的說明。
//
// 空間不足時作業系統只回「no space left on device」。在記憶體隔離專案裡，
// 那個裝置是專案自己的 RAM Disk，容量在建立時就固定；使用者看到原始訊息
// 完全不會聯想到「要把 RAM SIZE 調大」，只會以為工具壞了。
func DescribeWriteError(path string, err error) error {
	if err == nil {
		return nil
	}
	if !errors.Is(err, syscall.ENOSPC) {
		return err
	}
	if strings.Contains(path, ramDiskNameMarker) {
		return fmt.Errorf("寫入失敗：這個記憶體隔離專案的 RAM Disk 已滿。"+
			"RAM Disk 容量在建立專案時固定，無法即時擴充；請清理不需要的檔案，"+
			"或以較大的 RAM SIZE 另建一個專案。原始錯誤：%w", err)
	}
	return fmt.Errorf("寫入失敗：磁碟空間不足。原始錯誤：%w", err)
}
