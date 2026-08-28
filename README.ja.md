# Tanpopo

[繁體中文](README.md) · [English](README.en.md) · [日本語](README.ja.md) · [한국어](README.ko.md)

Tanpopo は Go で実装されたローカルモデルサービス管理ツールです。名称は日本語の「蒲公英（たんぽぽ）」に由来し、生成された Token が種のように外へ広がるイメージを表しています。GGUF 向けのクロスプラットフォーム `llama-server` と、Apple Silicon 向けのネイティブ Swift／MLX `mlx-server` を管理できます。

## 主な機能

- モデル Runtime の起動、停止、状態復元、ログ確認を 1 つの管理画面で実行。
- サブフォルダーを含む GGUF と完全な MLX モデルフォルダーを自動検出。
- Hugging Face の公開、gated、private repository から GGUF または MLX モデルをダウンロード。
- Context Size、GPU Layers、Threads、KV Cache、MTP、DFlash の起動プロファイルを保存。
- DFlash の対応状況を検出し、有効化前に互換性のある Draft モデルの存在を確認。
- Markdown、数式、reasoning 分離表示、待機アニメーション、Token 数、毎秒出力 Token 数に対応した一時的なローカルチャット。
- MLX から Token 単位で送信する OpenAI 互換 SSE。クライアントの切断または Cancel は対応する生成 Task を直ちに停止。
- モデル API にアクセスキー、IP 許可リスト、両方、または制限なしを設定可能。
- Tanpopo 再起動時、管理画面へログインする前に Runtime と稼働状態を復元。
- 管理画面は `AUTO`、繁体字中国語、英語、日本語、韓国語に対応。
- macOS では AppKit／WKWebView のネイティブ UI とメニューバー常駐モードを提供。

## クイックスタート

開発には Go 1.25 以降、CMake、C/C++ ツールチェーンが必要です。Apple Silicon で `mlx-server` をビルドする場合は Swift 6 と Xcode も必要です。

```bash
cd /path/to/Tanpopo
./run.command
```

初回起動時に `agent.sample.properties` から `agent.properties` を作成します。管理サービスの既定値は `0.0.0.0:10082` です。

```text
http://127.0.0.1:10082
```

既定の管理者アカウント：

```text
アカウント: root
パスワード: root
```

環境設定から認証情報を変更できます。確認ダイアログの後に管理画面ログインを無効化することもできます。認証を再度有効にできるよう、元の認証情報はローカルに保持されます。

macOS の常駐モードを有効にすると、Tanpopo がシステムメニューバーに表示されます。ウィンドウを閉じても UI のみ非表示になり、完全に停止するにはメニューの「Tanpopo を終了」を使用します。常駐モードの既定値はオフです。

```bash
TANPOPO_UI=shell ./run.command  # Shell モードを強制
TANPOPO_UI=gui ./run.command    # 対応環境でネイティブ UI を強制
```

## モデル Runtime

### llama-server

`llama-server` は GGUF と複数プラットフォームに対応します。既定の GGUF フォルダーは `~/services/models` です。起動時に選択したモデルと保存済みプロファイルを組み合わせます。マルチモーダルモデルでは対応する `mmproj` を選択でき、MLX 選択時には mmproj 項目を表示しません。

DFlash は既定でオフです。アーキテクチャとペア情報を検証し、必要な Draft GGUF が存在しない場合は有効化を取り消してダウンロードを案内します。

### mlx-server

`mlx-server` は Swift、SwiftNIO、MLX Swift で構築された Apple Silicon 専用 Runtime です。Python、pip、`mlx_lm.server` は使用しません。既定の MLX モデルフォルダーは `~/services/mlx-models` で、有効なモデルには `config.json` と safetensors が必要です。

主な互換エンドポイント：

```text
GET  /health
GET  /v1/health
GET  /models
GET  /v1/models
POST /v1/chat/completions
POST /v1/completions
POST /completion
```

Runtime 状態に表示される Base URL（通常 `http://127.0.0.1:8080/v1`）を使用してください。`/models` と `/v1/models` は現在ロード中の正しい Model ID を返します。

`/v1/chat/completions` で `stream: true` を指定すると、生成中に Token 単位の OpenAI 互換 SSE を返します。HTTP Channel が閉じると producer Task と MLX 生成ストリームもキャンセルされます。

## API セキュリティ

アクセスキーと IP 許可リストは独立して有効化できます。両方を有効にした場合は両方の検証に合格する必要があります。キーは Bearer または `X-OpenLoader-Key` で送信できます。キーの平文は発行時に一度だけ表示され、保存されるのは SHA-256 ハッシュだけです。

IP 許可リストは IPv4、IPv6、CIDR、ワイルドカード接尾辞、`*` に対応します。有効なポリシーのスナップショットが欠落または破損している場合、Runtime は fail-closed で拒否します。

## ローカルデータ

- `data/settings.json`: 一般設定と言語。原子的に保存。
- `data/runtime_state.json`: Runtime、モデル、起動プロファイル、希望する稼働状態。
- `agent.properties`: 管理画面の認証設定。
- API セキュリティファイル: ポリシーとキーのハッシュ。

チャット内容は保存されません。

## ビルド

```bash
go build -buildvcs=false -trimpath -o bin/Tanpopo ./src/cmd/llamaloader
./scripts/build-mlx-server-runtime.sh  # Apple Silicon のみ
```

## ライセンス

[LICENSE](LICENSE)、[COMMERCIAL-LICENSE.md](COMMERCIAL-LICENSE.md)、[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) を参照してください。
