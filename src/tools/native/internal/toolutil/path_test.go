package toolutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 這些測試是沙箱的唯一保證：ResolvePath 是模型能碰到的每個檔案工具的共同入口。
func newWorkspace(t *testing.T) (root string, outside string) {
	t.Helper()
	base := t.TempDir()
	root = filepath.Join(base, "workspace")
	outside = filepath.Join(base, "outside")
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := os.MkdirAll(outside, 0o750); err != nil {
		t.Fatalf("create outside directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	return root, outside
}

func TestResolvePathAcceptsPathsInsideWorkspace(t *testing.T) {
	root, _ := newWorkspace(t)
	if err := os.MkdirAll(filepath.Join(root, "nested"), 0o750); err != nil {
		t.Fatalf("create nested: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "nested", "file.txt"), []byte("hi"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	resolved, err := ResolvePath(root, "nested/file.txt", true)
	if err != nil {
		t.Fatalf("ResolvePath: %v", err)
	}
	if !strings.HasSuffix(resolved, filepath.Join("nested", "file.txt")) {
		t.Fatalf("resolved = %q, want a path inside the workspace", resolved)
	}
}

func TestResolvePathRejectsTraversal(t *testing.T) {
	root, _ := newWorkspace(t)

	for _, requested := range []string{"../outside/secret.txt", "nested/../../outside/secret.txt", "../.."} {
		if _, err := ResolvePath(root, requested, false); err == nil {
			t.Errorf("ResolvePath(%q) succeeded; want an escape error", requested)
		}
	}
}

func TestResolvePathRejectsAbsolutePathOutsideWorkspace(t *testing.T) {
	root, outside := newWorkspace(t)

	if _, err := ResolvePath(root, filepath.Join(outside, "secret.txt"), false); err == nil {
		t.Fatal("ResolvePath accepted an absolute path outside the workspace")
	}
}

// TestResolvePathRejectsSymlinkEscape 覆蓋既有 symlink：路徑字串看起來在 workspace 內，
// 實際目標卻在外面。
func TestResolvePathRejectsSymlinkEscape(t *testing.T) {
	root, outside := newWorkspace(t)
	link := filepath.Join(root, "leak.txt")
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if _, err := ResolvePath(root, "leak.txt", true); err == nil {
		t.Fatal("ResolvePath followed a symlink out of the workspace")
	}
}

// TestResolvePathRejectsWriteThroughSymlinkedParent 覆蓋尚未存在的目標檔：
// 父目錄是 symlink 時，寫入仍然會落在 workspace 之外。
func TestResolvePathRejectsWriteThroughSymlinkedParent(t *testing.T) {
	root, outside := newWorkspace(t)
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if _, err := ResolvePath(root, "escape/new-file.txt", false); err == nil {
		t.Fatal("ResolvePath allowed a write through a symlinked parent directory")
	}
}

func TestResolvePathAllowsNewFileInRealDirectory(t *testing.T) {
	root, _ := newWorkspace(t)
	if err := os.MkdirAll(filepath.Join(root, "nested"), 0o750); err != nil {
		t.Fatalf("create nested: %v", err)
	}

	resolved, err := ResolvePath(root, "nested/new-file.txt", false)
	if err != nil {
		t.Fatalf("ResolvePath: %v", err)
	}
	// ResolvePath 回傳的是解析過 symlink 的實體路徑，比較前要把 root 也解析，
	// 否則在 /var 是 symlink 的平台（macOS）上會誤判。
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	if !Within(realRoot, resolved) {
		t.Fatalf("resolved = %q, want a path inside %q", resolved, realRoot)
	}
}

func TestResolvePathRequiresExistenceWhenAsked(t *testing.T) {
	root, _ := newWorkspace(t)

	if _, err := ResolvePath(root, "missing.txt", true); err == nil {
		t.Fatal("ResolvePath accepted a missing path when existence was required")
	}
}

func TestResolvePathRequiresWorkspaceRoot(t *testing.T) {
	if _, err := ResolvePath("  ", "file.txt", false); err == nil {
		t.Fatal("ResolvePath accepted an empty workspace root")
	}
}

func TestWithinRejectsSiblingPrefix(t *testing.T) {
	if Within("/tmp/workspace", "/tmp/workspace-other/file.txt") {
		t.Fatal("Within treated a sibling directory sharing a name prefix as inside the workspace")
	}
}

// TestDisplayPathThroughSymlinkedWorkspaceRoot 覆蓋 data_dir 位於 symlink 之後的情況
// （macOS 的 /tmp、/var 都是如此）：session metadata 存的是未解析的 root，
// ResolvePath 回傳的卻是實體路徑，DisplayPath 必須自己把兩邊對齊。
func TestDisplayPathThroughSymlinkedWorkspaceRoot(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	root := filepath.Join(real, "workspace")
	if err := os.MkdirAll(filepath.Join(root, "nested"), 0o750); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "nested", "file.txt"), []byte("hi"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := os.Symlink(real, filepath.Join(base, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	// 模型與 session metadata 看到的是 symlink 形式的 root。
	linkedRoot := filepath.Join(base, "link", "workspace")

	resolved, err := ResolvePath(linkedRoot, "nested/file.txt", true)
	if err != nil {
		t.Fatalf("ResolvePath: %v", err)
	}

	if got, want := DisplayPath(linkedRoot, resolved), "nested/file.txt"; got != want {
		t.Fatalf("DisplayPath = %q, want %q", got, want)
	}
}

func TestDisplayPathFallsBackWhenRootCannotBeResolved(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")

	if got, want := DisplayPath(root, filepath.Join(root, "nested", "file.txt")), "nested/file.txt"; got != want {
		t.Fatalf("DisplayPath = %q, want %q", got, want)
	}
}

// caseInsensitiveFilesystem 探測這個目錄所在的檔案系統分不分大小寫。
// macOS 的 APFS 預設不分，Linux 的 ext4 分——測試要據此分流，不能假設。
func caseInsensitiveFilesystem(t *testing.T, directory string) bool {
	t.Helper()
	probe := filepath.Join(directory, "CaseProbe")
	if err := os.Mkdir(probe, 0o750); err != nil {
		t.Fatalf("mkdir probe: %v", err)
	}
	defer os.RemoveAll(probe)
	_, err := os.Stat(filepath.Join(directory, "caseprobe"))
	return err == nil
}

// 沙箱根是 FastChIME、工具要求 FastCHIME 時，檔案打得開卻被判定逃出沙箱。
// 「檔案存不存在」交給檔案系統（不分大小寫），「在不在沙箱內」卻是字串比對。
func TestResolvePathAcceptsDifferentCaseWhenFilesystemIsCaseInsensitive(t *testing.T) {
	base := t.TempDir()
	if !caseInsensitiveFilesystem(t, base) {
		t.Skip("檔案系統分大小寫，這個情境不會發生")
	}
	root := filepath.Join(base, "FastChIME")
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	target := filepath.Join(base, "FastCHIME", "src")

	resolved, err := ResolvePath(root, target, true)
	if err != nil {
		t.Fatalf("大小寫不同但確實在沙箱內的路徑應被接受：%v", err)
	}
	// root 可能位於 symlink 之後（macOS 的 /var），比對前要化成同一種形式。
	evaluatedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("EvalSymlinks(root): %v", err)
	}
	if !Within(evaluatedRoot, resolved) {
		t.Fatalf("回傳的路徑應落在沙箱內，得到 %q", resolved)
	}
	// 關鍵：回傳的是磁碟上的真實大小寫，不是呼叫端給的那個。
	if !strings.Contains(resolved, "FastChIME") || strings.Contains(resolved, "FastCHIME") {
		t.Fatalf("回傳的路徑應對齊為磁碟上的 FastChIME，得到 %q", resolved)
	}
}

// 對齊大小寫不能變成逃逸的後門：沙箱外的路徑照樣要擋。
func TestResolvePathStillRejectsSiblingDirectory(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "InsideRoot")
	outside := filepath.Join(base, "OutsideRoot")
	for _, directory := range []string{root, outside} {
		if err := os.MkdirAll(directory, 0o750); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	for _, requested := range []string{outside, filepath.Join(base, "outsideroot"), filepath.Join(base, "OUTSIDEROOT")} {
		if _, err := ResolvePath(root, requested, false); err == nil {
			t.Fatalf("沙箱外的路徑 %q 不該被接受", requested)
		}
	}
}

// 在分大小寫的檔案系統上逐層只會找到完全相同的名稱，結果必須不變。
func TestAlignPathCaseLeavesExactPathsUnchanged(t *testing.T) {
	base := t.TempDir()
	exact := filepath.Join(base, "Exact", "Nested")
	if err := os.MkdirAll(exact, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if got := alignPathCase(exact); got != exact {
		t.Fatalf("完全相符的路徑不該被改動：%q -> %q", exact, got)
	}
	// 尚未建立的尾段原樣保留，讓後續步驟自行處理。
	pending := filepath.Join(exact, "NotCreatedYet.txt")
	if got := alignPathCase(pending); got != pending {
		t.Fatalf("不存在的路徑段應原樣保留：%q -> %q", pending, got)
	}
}

// 多根沙箱下，「路徑不存在」不能被回報成「不在沙箱內」。
//
// 實測過的後果：Agent 拿到錯的原因，就一再改寫路徑的大小寫與前綴去修正一個
// 根本不存在的問題，每次工具呼叫都要失敗一輪才走對。
func TestResolvePathInRootsReportsTheRealReason(t *testing.T) {
	base := t.TempDir()
	first := filepath.Join(base, "first")
	second := filepath.Join(base, "second")
	for _, directory := range []string{first, second} {
		if err := os.MkdirAll(directory, 0o750); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	roots := []string{first, second}

	// 落在某個根之內、但檔案不存在：要說不存在。
	_, err := ResolvePathInRoots(roots, filepath.Join(second, "missing.txt"), true)
	if err == nil {
		t.Fatal("不存在的路徑應該失敗")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("應回報真正的原因（不存在），得到：%v", err)
	}

	// 真的在所有根之外：才說不在沙箱內。
	outside := filepath.Join(base, "outside", "file.txt")
	if err := os.MkdirAll(filepath.Dir(outside), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(outside, []byte("x"), 0o640); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err = ResolvePathInRoots(roots, outside, true)
	if err == nil {
		t.Fatal("沙箱外的路徑應該失敗")
	}
	if !strings.Contains(err.Error(), "outside the project sandbox") {
		t.Fatalf("真正在沙箱外時才該這樣說，得到：%v", err)
	}
}
