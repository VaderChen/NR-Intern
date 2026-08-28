package toolutil

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ResolvePath 將模型提供的路徑限制在單一 Sandbox 根目錄，並防止既有 symlink 逃逸。
func ResolvePath(workspaceRoot, requested string, mustExist bool) (string, error) {
	workspaceRoot = strings.TrimSpace(workspaceRoot)
	if workspaceRoot == "" {
		return "", fmt.Errorf("workspace root is required")
	}
	root, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return "", fmt.Errorf("resolve workspace root: %w", err)
	}
	root = filepath.Clean(root)
	if evaluated, evaluateErr := filepath.EvalSymlinks(root); evaluateErr == nil {
		root = evaluated
	}
	requested = strings.TrimSpace(requested)
	if requested == "" {
		requested = "."
	}
	candidate := requested
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, candidate)
	}
	candidate, err = filepath.Abs(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}
	candidate = filepath.Clean(candidate)
	if !Within(root, candidate) {
		return "", fmt.Errorf("path escapes the sandbox")
	}
	resolved, exists, err := resolveExistingPath(candidate)
	if err != nil {
		return "", err
	}
	if mustExist && !exists {
		return "", fmt.Errorf("path does not exist: %s", requested)
	}
	candidate = resolved
	if !Within(root, candidate) {
		return "", fmt.Errorf("symlink target escapes the sandbox")
	}
	return candidate, nil
}

// ResolvePathInRoots 將絕對路徑限制在任一 Sandbox 根目錄；相對路徑固定以第一個根目錄為基準。
func ResolvePathInRoots(workspaceRoots []string, requested string, mustExist bool) (string, error) {
	roots := make([]string, 0, len(workspaceRoots))
	for _, root := range workspaceRoots {
		if root = strings.TrimSpace(root); root != "" {
			roots = append(roots, root)
		}
	}
	if len(roots) == 0 {
		return "", fmt.Errorf("sandbox roots are required")
	}
	requested = strings.TrimSpace(requested)
	if requested == "" || !filepath.IsAbs(requested) {
		return ResolvePath(roots[0], requested, mustExist)
	}
	for _, root := range roots {
		path, err := ResolvePath(root, requested, mustExist)
		if err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("path is outside the project sandbox")
}

// resolveExistingPath 會解析最深的既有父路徑，再接回尚未建立的部分。
// 如此即使目標檔尚不存在，也不能透過既有父層 symlink 逃出 workspace。
func resolveExistingPath(candidate string) (string, bool, error) {
	current := filepath.Clean(candidate)
	remainder := []string{}
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			for index := len(remainder) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, remainder[index])
			}
			return filepath.Clean(resolved), len(remainder) == 0, nil
		}
		if !os.IsNotExist(err) {
			return "", false, fmt.Errorf("resolve symlink: %w", err)
		}
		if info, statErr := os.Lstat(current); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
			return "", false, fmt.Errorf("path contains an unresolved symlink: %s", current)
		} else if statErr != nil && !os.IsNotExist(statErr) {
			return "", false, fmt.Errorf("inspect path: %w", statErr)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", false, fmt.Errorf("cannot resolve an existing parent for path")
		}
		remainder = append(remainder, filepath.Base(current))
		current = parent
	}
}

func Within(root, candidate string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

// DisplayPath 會先把 workspace root 解析成實體路徑，才與 ResolvePath 回傳的路徑相減。
// 兩邊必須是同一種形式，否則在 data_dir 位於 symlink 之後的平台（macOS 的
// /tmp -> /private/tmp、/var -> /private/var）會算出一長串 ../.. 前綴。
func DisplayPath(workspaceRoot, path string) string {
	root := workspaceRoot
	if evaluated, err := filepath.EvalSymlinks(root); err == nil {
		root = evaluated
	}
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(relative)
}

// DisplayPathInRoots 在單一根目錄時維持相對路徑；多根目錄時回傳絕對路徑，避免同名資料夾造成歧義。
func DisplayPathInRoots(workspaceRoots []string, path string) string {
	if len(workspaceRoots) == 1 {
		return DisplayPath(workspaceRoots[0], path)
	}
	return filepath.ToSlash(path)
}
