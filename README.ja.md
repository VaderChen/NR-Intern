# 休まないインターン

[繁體中文](README.md) · [English](README.en.md) · [日本語](README.ja.md) · [한국어](README.ko.md)

NR-Intern は Go で開発されたデスクトップ AI Agent です。現在の会話、確認済みのツール結果、永続化された状態を基に作業を継続します。単純な依頼は直接処理し、長いタスクは計画を作成して段階的に実行・検証できます。

## 主な機能

- OpenAI-compatible Chat Completions と、ChatGPT／Codex OAuth で認証する OpenAI Codex Responses に対応した拡張可能な Provider Router。
- 会話ごとに Provider とモデルを選択できる `Workspace → Project → Session` 構造。
- Workspace と Project の常時指示。作業ルールを一度書けば、以降の会話とスケジュールに自動的に引き継がれます。
- 複数の作業計画、ドラッグによる順序変更、ステップ状態、ツール結果に基づく検証証拠。
- Thinking の段階を選択できます。Session の計画はロックして順番に実行することも、ロックを外して並列実行することもできます。
- 独立したスケジュール欄。繰り返しとサンドボックスを個別に設定し、時刻になると新しい会話を作成して作業を始めます。
- Durable Run、再生可能な SSE、ストリーミング応答、再接続。
- 異常復旧、任意で有効化できる永続通知センター、Run の一時停止／再開／全件キャンセル、脱敏診断パッケージ。
- グローバル検索、安全なバックアップ／復元、読み取り専用の権限センター。復元前には復元可能なスナップショットを自動保存します。
- 公式 GitHub Release のバージョン更新チェック。通知センターを有効にすると新しいバージョンが通知センターに入り、同じバージョンは重複通知しません。
- 会話の実行中も続けて入力でき、後続メッセージは現在の UI の送信待ちキューに入り、前の Run が終わると順番に送信されます。
- Context の自動整理、Session 間の長期メモリ、メモリ範囲の制御。
- Run と Session ごとの input／output／total token 使用量。モデル単価を設定すると推定コストも表示し、未設定時は token だけを表示します。
- Sandbox と実行承認で保護されたファイル、文書、Shell、SSH、計画ツール。
- モデルに合わせて、簡易／拡張ツールセット、ツール検索、native／instruction のツール呼び出し方式を選択できます。
- 非同期処理にはキャンセル可能な `wait_for` を使え、リモートデプロイは `ssh_wait` で読み取り専用の確認を繰り返し、bytes、SHA-256、サービス準備完了を確認してから完了と判定します。
- 外部リソースを読み取る `http_fetch`。HTML は自動で純テキストに変換し、localhost とプライベートアドレスは既定で許可、管理画面から丸ごと無効化できます。
- 内蔵 MCP Client からローカル stdio、レガシー SSE、またはリモート Streamable HTTP Server に接続し、外部ツールにも同じ権限・承認フローを適用します。
- オプションの NetPass リバースプロキシは、デスクトップ制御 UI を公開せずにバックエンド API を公開できます。
- Provider の有効化、モデル検出、ツール権限、監査記録。
- ライト／ダーク表示と、AUTO、繁体字中国語、英語、日本語、韓国語の UI。
- Windows x64、Windows ARM64、macOS ARM64 のデスクトップ環境。
- macOS では起動時からステータス項目を表示します。作業を続けたまま UI を隠し、メニューまたはアプリの再起動操作から元のウィンドウを復元できます。
- Agent に送信しないメモを端末内だけに保存できるメモ帳を、サイドバーから開けます。
- 会話入力欄から画面キャプチャを開始できます。macOS ではシステムの範囲選択後、四角形・直線・文字を追加する編集画面が開き、コピーまたは終了時に編集済み画像でクリップボードを更新します。Windows ではシステムの切り取り UI を開きます。

表示言語は UI と未変更の既定名だけに適用されます。Agent は現在のメッセージ、最近の会話、明示された設定から利用者の慣用言語を判断します。

## はじめに

1. `configs/ai-agent/config.example.json` を基にローカル設定を作成します。
2. Provider endpoint、モデル、必要な認証情報をローカルまたは管理画面で設定します。
3. Workspace、Project、Session を作成し、会話画面からタスクを依頼します。

実際の設定、実行データ、認証情報はバージョン管理に含めないでください。設計の詳細は以下の文書を参照してください。

## ドキュメント

[ドキュメントサイトを開く](https://vaderchen.github.io/NR-Intern/)

- [アーキテクチャ](https://vaderchen.github.io/NR-Intern/architecture.html)
- [文書ツール](https://vaderchen.github.io/NR-Intern/document-tools.html)
- [開発ガイド](https://vaderchen.github.io/NR-Intern/development.html)
- [HTTP API](https://vaderchen.github.io/NR-Intern/http-api.html)
- [セキュリティ](https://vaderchen.github.io/NR-Intern/security.html)
- [OpenAPI 契約](https://vaderchen.github.io/NR-Intern/openapi.html)

リリースのパッケージ化と署名はメンテナー専用の内部手順で行います。この README ではパッケージ化コマンド、パラメーター、署名設定を公開しません。

## データセキュリティ

- Repository に含まれるのはサンプル設定だけです。Provider／OAuth／MCP／NetPass キー、SSH 認証情報、ログ、実行データ、リリース成果物は `.gitignore` で除外されます。
- 文書と例では相対パス、localhost、example domain のみを使用し、開発者固有の絶対ディレクトリを含めません。
- API Key、Token、秘密鍵、証明書、署名 ID、実サービスのアドレスを commit しないでください。

## ライセンス

本プロジェクトはデュアルライセンスです。

- オープンソース利用には [GNU General Public License v3.0 only](LICENSE) が適用されます。
- GPLv3 の条件が適さない商用利用については、[COMMERCIAL-LICENSE.md](COMMERCIAL-LICENSE.md) を参照してください。

第三者コンポーネントには、それぞれ同梱されているライセンスが適用されます。
