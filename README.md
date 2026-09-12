# 永不休息的實習生

[繁體中文](README.md) · [English](README.en.md) · [日本語](README.ja.md) · [한국어](README.ko.md)

NR-Intern 是以 Go 建立的桌面 AI Agent。它會依目前對話、實際工具結果與持久化狀態持續工作；簡單任務直接處理，長任務則可建立計畫、逐步執行並驗證結果。

![NR-Intern 對話介面](images/cap0001.jpg)

## 主要能力

- 可擴充的 Provider Router，支援 OpenAI-compatible Chat Completions，以及使用 ChatGPT／Codex OAuth 的 OpenAI Codex Responses。Codex 帳號若有用量上限重置額度，可在 Provider 設定直接兌換，確認前會揭露可用次數與最早到期時間。
- `Workspace → Project → Session` 管理結構，以及每個對話獨立的 Provider 與模型選擇。
- Workspace 與 Project 的職務說明：常駐工作規則只寫一次，之後每次對話與排程都自動帶入。
- 多份有序工作計畫、拖曳排序、步驟狀態與工具驗證證據。
- 可選的 Thinking 思考程度；Session 可鎖定計畫依序執行，或解除鎖定以切換未完成計畫；不同 Session 可同時工作。
- 獨立的排程區塊：可自訂週期與 Sandbox，到點自動建立新對話並開工。
- Durable Run、可重播 SSE、串流回答與斷線續接；UI 重開後可勾選要重新連線的對話，中斷的工作保留重試入口。
- 異常恢復、可選的持久化通知中心、Run 暫停／恢復／取消全部控制，以及脫敏診斷包。
- 全域搜尋、安全備份／還原與唯讀權限中心；還原前會自動保留可復原快照。
- 可下載遮蔽憑證欄位的設定包，供換機時參考 Provider、MCP、反向代理與服務設定。
- 「關於」頁提供版本資訊與更新檢查。
- 對話執行中仍可繼續輸入，後續訊息會寫入 Browser IndexedDB 的 Durable Outbox，上一輪結束後依序送出；網路中斷時保留固定 Idempotency-Key 供安全重試。
- Context 自動整理搭配歷史字元上限，並支援長期記憶與記憶範圍控制。
- 可選的實驗性回憶空間：重用偏好、決策與作法，提供近似去重、Project 優先的記憶範圍與精簡召回；工具失敗時可參考過往記憶。
- 每個 Run 的 input／output／total token 統計，主模型、Context 摘要與備援分模型記帳；Session 彙總獨立用量快照，Run 明細淘汰不扣除累計值。未設定價格時只顯示 token。
- 原生檔案、文件、Shell、SSH 與計畫工具，搭配 Sandbox 及執行審核。
- 記憶體隔離專案：對話、計畫、附件與工作檔案**執行期間就寫在 RAM Disk 上**，關閉或重啟即消失，只保留 Project 設定；每個 Project 使用獨立、可調容量的 RAM Disk Sandbox。只有單一隔離根目錄內、具後端工作區限制的工具可免逐次核准，Shell、SSH、MCP 與外部轉換／渲染不因此豁免；可在實驗性功能中關閉新建（預設開啟）。
- 精簡工具集即支援文件讀取、建立與轉換；可切換擴充工具集、工具檢索與 native／instruction 呼叫模式。
- 長對話採索引分頁，提供提問段落預覽與快速跳轉；累計用量以 K／M 顯示，滑過可看精確值。
- 非同步工作可使用 `wait_for` 進行可取消等待，遠端部署可用 `ssh_wait` 輪詢唯讀檢查，確認檔案大小、SHA-256 或服務就緒後才算完成。
- `http_fetch` 對外讀取網路資源：HTML 自動轉純文字，localhost 與私有網段預設允許，並可從管理介面直接關閉。
- 內建 MCP Client，可連接本機 stdio、舊版 SSE 或遠端 Streamable HTTP Server，將外部工具納入相同的權限與審核流程。
- 選用的 NetPass 反向代理可公開後端 API；桌面控制 UI 不會經由通道公開。
- Provider 啟用控制、模型探索、工具權限與稽核記錄。
- 淺色／深色外觀，以及 AUTO、繁體中文、英文、日文、韓文介面。
- Windows x64、Windows ARM64 與 macOS ARM64 桌面環境。
- macOS 啟動時即建立狀態列圖示；工作進行中可隱藏 UI 繼續背景執行，再由選單或重新啟動程式恢復原視窗。
- Windows 啟動時即建立 Tray Icon；左鍵可重新開啟 UI，右鍵可開啟 NR-Intern 或結束程式，即使 UI 使用瀏覽器 fallback 也維持相同生命週期。
- 側邊欄內建只保存在目前裝置的記事本，適合暫存不需要送給 Agent 的文字。
- 對話輸入區可啟動畫面擷取；macOS 會使用系統區域截圖並開啟方框、直線與文字標註編輯器，完成或關閉時把編輯結果更新到剪貼簿。Windows 則開啟系統剪取介面。

介面語言只控制 UI 與尚未自訂的預設名稱；Agent 會依目前訊息、近期對話與明確偏好判斷使用者的慣用語言。

## 任務執行與恢復

- Context 不裁掉固定任務約束；仍無法容納時明確停止並要求縮小範圍。
- 工具結果未知時不自動重送；人工重試須逐次確認，永久核准不適用，也不保證副作用只發生一次。
- 計畫完成須引用驗證階段的成功工具結果 ID；含略過步驟的計畫標為 `partial`。證據來源與時序檢查不等於功能實測。
- 程式重啟後，沒有執行者的 LOOP 轉為可續跑的暫停狀態，保留步驟與次數；不自動重做中斷操作。
- MCP 追加輸入透過受限續接 ID 處理，執行前再次核對工具契約。互動 HTML 使用原始碼寫入工具，與文字文件產生器分流。

本輪變更與升級限制見 [更新紀錄](docs/ai-agent/CHANGELOG.md)。

## 開始使用

桌面安裝檔可從 [GitHub Releases](https://github.com/VaderChen/NR-Intern/releases/latest) 下載：
macOS ARM64 使用已簽章及公證的 DMG；Windows x64／ARM64 使用對應架構的 `setup.exe`，
採每位使用者安裝並提供解除安裝。Windows 安裝檔目前未簽章，可能出現發行者或 SmartScreen 警告；
下載後可用 Release 附件 `RELEASE-SHA256SUMS` 核對完整性，雜湊不代表程式碼簽章。

1. 以 `configs/ai-agent/config.example.json` 建立自己的本機設定檔。
2. 在本機設定 Provider endpoint、模型與必要憑證，或從管理介面完成設定。
3. 建立 Workspace、Project 與 Session，再從對話區交付任務。

實際設定檔、執行資料與憑證不應提交到版本庫。完整架構、API 與安全設計請參考下方文件。

對話、工作狀態與設定由後端保存；通知中心預設關閉，可在一般設定開啟。
設定包不含對話與附件，也不是完整備份；URL、帳號與路徑等內容分享前仍需人工檢查。

升級前請先備份。較舊的 Run、事件檔與超過保留期限的失效記憶會自動整理，Session 對話紀錄
不因此刪除；長期用量與稽核資料請另存匯出，詳見[保留規則](docs/ai-agent/DEVELOPMENT.md#長對話讀取與儲存維護)。

## 文件

[開啟線上文件網站](https://vaderchen.github.io/NR-Intern/)

- [架構設計](https://vaderchen.github.io/NR-Intern/architecture.html)
- [辦公文件工具](https://vaderchen.github.io/NR-Intern/document-tools.html)
- [開發說明](https://vaderchen.github.io/NR-Intern/development.html)
- [HTTP API](https://vaderchen.github.io/NR-Intern/http-api.html)
- [安全設計](https://vaderchen.github.io/NR-Intern/security.html)
- [OpenAPI 契約](https://vaderchen.github.io/NR-Intern/openapi.html)

## 資料安全

- Repository 只包含範例設定；Provider／OAuth／MCP／NetPass 金鑰、SSH 憑證、日誌、執行資料及發行產物均由 `.gitignore` 排除。
- 文件與範例僅使用相對路徑、localhost 或示例網域，不包含開發者電腦的絕對目錄。
- 請勿提交 API Key、Token、私鑰、憑證、簽章身分或真實服務位址。

## 授權

本專案依 [NR-Intern 原始碼公開・禁止商業販售授權 v1.1](LICENSE.md) 提供。
使用與另行授權範圍以授權原文及 [商業授權說明](COMMERCIAL-LICENSE.md) 為準。

第三方元件仍依各自附帶的授權條款提供。
