# 安全與權限邊界

## 網路邊界

- `listen_address` 是 loopback 時可不設 token；監聽非 loopback 位址時，啟動設定必須提供 `api_token`，否則後端拒絕啟動。
- 除 `/healthz`、`/readyz` 與 CORS preflight 外，設定 token 後所有 API（包含 diagnostics、OpenAPI 與 SSE）都需要 Bearer token。
- 未設定 `allowed_origins` 時只允許 loopback Browser origin；跨來源白名單不等同認證。若使用萬用 Origin，必須同時設定 API token。
- HTTP request body 有大小上限、JSON 拒絕未知欄位與多個根值；request ID 只接受長度不超過 128 的安全字元。
- 回應加入 `nosniff`、`no-referrer` 與禁止 frame 的安全標頭。
- 伺服器內部錯誤只對 Client 回傳通用訊息與 request ID；詳細原因寫入後端紀錄，避免暴露檔案路徑或內部狀態。

桌面 Console 只允許監聽 loopback。桌面控制 API 使用 HttpOnly、SameSite=Strict cookie；Browser 不會取得後端 Bearer token，reverse proxy 只在送往後端時加入。

## Workspace、Session 與 Run

- Workspace 是管理與 Provider 預設值的邊界；不是後端全域切換狀態。每個 Browser 分頁可獨立選取 Workspace。
- Project 必須屬於 Workspace；Session 的 `workspace_id` 與 `project_id` 必須一致。
- 啟動時會檢查已持久化的 Workspace／Project／Session 關聯與 Provider 引用；資料不一致時 fail fast，不在背景靜默搬移或改寫。
- Workspace／Project 只允許在沒有子項目時刪除，不做隱含級聯刪除。
- Session 有 queued/running/waiting_approval Run 時，禁止更新或刪除 Session，避免執行途中改變 Provider、工具權限、workspace path 或移除資料。
- Session 的工具沙箱根目錄由後端在建立 Session 時寫入，request 帶入的 `metadata.workspace_root` 會被覆寫。

## 工具沙箱範圍

工具能觸及的檔案系統範圍**不侷限於 Session 目錄**，而是由 Session 所屬 Project 的
`sandbox_roots` 決定：

- Project 沒有設定 `sandbox_roots` 時，沙箱是該 Session 專屬的 `sessions/<id>/workspace/` 目錄。
- Project 設定了 `sandbox_roots` 時，沙箱是那些目錄；工具可以讀寫其中任何路徑。

`sandbox_roots` 是**後端根據 Project 實體產生的保留欄位**，呼叫端無法自行擴權。
`metadata.sandbox_roots` 在三處被剝除：建立 Session（`application.Service.CreateSession`）、
建立 Run（`StartRun`，剝除後才從 Project 填入）、以及持久化層（`filestore.SessionRepository.Create`）。

Project 的 `sandbox_roots` 可以經由 `POST /api/v1/projects` 與 `PATCH /api/v1/projects/{id}` 設定，
並在寫入前驗證：必須是絕對路徑、解析 symlink 後必須存在且為目錄、不得是檔案系統根目錄、
重複值會去除。儲存的是解析後的實體路徑，與 `ResolvePath` 的比較形式一致。

多根沙箱下，相對路徑以第一個根目錄為基準；絕對路徑必須落在任一根之內，
否則回報 `path is outside the project sandbox`。每個根各自套用單根的逃逸檢查
（解析最深既有父路徑與 symlink），多根不會放寬單根的限制。

**這代表在單一共用 Bearer token 的信任模型下，任何通過驗證的 Client 都能建立
指向機器上任意既有目錄的 Project，並讓 Agent 在其中讀寫。** 這是刻意的設計
（使用者要指定 Agent 的工作目錄），但也表示 token 的保護範圍等同於這些目錄的存取權。
需要更窄的邊界時，用作業系統帳號權限限制後端程序本身能觸及的範圍。
- 同一 Session 的 Run 經 single-writer gate 序列化，不同 Session 可並行。

## Provider 與秘密

- Provider adapter 只由後端設定註冊，模型不能新增 endpoint。Workspace 只能引用已註冊 Provider ID。
- Workspace 保存 `provider_ids` 集合與其中一個 `default_provider_id`；Session／Run override 必須是已註冊且屬於該 Workspace 集合的 ID。仍被 Session 引用的 Provider 不可直接移出集合。
- Diagnostics 只回傳 endpoint、協定、預設模型、串流能力與「API key 是否存在」，不回傳 API key、額外 header 值或 token。
- API key 應由環境變數或權限受限的設定檔提供；不要把真實秘密提交到版本控制。

## 權限 profile 與信任模型

後端目前只有單一共用 Bearer token，沒有呼叫端身分。因此**任何通過 token 驗證的 Client 都是同等信任的**，
Session 的 permission profile 不能被當成對抗 API 呼叫端的防線，只能是後端自己設定的邊界。

`permissions` 設定區塊是唯一決定 profile 的地方：

- `default_profile`：未指定時套用的 profile。
- `elevated_profiles`：後端承認為 elevated 的 profile 名稱。**空集合代表沒有任何 profile 能使用高權限工具**（fail closed）。
- `allow_client_choice`：API request 是否可以指定 `permission_profile`，預設 `false`。

`allow_client_choice=false` 時，`POST /sessions` 或 `PATCH /sessions/{id}` 只要帶入非預設的
`permission_profile` 就會得到 400，而不是靜默忽略——「要求提權但沒有生效」必須是可見的錯誤。
需要在本機取得完整權限時，正確做法是把 `default_profile` 設成 `elevated_profiles` 之一
（等於宣告整個後端受信任），或明確開啟 `allow_client_choice`；兩者都是後端設定寫出來的決定。

`allow_elevated_tools=false` 一律優先，任何 profile 都拿不到高權限工具。

## 原生工具

- 工具必須同時通過後端 allowlist 與 Session permission profile；profile 本身由上述後端策略指派。
- 寫檔、Shell 與 SSH 是 elevated tools；還需要 `allow_elevated_tools=true`。
- elevated tool 在實際執行前還要經過單次人工 Approval；permission profile 是資格邊界，Approval 是本次具體參數的副作用確認，兩者不能互相取代。
- Approval decision 必須攜帶目前 `approval_id`；後端拒絕過期或不相符的決策，避免舊 UI 操作誤套到下一個工具呼叫。
- Session 永久核准只能在有效 pending approval 上以 `approve + permanent` 開啟，不能透過一般 Session PATCH 提權；拒絕、過期 approval 或其他 Session 都不會繼承。
- Approval 對外參數會移除 `content`、`old_text`、`new_text`、`patch` 等本文欄位，只顯示路徑、操作模式與前置條件等判斷副作用所需資料。
- 檔案工具會解析最深既有父路徑與 symlink，限制在目前 Session 的沙箱根目錄集合內（見上節「工具沙箱範圍」）。
- 文件工具同樣受 Sandbox 限制，並限制來源檔案、Open XML 解壓項目與回傳內容大小；只讀取 PDF、DOCX、XLSX、PPTX 的必要結構，不執行文件中的巨集、外部關聯或嵌入物件。
- Shell 子程序只繼承必要 OS 環境，避免無條件洩漏 Provider API key 或後端 token；timeout/cancel 會終止程序樹。
- SSH 憑證只存在後端 profile。正式環境應設定 known_hosts 或 SHA-256 host key；`insecure_ignore_host_key` 只供明確接受風險的隔離環境。
- SSH 初始連線最多三次；遠端命令執行中斷線不自動重跑，避免重複副作用。

## 本機資料

Filestore 目錄預設使用 owner/group 受限權限，記憶、Workspace 與 Project snapshot 使用 `0600`。實際保護仍取決於作業系統帳號、父目錄 ACL、備份與磁碟加密。Session transcript、工具結果與長期記憶可能含敏感內容，應按資料生命週期管理。
