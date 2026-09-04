# AI Agent 架構

## 目標

本系統是從舊智慧問答概念重新建立的新 AI Agent，不維持舊 API、問題拆解流程或舊工具相容性。舊程式只作為行為與資料來源參考。

核心要求如下：

- 後端是可獨立啟動的 HTTP API，不依賴桌面前端。
- Browser、任意 HTTP Client 與桌面程式都使用同一套 API。
- 桌面程式可以連接外部後端，或啟動、停止、重啟自己擁有的後端子程序。
- Agent 採 Harness 工作迴圈；簡單任務不強制規劃，長任務則可建立結構化計畫並逐步驗證。
- Agent 原生工具使用 Go 實作，不依賴 `grep`、`find`、`diff`、`ssh` 等本機外部指令。
- Provider Router 以相同介面承載 OpenAI-compatible Chat Completions 與 OpenAI Codex Responses；認證與協定差異留在 adapter／bootstrap 邊界。
- 主系統可作為 MCP Client，連接本機 stdio、舊版 SSE 或遠端 Streamable HTTP Server，再把工具納入既有 Harness 權限與稽核流程。
- 管理層級固定為 `Workspace → Project → Session`，不同前端可同時操作不同 Workspace。

設計參考 `pi` 的 Agent Loop：模型輸出工具呼叫時執行工具，將結果加入訊息，再進入下一回合；沒有工具呼叫時完成回覆。參考來源：[pi agent package](https://github.com/earendil-works/pi/tree/main/packages/agent)。

## 最終目錄邊界

所有新程式均直接放在 `src/` 下，不再增加 `src/aiagent/` 包裝層。

```text
src/
├── cmd/
│   ├── server/                 # 可獨立執行的 HTTP 後端
│   ├── desktop/                # 本機控制器與 HTML UI 啟動程式
│   └── release/                # 跨平台發行建置與 SHA-256 manifest
├── domain/                     # Agent、Session、Plan、Run、Message、ToolCall、Event
├── ports/                      # Model、ToolRuntime、Repository、AgentEngine
├── harness/                    # Agent 工作迴圈、長任務計畫與完成度閘門
├── agents/harness/             # Harness Runner 的具體 AgentEngine adapter
├── memory/                     # 長期記憶 scope、召回與提示注入
├── tokens/                     # 不依賴 Provider tokenizer 的 token 估算
├── modelrouter/                # Provider ID → ports.Model adapter 路由
├── providerauth/               # ChatGPT／Codex OAuth PKCE、loopback callback 與 Token 保存
├── mcpclient/                  # stdio／SSE／Streamable HTTP MCP Client 與遠端工具轉接
├── netpass/                    # 選用的 NetPassClient 子程序與反向代理狀態
├── application/                # use case、Agent registry、run/session 協調與排程執行器；不依賴具體 Harness
├── adapters/
│   ├── openaicompat/           # 目前已實作的 Provider adapter
│   └── filestore/              # Workspace、Project、Session、Plan、Run、排程與記憶持久化
├── tools/
│   ├── registry.go             # 原生工具註冊、權限與統一執行入口
│   └── native/
│       ├── internal/toolutil/  # 參數、workspace 路徑與輸出限制
│       ├── files/              # directory 與 file read/search/compare/write/edit
│       ├── documents/          # PDF、DOCX、XLSX、PPTX 讀寫、範本、驗證、字型與視覺渲染
│       ├── plans/              # plan_get/create/step_update 計畫控制工具
│       ├── shell/              # shell_exec 與 OS platform adapter
│       ├── ssh/                # Go SSH client
│       ├── network/            # http_fetch 與外部網路邊界
│       └── memories/           # memory_search/remember/forget
├── transport/httpapi/          # REST、SSE、驗證、CORS、Problem JSON
├── bootstrap/                  # 設定與 dependency composition root
├── desktop/
│   ├── supervisor/             # 後端程序生命週期與紀錄
│   ├── httpui/                 # 本機 UI server 與後端 reverse proxy
│   ├── screencapture/          # 系統擷取介面與圖像剪貼簿 adapter
│   └── launcher/               # 跨平台開啟 Browser
└── web/console/                # 內嵌 HTML/CSS/JavaScript
```

新增 Provider 類型時，在 `adapters/<provider>/` 實作 `ports.Model`，並只在 bootstrap 工廠註冊 type；Workspace、Harness 與 HTTP 不依賴個別 Provider。新增 Agent 工具放在 `tools/native/<類別>`，Harness、HTTP 與前端都不直接 import 個別工具。

## Harness 工作迴圈

```text
收到使用者訊息
  → 寫入 append-only session transcript
  → 讀取 Session 計畫；長任務可由模型建立計畫
  → 經 Provider Router 呼叫模型（含目前可用工具 schema）
  → 有 tool_calls？
      ├─ 是：分組執行原生工具 → 依原順序寫入 tool result → 下一回合
      └─ 否：寫入 assistant final message → 完成 run
```

主要事件：

- `run.started` / `run.completed` / `run.failed` / `run.canceled`
- `agent.start` / `agent.end`
- `turn.start` / `turn.end`
- `message.start` / `message.delta` / `message.end`
- `message.thinking_delta`：模型 reasoning 內容的增量；只保留在目前 Run 記憶體，重播與重新載入 transcript 不還原
- `tool_call.delta`：工具參數的串流片段，帶 `index`、`tool_call_id` 與 `tool_name`
- `turn.usage`：Provider 回報的 token 使用量
- `tool.execution.start` / `tool.execution.update` / `tool.execution.end`
- `run.budget_exceeded`：Run 達到回合、wall-clock、token 或工具呼叫上限後正常收尾
- `run.approval_required` / `run.approval_resolved`：高風險工具的人工審核生命週期

同一個 session 採 single-writer gate，避免兩個 run 同時寫入同一份 transcript。不同 session 可並行。

### Session 計畫

計畫是 Session 內的獨立持久化有序佇列，不是 Prompt 文字或一般聊天訊息。使用者可從
Console 建立多份計畫、收合步驟與拖曳排序，Agent 遇到多步驟長任務時也可使用
`plan_create` 新增。兩者讀寫同一個 `PlanRepository`，因此切換對話、重新載入前端或後端
重啟都不會遺失進度。舊版單計畫 JSON 會在下一次寫入時自動升級為 version 2 集合格式。

`lock_plans=true` 時，每個 Session 最多只有第一份未完成計畫為 `active`，後續均為 `queued`；
active 完成或刪除後，Repository 會自動啟用下一份。`lock_plans=false`（預設）時，未完成計畫
可以同時為 `active`，Agent 可在不同計畫間切換；這不會取消同一 Session 的 Run single-writer
限制，跨 Session 才能平行執行 Run。兩種模式都要求排序列表完整且不重複；
已有步驟進度的 active 計畫不能被移到其他未完成計畫後面，避免 UI 顯示順序與 Agent 實際執行
順序分裂。

每一步由 Domain 強制依序轉移：`pending → in_progress → verifying → completed`。只有目前
步驟可開始；進入 `completed` 前必須已在 `verifying`，並提供實際工具檢查所得的
`evidence`。受阻時可標記 `blocked` 並保留原因。只要計畫仍有未完成且未受阻的步驟，
Harness 就會攔截模型過早產生的 final answer，要求繼續執行與驗證；攔截次數有上限，
避免 Provider 不遵循工具協定時形成無限迴圈。

## 工具供應階段

Session 有 `shell_exec` 時，每個 Run 從「系統工具優先階段」開始，本輪公開：

- **唯讀工具**（`file_read`、`directory_list`、`file_search`、`file_compare`、
  `memory_search` 與文件檢視等），仍受工具集設定與檢索結果限制。
- `shell_exec`，以及 Harness 計畫控制工具與已連線的 MCP 工具。
- `document_create`、`document_convert`；擴充工具集允許時也包含 `document_edit`。
- `wait_for`／`ssh_wait` 只有已啟用且由檢索帶入時才公開，不屬於固定核心工具。

其他寫入型內建工具（建立目錄、寫檔、文字編輯）與 `ssh_exec` 仍維持 Shell 優先：需要這類
副作用時先用 `shell_exec` 實際執行，Shell 真正執行失敗後才由備援階段公開完整目錄。人工拒絕、
Approval 中斷或 loop guard 都不算 Shell 失敗，不能藉此解鎖。

文件產出、轉換與編輯可直接使用專門工具，避免先尋找 Shell 套件或自行改用不符需求的格式。
較大的文件 schema 由工具檢索控制；其他文件副作用工具仍依原有階段規則供應。

唯讀工具提前公開是刻意的取捨：先前它們要等一次 Shell 失敗才解鎖，等於每個「讀檔案、盤點目錄」
的需求都固定多花一輪跑一個註定失敗的命令卻沒有任何產出。唯讀工具沒有副作用，提前公開不會
放寬權限邊界；原生高權限工具仍須 elevated profile 與人工 Approval，MCP 唯讀例外則只在管理者
信任 Server 的 `readOnlyHint` 時成立。階段提示也會列出本輪可直接呼叫的 MCP 工具名稱，避免模型
誤以為 MCP 要等到備援階段才能用而先花一輪描述計畫。

計畫工具屬於 Harness 控制工具，在系統工具優先與內建備援階段都可使用；實際檔案、系統或
外部狀態仍由當輪公開的工作工具處理。只有計畫工具的回合不消耗 autonomous work-tool
turn 配額，但仍受整體 `max_turns` 與已啟用的 `max_tool_calls` 限制。

Harness 另有結構化重複操作防護：已成功的相同副作用工具呼叫不會執行第二次；具
`atomic-replace` 能力的工具對同一資源成功改寫三次後，下一輪會停用工具並強制整理
目前結果。判定只使用工具定義、參數與執行結果，不分析模型自然語言。

失敗防護區分操作策略與內容修正：一般錯誤以工具、目標與控制參數辨識；錯誤指向
`cell_updates`、`blocks` 等內容欄位時，另把內容摘要加入 attempt key，避免把真正修正後的
輸入誤判成同一次失敗。同一工具對同一資源累計四次相同錯誤時仍會攔截後續嘗試，要求改變做法；
這不是繞過工具驗證、強制完成計畫或自動重做已成功的副作用。

### Run budget

每個 Run 可受 `max_turns`、`max_wall_clock_seconds`、`max_tokens` 與
`max_tool_calls` 限制。`max_tokens` 與 `max_tool_calls` 設為 `0` 時不限制，預設採此模式，
避免長任務因累計用量提前停止；兩者仍可從系統管理或設定檔重新啟用限制。
`max_wall_clock_seconds` 保留正整數上限，並可在系統管理調整。wall-clock 使用可取消的
context，會中止仍在等待的模型或工具；token 依 Provider 回傳的 usage 逐輪累計。
管理設定只套用到之後開始的 Run，執行中的 Run 保留啟動時快照。

達到已啟用的上限不是系統錯誤：Run 以 `completed` 收尾，`result.budget_exceeded` 與
`metadata.termination=budget_exceeded` 說明終止原因，並保留最後一則 assistant 訊息。

如果上限觸發時仍有尚未執行的 tool call，Harness 會寫入帶
`metadata.synthesized=true` 的 budget 工具結果。這些結果不代表工具曾執行，目的在於
避免正常的預算收尾破壞 assistant/tool 配對協定。

### Run／Session 用量與成本

Harness 會在每輪 Provider 回報 usage 時累加 input、output 與 total token；Run 收尾時由
Application 保存一次 `Run.Usage` 快照，即使 Run 失敗或取消，也保留錯誤前已收到的用量。
重試會建立新的 Run 並保留原 Run，因此每個 Run 的統計彼此獨立，不會因重試或重新讀取重複計算。

Session 的 `Usage` 不寫入 `session.json`，而是每次由既有 Run 清單即時彙總，並依
Provider／Model 提供 `by_model` 明細。`model_prices` 設定以 Provider ID 與模型名稱索引，
單價單位為每百萬 token 的 USD；沒有對應價格時只回傳 token，不提供估算金額。歷史 Run 的
估算成本在收尾時保存，日後調整價格表不會改寫既有紀錄。

Console 標題列顯示 Session 累計 token，以 K／M 縮寫並在 tooltip 提供精確值；沒有成本時隱藏
金額與分隔符號。重新提問不退還已消耗的 token，不能用累計用量推算當前上下文占比。

Session 彙總只涵蓋仍保留的 Run。RunRepository 整理掉舊紀錄後，對應用量也會從查詢與匯出
消失；這不是永久帳務總帳，長期統計應另行保存匯出。

### Human-in-the-loop Approval

`RequiresPermission` 的原生工具在實際執行前會建立 durable approval request，Run 狀態改為
`waiting_approval`。前端以 `POST /api/v1/runs/{run_id}/decision` 傳送 `approve` 或
`deny`；拒絕會成為帶理由的 tool observation，模型仍會進入下一輪調整做法，不會把
整個 Run 標成 failed。

核准時可另外傳送 `permanent=true`。Application 會先把核准狀態持久化到目前 Session，
再喚醒 Harness；同一 Run 的後續工具與日後該 Session 的 Run 都會略過逐次審核。這個
狀態不放在一般 Session 更新輸入中，因此 Client 無法在沒有 pending approval 時自行提權。

等待期間 Harness goroutine 只等待決策，不持有實體 session gate。Application 仍保留該
Session 對原 Run 的邏輯預約，因此其他已排隊 Run 不會插入尚未完成的 assistant/tool
協定區段。決策完成後原 Run 先重新取得 gate，才寫入 resolved 事件與工具結果。

### 工具分組執行

同一回合的 tool call 會切成群組：連續的 read-only 工具（`file_read`、`file_search`、
`file_compare`、`directory_list`、`document_inspect`、`document_read`、`document_compare`、`document_validate`、
`document_fonts`、`memory_search`）在同一群組內並行執行，其餘工具各自成組依序執行。

只有「連續」的 read-only 呼叫可以並行：`[read, write, read]` 之中兩個 read 隔著一個 write，
併發執行會讓第二個 read 讀到不確定的狀態。工具定義裡沒有的名稱一律視為非 read-only。
單一群組最多同時執行 8 個。

開始事件與結果寫入都依原本的 tool_calls 順序進行，因此 transcript 與事件流的順序不受併發影響。
`tool.execution.update` 經過序列化後才送出，因為下游的 durable event log 以遞增 sequence 寫入，
不可併發呼叫。**`BeforeTool` 與 `AfterTool` hook 因此必須是併發安全的。**

工具執行完成後的結果寫入使用不受取消影響的 context：工具可能已經產生副作用，結果不能因為
`CancelRun` 而消失；Run 狀態則會先立即標記為 `canceled`，讓 UI 不必等待底層工具收尾。未
執行的 tool call 不會留下假結果。

Run 建立與 HTTP request 生命週期分離：`POST` 先回傳 durable queued Run，背景 worker 使用後端持有的 context 繼續執行。所有 event 先 append 到 `runs/events/<run-id>.jsonl`，再通知目前的 SSE 訂閱者；訂閱通知只負責喚醒，Client 一律依最後 sequence 從 durable log 取資料，因此通知合併或短暫離線不會形成事件缺口。

SSE 使用 event sequence 作為 `Last-Event-ID`。Browser Console 斷線後最多重連三次，每次從最後完整事件補流；三次仍失敗才結束前端連線，後端 Run 本身不受影響。後端重啟時，原本 queued/running/paused/waiting_approval Run 會標記為可重試的 `server_restarted` failure，啟動期也會補齊缺少的 terminal event。

Browser Console 允許使用者在目前 Run 尚未結束時繼續輸入。這些訊息會先寫入該 Browser profile
的 IndexedDB Durable Outbox，內容包含 Session、輸入、附件 File、固定 Idempotency-Key 與目前
送出狀態；等目前 SSE 收到 terminal event 後依序建立下一個 Run。只有收到 Run terminal event
才移除 outbox item，網路錯誤、UI 關閉或重新整理都會保留可重試資料；`sending` 狀態在 UI
重開時會恢復為 `pending`。附件上傳成功後也保存 Attachment ID，避免重送時重複上傳。後端仍
以 Session single-writer gate 最後保護，因此其他 Client 同時送入同一 Session 時也不會交錯寫入
transcript。

UI 啟動時先讀取目前 Workspace 的 queued／running／paused／waiting approval Run，透過恢復
視窗讓使用者勾選，確認後才掛接所選 Run 的 durable SSE；中斷且可重試的 Run 保留重試入口。恢復
視窗按下取消時只停止該 Session 的 UI 恢復，不取消後端背景 Run，並在目前 UI 生命週期內記住
這個 Session 的排除狀態。之後主動開啟該 Session 時會重新查詢並連線，讓使用者能看見真實
狀態及操作停止；真正停止執行仍須 Run cancel。取消恢復不等於取消後端工作。

長對話左側以提問建立段落刻度，可預覽與跳轉，少於兩則提問時隱藏。串流文字本身表示進度，
沒有新文字超過兩秒且 Run 仍在處理時，狀態列重新顯示處理動畫；等待人工核准時不套用此動畫。

## 工作管理與系統能力

管理功能共用同一個後端狀態來源，不在 Browser 端自行推導或保存權限結果。

### 異常恢復、通知與 Run 控制

Filestore 啟動時會檢查所有 queued、running、paused 與 waiting approval Run。這些狀態代表上一個
後端程序未正常完成，因此會被標記為 `failed`、錯誤碼 `server_restarted` 並保留
`retryable=true`；Application 會補齊缺少的 terminal event；啟用通知中心時才會產生一次可重試摘要。
原始 Run 與事件不被覆寫，使用既有 retry API 建立新的可追蹤 Run。

通知以 `NotificationRepository` 持久化，最多保留 1000 筆，支援未讀、單筆／全部已讀與清除
已讀。通知中心由 `notifications_enabled` 控制，預設關閉；關閉時不建立新的 Run、等待工具核准
或版本更新通知，前端也不顯示通知按鈕。開啟後 Run 完成、失敗、取消、等待工具核准及後端重啟
中斷都會建立通知；`DedupeKey` 防止重播或重啟時重複建立同一則通知，既有資料不因關閉而刪除。

Run 控制提供 `pause`、`resume` 與 `cancel-all`。暫停只設定 durable 狀態，Harness 在
目前 Provider request、工具執行與事件寫入完成後，於下一個安全回合邊界等待；不強制中止已送出
的 Provider HTTP request。取消則會先將 Run durable 標記為 `canceled` 並送出 terminal event，
因此 UI 不會因第三方 Provider 不理會 context 而一直顯示執行中；底層請求仍會收到取消訊號並在
背景收尾。Run 狀態與控制事件共用序列化鎖，避免 `run.paused`／`run.resumed` 與 terminal event
使用重複 sequence；取消後的 late event 也不會排在 `run.canceled` 之後。等待人工核准的 Run
不接受一般 pause，必須核准、拒絕或取消。

### 診斷、搜尋、備份與權限中心

`DiagnosticsExport` 只輸出脫敏的管理摘要，移除 API token、DataDir 絕對路徑與憑證欄位；
不把對話全文放入診斷包。安全備份則保存 sessions、projects、workspaces、runs、events、
plans、attachments、memories、schedules、notifications 與非秘密的 service settings，明確
排除 Provider、OAuth、MCP 與 NetPass 憑證檔。還原前先保存 `backups/pre-restore-*.zip`，
只接受白名單資料目錄、拒絕絕對路徑、`..`、符號連結及超過 512 MiB 的解壓內容，完成後回傳
`restart_required=true`。

設定包是獨立的管理匯出，不是安全備份。`Runtime.ConfigBundle` 只讀取 `data_dir` 中已存在的
`providers.json`、`mcp-servers.json`、`netpass.json`、`service-settings.json`，依欄位名稱
遮蔽秘密，再加上 `manifest.json` 產生 ZIP。HTTP 層只提供 `GET /api/v1/admin/config-bundle`，
Console 從系統工具下載；不包含對話、附件、OAuth token 或環境變數，也沒有設定包專用匯入。
URL、帳號與命令列參數不是通用匿名化的對象，分享限制見 [安全設計](SECURITY.md)。

全域搜尋由後端掃描 Workspace、Project、Session、Message、Plan 與 Schedule，限制查詢 200
字元、最多 100 筆，只回傳 bounded snippet，不把完整訊息或檔案內容交給前端。唯讀權限中心
回傳後端 permission policy、工具的 standard/elevated 分類、可用性與待核准 Run 數量；不提供
修改 permission policy 或繞過 Approval 的 API。

版本更新檢查是固定目標的唯讀整合：只查詢 `VaderChen/NR-Intern` 官方 GitHub latest Release，
不允許 Client 提供任意 URL。Runtime 啟動後檢查一次並每 6 小時重查，管理 API 可要求立即檢查。
版本比較支援一般 semver 與本專案的 `1.YY.MMDDbHHmm`／`1.YY.MMDD build HHmm` 格式；啟用
通知中心時，新版本會透過現有 NotificationRepository 以 Release tag 去重，檢查失敗只更新狀態，
不影響 Agent 工作。

`GET /api/v1/admin/status` 同時回傳 `api_version`、`event_schema_version` 與後端
`capabilities`。Browser Console 在載入 Workspace 前檢查 API major version 與必要 capability；
缺少新能力時顯示可理解的不相容提示，不讓使用者在操作中才遇到單一端點 404。Run 建立時會
對 Session、輸入、附件 IDs、Provider 與 Model 產生持久化 Idempotency fingerprint；同一
Session 的同一 `Idempotency-Key` 只有在 fingerprint 相同時才回傳原 Run，內容不同則回傳
`409 Conflict`。

macOS 的 WKWebView 容器不會自動建立主選單，因此系統層級快捷鍵預設全部失效。桌面啟動時會補上
App 選單（⌘Q 結束、⌘H 隱藏、⌥⌘H 隱藏其他）、編輯選單（⌘Z／⇧⌘Z／⌘X／⌘C／⌘V／⌥⇧⌘V／⌘A）
與視窗選單（⌘M 最小化、⌘W 關閉）。安裝前以 selector 檢查是否已有同一動作，避免與 webview 版本
自帶的選單重複；⌘W 仍走既有的 `windowShouldClose:` 流程，工作進行中只隱藏視窗。Windows WebView2
與 Linux 瀏覽器由各自引擎處理 Ctrl 快捷鍵。

桌面生命週期方面，macOS 使用原生 status item；Windows 使用 Win32 Notify Icon，啟動就建立
Tray，左鍵開啟 UI、右鍵選單提供開啟與結束。Windows 沒有可用原生視窗時，Tray 仍持有桌面
程序與 Browser fallback 的生命週期；再次啟動會對既有 UI listener 發送 restore，不建立第二個
桌面程序。

## Workspace、Project 與多前端

Workspace 是最上層持久化邊界，Project 必須屬於一個 Workspace，Session 同時保存 `workspace_id` 與可選的 `project_id`。後端不保存「目前選取 Workspace」這種全域狀態；Browser 每個分頁以自己的 `sessionStorage` 保存選擇，因此多開前端時可以同時操作不同 Workspace。

Workspace Provider 採「集合＋預設值」：

- `provider_ids`：該 Workspace 已納入的 Provider 集合。
- `default_provider_id`：目前未指定 Session／Run override 時使用的 Provider，必須存在於集合。
- `model`：Workspace 預設模型。

目前 Workspace 前端仍是 Provider 單選，會寫入單元素集合；資料與 API 已可直接擴充成多選。
Workspace 的集合只負責自身預設，不限制對話選項。Session／Run 的 `provider_id` override 可選擇
任一全域已啟用 Provider；留空時使用 Workspace 預設值。`model` 同樣可由 Session／Run
override。停用或刪除仍被 Session 明確引用的 Provider 會回傳 conflict，必須先將該對話切換至
其他 Provider 或恢復 Workspace 預設。Provider Router 依 ID 選擇 adapter；同一 Session 維持
single-writer，不同 Workspace／Session 可並行。

Workspace 與 Project 都可以保存「職務說明」（`instructions`）：使用者只寫一次的常駐工作規則，
每次 Run 由後端依 Session 所屬層級組成 `## 職務說明` 段落，接在 Host 環境提示之後注入。
順序固定為 Workspace → Project，範圍較小者優先；提示同時寫明與使用者本輪明確要求衝突時，
以本輪要求為準，避免常駐指示反過來蓋掉當下的指示。內容與 `sandbox_roots` 一樣是後端產生的
保留欄位：`StartRun` 會先移除呼叫端自帶的 `metadata.instructions`，任何 API client 都不能藉此
往提示注入內容。單一層級上限 8000 字，因為這段文字每一輪都會佔用 Context 預算。

排程（Schedule）與 Project 並列在 Workspace 之下，但刻意不與 Project 或 Session 建立關聯：
使用者要的是「每天九點做這件事」，不是「維護一個長對話」。每個排程自己保存交辦內容、週期與
`sandbox_roots`；到點時由排程執行器建立一個全新的 Session 再送出交辦內容，因此每次執行都是
乾淨的上下文。詳見 [排程](#排程)。

Project 與 Workspace 刪除都不做級聯操作：只要還有子 Project 或 Session 就回傳 conflict，避免 UI 管理動作隱含遺失對話。

Project 可保存多個 `sandbox_roots`。建立與更新時由後端解析實體路徑、確認目錄存在、去重並拒絕檔案系統根目錄；Run 開始時才依 Session 所屬 Project 產生不可由 Client 覆寫的 Sandbox metadata。檔案與 Shell 工具對絕對路徑逐一檢查是否位於任一根目錄，相對路徑固定以第一個根目錄為基準，且既有 symlink 解析後仍須留在允許範圍。未設定 Project Sandbox 時，維持 Session 私有 `workspace/`。

## 排程

排程是 Workspace 層級的獨立實體，儲存在 `data/ai-agent/schedules/schedules.json`。週期只提供
固定間隔、每日與每週三種結構化設定，不使用 cron 字串：桌面使用者要的是可以在 UI 上完整
回顯與驗證的週期，而不是一段容易寫錯的表達式。每日與每週採後端所在時區的牆上時間，跨日
使用日期加法而非固定加 24 小時，夏令時間切換時仍停在使用者設定的時刻。

`application.Service` 內只有一個排程迴圈，隨 Service 的 root context 一起結束，每 20 秒檢查
一次到期排程。到點後的處理順序固定：先算出下一次時間，再建立 Session 與 Run；即使這次啟動
失敗，時間軸仍會往前走，不會每 20 秒重試同一個到期時間點。失敗原因寫回排程的
`last_status` 與 `last_error`，Console 會顯示在該列。

錯過的時間點不補跑。後端重新啟動時，`next_run_at` 落後超過 15 分鐘寬限的排程會直接對齊到
下一次；關機一週再開機時，使用者要的是下一次執行，不是一次湧出的補償 Run。落在寬限內的
排程仍會照常執行，短暫重啟不會漏掉。

排程建立的 Session 不屬於任何 Project，metadata 帶有 `schedule_id` 與 `schedule_name`；沙箱則
由排程自己的 `sandbox_roots` 決定，經 `RunInput.SandboxRoots` 這個不解析 JSON 的後端專用欄位
傳入，Client 無法自行帶入。刪除排程不會刪除已經建立的 Session。

## Session 與資料

每個 session 使用獨立目錄：

```text
data/ai-agent/sessions/<session-id>/
├── session.json
├── entries.jsonl
└── workspace/                  # Session 預設沙箱目錄，非管理層 Workspace

data/ai-agent/memory/
└── memories.json               # 跨 session 長期記憶與治理狀態

data/ai-agent/runs/
├── runs.json                   # Run 狀態與結果
└── events/<run-id>.jsonl       # 可重播的 Run event log

data/ai-agent/workspaces/
└── workspaces.json              # Workspace、Provider 集合與預設值

data/ai-agent/projects/
└── projects.json                # Project 與 Workspace 關聯

data/ai-agent/plans/
└── <session-id-encoded>.json    # version 2 有序計畫集合、步驟狀態與驗證證據
```

此處小寫 `workspace/` 只是**預設**沙箱：Session 所屬 Project 若設定了 `sandbox_roots`，
工具的範圍改由那些目錄決定，可以在 Session 目錄之外。詳見 `docs/ai-agent/SECURITY.md`
的「工具沙箱範圍」。

`entries.jsonl` 是 append-only transcript，序號單調遞增。append 不重寫 `session.json`；
`UpdatedAt` 於讀取時併入 transcript 的 mtime，避免一次 run 為了時間戳做十幾次完整檔案替換。

組裝 context 時只讀取上一次 compaction 之後的 entry：`ListEntriesAfter` 以 streaming decoder
取得每行的 `sequence` 與 `type`，只對真正需要的行做完整解碼。更早的內容已經在摘要裡，
每個 turn 重新解碼它們（尤其是數十 KB 的工具輸出）沒有用途，而那正是長 session 變慢的主因。
不論使用 Session 預設沙箱或 Project 的 `sandbox_roots`，既有 symlink 都會檢查實際目標是否逃逸。

JSONL 逐行讀取不使用 `bufio.Scanner` 的固定 token 上限。Provider 解碼後的 tool arguments
重新 JSON 跳脫時可能膨脹數倍；單筆合法 entry 不應讓整個 Session 在重啟後永久無法讀取或追加。

Harness 以「模型可用輸入預算」的 90% 為 soft limit。每輪組裝 context 時會比較本機估算值與
上一次 Provider 回報的 input usage，採較大者判斷；到達門檻後先送出
`context.compaction.started`，在同一 Run 事件序列中同步壓縮，再送出 `context.compacted`。
壓縮失敗則送出 `context.compaction.failed`，不會在背景工作與目前 Run 之間競爭事件順序。

完整訊息仍保留在 `entries.jsonl`，模型 context 則改用「較早紀錄摘要＋近期完整訊息」。摘要會
留下 `compaction` entry、`through_sequence`、壓縮前後 token、保留訊息數與觸發比例；後續
compaction 在既有摘要上增量合併。摘要輸入依時間採平方遞增的 recency 權重：越舊的內容分配
越少字元、壓縮越重，越新的工作狀態保留越多細節；模型摘要全部失敗時改用可重現的本機摘要，
避免單一摘要 Provider 的憑證或連線問題中止 Agent Run。

`context.summary_provider_id` 與 `summary_model` 可把摘要路由到較便宜的 Provider／Model；未指定時
沿用 Session 的模型。模型宣告的 context window 若連輸出保留額都容納不下，後端會明確拒絕，
不會虛構最低輸入容量後再交給 Provider 失敗。

### Tool call 協定不變式

送入模型的訊息序列一律經過修復：帶 `tool_calls` 的 assistant 訊息，其每一個 tool call 都必須有對應的
tool result；沒有對應 tool call 的 tool 訊息會被丟棄。中斷、當機或寫入失敗留下的孤兒 tool call
會補上明確標示的合成結果（`metadata.synthesized=true`）。

這個不變式由 context 組裝負責，而不是由寫入端保證。原因是任何一次中斷都可能讓 transcript 停在
不合法的狀態，而 OpenAI-compatible Provider 會直接拒絕整段對話，使該 session 從此無法再使用。
原始 `entries.jsonl` 不受影響，修復只作用於送進模型的檢視。

### Provider 串流事件

`ports.Model` 的 `domain.ModelEvent` 是有型別的事件，不是單純的 `{type, text}`：
`text_delta`、`thinking_delta`、`tool_call_delta`（帶 `ToolCallDelta`）、`usage`、`progress`。
工具參數在 adapter 內部累積完才一次吐出時，前端無法顯示工具呼叫的形成過程；
thinking 與 usage 同樣需要能被獨立辨識。相容服務的思考欄位名稱不一致，
adapter 同時接受 `reasoning_content` 與 `reasoning`。

Provider 有回傳 reasoning 時，串流期間透過事件顯示，但不寫入 assistant message 或持久化
transcript；重新載入對話時沒有歷史 reasoning 記憶。工具決策與工具輸出仍維持內部資料，不混入
最終回答內容。
Provider 只有在尚未送出任何可見模型輸出時才可重試；text、thinking 或 tool-call delta
任一已送出後若串流中斷，必須直接回報錯誤，避免 durable event log 出現重複片段。

### 模型能力與 Context 預算

Workspace、Session 與 Run 三層都能覆寫 model，而不同模型的 context window 相差一個數量級，
所以預算不能綁在單一全域設定值。`ports.ModelCatalog` 依 provider 與 model 回報宣告的限制，
`ContextManager` 以此推導預算：

```text
budget = context_window − max(context.reserved_output_tokens, model 的 max_output_tokens)
```

`context_window` 未宣告（0）時退回 `context.max_estimated_tokens`，預設為 262,144（256K budget）。
啟動時透過 Router 在背景探索模型清單與限制，共用 20 秒期限；探索失敗不阻擋啟動。各 adapter
依其能力來源、Provider 的 `context_window`／`max_output_tokens` 與個別 `model_limits` 合成
有效限制，不以模型名稱猜測容量。未宣告時 Run 日誌警告使用後備預算，Console 百分比加星號
並標示估算；Provider 表單另外顯示能力查詢值，不把人工欄位的 0 當成有效容量。

OpenAI-compatible adapter 的非零目錄回報值優先於人工設定；只有缺少相應回報欄位時才依
個別 `model_limits`、Provider 層級設定與 Context 後備預算決定。

### Token 估算

context 預算透過 `ports.TokenCounter` 估算，預設實作 `tokens.HeuristicCounter` 依字元類別加權：
CJK 字元約 1 token、ASCII 約 0.25 token。以英文為前提的 characters/4 對中文會低估 3～4 倍，
使 compaction 幾乎不觸發或讓請求超出 Provider 的 context window。估算涵蓋 system prompt、
摘要、訊息、tool call 參數，以及每次請求都會重送的工具 schema。低估的後果是請求被拒絕，
高估只會提早 compaction，因此預設權重刻意偏保守。

大型 tool result 只會在送入模型的 context view 中保留頭尾並縮減；原始工具輸出不會被改寫。若
固定提示本身已佔滿極小的 context window，ContextManager 會優先縮短摘要，再以頭尾保留策略
產生單一預算版 prompt，避免把正常 compaction 誤報為安全中止。每次 Run、Turn 與 Tool execution
另有 operation records，供當機診斷與後續復原使用。

## 紀錄與可觀測性

核心層以注入的 `*slog.Logger` 記錄，未注入時退回 `slog.Default()`。Run 範圍的 logger 帶有
`operation_id`、`run_id` 與 `session_id`，工具與 turn 的紀錄再加上 `turn`、`tool_name` 與 `duration_ms`。

紀錄刻意只包含名稱、狀態、時間與大小，**不包含工具參數、工具輸出或訊息內容**：
那些可能含有憑證與使用者資料，而且體積會淹沒紀錄。需要完整內容時看 session transcript。

主要紀錄點：run 生命週期與 panic（過去 panic 被完全吞掉，只留下一筆沒有原因的 failed run）、
每次 turn 的 stop reason 與 token 使用量、工具執行結果、context compaction、
Provider 重試、以及被拒絕的高權限工具呼叫（屬於安全事件）。

## 每輪提示的固定成本

`system`、`host`、`tools`、`phase`、`context` 這幾段在每一次模型請求都會重送，因此它們的長度
就是每一輪的固定思考負擔。Harness 只送與本輪實際可用能力相關的內容：

- 工具目錄依「工具供應階段」公開的集合產生，辦公文件家族留到備援階段。
- 探索與收斂策略依本輪公開的工具挑段落：沒有文件工具就不送辦公文件流程，沒有 SSH 工具就不送
  遠端部署驗證，沒有內建寫入工具就不送寫入生命週期。MCP 工具的副作用由遠端服務定義；唯讀
  例外遵守 `trust_annotations`，其餘仍套用 permission profile 與人工 Approval。
- Session 還沒有任何計畫時，計畫規則只送一句「簡單工作不必建立計畫」，完整的五步生命週期等到
  真的有計畫時才送。

以一句話的查詢為例（第一階段、無計畫），這幾段從 4,226 字降到 2,424 字；工具目錄同時從 18 個
工具（6,621 字）降到 12 個（4,574 字）。省下的不只是 token：多送的每一段都在鼓勵模型多想一輪。

複合問題（「有多少部門跟人員」）需要兩份彼此獨立的資料。instruction 協定原本限制一輪只能輸出
一個 JSON object，模型只能先花一輪規劃怎麼分次查；實際執行端本來就支援一輪多個工具呼叫
（唯讀且免核准的會並行），因此協定放寬為可在同一輪輸出多個 `tool_use` object 或一個 JSON
陣列，上限沿用 `maxParallelTools`。批次中只要有一個工具名稱不存在，整批進入協定修正，不會
只執行一半。彼此有相依關係的呼叫仍應分輪，先取得前一個結果再決定下一個。

使用者不會每次都指名「使用某某 MCP」。為了讓模型不必靠英文工具名跨語言猜測，工具供應階段
會列出本輪可呼叫的 MCP 工具與其說明（`mcp__mars-mes__query_work_orders：查詢製令`），
instruction 模式的工具目錄也帶上 `label`（MCP 的 title），並明確要求「直接呼叫語意最接近的
工具，不要因為使用者沒有指名服務就改成詢問或先描述計畫」。說明品質取決於 MCP Server 自己
宣告的 title 與 description；Server 什麼都沒宣告時只能顯示工具名。

Server instructions 屬於整個 MCP Server，過去在 instruction 模式被複製到該 Server 的每一個
工具定義裡（20 個工具就是同一段文字出現 20 次），現在改為每個 Server 只列一次。

native 模式下工具清單已經走 OpenAI-compatible 的 `tools` 欄位，文字提示只補「必須透過
tool_calls 呼叫、不可要求使用者代為執行」這類規則，不再重列一次工具名稱與說明——同一份
資訊送兩次，對小型與本機模型只是多一份要讀的內容。Server instructions 不在 `tools` 欄位裡，
仍會保留。

`## 使用者可見的工作進度` 只在這次 Run 可能產生 reasoning 時才送出；thinking 設為 `none`
時整段略過，避免多送一段用不到的寫作規範，也不會誘使模型額外生成進度文字。

模型看到的工具目錄只保留挑選與呼叫工具需要的欄位（name、label、description、input_schema、
output_schema、read_only、requires_permission）。`platforms` 與 `capabilities` 是工具目錄 UI
用的中繼資料，留在 `domain.ToolDefinition` 供管理介面使用，但不進提示——六個內建工具實測可省
734 字（4,661 → 3,927），工具越多省得越多。

工具結果本身也是每一輪的成本。宣告 output schema 的 MCP Server 會依規格同時回傳
`structuredContent` 與內容相同的 text block；兩份都收進工具結果，等於同一筆資料在 transcript
存兩次、之後每一輪都重讀兩次。`mcpclient` 只在文字區塊沒有涵蓋同一份 JSON 時才附加結構化結果，
結構化資料仍完整保留在工具結果的 metadata 供介面使用。

`src/harness/mars_scenario_smoke_test.go` 用真的 MCP Server 重現實際情境並量出每一輪送出的字數：
先問製令數量再換題目問部門、複合問題一輪送出兩個工具、清單型查詢回傳 500 筆機台、
超大結果被 context 預算截斷、以及同一輪兩個需要核准的工具。改動提示、工具結果格式或協定時，
這組測試會直接顯示成本變化。

單一工具結果進入模型前受 `context.max_tool_result_characters`（預設 24,000 字）限制；截斷時附上
的說明會要求模型縮小查詢範圍或加上篩選條件再查一次，而不是憑半份資料推測整體。清單型查詢
（「列出所有機台」）即使被截斷也不會讓提示無限膨脹，但每一輪仍要重讀這 24,000 字——真正的
解法是讓 MCP 工具支援統計或分頁，而不是回傳整份資料集。

## 工具集與工具呼叫協定的開關

工具目錄每一輪都會整份進入提示，工具越多，小型與本機模型越慢也越容易挑錯。因此有三個
可在管理介面即時切換、不需重啟後端的開關：

- **擴充工具集**（`extended_tools`，預設關閉）：關閉時只公開精簡集合——`shell_exec`、
  `file_read`、`directory_list`、`file_search`、四個文件工具（`document_inspect`、
  `document_read`、`document_create`、`document_convert`）與三個計畫控制工具，取設定檔
  `allowed_tools` 的交集，不會放大原本的授權範圍。需要寫入、編輯、SSH 或記憶工具時再打開。
  檢索（下一項）會在這個結果之上再過濾一次。

  文件工具原本不在精簡集合裡。實際後果是使用者說「把結果轉成 Excel」時，Agent 只剩
  `shell_exec` 可用，於是寫了一個沒有 UTF-8 BOM 的 CSV——Excel 打開全是亂碼，而且那也
  不是使用者要的格式。精簡集合當初是為了壓低提示大小，這件事現在由工具檢索處理，
  沒有必要再用「拿掉能力」來換。
- **工具檢索**（`tool_retrieval`，預設開啟）：見下一節。內建工具與 MCP 工具都適用。
- **工具呼叫協定**（`tool_call_mode`，預設 `native`）：多數推論引擎會以 grammar 約束
  `tool_calls` 輸出，比要求模型自行輸出 JSON 指令可靠得多；Provider 不支援原生工具呼叫時
  才切到 `instruction`。Provider 設定頁的「測試」會回報該 endpoint 是否支援。

三者都寫入 `service-settings.json`，設定檔提供的值是初始值。切換後下一個 Run 生效，
執行中的 Run 保留啟動時的設定。

## 工具檢索

外掛型 MCP Server 動輒公開數十上百個工具。工具定義（名稱、說明、JSON schema）每一次請求
都整份送出，實測有 Server 讓一句「HELLO」變成 111,172 tokens——本機模型光是預填提示就跑
不完。精簡工具集那個開關幫不上忙：它砍的是 7 個內建工具，佔比不到 1%。

問題也不是「哪些工具用不到」——使用者通常全部都要用——而是「這一輪用得到哪些」。
因此 `harness.toolRetriever` 在每個 Run 開始時對工具目錄做一次檢索。內建工具同樣納入：
擴充工具集打開後有近二十個內建工具，`document_*`、`ssh_*` 的 schema 都不小，而任何一次需求
通常只會用到其中一兩個。核心工具（`coreToolNames`：Shell、讀檔、列目錄、搜尋與計畫控制）不參與檢索——
階段提示直接點名它們，少了任何一個模型會先卡一輪。

`wait_for` 與 `ssh_wait` 刻意**不是**核心工具。它們一度被列入，理由只是「階段提示提到
它們」——那是錯的理由：這兩個工具會實際阻塞（`wait_for` 單次最長 30 分鐘），而且是唯讀
工具、不需要人工核准。永遠掛在目錄裡的結果是小模型隨手呼叫一次，整個 Run 就安靜停住，
畫面上與當機無法區分。需要等待時由檢索帶出來即可。

1. **第一層過濾**：以這次的使用者輸入、加上同一個 session 最近幾則使用者訊息為查詢，
   取相關度最高的工具（上限 `mcpRetrievalLimit`）
   進入模型目錄。工具名稱、標題與說明建立索引；ASCII 取詞、CJK 取 bigram，因此中文問題
   不需要斷詞就能對上中文說明。出現在 60% 以上工具裡的共通詞權重歸零，避免外掛型 Server
   一律叫 `*_query` 的命名讓任何問題都命中整份目錄。目錄在 `mcpRetrievalThreshold`
   （12 個工具）以下時完全不啟動，小型 Server 行為不變。
2. **沒被選中的工具照樣存在**：模型可呼叫 `find_tools`，以關鍵字取回工具名稱與參數
   schema（過大的 schema 會降級成只保留形狀，而不是截斷成無法解析的 JSON），取回後即可
   直接呼叫。這是檢索失準時的退路，因此這個工具永遠在目錄裡，且提示會明講「目錄沒列出
   不代表做不到」。
3. **目錄與可呼叫集合刻意不同**：目錄決定模型看得到什麼，執行端接受的是完整工具集合。
   模型若已從 history 或使用者指名知道工具名稱，直接呼叫就會執行，不必多花一輪重新檢索。
   呼叫過與取回過的工具會留在該 Run 的目錄裡，連續使用同一個工具不會每輪重找。

實測（`tool_retrieval_smoke_test.go`）：67 個工具的目錄，工具負載從 30,210 字降到 1,399 字。

### 手動壓縮

自動壓縮的門檻是以「送出前會不會爆掉」為準。使用者知道自己接下來要貼一大段東西時，
那個門檻剛好擋住他——上下文用量彈窗的「立即壓縮」把決定權交回去
（`POST /api/v1/sessions/{id}/context:compact`）。執行期間沿用與自動壓縮相同的
「正在壓縮上下文」指示器：按鈕在彈窗裡，沒有這個提示，對話區完全沒有反應。

與自動壓縮共用同一條 `compactMessages`，摘要格式、transcript 紀錄與後續讀取完全一致，
只有 `reason` 標為 `manual` 而不是 `context_budget`。兩個保護條件：

- **Session 有進行中的工作時拒絕**：壓縮會寫入 transcript，而 Run 正在同一份 transcript
  上讀寫，兩邊同時動會讓那一輪送給模型的歷史跟畫面上的對不起來。
- **壓完不會變小時保持原狀**：短對話的摘要可能比被摘要的訊息還長。自動壓縮遇不到這件事
  （它只在 context 已經很大時觸發），但手動按鈕沒有那層保護；判斷在寫入 transcript 之前，
  因此放棄是乾淨的，對話完全沒有被動過。

## 對話歷史的字元上限

歷史整理有兩個獨立觸發條件：token 使用量達到輸入預算的 90%，或整形後的歷史字元數超過
`context.max_history_characters`（預設 60,000）。後者用來補足 JSON、代碼與識別碼等內容的
token 估算誤差；單則工具結果會先套用 `max_tool_result_characters`，避免單一超大結果誤觸發。

字元條件超量時會縮小保留訊息數，再依既有摘要流程壓縮。組合 prompt 前的
`Runner.enforceHistoryCharacterLimit` 再檢查一次，必要時由舊而新移除模型可見的歷史，
並修補 tool call／result 配對。它不刪除持久化 transcript，也不改變 Run 的累計 token、
工具呼叫或 wall-clock 預算。

這不是整份模型請求的硬性上限：至少保留最新一則，且 system prompt、工具 schema、當前輸入
與配對修補仍可能增加大小。因此仍需依 Provider 能力設定預算，不應把字元上限當成容量保證。

每輪送出前的 `model request` 日誌提供 `tools`、`tool_chars`、`steering_chars`、
`history_messages`、`history_chars`、`user_chars` 與工具名稱，不記錄本文。歷史被最後防線
裁切時另有 WARN，供區分模型回應慢與請求負載過大。

## 工具呼叫相容性

原生模式沒有 `tool_calls`、卻在回答中輸出疑似工具參數時，Harness 會依可呼叫工具的 schema
比對 JSON 物件或陣列，進入有限次數的協定修正，要求模型改用工具呼叫欄位。修正耗盡會以
部分完成收尾；JSON 文字本身不代表檔案已建立或工具已執行。這是啟發式偵測，不是語意理解保證。

參數校正集中在 `src/schemaargs`，原生工具（`tools.Registry`）與 MCP 呼叫共用同一份。
先前兩邊各有一份幾乎相同的實作然後開始分岔——原生端陸續補上布林、數字與巢狀遞迴，
MCP 端停在只處理最上層的物件／陣列，於是同一個模型犯同一個錯，走原生會被救回來、
走 MCP 就失敗；這種差異從各自的檔案裡看不出來，抽成同一份才不會再飄。

校正依 schema **遞迴**進行，只往下走 `object.properties` 與 `array.items` 兩種語意明確的
構造（不處理 `$ref`／`oneOf`：猜錯會默默改壞一個本來正確的參數）。宣告為字串的位置接受
數字、布林與 null（表格的數量欄寫成 `264` 而不是 `"264"` 是最自然的寫法）；宣告為數字或
布林的位置接受 `"2"`、`"true"`。字串化的物件與陣列會被解回結構，並對部分非標準 JSON
嘗試格式修復（尾逗號、單引號、裸鍵、Python 字面值、彎引號、缺少的右括號，上限四個）。

**結構寫錯不轉**：一列表格寫成物件不是型別問題，硬轉只會把錯誤藏進文件內容裡；
這類情況保留原值交由工具驗證，並由工具回報可以照抄的正確寫法。純文字欄位不因看起來
像 JSON 而轉型。校正不改變 allowlist、權限、Sandbox 與輸入大小限制。

## 實驗性回憶空間

`memory_space` 預設關閉，透過 Config、service settings 與 Console「實驗性功能」頁保存，
Runtime 以原子開關即時套用至 `memory.Manager`。Agent 的記憶工具統一經過 Manager，啟用時
套用種類／秘密樣式檢查、近似去重、Project → Workspace 的預設 scope 與召回排序。
明確的 `memory_scope` 優先，唯一例外是記憶體隔離 Session（見下方 RAM disk 一節）；仍遵守既有
`memory.enabled`、`auto_recall` 與 `allow_writes`。

自動召回預設最多 6 筆、1200 UTF-8 bytes（包含包裝說明），依文字相關度、新鮮度與 confidence
排序；初始召回留在後續 context，符合工具重複失敗條件時另補一次、只注入下一輪。
`memory.recalled` 帶引用 ID、筆數與截斷狀態，失敗觸發另有 `trigger=tool_failure`，Console
以筆數與原因顯示。模型主動查詢不受這個自動注入窗口限制。

同 scope active 記憶以 500 筆為整理目標，超量時軟性淘汰較低價值內容。種類與憑證樣式可由
程式檢查，可重用性、可推導性與矛盾則不能完全自動判斷；明確取代使用 `supersedes`。
實作、參數、限制與 30 天失效記憶保留規則見 [回憶空間](MEMORY_SPACE.md)。

## 長對話索引與儲存保留

`SessionRepository` 的記憶體索引只存每筆 entry 的 sequence、type 與 byte offset，首次掃描
header，後續以二分搜尋及 seek 讀取指定頁。append 同步更新索引；大小不符可重建，無法建立
索引時退回掃描，不掩蓋真正的 JSON 損毀。`ListEntriesPage` 從 HTTP 經 application、agent
一路傳到 Repository，避免每翻一頁都完整解碼 transcript；既有游標契約不變。

前端訊息查找直接使用 ID 屬性選擇器，處理計時只查當前 operation 的節點；段落刻度保存目標
節點參照，減少長對話捲動時的重複查找。

RunRepository 在啟動與 Save 時，以總數 500 為整理基準，依建立時間淘汰最舊範圍中已結束的
Run，未終態一律保留，所以不是硬性上限。Runtime 啟動後及每 30 分鐘另清理事件檔：保留最新
50 筆 Run 與所有未終態 Run 的事件，其餘及孤兒事件檔移除。非 active 且 UpdatedAt 早於
30 天前的記憶也會永久移除，與回憶空間開關無關。

這些固定保留值目前不接受配置。Session transcript 不因維護而刪除，但舊 Run API、用量
快照、事件重播與失效記憶稽核資料可能不再可用；升級前須備份，長期稽核另存匯出。

## 完成度判定

Harness 過去只看「這一輪有沒有 tool_calls」就接受模型的完成宣告。模型可以在工具失敗之後
直接產出一段聽起來已完成的文字，而後端沒有任何機制察覺宣稱與實際狀態不一致。

`completionTracker` 追蹤本次 run 內尚未解決的工具失敗，判定完全來自執行記錄，不解讀模型文字：
某個工具失敗後，同一個工具名稱只要在之後成功執行過一次，就視為已解決（那正是模型自我修正的正常樣態）。

模型給出不含 tool_calls 的最終回覆時，若仍有未解決的失敗，Harness 會：

1. 寫入 `completion_check` transcript entry 並發出 `run.completion_check` 事件。
2. 把追問內容注入**下一輪的 system prompt**（不是偽裝成使用者訊息，避免污染對話記錄）。
3. 繼續迴圈，讓模型面對事實後重新決定要繼續工作還是誠實說明未完成的部分。

完成度閘門另外處理一種同樣「宣稱與實際不符」的情況：**整個 run 一個工具都沒有執行**。
模型很常先輸出一段「我會先確認⋯⋯再讀取⋯⋯」的計畫，然後把這段話當成最終回答交出去，
使用者看到的是一個承諾，實際上什麼都沒做。判定同樣只用執行記錄（工具執行次數為 0，
且該輪確實有工具可用），不解讀模型文字；追問內容要求模型二選一：需要外部狀態就直接輸出
工具指令，不需要工具就直接回答。`run.completion_check` 事件與 transcript entry 會帶
`reason=no_tool_executed`，與「工具失敗後宣稱完成」區分。

兩種追問共用同一份額度，由 `max_completion_checks` 限制（預設 1，設為 0 停用），
避免無止境拉扯；純聊天的回答最多只會多一次追問。

追問與否必須在寫入 assistant 訊息**之前**決定：被追問的那一段只是中間產物，會標記
`internal=true`、`phase=completion_check` 保留在稽核 transcript，但不出現在對話裡。
少了這個順序，使用者會看到兩份幾乎一樣的答案——被追問的那份，加上重新產生的那份。
計畫完成度閘門原本就是這個作法。

System prompt 另外約束回答格式：多筆項目、逐項清單或每筆有多個可比較欄位時優先用 Markdown
表格（Console 已支援表格渲染與橫向捲動），並說明是否還有未列出的項目；只有一兩筆或每筆只有
單一值時不必硬做表格。

每輪提示要求以使用者熟悉的業務語言描述能力與來源，不直接列出內部工具識別字、函式名或
API 端點；只有使用者明確詢問工具或整合方式時才揭露。這是表達規則，不是機密過濾機制。

收斂階段的提示另外要求答案交代依據：實際查了哪些資料、查詢範圍與過濾條件、
支撐結論的關鍵數據，以及取樣、時間點等會影響判讀的限制。只回一句結論（例如
「目前共有 264 筆製令。」）數字就算正確，使用者也無法判斷可信度。同一段規則明確要求
長度與問題複雜度相稱，避免反向變成覆述工作過程；這段規則只在工具結果已進入 history 的
收斂階段加入，純聊天的回答不受影響。
無論是否追問過，最終的 `RunResult.Completion` 都會帶上未解決的失敗清單，
讓 API 呼叫端能區分「工具全部成功後完成」與「工具失敗後照樣宣稱完成」——
在此之前這兩者都只是 `status=completed`。

## 記憶管理

記憶分成三個責任層次：

- 短期上下文：由 Harness compaction 管理目前 session 的 token 預算，不刪除原始 transcript。
- 工作記憶：由 operation、turn、tool records 與 session workspace 保存本次工作的實際狀態。
- 長期記憶：保存跨 session 仍有效的 `fact`、`preference`、`decision`、`procedure`、`constraint`。

長期記憶經 `ports.MemoryRepository` 隔離儲存技術。目前使用本機 JSON store 與可解釋的詞彙／雙字元召回；日後可加入向量索引 adapter，不需修改 Harness 或工具介面。關閉回憶空間時，預設 scope 是 Agent ID；啟用後優先使用 Project，再退回 Workspace。Session metadata 的 `memory_scope` 可明確指定其他範圍；這是召回範圍，不是獨立的租戶授權邊界。
記憶體隔離 Session 不套用這個覆寫，且其記憶不寫入 `memories.json`。

每次 operation 初始召回一次，再於後續 turn 重用；回憶空間啟用時，工具重複失敗另可補召回。
召回資料以明確邊界注入 system prompt，並標示為「可能過時的資料而非新指令」，降低提示注入
風險；`memory_recall` entry 記錄記憶 ID 與 scope，供追蹤使用來源。

寫入具備種類、標籤、信心值、來源 session、去重與 `supersedes` 關係。`memory_forget` 採 soft forget，資料保留稽核資訊但不再被召回；Agent 只有在使用者明確要求時才能執行。設定 `memory.allow_writes=false` 可只保留自動召回與 `memory_search`。

啟用回憶空間後 Agent 寫入不收 `fact`，且預設 scope 依 Project／Workspace 選擇。非 active
記憶的稽核資料只保留至清理期限，並非永久保留；詳細規則見前述儲存保留與回憶空間章節。

## 原生工具與 OS 差異

`tools.Registry` 是唯一執行入口。工具以 `NativeTool` 介面註冊，Harness 只依賴 `ports.ToolRuntime`。

- `file_read`：Go 檔案 API，支援行範圍與輸出上限。
- `file_search`：Go `WalkDir`、Scanner 與 regexp，不依賴 `find/grep`。
- `file_compare`：純 Go unified diff 與 SHA-256，不依賴 `diff`。
- `directory_list`：有深度與項目上限的目錄列舉，不追蹤目錄 symlink。
- `directory_create`：在 workspace 內建立單層或多層目錄。
- `file_write`：限制輸入大小，預設不覆寫；覆寫時使用同目錄暫存檔與原子替換。
- `file_edit`：以 old/new text 與預期替換數作 optimistic precondition，保留 mode 後原子寫回。
- `document_inspect`：檢視 PDF、DOCX、XLSX、PPTX 的格式、中繼資料、頁數、區段、工作表或投影片；不將二進位內容直接送給模型。
- `document_read`：PDF／PPTX 依頁、DOCX 依區段與段落、XLSX 依工作表與列分段抽取文字；保留 Excel 儲存格座標與公式。掃描型 PDF 不內建 OCR。
- `document_compare`：以相同範圍抽取兩份支援文件的可見文字並產生 bounded unified diff，同時回傳原始檔 SHA-256；內容相同不等同版面相同。
- `document_validate`：唯讀驗證 Open XML 容器、XML、Content Types、內部關聯、必要部件與格式特有結構；掃描 XLSX 公式錯誤，辨識巨集、外部關聯與嵌入物件，並回報視覺渲染後端狀態。
- `document_fonts`：唯讀探索應用程式、使用者與系統 TrueType 字型，使用 SFNT cmap 驗證指定文字的字形覆蓋率；輸出不揭露絕對路徑。
- `ask_user`：工作進行到一半需要使用者做抉擇時跳出選單，選項之外一律附自訂輸入欄。
  阻塞在工具裡而不是結束 Run 再開一輪——需要抉擇時 Agent 正做到一半，中斷再重來會讓它
  把已經確認過的事重新查一遍；對模型來說這只是一個比較慢的工具。等待狀態只在記憶體裡
  （`src/question`），工具每五秒重送一次事件讓重新連線的介面把對話框叫回來，逾時（預設
  10 分鐘）與取消都回傳「未回答」而不是錯誤：使用者沒有義務回答，把它當失敗會讓迴圈防護
  開始計數。同一輪不與其他工具並行，因為使用者一次只看得到一個對話框。
- `document_create`：以結構化輸入建立 DOCX、XLSX、PPTX、PDF、Markdown、TXT、CSV 或 HTML。純文字格式沿用同一組輸入結構（blocks 對應 Markdown／TXT／HTML，sheets 對應 CSV），不另開工具——它們原本沒有第一級建立路徑，會被 Shell 優先政策推到 heredoc，中文與引號在那裡最容易出錯。也可指定同格式 `template_path`，在不重建版面的情況下以 replacements、cell_updates 或 annotations 填入既有範本。Unicode PDF 優先使用指定字型，未指定時自動選擇完整覆蓋的系統 TTF。
- `document_edit`：來源與輸出路徑分離，保留原檔。DOCX/PPTX 可跨相鄰文字 run 精確替換，XLSX 可更新指定工作表儲存格或文字，PDF 則保留原頁並疊加文字、線段或方框。
- `document_convert`：elevated 工具；由固定探索的 LibreOffice 將 Office／OpenDocument 文件轉成 PDF，或將舊式 DOC／XLS／PPT 與 ODT／ODS／ODP／RTF 遷移到同家族 Open XML。來源與輸出不可為同一路徑。
- `pdf_pages`：elevated 工具；以純 Go PDF 匯入器完成合併、擷取、重排與分批拆分，保留來源檔與各頁尺寸，單次最多 500 頁。
- `document_render`：elevated 工具；PDF 由 Poppler 輸出逐頁 PNG，Office 文件先用獨立 LibreOffice profile 轉成 PDF。後端只從固定環境變數、PATH、封裝資源與標準安裝位置探索，不接受模型指定任意 executable。
- `shell_exec`：必要高權限工具；Unix 使用 process group，Windows 使用 Job Object，取消或逾時會終止子程序樹；子程序只繼承必要 OS 環境，另有不經 shell 的 direct 模式。
- `ssh_exec`：使用 Go SSH client；連線憑證只存在後端 profile，模型只取得 profile 名稱。初始連線預設最多三次並使用 keepalive；工作中斷線不自動重跑遠端命令，以免重複副作用。
- `wait_for`：不執行命令的可取消等待，具有最大秒數與進度事件；適合讓非同步上傳或服務啟動完成後再進行下一次檢查。
- `ssh_wait`：只輪詢唯讀、冪等的遠端檢查命令，每次檢查重新建立 SSH session，支援預期 exit code、stdout 包含／完全相等與連續穩定檢查；逾時不會被視為部署成功。
- `http_fetch`：唯一由 Agent 直接指定任意網址的內建工具。以 Go `net/http` 讀取 http／https 資源，回傳文字內容；HTML 會先轉成純文字再送進上下文。回應大小、逾時與轉址次數都有上限，localhost 與私有網段預設允許，並且可由管理介面即時關閉。
- `memory_search`：查詢目前 scope 的有效長期記憶。
- `memory_remember`：保存耐久資訊並支援去重與取代舊記憶。
- `memory_forget`：依明確要求軟性遺忘，保留稽核狀態。

`http_fetch` 是唯一可由模型自行決定目標網址的內建對外工具，因此多一層閘門：`NativeTool` 可選擇實作 `tools.ToggleableTool`，
關閉時工具不會出現在模型的工具清單，執行請求也直接拒絕。開關存在 `ServiceSettings`，由管理介面
即時套用，不需要改設定檔或重啟後端——工具留在清單上但保證失敗，只會讓模型反覆重試。
私有網段的判斷放在 Dial 階段而不是網址解析階段，因此 DNS 重新綁定與轉址都由同一條規則攔下；
Client 也刻意不使用系統 Proxy 設定，避免實際連線目標與網址主機不一致而讓檢查失效。

Shell 與 SSH 必須同時符合：後端 `allow_elevated_tools=true`、工具在 allowlist、session permission profile 屬於
`permissions.elevated_profiles`。profile 由後端 `permissions` 策略指派，預設不接受 API request 指定；
未宣告任何 elevated profile 時 fail closed。詳見 `docs/ai-agent/SECURITY.md`。

`document_inspect`、`document_read`、`document_compare`、`document_validate`、`document_fonts` 是 read-only；`document_create`、`document_edit`、`document_convert`、`pdf_pages`、`document_render` 是 elevated 寫入工具，須通過 allowlist、permission profile 與單次 Approval。所有模型提供的文件、範本、字型與輸出路徑都必須通過 Project／Session Sandbox。單一文件預設上限 128MB，Open XML 單一解壓項目上限 64MB，文字讀取／驗證／比較輸出沿用 `max_tool_output_bytes`，建立／編輯／PDF 頁面整理的結構化參數沿用 `max_file_input_bytes`。PDF 頁面操作單次最多 500 頁，渲染單次最多 200 頁、最高 300 DPI、總輸出上限 512MB。文件寫入先在記憶體或隔離暫存目錄完成格式驗證，再以同目錄暫存檔原子替換；編輯與轉換工具禁止來源與輸出使用同一路徑。

工具參數、格式差異與範例見 `docs/ai-agent/DOCUMENT_TOOLS.md`。

大型文件應先呼叫 `document_inspect`，再縮小頁面、段落、工作表列或投影片範圍；舊式二進位 DOC／XLS／PPT 不由原生 reader 直接讀取，可先透過 `document_convert` 遷移。Open XML 編輯與範本填入只重寫被修改的 XML part，其餘 ZIP part 原樣帶入新檔。PDF 文字抽取使用純 Go reader；沒有文字層時回報需要 OCR，不臆測掃描影像內容。PDF 建立／文字標註若含非 ASCII 字元，會先驗證指定 Sandbox `.ttf`，未指定時從固定的應用程式／系統字型目錄探索完整覆蓋的 TTF；找不到就拒絕輸出缺字文件。既有 PDF 的「編輯」採頁面匯入後疊加標註；頁面合併、擷取、重排與拆分則由 `pdf_pages` 明確處理，不宣稱能原地重排既有 PDF 文字。

結構驗證與視覺驗證是兩個獨立階段。`document_validate` 的 `valid=true` 只代表沒有結構 error，回傳 `visual_check=not_run`；成功呼叫 `document_render` 並檢查所有 PNG 後，呼叫端才可宣稱排版已驗證。LibreOffice／Poppler 不存在時，建立、讀取、編輯與結構驗證仍可使用，但視覺交付閘門不可標記為通過。

`directory_create`、`file_write`、`file_edit`、`document_create`、`document_edit`、`document_convert`、`pdf_pages`、`document_render` 都屬於寫入型高權限工具，使用相同閘門。`ResolvePath` 除了檢查目標，也會解析最深的既有父路徑；即使新檔尚不存在，仍不能藉父層 symlink 寫出 workspace。Unix 與 Windows 的檔案替換分別使用原生 rename／MoveFileEx 語意。

## MCP Client 與外部工具

`mcpclient.Manager` 讓主系統作為 MCP Host，支援本機 `stdio`、舊版 `sse` 與遠端
`streamable-http`。OpenAI-compatible Provider 預設使用原生 `tools`／`tool_calls` 欄位，
因此 MCP 工具會直接交給模型選擇，不再只依賴文字指令格式。
已啟用的 Server 會在背景暖機，完成 `initialize` 與 `tools/list` 後，把遠端工具轉為
`domain.ToolDefinition`；Server 通知工具清單變動時會重新載入。公開工具名稱會加入 Server
命名空間，過長時以穩定雜湊截短，避免不同 Server 的同名工具互相覆蓋。

MCP 工具與原生工具進入同一個 `ports.ToolRuntime`，並先通過同一個 elevated permission profile。
唯讀工具是否免逐次人工 Approval 取決於 Server 宣告的 `readOnlyHint` 與管理者設定的
`trust_annotations`；這個選項**預設開啟**（設定檔未提供時採預設值，明確設為 false 則沿用）。
未信任的 Server 或非唯讀工具仍須逐次核准。豁免會發出 `run.approval_skipped`
（`reason=read_only_tool`）留下紀錄，`trust_annotations` 同時仍用於唯讀並行排程。

記憶體隔離專案是第二種豁免：這類 Session 的所有工具都免逐次核准，紀錄的
`reason` 為 `ephemeral_project`。判斷依據是 Run metadata 的 `ephemeral_project`
旗標——與 `sandbox_roots` 同屬後端保留欄位，Client 夾帶的同名值會先被清除，
因此不存在「宣告自己是記憶體專案就免審核」的路徑。這個豁免不改變並行排程：
`shell_exec` 不是唯讀工具，仍然序列執行。進度通知會轉成 `tool.execution.update`，結果文字受全域工具輸出上限約束；結構化
結果會一併提供給模型。Server 要求額外互動輸入時，會把 `input_required`、輸入請求與
`requestState` 回傳給模型，模型可補上控制欄位後重試，不會假裝完成。

連線壽命由三層機制維持，長時間執行的 Session 不必靠使用者手動重新連線：

- Client 端 keepalive 每 30 秒 ping 一次（容忍一次失敗），避免閒置連線被伺服器或反向代理
  默默回收；連線真的結束時，`session.Wait()` 的監看會立即清掉狀態與工具快取。
- 每次使用前先確認連線可用：已結束的連線直接重建；閒置超過 20 秒的連線會先 ping 再使用。
  ping 沒有副作用，因此這個檢查對所有工具都安全。
- 呼叫失敗時區分「伺服器沒有收下」與「可能已執行」。`session not found`、session 過期與
  連線根本沒建立起來代表遠端不可能執行過工具，這種情況重新連線後重送一次，不受
  idempotent 宣告限制；其餘連線層錯誤維持只有 Server 宣告 idempotent 的工具才重試，
  避免同一個副作用執行兩次。

`call_timeout_seconds` 是「沒有回應或進度更新」的容忍時間，不是總時長上限：MCP Server 只要
持續送出進度通知就能繼續執行，長時間的遠端工作不會在固定秒數被砍掉；整體時間仍由 Run 的
wall-clock 預算控制。停滯逾時的訊息會明確說明是多久沒有回應，而不是回報一個籠統的
context 錯誤。

延長不是無限的：單次呼叫另有絕對上限（閒置視窗的 4 倍，最多 24 小時）。Server 一直送進度卻
永遠不回結果時仍會收斂，並在錯誤訊息附上期間收到幾次進度，避免畫面上出現永遠轉不完的圈。
等待期間每 15 秒送出一次 `tool.execution.update`（`phase=mcp_waiting`），內容包含是哪一個
MCP、已等待多久、收到幾次進度；等待人工核准時同樣每 30 秒回報一次
（`phase=waiting_approval`）。核准對話框可能因為切換對話而沒被看到，這兩個回報讓「到底卡在
哪一段」直接出現在執行過程裡，而不是只有一個沒有說明的轉圈圈。

stdio 子程序只繼承必要的 OS 環境與該 Server 明確設定的變數。SSE 與 Streamable HTTP 可加入
Bearer Token、Basic Auth 與自訂 headers，並有啟動／呼叫逾時。這些秘密只寫入權限限制檔案；管理 API 僅回傳
`has_api_key`、`has_environment` 與 `has_headers`，不讀回明文。

因為明文不會回傳，管理 API 另外回報 `auth_mode`（`none`／`bearer`／`basic`／`headers`），
也就是後端這一刻實際會送出的驗證方式；憑證被拒絕時，錯誤訊息會直接指出送出的是哪一種
（例如「HTTP 401 Unauthorized；目前送出的驗證方式是 Basic Auth（帳號 xxx）」），
使用者不必猜「金鑰到底有沒有被使用」。優先順序是 Bearer Token → Basic Auth → 自訂 headers；
`stdio` 不使用 HTTP 驗證，憑證請放在該 Server 的環境變數。

## Provider Router、Chat Completions 與 Codex Responses

`modelrouter.Router` 實作 `ports.ProviderCatalog`，以 Provider ID 路由至任意 `ports.Model`。
設定以 `providers.<id>.type` 選擇 bootstrap adapter 工廠；目前實作兩種 type：

- `openai-compatible`：OpenAI-compatible Chat Completions，可連遠端或本機相容服務。
- `openai-codex-responses`：固定使用 Codex Responses 協定，透過 ChatGPT／Codex OAuth 取得認證。

Provider 可透過管理頁右上角的啟用 Switch 或 `providers.<id>.enabled` 停用。停用只會從 Router、Workspace、Session 與對話選單移除，原設定與憑證仍保留，管理頁也仍可測試連線。為保持資料一致，系統預設 Provider 及仍被 Workspace 使用的 Provider 不可停用；舊設定未提供 `enabled` 時視為啟用。

OpenAI-compatible adapter 行為如下：

- system/developer、user、assistant、tool message。
- function tools schema。
- assistant `tool_calls`。
- tool result `tool_call_id`。
- SSE 文字、refusal 與 tool call arguments 串流累積，支援多行 SSE 與常見 NDJSON 相容輸出。
- `stream_options.include_usage` 可設定；不支援串流或 `tool_choice` 的相容服務可分別停用。
- 初始連線、408/409/429 與暫時性 5xx 最多嘗試三次，並遵守 `Retry-After`（上限 30 秒）。一旦已送出模型文字 delta 就不重試，避免重複輸出。
- 每次請求送出 `X-Client-Request-Id`，並將 Provider 回傳的 `x-request-id` 保存於 assistant message 與 turn record；API key 不會出現在診斷資料。
- Provider HTTP 錯誤解析為 status、code、request ID、可重試狀態與受限長度訊息。

Codex Responses adapter 將 Responses 的 message、reasoning 與 function call item 轉成相同的
Harness 事件與 transcript 結構，工具結果也會在下一輪完整回送。它不依賴 `/models` 目錄；管理
介面無法取得清單時顯示 `-`，仍允許使用者指定模型名稱。上游回應若帶有 primary／secondary
rate-limit 視窗，Provider 用量介面會顯示 5 小時與 7 天的剩餘比例；沒有資料時顯示 `-`，不推測
為 100%。後端啟動時立即刷新一次，之後每 3 分鐘背景更新；Provider 設定異動後也會要求刷新。

ChatGPT／Codex OAuth 使用 authorization-code + PKCE。驗證期間只啟動 loopback callback
server，登入與帳號選擇在 Browser 完成；access token、refresh token 與 PKCE verifier 不經管理
API 回傳，Token 另存於權限限制檔案。這是 NR-Intern 的 Provider 整合流程，不代表一般
OpenAI-compatible Provider 會自動取得相同登入能力。

OpenAI 官方協定參考：[Chat Completions API](https://developers.openai.com/api/reference/cli/resources/chat/subresources/completions)、[Responses API](https://developers.openai.com/api/reference/cli/resources/responses/methods/create)；request ID 診斷參考：[OpenAI API Overview](https://developers.openai.com/api/reference/overview#debugging-requests)。

## 記憶體隔離 Project 與 RAM disk 生命週期

Project 建立時可設定不可變更的 `ephemeral=true` 與 `ram_disk_size_mb`。`bootstrap.RAMDiskPool`
以 Project ID 建立互不共用的揮發性工作空間；Application 在 Run 開始前取得該 Project 的可信任
根目錄並放在 Sandbox 第一順位。Project 刪除時立即卸載，`Runtime.Close` 則在 Application、MCP
與其他子程序都關閉後清理剩餘磁碟。啟動期間任一步驟失敗也會回滾已建立的磁碟。

- macOS：以參數陣列直接呼叫 `hdiutil attach -nomount` 與 `diskutil eraseVolume`，不經 Shell；
  正常卸載失敗時才使用強制 detach。
- Linux：確認 `/dev/shm` 確實是 tmpfs 且可用空間不小於設定容量，再建立權限為 `0700` 的子目錄。
- Windows：以 ImDisk 的 `vm` backing store 建立 NTFS RAM disk，自動挑選未使用的磁碟代號；找不到
  `imdisk.exe` 時明確拒絕啟動，不會靜默落到硬碟。此路徑仍標示為待 x64／ARM64 實機驗證。
- 其他平台：使用可清理的系統暫存目錄，但 `Volatile()` 回報 false，呼叫端不得把它呈現成 RAM disk。

殘留清理由固定名稱前綴、結構化專用標記、平台種類與建立程序 PID 共同判定。仍活著的程序一律略過；
缺少標記、標記格式不符或裝置名稱不合法時也不處理，以免卸載使用者自行建立的磁碟。

能不能新建記憶體隔離 Project 由服務設定 `memory_isolated_projects` 控制，**預設開啟**——
與仍在設計中的實驗性功能不同，這是已完成的能力。關閉只擋新建（回 409）：既有隔離 Project
的 RAM disk、對話與關閉時清理都照常，一次誤關不會讓使用者手上的專案失效。在缺少 RAM disk
支援的環境（Windows 需安裝 ImDisk）可以整個關掉，讓建立對話框不再提供註定失敗的選項。

記憶體隔離 Project 的設定本身持久化，但其 Session 只在目前程序生命週期內存在。重啟後 Project 仍在，
但底下不會恢復任何對話。隔離類型與容量建立後不可 PATCH，避免尚未實作搬移時產生資料保留語意不一致。

### 執行期間就不落地

對話資料在**寫入當下**就走向該 Project 的 RAM disk，而不是先寫進 `dataDir` 再靠清理補救。
歸屬編碼在 ID 裡：隔離對話的 Session ID 為 `session_v<projectHex>_<random>`，Run ID 沿用同一個
代碼。因為對話建立後不能換 Project（`Service.validateEphemeralSessionMove`），這個代碼永遠成立，
路徑解析只需要字串處理，不必查詢狀態，也沒有需要失效的快取。

| 資料 | 作法 | 解析依據 |
| --- | --- | --- |
| Session／transcript | 目錄建在 RAM disk 上 | Session ID |
| 計畫 | 同上，`<disk>/store/plans` | Session ID |
| 附件 | 同上，Session 目錄底下 | Session ID |
| Run 事件 | `<disk>/store/runs/events` | Run ID |
| Run 紀錄（`runs.json`） | 只留在記憶體，不寫入檔案 | `Run.SessionID` |
| 通知（`notifications.json`） | 只留在記憶體，不寫入檔案 | `Notification.SessionID` |
| 回憶（`memories.json`） | 只留在記憶體，不寫入檔案 | `Memory.SourceSessionID` |

前四項各自有獨立路徑，所以按根目錄分流（`filestore.ProjectRoots`）。後兩項是**單一檔案存放全部
紀錄**、每次寫入整份重寫，沒有可分流的路徑，因此改在寫入時濾掉——記憶體中仍然完整，本次執行
期間的顯示、查詢與已讀狀態都不受影響，程序結束就消失，與 RAM disk 上的對話語意一致。

回憶另有一項前提：隔離對話一律使用專案專屬 scope（`memory.ScopeForSessionWithSpace`），
且不接受 session metadata 的 `memory_scope` 覆寫。去重與取代邏輯只在同一個 scope 內生效，
共用 scope 時一筆即將消失的記憶會就地改寫、或標記取代掉持久記憶——寫入時過濾反而會把
一般對話記住的東西一起帶走。分開 scope 之後，過濾只會動到本來就要消失的那些。

RAM disk 分成 `workspace` 與 `store` 兩層：Sandbox 只拿到 `workspace`，上述後端資料全部放在
`store`，Agent 因此讀不到自己的 transcript 與計畫。磁碟未掛載（重開機後必然如此）時解析回到預設
根，該對話自然變成「找不到」——這正是揮發語意要的結果。

`purgeEphemeralProjectSessions` 保留一輪作為升級路徑，清掉舊版留在 `dataDir` 的殘留，下一版移除。

## 程序拓樸

```text
HTTP Client ─────────────────────────→ server :8787 ─→ Provider
Browser ─→ desktop :8790 ───────────→ server :8787 ─→ MCP Server
                    │                         │
                    │                         └─→ NetPassClient（選用的對外通道）
                    └─ 僅管理自己啟動的 backend child
```

`desktop` 不會停止或重啟已存在的外部後端。桌面 reverse proxy 會在 server 設有 Bearer token
時代為加入，不將 token 暴露給 Browser JavaScript。NetPass 只轉送 backend port，不轉送 desktop
UI；它是獨立、選用的 Runtime，啟動前必須由使用者明確接受公開網路風險。桌面自行啟動的
backend child 會記錄父程序 PID；父程序消失時 child 會自動結束，避免 UI 關閉後留下孤兒後端。

macOS 原生視窗啟動時就安裝狀態列項目，提供顯示主視窗、隱藏主視窗與結束程式。對話進行中按下
關閉會詢問是否隱藏 UI 並讓後端 Run 繼續；狀態列項目與 Dock reopen 都能恢復同一個視窗。若使用者
再次啟動程式而本機 desktop port 已被既有 NR-Intern 使用，新程序會先驗證 identity endpoint，再
要求既有程序顯示 UI，不會啟動第二套後端。這項原生狀態列生命週期目前只在 macOS 實作。

畫面擷取是 desktop-local 能力，不屬於對外後端 API。macOS adapter 只啟動 Apple 的
`Screenshot.app`，由系統完成互動式區域選取並寫入剪貼簿；desktop bridge 監看剪貼簿中的 PNG，
再交給 WebView 內的 Canvas 標註編輯器。這條路徑不讓 NR-Intern 直接讀取整個顯示器，也不公開
桌面擷取端點到 NetPass 反向代理。Windows adapter 則啟動系統剪取 URI；兩個平台最後都以系統
剪貼簿作為圖像交接邊界。編輯器的方框、直線與文字操作只存在前端記憶體，使用者按複製或關閉
後才輸出合成 PNG。
