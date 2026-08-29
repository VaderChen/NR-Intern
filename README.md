# 永不休息的實習生

[繁體中文](README.md) · [English](README.en.md) · [日本語](README.ja.md) · [한국어](README.ko.md)

NR-Intern 是以 Go 建立的桌面 AI Agent。它會依目前對話、實際工具結果與持久化狀態持續工作；簡單任務直接處理，長任務則可建立計畫、逐步執行並驗證結果。

## 主要能力

- 可擴充的 OpenAI-compatible Provider Router，支援遠端或本機模型服務。
- `Workspace → Project → Session` 管理結構，以及每個對話獨立的 Provider 與模型選擇。
- 多份有序工作計畫、拖曳排序、步驟狀態與工具驗證證據。
- Durable Run、可重播 SSE、串流回答與斷線續接。
- Context 自動整理、跨 Session 長期記憶與記憶範圍控制。
- 原生檔案、文件、Shell、SSH 與計畫工具，搭配 Sandbox 及執行審核。
- Provider 啟用控制、模型探索、工具權限與稽核記錄。
- 淺色／深色外觀，以及 AUTO、繁體中文、英文、日文、韓文介面。
- Windows x64、Windows ARM64 與 macOS ARM64 桌面環境。

介面語言只控制 UI 與尚未自訂的預設名稱；Agent 會依目前訊息、近期對話與明確偏好判斷使用者的慣用語言。

## 開始使用

1. 以 `configs/ai-agent/config.example.json` 建立自己的本機設定檔。
2. 在本機設定 Provider endpoint、模型與必要憑證，或從管理介面完成設定。
3. 建立 Workspace、Project 與 Session，再從對話區交付任務。

實際設定檔、執行資料與憑證不應提交到版本庫。完整架構、API 與安全設計請參考下方文件。

## 文件

- [架構設計](docs/ai-agent/ARCHITECTURE.md)
- [開發說明](docs/ai-agent/DEVELOPMENT.md)
- [HTTP API](docs/ai-agent/HTTP_API.md)
- [安全設計](docs/ai-agent/SECURITY.md)
- [OpenAPI 契約](src/transport/httpapi/openapi.yaml)

發行封裝與簽章由維護者的內部流程處理；README 不提供封裝命令、參數或簽章設定。

## 資料安全

- Repository 只包含範例設定；Provider 金鑰、SSH 憑證、日誌、執行資料及發行產物均由 `.gitignore` 排除。
- 文件與範例僅使用相對路徑、localhost 或示例網域，不包含開發者電腦的絕對目錄。
- 請勿提交 API Key、Token、私鑰、憑證、簽章身分或真實服務位址。

## 授權

本專案採雙重授權：

- 開放原始碼使用適用 [GNU General Public License v3.0 only](LICENSE)。
- 不適合 GPLv3 義務的商業使用，可另行洽談商業授權，詳見 [COMMERCIAL-LICENSE.md](COMMERCIAL-LICENSE.md)。

第三方元件仍依各自附帶的授權條款提供。
