(() => {
  const supportedPreferences = new Set(["auto", "zh-TW", "en", "ja", "ko"]);
  const attributeNames = ["placeholder", "title", "aria-label"];
  const ignoredSelector = [
    "[data-i18n-ignore]",
    "#brandName",
    "#sessionTitle",
    "#workspaceSelect",
    ".project-list",
    ".sessions",
    "#messages",
    ".session-content-list",
    ".provider-settings-list",
    ".plan-list",
    "pre",
    "code",
  ].join(",");

  const catalogs = {
    en: {
      "檢查中": "Checking",
      "已釘選": "Pinned",
      "專案": "Projects",
      "系統管理": "System settings",
      "選擇或建立對話": "Select or create a conversation",
      "模型": "Model",
      "外觀": "Appearance",
      "自動": "Auto",
      "明亮": "Light",
      "深色": "Dark",
      "從實際行動開始": "Start with real action",
      "Agent 不會先固定拆解問題，而會依目前狀態、記憶與原生工具結果持續運作。": "The Agent works continuously from the current state, memory, and native tool results.",
      "重試 Run": "Retry run",
      "計畫": "Plan",
      "建立": "Create",
      "← 返回": "← Back",
      "後端與 Session": "Backend and sessions",
      "一般設定": "General settings",
      "Provider 設定": "Provider settings",
      "工具": "Tools",
      "稽核": "Audit",
      "服務與執行設定": "Service and run settings",
      "顯示名稱": "Display name",
      "介面語言": "Interface language",
      "單次工作時間上限（分鐘）": "Run time limit (minutes)",
      "單次工作 Token 上限": "Token limit per run",
      "單次工作工具呼叫上限": "Tool-call limit per run",
      "Token 與工具呼叫填 0 表示不限制；時間上限為 1～1440 分鐘。新數值會套用到之後開始的工作，不影響正在執行中的工作。": "Set tokens or tool calls to 0 for unlimited. Time must be 1–1440 minutes. New values apply to future runs only.",
      "儲存服務設定": "Save service settings",
      "後端程序": "Backend process",
      "啟動": "Start",
      "重啟": "Restart",
      "停止": "Stop",
      "後端診斷": "Backend diagnostics",
      "重新整理": "Refresh",
      "脫敏設定": "Redacted configuration",
      "Workspace 設定": "Workspace settings",
      "名稱": "Name",
      "說明": "Description",
      "預設 Provider": "Default provider",
      "預設模型": "Default model",
      "儲存 Workspace 設定": "Save workspace settings",
      "Session 設定": "Session settings",
      "所屬專案": "Project",
      "工具權限": "Tool permissions",
      "標準權限": "Standard permissions",
      "信任工具權限": "Trusted tool permissions",
      "釘選此對話": "Pin this conversation",
      "儲存 Session 設定": "Save session settings",
      "選擇 Session 後可管理模型、權限與記憶 scope。": "Select a session to manage its model, permissions, and memory scope.",
      "Provider 清單": "Provider list",
      "可建立多個 Provider，並由 Workspace 選擇使用。": "Create multiple providers and select them from a workspace.",
      "設定儲存後會直接套用到新的模型請求。": "Saved settings apply to new model requests.",
      "啟用": "Enabled",
      "類型": "Type",
      "清除目前已儲存的 API Key": "Clear the saved API key",
      "更新列表": "Refresh list",
      "模型列表尚未載入，仍可手動輸入模型名稱。": "The model list is not loaded; you can still enter a model name.",
      "重試次數": "Retry attempts",
      "設為系統預設": "Set as system default",
      "停用 Streaming": "Disable streaming",
      "串流包含 Usage": "Include usage in stream",
      "省略 tool_choice": "Omit tool_choice",
      "進階設定": "Advanced settings",
      "請求逾時（秒）": "Request timeout (seconds)",
      "連線逾時（秒）": "Connection timeout (seconds)",
      "回應標頭逾時（秒）": "Response header timeout (seconds)",
      "最大輸出 Tokens": "Maximum output tokens",
      "刪除 Provider": "Delete provider",
      "測試連線": "Test connection",
      "儲存 Provider": "Save provider",
      "請選擇 Provider，或建立新的 Provider。": "Select a provider or create a new one.",
      "原生工具": "Native tools",
      "可用性同時由後端 allowlist、elevated tools 設定與 Session 權限判斷。": "Availability is determined by the backend allowlist, elevated-tool settings, and session permissions.",
      "Harness 稽核": "Harness audit",
      "依 sequence 顯示 operation、turn 與 tool call 的持久化生命週期。": "Shows the persisted lifecycle of operations, turns, and tool calls by sequence.",
      "載入更多": "Load more",
      "檢視內容": "View content",
      "重新命名": "Rename",
      "釘選對話": "Pin conversation",
      "刪除對話": "Delete conversation",
      "開啟": "Open",
      "在檔案管理器中顯示": "Show in file manager",
      "複製": "Copy",
      "建立新 Session": "Create session",
      "建立後釘選": "Pin after creation",
      "取消": "Cancel",
      "儲存": "Save",
      "對話內容": "Conversation content",
      "建立新專案": "Create project",
      "專案設定": "Project settings",
      "Sandbox 目錄": "Sandbox directories",
      "拖入一個或多個目錄": "Drop one or more directories",
      "或按一下使用系統目錄選擇器": "or click to use the system directory picker",
      "刪除專案": "Delete project",
      "建立新 Workspace": "Create workspace",
      "工具執行審核": "Tool execution approval",
      "Agent 要求執行": "The Agent requests",
      "工具參數": "Tool arguments",
      "決策理由（選填）": "Decision reason (optional)",
      "拒絕": "Deny",
      "核准執行": "Approve",
      "工作計畫": "Work plans",
      "依序執行並驗證每一個步驟": "Execute and verify each step in order",
      "拖曳計畫可調整執行順序": "Drag plans to change execution order",
      "＋ 新增計畫": "+ New plan",
      "尚未建立計畫": "No plans yet",
      "你可以先拆解長任務；Agent 遇到多步驟工作時也能自行建立計畫。": "Break down a long task, or let the Agent create a plan for multi-step work.",
      "新增計畫": "New plan",
      "計畫名稱": "Plan name",
      "目標": "Objective",
      "執行步驟": "Steps",
      "＋ 新增步驟": "+ Add step",
      "每一步都要有可由工具確認的客觀驗證條件。": "Each step needs an objective verification condition that tools can confirm.",
      "儲存計畫": "Save plan",
      "輸入想做的事 或 請 Agent 幫你制定一個計畫": "Describe what you want to do, or ask the Agent to create a plan",
      "送出訊息": "Send message",
      "顯示模式": "Display mode",
      "關閉": "Close",
      "系統管理功能": "System settings",
      "後端執行中": "Backend running",
      "已連接外部後端": "External backend connected",
      "後端未啟動": "Backend stopped",
      "儲存中…": "Saving…",
      "已儲存": "Saved",
      "儲存失敗": "Save failed",
      "設定值無效": "Invalid settings",
      "已停用": "Disabled",
      "已完成": "Completed",
      "執行中": "Running",
      "待處理": "Pending",
    },
    ja: {
      "檢查中": "確認中",
      "已釘選": "ピン留め",
      "專案": "プロジェクト",
      "系統管理": "システム設定",
      "選擇或建立對話": "会話を選択または作成",
      "模型": "モデル",
      "外觀": "外観",
      "自動": "自動",
      "明亮": "ライト",
      "深色": "ダーク",
      "從實際行動開始": "実際のアクションから開始",
      "Agent 不會先固定拆解問題，而會依目前狀態、記憶與原生工具結果持續運作。": "Agent は現在の状態、メモリ、ネイティブツールの結果に基づいて継続的に動作します。",
      "重試 Run": "再実行",
      "計畫": "計画",
      "建立": "作成",
      "← 返回": "← 戻る",
      "後端與 Session": "バックエンドとセッション",
      "一般設定": "一般設定",
      "Provider 設定": "Provider 設定",
      "工具": "ツール",
      "稽核": "監査",
      "服務與執行設定": "サービスと実行の設定",
      "顯示名稱": "表示名",
      "介面語言": "表示言語",
      "單次工作時間上限（分鐘）": "実行時間の上限（分）",
      "單次工作 Token 上限": "1回の Token 上限",
      "單次工作工具呼叫上限": "1回のツール呼び出し上限",
      "Token 與工具呼叫填 0 表示不限制；時間上限為 1～1440 分鐘。新數值會套用到之後開始的工作，不影響正在執行中的工作。": "Token とツール呼び出しは 0 で無制限です。時間は 1～1440 分。新しい値は今後の実行に適用されます。",
      "儲存服務設定": "サービス設定を保存",
      "後端程序": "バックエンドプロセス",
      "啟動": "起動",
      "重啟": "再起動",
      "停止": "停止",
      "後端診斷": "バックエンド診断",
      "重新整理": "更新",
      "脫敏設定": "マスキング済み設定",
      "Workspace 設定": "Workspace 設定",
      "名稱": "名前",
      "說明": "説明",
      "預設 Provider": "既定の Provider",
      "預設模型": "既定のモデル",
      "儲存 Workspace 設定": "Workspace 設定を保存",
      "Session 設定": "Session 設定",
      "所屬專案": "プロジェクト",
      "工具權限": "ツール権限",
      "標準權限": "標準権限",
      "信任工具權限": "信頼済みツール権限",
      "釘選此對話": "この会話をピン留め",
      "儲存 Session 設定": "Session 設定を保存",
      "選擇 Session 後可管理模型、權限與記憶 scope。": "Session を選択すると、モデル、権限、メモリスコープを管理できます。",
      "Provider 清單": "Provider 一覧",
      "可建立多個 Provider，並由 Workspace 選擇使用。": "複数の Provider を作成し、Workspace から選択できます。",
      "設定儲存後會直接套用到新的模型請求。": "保存した設定は新しいモデルリクエストに適用されます。",
      "啟用": "有効",
      "類型": "種類",
      "清除目前已儲存的 API Key": "保存済み API Key を消去",
      "更新列表": "一覧を更新",
      "模型列表尚未載入，仍可手動輸入模型名稱。": "モデル一覧は未読み込みです。モデル名を直接入力できます。",
      "重試次數": "再試行回数",
      "設為系統預設": "システム既定に設定",
      "停用 Streaming": "ストリーミングを無効化",
      "串流包含 Usage": "ストリームに Usage を含める",
      "省略 tool_choice": "tool_choice を省略",
      "進階設定": "詳細設定",
      "請求逾時（秒）": "リクエストタイムアウト（秒）",
      "連線逾時（秒）": "接続タイムアウト（秒）",
      "回應標頭逾時（秒）": "応答ヘッダータイムアウト（秒）",
      "最大輸出 Tokens": "最大出力 Token",
      "刪除 Provider": "Provider を削除",
      "測試連線": "接続テスト",
      "儲存 Provider": "Provider を保存",
      "請選擇 Provider，或建立新的 Provider。": "Provider を選択するか、新しく作成してください。",
      "原生工具": "ネイティブツール",
      "可用性同時由後端 allowlist、elevated tools 設定與 Session 權限判斷。": "利用可否はバックエンドの allowlist、elevated tools 設定、Session 権限によって決まります。",
      "Harness 稽核": "Harness 監査",
      "依 sequence 顯示 operation、turn 與 tool call 的持久化生命週期。": "sequence 順に operation、turn、tool call の永続化ライフサイクルを表示します。",
      "載入更多": "さらに読み込む",
      "檢視內容": "内容を表示",
      "重新命名": "名前を変更",
      "釘選對話": "会話をピン留め",
      "刪除對話": "会話を削除",
      "開啟": "開く",
      "在檔案管理器中顯示": "ファイルマネージャーで表示",
      "複製": "コピー",
      "建立新 Session": "Session を作成",
      "建立後釘選": "作成後にピン留め",
      "取消": "キャンセル",
      "儲存": "保存",
      "對話內容": "会話内容",
      "建立新專案": "プロジェクトを作成",
      "專案設定": "プロジェクト設定",
      "Sandbox 目錄": "Sandbox ディレクトリ",
      "拖入一個或多個目錄": "1つ以上のディレクトリをドロップ",
      "或按一下使用系統目錄選擇器": "またはクリックしてシステムの選択画面を使用",
      "刪除專案": "プロジェクトを削除",
      "建立新 Workspace": "Workspace を作成",
      "工具執行審核": "ツール実行の承認",
      "Agent 要求執行": "Agent の実行要求",
      "工具參數": "ツール引数",
      "決策理由（選填）": "判断理由（任意）",
      "拒絕": "拒否",
      "核准執行": "実行を承認",
      "工作計畫": "作業計画",
      "依序執行並驗證每一個步驟": "各ステップを順番に実行して検証",
      "拖曳計畫可調整執行順序": "計画をドラッグして実行順序を変更",
      "＋ 新增計畫": "＋ 計画を追加",
      "尚未建立計畫": "計画はまだありません",
      "你可以先拆解長任務；Agent 遇到多步驟工作時也能自行建立計畫。": "長いタスクを分解するか、Agent に複数ステップの計画を作成させることができます。",
      "新增計畫": "計画を追加",
      "計畫名稱": "計画名",
      "目標": "目標",
      "執行步驟": "実行ステップ",
      "＋ 新增步驟": "＋ ステップを追加",
      "每一步都要有可由工具確認的客觀驗證條件。": "各ステップにはツールで確認できる客観的な検証条件が必要です。",
      "儲存計畫": "計画を保存",
      "輸入想做的事 或 請 Agent 幫你制定一個計畫": "やりたいことを入力するか、Agent に計画作成を依頼してください",
      "送出訊息": "メッセージを送信",
      "顯示模式": "表示モード",
      "關閉": "閉じる",
      "系統管理功能": "システム設定",
      "後端執行中": "バックエンド実行中",
      "已連接外部後端": "外部バックエンドに接続済み",
      "後端未啟動": "バックエンド停止中",
      "儲存中…": "保存中…",
      "已儲存": "保存済み",
      "儲存失敗": "保存に失敗",
      "設定值無效": "設定値が無効です",
      "已停用": "無効",
      "已完成": "完了",
      "執行中": "実行中",
      "待處理": "保留中",
    },
    ko: {
      "檢查中": "확인 중",
      "已釘選": "고정됨",
      "專案": "프로젝트",
      "系統管理": "시스템 설정",
      "選擇或建立對話": "대화를 선택하거나 만드세요",
      "模型": "모델",
      "外觀": "화면 모드",
      "自動": "자동",
      "明亮": "라이트",
      "深色": "다크",
      "從實際行動開始": "실제 작업부터 시작",
      "Agent 不會先固定拆解問題，而會依目前狀態、記憶與原生工具結果持續運作。": "Agent는 현재 상태, 메모리 및 네이티브 도구 결과를 바탕으로 계속 작업합니다.",
      "重試 Run": "다시 실행",
      "計畫": "계획",
      "建立": "만들기",
      "← 返回": "← 돌아가기",
      "後端與 Session": "백엔드 및 세션",
      "一般設定": "일반 설정",
      "Provider 設定": "Provider 설정",
      "工具": "도구",
      "稽核": "감사",
      "服務與執行設定": "서비스 및 실행 설정",
      "顯示名稱": "표시 이름",
      "介面語言": "인터페이스 언어",
      "單次工作時間上限（分鐘）": "실행 시간 제한(분)",
      "單次工作 Token 上限": "실행당 Token 제한",
      "單次工作工具呼叫上限": "실행당 도구 호출 제한",
      "Token 與工具呼叫填 0 表示不限制；時間上限為 1～1440 分鐘。新數值會套用到之後開始的工作，不影響正在執行中的工作。": "Token 및 도구 호출은 0이면 무제한입니다. 시간은 1~1440분이며 새 값은 이후 실행부터 적용됩니다.",
      "儲存服務設定": "서비스 설정 저장",
      "後端程序": "백엔드 프로세스",
      "啟動": "시작",
      "重啟": "다시 시작",
      "停止": "중지",
      "後端診斷": "백엔드 진단",
      "重新整理": "새로 고침",
      "脫敏設定": "민감 정보 제거 설정",
      "Workspace 設定": "Workspace 설정",
      "名稱": "이름",
      "說明": "설명",
      "預設 Provider": "기본 Provider",
      "預設模型": "기본 모델",
      "儲存 Workspace 設定": "Workspace 설정 저장",
      "Session 設定": "Session 설정",
      "所屬專案": "프로젝트",
      "工具權限": "도구 권한",
      "標準權限": "표준 권한",
      "信任工具權限": "신뢰 도구 권한",
      "釘選此對話": "이 대화 고정",
      "儲存 Session 設定": "Session 설정 저장",
      "選擇 Session 後可管理模型、權限與記憶 scope。": "Session을 선택하면 모델, 권한 및 메모리 범위를 관리할 수 있습니다.",
      "Provider 清單": "Provider 목록",
      "可建立多個 Provider，並由 Workspace 選擇使用。": "여러 Provider를 만들고 Workspace에서 선택할 수 있습니다.",
      "設定儲存後會直接套用到新的模型請求。": "저장된 설정은 새 모델 요청에 적용됩니다.",
      "啟用": "활성화",
      "類型": "유형",
      "清除目前已儲存的 API Key": "저장된 API Key 지우기",
      "更新列表": "목록 새로 고침",
      "模型列表尚未載入，仍可手動輸入模型名稱。": "모델 목록을 불러오지 않았습니다. 모델 이름을 직접 입력할 수 있습니다.",
      "重試次數": "재시도 횟수",
      "設為系統預設": "시스템 기본값으로 설정",
      "停用 Streaming": "스트리밍 비활성화",
      "串流包含 Usage": "스트림에 Usage 포함",
      "省略 tool_choice": "tool_choice 생략",
      "進階設定": "고급 설정",
      "請求逾時（秒）": "요청 제한 시간(초)",
      "連線逾時（秒）": "연결 제한 시간(초)",
      "回應標頭逾時（秒）": "응답 헤더 제한 시간(초)",
      "最大輸出 Tokens": "최대 출력 Token",
      "刪除 Provider": "Provider 삭제",
      "測試連線": "연결 테스트",
      "儲存 Provider": "Provider 저장",
      "請選擇 Provider，或建立新的 Provider。": "Provider를 선택하거나 새로 만드세요.",
      "原生工具": "네이티브 도구",
      "可用性同時由後端 allowlist、elevated tools 設定與 Session 權限判斷。": "사용 가능 여부는 백엔드 allowlist, elevated tools 설정 및 Session 권한에 따라 결정됩니다.",
      "Harness 稽核": "Harness 감사",
      "依 sequence 顯示 operation、turn 與 tool call 的持久化生命週期。": "sequence 순서로 operation, turn 및 tool call의 영구 수명 주기를 표시합니다.",
      "載入更多": "더 불러오기",
      "檢視內容": "내용 보기",
      "重新命名": "이름 바꾸기",
      "釘選對話": "대화 고정",
      "刪除對話": "대화 삭제",
      "開啟": "열기",
      "在檔案管理器中顯示": "파일 관리자에서 보기",
      "複製": "복사",
      "建立新 Session": "Session 만들기",
      "建立後釘選": "생성 후 고정",
      "取消": "취소",
      "儲存": "저장",
      "對話內容": "대화 내용",
      "建立新專案": "프로젝트 만들기",
      "專案設定": "프로젝트 설정",
      "Sandbox 目錄": "Sandbox 디렉터리",
      "拖入一個或多個目錄": "하나 이상의 디렉터리 놓기",
      "或按一下使用系統目錄選擇器": "또는 클릭하여 시스템 디렉터리 선택기 사용",
      "刪除專案": "프로젝트 삭제",
      "建立新 Workspace": "Workspace 만들기",
      "工具執行審核": "도구 실행 승인",
      "Agent 要求執行": "Agent 실행 요청",
      "工具參數": "도구 인수",
      "決策理由（選填）": "결정 사유(선택 사항)",
      "拒絕": "거부",
      "核准執行": "실행 승인",
      "工作計畫": "작업 계획",
      "依序執行並驗證每一個步驟": "각 단계를 순서대로 실행하고 검증",
      "拖曳計畫可調整執行順序": "계획을 끌어 실행 순서 변경",
      "＋ 新增計畫": "＋ 계획 추가",
      "尚未建立計畫": "아직 계획이 없습니다",
      "你可以先拆解長任務；Agent 遇到多步驟工作時也能自行建立計畫。": "긴 작업을 나누거나 Agent가 다단계 작업 계획을 만들도록 할 수 있습니다.",
      "新增計畫": "계획 추가",
      "計畫名稱": "계획 이름",
      "目標": "목표",
      "執行步驟": "실행 단계",
      "＋ 新增步驟": "＋ 단계 추가",
      "每一步都要有可由工具確認的客觀驗證條件。": "각 단계에는 도구로 확인할 수 있는 객관적인 검증 조건이 필요합니다.",
      "儲存計畫": "계획 저장",
      "輸入想做的事 或 請 Agent 幫你制定一個計畫": "하고 싶은 일을 입력하거나 Agent에게 계획 작성을 요청하세요",
      "送出訊息": "메시지 보내기",
      "顯示模式": "화면 모드",
      "關閉": "닫기",
      "系統管理功能": "시스템 설정",
      "後端執行中": "백엔드 실행 중",
      "已連接外部後端": "외부 백엔드 연결됨",
      "後端未啟動": "백엔드 중지됨",
      "儲存中…": "저장 중…",
      "已儲存": "저장됨",
      "儲存失敗": "저장 실패",
      "設定值無效": "잘못된 설정 값",
      "已停用": "비활성화됨",
      "已完成": "완료",
      "執行中": "실행 중",
      "待處理": "대기 중",
    },
  };

  const sources = new Set(Object.values(catalogs).flatMap((catalog) => Object.keys(catalog)));
  const textSources = new WeakMap();
  const attributeSources = new WeakMap();
  let preference = "auto";
  let language = "zh-TW";

  function normalizePreference(value) {
    return supportedPreferences.has(value) ? value : "auto";
  }

  function resolveLanguage(value) {
    if (value !== "auto") return value;
    for (const candidate of navigator.languages || [navigator.language || ""]) {
      const normalized = String(candidate).toLowerCase();
      if (normalized.startsWith("zh")) return "zh-TW";
      if (normalized.startsWith("ja")) return "ja";
      if (normalized.startsWith("ko")) return "ko";
      if (normalized.startsWith("en")) return "en";
    }
    return "zh-TW";
  }

  function translated(source) {
    return language === "zh-TW" ? source : catalogs[language]?.[source] || source;
  }

  function shouldIgnore(element) {
    return Boolean(element?.closest?.(ignoredSelector));
  }

  function translateTextNode(node) {
    if (!node?.parentElement || shouldIgnore(node.parentElement)) return;
    const current = node.nodeValue || "";
    const trimmed = current.trim();
    let source = textSources.get(node);
    if (sources.has(trimmed)) {
      source = trimmed;
      textSources.set(node, source);
    }
    if (!source) return;
    const leading = current.match(/^\s*/)?.[0] || "";
    const trailing = current.match(/\s*$/)?.[0] || "";
    const next = `${leading}${translated(source)}${trailing}`;
    if (next !== current) node.nodeValue = next;
  }

  function translateAttribute(element, name) {
    if (!element?.hasAttribute?.(name) || shouldIgnore(element)) return;
    const current = element.getAttribute(name) || "";
    let stored = attributeSources.get(element);
    if (!stored) {
      stored = {};
      attributeSources.set(element, stored);
    }
    if (sources.has(current)) stored[name] = current;
    const source = stored[name];
    if (!source) return;
    const next = translated(source);
    if (next !== current) element.setAttribute(name, next);
  }

  function translateTree(root = document.body) {
    if (!root) return;
    if (root.nodeType === Node.TEXT_NODE) {
      translateTextNode(root);
      return;
    }
    if (root.nodeType !== Node.ELEMENT_NODE && root.nodeType !== Node.DOCUMENT_NODE) return;
    const element = root.nodeType === Node.ELEMENT_NODE ? root : null;
    if (element && shouldIgnore(element)) return;
    if (element) for (const name of attributeNames) translateAttribute(element, name);
    const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
    for (let node = walker.nextNode(); node; node = walker.nextNode()) translateTextNode(node);
    const elements = root.querySelectorAll?.(attributeNames.map((name) => `[${name}]`).join(",")) || [];
    for (const target of elements) for (const name of attributeNames) translateAttribute(target, name);
  }

  function setLanguage(value) {
    preference = normalizePreference(value);
    language = resolveLanguage(preference);
    document.documentElement.dataset.uiLanguage = preference;
    document.documentElement.lang = language;
    translateTree(document.body);
    window.dispatchEvent(new CustomEvent("nr-intern-language-change", {
      detail: { preference, language },
    }));
    return language;
  }

  const observer = new MutationObserver((records) => {
    for (const record of records) {
      if (record.type === "characterData") translateTextNode(record.target);
      if (record.type === "attributes") translateAttribute(record.target, record.attributeName);
      for (const node of record.addedNodes || []) translateTree(node);
    }
  });
  observer.observe(document.body, {
    subtree: true,
    childList: true,
    characterData: true,
    attributes: true,
    attributeFilter: attributeNames,
  });

  window.NRInternI18n = {
    setLanguage,
    t: (source) => translated(source),
    get preference() { return preference; },
    get language() { return language; },
    supportedPreferences: [...supportedPreferences],
  };

  window.addEventListener("languagechange", () => {
    if (preference === "auto") setLanguage("auto");
  });
  setLanguage(document.documentElement.dataset.uiLanguage || "auto");
})();
