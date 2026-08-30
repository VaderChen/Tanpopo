# Tanpopo

[繁體中文](README.md) · [English](README.en.md) · [日本語](README.ja.md) · [한국어](README.ko.md)

Tanpopo は Go で実装されたローカルモデルサービス管理ツールです。名称は日本語の「蒲公英（たんぽぽ）」に由来し、生成された Token が種のように外へ広がるイメージを表しています。GGUF 向けのクロスプラットフォーム `llama-server` と、Apple Silicon 向けのネイティブ Swift／MLX `mlx-server` を管理できます。

## 主な機能

- モデル Runtime の起動、停止、状態復元、ログ確認を 1 つの管理画面で実行。
- サブフォルダーを含む GGUF と完全な MLX モデルフォルダーを自動検出。Apple Silicon の `mlx-server` は、safetensors への事前変換なしで対応 GGUF を直接読み込めます。
- **Fast GGUF モード**は `mlx-server` で GGUF を選択したとき既定で有効になり、モデル名に依存しない tensor 規則で INT4、INT8、BF16、group size、recurrent controls を選択し、変換済みウェイトを `.fgguf` に永続保存します。mlx-server が解析できる多くの GGUF に有効ですが、すべてのアーキテクチャ、量子化形式、独自 checkpoint の読み込み、加速、同一精度を保証するものではありません。
- mlx-server のモデルを GGUF／MLX に分類。Runtime がまだ対応を明示していない言語モデルも選択可能な「未テスト（N）」グループに残し、起動時の実際の読み込みで互換性を確認します。
- Hugging Face の公開、gated、private repository から GGUF または MLX モデルをダウンロード。
- 外部 JSON カタログによる常用モデルのクイック選択。GGUF と MLX をそれぞれ 8B クラス、30B クラス、70B 以上に分類し、各グループ内ではモデル名のアルファベット順に表示します。Runtime、repository、revision、GGUF ファイル名を自動入力し、モデル情報は JavaScript に固定しません。
- Server が Range をサポートする場合、大きなファイルを 64 MiB 単位、最大 4 Worker で並行ダウンロードし、非対応時は単一 Stream に自動で戻します。完了項目は自動的に消去されます。保存先を開く操作は Desktop App のみ有効で、Browser では無効です。
- Context Size、GPU Layers、Threads、KV Cache、MTP、DFlash の起動プロファイルを保存。
- 独立した DFlash と MMap スイッチをコンパクトな「詳細設定」ポップオーバーにまとめ、項目が増えてもページが縦に伸び続けない構成。
- DFlash の対応状況を検出し、有効化前に互換性のある Draft モデルの存在を確認。独立した MMap スイッチは llama-server と Apple Silicon mlx-server の両方に対応し、ファイルバックページでモデル読み込み時のメモリ負荷を抑えます。
- KV Cache、MMap、DFlash は、機能既定値、起動 Profile、実行スイッチ、互換性事前検査、Runtime 引数、状態保存、エラー表示まで一貫して統合されています。実際の利用可否は Runtime とモデルに依存し、DFlash には互換 Target／Draft が必要で、KV Cache 量子化とは同時に有効化できません。
- Hugging Face metadata とモデル設定を検証し、同一または別 repository の対応 DFlash Draft をモデル名の固定リストなしで自動検索・ダウンロード。
- Markdown、数式、reasoning 分離表示に対応した一時的なローカルチャット。思考過程は生成中に既定で展開され、三点アニメーションを表示し、生成完了後に自動で折りたたまれます。Token 数と毎秒出力 Token 数も表示します。
- モデル起動時には、大規模モデルで変換が必要な場合があることを知らせる読み込みダイアログを直ちに表示します。実行中モデルのテストではアニメーションダイアログを表示し、完了後に入出力 Token、生成速度、所要時間、または読み込み／接続エラーを表示。メイン画面の再読み込みには別の小型進捗ダイアログを使用します。
- MLX から Token 単位で送信する OpenAI 互換 SSE。クライアントの切断または Cancel は対応する生成 Task を直ちに停止。
- モデル API にアクセスキー、IP 許可リスト、両方、または制限なしを設定可能。
- Tanpopo 再起動時、管理画面へログインする前に Runtime と稼働状態を復元。
- 管理画面は `AUTO`、繁体字中国語、英語、日本語、韓国語に対応。
- 蒲公英、晴空ブルー、桜ピンク、ダークテーマの深夜パープルという 4 種類の配色を選択可能。
- 画面下部の状態バーで CPU、GPU、MEMORY、ネットワークを 3 秒ごとに更新し、50%／80% を境に低彩度の緑・黄・赤で表示。
- 「システム設定 → システム情報」に OS、Kernel、Architecture、Host 名、CPU、GPU、Memory、Network interface、到達可能な管理 URL を読み取り専用で表示。Loopback URL は共有用一覧に表示しません。
- 起動時と 1 時間ごとに GitHub の最新正式 Release を確認し、更新がある場合は通知。「システム設定 → このアプリについて」では現在のバージョン、手動更新確認、クリック可能な GitHub URL を表示。
- macOS では AppKit／WKWebView のネイティブ UI とメニューバー常駐モードを提供。

## 公開テストレポート

- [モデル互換性レポート](https://vaderchen.github.io/Tanpopo/reports/model-compatibility.html)：ネイティブ MLX、MLX による GGUF 読み込み、llama.cpp GGUF、マルチモーダル投影、KV Cache 量子化、推測デコードの対応範囲と互換性境界をまとめています。
- [性能・精度レポート](https://vaderchen.github.io/Tanpopo/reports/performance-comparison.html)：3 組の対応モデルでネイティブ MLX、Fast GGUF オフ、Fast GGUF オン、llama+GGUF を比較し、生成速度、固定データセット精度、テスト環境、再測定条件を公開しています。

両 HTML レポートは `AUTO`、繁体字中国語、英語を切り替えられます。結果は記載された日付、ハードウェア、Runtime バージョン、サンプルでの再現可能なスナップショットであり、すべてのモデルやデバイスで同じ結果になること、また Fast GGUF の互換性、速度、精度を保証するものではありません。

## クイックスタート

開発には Go 1.25 以降、CMake、C/C++ ツールチェーンが必要です。Apple Silicon で `mlx-server` をビルドする場合は Swift 6 と Xcode も必要です。

```bash
cd /path/to/Tanpopo
./run.command
```

`run.command` は最初に `build.command --runtime` を呼び出します。固定バージョンが一致し、Runtime のソースが prebuilt 実行ファイルより新しくない場合だけ既存 Runtime を再利用し、それ以外は自動的に再ビルドします。

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

アプリのバージョンは `1.YY.MMDD build HHmm` 形式です。`run.command`、`run.sh`、`build.command`、`pack.command` はすべて `Asia/Taipei` の当日から `1.YY.MMDD`、実行時刻から `build HHmm` を直接生成します。ルートの `VERSION` を読み取り、検証、変更しないため、日付をまたいだ実行や再パッケージでも手動の日付更新は不要です。過去版を意図して再作成する場合のみ `TANPOPO_VERSION` を、Build を固定する場合は `TANPOPO_BUILD` を指定してください。生成したパッケージには実際に使用した版号を書き込みます。ルートの `VERSION` はバージョン metadata と旧フロー互換のためだけに残ります。Tanpopo は起動直後と、その後 1 時間ごとに GitHub の最新正式 Release を確認し、同じ UI セッションでは同じ新バージョンを一度だけ通知します。「システム設定 → このアプリについて」では現在・最新バージョン、最終確認時刻、手動確認ボタン、クリック可能な [GitHub リポジトリ URL](https://github.com/VaderChen/Tanpopo) を表示します。更新確認では `1.YY.MMDD` のみを比較し、Draft と prerelease は最新版として扱いません。

## モデル Runtime

### llama-server

`llama-server` は GGUF と複数プラットフォームに対応します。既定の GGUF フォルダーは `~/services/models` です。起動時に選択したモデルと保存済みプロファイルを組み合わせます。マルチモーダルモデルでは対応する `mmproj` を選択できます。

DFlash は既定でオフです。アーキテクチャとペア情報を検証し、必要な Draft GGUF が存在しない場合は有効化を取り消してダウンロードを案内します。

MMap は実行状態ページの「詳細設定」ポップオーバーにある独立したスイッチで、既定ではオフです。`llama-server` と Apple Silicon の `mlx-server` の両方に対応します。llama-server は `--load-mode mmap` を使用し、mlx-server は対応する safetensors と直接利用可能な GGUF ウェイトをファイルバックページとしてマッピングします。起動プロファイルでは、自動または 4、8、16、24、32、48、64、96、128 GB のメモリ予約目標を選択できます。llama-server は `--fit-target` で GPU Layers を構成し、mlx-server は物理メモリから予約目標を引いた値を MLX の割り当て目標にします。この値は Runtime の厳密なメモリ上限ではありません。最初の Token の待ち時間と生成速度はストレージ性能とページ負荷に左右されます。

起動プロファイルでは KV Cache の Q8 または Q4 を選択でき、「詳細設定」の独立したスイッチで今回の起動に適用するかを決めます。スイッチをオフにすると量子化しません。Q4 はより省メモリで、Q8 はより高い精度を維持します。KV Cache 量子化と DFlash は UI とバックエンドの両方で排他的です。

### mlx-server

`mlx-server` は Swift、SwiftNIO、MLX Swift で構築された Apple Silicon 専用 Runtime です。Python、pip、`mlx_lm.server` は使用しません。`~/services/mlx-models` の完全な MLX モデルに加え、通常の GGUF フォルダーにある対応 GGUF を直接読み込めます。ネイティブ MLX の対応型は内蔵 `mlx-swift-lm 3.31.4` の Registry から動的に取得し、マルチモーダル Gemma 4 を含みます。Runtime が現在報告する GGUF 直接読み込み対象は Gemma、Llama、Mimo、MiniCPM、Mistral、Qwen 2、Qwen 3、Qwen 3.5、SmolLM3 で、未対応の検出済み言語モデルは無効状態で表示します。マルチモーダル Qwen 3.5 GGUF では対応する `mmproj` を選択できます。

**Fast GGUF モード**は mlx-server の汎用 GGUF 最適化入口で、GGUF 選択時に既定で有効です。モデル名ではなく tensor の型と形状を検査し、浮動小数点ウェイトは BF16、Q8 は INT8、対応する低ビット行列は INT4 を使用します。group size は 64 を優先して必要時に 32 へ安全にフォールバックし、recurrent controls は高精度を維持します。`--gguf-group-size auto|32|64` と `--gguf-profile auto|quality|speed` で上書きでき、`quality` では Q5_K／Q6_K を INT8 にします。

この戦略はモデルごとの特例なしで、mlx-server が解析できる多くの GGUF に適用できますが、互換性、速度、精度を保証するものではありません。アーキテクチャ契約、GGUF metadata、Tokenizer、tensor layout、量子化方式、独自変更によっては、読み込み失敗、速度向上なし、品質差が発生します。異常時は Fast GGUF を無効にして比較し、実際の生成品質を検証してください。GGUF Target は通常の MLX 生成を使用し、DFlash は MLX safetensors Target と互換 Draft の組み合わせに限定されます。

mlx-server で KV Cache 量子化を有効にすると、プロファイルの Q8 または Q4、group size 64、2,048 Token 後からの遅延量子化を使用します。プロファイルの Context Size が量子化 KV Cache の上限になります。

KV Cache、MMap、DFlash は手動の隠しフラグではなく、Tanpopo の第一級機能です。システム設定でモデル機能の既定値を保存し、起動 Profile で詳細を定義し、実行画面で今回の読み込みだけ上書きできます。Backend は互換性と排他条件を検証し、Runtime 引数、状態保存、エラー表示まで処理します。ただし、すべてのモデルが全機能に対応する意味ではありません。KV Cache 形式と MMap は Runtime に依存し、DFlash には互換 Target／Draft が必要です。

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
