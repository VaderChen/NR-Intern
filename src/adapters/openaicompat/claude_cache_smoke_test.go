package openaicompat

import (
	"AgenticService/src/domain"
	"encoding/json"
	"reflect"
	"testing"
)

type claudeCachePayload struct {
	System   []map[string]any `json:"system"`
	Tools    []map[string]any `json:"tools"`
	Messages []map[string]any `json:"messages"`
}

func claudeCacheRequest(t *testing.T, request domain.ModelRequest) ([]byte, claudeCachePayload) {
	t.Helper()
	m, err := New(Config{UseClaude: true, BaseURL: "https://example.invalid/v1", Model: "synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	body, err := m.claudeRequest(request, "synthetic", true)
	if err != nil {
		t.Fatal(err)
	}
	var payload claudeCachePayload
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	return body, payload
}

func claudeCacheTools() []domain.ToolDefinition {
	schema := map[string]any{"type": "object", "properties": map[string]any{}}
	return []domain.ToolDefinition{
		{Name: "lookup", Description: "查詢", InputSchema: schema},
		{Name: "write", Description: "寫入", InputSchema: schema},
	}
}

func TestClaudeCacheBreakpointsSmoke(t *testing.T) {
	request := domain.ModelRequest{
		SystemPrompt: "固定系統", HostPrompt: "主機環境", ToolPrompt: "工具說明", PhasePrompt: "已完成 1 個回合",
		ContextPrompt: "記憶", Tools: claudeCacheTools(), UserPrompt: "查詢",
	}
	_, payload := claudeCacheRequest(t, request)
	want := map[string]any{"type": "ephemeral"}
	if len(payload.Tools) != 2 || len(payload.System) != 4 {
		t.Fatalf("tools=%d system=%d", len(payload.Tools), len(payload.System))
	}
	if _, marked := payload.Tools[0]["cache_control"]; marked {
		t.Fatal("只應標記最後一個工具定義")
	}
	if !reflect.DeepEqual(payload.Tools[1]["cache_control"], want) {
		t.Fatalf("最後一個工具定義未標記：%v", payload.Tools[1])
	}
	for index, block := range payload.System {
		_, marked := block["cache_control"]
		if marked != (index == 2) {
			t.Fatalf("system[%d] 的快取標記不正確：%v", index, block)
		}
	}
	if !reflect.DeepEqual(payload.System[2]["cache_control"], want) {
		t.Fatalf("穩定指示的結尾未標記：%v", payload.System[2])
	}
	for _, message := range payload.Messages {
		for _, block := range message["content"].([]any) {
			if _, marked := block.(map[string]any)["cache_control"]; marked {
				t.Fatal("對話內容不應標記")
			}
		}
	}

	// 階段提示每一輪都不同；斷點之前的內容必須完全相同才會命中。
	changed := request
	changed.PhasePrompt = "已完成 2 個回合"
	_, next := claudeCacheRequest(t, changed)
	if !reflect.DeepEqual(payload.Tools, next.Tools) || !reflect.DeepEqual(payload.System[:3], next.System[:3]) {
		t.Fatal("階段提示改變不應影響斷點之前的內容")
	}
	if len(request.Tools[1].InputSchema) != 2 {
		t.Fatal("污染呼叫端的工具定義")
	}
}

func TestClaudeCacheBreakpointEdgesSmoke(t *testing.T) {
	_, onlySystem := claudeCacheRequest(t, domain.ModelRequest{SystemPrompt: "固定系統", PhasePrompt: "階段", UserPrompt: "你好"})
	if len(onlySystem.Tools) != 0 || len(onlySystem.System) != 2 {
		t.Fatalf("tools=%d system=%d", len(onlySystem.Tools), len(onlySystem.System))
	}
	if _, marked := onlySystem.System[0]["cache_control"]; !marked {
		t.Fatal("只有系統提示時應標記它")
	}
	if _, marked := onlySystem.System[1]["cache_control"]; marked {
		t.Fatal("階段提示不應標記")
	}
	body, bare := claudeCacheRequest(t, domain.ModelRequest{PhasePrompt: "階段", UserPrompt: "你好"})
	if len(bare.System) != 1 {
		t.Fatalf("system=%d", len(bare.System))
	}
	if _, marked := bare.System[0]["cache_control"]; marked {
		t.Fatalf("沒有穩定指示時不應標記：%s", body)
	}
}

// 斷點不是內容：思考簽章綁定的前綴摘要必須與沒有斷點時相同，
// 加入快取之前保存的摘要才不會全部失效。
func TestClaudeCacheKeepsPrefixHashSmoke(t *testing.T) {
	body, _ := claudeCacheRequest(t, domain.ModelRequest{
		SystemPrompt: "固定系統", HostPrompt: "主機環境", PhasePrompt: "階段", Tools: claudeCacheTools(), UserPrompt: "查詢",
	})
	var generic map[string]any
	if err := json.Unmarshal(body, &generic); err != nil {
		t.Fatal(err)
	}
	removed := 0
	for _, key := range []string{"system", "tools"} {
		for _, item := range generic[key].([]any) {
			if _, marked := item.(map[string]any)["cache_control"]; marked {
				delete(item.(map[string]any), "cache_control")
				removed++
			}
		}
	}
	if removed != 2 {
		t.Fatalf("預期兩個斷點，實際 %d", removed)
	}
	plain, err := json.Marshal(generic)
	if err != nil {
		t.Fatal(err)
	}
	marked, err := claudeRequestPrefixHash(body)
	if err != nil {
		t.Fatal(err)
	}
	unmarked, err := claudeRequestPrefixHash(plain)
	if err != nil {
		t.Fatal(err)
	}
	if marked != unmarked {
		t.Fatal("快取斷點改變了前綴摘要")
	}
}
