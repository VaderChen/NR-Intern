# 更新紀錄

## 1.26.1110 build 1637

本版整合 Claude Platform API、Provider 管理、Harness 可靠性與效能優化。

### Provider 與模型

- 新增 Claude 原生 Messages API：模型分頁列表、串流／非串流、工具呼叫與結果、思考簽章續接、effort 與用量統計。
- Claude 固定指示與工具定義加入 prompt caching；快取讀取／寫入 token 個別記錄，成本估算仍使用單一輸入單價。
- 前綴內容變動時省略失效的思考區塊；工具配對維持完整，不因簽章錯誤重做工具。
- 修正 Claude 連線測試的工具選擇方式、串流終態及輸出截斷判讀；截斷參數不交付執行。
- 修正 Provider 計價殘留參照；刪除 Provider 後，閒置 Session 遷移至系統預設 Provider／模型。執行中工作與 Workspace 引用仍受保護。
- Workspace 與建立 Session 的模型列表隨 Provider 更新，保留手動輸入／預設繼承，避免舊請求覆蓋新選擇。
- Codex 剩餘用量加入百分比、進度條與資料更新時間；Claude 速率快照僅供內部使用，不當成帳戶餘額。

### Harness、效能與架構

- 工具指令整批驗證，取消或提早離開時保存尚未派送的結果；展示 hook 不得改寫真實失敗或未知狀態。
- 改善回合及 Run 終態收尾、panic 邊界與儲存失敗處理；輸出截斷最多一次要求縮小工作，仍受原有預算限制。
- Context 裁切保留完整工具群組，摘要輸入不回送原 Provider 的不透明續接狀態。
- 改善串流判讀、工具檢索、記憶搜尋、字型讀取、試算表定位與前端渲染；Session／事件索引採 128 份 LRU，日誌使用可重用緩衝。
- Application、Harness 與 MCP Client 依職責拆分；加入套件相依白名單檢查及 GitHub Go 檢查工作流程。

### 升級與驗證範圍

- 升級前備份所需資料；RAM 工作區不跨重啟保留，已執行的舊 APP 不會自動載入新程式。
- Claude 使用 Platform API Key 與 Messages；「使用 Response」是另一種協定。圖片／PDF 輸入、手動 thinking budget 及伺服器端工具尚未支援。
- 最大輸出須符合模型能力，未設定時仍為 4096。歷次授權 API 實測及合成效能資料記錄於開發說明，不代表整體正確率、併發容量或長時間穩定性驗收。
- 本次發布重新執行 Go 編譯、對應 Smoke 與 JavaScript 語法檢查；未另行呼叫付費模型。具體發布驗證結果見本版 GitHub Release。
- macOS ARM64 使用簽章及公證 DMG；Windows x64／ARM64 使用 NSIS `setup.exe`，目前未具備 Authenticode 簽章，可能出現 SmartScreen 警告。Windows 實機安裝、升級及移除仍未驗收。
- Windows ARM64 未內附 NetPassClient Runtime，該平台的反向代理功能不可用。
- MCP 重啟案例依安全政策驗證：無法確認派送狀態時停止自動重送，重新取得目錄後的明確呼叫只執行一次。
- Release 附件以 `RELEASE-SHA256SUMS` 核對完整性；`PACKAGES-SHA256SUMS` 僅列通過簽章驗證的產物。

## 1.26.0912 build 1635

本版聚焦任務約束、工具執行、長任務恢復與交付結果。

### 任務執行與安全

- RAM 工作區不再全面豁免核准；只有單一隔離根目錄內具後端限制的工具適用例外。
- Context 不裁切固定指示或將歷史資料提升為系統指令，超額時明確要求縮小工作範圍。
- 內容不同的正常修改不因成功次數而被停止；重複操作收斂後仍可唯讀查證。
- 工具派送與結果分別保存。未知結果禁止自動重送，人工重試必須逐次核准，永久授權不適用。
- 完成度按工具契約與目標操作追蹤，不能以另一個目標成功掩蓋失敗。
- 計畫完成須引用驗證階段成功工具結果 ID；含略過步驟的計畫改為 `partial`。

### 長任務與 MCP

- 重啟後無執行者的 LOOP 轉為可續跑的暫停狀態，保留步驟、證據、已開始次數與交接內容。
- 事件未完成尾筆先備份再修復；中段損毀按 Run 隔離，不讓單一事件檔阻止服務啟動。
- 主模型、Context 摘要與備援共用預算，計入串流及失敗前的實際用量；成本逐模型計算。
- 串流輸出以固定字元區塊估算，避免封包切割造成過早耗盡額度。
- MCP 追加輸入以受限續接 ID 串接，opaque state 不進模型或 transcript。
- 每輪目錄、核准與派送皆核對模型看過的工具契約；重連不能沿用過期契約或重新啟用已替換來源。
- 修正 MCP 重連鎖重入，以及 RAM 計畫暫存檔誤寫到一般磁碟的問題。

### 檔案與交付

- 互動 HTML／網頁原始碼使用 `file_write`，不經文字文件模板跳脫。
- 修正本機產物連結處理、原子寫入與編輯的前置條件，以及隔離儲存的根目錄路由。
- 上游暫時性錯誤沿用有上限、可取消的重試；已有文字、思考或工具輸出時避免自動重送。
- 同步公開 API 的 `one_time_only`、`partial`、驗證證據、工具契約及分模型用量欄位。

### 升級與驗證範圍

- 升級前請備份需要保留的資料；RAM 工作區內容不會跨程式重啟保留。
- 舊 `verifying` 步驟若缺少驗證起點，須先標為 `blocked`，再重新開始驗證；不回填歷史證據。
- 已完成指定 Go 編譯及 JavaScript 語法檢查，未新增執行功能測試。
- 過載、斷線、重連、重啟恢復與 Windows 目標系統尚未實測；不能宣稱已實測排除問題。
- 工具證據只核對來源與時序，不等於任意功能驗收；未知副作用也不保證恰好執行一次。
- 本版提供已簽章及公證的 macOS ARM64 DMG，以及 Windows x64／ARM64 NSIS `setup.exe` 安裝程式；Windows 安裝檔尚未具備 Authenticode 簽章，可能顯示發行者或 SmartScreen 警告。
- Windows Installer 採每位使用者安裝，提供捷徑與解除安裝；ARM64 版本未內附 NetPassClient Runtime。尚未驗證 Windows 實機安裝、升級及移除流程。
- 正式下載以 Release 附件及 `RELEASE-SHA256SUMS` 為準；雜湊用於檔案完整性檢查，不代表簽章或功能驗收。封裝腳本原有的 `PACKAGES-SHA256SUMS` 僅列簽章驗證通過的產物，不代表本版全部附件。

架構與限制詳見 [ARCHITECTURE.md](ARCHITECTURE.md)、[SECURITY.md](SECURITY.md) 與
[MCP_CONFIG.md](MCP_CONFIG.md)。授權以版本庫現有 [授權原文](../../LICENSE.md) 為準。
