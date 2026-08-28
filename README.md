# NR-Intern AI Agent

獨立的 Go AI Agent 專案，包含：

- 簡單任務直接執行；長任務可建立多份有序持久化計畫，逐份、逐步執行並以工具結果驗證。
- OpenAI／ChatGPT-compatible Chat Completions Provider。
- 可擴充的 Provider Router，以及 `Workspace → Project → Session` 管理結構。
- Session 釘選、Context compaction 與跨 Session 長期記憶。
- 使用者與 Agent 共用的多計畫佇列、拖曳順序、進度與驗證證據。
- 跨平台 Go 原生檔案、Shell 與 SSH 工具。
- Durable Run、可重播 SSE 與斷線三次續接的獨立 HTTP 後端。
- 可控制後端並載入 HTML Console 的桌面程式。

程式全部放在 `src/` 下。完整架構請參考 `docs/ai-agent/ARCHITECTURE.md`。

API 契約位於 `src/transport/httpapi/openapi.yaml`；執行、安全與發行說明位於 `docs/ai-agent/`。

## 啟動後端

```bash
go run ./src/cmd/server -config ./configs/ai-agent/config.example.json
```

## 啟動桌面 Console

```bash
go run ./src/cmd/desktop -config ./configs/ai-agent/config.example.json
```

## 建置與圖示資產

執行 `build.command` 會建立本機執行檔，並依設定產生 Windows x64、Windows ARM64 與 macOS ARM64 發行檔。圖示檔可透過 `NR_INTERN_MAC_ICON_PATH` 與 `NR_INTERN_WINDOWS_ICON_PATH` 傳入，不由發行程式固定指定。

需要重新產生平台圖示時，請呼叫 `build-icon-assets.command`，並明確傳入來源檔、每個輸出檔與轉檔工具位置；腳本不包含開發機專用路徑。

## 資料安全

Repository 僅包含範例設定。執行資料、實際 Provider 金鑰、SSH 設定、日誌、建置產物、安裝檔與舊版控管資料都由 `.gitignore` 排除。文件中的路徑一律使用通用工作區範例。

## 授權

本專案採雙重授權：

- 開放原始碼使用適用 [GNU General Public License v3.0 only](LICENSE)。
- 不適合 GPLv3 義務的商業使用，可另行洽談商業授權，詳見 [COMMERCIAL-LICENSE.md](COMMERCIAL-LICENSE.md)。

第三方元件仍依各自附帶的授權條款提供。
