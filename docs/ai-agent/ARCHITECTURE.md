# AI Agent 架構

## 目標

本系統是從舊智慧問答概念重新建立的新 AI Agent，不維持舊 API、問題拆解流程或舊工具相容性。舊程式只作為行為與資料來源參考。

核心要求如下：

- 後端是可獨立啟動的 HTTP API，不依賴桌面前端。
- Browser、任意 HTTP Client 與桌面程式都使用同一套 API。
- 桌面程式可以連接外部後端，或啟動、停止、重啟自己擁有的後端子程序。
- Agent 採 Harness 工作迴圈；簡單任務不強制規劃，長任務則可建立結構化計畫並逐步驗證。
- Agent 原生工具使用 Go 實作，不依賴 `grep`、`find`、`diff`、`ssh` 等本機外部指令。
- Provider Router 在結構上支援多種 adapter；目前只實作 OpenAI Chat Completions 相容協定。
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
├── application/                # use case、Agent registry、run/session 協調；不依賴具體 Harness
├── adapters/
│   ├── openaicompat/           # 目前已實作的 Provider adapter
│   └── filestore/              # Workspace、Project、Session、Plan、Run 與記憶持久化
├── tools/
│   ├── registry.go             # 原生工具註冊、權限與統一執行入口
│   └── native/
│       ├── internal/toolutil/  # 參數、workspace 路徑與輸出限制
│       ├── files/              # directory 與 file read/search/compare/write/edit
│       ├── documents/          # PDF、DOCX、XLSX、PPTX 結構檢視與分段文字抽取
│       ├── plans/              # plan_get/create/step_update 計畫控制工具
│       ├── shell/              # shell_exec 與 OS platform adapter
│       ├── ssh/                # Go SSH client
│       └── memories/           # memory_search/remember/forget
├── transport/httpapi/          # REST、SSE、驗證、CORS、Problem JSON
├── bootstrap/                  # 設定與 dependency composition root
├── desktop/
│   ├── supervisor/             # 後端程序生命週期與紀錄
│   ├── httpui/                 # 本機 UI server 與後端 reverse proxy
│   └── launcher/               # 跨平台開啟 Browser
└── web/console/                # 內嵌 HTML/CSS/JavaScript
```

新增 Provider 類型時，在 `adapters/<provider>/` 實作 `ports.Model`，並只在 bootstrap 工廠註冊 type；Workspace、Harness 與 HTTP 不依賴個別 Provider。新增 Agent 工具放在 `tools/native/<類別>`，Harness、HTTP 與前端都不直接 import 個別工具。

## Harness 工作迴圈

```text
收到使用者訊息
  → 寫入 append-only session transcript
  → 讀取 Session 計畫；長任務可由模型建立計畫
  → 呼叫 OpenAI-compatible LLM（含目前可用工具 schema）
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
`file_compare`、`directory_list`、`document_inspect`、`document_read`、`memory_search`）在同一群組內並行執行，其餘工具各自成組依序執行。

只有「連續」的 read-only 呼叫可以並行：`[read, write, read]` 之中兩個 read 隔著一個 write，
併發執行會讓第二個 read 讀到不確定的狀態。工具定義裡沒有的名稱一律視為非 read-only。
單一群組最多同時執行 8 個。

開始事件與結果寫入都依原本的 tool_calls 順序進行，因此 transcript 與事件流的順序不受併發影響。
`tool.execution.update` 經過序列化後才送出，因為下游的 durable event log 以遞增 sequence 寫入，
不可併發呼叫。**`BeforeTool` 與 `AfterTool` hook 因此必須是併發安全的。**

工具執行完成後的結果寫入使用不受取消影響的 context：工具可能已經產生副作用，結果不能因為
`CancelRun` 而消失。取消會在目前工具結果落盤後才中止 run，未執行的 tool call 不會留下假結果。

Run 建立與 HTTP request 生命週期分離：`POST` 先回傳 durable queued Run，背景 worker 使用後端持有的 context 繼續執行。所有 event 先 append 到 `runs/events/<run-id>.jsonl`，再通知目前的 SSE 訂閱者；訂閱通知只負責喚醒，Client 一律依最後 sequence 從 durable log 取資料，因此通知合併或短暫離線不會形成事件缺口。

SSE 使用 event sequence 作為 `Last-Event-ID`。Browser Console 斷線後最多重連三次，每次從最後完整事件補流；三次仍失敗才結束前端連線，後端 Run 本身不受影響。後端重啟時，原本 queued/running/waiting_approval Run 會標記為可重試的 `server_restarted` failure，啟動期也會補齊缺少的 terminal event。

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

Project 與 Workspace 刪除都不做級聯操作：只要還有子 Project 或 Session 就回傳 conflict，避免 UI 管理動作隱含遺失對話。

Project 可保存多個 `sandbox_roots`。建立與更新時由後端解析實體路徑、確認目錄存在、去重並拒絕檔案系統根目錄；Run 開始時才依 Session 所屬 Project 產生不可由 Client 覆寫的 Sandbox metadata。檔案與 Shell 工具對絕對路徑逐一檢查是否位於任一根目錄，相對路徑固定以第一個根目錄為基準，且既有 symlink 解析後仍須留在允許範圍。未設定 Project Sandbox 時，維持 Session 私有 `workspace/`。

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
每個 turn 重新解碼它們（尤其是數十 KB 的工具輸出）沒有用途，而那正是長 session 變慢的主因。此處小寫 `workspace/` 是 Session 專屬目錄，不是管理層的 Workspace 實體。它是**預設**沙箱；Session 所屬 Project 設定了 `sandbox_roots` 時，工具範圍改由那些目錄決定。任一情況下既有 symlink 都會檢查實際目標是否逃逸。

JSONL 逐行讀取不使用 `bufio.Scanner` 的固定 token 上限。Provider 解碼後的 tool arguments
重新 JSON 跳脫時可能膨脹數倍；單筆合法 entry 不應讓整個 Session 在重啟後永久無法讀取或追加。

Harness 在 context 預算 80% 的 soft limit 於 turn 完整落盤後排程背景 compaction，
讓下一輪通常不必同步等待摘要模型；如果仍直接碰到 hard limit，`Build` 會同步壓縮作為最後保護。
完整訊息仍保留在 `entries.jsonl`，模型 context 則改用「較早紀錄摘要＋近期完整訊息」。
摘要會留下 `compaction` entry、`through_sequence` 與觸發比例，後續 compaction 在既有摘要上增量合併。

背景工作同一 Session 最多一個，不送 Run event；`AppendEntry` 的序號鎖負責與目前 Run 的
transcript 寫入序列化。`context.summary_provider_id` 與 `summary_model` 可把摘要路由到較便宜的
Provider／Model；未指定時沿用 Session 的模型。模型宣告的 context window 若連輸出保留額都
容納不下，後端會明確拒絕，不會虛構最低輸入容量後再交給 Provider 失敗。

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

`context_window` 未宣告（0）時退回 `context.max_estimated_tokens`，預設為 524,288（512K budget）。相容端點無法可靠探測，
用模型名稱字串比對又太脆弱，因此限制由設定提供：Provider 層級的 `context_window` /
`max_output_tokens`，以及覆寫個別模型的 `model_limits`。

### Token 估算

context 預算透過 `ports.TokenCounter` 估算，預設實作 `tokens.HeuristicCounter` 依字元類別加權：
CJK 字元約 1 token、ASCII 約 0.25 token。以英文為前提的 characters/4 對中文會低估 3～4 倍，
使 compaction 幾乎不觸發或讓請求超出 Provider 的 context window。估算涵蓋 system prompt、
摘要、訊息、tool call 參數，以及每次請求都會重送的工具 schema。低估的後果是請求被拒絕，
高估只會提早 compaction，因此預設權重刻意偏保守。

大型 tool result 只會在送入模型的 context view 中保留頭尾並縮減；原始工具輸出不會被改寫。每次 Run、Turn 與 Tool execution 另有 operation records，供當機診斷與後續復原使用。

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
- `shell_exec`：必要高權限工具；Unix 使用 process group，Windows 使用 Job Object，取消或逾時會終止子程序樹；子程序只繼承必要 OS 環境，另有不經 shell 的 direct 模式。
- `ssh_exec`：使用 Go SSH client；連線憑證只存在後端 profile，模型只取得 profile 名稱。初始連線預設最多三次並使用 keepalive；工作中斷線不自動重跑遠端命令，以免重複副作用。
- `memory_search`：查詢目前 scope 的有效長期記憶。
- `memory_remember`：保存耐久資訊並支援去重與取代舊記憶。
- `memory_forget`：依明確要求軟性遺忘，保留稽核狀態。

Shell 與 SSH 必須同時符合：後端 `allow_elevated_tools=true`、工具在 allowlist、session permission profile 屬於
`permissions.elevated_profiles`。profile 由後端 `permissions` 策略指派，預設不接受 API request 指定；
未宣告任何 elevated profile 時 fail closed。詳見 `docs/ai-agent/SECURITY.md`。

文件工具是 read-only，仍必須通過 Project／Session Sandbox。單一文件預設上限 128MB，Open XML 單一解壓項目上限 64MB，最終輸出沿用 `max_tool_output_bytes`。大型文件應先呼叫 `document_inspect`，再縮小頁面、段落、工作表列或投影片範圍；不支援舊式二進位 DOC／XLS，這類文件需先由 Host 工具轉換。PDF 文字抽取使用純 Go reader；沒有文字層時回報需要 OCR，不臆測掃描影像內容。

`directory_create`、`file_write`、`file_edit` 也屬於寫入型高權限工具，使用相同閘門。`ResolvePath` 除了檢查目標，也會解析最深的既有父路徑；即使新檔尚不存在，仍不能藉父層 symlink 寫出 workspace。Unix 與 Windows 的檔案替換分別使用原生 rename／MoveFileEx 語意。

## Provider Router 與 OpenAI-compatible Adapter

`modelrouter.Router` 實作 `ports.ProviderCatalog`，以 Provider ID 路由至任意 `ports.Model`。設定以 `providers.<id>.type` 選擇 bootstrap adapter 工廠；目前唯一已實作的 type 是 `openai-compatible`，使用 `POST /v1/chat/completions`。

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

OpenAI 官方 Chat Completions schema 參考：[OpenAI Chat Completions API](https://developers.openai.com/api/reference/cli/resources/chat/subresources/completions)；request ID 診斷參考：[OpenAI API Overview](https://developers.openai.com/api/reference/overview#debugging-requests)。

## 程序拓樸

```text
HTTP Client ───────────────→ server :8787
Browser ─→ desktop :8790 ─→ server :8787
                    │
                    └─ 僅管理自己啟動的 backend child
```

`desktop` 不會停止或重啟已存在的外部後端。桌面 reverse proxy 會在 server 設有 Bearer token 時代為加入，不將 token暴露給 Browser JavaScript。
