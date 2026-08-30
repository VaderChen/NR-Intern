# 辦公文件工具

NR-Intern 以十個原生工具處理 PDF、DOCX、XLSX 與 PPTX。讀取、建立、編輯、比較、PDF 頁面整理、字型檢查與結構驗證不依賴 Microsoft Office；格式轉換與視覺渲染則使用可探測的 LibreOffice／Poppler 後端：

- `document_inspect`：先取得格式、頁數、段落、工作表、投影片與中繼資料。
- `document_read`：依頁面、段落、工作表列或投影片分段讀取。
- `document_compare`：比對兩份文件指定範圍的抽取文字，並回傳原始檔雜湊。
- `document_validate`：驗證容器、XML、必要部件、內部關聯與格式特有結構。
- `document_fonts`：探索可用 TrueType 字型，並檢查指定文字的字形覆蓋率。
- `document_create`：由結構化內容建立新文件。
- `document_edit`：保留來源，將局部編輯另存到 `output_path`。
- `document_convert`：將 Office／OpenDocument 文件轉成 PDF，或遷移舊格式到 Open XML。
- `pdf_pages`：合併、擷取、重排或拆分 PDF 頁面。
- `document_render`：將文件渲染成逐頁 PNG，供交付前視覺檢查。

所有模型提供的來源、範本、字型與輸出路徑都必須位於目前 Project／Session Sandbox。建立、編輯、轉換、PDF 頁面整理與渲染是 elevated 操作，須通過工具 allowlist、permission profile 與單次 Approval。輸出預設不可覆寫；需要覆寫時明確設定 `overwrite=true`。

## 建立文件

DOCX 與 PDF 使用 `blocks`。區塊類型為 `heading`、`paragraph`、`bullet`、`numbered`、`table`、`page_break`：

```json
{
  "path": "output/report.docx",
  "format": "docx",
  "title": "Quarterly Report",
  "author": "Operations",
  "create_parent": true,
  "blocks": [
    {"type": "heading", "level": 1, "text": "Summary"},
    {"type": "paragraph", "text": "Revenue and delivery remained on plan."},
    {"type": "bullet", "text": "Launch completed"},
    {"type": "table", "rows": [["Metric", "Value"], ["Availability", "99.95%"]]}
  ]
}
```

XLSX 使用 `sheets`；JSON 數字與布林值會保留為試算表型別，公式可有或沒有前導 `=`：

```json
{
  "path": "output/metrics.xlsx",
  "sheets": [{
    "name": "Summary",
    "rows": [["Item", "Value"], ["Orders", 42], ["Ready", true]],
    "formulas": {"B4": "=SUM(B2:B3)"},
    "column_widths": {"A": 24, "B": 14},
    "header_rows": 1,
    "freeze_rows": 1,
    "auto_filter": true
  }]
}
```

PPTX 使用 `slides`，輸出為 16:9 且文字框可在 PowerPoint、Keynote 或 LibreOffice Impress 中繼續編輯：

```json
{
  "path": "output/briefing.pptx",
  "title": "Launch Briefing",
  "slides": [
    {"title": "Launch Briefing", "subtitle": "2026 Q3"},
    {"title": "Status", "body": "Core services are ready.", "bullets": ["API", "Desktop", "Operations"]}
  ]
}
```

PDF 的非 ASCII 文字會自動探索完整涵蓋所需字形的系統 TrueType 字型。需要固定字型或系統找不到完整覆蓋時，可提供 Sandbox 內可嵌入的 `.ttf`；工具會先驗證字形覆蓋率：

```json
{
  "path": "output/report.pdf",
  "title": "營運報告",
  "font_path": "assets/fonts/NotoSansTC-Regular.ttf",
  "blocks": [{"type": "paragraph", "text": "本季服務維持穩定。"}]
}
```

## 使用既有範本

`document_create` 可透過 `template_path` 保留來源文件的樣式、主題、母片、頁首頁尾與未修改部件。範本格式須與輸出副檔名一致：

- DOCX／PPTX：使用 `replacements` 精確替換範本文字。
- XLSX：使用 `cell_updates` 更新既有儲存格，也可使用 `replacements`。
- PDF：使用 `annotations` 疊加標註。
- 未提供任何操作時，範本會原樣複製成輸出文件。

範本模式不接受 `blocks`、`sheets` 或 `slides`，避免在保留設計與重新生成版面之間產生模糊行為：

```json
{
  "path": "output/monthly-report.docx",
  "template_path": "templates/monthly-report.docx",
  "replacements": [
    {"old_text": "{{REPORT_MONTH}}", "new_text": "2026-08"},
    {"old_text": "{{SUMMARY}}", "new_text": "All services remained healthy."}
  ]
}
```

## 編輯文件

DOCX 與 PPTX 的 `replacements` 可跨相鄰文字 run 精確替換；預設只取第一處。使用 `replace_all` 與 `expected_replacements` 可建立 optimistic concurrency 前置條件：

```json
{
  "path": "input/report.docx",
  "output_path": "output/report-revised.docx",
  "replacements": [{
    "old_text": "Draft",
    "new_text": "Final",
    "replace_all": true,
    "expected_replacements": 2
  }]
}
```

XLSX 以 A1 座標更新指定工作表；`formula` 優先作為公式，`value` 可作為儲存格值：

```json
{
  "path": "input/metrics.xlsx",
  "output_path": "output/metrics-revised.xlsx",
  "cell_updates": [
    {"sheet": "Summary", "cell": "B2", "value": 48},
    {"sheet": "Summary", "cell": "B4", "formula": "SUM(B2:B3)"}
  ]
}
```

PDF 不宣稱能原地重排既有文字。`document_edit` 會匯入每個原頁，再以頁面左上角為座標原點、單位為 point 疊加 `text`、`line` 或 `rectangle`：

```json
{
  "path": "input/form.pdf",
  "output_path": "output/form-marked.pdf",
  "font_path": "assets/fonts/NotoSansTC-Regular.ttf",
  "annotations": [
    {"page": 1, "type": "rectangle", "x": 72, "y": 120, "width": 180, "height": 48, "color": "#EF4444", "line_width": 2},
    {"page": 1, "type": "text", "x": 80, "y": 132, "width": 160, "text": "請確認", "color": "#EF4444", "font_size": 14}
  ]
}
```

## 字型與字形覆蓋

`document_fonts` 不回傳系統或使用者目錄的絕對路徑，只揭露字型名稱、檔名、來源範圍與覆蓋結果：

```json
{
  "text": "繁體中文 English 123",
  "limit": 10
}
```

建立或標註 Unicode PDF 時的選擇順序為：

1. 呼叫端提供的 Sandbox `.ttf`，且字形檢查完整通過。
2. 應用程式封裝於 `resources/fonts` 的字型。
3. 使用者與作業系統字型目錄內完整覆蓋的 `.ttf`。
4. 都找不到時明確失敗，不輸出缺字 PDF。

## 文件比較

`document_compare` 使用和 `document_read` 相同的範圍選擇，抽取兩份文件的可見文字後產生 unified diff。兩份文件格式可不同，例如比對 DOCX 原稿與 PDF 定稿；同時回傳原始檔 SHA-256，區分「內容相同」與「二進位檔完全相同」：

```json
{
  "left_path": "input/report-v1.docx",
  "right_path": "input/report-v2.docx",
  "start_paragraph": 1,
  "end_paragraph": 300,
  "context_lines": 3
}
```

`text_equal=true` 只表示選取範圍的抽取文字一致，不表示字型、分頁、圖片或版面一致；`visual_check` 固定回報 `not_run`，版面仍須使用 `document_render` 檢查。

## 格式轉換

`document_convert` 只使用部署者配置、PATH、封裝資源或標準安裝位置中的 LibreOffice，不接受模型傳入 executable：

- DOCX、XLSX、PPTX、ODT、ODS、ODP、RTF 與舊式 DOC／XLS／PPT 可轉成 PDF。
- DOC／ODT／RTF 可遷移到 DOCX。
- XLS／ODS 可遷移到 XLSX。
- PPT／ODP 可遷移到 PPTX。
- 不允許跨文件家族轉換，例如把試算表直接轉成 DOCX。

```json
{
  "path": "input/legacy-report.doc",
  "output_path": "output/legacy-report.docx",
  "create_parent": true
}
```

轉換結果會重新檢查檔案簽章與 Open XML／PDF 類型；來源檔永遠保留。轉成 PDF 或遷移格式可能產生字型替代與分頁差異，因此交付前仍要執行 `document_validate` 與 `document_render`。

## PDF 頁面整理

`pdf_pages` 的 `compose` 依 `sources` 和每個來源 `pages` 的順序輸出新 PDF；因此同一套介面可完成合併、擷取、重排及必要時的頁面複製：

```json
{
  "operation": "compose",
  "sources": [
    {"path": "input/cover.pdf", "pages": [1]},
    {"path": "input/report.pdf", "pages": [1, 3, 2]}
  ],
  "output_path": "output/final.pdf",
  "create_parent": true
}
```

`split` 只接受一個來源，可先用 `pages` 選頁，再按 `chunk_size` 輸出多份：

```json
{
  "operation": "split",
  "sources": [{"path": "input/report.pdf", "pages": [1, 2, 5, 6]}],
  "output_dir": "output/report-parts",
  "chunk_size": 2,
  "create_parent": true
}
```

單次最多處理 500 頁，來源總量與拆分輸出總量各限制 512 MB。工具會保留每頁尺寸，但重新封裝後 PDF 二進位雜湊一定會改變；加密或匯入器不支援的 PDF 會明確失敗。

## 結構驗證

`document_validate` 是唯讀工具，檢查項目包括：

- Open XML ZIP 路徑安全、重複部件、解壓大小、所有 XML 語法與 Content Types。
- `.rels` 的重複 ID、遺失內部目標與外部關聯。
- DOCX 樣式與編號部件、XLSX 工作表及 `#REF!`／快取公式錯誤、PPTX 可達投影片、PDF 可解析頁數。
- 巨集、OLE／嵌入物件；預設回報 warning，`strict=true` 時視為 error。
- LibreOffice／Poppler 渲染後端是否可用。

```json
{
  "path": "output/report.docx",
  "strict": true,
  "require_render_backend": true
}
```

回傳的 `valid=true` 只代表結構驗證沒有 error，`visual_check` 仍會是 `not_run`；必須成功呼叫 `document_render` 並人工／模型檢查 PNG，才能宣稱版面已驗證。

## 視覺渲染

`document_render` 的處理流程：

1. PDF 直接交給 Poppler `pdftoppm`。
2. DOCX、XLSX、PPTX 先由 LibreOffice 以獨立暫存 profile 轉成 PDF。
3. 依頁面範圍輸出 `page-N.png`；PPTX 使用 `slide-N.png`。
4. 所有結果先在暫存目錄完成，再寫入 Sandbox 輸出目錄。

```json
{
  "path": "output/report.docx",
  "output_dir": "output/report-preview",
  "start_page": 1,
  "end_page": 20,
  "dpi": 144,
  "emit_pdf": true
}
```

單次最多渲染 200 頁、最高 300 DPI、總輸出上限 512 MB。工具不接受任意可執行檔路徑；後端只會從固定環境變數、`PATH`、應用程式 `resources/bin` 與標準安裝位置探索。部署者可設定：

- `NR_INTERN_SOFFICE`
- `NR_INTERN_PDFTOPPM`

## 邊界與相容性

- 支援 Open XML 格式 `.docx`、`.xlsx`、`.pptx` 與 PDF；不支援舊式二進位 `.doc`、`.xls`、`.ppt`。
- 舊式 DOC／XLS／PPT 不提供原生讀取或編輯，可先以 `document_convert` 遷移到 Open XML；轉換需安裝或封裝 LibreOffice。
- 不執行巨集、外部關聯、OLE 或嵌入物件。
- Office 編輯只重寫被修改的 XML part，其餘 ZIP part 帶入輸出副本。
- PDF 匯入器可能拒絕加密、損毀或使用不支援壓縮結構的來源；工具會回傳錯誤，不會輸出半成品。
- `document_validate` 負責結構檢查；`document_render` 負責產生視覺檢查材料，兩者不能互相替代。
- LibreOffice 與 Poppler 不存在時，建立／讀取／編輯／結構驗證仍可使用，但不得宣稱通過視覺驗證。
