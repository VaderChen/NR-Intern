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
		// 這道檢查是純字串比對，會有兩種假警報：呼叫端給的大小寫與磁碟不同
		// （macOS 的 APFS 預設不分大小寫），或路徑位於 symlink 之後而 root
		// 已經被 EvalSymlinks 解析過（macOS 的 /tmp、/var）。兩者都是「真的在
		// 沙箱裡卻被判定逃逸」，所以先把候選路徑化成與 root 同一種形式再比一次。
		//
		// 只在失敗時做：解析與逐層對齊都要碰檔案系統，放在正常路徑上太貴。
		// 重比仍然用 Within，真正的逃逸照樣擋得住。
		aligned := candidate
		if resolvedCandidate, _, resolveErr := resolveExistingPath(candidate); resolveErr == nil {
			aligned = resolvedCandidate
		}
		aligned = alignPathCase(aligned)
		if !Within(alignPathCase(root), aligned) {
			return "", fmt.Errorf("path escapes the sandbox")
		}
		root, candidate = alignPathCase(root), aligned
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
		if aligned := alignPathCase(candidate); Within(root, aligned) {
			candidate = aligned
		} else {
			return "", fmt.Errorf("symlink target escapes the sandbox")
		}
	}
	return candidate, nil
}

// alignPathCase 回傳路徑在磁碟上的真實大小寫。
//
// macOS 的 APFS 預設不分大小寫：EvalSymlinks 對「大小寫不同但存在」的路徑會成功，
// 卻原樣保留呼叫端給的大小寫。於是「檔案打不打得開」與「路徑在不在沙箱內」用了
// 兩套規則——前者交給檔案系統（不分大小寫），後者是字串比對（分大小寫）——
// 真實存在、也真的在沙箱裡的路徑因此被判定逃逸。實際踩過：沙箱根是
// FastChIME，工具要求 FastCHIME，Rel 算出 ../FastCHIME 就被擋下。
//
// 只在比對失敗時呼叫：每一層都要讀一次目錄，放在正常路徑上太貴。
// 在分大小寫的檔案系統上逐層只會找到完全相同的名稱，結果不變——
// 這不會放寬 Linux 的檢查，只是讓檢查與檔案系統的實際行為一致。
func alignPathCase(path string) string {
	path = filepath.Clean(path)
	volume := filepath.VolumeName(path)
	rest := strings.TrimPrefix(path, volume)
	if !strings.HasPrefix(rest, string(filepath.Separator)) {
		// 相對路徑沒有可靠的起點可以逐層比對，原樣回傳。
		return path
	}
	current := volume + string(filepath.Separator)
	for _, part := range strings.Split(strings.Trim(rest, string(filepath.Separator)), string(filepath.Separator)) {
		if part == "" {
			continue
		}
		current = filepath.Join(current, matchDirEntryCase(current, part))
	}
	return current
}

// matchDirEntryCase 在 parent 底下找出與 name 大小寫無關相符的真實名稱。
// 找不到（例如尚未建立的檔案）就原樣回傳，讓後續步驟自行處理。
func matchDirEntryCase(parent, name string) string {
	entries, err := os.ReadDir(parent)
	if err != nil {
		return name
	}
	for _, entry := range entries {
		if entry.Name() == name {
			return name
		}
	}
	for _, entry := range entries {
		if strings.EqualFold(entry.Name(), name) {
			return entry.Name()
		}
	}
	return name
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

// ProducedFiles 在工具結果的 details 裡標記這次實際產生或修改的檔案。
//
// 結果的 details 只會送到 UI，不會進入模型的請求（Message.Metadata 從來不編進
// provider payload），所以這裡放絕對路徑是安全的——而桌面端的開檔橋接只接受絕對
// 路徑，display path 在單一 sandbox 根目錄時是相對的，前端自己拼不回來。
//
// 少了這個標記，Agent 產出的檔案在畫面上只是一行文字：使用者知道有這個檔案，
// 但要自己去 Finder 裡找。
func ProducedFiles(details map[string]any, paths ...string) map[string]any {
	if details == nil {
		details = map[string]any{}
	}
	produced := make([]string, 0, len(paths))
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" || !filepath.IsAbs(path) {
			continue
		}
		produced = append(produced, filepath.Clean(path))
	}
	if len(produced) == 0 {
		return details
	}
	details["produced_paths"] = produced
	return details
}
