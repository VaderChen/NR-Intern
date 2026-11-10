package layering

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// rule 描述一個套件（或一組套件）允許 import 的內部套件。
//
// pkg 與 allow 的寫法相同：不以 "/" 結尾者為完全相符，以 "/" 結尾者為前綴相符。
// 一個套件同時符合多條規則時採用 pkg 最長的那一條。allow 為空代表不得 import
// 任何內部套件。
type rule struct {
	pkg   string
	allow []string
}

// rules 是 src 下的分層白名單，路徑皆相對於 src。
//
// 只檢查非測試檔：測試可以為了整合驗證而跨層（例如 harness 的測試會接上真正的
// filestore 與 mcpclient）。新增套件時必須在這裡宣告它屬於哪一層，否則測試失敗。
var rules = []rule{
	// 核心：domain 不依賴任何內部套件，ports 只描述以 domain 型別表達的介面。
	{pkg: "domain"},
	{pkg: "ports", allow: []string{"domain"}},

	// 不含業務語意的葉節點。
	{pkg: "internal/"},
	{pkg: "schemaargs"},
	{pkg: "providerauth"},
	{pkg: "netpass"},
	{pkg: "web/console"},

	// 只依賴 domain 的小型領域服務。
	{pkg: "tokens", allow: []string{"domain"}},
	{pkg: "approval", allow: []string{"domain"}},
	{pkg: "question", allow: []string{"domain"}},

	{pkg: "memory", allow: []string{"domain", "ports", "internal/"}},
	{pkg: "modelrouter", allow: []string{"domain", "ports"}},

	// Harness 工作迴圈：不得依賴 adapters、tools、mcpclient、transport、application。
	{pkg: "harness", allow: []string{"domain", "ports", "memory", "tokens", "internal/"}},
	{pkg: "agents/harness", allow: []string{"domain", "ports", "harness"}},

	// Application：不依賴具體 Harness、adapter、transport 或 bootstrap。
	{pkg: "application", allow: []string{"domain", "ports", "internal/"}},

	// 對外部系統的實作。
	{pkg: "adapters/", allow: []string{"domain", "ports", "internal/", "providerauth"}},
	{pkg: "mcpclient", allow: []string{"domain", "ports", "internal/", "schemaargs"}},

	// 工具：聚合層不認識 MCP Client 實作，個別工具不互相依賴。
	{pkg: "tools", allow: []string{"domain", "ports", "internal/", "schemaargs"}},
	{pkg: "tools/native/internal/"},
	{pkg: "tools/native/", allow: []string{"domain", "ports", "tools", "tools/native/internal/"}},
	{pkg: "tools/native/memories", allow: []string{"domain", "ports", "tools", "tools/native/internal/", "memory"}},

	{pkg: "transport/httpapi", allow: []string{"domain", "ports", "application"}},

	// Composition root：可以組裝後端的一切，但不得依賴桌面、內嵌前端或進入點。
	{pkg: "bootstrap", allow: []string{
		"domain", "ports", "internal/", "application", "harness", "agents/", "adapters/",
		"approval", "question", "memory", "tokens", "modelrouter", "mcpclient", "netpass",
		"providerauth", "tools", "tools/", "transport/",
	}},

	// 桌面程式只透過 HTTP 與後端溝通。
	{pkg: "desktop/", allow: []string{"desktop/", "internal/", "web/console"}},

	// 進入點。
	{pkg: "cmd/server", allow: []string{"bootstrap"}},
	{pkg: "cmd/desktop", allow: []string{"bootstrap", "desktop/"}},
	{pkg: "cmd/release"},
}

func matches(pattern, pkg string) bool {
	if strings.HasSuffix(pattern, "/") {
		return strings.HasPrefix(pkg, pattern)
	}
	return pattern == pkg
}

func ruleFor(pkg string, table []rule) (rule, bool) {
	best, found := rule{}, false
	for _, candidate := range table {
		if matches(candidate.pkg, pkg) && (!found || len(candidate.pkg) > len(best.pkg)) {
			best, found = candidate, true
		}
	}
	return best, found
}

// importGraph 是「套件 → 它 import 的內部套件 → 其中一個出處檔案」。
type importGraph map[string]map[string]string

// check 回傳所有違規的說明，已排序；沒有違規時為空。
func check(graph importGraph, table []rule) []string {
	var problems []string
	for pkg, imports := range graph {
		matched, ok := ruleFor(pkg, table)
		if !ok {
			problems = append(problems, fmt.Sprintf("套件 %s 沒有分層規則；請在 layering_test.go 的 rules 宣告它允許依賴的套件", pkg))
			continue
		}
		for imported, file := range imports {
			allowed := false
			for _, pattern := range matched.allow {
				if matches(pattern, imported) {
					allowed = true
					break
				}
			}
			if !allowed {
				problems = append(problems, fmt.Sprintf("套件 %s 不得 import %s（出處 %s；適用規則 %q 允許 %v）", pkg, imported, file, matched.pkg, matched.allow))
			}
		}
	}
	sort.Strings(problems)
	return problems
}

// moduleRoot 由目前目錄往上找 go.mod，回傳模組根目錄與模組名稱。
func moduleRoot(t *testing.T) (string, string) {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				if name, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
					return dir, strings.TrimSpace(name)
				}
			}
			t.Fatalf("%s 沒有 module 宣告", filepath.Join(dir, "go.mod"))
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("找不到 go.mod")
		}
		dir = parent
	}
}

// loadGraph 解析 src 下所有非測試 Go 檔的 import。直接讀原始碼而不呼叫 go list，
// 因此其他作業系統專用的檔案（*_windows.go 等）也在檢查範圍內。
func loadGraph(t *testing.T) importGraph {
	t.Helper()
	root, module := moduleRoot(t)
	source := filepath.Join(root, "src")
	prefix := module + "/src/"
	graph := importGraph{}
	files := token.NewFileSet()
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := entry.Name()
		if entry.IsDir() {
			if path != source && (name == "testdata" || name == "vendor" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		relative, err := filepath.Rel(source, filepath.Dir(path))
		if err != nil {
			return err
		}
		pkg := filepath.ToSlash(relative)
		if graph[pkg] == nil {
			graph[pkg] = map[string]string{}
		}
		parsed, err := parser.ParseFile(files, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range parsed.Imports {
			value, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			imported, ok := strings.CutPrefix(value, prefix)
			if !ok || imported == pkg {
				continue
			}
			if _, seen := graph[pkg][imported]; !seen {
				graph[pkg][imported] = filepath.ToSlash(filepath.Join("src", pkg, name))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return graph
}

func TestPackageDependenciesFollowLayering(t *testing.T) {
	graph := loadGraph(t)
	if len(graph) < 20 {
		t.Fatalf("只找到 %d 個套件，掃描範圍可能有誤", len(graph))
	}
	for _, problem := range check(graph, rules) {
		t.Error(problem)
	}
}

// 規則沒有對應任何套件時多半是套件已搬移或改名，留著只會讓人誤以為仍受保護。
func TestLayeringRulesAreNotStale(t *testing.T) {
	graph := loadGraph(t)
	for _, candidate := range rules {
		used := false
		for pkg := range graph {
			if matched, ok := ruleFor(pkg, rules); ok && matched.pkg == candidate.pkg {
				used = true
				break
			}
		}
		if !used {
			t.Errorf("規則 %q 沒有對應任何套件", candidate.pkg)
		}
	}
}

func TestCheckReportsViolations(t *testing.T) {
	graph := importGraph{
		"domain":                {"ports": "src/domain/x.go"},
		"application":           {"domain": "src/application/a.go", "harness": "src/application/b.go"},
		"desktop/httpui":        {"domain": "src/desktop/httpui/server.go", "desktop/supervisor": "src/desktop/httpui/server.go"},
		"tools":                 {"mcpclient": "src/tools/runtime.go"},
		"tools/native/files":    {"tools/native/shell": "src/tools/native/files/x.go", "tools/native/internal/toolutil": "src/tools/native/files/y.go"},
		"tools/native/memories": {"memory": "src/tools/native/memories/tool.go"},
		"brandnew":              {},
	}
	problems := check(graph, rules)
	want := []string{
		"套件 application 不得 import harness",
		"套件 brandnew 沒有分層規則",
		"套件 desktop/httpui 不得 import domain",
		"套件 domain 不得 import ports",
		"套件 tools 不得 import mcpclient",
		"套件 tools/native/files 不得 import tools/native/shell",
	}
	if len(problems) != len(want) {
		t.Fatalf("違規數量為 %d，預期 %d：\n%s", len(problems), len(want), strings.Join(problems, "\n"))
	}
	for index, expected := range want {
		if !strings.HasPrefix(problems[index], expected) {
			t.Errorf("第 %d 筆為 %q，預期開頭 %q", index, problems[index], expected)
		}
	}
	if !strings.Contains(problems[0], "src/application/b.go") {
		t.Errorf("違規訊息應指出出處檔案：%s", problems[0])
	}
}
