# 永不休息的實習生

[繁體中文](README.md) · [English](README.en.md) · [日本語](README.ja.md) · [한국어](README.ko.md)

NR-Intern 是以 Go 建立的桌面 AI Agent。它會依目前對話、實際工具結果與持久化狀態持續工作；簡單任務直接處理，長任務則可建立計畫、逐步執行並驗證結果。

## 主要能力

- 可擴充的 Provider Router，支援 OpenAI-compatible Chat Completions，以及使用 ChatGPT／Codex OAuth 的 OpenAI Codex Responses。
- `Workspace → Project → Session` 管理結構，以及每個對話獨立的 Provider 與模型選擇。
- Workspace 與 Project 的職務說明：常駐工作規則只寫一次，之後每次對話與排程都自動帶入。
- 多份有序工作計畫、拖曳排序、步驟狀態與工具驗證證據。
- 可選的 Thinking 思考程度；Session 可鎖定計畫依序執行，也可讓未完成計畫平行運作。
- 獨立的排程區塊：可自訂週期與 Sandbox，到點自動建立新對話並開工。
- Durable Run、可重播 SSE、串流回答與斷線續接；UI 重開後會自動恢復仍在執行或可重試的 Run。
- 異常恢復、可選的持久化通知中心、Run 暫停／恢復／取消全部控制，以及脫敏診斷包。
- 全域搜尋、安全備份／還原與唯讀權限中心；還原前會自動保留可復原快照。
- 官方 GitHub Release 版本更新檢查；啟用通知中心後新版本會進入通知中心，同一版本不重複通知。
- 對話執行中仍可繼續輸入，後續訊息會寫入 Browser IndexedDB 的 Durable Outbox，上一輪結束後依序送出；網路中斷時保留固定 Idempotency-Key 供安全重試。
- Context 自動整理、跨 Session 長期記憶與記憶範圍控制。
- 每個 Run 與 Session 的 input／output／total token 統計，並可透過設定的模型單價顯示估算成本；未設定價格時只顯示 token。
- 原生檔案、文件、Shell、SSH 與計畫工具，搭配 Sandbox 及執行審核。
- 精簡／擴充工具集、工具檢索，以及 native／instruction 兩種工具呼叫模式，依模型能力調整。
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

## 開始使用

1. 以 `configs/ai-agent/config.example.json` 建立自己的本機設定檔。
2. 在本機設定 Provider endpoint、模型與必要憑證，或從管理介面完成設定。
3. 建立 Workspace、Project 與 Session，再從對話區交付任務。

實際設定檔、執行資料與憑證不應提交到版本庫。完整架構、API 與安全設計請參考下方文件。

對話、工作狀態與設定均由後端持久化保存；UI 重開或後端重啟後，可恢復仍在執行或可重試的工作。
通知中心可在一般設定關閉，並只保存工作狀態摘要。

## 文件

[開啟線上文件網站](https://vaderchen.github.io/NR-Intern/)

- [架構設計](https://vaderchen.github.io/NR-Intern/architecture.html)
- [辦公文件工具](https://vaderchen.github.io/NR-Intern/document-tools.html)
- [開發說明](https://vaderchen.github.io/NR-Intern/development.html)
- [HTTP API](https://vaderchen.github.io/NR-Intern/http-api.html)
- [安全設計](https://vaderchen.github.io/NR-Intern/security.html)
- [OpenAPI 契約](https://vaderchen.github.io/NR-Intern/openapi.html)

發行封裝與簽章由維護者的內部流程處理；README 不提供封裝命令、參數或簽章設定。

## 資料安全

- Repository 只包含範例設定；Provider／OAuth／MCP／NetPass 金鑰、SSH 憑證、日誌、執行資料及發行產物均由 `.gitignore` 排除。
- 文件與範例僅使用相對路徑、localhost 或示例網域，不包含開發者電腦的絕對目錄。
- 請勿提交 API Key、Token、私鑰、憑證、簽章身分或真實服務位址。

## 授權

本專案採雙重授權：

- 開放原始碼使用適用 [GNU General Public License v3.0 only](LICENSE)。
- 不適合 GPLv3 義務的商業使用，可另行洽談商業授權，詳見 [COMMERCIAL-LICENSE.md](COMMERCIAL-LICENSE.md)。

第三方元件仍依各自附帶的授權條款提供。
