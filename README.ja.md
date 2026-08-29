# Tanpopo

[繁體中文](README.md) · [English](README.en.md) · [日本語](README.ja.md) · [한국어](README.ko.md)

Tanpopo は Go で実装されたローカルモデルサービス管理ツールです。名称は日本語の「蒲公英（たんぽぽ）」に由来し、生成された Token が種のように外へ広がるイメージを表しています。GGUF 向けのクロスプラットフォーム `llama-server` と、Apple Silicon 向けのネイティブ Swift／MLX `mlx-server` を管理できます。

## 主な機能

- モデル Runtime の起動、停止、状態復元、ログ確認を 1 つの管理画面で実行。
- サブフォルダーを含む GGUF と完全な MLX モデルフォルダーを自動検出。
- Hugging Face の公開、gated、private repository から GGUF または MLX モデルをダウンロード。
- 外部 JSON カタログによる常用モデルのクイック選択。GGUF と MLX を分けて表示し、Runtime、repository、revision、GGUF ファイル名を自動入力。モデル情報は JavaScript に固定しません。
- Server が Range をサポートする場合、大きなファイルを 64 MiB 単位、最大 4 Worker で並行ダウンロードし、非対応時は単一 Stream に自動で戻します。完了項目は自動的に消去されます。保存先を開く操作は Desktop App のみ有効で、Browser では無効です。
- Context Size、GPU Layers、Threads、KV Cache、MTP、DFlash の起動プロファイルを保存。
- DFlash の対応状況を検出し、有効化前に互換性のある Draft モデルの存在を確認。
- Hugging Face metadata とモデル設定を検証し、同一または別 repository の対応 DFlash Draft をモデル名の固定リストなしで自動検索・ダウンロード。
- Markdown、数式、reasoning 分離表示、待機アニメーション、Token 数、毎秒出力 Token 数に対応した一時的なローカルチャット。
- MLX から Token 単位で送信する OpenAI 互換 SSE。クライアントの切断または Cancel は対応する生成 Task を直ちに停止。
- モデル API にアクセスキー、IP 許可リスト、両方、または制限なしを設定可能。
- Tanpopo 再起動時、管理画面へログインする前に Runtime と稼働状態を復元。
- 管理画面は `AUTO`、繁体字中国語、英語、日本語、韓国語に対応。
- 蒲公英、晴空ブルー、桜ピンク、ダークテーマの深夜パープルという 4 種類の配色を選択可能。
- 画面下部の状態バーで CPU、GPU、MEMORY、ネットワークを 3 秒ごとに更新し、50%／80% を境に低彩度の緑・黄・赤で表示。
- 「システム設定 → システム情報」に OS、Kernel、Architecture、Host 名、CPU、GPU、Memory、Network interface、到達可能な管理 URL を読み取り専用で表示。Loopback URL は共有用一覧に表示しません。
- 起動時と 1 時間ごとに GitHub の最新正式 Release を確認し、更新がある場合は通知。「システム設定 → このアプリについて」では現在のバージョン、手動更新確認、クリック可能な GitHub URL を表示。
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

初期のローカル認証情報は `agent.sample.properties` から作成され、初回起動のみを想定しています。LAN 公開または Reverse Proxy の有効化前に必ず変更してください。システム設定では、確認後に管理画面ログインを無効化することもできます。再び認証を有効化できるよう、認証情報はローカルだけに保持されます。

macOS の常駐モードを有効にすると、Tanpopo がシステムメニューバーに表示されます。ウィンドウを閉じても UI のみ非表示になり、完全に停止するにはメニューの「Tanpopo を終了」を使用します。常駐モードの既定値はオフです。

```bash
TANPOPO_UI=shell ./run.command  # Shell モードを強制
TANPOPO_UI=gui ./run.command    # 対応環境でネイティブ UI を強制
```

アプリのバージョンは `1.YY.MMDD build HHmm` 形式（例：`1.26.0829 build 1430`）です。ルートの `VERSION` には GitHub Release の比較に使う `1.YY.MMDD` のみを記録し、ビルド時刻から生成した `build HHmm` はビルド／起動スクリプトが実行ファイルへ埋め込みます。Tanpopo は起動直後と、その後 1 時間ごとに GitHub の最新正式 Release を確認し、同じ UI セッションでは同じ新バージョンを一度だけ通知します。「システム設定 → このアプリについて」では現在・最新バージョン、最終確認時刻、手動確認ボタン、クリック可能な [GitHub リポジトリ URL](https://github.com/VaderChen/Tanpopo) を表示します。更新確認では `1.YY.MMDD` のみを比較し、Draft と prerelease は最新版として扱いません。

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

## NetPass Reverse Proxy

NetPass は管理者ログイン認証と Model API Access Key 認証の両方が有効な場合だけ開始できます。有効化操作時に前提条件を確認し、多言語の利用方針・責任説明を表示します。利用者が内容を読んだことを明示的に確認してから、Reverse Proxy を開始できます。接続後は割り当てられた公開 NetPass URL を表示し、本機の管理画面と API 呼び出しが公共ネットワークから到達可能になります。

NetPass は Tanpopo と Mars Semi Corp. の技術協力による、技術交流および実験用途のサービスです。現在は無償で提供されていますが、Mars Semi Corp. は利用方針を随時変更でき、重要な変更は別途告知します。利用者は必要性を評価し、適切な安全設定を行い、ネットワーク上のリスクを負うものとします。本サービスの使用に起因する Security incident、Data exposure、その他の損失について、Mars Semi Corp. と Tanpopo は責任を負いません。

NetPassClient は独立した Closed-source component です。その Source、Binary、Credential、正式な Packaging process は本 Repository に含まれず、GitHub に同期されません。公式署名済み Installer には、管理された Platform-specific binary が含まれる場合があります。Server API Key は権限を制限したローカル設定にのみ保存され、Management API が平文を返すことはありません。

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

[LICENSE](LICENSE)、[COMMERCIAL-LICENSE.md](COMMERCIAL-LICENSE.md)、[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) を参照してください。Security report とローカル秘密情報の扱いは [SECURITY.md](SECURITY.md) に記載しています。
