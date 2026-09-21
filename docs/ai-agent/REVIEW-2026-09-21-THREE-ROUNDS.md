# 2026-09-21 接續三輪檢查與修正

## 範圍與原則

接續現有程式逐輪檢查，不把前一輪結論當作已驗證事實。保持介面、使用操作、路由、
事件格式、認證方式與核准政策不變；保留全部既有修改。只讀取程式與合成測試資料，
未讀取本機金鑰／帳號設定，未執行 Git、封裝、發布，也未重啟或終止 APP。
大量修改前建立本次專用 `.bak`，全部驗證完成後才清除本次備份。

## 第一輪：工具派送與結果保存

- 原本同批工具全部返回後，逐筆「保存結果 → 事件」；第一筆事件失敗或取消即離開，
  會丟失其他工具已經完成的觀察。改為先保存整批結果與稽核，再發送展示事件。
- 個別儲存失敗仍繼續嘗試保存其他結果，最後以彙總錯誤結束；每批共用 10 秒收尾期限。
- 派送前錯誤明確記為 `not_dispatched`，不偽造成功，也不讓下次執行誤判為已派送。
- `AfterTool` 可以調整內容，但不得移除先前的未派送／未知事實並改成成功。
- 合成 Smoke 覆蓋事件失敗、取消、派送紀錄失敗、單筆結果寫入失敗、Hook 改寫；
  核對其他結果確實落盤與實際工具執行次數。

## 第二輪：長對話索引與追加一致性

- 原索引僅比對大小，同大小改寫可能沿用過期 type／offset；截短後還會偏好較大的舊索引。
  改為檔案身分、大小、修改時間共同驗證，索引重建採用實際開啟檔案的狀態。
- 讀取與追加原本可交錯，且快取 size 有無鎖讀寫。現在查詢、追加、刪除使用相同的
  固定 64 分片鎖；索引 map 鎖不涵蓋磁碟讀寫，不把所有 Session 序列化到單一鎖。
- 移除獨立序號快取，每次追加從已驗證索引取號；已知正常追加仍增量更新，不重掃全文。
- 完整尾筆沒有換行時補分隔，避免兩筆 JSON 黏在一起。不完整尾筆拒絕追加，原文保留。
  寫入失敗則失效索引，不能把「回報失敗」誤認為磁碟完全沒有寫入。
- 合成 Smoke 覆蓋同大小覆寫、同大小同時間戳但不同檔案身分的替換、截短、缺換行、
  損壞尾筆及並行查詢／追加；核對序號與索引位移。

## 第三輪：上游串流與終態交叉檢查

- 原代理拒絕在第一個文字 delta 就分類，原因拆段時可能漏掉後段的過載訊息；
  Chat 後續省略 model／ID 時也可能失去辨識。現在保留身分、收集有界錯誤原因後再分類，
  不把代理錯誤發送成正常回答。一般回答引用同樣文字仍正常通過。
- Responses 原本接受單獨 `[DONE]` 當完成，可能交付尚未完成的工具呼叫。
  現在必須有帶 response 物件的 `response.completed`，不完整終態回報錯誤。
- 單 byte 分段測試覆蓋 UTF-8／CRLF；另驗證三次總嘗試耗盡、退避取消，以及已送出
  文字／思考／工具 delta 後不重新請求。延遲政策保持 10、20 秒，測試重試上限時使用
  合成伺服器的 `Retry-After: 1`，不存取真實上游。
- 回歸檢查 Run 終態修復、SSE 重連、核准、工具契約、備份還原與前端現有 Smoke。

## 驗證與限制

以下驗證全部通過：

- `go build -buildvcs=false ./src/...`。
- `go vet ./src/...`。
- Harness、filestore、OpenAI-compatible adapter、Application、HTTP API、工具 Runtime、
  核准、問答與備份還原的相關 Smoke／回歸測試，使用 `-race -count=1 -timeout=60s`。
- `node --check src/web/console/assets/app.js`。
- `node scripts/smoke-run-reconnect.cjs`、`node scripts/smoke-provider-responses.cjs`、
  `node scripts/smoke-provider-accounts.cjs`。
- `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -buildvcs=false ./src/harness ./src/adapters/filestore ./src/adapters/openaicompat ./src/application ./src/transport/httpapi`。

本機建置仍有既有 duplicate `-lobjc` 警告。測試使用暫存目錄與合成本機伺服器，沒有
對真實外部系統執行副作用或實際帳號登入。不宣稱實際上游過載、APP 長時間執行或
Windows 安裝已驗收。

結果保存不是整批原子交易；永久 I/O 故障、忽略 context 的儲存實作、程序中止仍可能
遺失觀察。索引同步限同一 Repository 實例，不保證多程序共同寫入或惡意保留身分與時間戳
的改寫。代理錯誤的收集仍受原有請求逾時／Run 預算控制。只傳 `[DONE]` 而缺少完成事件
的非標準 Responses 上游現在會回報錯誤，必須由上游送出完整終態，不能以不完整回覆派送工具。
