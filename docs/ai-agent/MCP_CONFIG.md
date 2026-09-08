# MCP `.mcp` 設定檔

NR-Intern 的「使用 MCP」頁面支援把 JSON 格式的 `.mcp` 檔案拖入匯入區。檔案只會先在瀏覽器記憶體中解析，使用者在對話框確認並補齊必要欄位後，才會送到後端安裝。

## 規範說明

MCP 協定本身定義 Client 與 Server 的通訊方式，沒有規定一個所有 Client 都必須使用的 `.mcp` 設定檔副檔名或完整設定檔格式。不同產品常用自己的檔名與欄位，例如 `mcpServers`、`servers`、`command`、`args`、`env`、`url` 與 `headers`。

NR-Intern 採用最常見的 `mcpServers` 物件格式，並兼容單一 Server、`servers` 陣列及 `servers` 物件格式：

```json
{
  "mcpServers": {
    "local-example": {
      "command": "npx",
      "args": ["-y", "@example/mcp-server"],
      "env": {
        "MCP_API_TOKEN": ""
      }
    },
    "remote-example": {
      "type": "http",
      "url": "https://example.com/mcp",
      "headers": {
        "X-API-Key": "${MCP_API_KEY}"
      }
    },
    "remote-basic-example": {
      "type": "http",
      "url": "https://example.com/private-mcp",
      "username": "${MCP_USERNAME}",
      "password": "${MCP_PASSWORD}"
    }
  }
}
```

支援的主要欄位如下：

- 本機 Server：`command`、`args`、`env`／`environment`、`cwd`／`work_dir`。
- 遠端 Server：`url`／`server_url`／`endpoint`、`headers`、`username`／`password`、`type: "http"`、`type: "sse"` 或 `transport: "streamable-http"`／`"sse"`。
- 共用欄位：`id`、`name`／`display_name`、`enabled`、`startup_timeout_seconds`、`call_timeout_seconds`、`trust_annotations`。

有 `command` 時會判定為 `stdio`；有 URL 且標示 `sse` 時會使用舊版 SSE，其他有 URL 的設定會使用 Streamable HTTP。未標示類型的 `http` 會視為 Streamable HTTP。

匯入對話框的「驗證方式」選項只會依 `.mcp` 中明確宣告的 `auth_method`／`auth_methods`，或實際存在的金鑰、帳號密碼、HTTP Headers 欄位建立；沒有出現在檔案中的驗證方式不會列入選單。若只有一種方式，仍會顯示目前方式供確認。

## 連線維持與憑證顯示

- 連線會定期 ping（keepalive），閒置後再次使用前也會先確認連線仍活著，因此 MCP Server 重啟、
  session 過期或反向代理回收連線後，下一次工具呼叫會自動重新連線，不需要回到管理頁按「測試連線」。
- 只有伺服器明確拒收的呼叫（`session not found`、session 過期、連線未建立）才會自動重送；
  其餘連線錯誤仍只對 Server 宣告 idempotent 的工具重試，避免同一個副作用執行兩次。
- `call_timeout_seconds` 是「沒有回應或進度更新」的容忍秒數，不是總時長上限。MCP Server 只要
  持續回報進度，長時間工作就能跑完；完全沒有回應時，錯誤訊息會直接說明停滯了多久。
  預設 1800 秒對「查一筆資料」這類工具太長，Server 沒回應時要等 30 分鐘才會失敗；
  查詢型 Server 建議把這個值調成 60～180 秒。
- 延長有絕對上限（閒置視窗的 4 倍，最多 24 小時），Server 一直送進度卻不回結果時仍會中止。
- 等待期間每 15 秒會在執行過程回報一次「等待 MCP ⋯⋯（已 N 秒）」；等待人工核准時每 30 秒
  回報一次「等待人工核准 ⋯⋯（已 N 秒）」。卡住時先看這兩行，就能分辨是 MCP 沒回應，
  還是核准對話框沒被按。
- 管理頁的連線狀態會顯示「目前送出的驗證方式」（不使用驗證／Bearer Token／Basic Auth／自訂
  HTTP Headers）。憑證明文不會從後端讀回，這一行用來確認已儲存的金鑰確實被採用；憑證遭拒時，
  錯誤訊息也會指出送出的是哪一種驗證。

## 唯讀工具與核准

- 唯讀工具不需要逐次人工核准。MCP 工具是否算唯讀，取決於 Server 宣告的 `readOnlyHint`
  以及該 Server 是否開啟「信任 Server 宣告的唯讀工具屬性」（`trust_annotations`）。
- `trust_annotations` 預設開啟：唯讀查詢不必逐次核准。關閉後該 Server 的所有工具（含唯讀）
  都會逐次詢問。設定檔沒有這個欄位時採預設值，明確寫 `false` 則沿用你的決定。
- 跳過核准的呼叫會留下 `run.approval_skipped` 紀錄，可在稽核事件中查到。
- 有副作用的 MCP 工具不受影響，仍然需要 elevated 權限與單次核准。

## 只公開需要的工具

MCP 工具的定義（名稱、說明、JSON schema）**每一次請求都會整份送給模型**。外掛型 Server 動輒
數十上百個工具，光是 schema 就可能佔掉數萬 token——實測有 Server 讓一句「HELLO」的請求變成
十一萬 token，模型光是處理提示就要好幾分鐘。

### 檢索（預設行為，不需要設定）

多數情況下這件事由 Harness 自動處理：可選工具超過 12 個時，每個 Run 會先依這次的需求
檢索目錄，只有相關的工具進入提示（內建工具與 MCP 工具都適用）。**沒有任何工具被停用**——
模型看不到的工具仍然可以呼叫，也可以用 `find_tools` 以關鍵字（中英文皆可）把工具連同參數
schema 取回，取回後直接呼叫；呼叫過的工具會留在該 Run 的目錄裡。查詢會帶上同一個 session
最近幾則使用者訊息，跟進提問只剩代名詞時（「再查一次」）仍然檢索得到。
實測 67 個工具的目錄從 30,210 字降到 1,399 字。

檢索用工具名稱、標題與說明建立索引，中文以 bigram 比對，出現在大半個目錄裡的共通詞
（例如每個工具都有的 `query`）權重歸零，避免任何問題都命中整份目錄。這個行為可在
管理介面「工具與協定」關閉（設定欄位 `tool_retrieval`），關閉後整份目錄會回到每一次請求。

### 手動只公開部分工具

需要更嚴格的界線時（例如某些工具根本不該讓 Agent 碰），「使用 MCP」的功能表清單可以逐一勾選：
只勾要用的工具，其餘不會進入工具目錄，也不能被呼叫，但仍留在清單裡隨時可以打開。計數顯示為
「已公開／可用」（例如 `3/86`）。全部勾選代表不限制，Server 之後新增的工具會自動納入。
設定檔對應的欄位是 `enabled_tools`（remote name 陣列，空陣列＝全部）。檢索作用在這個結果之上。

## 拖放匯入與多檔分派

桌面 Console 的 MCP 與 Provider 兩個管理頁各有一個拖放區，但**兩邊接受的檔案相同**：
一次可以拖進多個檔案，依副檔名分派——`.mcp` 進 MCP、`.provider` 進 Provider、`.json`
沒有副檔名線索就看內容（有 `mcpServers`／`servers` 視為 MCP，有 `providers`／`base_url`
視為 Provider）。使用者不需要知道哪個檔案該拖到哪一區。

同一類的多個檔案會合併成一份草稿、一個對話框看完。兩類都有時排隊：前一個確認或取消
之後才開下一個，不會同時彈兩個。認不出來、不是有效 JSON、或超過 2 MB 的檔案會逐一列出
原因，其餘照常匯入。

排隊由安裝完成與取消／關閉按鈕推進，**不依賴 `<dialog>` 的 `close` 事件**：桌面端使用的
WebKit 實測不會送出那個事件（連全新建立的 dialog 都不會），靠它排隊的話第二個對話框永遠
不會出現。

桌面端的原生拖放走系統通道（`POST /desktop/api/mcp/files/dropped`），該端點的副檔名白名單
同樣是 `.mcp`、`.provider` 與 `.json`——只認 `.mcp` 的話，在 App 裡拖 `.provider` 進去會完全
沒有反應。瀏覽器的拖放直接讀取 `DataTransfer`，不經過這個端點。

## 憑證與安裝行為

- **檔案裡的憑證會一併匯入**（Bearer Token、Basic Auth 的帳號密碼）。原本一律清空是為了「設定檔不該帶密碼」，但實際要用匯入的人往往連金鑰是什麼都不知道，清空之後他就卡在那一格，功能等於不存在。代價要講清楚：**帶著憑證的設定檔本身就是一份活的憑證**，發檔案的人必須當成密碼保管，用短期、可撤銷的憑證，不要放進版本庫或群組信件。匯入對話框在偵測到憑證時會明白顯示這件事。
- `${VARIABLE}` 佔位符仍然不會匯入：那不是憑證，是一個沒有值的引用。名稱保留、值留空，對話框會提示哪幾格要自己補。敏感的環境變數同樣只保留名稱。
- 遠端 Server 的 URL 是必要欄位；匯入對話框可從不使用驗證、Bearer Token、Basic Auth 與自訂 HTTP Headers 中選擇要採用的方式。
- Basic Auth 會由後端以 HTTP `Authorization: Basic ...` 送出，密碼不會回傳給 UI。
- 若 MCP ID 已存在，預設不覆蓋；勾選「覆蓋相同 MCP ID 的既有設定」後才會替換。
- 確認安裝後沿用既有 MCP 管理 API 的格式、後端驗證、權限與背景連線流程。
- 範例檔只有示例網域與空白憑證，不應填入真實金鑰後提交到版本庫。
