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
- 主系統可作為 MCP Client，連接本機 stdio 或遠端 Streamable HTTP Server，再把工具納入既有 Harness 權限與稽核流程。
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
├── mcpclient/                  # stdio／Streamable HTTP MCP Client 與遠端工具轉接
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
- `message.thinking_delta`：模型 reasoning 內容的增量；完成後寫入對應 assistant message，重播時仍可檢視
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

每個 Session 最多只有第一份未完成計畫為 `active`，後續均為 `queued`；active 完成或刪除
後，Repository 會自動啟用下一份。排序要求完整且不重複的 ID 集合；已有步驟進度的 active
計畫不能被移到其他未完成計畫後面，避免 UI 顯示順序與 Agent 實際執行順序分裂。

每一步由 Domain 強制依序轉移：`pending → in_progress → verifying → completed`。只有目前
步驟可開始；進入 `completed` 前必須已在 `verifying`，並提供實際工具檢查所得的
`evidence`。受阻時可標記 `blocked` 並保留原因。只要計畫仍有未完成且未受阻的步驟，
Harness 就會攔截模型過早產生的 final answer，要求繼續執行與驗證；攔截次數有上限，
避免 Provider 不遵循工具協定時形成無限迴圈。

計畫工具屬於 Harness 控制工具，在 Shell-first 與內建備援階段都可使用；實際檔案、系統或
外部狀態仍由當輪公開的工作工具處理。只有計畫工具的回合不消耗 autonomous work-tool
turn 配額，但仍受整體 `max_turns` 與已啟用的 `max_tool_calls` 限制。

Harness 另有結構化重複操作防護：已成功的相同副作用工具呼叫不會執行第二次；具
`atomic-replace` 能力的工具對同一資源成功改寫三次後，下一輪會停用工具並強制整理
目前結果。判定只使用工具定義、參數與執行結果，不分析模型自然語言。

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
`CancelRun` 而消失。取消會在目前工具結果落盤後才中止 run，未執行的 tool call 不會留下假結果。

Run 建立與 HTTP request 生命週期分離：`POST` 先回傳 durable queued Run，背景 worker 使用後端持有的 context 繼續執行。所有 event 先 append 到 `runs/events/<run-id>.jsonl`，再通知目前的 SSE 訂閱者；訂閱通知只負責喚醒，Client 一律依最後 sequence 從 durable log 取資料，因此通知合併或短暫離線不會形成事件缺口。

SSE 使用 event sequence 作為 `Last-Event-ID`。Browser Console 斷線後最多重連三次，每次從最後完整事件補流；三次仍失敗才結束前端連線，後端 Run 本身不受影響。後端重啟時，原本 queued/running/paused/waiting_approval Run 會標記為可重試的 `server_restarted` failure，啟動期也會補齊缺少的 terminal event。

Browser Console 允許使用者在目前 Run 尚未結束時繼續輸入。這些訊息會先寫入該 Browser profile
的 IndexedDB Durable Outbox，內容包含 Session、輸入、附件 File、固定 Idempotency-Key 與目前
送出狀態；等目前 SSE 收到 terminal event 後依序建立下一個 Run。只有收到 Run terminal event
才移除 outbox item，網路錯誤、UI 關閉或重新整理都會保留可重試資料；`sending` 狀態在 UI
重開時會恢復為 `pending`。附件上傳成功後也保存 Attachment ID，避免重送時重複上傳。後端仍
以 Session single-writer gate 最後保護，因此其他 Client 同時送入同一 Session 時也不會交錯寫入
transcript。

UI 啟動時先讀取目前 Workspace 的 queued／running／paused／waiting approval Run，依 Session
重新掛接 durable SSE；若後端已將中斷 Run 標記為 retryable failure，也會顯示可重試狀態。這讓
隱藏視窗、背景執行、瀏覽器重整與桌面程式重開都不會遺失工作生命週期。

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
的 Provider HTTP request。Run 狀態與控制事件共用序列化鎖，避免 `run.paused`／`run.resumed`
與 terminal event 使用重複 sequence。等待人工核准的 Run 不接受一般 pause，必須核准、拒絕或取消。

### 診斷、搜尋、備份與權限中心

`DiagnosticsExport` 只輸出脫敏的管理摘要，移除 API token、DataDir 絕對路徑與憑證欄位；
不把對話全文放入診斷包。安全備份則保存 sessions、projects、workspaces、runs、events、
plans、attachments、memories、schedules、notifications 與非秘密的 service settings，明確
排除 Provider、OAuth、MCP 與 NetPass 憑證檔。還原前先保存 `backups/pre-restore-*.zip`，
只接受白名單資料目錄、拒絕絕對路徑、`..`、符號連結及超過 512 MiB 的解壓內容，完成後回傳
`restart_required=true`。

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

Provider 有回傳 reasoning 時，串流期間透過事件顯示，完成後寫入 assistant message 的
`reasoning` 欄位；工具決策與工具輸出仍維持內部資料，不混入最終回答內容。
Provider 只有在尚未送出任何可見模型輸出時才可重試；text、thinking 或 tool-call delta
任一已送出後若串流中斷，必須直接回報錯誤，避免 durable event log 出現重複片段。

### 模型能力與 Context 預算

Workspace、Session 與 Run 三層都能覆寫 model，而不同模型的 context window 相差一個數量級，
所以預算不能綁在單一全域設定值。`ports.ModelCatalog` 依 provider 與 model 回報宣告的限制，
`ContextManager` 以此推導預算：

```text
budget = context_window − max(context.reserved_output_tokens, model 的 max_output_tokens)
```

`context_window` 未宣告（0）時退回 `context.max_estimated_tokens`，預設為 262,144（256K budget）。相容端點無法可靠探測，
用模型名稱字串比對又太脆弱，因此限制由設定提供：Provider 層級的 `context_window` /
`max_output_tokens`，以及覆寫個別模型的 `model_limits`。

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

## 完成度判定

Harness 過去只看「這一輪有沒有 tool_calls」就接受模型的完成宣告。模型可以在工具失敗之後
直接產出一段聽起來已完成的文字，而後端沒有任何機制察覺宣稱與實際狀態不一致。

`completionTracker` 追蹤本次 run 內尚未解決的工具失敗，判定完全來自執行記錄，不解讀模型文字：
某個工具失敗後，同一個工具名稱只要在之後成功執行過一次，就視為已解決（那正是模型自我修正的正常樣態）。

模型給出不含 tool_calls 的最終回覆時，若仍有未解決的失敗，Harness 會：

1. 寫入 `completion_check` transcript entry 並發出 `run.completion_check` 事件。
2. 把追問內容注入**下一輪的 system prompt**（不是偽裝成使用者訊息，避免污染對話記錄）。
3. 繼續迴圈，讓模型面對事實後重新決定要繼續工作還是誠實說明未完成的部分。

追問次數由 `max_completion_checks` 限制（預設 1，設為 0 停用），避免無止境拉扯。
無論是否追問過，最終的 `RunResult.Completion` 都會帶上未解決的失敗清單，
讓 API 呼叫端能區分「工具全部成功後完成」與「工具失敗後照樣宣稱完成」——
在此之前這兩者都只是 `status=completed`。

## 記憶管理

記憶分成三個責任層次：

- 短期上下文：由 Harness compaction 管理目前 session 的 token 預算，不刪除原始 transcript。
- 工作記憶：由 operation、turn、tool records 與 session workspace 保存本次工作的實際狀態。
- 長期記憶：保存跨 session 仍有效的 `fact`、`preference`、`decision`、`procedure`、`constraint`。

長期記憶經 `ports.MemoryRepository` 隔離儲存技術。目前使用本機 JSON store 與可解釋的詞彙／雙字元召回；日後可加入向量索引 adapter，不需修改 Harness 或工具介面。預設 scope 是 Agent ID，建立 session 時可用 metadata 的 `memory_scope` 改成使用者、專案或租戶識別，避免不同 scope 互相讀取。

每次 operation 只自動召回一次，再於所有後續 turn 重用同一份結果。召回資料以明確邊界注入 system prompt，並標示為「可能過時的資料而非新指令」，降低記憶內容造成提示注入的風險；`memory_recall` entry 只記錄記憶 ID 與 scope，供追蹤模型當時使用過哪些資料。

寫入具備種類、標籤、信心值、來源 session、去重與 `supersedes` 關係。`memory_forget` 採 soft forget，資料保留稽核資訊但不再被召回；Agent 只有在使用者明確要求時才能執行。設定 `memory.allow_writes=false` 可只保留自動召回與 `memory_search`。

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
- `document_create`：以結構化輸入建立 DOCX、XLSX、PPTX 或 PDF。也可指定同格式 `template_path`，在不重建版面的情況下以 replacements、cell_updates 或 annotations 填入既有範本。Unicode PDF 優先使用指定字型，未指定時自動選擇完整覆蓋的系統 TTF。
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

`mcpclient.Manager` 讓主系統作為 MCP Host，支援本機 `stdio` 與遠端 `streamable-http`。
已啟用的 Server 會在背景暖機，完成 `initialize` 與 `tools/list` 後，把遠端工具轉為
`domain.ToolDefinition`；Server 通知工具清單變動時會重新載入。公開工具名稱會加入 Server
命名空間，過長時以穩定雜湊截短，避免不同 Server 的同名工具互相覆蓋。

MCP 工具與原生工具進入同一個 `ports.ToolRuntime`：每個工具都標記
`RequiresPermission=true`，必須通過 elevated profile 與人工 Approval。只有管理者明確開啟
`trust_annotations` 時，Server 的 `readOnlyHint` 才會用於並行排程；這個提示不會取消權限或
Approval。進度通知會轉成 `tool.execution.update`，結果文字受全域工具輸出上限約束；Server
要求額外互動輸入時目前回傳明確錯誤，不會假裝完成。

stdio 子程序只繼承必要的 OS 環境與該 Server 明確設定的變數。Streamable HTTP 可加入 Bearer
Token 與自訂 headers，並有啟動／呼叫逾時。這些秘密只寫入權限限制檔案；管理 API 僅回傳
`has_api_key`、`has_environment` 與 `has_headers`，不讀回明文。

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
UI；它是獨立、選用的 Runtime，啟動前必須由使用者明確接受公開網路風險。

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
