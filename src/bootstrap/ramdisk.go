package bootstrap

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	DefaultRAMDiskSizeMB = 512
	minRAMDiskSizeMB     = 256
	maxRAMDiskSizeMB     = 1024 * 1024

	ramDiskNamePrefix  = "NRIntern-RAM-"
	ramDiskMarkerName  = ".nr-intern-ramdisk.json"
	ramDiskMarkerMagic = "nr-intern-ramdisk-v1"
)

// RAMDiskConfig 控制 Runtime 啟動時準備的揮發性工作空間。Enabled 獨立存在，
// 是為了讓無法配置記憶體的部署環境可以明確停用，而不必用特殊容量值表達狀態。
type RAMDiskConfig struct {
	Enabled bool `json:"enabled"`
	SizeMB  int  `json:"size_mb"`
}

// RAMDisk 是跨平台揮發性工作空間的生命週期控制器。macOS、Linux 與安裝 ImDisk
// 的 Windows 都會回報 Volatile=true；未知平台才退回可清理的暫存目錄。
type RAMDisk struct {
	root       string
	mode       string
	volatile   bool
	closeFn    func(context.Context) error
	closeMutex sync.Mutex
	closed     bool
}

type platformRAMDisk struct {
	root     string
	mode     string
	volatile bool
	close    func(context.Context) error
}

type ramDiskMarker struct {
	Magic     string    `json:"magic"`
	Kind      string    `json:"kind"`
	Name      string    `json:"name"`
	OwnerID   string    `json:"owner_id,omitempty"`
	Device    string    `json:"device,omitempty"`
	PID       int       `json:"pid"`
	SizeBytes int64     `json:"size_bytes,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

func newRAMDisk(ctx context.Context, config RAMDiskConfig, ownerID string, logger *slog.Logger) (*RAMDisk, error) {
	if !config.Enabled {
		return nil, nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	sizeBytes := int64(config.SizeMB) * 1024 * 1024
	value, err := newPlatformRAMDisk(ctx, sizeBytes, strings.TrimSpace(ownerID), logger)
	if err != nil {
		return nil, err
	}
	disk := &RAMDisk{
		root:     value.root,
		mode:     value.mode,
		volatile: value.volatile,
		closeFn:  value.close,
	}
	if err := disk.prepareLayout(); err != nil {
		_ = disk.Close(ctx)
		return nil, err
	}
	return disk, nil
}

// ramDiskWorkspaceDir 是 Agent 沙箱看得到的部分；ramDiskStoreDir 放後端自己的
// 資料（transcript、計畫等）。
//
// 兩者必須分開：沙箱若直接用磁碟根目錄，Agent 就能讀寫自己的對話紀錄與計畫，
// 隔離專案的「揮發」也會變成 Agent 可以自行竄改的東西。
const (
	ramDiskWorkspaceDir = "workspace"
	ramDiskStoreDir     = "store"
)

func (disk *RAMDisk) Root() string {
	if disk == nil {
		return ""
	}
	return disk.root
}

// WorkspaceRoot 是要交給 Agent 沙箱的目錄。
func (disk *RAMDisk) WorkspaceRoot() string {
	if disk == nil || disk.root == "" {
		return ""
	}
	return filepath.Join(disk.root, ramDiskWorkspaceDir)
}

// StoreRoot 是後端資料的根，Agent 看不到。
func (disk *RAMDisk) StoreRoot() string {
	if disk == nil || disk.root == "" {
		return ""
	}
	return filepath.Join(disk.root, ramDiskStoreDir)
}

// prepareLayout 建立兩個子目錄。任一個失敗都視為磁碟不可用：
// 少了 store 會讓對話悄悄落回 dataDir，少了 workspace 則沙箱根本不存在。
func (disk *RAMDisk) prepareLayout() error {
	if disk == nil || disk.root == "" {
		return nil
	}
	for _, path := range []string{disk.WorkspaceRoot(), disk.StoreRoot()} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return fmt.Errorf("create RAM disk layout: %w", err)
		}
	}
	return nil
}

func (disk *RAMDisk) Mode() string {
	if disk == nil {
		return "disabled"
	}
	return disk.mode
}

func (disk *RAMDisk) Volatile() bool {
	return disk != nil && disk.volatile
}

// Close 只在清理成功後標示為已關閉。若卸載因仍有子程序持有檔案而失敗，呼叫端
// 還能再次嘗試；即使程序直接結束，下次啟動也會依專用標記清除殘留項目。
func (disk *RAMDisk) Close(ctx context.Context) error {
	if disk == nil {
		return nil
	}
	disk.closeMutex.Lock()
	defer disk.closeMutex.Unlock()
	if disk.closed {
		return nil
	}
	if disk.closeFn != nil {
		if err := disk.closeFn(ctx); err != nil {
			return err
		}
	}
	disk.closed = true
	return nil
}

func newManagedRAMDiskDirectory(base, kind, ownerID string, sizeBytes int64, logger *slog.Logger) (*platformRAMDisk, error) {
	if err := cleanupManagedRAMDiskDirectories(base, kind, logger); err != nil {
		return nil, err
	}
	root, err := os.MkdirTemp(base, strings.ToLower(ramDiskNamePrefix))
	if err != nil {
		return nil, fmt.Errorf("create %s workspace: %w", kind, err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		_ = os.RemoveAll(root)
		return nil, fmt.Errorf("protect %s workspace: %w", kind, err)
	}
	marker := ramDiskMarker{
		Magic:     ramDiskMarkerMagic,
		Kind:      kind,
		Name:      filepath.Base(root),
		OwnerID:   ownerID,
		PID:       os.Getpid(),
		SizeBytes: sizeBytes,
		CreatedAt: time.Now().UTC(),
	}
	if err := writeRAMDiskMarker(root, marker); err != nil {
		_ = os.RemoveAll(root)
		return nil, err
	}
	return &platformRAMDisk{
		root:     root,
		mode:     kind,
		volatile: kind == "linux-tmpfs",
		close: func(context.Context) error {
			if !isManagedRAMDiskPath(base, root) {
				return fmt.Errorf("refuse to remove unmanaged RAM disk path %q", root)
			}
			if err := os.RemoveAll(root); err != nil {
				return fmt.Errorf("remove %s workspace: %w", kind, err)
			}
			return nil
		},
	}, nil
}

func cleanupManagedRAMDiskDirectories(base, kind string, logger *slog.Logger) error {
	entries, err := os.ReadDir(base)
	if err != nil {
		return fmt.Errorf("scan stale %s workspaces: %w", kind, err)
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(strings.ToLower(entry.Name()), strings.ToLower(ramDiskNamePrefix)) {
			continue
		}
		root := filepath.Join(base, entry.Name())
		marker, err := readRAMDiskMarker(root)
		if err != nil || !validRAMDiskMarker(marker, entry.Name(), kind) {
			continue
		}
		if marker.PID > 0 && ramDiskProcessAlive(marker.PID) {
			continue
		}
		if err := os.RemoveAll(root); err != nil {
			logger.Warn("failed to remove stale RAM disk workspace", "path", root, "error", err)
		}
	}
	return nil
}

func isManagedRAMDiskPath(base, root string) bool {
	base = filepath.Clean(base)
	root = filepath.Clean(root)
	return filepath.Dir(root) == base && strings.HasPrefix(strings.ToLower(filepath.Base(root)), strings.ToLower(ramDiskNamePrefix))
}

func writeRAMDiskMarker(root string, marker ramDiskMarker) error {
	encoded, err := json.Marshal(marker)
	if err != nil {
		return fmt.Errorf("encode RAM disk marker: %w", err)
	}
	path := filepath.Join(root, ramDiskMarkerName)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create RAM disk marker: %w", err)
	}
	_, writeErr := file.Write(append(encoded, '\n'))
	closeErr := file.Close()
	if writeErr != nil {
		return fmt.Errorf("write RAM disk marker: %w", writeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close RAM disk marker: %w", closeErr)
	}
	return nil
}

func readRAMDiskMarker(root string) (ramDiskMarker, error) {
	path := filepath.Join(root, ramDiskMarkerName)
	info, err := os.Lstat(path)
	if err != nil {
		return ramDiskMarker{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > 4096 {
		return ramDiskMarker{}, fmt.Errorf("invalid RAM disk marker")
	}
	file, err := os.Open(path)
	if err != nil {
		return ramDiskMarker{}, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 4097))
	decoder.DisallowUnknownFields()
	var marker ramDiskMarker
	if err := decoder.Decode(&marker); err != nil {
		return ramDiskMarker{}, err
	}
	return marker, nil
}

func validRAMDiskMarker(marker ramDiskMarker, name, kind string) bool {
	return marker.Magic == ramDiskMarkerMagic && marker.Kind == kind && marker.Name == name
}

func newRAMDiskSuffix() (string, error) {
	value := make([]byte, 4)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("create RAM disk identifier: %w", err)
	}
	return hex.EncodeToString(value), nil
}
