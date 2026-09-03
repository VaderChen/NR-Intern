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

服務設定也可由管理介面調整工具供應方式：`extended_tools` 預設關閉，精簡集合已包含 Shell、
讀檔、目錄、搜尋、計畫控制，以及 `document_inspect`、`document_read`、`document_create`、
`document_convert`。需要文件編輯、驗證、渲染、SSH 或記憶等工具時再公開擴充集合；所有工具仍取
`allowed_tools` 的交集。`tool_retrieval` 預設開啟，先依需求縮小工具提示，其他已啟用工具仍可由
`find_tools` 取回；`tool_call_mode` 預設為 `native`，Provider 不支援原生 `tool_calls` 時才切換
為 `instruction`。這些設定只影響之後開始的 Run。文件產出與轉換不必先等待 Shell 失敗；
`wait_for`／`ssh_wait` 則依需求檢索，不再固定放入核心工具目錄。

通知、工具供應、對外網路與實驗性功能開關在切換時即儲存；名稱、語言與數值上限仍由儲存按鈕
提交。「回憶空間」的 `memory_space` 預設 `false`，啟用後即時套用種類／憑證樣式檢查、近似
去重、Project 優先的 scope 與較小的自動召回視窗；仍需 `memory.enabled`，並遵守
`memory.auto_recall`、`memory.allow_writes` 與工具供應設定。策略參數放在 `memory.space`，
詳見 [回憶空間](MEMORY_SPACE.md)。目前注入預算按 UTF-8 bytes 計算，不是 Unicode 字數。

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

`context.max_history_characters` 預設 60,000（非正值使用預設），用來補足 JSON、代碼等內容的
token 估算誤差。整形後歷史超過此值也會觸發整理；送出前仍超量時再裁去較舊訊息，只改變模型
所見的歷史，不刪除 transcript。至少保留最新一則，因此不是整份請求的硬性字元上限；system
prompt、工具 schema 與當前輸入仍需另計。`max_tokens` 與 wall-clock 的 Run 預算不受此設定改變。

後端啟動時會在背景探索 Provider 模型限制，使用共用 20 秒期限；探索失敗不阻擋服務啟動。
未取得 context window 時，Harness 日誌會標示採用後備預算。Console 以星號標示後備估算的
百分比；Provider 設定頁另列有效能力供比對，人工欄位的 0 不代表模型沒有上下文容量。

模型成本表 `model_prices` 也是非秘密設定，依 Provider ID 與模型名稱指定每百萬 token 的 input／output
單價；未設定價格時只顯示 token，不會假造成本。收尾後的 Run 成本是當時價格的快照，日後修改價格
表不會改寫歷史 Run。

但 Session 累計依目前保留的 Run 彙總；舊 Run 被儲存維護淘汰後，統計與匯出不再包含那筆用量。
需要長期成本追蹤時應定期另存匯出，不能把畫面數字當成終身用量或 Provider 帳單。

## 長對話讀取與儲存維護

Transcript 的記憶體索引只保存 sequence、type 與 byte offset，不複製工具本文；首次讀取掃描
一次，後續分頁直接定位需要的範圍，append 時同步延長索引。索引失效時可重建，不能建立時退回
掃描；檔案本身損毀仍可能回報錯誤。對話壓縮不會刪除原始 transcript。

目前保留規則為程式內固定值，沒有設定頁或 JSON 開關：

- RunRepository 啟動／儲存時，以 500 筆為整理基準，只淘汰最舊範圍內已結束的 Run；未終態保留，
  因此總數可超過 500。
- 背景維護在啟動後與每 30 分鐘執行，保留依建立時間最新 50 筆 Run 的事件檔與所有未終態 Run
  的事件檔，其餘及孤兒事件檔會刪除。
- 非 active 且 `UpdatedAt` 早於 30 天前的記憶會永久清理，不受 `memory_space` 開關影響。

**升級前先備份。** Run metadata、事件檔與過期記憶稽核資料遭清理後，無法僅由對話文字恢復
原本的 Run API、用量或事件重播。需要稽核時另外保存匯出；安全備份也不包含所有秘密設定。

## 對外網路設定

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
go run -buildvcs=false ./src/cmd/server -config ./configs/ai-agent/config.example.json
```

後端不載入前端資源，也不依賴桌面 package。

## 桌面 Console

```bash
go run -buildvcs=false ./src/cmd/desktop -config ./configs/ai-agent/config.example.json
```

桌面 UI 預設使用 `http://127.0.0.1:8790`，後端使用 `http://127.0.0.1:8787`。如果後端已存在，桌面只連接；否則桌面會啟動自己的 backend child。

連接其他已啟動後端：

```bash
go run -buildvcs=false ./src/cmd/desktop \
  -auto-start=false \
  -backend-url http://127.0.0.1:9000 \
  -backend-token YOUR_TOKEN
```

macOS 版啟動時就建立狀態列項目；Windows 版啟動時就建立 Tray Icon。對話進行中關閉主視窗可
選擇只隱藏 UI，Run 會繼續由後端執行；狀態列／Tray 選單可顯示、隱藏或真正結束程式。若再次
啟動時 desktop port 已由 NR-Intern 使用，新程序會要求既有程序恢復主視窗後結束，不會建立第二個
UI／backend。桌面自行啟動的 backend child 會綁定父程序；父程序消失時 child 會自動退出，避免
背景留下孤兒後端。

Console 的待送訊息使用目前 Browser／WebView profile 的 IndexedDB Durable Outbox，上一個 Run
結束後依序送出；重新整理可載回待送內容，清除網站資料或更換 profile 則不保留。已送出的 Run
由後端持久化，UI 斷線不會取消。重開 UI 時先由恢復視窗選擇要重新連線的 Session。

長對話左側有提問段落導覽，兩則以上提問才顯示；滑過可預覽、點擊可跳轉。Session 用量以 K／M
縮寫顯示，提示保留精確值；未設定模型價格時隱藏金額。用量包含已消耗的歷史 Run，重新提問
不會扣回 token，也不等於當前送往模型的上下文大小。

## 匯出設定

管理介面「系統工具 → 下載設定包」透過 `GET /api/v1/admin/config-bundle` 下載
`nr-intern-config.zip`。它只收錄 `data_dir` 已存在的 `providers.json`、`mcp-servers.json`、
`netpass.json`、`service-settings.json` 與 `manifest.json`；不收錄對話或附件，不讀取 OAuth
token 檔，也不是完整有效設定的快照：只在原始設定檔或環境變數中提供的值不會自動納入。

設定包保留結構並遮蔽可辨識的秘密欄位，供換機時參考與重新設定；目前沒有設定包專用匯入端點，
不能交給「還原備份」直接還原。金鑰需另外輸入。URL、帳號、路徑、command／args 等仍可能含
敏感內容，分享前務必人工檢查，完整限制見 [安全設計](SECURITY.md)。

側邊欄記事本使用 Browser／WebView 的 `localStorage`，內容只留在目前裝置，不會傳入後端、Session
或 Agent Prompt。畫面擷取由 desktop-local bridge 處理：macOS 啟動系統 `Screenshot.app` 的區域
截圖模式，先把原圖送到系統剪貼簿，再將 PNG 回傳內建標註編輯器；按複製或關閉編輯器都會以
標註後的 PNG 覆寫剪貼簿。若啟用「擷取畫面時隱藏視窗」，WebView 會透過原生 binding 暫時隱藏
NR-Intern，擷取完成或取消後再恢復。Windows 目前啟動系統剪取介面，完成結果由 Windows 寫入
剪貼簿，不會自動把 PNG 回傳編輯器。

## MCP Client

主系統可從管理頁或 `mcp_servers` 設定連接 MCP Server：

- `stdio`：指定 command、args、work directory 與必要 environment。子程序不會繼承完整後端環境。
- `sse`：連接遵循 MCP 2024-11-05 的舊版 SSE endpoint，可另帶 Bearer Token、Basic Auth 與 headers。
- `streamable-http`：指定 http／https URL，可另帶 Bearer Token、Basic Auth 與 headers。

儲存後 `mcpclient.Manager` 在背景連線並刷新 `tools/list`。OpenAI-compatible Provider 會以原生
`tools`／`tool_calls` 提供 MCP 工具，模型不必依賴文字指令猜測格式。每個 MCP 工具都經由既有
permission profile、工具事件與輸出限制；Server 宣告 `readOnlyHint` 且管理者啟用
`trust_annotations` 時，唯讀工具可免逐次人工 Approval，其他工具仍須核准，不應直接註冊成繞過
`tools.Registry` 的捷徑。
結構化結果與 `input_required` 多輪控制資料會回到模型；只有 idempotent 工具的連線層錯誤可以安全重試。
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
gofmt -l src/
go build -buildvcs=false ./...
go vet -buildvcs=false ./src/...
go test -buildvcs=false ./src/...
```

`gofmt -l src/` 應沒有輸出；上述命令同時檢查全專案建置、`src/` 靜態分析與測試。

## 跨平台發行

專案支援 Windows x64、Windows ARM64 與 macOS ARM64。發行封裝、版本產生、安裝檔建立、完整性清單與簽章均由維護者的內部流程處理；公開文件不提供封裝命令、參數、工具位置或簽章設定。

發行產物不得內嵌實際設定、Provider API Key、SSH 憑證或開發者電腦的絕對路徑。部署時應另外提供受保護的本機設定，並在發布前檢查產物與版本庫是否含有敏感資訊。

## MCP smoke test

`src/harness/mcp_smoke_test.go` 不使用 fake 工具，而是把真的 MCP Server（go-sdk 起在
`httptest`）、真的 `mcpclient.Manager`、真的 `tools.Runtime` 與真的 Harness Runner 串起來，
只有模型是腳本化的。驗的是「模型看得到工具 → 呼叫得到 → 結果回得來」這條路徑：

```bash
go test -buildvcs=false ./src/harness/ -run TestSmoke -v
```

涵蓋工具探索與公開命名、instruction 模式呼叫與參數傳遞、未知工具名進入協定修正、
執行中 MCP Server 重啟後自動重連、人工核准、MCP 無法連線時的清楚失敗，以及
「模型只給計畫卻沒呼叫工具」會被完成度閘門擋下。修改 MCP 或 Harness 工具路徑後，
這組測試是最快的回歸檢查。
