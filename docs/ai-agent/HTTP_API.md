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
| GET | `/api/v1/admin/status` | 版本、instance 與啟動時間 |
| GET | `/api/v1/admin/diagnostics` | 脫敏後端設定、Provider、Session、Run 與工具統計 |
| GET | `/api/v1/tools` | 原生工具定義、allowlist 與目前可用性 |
| GET | `/api/v1/providers` | 已註冊 Provider adapter 清單 |
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
| GET | `/api/v1/agents` | Agent 清單 |
| GET | `/api/v1/agents/{agent_id}` | Agent 描述 |
| GET | `/api/v1/agents/{agent_id}/sessions` | Session 清單 |
| POST | `/api/v1/agents/{agent_id}/sessions` | 建立 Session |
| GET | `/api/v1/sessions/{session_id}` | 讀取 Session |
| PATCH | `/api/v1/sessions/{session_id}` | 更新標題、Provider、模型、權限或記憶 scope |
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
| GET | `/api/v1/sessions/{session_id}/entries` | 分頁讀取完整 Harness 稽核 entries |
| GET | `/api/v1/sessions/{session_id}/runs` | Session 的 Runs |
| POST | `/api/v1/sessions/{session_id}/runs` | 建立非同步 Run，立即回傳 `202` |
| POST | `/api/v1/sessions/{session_id}/runs:stream` | 建立或取得 Run，並從 sequence 0 回傳 SSE |
| GET | `/api/v1/runs` | Run 清單，可用 `session_id` 篩選 |
| GET | `/api/v1/runs/{run_id}` | Run 狀態與結果 |
| GET | `/api/v1/runs/{run_id}/events` | 重播並持續訂閱 Run SSE 事件 |
| POST | `/api/v1/runs/{run_id}/cancel` | 取消執行中的 Run |
| POST | `/api/v1/runs/{run_id}/decision` | 核准或拒絕等待中的高風險工具 |
| POST | `/api/v1/runs/{run_id}/retry` | 以原始輸入建立新的可追溯 Run |

Run 達到後端設定的回合、wall-clock、token 或工具呼叫上限時仍以 `completed`
收尾；`result.budget_exceeded` 會回傳觸發資源、限制值、觀察值與累計用量，
`metadata.termination` 則為 `budget_exceeded`。事件流會先送出
`run.budget_exceeded`，最後仍以 `run.completed` 結束。

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

## Workspace、Project 與 Session

正式層級為 `Workspace → Project → Session`。先建立可容納多個 Provider 的 Workspace；現階段前端為單選，因此會寫入單元素 `provider_ids`，但資料與 API 不需在未來擴充時搬移：

```bash
curl -X POST http://127.0.0.1:8787/api/v1/workspaces \
  -H 'Content-Type: application/json' \
  -d '{"name":"產品開發","provider_ids":["openai-compatible"],"default_provider_id":"openai-compatible","model":"gpt-5.4"}'
```

`default_provider_id` 必須存在於 `provider_ids`。Session／Run 的 Provider override 也必須屬於該 Workspace 集合；仍有 Session 引用時不能移除該 Provider。目前 Provider 工廠只實作 `openai-compatible`，Router、Workspace 與 Harness 介面則允許日後註冊其他協定類型。

建立 Project 時可透過 `sandbox_roots` 指定多個既有的絕對目錄。Session 屬於該 Project 時，原生檔案工具與 Shell 的工作目錄只能落在其中一個根目錄內；空陣列則使用 Session 私有工作目錄：

```json
{
  "name": "NR-Intern",
  "workspace_id": "workspace_xxx",
  "sandbox_roots": [
    "projects/nr-intern",
    "documents/specifications"
  ]
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

## 管理 Session

Session 建立後可更新執行設定，但不能經由 HTTP API 變更 `workspace_root`：

```bash
curl -X PATCH http://127.0.0.1:8787/api/v1/sessions/SESSION_ID \
  -H 'Content-Type: application/json' \
  -d '{"title":"正式工作區","project_id":"PROJECT_ID","model":"gpt-5.4","permission_profile":"trusted","memory_scope":"project-a","pinned":true}'
```

所有欄位皆為選填，但 request 至少必須包含一個欄位。`memory_scope` 傳入空字串會移除 Session override，恢復 Agent 預設 scope。

Session 有 queued 或 running Run 時，更新與刪除都回傳 `409 Conflict`；請先等待完成或明確取消 Run，避免執行途中更換權限／Provider 或刪除工具目錄。

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

列表中第一份未完成計畫為 `active`，後續計畫為 `queued`；active 完成後會自動啟用下一份。
Console 可收合各計畫，並以拖曳後送出完整 `plan_ids` 調整順序。已經開始執行的 active
計畫不能移到其他未完成計畫之後。Run 執行期間不能由 HTTP 新增、重建、刪除或排序計畫，
以免 UI 與 Agent 同時改寫。Agent 使用 `plan_get`、`plan_create`、`plan_step_update` 控制
同一個有序佇列；Domain 只接受
`pending → in_progress → verifying → completed` 的依序流程，且 completed 必須附上
實際驗證證據。Session 匯出會一併包含全部計畫。舊版單數 `/plan` 端點仍保留相容行為，
優先操作目前 active 計畫，沒有 active 時才使用列表第一份。

## 工具目錄與後端診斷

不指定 Session 時可讀取後端註冊的完整工具目錄；需要判斷權限時帶入 `session_id`：

```bash
curl 'http://127.0.0.1:8787/api/v1/tools?session_id=SESSION_ID'
curl http://127.0.0.1:8787/api/v1/admin/diagnostics
```

每個工具都回傳 `allowed`、`available` 與可能的 `unavailable_reason`。診斷資料只包含 SSH profile 名稱、API token 是否已設定，以及 Provider 的脫敏設定；不回傳 HTTP token、LLM API key、SSH 密碼、passphrase 或私鑰內容。

完整機器可讀契約位於 [`src/transport/httpapi/openapi.yaml`](../../src/transport/httpapi/openapi.yaml)，執行中的後端也會由 `/api/v1/openapi.yaml` 提供相同內容。

## Harness 稽核 Entries

`GET /api/v1/sessions/{session_id}/entries` 讀取包含 operation、turn 與 tool call 生命週期的完整稽核資料。使用 `after_sequence` 與 `limit` 分頁，`limit` 預設 200、最大 1000；回應會附上 `next_after_sequence` 與 `has_more`。

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

Browser Console 的策略是：斷線後最多重連 3 次，依序等待 400、800、1600 毫秒；每次都攜帶最後 sequence 補回缺漏，第三次仍失敗才顯示連線中斷。自訂 HTTP Client 應採相同行為，並固定重用原本的 `Idempotency-Key`，避免在尚未取得 Run ID 時重複建立工作。

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

`scope` 留空時使用後端預設 scope，與 Harness 對 Session 的預設一致。
後端設定 `memory.enabled=false` 時，這組端點回傳 409 而不是假裝成功。

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
