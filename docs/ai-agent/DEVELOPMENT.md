# 執行與開發

## 設定

複製 `configs/ai-agent/config.example.json`，或使用環境變數：

| 環境變數 | 說明 |
|---|---|
| `AI_AGENT_LISTEN` | 後端監聽位址 |
| `AI_AGENT_DATA_DIR` | Session、Run 與 workspace 根目錄 |
| `AI_AGENT_API_TOKEN` | HTTP Bearer token |
| `AI_AGENT_DEFAULT_PROVIDER_ID` | 環境變數要覆寫的預設 Provider ID |
| `AI_AGENT_LLM_BASE_URL` / `OPENAI_BASE_URL` | OpenAI-compatible base URL |
| `AI_AGENT_LLM_API_KEY` / `OPENAI_API_KEY` | LLM API key |
| `AI_AGENT_LLM_MODEL` | 模型名稱 |
| `AI_AGENT_LLM_MAX_ATTEMPTS` | Provider 初始／暫時性錯誤的總嘗試次數，最多 3 |
| `AI_AGENT_LLM_DISABLE_STREAMING` | 相容服務不支援 SSE 時改用 JSON 回應 |
| `AI_AGENT_ALLOWED_TOOLS` | 逗號分隔工具 allowlist |
| `AI_AGENT_ALLOW_ELEVATED_TOOLS` | 是否允許 elevated session 使用寫檔、Shell／SSH；單機範例預設開啟，但每次高風險操作仍須人工核准 |
| `AI_AGENT_MAX_TURNS` | Harness 最大回合數 |
| `AI_AGENT_MAX_WALL_CLOCK_SECONDS` | 單次 Run 最長執行秒數；到期會取消正在等待的模型或工具 |
| `AI_AGENT_MAX_TOKENS` | 單次 Run 累計 Provider token 上限 |
| `AI_AGENT_MAX_TOOL_CALLS` | 單次 Run 最多可實際執行的工具呼叫數 |
| `AI_AGENT_CONTEXT_MAX_TOKENS` | 觸發上下文壓縮的估算 token 上限 |
| `AI_AGENT_MAX_FILE_INPUT_BYTES` | `file_write`／`file_edit` 可處理的內容上限 |
| `AI_AGENT_MEMORY_ENABLED` | 啟用長期記憶儲存與工具 |
| `AI_AGENT_MEMORY_AUTO_RECALL` | 每次 operation 自動召回相關記憶 |
| `AI_AGENT_MEMORY_ALLOW_WRITES` | 開放記憶寫入與軟性遺忘工具 |

JSON 設定使用具名 Provider registry。`type` 是 adapter 工廠辨識欄位；目前支援 `openai-compatible`，日後新增類型時不修改 Workspace 或 Harness：

```json
{
  "default_provider_id": "openai-compatible",
  "providers": {
    "openai-compatible": {
      "type": "openai-compatible",
      "openai_compatible": {
        "base_url": "https://api.openai.com/v1",
        "api_key": "",
        "model": "gpt-4o-mini",
        "max_attempts": 3
      }
    }
  }
}
```

`AI_AGENT_LLM_*` 與 `OPENAI_*` 只覆寫 `AI_AGENT_DEFAULT_PROVIDER_ID` 指定的 OpenAI-compatible Provider；其他具名 Provider 使用 JSON 設定。

Context 摘要可在 JSON 的 `context.summary_provider_id`／`summary_model` 指定較便宜的路由；
Provider ID 必須已存在於 `providers`。留空時摘要沿用 Session 的 Provider 與 Model。

## 獨立後端

```bash
go run ./src/cmd/server -config ./configs/ai-agent/config.example.json
```

後端不載入前端資源，也不依賴桌面 package。

## 桌面 Console

```bash
go run ./src/cmd/desktop -config ./configs/ai-agent/config.example.json
```

桌面 UI 預設使用 `http://127.0.0.1:8790`，後端使用 `http://127.0.0.1:8787`。如果後端已存在，桌面只連接；否則桌面會啟動自己的 backend child。

連接其他已啟動後端：

```bash
go run ./src/cmd/desktop \
  -auto-start=false \
  -backend-url http://127.0.0.1:9000 \
  -backend-token YOUR_TOKEN
```

## 新增原生工具

1. 在 `src/tools/native/<category>/` 實作 `tools.NativeTool`。
2. `Definition()` 宣告 JSON Schema、platform 與 permission。
3. `Execute()` 使用 Go API；OS 差異放入同 package 的 build-tag platform adapter。
4. 在 `src/bootstrap/runtime.go` composition root 註冊。
5. 不修改 Harness、HTTP handler 或 Web UI。

需要檔案路徑的工具必須使用 `native/internal/toolutil.ResolvePathInRoots` 限制在 Project／Session Sandbox；長輸出使用 `LimitedBuffer`。不使用 Sandbox 的工具仍應透過明確 scope 或後端 profile 隔離資料。

## 編譯檢查

```bash
gofmt -w ./src
go test -run '^$' ./src/...
```

`-run '^$'` 只進行 package 編譯，不執行測試案例。

## 跨平台發行

專案支援 Windows x64、Windows ARM64 與 macOS ARM64。發行封裝、版本產生、安裝檔建立、完整性清單與簽章均由維護者的內部流程處理；公開文件不提供封裝命令、參數、工具位置或簽章設定。

發行產物不得內嵌實際設定、Provider API Key、SSH 憑證或開發者電腦的絕對路徑。部署時應另外提供受保護的本機設定，並在發布前檢查產物與版本庫是否含有敏感資訊。
