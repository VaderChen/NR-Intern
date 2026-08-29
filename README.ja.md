# 休まないインターン

[繁體中文](README.md) · [English](README.en.md) · [日本語](README.ja.md) · [한국어](README.ko.md)

NR-Intern は Go で開発されたデスクトップ AI Agent です。現在の会話、確認済みのツール結果、永続化された状態を基に作業を継続します。単純な依頼は直接処理し、長いタスクは計画を作成して段階的に実行・検証できます。

## 主な機能

- リモートおよびローカルのモデルサービスに対応する、拡張可能な OpenAI-compatible Provider Router。
- 会話ごとに Provider とモデルを選択できる `Workspace → Project → Session` 構造。
- 複数の作業計画、ドラッグによる順序変更、ステップ状態、ツール結果に基づく検証証拠。
- Durable Run、再生可能な SSE、ストリーミング応答、再接続。
- Context の自動整理、Session 間の長期メモリ、メモリ範囲の制御。
- Sandbox と実行承認で保護されたファイル、文書、Shell、SSH、計画ツール。
- Provider の有効化、モデル検出、ツール権限、監査記録。
- ライト／ダーク表示と、AUTO、繁体字中国語、英語、日本語、韓国語の UI。
- Windows x64、Windows ARM64、macOS ARM64 のデスクトップ環境。

表示言語は UI と未変更の既定名だけに適用されます。Agent は現在のメッセージ、最近の会話、明示された設定から利用者の慣用言語を判断します。

## はじめに

1. `configs/ai-agent/config.example.json` を基にローカル設定を作成します。
2. Provider endpoint、モデル、必要な認証情報をローカルまたは管理画面で設定します。
3. Workspace、Project、Session を作成し、会話画面からタスクを依頼します。

実際の設定、実行データ、認証情報はバージョン管理に含めないでください。設計の詳細は以下の文書を参照してください。

## ドキュメント

- [アーキテクチャ](docs/ai-agent/ARCHITECTURE.md)
- [開発ガイド](docs/ai-agent/DEVELOPMENT.md)
- [HTTP API](docs/ai-agent/HTTP_API.md)
- [セキュリティ](docs/ai-agent/SECURITY.md)
- [OpenAPI 契約](src/transport/httpapi/openapi.yaml)

リリースのパッケージ化と署名はメンテナー専用の内部手順で行います。この README ではパッケージ化コマンド、パラメーター、署名設定を公開しません。

## データセキュリティ

- Repository に含まれるのはサンプル設定だけです。Provider キー、SSH 認証情報、ログ、実行データ、リリース成果物は `.gitignore` で除外されます。
- 文書と例では相対パス、localhost、example domain のみを使用し、開発者固有の絶対ディレクトリを含めません。
- API Key、Token、秘密鍵、証明書、署名 ID、実サービスのアドレスを commit しないでください。

## ライセンス

本プロジェクトはデュアルライセンスです。

- オープンソース利用には [GNU General Public License v3.0 only](LICENSE) が適用されます。
- GPLv3 の条件が適さない商用利用については、[COMMERCIAL-LICENSE.md](COMMERCIAL-LICENSE.md) を参照してください。

第三者コンポーネントには、それぞれ同梱されているライセンスが適用されます。
