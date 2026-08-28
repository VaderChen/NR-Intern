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
