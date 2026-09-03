# 回憶空間（實驗性）

回憶空間讓已確認的偏好、決策、限制與作法能在後續對話重用，同時縮小注入模型的記憶量。
`memory_space` 預設為 `false`；管理介面「實驗性功能」或
`GET／PUT /api/v1/admin/service-settings` 可讀寫，省略時保留，切換立即生效而不需重啟。

此版已實作種類檢查、憑證樣式檢查、近似去重、預設 scope 選擇、召回排序與視窗、失敗後補召回，
以及 scope 超量時的軟性淘汰。儲存維護另會永久清理超過 30 天的失效記憶。
這些機制不是完整語意判斷、個資偵測或多租戶隔離。

## 啟用條件與設定

`memory.enabled` 仍決定是否建立記憶儲存與工具；`memory.auto_recall` 控制自動召回，
`memory.allow_writes` 控制寫入／遺忘工具。模型可呼叫的記憶工具另受擴充工具集與 allowlist 限制。
只打開 `memory_space` 不會繞過這些設定。

JSON 的 `memory.space` 可設定以下策略參數；非正值使用預設。是否啟用以頂層
`memory_space` 為準，不以巢狀 `space.enabled` 另外啟用。

| 欄位 | 預設 | 用途 |
|---|---|---|
| `recall_limit` | 6 | 每次自動召回的最大筆數 |
| `max_injected_characters` | 1200 | 注入大小預算；目前實作按 UTF-8 bytes 計算，包含標記與說明 |
| `min_relevance` | 0.15 | 綜合排序分數的最低門檻；仍先要求文字相關度大於零 |
| `scope_cap` | 500 | 同 scope 的 active 記憶整理目標 |
| `stale_days` | 90 | 新鮮度衰減到零的天數，不是固定刪除期限 |

關閉回憶空間後，後續查詢／寫入回到既有記憶規則，不搬移已存記憶。切換 scope 可能使先前
記憶不再出現在預設查詢；需要時以明確 scope 查看。失效記憶的 30 天儲存清理獨立於此開關。

## 寫入准入與去重

`memory.Manager.Remember` 是 Agent 記憶工具的統一寫入入口：

- 開啟時只接受 `preference`、`decision`、`constraint`、`procedure`；`fact` 會拒絕。
- 對記憶本文檢查常見秘密關鍵字與 token／私鑰樣式，命中便拒絕。
- 提示要求模型只保存可跨任務重用、無法直接從專案推導的資訊；這兩項仍由模型判斷，
  不是寫入端完整驗證。樣式檢查可能誤擋一般文字，也不能保證攔住所有憑證或個資。

寫入前以 `ListScope` 取得同 scope 的 active 記憶，比較詞集合重疊；中文使用連續兩字，
英數使用整詞。重疊至少 0.9，且沒有互相缺少的 ASCII 識別詞時，合併標籤並提高信心值，
沿用原本文字；Repository 可更新既有紀錄，不額外留下近似的 active 副本。

矛盾偵測不自動進行。更新既有決策時由模型明確提供 `supersedes`，跳過近似去重並建立取代關係。
這樣可避免把「改成另一個環境」誤當成同一句話的重述。已被取代的紀錄不再召回，之後依保留期清理。

## 共享範圍

`ScopeForSessionWithSpace` 的選擇順序：

1. Session metadata 明確指定的 `memory_scope`。
2. 開啟回憶空間且 Session 有 Project 時，使用 `project:<id>`。
3. 開啟但沒有 Project 時，使用 `workspace:<id>`。
4. 其餘情況使用 Agent ID，缺少時用 `default`。

可明確指定 `session:<id>` 限制在單一對話，或選擇其他 scope；系統不自動搬移舊 Agent scope 的
記憶。這是查詢範圍約定，不是針對已認證 API Client 的權限隔離。

## 召回時機與視窗

Run 開始時召回一次；回憶空間開啟時，查詢與工具檢索共用當前需求及近期使用者訊息。
符合連續失敗條件的工具可再以名稱與錯誤訊息前 300 字補召回，每個工具在同一 Run 最多一次；
命中後發出 `memory.recalled`，`trigger=tool_failure`。Console 顯示引用筆數與觸發原因，
不會把完整記憶本文直接列到進度區。

初始召回結果會留在後續各輪的 context prompt；失敗補召回只附在下一輪。
「只檢索一次」不代表記憶只送給模型一次。模型主動使用 `memory_search` 是直接查詢，
不套用自動召回的 6 筆／注入大小視窗。

自動召回先取得較寬候選（預設 24 筆），再以
`文字相關度 × 0.6 + 新鮮度 × 0.2 + confidence × 0.2` 排序，要求相關度大於零且綜合分數
不低於 `min_relevance`。這裡的 confidence 是信心值，不是觀測到的命中率。
預設最多 6 筆，並受 1200 UTF-8 bytes 的注入預算限制；中文通常裝不下 1200 個字，
完整項目裝不下時不截成半筆。

召回內容以 `<recalled_memories>` 包裝並標示可能過時、不完整，不得凌駕當前指令或實際工具結果。

## 成長與保留期限

一般新記憶寫入後，同 scope 的 active 紀錄若超過 `scope_cap`，依
`新鮮度 × 0.7 + confidence × 0.3` 將較低分項目標為 `forgotten`。
新鮮度依較新的 `UpdatedAt`／`LastAccessedAt` 線性遞減；90 天只是預設分數衰減窗口。
去重路徑或清理失敗時不保證立即壓至 500 筆，因此不是寫入端的硬性配額。

明確 `memory_forget` 也先轉成 `forgotten`，不是立即刪檔。背景儲存維護在啟動後及每 30 分鐘
檢查，將 `forgotten`／`superseded` 等非 active 且 `UpdatedAt` 早於 30 天前的記憶永久移除。
此清理也會在回憶空間關閉時執行；沒有備份就無法還原被清理的稽核資料，取代鏈也可能指向已移除
的舊 ID。需要長期稽核時請在升級前備份，並另行保留匯出資料。

## 實作位置

| 職責 | 實作 |
|---|---|
| 准入、scope、排序 | `src/memory/space.go` |
| 寫入、去重、scope 超量整理 | `src/memory/write.go` |
| 自動召回與即時開關 | `src/memory/manager.go` |
| 失敗補召回 | `src/memory/failure.go`、`src/harness/loop.go` |
| Agent 記憶工具入口 | `src/tools/native/memories/tools.go` |
| 永久清理失效記憶 | `src/bootstrap/maintenance.go`、`src/adapters/filestore/memory_repository.go` |

管理 API 的記憶讀取、手動寫入與軟性遺忘仍直接使用 Repository，不能宣稱所有管理路徑都經由 Manager；
完整信任邊界見 [安全設計](SECURITY.md)。
