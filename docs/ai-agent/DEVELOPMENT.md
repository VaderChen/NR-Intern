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
| `AI_AGENT_MAX_AUTONOMOUS_TOOL_TURNS` | 自主工作工具回合的額外上限；`0` 表示不另設固定上限，仍受 `max_turns` 與其他 Run budget 約束 |
| `AI_AGENT_MAX_WALL_CLOCK_SECONDS` | 單次 Run 最長執行秒數；到期會取消正在等待的模型或工具 |
| `AI_AGENT_MAX_TOKENS` | 單次 Run 累計 Provider token 上限 |
| `AI_AGENT_MAX_TOOL_CALLS` | 單次 Run 最多可實際執行的工具呼叫數 |
| `AI_AGENT_CONTEXT_MAX_TOKENS` | 模型未回報 context window 時使用的輸入預算後備值；預設 256K |
| `AI_AGENT_MAX_FILE_INPUT_BYTES` | `file_write`／`file_edit` 可處理的內容上限 |
| `AI_AGENT_MEMORY_ENABLED` | 啟用長期記憶儲存與工具 |
| `AI_AGENT_MEMORY_AUTO_RECALL` | 每次 operation 自動召回相關記憶 |
| `AI_AGENT_MEMORY_ALLOW_WRITES` | 開放記憶寫入與軟性遺忘工具 |

載入優先序為：內建預設 → JSON 設定 → 管理介面持久化設定 → 環境變數。環境變數在持久化設定
載入後會再套用一次，因此永遠具有最高優先權；管理介面不會覆寫部署環境明確注入的值。

JSON 設定使用具名 Provider registry。`type` 是 adapter 工廠辨識欄位；目前支援
`openai-compatible` 與 `openai-codex-responses`，新增類型時不修改 Workspace 或 Harness：

```json
{
  "default_provider_id": "openai-compatible",
  "providers": {
    "openai-compatible": {
      "type": "openai-compatible",
      "openai_compatible": {
        "base_url": "https://llm.example.com/v1",
        "api_key": "",
        "model": "example-model",
        "max_attempts": 3
      }
    }
  }
}
```

Codex Responses Provider 不設定自訂 endpoint 或 API Key；先保存 Provider，再從管理頁完成
ChatGPT／Codex OAuth：

```json
{
  "providers": {
    "codex": {
      "type": "openai-codex-responses",
      "enabled": true,
      "openai_codex_responses": {
        "model": "YOUR_CODEX_MODEL"
      }
    }
  }
}
```

OAuth Token、管理頁保存的 Provider 集合、MCP Server 集合、服務設定與 NetPass Key 都位於
`data_dir` 下的權限限制檔案，不應複製回範例設定或提交版本庫。管理 API 只回傳是否已設定，
不提供秘密明文。

`AI_AGENT_LLM_*` 與 `OPENAI_*` 只覆寫 `AI_AGENT_DEFAULT_PROVIDER_ID` 指定的 OpenAI-compatible Provider；其他具名 Provider 使用 JSON 設定。

Context 摘要可在 JSON 的 `context.summary_provider_id`／`summary_model` 指定較便宜的路由；
Provider ID 必須已存在於 `providers`。留空時摘要沿用 Session 的 Provider 與 Model。

`http_fetch` 使用獨立設定區塊：

```json
{
  "http_fetch": {
    "enabled": true,
    "allow_private_networks": true,
    "max_response_bytes": 1048576,
    "timeout_seconds": 30,
    "max_redirects": 5,
    "allowed_hosts": [],
    "blocked_hosts": []
  }
}
```

`enabled` 與 `allow_private_networks` 可由管理介面即時調整；其餘邊界只讀取 JSON 設定。
`allowed_hosts` 非空時採白名單，`blocked_hosts` 永遠優先。`allow_private_networks` 預設為 `true`，允許
localhost、loopback、私有網段、link-local、CGNAT 與 multicast；不需要存取私有服務時可關閉。

遠端部署若包含非同步上傳，應把上傳／部署命令與狀態確認分開：副作用命令只執行一次，必要時以 `wait_for` 等待，再使用 `ssh_wait` 以同一 SSH profile 執行唯讀檢查。檢查命令可用 `output_equals` 或 `output_contains` 比對預期 bytes／SHA-256／就緒訊息，並可用 `stable_checks` 要求連續多次符合；`ssh_wait` 逾時時必須保留未完成狀態。

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

macOS 版啟動時就建立狀態列項目。對話進行中關閉主視窗可選擇只隱藏 UI，Run 會繼續由後端執行；
狀態列選單可顯示、隱藏或真正結束程式。若再次啟動時 desktop port 已由 NR-Intern 使用，新程序會
要求既有程序恢復主視窗後結束，不會建立第二個 UI／backend。Windows 目前沒有這組原生狀態列
生命週期。

Console 的待送訊息佇列只存在目前 Browser／WebView 記憶體，上一個 Run 結束後依序送出；重新整理
頁面或結束 UI 不保證保留尚未送出的項目。已送出的 Run 則是 durable，UI 斷線不會取消。

側邊欄記事本使用 Browser／WebView 的 `localStorage`，內容只留在目前裝置，不會傳入後端、Session
或 Agent Prompt。畫面擷取由 desktop-local bridge 處理：macOS 啟動系統 `Screenshot.app` 的區域
截圖模式，先把原圖送到系統剪貼簿，再將 PNG 回傳內建標註編輯器；按複製或關閉編輯器都會以
標註後的 PNG 覆寫剪貼簿。若啟用「擷取畫面時隱藏視窗」，WebView 會透過原生 binding 暫時隱藏
NR-Intern，擷取完成或取消後再恢復。Windows 目前啟動系統剪取介面，完成結果由 Windows 寫入
剪貼簿，不會自動把 PNG 回傳編輯器。

## MCP Client

主系統可從管理頁或 `mcp_servers` 設定連接 MCP Server：

- `stdio`：指定 command、args、work directory 與必要 environment。子程序不會繼承完整後端環境。
- `streamable-http`：指定 http／https URL，可另帶 Bearer Token 與 headers。

儲存後 `mcpclient.Manager` 在背景連線並刷新 `tools/list`。每個 MCP 工具都經由既有 permission
profile、人工 Approval、工具事件與輸出限制，不應直接註冊成繞過 `tools.Registry` 的捷徑。
新增 transport 或內容類型時，維持「秘密不回讀、外部內容不受信任、取消可傳遞、輸出有上限」四個
邊界。

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
