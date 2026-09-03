# HTTP API

預設位址為 `http://127.0.0.1:8787`。若設定 `api_token`，除 `/healthz`、`/readyz` 與 CORS preflight 外，請帶入：

```http
Authorization: Bearer <token>
```

## Endpoints

| Method | Path | 用途 |
|---|---|---|
| GET | `/healthz` | Process health |
| GET | `/readyz` | Application readiness |
| GET | `/api/v1/openapi.yaml` | 完整 OpenAPI 3.1 契約 |
| GET | `/api/v1/admin/status` | 版本、API／事件 schema、capabilities、instance 與啟動時間 |
| GET | `/api/v1/admin/diagnostics` | 脫敏後端設定、Provider、Session、Run 與工具統計 |
| GET | `/api/v1/admin/diagnostics/export` | 下載不含憑證與 DataDir 絕對路徑的診斷 JSON |
| GET | `/api/v1/admin/backup` | 下載排除 Provider／OAuth／MCP／NetPass 憑證的安全 ZIP 備份 |
| POST | `/api/v1/admin/restore` | 上傳安全備份還原；還原前保留 pre-restore 快照，完成後需重啟後端 |
| GET | `/api/v1/admin/permissions` | 唯讀權限策略、工具權限與待核准 Run 摘要 |
| GET | `/api/v1/admin/update` | 最近一次版本更新檢查結果 |
| POST | `/api/v1/admin/update/check` | 立即檢查官方 GitHub Release；啟用通知中心時發現新版本才寫入通知 |
| GET | `/api/v1/notifications` | 通知中心清單，可依未讀篩選 |
| POST | `/api/v1/notifications/{notification_id}/read` | 將單筆通知標為已讀 |
| POST | `/api/v1/notifications/read-all` | 將全部通知標為已讀 |
| DELETE | `/api/v1/notifications/read` | 清除已讀通知 |
| GET | `/api/v1/search` | 搜尋 Workspace、Project、Session、Message、Plan 與 Schedule 的短摘要 |
| GET／PUT | `/api/v1/admin/service-settings` | 讀取或更新顯示名稱、介面語言、通知中心、Run 上限、工具供應方式與 `http_fetch` 開關 |
| GET | `/api/v1/admin/config-bundle` | 下載遮蔽秘密欄位的設定 ZIP；不包含對話與附件 |
| GET／PUT | `/api/v1/admin/provider-settings` | 讀取或完整取代脫敏 Provider 設定 |
| GET | `/api/v1/admin/provider-settings/{provider_id}/models` | 重新取得模型目錄；沒有目錄時回傳空陣列 |
| POST | `/api/v1/admin/provider-settings/{provider_id}/test` | 送出最小模型請求並測試工具呼叫 |
| POST | `/api/v1/admin/provider-settings/{provider_id}/oauth/start` | 啟動 ChatGPT／Codex OAuth PKCE 驗證 |
| GET | `/api/v1/admin/provider-settings/{provider_id}/oauth/status` | 讀取脫敏 OAuth 狀態 |
| DELETE | `/api/v1/admin/provider-settings/{provider_id}/oauth` | 中斷 OAuth 並刪除該 Provider Token |
| GET／PUT | `/api/v1/admin/mcp-settings` | 讀取或完整取代 MCP Server 設定 |
| POST | `/api/v1/admin/mcp-settings/{mcp_id}/test` | 重新連線 MCP Server 並刷新工具清單 |
| GET／PUT | `/api/v1/admin/reverse-proxy` | 讀取或更新 NetPass 反向代理設定 |
| POST | `/api/v1/admin/reverse-proxy/start` | 接受使用政策後啟動 NetPassClient |
| POST | `/api/v1/admin/reverse-proxy/stop` | 停止 NetPassClient |
| GET | `/api/v1/tools` | 內建與 MCP 工具定義、allowlist 與目前可用性 |
| GET | `/api/v1/providers` | 已註冊 Provider adapter 清單 |
| GET | `/api/v1/providers/{provider_id}/capabilities` | 讀取 Provider／Model 的 Context 與輸出限制 |
| GET | `/api/v1/providers/{provider_id}/usage` | 讀取最近一次 5 小時／7 天用量視窗；無資料時標記 unavailable |
| GET | `/api/v1/memories` | 搜尋長期記憶（`scope`、`q`、`kinds`、`tags`、`limit`） |
| POST | `/api/v1/memories` | 由使用者寫入長期記憶 |
| GET | `/api/v1/memories/{memory_id}` | 讀取單筆長期記憶 |
| DELETE | `/api/v1/memories/{memory_id}` | 軟性遺忘一筆長期記憶 |
| GET | `/api/v1/sessions/{session_id}/export` | 匯出整個 Session（`format=json\|markdown`） |
| GET | `/api/v1/workspaces` | Workspace 清單 |
| POST | `/api/v1/workspaces` | 建立 Workspace |
| GET | `/api/v1/workspaces/{workspace_id}` | 讀取 Workspace |
| PATCH | `/api/v1/workspaces/{workspace_id}` | 更新 Provider 集合、預設 Provider 與模型 |
| DELETE | `/api/v1/workspaces/{workspace_id}` | 刪除空 Workspace |
| GET | `/api/v1/projects` | 結構化 Project 清單 |
| POST | `/api/v1/projects` | 建立 Project |
| GET | `/api/v1/projects/{project_id}` | 讀取 Project |
| PATCH | `/api/v1/projects/{project_id}` | 更新 Project 名稱、描述或排序位置 |
| DELETE | `/api/v1/projects/{project_id}` | 刪除空 Project |
| GET | `/api/v1/schedules` | 排程清單（`workspace_id` 過濾） |
| POST | `/api/v1/schedules` | 建立排程 |
| GET | `/api/v1/schedules/{schedule_id}` | 讀取排程 |
| PATCH | `/api/v1/schedules/{schedule_id}` | 更新排程；變更週期或啟用狀態會重算 `next_run_at` |
| DELETE | `/api/v1/schedules/{schedule_id}` | 刪除排程；已建立的 Session 保留 |
| POST | `/api/v1/schedules/{schedule_id}/run` | 立刻執行一次，不改變 `next_run_at` |
| GET | `/api/v1/agents` | Agent 清單 |
| GET | `/api/v1/agents/{agent_id}` | Agent 描述 |
| GET | `/api/v1/agents/{agent_id}/sessions` | Session 清單 |
| POST | `/api/v1/agents/{agent_id}/sessions` | 建立 Session |
| PUT | `/api/v1/agents/{agent_id}/sessions/order` | 調整同一 Project 內未釘選 Session 的完整順序 |
| GET | `/api/v1/sessions/{session_id}` | 讀取 Session |
| PATCH | `/api/v1/sessions/{session_id}` | 更新標題、Provider、模型、思考程度、計畫鎖定、權限或記憶 scope |
| DELETE | `/api/v1/sessions/{session_id}` | 刪除 Session 與 workspace |
| GET | `/api/v1/sessions/{session_id}/plan` | 讀取 Session 計畫；未建立時 `data` 為 `null` |
| PUT | `/api/v1/sessions/{session_id}/plan` | 由使用者建立或重建計畫 |
| DELETE | `/api/v1/sessions/{session_id}/plan` | 刪除計畫 |
| GET | `/api/v1/sessions/{session_id}/plans` | 依執行順序讀取多計畫列表 |
| POST | `/api/v1/sessions/{session_id}/plans` | 新增計畫到佇列尾端 |
| PUT | `/api/v1/sessions/{session_id}/plans/{plan_id}` | 重建指定計畫 |
| DELETE | `/api/v1/sessions/{session_id}/plans/{plan_id}` | 刪除指定計畫 |
| PUT | `/api/v1/sessions/{session_id}/plans/order` | 以完整 `plan_ids` 列表調整執行順序 |
| GET | `/api/v1/sessions/{session_id}/messages` | Transcript messages |
| POST | `/api/v1/sessions/{session_id}/messages/{message_id}/retract` | 撤回一則尚未固定的使用者訊息並建立重新提問流程 |
| POST | `/api/v1/sessions/{session_id}/attachments` | 上傳這次對話要使用的附件 |
| GET | `/api/v1/sessions/{session_id}/entries` | 分頁讀取完整 Harness 稽核 entries |
| GET | `/api/v1/sessions/{session_id}/runs` | Session 的 Runs |
| POST | `/api/v1/sessions/{session_id}/runs` | 建立非同步 Run，立即回傳 `202` |
| POST | `/api/v1/sessions/{session_id}/runs:stream` | 建立或取得 Run，並從 sequence 0 回傳 SSE |
| GET | `/api/v1/runs` | Run 清單，可用 `session_id` 篩選 |
| GET | `/api/v1/runs/{run_id}` | Run 狀態與結果 |
| GET | `/api/v1/runs/{run_id}/events` | 重播並持續訂閱 Run SSE 事件 |
| POST | `/api/v1/runs/{run_id}/cancel` | 取消執行中的 Run |
| POST | `/api/v1/runs/{run_id}/pause` | 在安全回合邊界暫停 queued／running Run |
| POST | `/api/v1/runs/{run_id}/resume` | 恢復 paused Run |
| POST | `/api/v1/runs/cancel-all` | 對所有 queued／running／paused／waiting approval Run 送出取消要求 |
| POST | `/api/v1/runs/{run_id}/decision` | 核准或拒絕等待中的高風險工具 |
| POST | `/api/v1/runs/{run_id}/retry` | 以原始輸入建立新的可追溯 Run |

取消端點回傳的 Run 會立即是 `canceled`，並寫入 `run.canceled` terminal event；底層 Provider
會同步收到取消訊號，但若第三方實作不遵守 context，仍可能在背景完成收尾。這段收尾不會再
把 Run 改回 `running` 或 `completed`。

Run 達到後端設定的回合、wall-clock、token 或工具呼叫上限時仍以 `completed`
收尾；`result.budget_exceeded` 會回傳觸發資源、限制值、觀察值與累計用量，
`metadata.termination` 則為 `budget_exceeded`。事件流會先送出
`run.budget_exceeded`，最後仍以 `run.completed` 結束。

### 用量與成本

`GET /api/v1/runs`、`GET /api/v1/runs/{run_id}`、Session 相關端點與 JSON 匯出會帶出用量：

- Run 的 `usage` 保存該次實際收到的 input／output／total token，以及收尾時依
  `model_prices` 計算的 `estimated_cost_usd`；沒有價格設定時省略成本欄位。
- Session 的 `usage` 是所有 Run 的累計，包含 token 總數與依 Provider／Model 分組的
  `by_model`。若任一有用量的 Run 沒有價格，Session 總成本也省略，避免顯示不完整的金額。
- `model_prices` 位於後端 JSON 設定檔，單價為每百萬 token 的 USD，不能由模型或一般 Run
  request 提供。範例：

```json
{
  "model_prices": {
    "openai-compatible": {
      "gpt-4o-mini": {
        "input_per_million": 0.15,
        "output_per_million": 0.60,
        "currency": "USD"
      }
    }
  }
}
```

取消或失敗的 Run 仍保存錯誤前已收到的用量；`retry` 會建立獨立 Run，不會覆寫原紀錄。

高風險工具執行前，Run 會進入 `waiting_approval` 並帶回 `pending_approval`。送出決策時
必須回傳同一個 `approval_id`，避免舊畫面的決策誤套到後續工具：

```bash
curl -X POST http://127.0.0.1:8787/api/v1/runs/RUN_ID/decision \
  -H 'Content-Type: application/json' \
  -d '{"approval_id":"APPROVAL_ID","decision":"approve","reason":"參數已確認"}'
```

`permanent: true` 只能和 `decision: "approve"` 一起送出。後端會把永久核准持久化到
目前 Session，這次之後該對話的高風險工具不再逐次詢問；其他 Session 不受影響。
一般 Session PATCH 不能直接開啟此欄位，必須經過一次有效的 pending approval。

`deny` 不會讓 Run 失敗；拒絕理由會寫成 tool observation，讓模型改走其他路徑。

後端重啟或其他 retryable failure 可建立新的 Run；原 Run 與事件保持不變，新 Run 的
`metadata.retry_of` 指向來源。網路重送時應重用同一個 `Idempotency-Key`：

```bash
curl -X POST http://127.0.0.1:8787/api/v1/runs/RUN_ID/retry \
  -H 'Idempotency-Key: retry-client-key'
```

## 管理設定與外部整合

管理端點與一般 API 使用相同的 Bearer 驗證。讀取設定時所有秘密都經過脫敏：API Key、OAuth
Token、MCP environment／headers 與 NetPass Key 只回傳是否已設定，永遠不回傳明文。

### Provider 與 ChatGPT／Codex OAuth

`PUT /api/v1/admin/provider-settings` 以完整集合套用設定，支援 `openai-compatible` 與
`openai-codex-responses`。OpenAI-compatible 的 `api_key` 省略時保留、空字串清除；Codex
Responses Token 不放在這份 payload，而由 OAuth 端點管理。仍被系統預設、Workspace 或 Session
引用的 Provider 不可直接移除或停用。

OAuth 流程如下：

1. `POST .../{provider_id}/oauth/start` 啟動 authorization-code + PKCE，並暫時建立只監聽
   loopback 的 callback server。
2. 回應提供公開的 `authorization_url` 與 `callback_uri`，桌面端會嘗試開啟 Browser；登入與帳號
   選擇都在 Browser 完成。
3. `GET .../{provider_id}/oauth/status` 輪詢 `pending`、`connected` 或 `failed`；狀態不含 access
   token 或 refresh token。
4. `DELETE .../{provider_id}/oauth` 中斷連線並刪除該 Provider 的本機 Token。

OpenAI Codex Responses 不提供模型目錄時，models endpoint 回傳空陣列，Console 顯示 `-`，仍可
手動設定模型名稱。`GET /api/v1/providers/{provider_id}/usage` 的 5 小時／7 天視窗若沒有上游資料，
`available=false`，呼叫端不得推測為 100%。

### MCP Client

MCP 設定以完整 `servers` 集合更新。每個 Server 支援本機 `stdio` 或遠端
`sse` 或 `streamable-http`；遠端連線可設定 Bearer Token、Basic Auth 與自訂 headers，本機程序可設定 args、工作目錄
與明確的 environment。秘密欄位省略時保留既有值，API Key 傳空字串、environment／headers 傳
空物件時清除。

```json
{
  "servers": [
    {
      "id": "example",
      "display_name": "Example MCP",
      "enabled": true,
      "transport": "streamable-http",
      "url": "https://mcp.example.com/mcp",
      "startup_timeout_seconds": 20,
      "call_timeout_seconds": 1800,
      "trust_annotations": false
    }
  ]
}
```

儲存後連線在背景暖機；`POST .../{mcp_id}/test` 會強制重連並刷新工具清單。所有 MCP 工具都先
進入 Session permission；Server 的 `readOnlyHint` 只有在 `trust_annotations=true` 時才會被採信，
此時唯讀工具可免逐次人工 Approval，但仍受權限、工具事件與輸出限制。其他工具仍須 Approval。

### NetPass 反向代理

反向代理只把 backend API port 交給 NetPassClient，不公開 desktop UI。API Key 留白時保留，
`clear_api_key=true` 才清除；執行中必須先停止才能改設定。啟動 request 必須明確帶入：

```json
{"accept_usage_policy": true}
```

NetPass Key 只用來連接通道，不等同後端 API 認證。啟動通道前必須另外設定強度足夠的
`api_token`；目前 NetPass 啟動流程不會代為驗證這個條件。公開網址與 Key 應視為敏感資料，
不應寫入文件、日誌或版本庫。

## Workspace、Project 與 Session

正式層級為 `Workspace → Project → Session`。先建立可容納多個 Provider 的 Workspace；現階段前端為單選，因此會寫入單元素 `provider_ids`，但資料與 API 不需在未來擴充時搬移：

```bash
curl -X POST http://127.0.0.1:8787/api/v1/workspaces \
  -H 'Content-Type: application/json' \
  -d '{"name":"產品開發","provider_ids":["openai-compatible"],"default_provider_id":"openai-compatible","model":"gpt-5.4"}'
```

`default_provider_id` 必須存在於 `provider_ids`，這個集合只管理 Workspace 自己的預設來源。Session／Run 的 Provider override 可選擇任一全域已啟用 Provider；未指定時才沿用 Workspace 預設。仍被 Workspace 或 Session 引用的 Provider 不可停用或刪除。目前 Provider 工廠支援 `openai-compatible` 與 `openai-codex-responses`；Router、Workspace 與 Harness 仍以協定無關介面運作。

建立 Project 時可透過 `sandbox_roots` 指定多個本機既有的絕對目錄。Session 屬於該 Project 時，原生檔案工具與 Shell 的工作目錄只能落在其中一個根目錄內；空陣列則使用 Session 私有工作目錄。公開文件不放入任何主機的絕對目錄，API Client 應在部署時提供實際路徑：

```json
{
  "name": "NR-Intern",
  "workspace_id": "workspace_xxx",
  "sandbox_roots": []
}
```

先建立 Project，再把 Session 明確歸入該 Project：

```bash
curl -X POST http://127.0.0.1:8787/api/v1/projects \
  -H 'Content-Type: application/json' \
  -d '{"name":"NR-Intern","workspace_id":"WORKSPACE_ID","description":"AI Agent 工程"}'
```

```bash
curl -X POST http://127.0.0.1:8787/api/v1/agents/general-agent/sessions \
  -H 'Content-Type: application/json' \
  -d '{"title":"檔案檢查","workspace_id":"WORKSPACE_ID","project_id":"PROJECT_ID","permission_profile":"default","pinned":true}'
```

要使用 `directory_create`、`file_write`、`file_edit`、`shell_exec` 或 `ssh_exec`，後端必須設定
`allow_elevated_tools=true`，並由 `permissions.default_profile` 指派 elevated profile；只有後端
明確開啟 `allow_client_choice` 時，Client 才能自行要求 `trusted`。Browser Console 會依這份
後端策略產生選項，不顯示目前不可選的 profile。

單機範例設定會把 `default` 列為 elevated，使檔案修改與 Shell 成為 Agent 的預設能力；
這些工具仍受 Project Sandbox 限制，且每次執行前都會進入人工 Approval。對外部署可將
`allow_elevated_tools` 設為 `false`，一次停用所有寫入型與命令型工具。

Workspace 與 Project 都是獨立持久化實體，Project 名稱只要求在所屬 Workspace 內唯一。Session 以 `workspace_id`、`project_id` 建立正式關聯；空 `project_id` 代表該 Workspace 內未分類。釘選使用 Session 的 `pinned` 與 `pinned_at`，不依賴瀏覽器 local storage。刪除仍含有子項目的 Workspace／Project 會回傳 `409 Conflict`，不會隱含刪除或搬移。

每個 Browser 分頁以自己的 `sessionStorage` 保存目前 Workspace，不修改後端全域狀態，因此可多開前端並同時操作不同 Workspace。後端只序列化同一 Session 的 Run，不同 Session 可並行。

## 職務說明

Workspace 與 Project 都有 `instructions` 欄位，用來保存只寫一次、每次對話都套用的工作規則：

```bash
curl -X PATCH http://127.0.0.1:8787/api/v1/workspaces/WORKSPACE_ID \
  -H 'Content-Type: application/json' \
  -d '{"instructions":"回覆一律使用繁體中文；提交前必須跑過測試。"}'
```

每次 Run 由後端依 Session 所屬層級組成 `## 職務說明` 提示段落，順序為 Workspace → Project，
範圍較小者優先；與使用者本輪明確要求衝突時以本輪要求為準。呼叫端自帶的
`metadata.instructions` 會在 `StartRun` 被移除，不能藉此注入提示。單一層級上限 8000 字，
空字串代表移除該層級的說明。排程建立的對話不屬於任何 Project，因此只會套用 Workspace 的說明。

## 管理 Session

Session 建立後可更新執行設定，但不能經由 HTTP API 變更 `workspace_root`：

```bash
curl -X PATCH http://127.0.0.1:8787/api/v1/sessions/SESSION_ID \
  -H 'Content-Type: application/json' \
  -d '{"title":"正式工作區","project_id":"PROJECT_ID","model":"gpt-5.4","thinking_mode":"medium","lock_plans":true,"permission_profile":"trusted","memory_scope":"project-a","pinned":true}'
```

所有欄位皆為選填，但 request 至少必須包含一個欄位。`thinking_mode` 傳入空字串或 `auto` 會移除
Session override，恢復 Provider／後端預設；`lock_plans` 預設為 `false`。`memory_scope` 傳入空字串
會移除 Session override，恢復 Agent 預設 scope。

Session 有 queued 或 running Run 時，更新與刪除都回傳 `409 Conflict`；請先等待完成或明確取消 Run，避免執行途中更換權限／Provider 或刪除工具目錄。

後端重新啟動時，所有停在 queued／running／paused／waiting_approval 的 Run 會被標記為
`failed`（`error.code=server_restarted`、`retryable=true`），不會留下永遠佔住 Session 的殭屍 Run。
執行中的 Run 另外受 `max_wall_clock_seconds` 約束：那個上限是 Run context 的 deadline，
因此連「等待人工核准」也會在上限到達時結束，不需要人工清理。

## Session 計畫

使用者可新增多份包含客觀驗證條件的計畫；新計畫會排在列表尾端：

```bash
curl -X POST http://127.0.0.1:8787/api/v1/sessions/SESSION_ID/plans \
  -H 'Content-Type: application/json' \
  -d '{
    "title":"發布前檢查",
    "objective":"完成修改並確認品質",
    "steps":[
      {"title":"完成程式修改","verification":"語法檢查通過"},
      {"title":"執行測試","verification":"go test ./... 全部通過"}
    ]
  }'
```

`lock_plans=true` 時，列表中第一份未完成計畫為 `active`，後續計畫為 `queued`；active 完成後
會自動啟用下一份。`lock_plans=false`（預設）時，未完成計畫可同時為 `active`，不同計畫可由
不同 Run 平行執行。Console 可收合各計畫，並以拖曳後送出完整 `plan_ids` 調整順序；「清除已完成」
只會刪除 `completed` 與 `canceled`，不影響未完成計畫。已經開始執行的 active 計畫不能移到其他
未完成計畫之後。Run 執行期間不能由 HTTP 新增、重建、刪除或排序計畫，
以免 UI 與 Agent 同時改寫。Agent 使用 `plan_get`、`plan_create`、`plan_step_update` 控制
同一個有序佇列；Domain 只接受
`pending → in_progress → verifying → completed` 的依序流程，且 completed 必須附上
實際驗證證據。Session 匯出會一併包含全部計畫。舊版單數 `/plan` 端點仍保留相容行為，
優先操作目前 active 計畫，沒有 active 時才使用列表第一份。

## 排程

排程是 Workspace 層級的獨立實體，不隸屬於任何 Project 或 Session：

```bash
curl -X POST http://127.0.0.1:8787/api/v1/schedules \
  -H 'Content-Type: application/json' \
  -d '{
    "workspace_id":"WORKSPACE_ID",
    "name":"每日巡檢",
    "prompt":"檢查昨天的執行結果並整理成摘要",
    "recurrence":{"frequency":"daily","time_of_day":"09:00"},
    "sandbox_roots":[]
  }'
```

`recurrence.frequency` 只接受 `interval`（`interval_minutes` 1–10080）、`daily`（`time_of_day`）與
`weekly`（`time_of_day` 加至少一個 `weekdays`，0 是星期日）；每日與每週採後端所在時區的牆上時間。
`sandbox_roots` 與 Project 套用同一組驗證；API Client 若指定，必須使用部署主機上實際存在的
絕對目錄。留空時只使用該次執行建立的 Session 私有工作目錄。

到點時後端會建立一個全新的 Session 再送出 `prompt`，因此每次執行都是乾淨的上下文；Session
metadata 帶有 `schedule_id`。後端沒有執行的時間點不補跑，重新啟動時超過 15 分鐘寬限的
`next_run_at` 會直接對齊到下一次。

```bash
curl "http://127.0.0.1:8787/api/v1/schedules?workspace_id=WORKSPACE_ID"
curl -X PATCH http://127.0.0.1:8787/api/v1/schedules/SCHEDULE_ID \
  -H 'Content-Type: application/json' -d '{"enabled":false}'
curl -X POST http://127.0.0.1:8787/api/v1/schedules/SCHEDULE_ID/run
curl -X DELETE http://127.0.0.1:8787/api/v1/schedules/SCHEDULE_ID
```

變更 `recurrence` 或 `enabled` 時 `next_run_at` 會立即重算，停用的排程不帶 `next_run_at`。
`/run` 立刻執行一次並回傳 Run，排程原本的 `next_run_at` 不變。刪除排程不會刪除已經建立的 Session。

## 工具目錄與後端診斷

不指定 Session 時可讀取後端註冊的完整工具目錄；需要判斷權限時帶入 `session_id`：

```bash
curl 'http://127.0.0.1:8787/api/v1/tools?session_id=SESSION_ID'
curl http://127.0.0.1:8787/api/v1/admin/diagnostics
```

每個工具都回傳 `allowed`、`available` 與可能的 `unavailable_reason`。`http_fetch` 是唯一可由模型自行指定任意 URL 的內建工具，
除了 allowlist 與 permission profile，另外受管理介面的開關控制：

```bash
curl -X PUT http://127.0.0.1:8787/api/v1/admin/service-settings \
  -H 'Content-Type: application/json' \
  -d '{"service_name":"永不休息的實習生","http_fetch_enabled":false}'
```

`http_fetch_enabled` 與 `http_fetch_allow_private_networks` 省略時保留目前設定，變更立即生效，
不需要重啟後端；關閉後工具不會出現在模型的工具清單，工具目錄會回報
`http_fetch is turned off in the backend service settings`。回應大小、逾時、轉址次數與
`allowed_hosts`／`blocked_hosts` 只能由設定檔的 `http_fetch` 區塊調整。診斷資料只包含 SSH profile 名稱、API token 是否已設定，以及 Provider 的脫敏設定；不回傳 HTTP token、LLM API key、SSH 密碼、passphrase 或私鑰內容。

`allow_private_networks` 預設為 `true`，因此 `http_fetch` 預設可連線到 localhost 與私有網段；若不需要存取本機、內網或雲端 metadata 服務，請將它設為 `false`。

通知中心由 `notifications_enabled` 控制，預設為 `false`。關閉時不建立新的 Run、工具授權或版本更新通知，前端也不顯示通知按鈕；既有通知資料會保留，重新開啟後仍可查看。省略此欄位時保留目前設定：

```bash
curl -X PUT http://127.0.0.1:8787/api/v1/admin/service-settings \
  -H 'Content-Type: application/json' \
  -d '{"service_name":"永不休息的實習生","notifications_enabled":false}'
```

完整機器可讀契約位於 [`src/transport/httpapi/openapi.yaml`](../../src/transport/httpapi/openapi.yaml)，執行中的後端也會由 `/api/v1/openapi.yaml` 提供相同內容。

### 工具供應與實驗性設定

`GET／PUT /api/v1/admin/service-settings` 另提供以下欄位。PUT 仍須提供 `service_name`，其他欄位
省略即保留；Console 的開關只送出本次變更，數值表單則另行儲存。

| 欄位 | 預設 | 語意 |
|---|---|---|
| `extended_tools` | `false` | 精簡集合已含文件檢視、讀取、建立、轉換；開啟後增加編輯、SSH、記憶等工具，仍受 allowlist 限制 |
| `tool_retrieval` | `true` | 依需求縮小模型工具目錄，其餘已啟用工具可由 `find_tools` 找回 |
| `tool_call_mode` | `native` | 使用原生 `tool_calls`；不支援的 Provider 可切換 `instruction` |
| `memory_space` | `false` | 即時套用 Agent 記憶准入、去重、Project 優先 scope、較小自動召回視窗與超量整理；仍受既有 memory 設定限制 |

自動召回預設最多 6 筆，注入預算為 1200 UTF-8 bytes（雖然設定名為
`max_injected_characters`），另有綜合排序門檻。策略參數由 JSON 的 `memory.space` 提供，
service-settings 端點只切換是否啟用；完整條件見 [回憶空間](MEMORY_SPACE.md)。

### 設定包下載

`GET /api/v1/admin/config-bundle` 沿用管理 API 認證，直接回傳 `application/zip`，不包在 JSON
envelope；`Content-Disposition` 建議檔名為 `nr-intern-config.zip`。

只從 `data_dir` 讀取已存在的 `providers.json`、`mcp-servers.json`、`netpass.json`、
`service-settings.json`。缺少的檔案略過，沒有設定檔時仍產生 manifest；不包含 Workspace、
Project、Session、Run、事件、計畫、附件、記憶、通知或 OAuth token 檔，也不補入環境變數。

`manifest.json` 的 `format` 為 `nr-intern-config-bundle`、`version` 為 `1`，並提供
`created_at`、`included`、`excluded`、`redacted`、`note`。`redacted` 使用 `檔名:欄位名`
表示被遮蔽的欄位，不是每個 JSON 節點的完整路徑。

遮蔽依欄位名稱比對 `api_key`、`apikey`、`token`、`secret`、`password`、`passphrase`、
`authorization`、`credential`、`private_key`、`client_secret`；字串變為空字串，字串陣列
變為空陣列，數字與布林值保留。`headers`、`environment`、`env` 物件的每個值均清為空字串。
其他值不會自動匿名化，尤其 URL、帳號、路徑或命令列參數，分享前應人工檢查。

目前僅提供設定包匯出，沒有對應的匯入 API，亦不能以安全備份還原端點匯入。

## Harness 稽核 Entries

`GET /api/v1/sessions/{session_id}/entries` 讀取包含 operation、turn 與 tool call 生命週期的完整稽核資料。使用 `after_sequence` 與 `limit` 分頁，`limit` 預設 200、最大 1000；回應會附上 `next_after_sequence` 與 `has_more`。

分頁現在由儲存層的記憶體位置索引定位，避免每頁載入整份 transcript；API 路徑、游標與回應
形狀不變。索引不落盤，第一次讀取會建立；不能建立索引時退回掃描，真正的資料損毀仍回報錯誤。

## 建立非同步 Run

```bash
curl -X POST http://127.0.0.1:8787/api/v1/sessions/SESSION_ID/runs \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: client-generated-unique-key' \
  -d '{"input":"請搜尋 workspace 內所有 TODO"}'
```

後端先持久化 `queued` Run，再回傳 `202 Accepted` 與 `Location: /api/v1/runs/RUN_ID`。實際執行不綁定這個 request；Client 可輪詢 Run，或另外連接事件 endpoint。

## SSE Run 與斷線續接

```bash
curl -N -X POST http://127.0.0.1:8787/api/v1/sessions/SESSION_ID/runs:stream \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: client-generated-unique-key' \
  -d '{"input":"請搜尋 workspace 內所有 TODO"}'
```

事件格式：

```text
id: 42
event: message.delta
data: {"schema_version":"1.0","id":"evt_xxx","type":"message.delta",...}
```

SSE 的 `id` 是該 Run 單調遞增的 event sequence，完整事件仍有獨立 `event.id`。事件會先持久化到 `data/ai-agent/runs/events/<run-id>.jsonl`，再送出喚醒通知，因此 Client 離線不會遺失事件，連線中斷也不會取消 Run。

重新連線時使用最後完整處理的 sequence：

```bash
curl -N http://127.0.0.1:8787/api/v1/runs/RUN_ID/events \
  -H 'Last-Event-ID: 42'
```

也可用 `?after_sequence=42`；若兩者同時提供，以 `Last-Event-ID` 為準。後端先補送 sequence 42 之後的 durable events，再等待新事件。同一串流最後會收到 `run.completed`、`run.failed` 或 `run.canceled`，terminal Run 補送完畢後會關閉連線。

Browser Console 的策略是：斷線後最多重連 3 次，依序等待 400、800、1600 毫秒；每次都攜帶最後 sequence 補回缺漏，第三次仍失敗才顯示連線中斷。自訂 HTTP Client 應採相同行為，並固定重用原本的 `Idempotency-Key`，避免在尚未取得 Run ID 時重複建立工作。相同 Session 的同一 Key 若搭配不同輸入、附件、Provider 或 Model，後端會回傳 `409 Conflict`。

前端送出訊息前會先寫入 IndexedDB Durable Outbox。附件上傳成功後保存 Attachment ID，只有 Run
收到 terminal event 才移除項目；`pending`、`sending` 與 `failed` 項目在 UI 重開或網路恢復後可
繼續送出。這是 Browser 端的持久化送出佇列，不取代後端已建立 Run 的 durable 狀態。

## 後端相容性與 UI 恢復

`GET /api/v1/admin/status` 的回應包含：

```json
{
  "api_version": "1.0",
  "event_schema_version": "1.0",
  "capabilities": ["durable-outbox.v1", "run-cancel-immediate.v1", "run-recovery.v1", "run-retry.v1"]
}
```

Browser Console 會在載入 Workspace 前檢查 API major version 與必要 capabilities。缺少相容資訊、
major version 不同或缺少必要功能時，UI 會顯示版本不相容提示；Client 不應把單一新端點的 404
當成唯一相容性判斷。

UI 重開時會查詢目前 Workspace 的 queued、running、paused、waiting approval Run，先顯示
恢復視窗，確認後才重新連接勾選的 Run。若後端重啟已將 Run 標記為 `server_restarted` 且
`retryable=true`，UI 會保留重試入口，不會自動重做工具副作用。
恢復對話視窗按下「取消」只會停止該 Session 的 UI 恢復與 SSE 重連，不會取消後端仍在背景
執行的 Run。使用者之後主動開啟該 Session 時，會重新讀取真實狀態並連線，供查看或停止工作。
要取消後端執行，須使用停止按鈕或 Run cancel API。

## 管理與復原

### Run 與事件的保留範圍

Run metadata 在啟動／Save 時以 500 筆為整理基準，只淘汰最舊範圍的終態 Run，未終態不刪；
因此總數可能超過 500。啟動後與每 30 分鐘的維護只保留最新 50 筆 Run 與所有未終態 Run 的
事件檔，其餘及孤兒事件檔會清除。舊 Run 被淘汰後查詢可能回傳 404；事件檔被清理後也不能
要求完整歷史重播。`entries` 仍讀取獨立的 Session transcript，不因事件清理而刪除。

Session 用量、`by_model` 與 export 的 `runs` 只彙總仍存在的 Run，不保證終身累計。
需要長期成本或稽核資料時，應在整理前另行匯出與備份。

### 狀態恢復

後端重啟會把 queued、running、paused 與 waiting approval 的未完成 Run 標記為 `failed`、錯誤碼 `server_restarted` 並保留可重試狀態；原 Run
不會被覆寫。`pause` 只在目前 Provider request 或工具回合結束後生效，等待人工核准的 Run
不能一般暫停；`cancel-all` 則對所有尚未 terminal 的 Run 送出取消要求，並立即反映為
`canceled`。

通知中心只回傳工作狀態摘要，最多保留 1000 筆，支援 `unread_only=true`、單筆／全部已讀與
清除已讀。全域搜尋的 `q` 最多 200 字元、回傳最多 100 筆，每筆只有短 snippet，不回傳完整
對話內容。

```bash
curl -X POST http://127.0.0.1:8787/api/v1/runs/RUN_ID/pause
curl -X POST http://127.0.0.1:8787/api/v1/runs/RUN_ID/resume
curl -X POST http://127.0.0.1:8787/api/v1/runs/cancel-all
curl 'http://127.0.0.1:8787/api/v1/search?q=部署&limit=20'
curl 'http://127.0.0.1:8787/api/v1/notifications?unread_only=true'
curl http://127.0.0.1:8787/api/v1/admin/diagnostics/export -o nr-intern-diagnostics.json
curl http://127.0.0.1:8787/api/v1/admin/backup -o nr-intern-backup.zip
curl -X POST http://127.0.0.1:8787/api/v1/admin/restore \
  -F 'file=@nr-intern-backup.zip'
```

安全備份包含使用者對話、附件、記憶與其他工作資料，但不包含 Provider、OAuth、MCP 或 NetPass
憑證。還原前後端會保留 pre-restore 備份；還原成功後必須重啟，且權限中心本身只有讀取能力，
不提供從 API 或 UI 修改後端 permission policy 的入口。

版本更新檢查只連固定的 `https://api.github.com/repos/VaderChen/NR-Intern/releases/latest`，
不接受 Client 傳入檢查網址。後端啟動後會背景檢查一次，之後每 6 小時檢查；管理頁也可手動
觸發。若 Release 版本高於目前版本，且已啟用通知中心，才會建立一則去重通知，通知內可開啟
官方 Release 頁面；同一版本不會重複通知。

## 錯誤格式

非 SSE 錯誤使用 `application/problem+json`：

```json
{
  "type": "about:blank",
  "title": "Invalid input",
  "status": 400,
  "detail": "invalid input: ...",
  "code": "invalid_input",
  "request_id": "req_..."
}
```


## 長期記憶

長期記憶原本只能經由 Agent 工具（`memory_search` / `memory_remember` / `memory_forget`）存取，
使用者無法檢視 Agent 記住了什麼，也無法在 Agent 記錯時自行更正。這組端點開放同一份 repository：

```bash
# 寫入（會標記 metadata.source=operator，與 Agent 自行寫入的記憶區分）
curl -X POST http://127.0.0.1:8787/api/v1/memories \
  -H 'Content-Type: application/json' \
  -d '{"content":"使用者偏好繁體中文回覆","kind":"preference","tags":["language"]}'

# 搜尋
curl 'http://127.0.0.1:8787/api/v1/memories?q=繁體&kinds=preference&limit=20'

# 軟性遺忘：資料保留稽核資訊但不再被召回，因此回傳更新後的內容而非空回應
curl -X DELETE 'http://127.0.0.1:8787/api/v1/memories/<id>?reason=不再適用'
```

`scope` 留空時使用後端管理預設 scope，不會依 Session 的 Project 自動切換；回憶空間開啟後，
若要管理特定 Project 記憶，請明確提供 `scope=project:<id>`。
後端設定 `memory.enabled=false` 時，這組端點回傳 409 而不是假裝成功。

手動記憶 API 直接呼叫 Repository，不套用 Agent 記憶工具的准入與超量策略。DELETE 先標記
為 `forgotten`；非 active 且 UpdatedAt 早於 30 天前的紀錄會由背景維護永久清理，清理後 GET
可能回傳 404，取代鏈中的舊 ID 也可能不再可查。

### 記憶引用事件

自動召回命中會發出 `memory.recalled`，payload 含 `count`、`memory_ids`、`truncated`；
工具失敗後補召回另有 `trigger=tool_failure`。它表示模型將參考這些記憶，不表示記憶內容
已經被驗證。Console 顯示筆數與觸發原因，事件不包含完整記憶本文。

## 依狀態列出 Run

`GET /api/v1/runs?status=waiting_approval` 可以直接取得等待人工核准的 Run。
`status` 接受逗號分隔的多個值；未知的值回傳 400，而不是靜默回傳全部——
後者會讓 UI 顯示錯誤的待審清單。

## 匯出 Session

`GET /api/v1/sessions/{id}/export` 預設回傳 JSON 快照（session、project、messages、runs）。
`format=markdown` 產生人類可讀的逐字稿，工具失敗會明確標示，
因為「工具失敗但對話看起來順利」正是閱讀記錄時最需要看見的事。
`include_entries=true` 另外附上完整 transcript，供稽核與問題重現使用。

## 完成度判定

`RunResult.completion` 說明這次「完成」的可信度：

- `checks_performed > 0`：模型曾宣稱完成，但執行記錄顯示仍有未解決的工具失敗，Harness 追問過。
- `unresolved_failures` 非空：仍有工具失敗且同名工具在之後沒有成功執行過。

兩者皆空代表這次完成沒有觸發任何疑慮。呼叫端應該把
`status=completed` 但 `unresolved_failures` 非空的 Run 視為「有保留的完成」。
