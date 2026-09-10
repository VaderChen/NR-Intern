# 執行與開發

## 設定

複製 `configs/ai-agent/config.example.json`，或使用環境變數：

| 環境變數 | 說明 |
|---|---|
| `AI_AGENT_LISTEN` | 後端監聽位址 |
| `AI_AGENT_DATA_DIR` | Session、Run 與 workspace 根目錄 |
| `AI_AGENT_RAM_DISK_ENABLED` | 是否在 Runtime 啟動時準備 RAM disk；預設開啟 |
| `AI_AGENT_RAM_DISK_SIZE_MB` | RAM disk 預設容量，預設 512 MiB；Project 實際容量至少 256 MiB且不得超過主機實體記憶體的 75% |
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

`ram_disk` 控制記憶體隔離 Project 的生命週期能力：macOS 以 `hdiutil` 建立 HFS+ RAM disk，Linux 在
`/dev/shm` 建立獨立且有 `size` 配額的 tmpfs 掛載（需要 CAP_SYS_ADMIN，失敗不退回普通目錄），
Windows 依賴 PATH 中可執行的 `imdisk.exe`。未知平台才使用系統
暫存目錄並標示為非真正揮發性儲存。每個記憶體隔離 Project 都有獨立磁碟與包含 Project ID、
建立程序 PID 的專用標記；正常關閉會清理，異常退出後由下次啟動清除。

磁碟分成 `workspace` 與 `store` 兩層：Run 的第一個 Sandbox 根目錄指向 `workspace`，後端資料放在
`store`，Agent 因此讀不到自己的 transcript 與計畫。Session、transcript、計畫與附件在**寫入當下**
就落在 `store`（歸屬編碼在 Session ID，事件檔用 Run ID）；`runs.json`、`notifications.json` 與
`memories.json` 是單一檔案無法分流，改為只留在記憶體、不寫入。因此只有 Project 設定跨程序保留。
`purgeEphemeralProjectSessions` 保留一輪清理舊版殘留，下一版移除。
Windows ImDisk 尚待目標 x64／ARM64 封裝與權限實測。

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

Codex 的模型清單來自帳號專屬的 model manifest，而 manifest **依 client version 分流**。
`codexManifestClientVersion`（`src/adapters/openaicompat/probe.go`）太舊會拿到舊清單、新模型
不會出現，而且不會有任何錯誤訊息——症狀就只是「模型沒有更新」。對不到新模型時先查這個常數，
並對照 Codex CLI 目前的版本。

同一組 OAuth 也用於「用量上限重置」：可用次數來自 `/wham/usage`，**到期時間一律以**
`/wham/rate-limit-reset-credits` 明細端點為準——用量回應的 `credits` 不完整，拿它當主要來源
會顯示成比實際更晚的到期時間。兌換會消耗帳號有限額度且不可還原，只在使用者於 Provider
設定按下「重置」並完成二次確認時送出。

OAuth Token、管理頁保存的 Provider 集合、MCP Server 集合、服務設定與 NetPass Key 都位於
`data_dir` 下的權限限制檔案，不應複製回範例設定或提交版本庫。管理 API 只回傳是否已設定，
不提供秘密明文。

`AI_AGENT_LLM_*` 與 `OPENAI_*` 只覆寫 `AI_AGENT_DEFAULT_PROVIDER_ID` 指定的 OpenAI-compatible Provider；其他具名 Provider 使用 JSON 設定。

Context 摘要可在 JSON 的 `context.summary_provider_id`／`summary_model` 指定較便宜的路由；
Provider ID 必須已存在於 `providers`。留空時摘要沿用 Session 的 Provider 與 Model。

`context.max_history_characters` 預設 60,000（非正值使用預設），用來補足 JSON、代碼等內容的
token 估算誤差。這個預設值是為**本機模型的 prefill 時間**訂的；視窗大的雲端 Provider 沿用它
會在很低的使用率就被壓縮（例如 262K 視窗約在 25% 就觸發），可在該 Provider 的
`max_history_characters` 調高。填 0 表示採用該類型的預設：**Codex Responses 為 200,000**
（它一定走雲端、視窗以 20 萬 token 起跳，全域那個值從來就不適用），其餘沿用全域。
預設值在設定載入時也會套用，既有設定不必重存即可生效。壓縮事件會回報是哪一道閘門觸發
（`trigger` 為 `context_budget` 或 `history_characters`）與當時的字元數，
介面據此說明原因——只轉圈圈不說原因時，使用者會以為系統在沒有數據的情況下亂壓。整形後歷史超過此值也會觸發整理；送出前仍超量時再裁去較舊訊息，只改變模型
所見的歷史，不刪除 transcript。至少保留最新一則，因此不是整份請求的硬性字元上限；system
prompt、工具 schema 與當前輸入仍需另計。`max_tokens` 與 wall-clock 的 Run 預算不受此設定改變。

後端啟動時會在背景探索 Provider 模型限制，使用共用 20 秒期限；探索失敗不阻擋服務啟動。
未取得 context window 時，Harness 日誌會標示採用後備預算。Console 以星號標示後備估算的
百分比；Provider 設定頁另列有效能力供比對，人工欄位的 0 不代表模型沒有上下文容量。

Console 新建 Provider 時，這三個欄位預設為 **Context window 256K、最大輸出 Tokens 8K、
歷史字元上限 512K**，而不是留 0（「自動」）。留 0 看起來安全，實際上把問題往後推：多數相容
服務不回報限制，自動就落到後備預估值，而那個估算保守到會提早壓縮——使用者只看到對話莫名
其妙被壓縮，卻不知道要去哪裡調。給一組看得見、也改得動的值。三個欄位都是下拉選單
（容量 128K–2M、輸出 4K–512K），預設值刻意對齊選單刻度。既有 Provider 不受影響，
是「自動」的仍然是「自動」；只有主動更改既有 Provider 的**類型**時會連同整組設定重建。

模型成本表 `model_prices` 也是非秘密設定，依 Provider ID 與模型名稱指定每百萬 token 的 input／output
單價；未設定價格時只顯示 token，不會假造成本。收尾後的 Run 成本是當時價格的快照，日後修改價格
表不會改寫歷史 Run。

但 Session 累計依目前保留的 Run 彙總；舊 Run 被儲存維護淘汰後，統計與匯出不再包含那筆用量。
需要長期成本追蹤時應定期另存匯出，不能把畫面數字當成終身用量或 Provider 帳單。

「清除目前已儲存的…」這類按鈕有三種狀態，而且要分得開：沒有已儲存的憑證時**停用並轉灰**
（不上警示色——沒有東西可清的按鈕不該看起來像個危險動作），可按而未按時是紅色外框，按下後
變成填滿的紅底並把文字換成「儲存後會清除（再按一次取消）」。這幾個按鈕只是把意圖記下來，
真正的清除發生在按下儲存之後，所以文字要說得出「還沒清除、什麼時候會清除、怎麼反悔」。
先前按下後的樣式與 `:hover` 共用同一條 CSS，滑鼠還停在按鈕上時畫面毫無變化，使用者只能猜
自己有沒有按到——而這個動作會刪掉一把可能拿不回來的金鑰。

Provider 管理頁也有拖放匯入，與 MCP 同一套：一次可拖多個檔案、依副檔名分派、檔案裡的
金鑰會一併匯入。目的是讓不熟電腦的使用者只要把檔案拖進來、按確認就能用完；位址、模型與
容量這些他無從判斷的欄位由發檔案的人決定。**帶著憑證的設定檔本身就是一份活的憑證**，
發檔案的人要當成密碼保管，用短期、可撤銷的憑證。細節見 `MCP_CONFIG.md`。

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

`data_dir` 預設是相對路徑（`data/ai-agent`），相對於**工作目錄**解析。從 Finder、Dock 或開始
選單啟動時工作目錄是根目錄，那在 macOS 上是唯讀的——後端建不出資料夾就直接退出，畫面只會
顯示「後端未啟動」。因此沒有指定 `-working-dir` 時，桌面程式會先實際測試目前工作目錄能不能
寫入，不行才改用使用者資料夾（macOS 為 `~/Library/Application Support/NR-Intern`）。
用寫入測試而不是比對路徑，才涵蓋得到唯讀磁碟與無權限目錄。從專案目錄直接執行時工作目錄
可寫，行為不變，仍使用該目錄下的 `data/ai-agent`。

桌面 UI 預設使用 `http://127.0.0.1:8790`，後端使用 `http://127.0.0.1:8787`。如果後端已存在，桌面只連接；否則桌面會啟動自己的 backend child。

連接其他已啟動後端：

```bash
go run -buildvcs=false ./src/cmd/desktop \
  -auto-start=false \
  -backend-url http://127.0.0.1:9000 \
  -backend-token YOUR_TOKEN
```

Windows 版也有原生視窗：與 macOS 共用 `webview_go`，在 Windows 上走 WebView2，
因此 Console 注入的三個回呼名稱兩邊一致，前端不必分平台判斷。這需要 cgo，而從 macOS
交叉編譯需要 MinGW——`x86_64`／`aarch64-w64-mingw32-gcc`，PATH 找不到時退回可攜式
LLVM-MinGW（預設 `~/.local/share/yourdesk/toolchains/llvm-mingw`，可用 `NR_INTERN_LLVM_MINGW`
指定）。找不到該架構的編譯器就退回無視窗版本：那個版本仍然可用，只是啟動時開的是預設瀏覽器。
目標機器需要 WebView2 Runtime；Win11 內建，Win10 可能要另外安裝。

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

每則回答右下角有時間戳：當天只顯示 `HH:MM:SS`，跨日改顯示 `YY/MM/DD HH:mm`——隔天之後回頭
看，精確到秒沒有意義。完整時間放在 tooltip。思考區塊不重複顯示時間，它底下的回答已經有了。

時間左邊有一個小的複製鈕，複製那一則回答。它複製的是 markdown 原始碼，但會**拿掉包住整則
訊息的那層程式碼圍籬**：Agent 交出來的接手 prompt 幾乎都整段包在 ```text 裡，那層圍籬是給人看
邊界用的，照原樣複製，貼到別處前還得手動刪掉頭尾兩行。只在第一行與最後一行都是圍籬、且開頭
圍籬的語言標註屬於文字類（`text`／`txt`／`markdown`／`md`／`plain`／`prompt` 或沒有標註）時才動手，
而且只拿掉那兩行——中間的標題、清單與巢狀圍籬原樣保留。以程式碼區塊開頭又以程式碼區塊結尾
的回答因此不會被誤傷。

開發時要注意上面提到的單一實例行為會蓋掉重新建置：port 已被佔用時新程序只會叫回舊視窗然後
退出，日誌留下一行 `existing desktop UI restored`，畫面跳出來、跑的卻仍是舊版。`run.command`
會在啟動前先停掉執行中的實例；手動 `go run` 時請自行確認沒有舊程序還在。

## 匯出設定

管理介面「系統工具 → 下載設定包」透過 `GET /api/v1/admin/config-bundle` 下載
`nr-intern-config.zip`。它只收錄 `data_dir` 已存在的 `providers.json`、`mcp-servers.json`、
`netpass.json`、`service-settings.json` 與 `manifest.json`；不收錄對話或附件，不讀取 OAuth
token 檔，也不是完整有效設定的快照：只在原始設定檔或環境變數中提供的值不會自動納入。

設定包預設保留結構並遮蔽可辨識的秘密欄位，供換機時參考與重新設定；目前沒有設定包專用匯入
端點，不能交給「還原備份」直接還原。

**要不要帶明文憑證由使用者自己勾。** 匯出前可勾選「設定包包含金鑰與密碼的明文」，勾了之後
`manifest.json` 的 `contains_secrets` 會是 `true`，檔名也會變成 `nr-intern-config-with-secrets.zip`
——兩個檔案混在下載資料夾裡時，看檔名就分得出哪一個要當密碼保管。預設仍然是遮蔽：帶明文
必須是按下按鈕的人主動要求的，不是漏勾的後果。

開放這個選項的理由與匯入端一致：遮蔽之後收到設定包的人得自己補金鑰，而實際需要這個設定包
的人，往往正是不知道金鑰是什麼的那一位。代價要講清楚——**帶憑證的設定包等同一組密碼**，
以安全管道傳遞、用完刪除，不要放進版本庫或群組信件。

Provider 與 MCP 的編輯畫面另有「匯出 Provider」「匯出 MCP Server」，在刪除鈕旁邊，**單獨匯出
一項**。按下後同樣先問要不要帶金鑰，預設不帶。輸出格式就是拖放匯入吃的格式——匯出的檔案
必須匯得回去，兩邊是同一份契約——所以把一個設好的 Provider 交給別人，對方拖進去按確認就
可以用。副檔名為 `.provider` 與 `.mcp`，帶憑證時檔名附加 `-with-secrets`。

下載在 App 與瀏覽器走不同的路。**App 會跳出系統存檔面板**（`POST /desktop/api/files/save`，
macOS 以 `osascript` 的 `choose file name` 實作，與資料夾選擇同一套，不需要 cgo），由使用者選定
位置後由桌面層寫檔，權限 `0600`——匯出檔可能含明文憑證，比照設定檔而不是一般下載檔。
**瀏覽器走原本的 `<a download>`**，那條路本來就正常。

判斷依據是 `window.nrInternSetConversationActive` 這個原生層無條件綁定的函式；瀏覽器連到同一個
8790 不會有它。用它而不是伺服器端判斷，是因為遠端瀏覽器連進來時，伺服器端判斷會讓存檔面板
彈在**主機**的螢幕上。使用者在面板按取消時前端保持安靜，不會再默默下載一份到下載資料夾。

Windows 的原生對話框（存檔面板、Sandbox 目錄選擇器）透過 PowerShell 叫用系統元件，與 macOS
走 `osascript` 是同一個形狀——用系統自己的腳本宿主，不必為了兩個對話框引入 COM 綁定。桌面版是
GUI subsystem 程式、本身沒有 console，因此這些子程序與 `shell_exec` 都要帶 `CREATE_NO_WINDOW`
與 `HideWindow`，否則每次執行都會閃過一個黑框。

**拖放目錄取得路徑只有 macOS 做得到。** macOS 的拖放剪貼簿（`NSPasteboardNameDrag`）在放開之後
仍然存在，原生層可以事後讀回路徑；Windows 的 OLE 拖放資料只在拖放進行中存在，而且直接交給
WebView 自己的放置目標。要攔截就得對 WebView 子視窗 `RevokeDragDrop` 再註冊自己的 `IDropTarget`，
那會連帶接管整個視窗的拖放、破壞聊天輸入區的附件拖放。因此取不到路徑時直接開啟目錄選擇器並
說明原因——使用者要的只是把目錄加進來，不該停在一句錯誤上。

URL、帳號、路徑、command／args 等即使在遮蔽模式下仍可能含敏感內容，分享前務必人工檢查，
完整限制見 [安全設計](SECURITY.md)。

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

Windows 的安裝檔是 `setup.exe`（先前為 `.msi`）。改用 NSIS 的理由是跨平台建置：MSI 需要只跑在
Windows 的 WiX，或 msitools 的 wixl——後者對 ARM64 的支援要靠事後改寫 Summary Template 才勉強
成立。安裝為**每位使用者**，裝在 `%LOCALAPPDATA%\Programs\NR-Intern`，不需要系統管理員、
不跳 UAC，並在「應用程式與功能」中提供移除項目。

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
