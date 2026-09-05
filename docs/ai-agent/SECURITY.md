# 安全與權限邊界

## 網路邊界

- `listen_address` 是 loopback 時可不設 token；監聽非 loopback 位址時，啟動設定必須提供 `api_token`，否則後端拒絕啟動。
- 除 `/healthz`、`/readyz` 與 CORS preflight 外，設定 token 後所有 API（包含 diagnostics、OpenAPI 與 SSE）都需要 Bearer token。
- 未設定 `allowed_origins` 時只允許 loopback Browser origin；跨來源白名單不等同認證。若使用萬用 Origin，必須同時設定 API token。
- HTTP request body 有大小上限、JSON 拒絕未知欄位與多個根值；request ID 只接受長度不超過 128 的安全字元。
- 回應加入 `nosniff`、`no-referrer` 與禁止 frame 的安全標頭。
- 伺服器內部錯誤只對 Client 回傳通用訊息與 request ID；詳細原因寫入後端紀錄，避免暴露檔案路徑或內部狀態。

後端有四類對外連線，信任來源不同，不能共用同一套假設：

- Provider endpoint 只來自後端管理設定；模型無法自行新增或改寫 endpoint。API Key 與 OAuth Token
  不會進入 Prompt、診斷回應或 Browser JavaScript。
- 遠端 MCP endpoint 也只由管理者設定，但目前只驗證 http／https URL，**不套用**
  `http_fetch` 的私有網段阻擋。這是刻意允許連接內部 MCP Server 的管理能力，因此能修改 MCP
  設定的 Client 與後端管理員同等受信任。
- `http_fetch` 是唯一可由模型自行決定任意目標網址的內建工具，套用下列額外限制。
- NetPassClient 是選用的對外通道，會把 backend API port 暴露到公共網路；它不公開 desktop UI，
  但也不會代替後端 Bearer 認證。

`http_fetch` 的限制如下：

- 工具必須同時通過 allowlist、elevated permission profile、單次人工 Approval，以及 `ServiceSettings.http_fetch.enabled` 開關；管理介面關閉後工具立即從模型的工具清單消失，不需重啟後端。
- 私有網段預設允許：`allow_private_networks` 預設為 `true`，因此 localhost、loopback、RFC1918、link-local（含 `169.254.169.254` 這類雲端 metadata 位址）、CGNAT 與 multicast 都可連線；設為 `false` 時全部拒絕。判斷在 Dial 階段執行，因此 DNS 重新綁定與轉址無法繞過。若不需要存取私有服務，建議由管理介面關閉此開關。
- 不使用系統 Proxy 設定，避免實際連線目標與網址主機不一致而讓上述檢查失效。可另外以 `http_fetch.allowed_hosts` 限制成白名單，`blocked_hosts` 一律優先拒絕。
- 只允許 http／https 與 GET、HEAD、POST；`Host`、`Content-Length`、`Connection` 等由 Transport 管理的 header 不可覆寫，帶換行的 header 值直接丟棄。回應大小、逾時與轉址次數都有上限，非文字內容不會送進上下文。
- Approval 對話框會顯示 `url`、`method`、`headers` 與 `body`，讓使用者在送出前確認實際會離開本機的內容。

ChatGPT／Codex OAuth 使用 authorization-code + PKCE，callback server 只監聽 loopback 且只在驗證期間
存在。access token、refresh token 與 verifier 不透過管理 API 回傳；狀態 API 只提供連線狀態與可顯示
的帳號資訊。

NetPass 連線前必須保存自己的 API Key 並明確接受使用政策；這只保護 NetPass 通道的建立，不保護
通道後方的 HTTP API。啟動前必須另外設定後端 `api_token`，並將公開網址與該 Token 視為同等敏感。
目前 NetPass 啟動流程不會強制檢查 `api_token` 是否存在，因此這項部署檢查由操作人員負責。

桌面 Console 只允許監聽 loopback。桌面控制 API 使用 HttpOnly、SameSite=Strict cookie；Browser 不會取得後端 Bearer token，reverse proxy 只在送往後端時加入。`/.well-known/nr-intern-desktop` 與 `/desktop/restore` 只用於本機第二次啟動時辨識並恢復既有視窗，未暴露到 NetPass 通道。

## Workspace、Session 與 Run

- Workspace 是管理與 Provider 預設值的邊界；不是後端全域切換狀態。每個 Browser 分頁可獨立選取 Workspace。
- Project 必須屬於 Workspace；Session 的 `workspace_id` 與 `project_id` 必須一致。
- 啟動時會檢查已持久化的 Workspace／Project／Session 關聯與 Provider 引用；資料不一致時 fail fast，不在背景靜默搬移或改寫。
- Workspace／Project 只允許在沒有子項目時刪除，不做隱含級聯刪除。
- Session 有 queued/running/paused/waiting_approval Run 時，禁止更新或刪除 Session，避免執行途中改變 Provider、工具權限、workspace path 或移除資料。
- Session 的工具沙箱根目錄由後端在建立 Session 時寫入，request 帶入的 `metadata.workspace_root` 會被覆寫。
- 職務說明（Workspace／Project 的 `instructions`）由後端依 Session 所屬層級注入提示；`StartRun` 會先移除呼叫端自帶的 `metadata.instructions`，避免 Client 直接對提示注入內容。單一層級上限 8000 字。

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

排程（Schedule）保存自己的 `sandbox_roots`，套用與 Project 完全相同的寫入前驗證。排程建立的
Session 不屬於任何 Project，沙箱只來自該排程；路徑經 `RunInput.SandboxRoots` 這個 `json:"-"`
的後端專用欄位傳入，HTTP 呼叫端無法自行帶入或擴權。後端重新啟動時，儲存的沙箱目錄若已不存在，
該排程會被停用並記錄原因，而不是讓後端起不來。

**這代表在單一共用 Bearer token 的信任模型下，任何通過驗證的 Client 都能建立
指向機器上任意既有目錄的 Project，並讓 Agent 在其中讀寫。** 這是刻意的設計
（使用者要指定 Agent 的工作目錄），但也表示 token 的保護範圍等同於這些目錄的存取權。
需要更窄的邊界時，用作業系統帳號權限限制後端程序本身能觸及的範圍。
- 同一 Session 的 Run 經 single-writer gate 序列化，不同 Session 可並行。

### Run 異常恢復與控制

- 後端重啟時，原本 queued、running、paused 或 waiting approval 的 Run 會標記為 `failed`，錯誤碼為
  `server_restarted` 且可重試；不會假裝恢復未完成的 Provider 或工具副作用。原 Run 保留，重試
  必須建立新的 Run 並以 `metadata.retry_of` 追溯來源。
- pause 只在安全回合邊界生效，不能用來強制切斷已送出的 Provider request；waiting approval
  Run 必須先完成核准流程或取消。pause／resume／terminal transition 共用狀態與事件序列化，
  避免舊快照覆蓋較新的 terminal 狀態。
- 啟用通知中心時只保存工作狀態摘要，不保存完整 prompt、工具參數或工具輸出；去重鍵避免重播
  事件造成重複通知。通知資料仍屬使用者資料，應依一般執行資料的生命週期保護；關閉通知中心
  時不建立新的通知，既有資料保留。

### 診斷與安全備份

- Diagnostics export 會移除 DataDir、API token 與其他憑證欄位，且不輸出對話全文；正常的管理
  diagnostics endpoint 仍可能包含管理者排障所需的本機設定，因此只應交給受信任的管理者。
- 安全備份排除 `providers.json`、`provider-oauth-tokens.json`、`mcp-servers.json` 與
  `netpass.json`，但不是匿名資料包：sessions、transcript、附件、記憶與排程可能含使用者
  個資，Session metadata 也可能包含本機工作目錄資訊。備份檔應存放在受限位置，不應直接上傳
  公開服務或版本庫。
- 還原只允許固定資料目錄白名單，拒絕絕對路徑、`..` segment、符號連結、資料目錄符號連結及
  超過 512 MiB 的解壓內容；寫入前會檢查目標與暫存檔，還原前另存 pre-restore backup。還原
  完成後必須重啟後端，讓 filestore 與既有記憶體狀態重新載入。
- Permission center 是唯讀資訊頁；它不接受修改 profile、elevated allowlist 或 Approval
  狀態的請求。任何高權限操作仍須同時符合後端 policy 與本次人工核准。

## Provider 與秘密

### 設定包的分享邊界

`GET /api/v1/admin/config-bundle` 與其他管理 API 共用 Bearer 認證，權限等同受信任的管理者。
匯出僅讀取 `data_dir` 四種設定檔與產生的 manifest，不遍歷對話資料，也不讀 OAuth token 檔。
這與安全備份不同：安全備份保留使用者資料；設定包保留 Provider、MCP、NetPass 與服務設定結構。

秘密名稱比對會清除 API Key、token、password 等字串值與字串陣列；Header／environment／env
物件保留鍵但清空值。數字與布林值不清除，避免誤傷 `max_tokens` 等上限。
`manifest.json` 只記錄被遮蔽的欄位名稱，不保存其原值。

**設定包不是匿名資料包，也不是任意文字的秘密掃描器。** URL 中的 userinfo／query、帳號、
本機絕對路徑、command／args、非標準名稱的文字欄位都可能保留。金鑰應使用專用秘密欄位，
不要嵌入 URL 或命令列；匯出檔僅供受信任管理者參考，分享前仍須人工檢查，不應直接公開。
目前沒有設定包專用匯入流程，不能當成完整備份還原。

### 憑證管理

- Provider adapter 只由後端設定註冊，模型不能新增 endpoint。Workspace 只能引用已註冊 Provider ID。
- Workspace 保存自己的 `provider_ids` 集合與其中一個 `default_provider_id`，用來提供新對話及未指定 override 時的預設值；Session／Run override 可選擇任一全域已啟用 Provider。仍被 Workspace 或 Session 引用的 Provider 不可停用或刪除。
- Provider 管理 API 只以 `has_api_key`／`has_oauth_token` 表示憑證狀態；Diagnostics 只回傳 endpoint、協定、預設模型、串流能力與是否已設定憑證，不回傳 API Key、額外 header、access token 或 refresh token。
- OpenAI-compatible API Key、ChatGPT／Codex OAuth Token、MCP Bearer Token／headers／environment 與 NetPass Key 都應只存在環境變數或權限受限的設定檔；不要把真實秘密提交到版本控制。
- Provider 與 MCP 的完整集合更新使用「省略即保留」語意，避免管理頁讀不到明文後又以空值覆寫；清除必須使用各欄位定義的明確空值或 `clear_*` 操作。

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

## 內建與 MCP 工具

- 工具必須同時通過後端 allowlist 與 Session permission profile；profile 本身由上述後端策略指派。
- 精簡工具集已包含文件建立與轉換；進入目錄或被 `find_tools` 找到不代表核准，仍須通過原有
  Sandbox、elevated policy、輸入大小與 Approval 檢查。Schema 導向參數正規化只處理型別／格式，
  不授予額外工具或路徑權限；MCP 參數與結果仍視為外部不受信任資料。
- 寫檔、Shell 與 SSH 是 elevated tools；還需要 `allow_elevated_tools=true`。
- elevated tool 在實際執行前還要經過單次人工 Approval；permission profile 是資格邊界，Approval 是本次具體參數的副作用確認，兩者不能互相取代。
- Approval decision 必須攜帶目前 `approval_id`；後端拒絕過期或不相符的決策，避免舊 UI 操作誤套到下一個工具呼叫。
- Session 永久核准只能在有效 pending approval 上以 `approve + permanent` 開啟，不能透過一般 Session PATCH 提權；拒絕、過期 approval 或其他 Session 都不會繼承。
- Approval 對外參數會移除 `content`、`old_text`、`new_text`、`patch` 等本文欄位，只顯示路徑、操作模式與前置條件等判斷副作用所需資料。
- 檔案工具會解析最深既有父路徑與 symlink，限制在目前 Session 的沙箱根目錄集合內（見上節「工具沙箱範圍」）。
- 文件工具同樣受 Sandbox 限制，並限制來源檔案、範本、結構化輸入、輸出文件、PDF 操作頁數／總量、渲染頁數／大小與 Open XML 解壓項目大小；不執行 PDF、DOCX、XLSX、PPTX 中的巨集、外部關聯或嵌入物件。`document_create`、`document_edit`、`document_convert`、`pdf_pages`、`document_render` 屬於 elevated tools；編輯與轉換必須另存新檔。模型指定的 TTF 必須位於 Sandbox；自動字型探索只讀固定的應用程式與作業系統字型目錄，回傳結果不揭露絕對路徑。LibreOffice／Poppler 只從固定環境變數、PATH、封裝資源與標準安裝位置探索，不接受工具參數指定任意 executable，且子程序只繼承 PATH、HOME、暫存目錄、語系與必要的作業系統環境，不繼承 Provider API key 或後端 token。Approval 對外參數會隱藏 blocks、sheets、slides、replacements、cell_updates、annotations、sources 與 pages 的實際本文。
- `http_fetch` 是唯一可由模型自行指定任意 URL 的內建工具，邊界見上節「網路邊界」；關閉後不會出現在模型的工具清單。
- MCP 工具一律標記 `RequiresPermission=true`，必須通過 elevated profile；權限邊界不因唯讀而放寬。
- **唯讀工具可免逐次 Approval，但不是無條件豁免。** MCP 工具的唯讀屬性來自 Server 自己宣告的 `readOnlyHint`，屬於外部不受信任資料，只有管理者對該 Server 開啟 `trust_annotations` 後才會被採信；未開啟時 MCP 工具不算唯讀，仍然逐次核准。跳過核准會發出 `run.approval_skipped`（`reason=read_only_tool`）留下紀錄。
- **記憶體隔離專案的對話在執行期間就不落地。** transcript、計畫、附件與 Run 事件在寫入當下即
  指向該 Project 的 RAM disk；`runs.json` 與 `notifications.json` 是單一檔案無法分流，改為只留在
  記憶體、不寫入。歸屬編碼在 Session／Run ID 裡，磁碟未掛載時解析回到預設根，該對話即視為不存在。
  磁碟不在時解析回傳 `ErrNotFound`、讀寫一律失敗，不會退回 `dataDir`。
  模型主動寫入回憶空間（`memory_remember`）的內容同樣不落地，且隔離對話一律使用專案專屬
  記憶 scope、不接受 `memory_scope` 覆寫——共用 scope 會讓一筆隨程序結束消失的記憶就地改寫或
  取代掉持久記憶。
  **邊界**：`shell_exec` 啟動的是真實子行程，仍可寫到 RAM disk 以外的路徑；管理者透過記憶 API
  直接寫入該專案 scope 的內容（非對話產生、無來源 Session）仍會持久化。作業系統的分頁檔、
  當機傾印與外部備份也不在本功能的控制範圍內。
- **用量上限重置會消耗帳號的有限額度且無法還原。** 只由使用者在 Provider 設定按下按鈕觸發，
  前端二次確認、後端不掛任何排程或預先擷取。重送沿用同一個 `Idempotency-Key`，因為連線中斷時
  無法分辨「沒送出」與「送出了但沒收到回應」，換鑰匙會扣掉第二次。額度不足與先前已兌換都以
  正常結果回覆，不當成錯誤重試。
- **記憶體隔離專案免逐次 Approval。** 這類專案的 sandbox 是揮發性 RAM Disk，關閉程式即消失，
  逐次詢問只會把使用者訓練成無條件按下核准，反而讓真正有副作用的操作更容易被順手放行。
  豁免會發出 `run.approval_skipped`（`reason=ephemeral_project`）留下紀錄。
  依據是後端在組裝 Run metadata 時寫入的 `ephemeral_project` 旗標，與 `sandbox_roots`
  一樣會先清除 Client 夾帶的同名值，且讀取端只接受布林 `true`。
  **這個豁免的邊界必須講清楚**：`shell_exec` 啟動的是真實子行程，sandbox 對它沒有強制力——
  working directory 在 RAM Disk 上，不代表命令不能寫到其他路徑。因此這裡豁免的語意是
  「信任這個專案裡執行的命令」，不是「保證只會寫入 RAM Disk」。需要嚴格邊界時，
  應改用一般專案並保留逐次核准。
- `trust_annotations` **預設開啟**：MCP Server 由管理者自己加入，唯讀查詢逐次核准只會增加不必要的操作負擔。設定檔沒有這個欄位時採預設值；明確設為 `false` 時保存並沿用該決定，不會被預設值蓋掉。對不完全信任的 Server 應主動關閉——關閉後該 Server 的所有工具都逐次核准。
- MCP stdio 子程序只繼承必要 OS 環境以及管理者明確設定的變數；SSE 與 Streamable HTTP 的 Bearer Token、Basic Auth 與 headers 不會顯示於工具定義、Prompt 或管理 API 回應。管理 API 只回傳 `auth_mode`（實際送出的驗證方式），讓使用者能確認憑證有被採用而不必顯示明文。
- MCP 連線在伺服器 session 失效或重啟後會自動重連。只有「伺服器明確拒收」的錯誤（session not found／session 過期／連線未建立）才會自動重送工具呼叫，因為這種情況遠端不可能執行過；其餘連線錯誤仍只對宣告 idempotent 的工具重試，避免重複副作用。
- MCP Server 回傳的工具描述、schema 與內容屬於外部不受信任資料。Harness 可用它來定義及執行工具，但不應把描述文字視為高於使用者要求、權限政策或 Host 指示的系統命令。
- Shell 子程序只繼承必要 OS 環境，避免無條件洩漏 Provider API key 或後端 token；timeout/cancel 會終止程序樹。
- SSH 憑證只存在後端 profile。正式環境應設定 known_hosts 或 SHA-256 host key；`insecure_ignore_host_key` 只供明確接受風險的隔離環境。
- SSH 初始連線最多三次；遠端命令執行中斷線不自動重跑，避免重複副作用。
- `ssh_wait` 只能用於 Agent 指定的唯讀、冪等檢查命令；它會重新建立 session 進行輪詢，且仍需要 elevated profile 與人工 Approval。上傳／部署命令不得放入輪詢 command，避免因重試造成重複副作用。
- `wait_for` 不執行 Shell 命令，只在 Run context 與最大等待時間內暫停並發出進度；等待完成本身不構成遠端部署成功證據。

## 用量與成本資料

- Run 的 token 用量只採信後端 Provider adapter 回報的 usage；Application 在單一 Run 收尾時
  保存快照，不從可被 Client 修改的事件或 transcript 反推，避免重播造成重複統計。
- `model_prices` 是非秘密的後端設定，但只由設定檔載入，Run request 與模型輸出不能自行提供
  價格。成本目前只接受 USD，沒有對應價格時 API 只回傳 token，不假造金額。
- Session 用量依已保存的 Run 快照即時彙總，不落盤到 `session.json`；取消、失敗與重試都保留
  各自的 Run 紀錄。成本估算不代表 Provider 的帳單，仍應以 Provider 官方帳務為準。
- Run metadata 受保留規則整理後，Session 用量與匯出僅涵蓋尚存 Run，可能低於曾經顯示的累計值。
  不得視為不可刪除的帳務或稽核總帳。

## 本機資料

記憶體隔離 Project 的掛載只接受後端驗證為至少 256 MB、且不超過主機實體記憶體 75% 的容量與程式產生的名稱，不把使用者
輸入拼成 Shell 指令。每個 Project 使用獨立 RAM disk，不能以共用根目錄讀取其他隔離專案。
macOS 殘留磁碟、Linux tmpfs 子目錄與 Windows ImDisk 磁碟都必須同時通過固定前綴、專用 JSON
標記、平台種類及建立程序存活檢查才會清理；裝置名稱另做格式限制。正常卸載失敗才嘗試強制卸載。
Windows 找不到 ImDisk 時直接回報相依套件缺失，不以硬碟暫存目錄冒充 RAM disk。ImDisk 的驅動
安裝、提權方式、現代 Windows 與 ARM64 支援仍待實機驗證，因此目前不屬於已驗證的安全保證。

`ephemeral=true` 時，Run 的 Project Sandbox 會導向 RAM disk，且 API 不接受同時指定主機
`sandbox_roots`。執行期間 transcript、附件、計畫與 Run 資料仍由既有 Repository 管理；正常關閉
會清除，異常退出則在下次啟動、HTTP Handler 對外服務前補做。Project 設定本身保留，因此重啟後
只留下空的隔離 Project。隔離旗標與容量不提供 PATCH，避免未實作資料搬移前產生錯誤承諾。

啟用 `memory_space` 時，Agent 記憶工具透過 Manager 檢查種類與常見憑證樣式，預設 scope
收斂到 Project，沒有 Project 時退回 Workspace；明確 `memory_scope` 優先。此為檢索範圍
約定，不是多租戶存取控制。本文樣式檢查不涵蓋所有憑證、個資或任意欄位，可重用性也仍由模型
判斷，不能宣稱敏感內容一定不會寫入。詳見 [回憶空間](MEMORY_SPACE.md)。

已認證的記憶管理 API 仍直接使用 Repository，包含手動寫入；它不套用 Agent 的 Manager
准入／去重／scope 淘汰規則，應只提供給同等受信任的管理者。

### 儲存保留與永久移除

目前保留值固定在程式內，沒有設定開關。RunRepository 啟動與 Save 時以 500 筆為整理基準，
只淘汰最舊範圍中的已結束 Run，未終態保留。Runtime 啟動後及每 30 分鐘只保留最新 50 筆 Run
與所有未終態 Run 的事件檔；孤兒與較舊事件檔永久刪除。非 active 且 UpdatedAt 早於 30 天前
的記憶也會永久清理，不受回憶空間開關影響。

Session transcript 不因上述維護而刪除，但不能從對話文字完整還原 Run metadata、用量快照
或 SSE 事件序列。升級前應備份，長期稽核需另存匯出；沒有備份的清理資料無法恢復。

上下文字元限制只縮減模型所見的歷史，不會清除持久化 transcript，也不是資料保留／刪除政策。
新 `model request` 日誌只記工具名稱、訊息數與字元數，不記錄提示或參數本文。

Filestore 目錄預設使用 owner/group 受限權限，記憶、Workspace 與 Project snapshot 使用 `0600`。
Provider、OAuth、MCP 與 NetPass 的持久化秘密同樣使用權限限制檔案，且管理 API 只回傳脫敏狀態。
實際保護仍取決於作業系統帳號、父目錄 ACL、備份與磁碟加密。Session transcript、工具結果、
附件與長期記憶可能含敏感內容，應按資料生命週期管理。
