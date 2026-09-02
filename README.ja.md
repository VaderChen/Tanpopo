# Tanpopo

[English](README.md) · [繁體中文](README.zh-TW.md) · [日本語](README.ja.md) · [한국어](README.ko.md)

Tanpopo は Go で実装されたローカルモデルサービス管理ツールです。名称は日本語の「蒲公英（たんぽぽ）」に由来し、生成された Token が種のように外へ広がるイメージを表しています。GGUF 向けのクロスプラットフォーム `llama-server` と、Apple Silicon 向けのネイティブ Swift／MLX `mlx-server` を管理できます。

## 主な機能

- モデル Runtime の起動、停止、状態復元、ログ確認を 1 つの管理画面で実行。
- サブフォルダーを含む GGUF と完全な MLX モデルフォルダーを自動検出。GGUF、MLX、mmproj はアルファベット順で「モデル名（ディレクトリ名）」と表示し、内部では完全な安全パスを保持します。macOS 以外では Apple Silicon 専用の `mlx-server` を表示しません。
- **Fast GGUF モード**はモデル名に依存しない tensor 規則を使用し、Mode 1（バランス・既定）、Mode 2（高精度）、Mode 3（最速）の三戦略を提供します。
- mlx-server のモデルを GGUF／MLX に分類。Runtime がまだ対応を明示していない言語モデルも選択可能な「未テスト（N）」グループに残し、起動時の実際の読み込みで互換性を確認します。
- Hugging Face の公開、gated、private repository から GGUF または MLX モデルをダウンロード。
- 外部 JSON カタログによる常用モデルのクイック選択。GGUF と MLX をそれぞれ 8B クラス、30B クラス、70B 以上に分類し、各グループ内ではモデル名のアルファベット順に表示します。Runtime、repository、revision、GGUF ファイル名を自動入力し、モデル情報は JavaScript に固定しません。
- Server が Range をサポートする場合、大きなファイルを 64 MiB 単位、最大 4 Worker で並行ダウンロードし、非対応時は単一 Stream に自動で戻します。完了項目は自動的に消去されます。保存先を開く操作は Desktop App のみ有効で、Browser では無効です。
- Context Size、GPU Layers、Threads、KV Cache、MTP、DFlash の起動プロファイルを保存。Apple Silicon では MLX DFlash と MLX MTP も利用できます。
- 独立した DFlash と MMap スイッチをコンパクトな「詳細設定」ポップオーバーにまとめ、項目が増えてもページが縦に伸び続けない構成。
- DFlash の対応状況を検出し、有効化前に互換性のある Draft モデルの存在を確認。独立した MMap スイッチは llama-server と Apple Silicon mlx-server の両方に対応し、ファイルバックページでモデル読み込み時のメモリ負荷を抑えます。
- KV Cache、MMap、DFlash、MTP は、機能既定値、起動 Profile、互換性事前検査、Runtime 引数、状態保存、エラー表示まで一貫して統合されています。推測デコード同士、および KV Cache 量子化との同時利用はできません。
- Hugging Face metadata とモデル設定を検証し、同一または別 repository の対応 DFlash Draft をモデル名の固定リストなしで自動検索・ダウンロード。
- Markdown、数式、reasoning API フィールド、`<think>`、`<|channel|>` 思考チャンネルの分離表示に対応した一時的なローカルチャット。思考過程は生成中に既定で展開され、三点アニメーションを表示し、生成完了後に自動で折りたたまれます。Token 数と毎秒出力 Token 数も表示します。
- モデルテストは単回、3 回反復、500 出力 Token 以上の長文テストに対応し、反復テストでは平均速度と中央値を表示します。
- MLX から Token 単位で送信する OpenAI 互換 SSE。クライアントの切断または Cancel は対応する生成 Task を直ちに停止。
- モデル API にアクセスキー、IP 許可リスト、両方、または制限なしを設定可能。
- Tanpopo 再起動時、管理画面へログインする前に Runtime と稼働状態を復元。
- 管理画面は `AUTO`、繁体字中国語、英語、日本語、韓国語に対応。
- 蒲公英、晴空ブルー、桜ピンク、ダークテーマの深夜パープルという 4 種類の配色を選択可能。
- 画面下部の状態バーで CPU、GPU、MEMORY、ネットワークを 3 秒ごとに更新し、50%／80% を境に低彩度の緑・黄・赤で表示。
- 「システム設定 → システム情報」に OS、Kernel、Architecture、Host 名、CPU、GPU、Memory、Network interface、到達可能な管理 URL を読み取り専用で表示。Loopback URL は共有用一覧に表示しません。
- 起動時と 1 時間ごとに GitHub の最新正式 Release を確認し、同日 Release の build 番号も比較します。Linux では認証済み管理者が正式 ZIP をアップロードし、検証、更新、再起動を自動実行できます。
- Linux パッケージは Vulkan 対応 llama.cpp のビルド経路、依存関係と GPU 権限の確認、`build-llama-server.sh`、ROCm がない場合の DRM GPU 使用率取得を含みます。
- macOS では AppKit／WKWebView のネイティブ UI とメニューバー常駐モードを提供。

## 公開テストレポート

- [モデル互換性レポート](https://vaderchen.github.io/Tanpopo/reports/model-compatibility.html)：ネイティブ MLX、MLX による GGUF 読み込み、llama.cpp GGUF、マルチモーダル投影、KV Cache 量子化、推測デコードの対応範囲と互換性境界をまとめています。
- [MLX と GGUF 変換の読み込み速度および演算精度](https://vaderchen.github.io/Tanpopo/reports/performance-comparison.html)：4B、9B、27B の対応モデルを、ネイティブ MLX、llama.cpp + GGUF、MLX + Fast GGUF Mode 1／2／3 の固定 100 問、生成速度、Fast GGUF 容量、プロセス RAM で比較します。

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

アプリ版は `1.YY.MMDD build HHmm`、GitHub Tag は `v1.YY.MMDD-build-HHmm` です。更新確認は日付版と build 番号の両方を比較するため、同日の後続 Release も検出します。Draft と prerelease は最新版として扱いません。

## モデル Runtime

### llama-server

`llama-server` は GGUF と複数プラットフォームに対応します。既定の GGUF フォルダーは `~/services/models` です。起動時に選択したモデルと保存済みプロファイルを組み合わせます。マルチモーダルモデルでは対応する `mmproj` を選択できます。

DFlash は既定でオフです。アーキテクチャとペア情報を検証し、必要な Draft GGUF が存在しない場合は有効化を取り消してダウンロードを案内します。

MMap は実行状態ページの「詳細設定」ポップオーバーにある独立したスイッチで、既定ではオフです。`llama-server` と Apple Silicon の `mlx-server` の両方に対応します。llama-server は `--load-mode mmap` を使用し、mlx-server は対応する safetensors と直接利用可能な GGUF ウェイトをファイルバックページとしてマッピングします。起動プロファイルでは、自動または 4、8、16、24、32、48、64、96、128 GB のメモリ予約目標を選択できます。llama-server は `--fit-target` で GPU Layers を構成し、mlx-server は物理メモリから予約目標を引いた値を MLX の割り当て目標にします。この値は Runtime の厳密なメモリ上限ではありません。最初の Token の待ち時間と生成速度はストレージ性能とページ負荷に左右されます。

起動プロファイルでは KV Cache の Q8 または Q4 を選択でき、「詳細設定」の独立したスイッチで今回の起動に適用するかを決めます。スイッチをオフにすると量子化しません。Q4 はより省メモリで、Q8 はより高い精度を維持します。KV Cache 量子化と DFlash は UI とバックエンドの両方で排他的です。

### mlx-server

`mlx-server` は Swift、SwiftNIO、MLX Swift で構築された Apple Silicon 専用 Runtime です。Python、pip、`mlx_lm.server` は使用しません。`~/services/mlx-models` の完全な MLX モデルに加え、通常の GGUF フォルダーにある対応 GGUF を直接読み込めます。ネイティブ MLX の対応型は内蔵 `mlx-swift-lm 3.31.4` の Registry から動的に取得し、マルチモーダル Gemma 4 を含みます。Runtime が現在報告する GGUF 直接読み込み対象は Gemma、Llama、Mimo、MiniCPM、Mistral、Qwen 2、Qwen 3、Qwen 3.5、SmolLM3 で、未対応の検出済み言語モデルは無効状態で表示します。マルチモーダル Qwen 3.5 GGUF では対応する `mmproj` を選択できます。

**Fast GGUF モード**は mlx-server の汎用 GGUF 最適化入口で、GGUF 選択時に既定で有効です。システム設定の独立した「Fast GGUF 戦略」カードで、既定スイッチと三つの戦略を保存します。

- **Mode 1（バランス・既定）**：K-Quant の元 4-bit block を必要な Group 32 で再利用し、その他は Group 64 を使用します。
- **Mode 2（高精度）**：低 bit の元ウェイトを INT8／Group 64 に再量子化します。
- **Mode 3（最速）**：低 bit の元ウェイトを INT4／Group 32 に再量子化し、手動 Group Size の影響を受けません。

Fast GGUF を無効にすると、一般の `auto + group auto + recurrent off` 変換を使用します。すべての戦略はモデル名ではなく tensor dtype、shape、source block、architecture metadata で判定します。Q4_K の 32 要素 Group は source sub-block 形式で定義され、全体を Group 32 にする意味ではありません。`quality` は FP32 参照ウェイトを使う診断用で、通常の性能モードではありません。

この戦略はモデル別の特例なしで適用されます。DFlash は互換 MLX Target／Draft を必要とし、MTP は互換するネイティブ MLX Target／Draft、または metadata と tensor 契約で内蔵予測層を確認できる GGUF を使用します。判定はファイル名ではなく architecture metadata と shape に基づきます。

mlx-server で KV Cache 量子化を有効にすると、プロファイルの Q8 または Q4、group size 64、2,048 Token 後からの遅延量子化を使用します。プロファイルの Context Size が量子化 KV Cache の上限になります。

KV Cache、MMap、DFlash、MTP は手動の隠しフラグではなく、Tanpopo の第一級機能です。Backend は互換性と排他条件を検証し、Runtime 引数、状態保存、エラー表示まで処理します。

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

## 設定の自動保存とモデル管理

システム設定のスイッチ、プルダウン、配色は変更後に自動保存されます。既存の保存ボタンは残し、テキスト欄は手動保存します。保存は順番に処理し、変更した項目と必要な排他設定だけを送信するため、入力途中のテキストは送信・消去しません。失敗時は選択を戻してエラーを表示します。危険な操作の確認は維持し、ログイン認証の再有効化には新しいパスワードと確認入力が必要です。「Access Token を消去」は確認後すぐに保存済み Token を消去するボタンです。

自動保存の対象はシステム設定です。モデル、ダウンロード、性能校正、Reverse Proxy を自動開始せず、別ページの起動プロファイル編集も自動保存しません。NetPass の利用方針は接続ごとに明示的な確認が必要です。

### 性能校正とメモリ圧力保護

「モデル取得元と設定」の性能校正は新規インストールで既定で有効です。明示的に保存した無効設定は維持します。有効化は手動校正への入口と保存済み結果の適用を許可するもので、初回起動時に自動測定はしません。

実行状態の「性能校正」はモデル未読み込みでも開けます。複数選択と「すべて／GGUF／MLX」の絞り込みに対応し、読み込み済みのモデルだけを事前選択します。絞り込みで選択は消えません。通常の GGUF は llama-server、ネイティブ MLX と Fast GGUF fallback は mlx-server を優先し、互換性のある読み込み済み設定を基準として再利用します。

各モデルで 3 設定を各 3 回測定し、個別の進捗と完了チェックを表示します。測定中の百分率が得られない場合は不定進捗表示を使用します。結果には各速度、平均、中央値、改善率、推奨設定を表示し、中央値が最良の設定を保存します。同じハードウェア、Runtime、モデルパス、起動引数の次回起動で自動適用します。設定比較では再起動が必要な場合があります。終了後は元のモデルまたは未読み込み状態に戻します。結果は測定時の用途に依存します。

実験設定のメモリ圧力保護は既定で無効で、変更は永続化されます。起動前にモデルと付属ファイルの必要量を推定し、最低 2 GiB または物理 RAM の 8% を確保します。必要に応じて Context、Batch／Prefill を縮小し、MTP／DFlash を無効化し、それでも不足する場合は起動を拒否します。実際の調整を画面に表示します。実行時のメモリ上限や常時 OOM 監視ではありません。

### Fast GGUF と元ファイルの削除

実験設定の「変換後に元の GGUF を削除」は既定で無効です。確認後、Fast GGUF の shard、manifest、独立起動用資産、正常な読み込みを検証してから元ファイルを削除します。元の GGUF がなくても、完全な Fast GGUF は GGUF 一覧の fallback として mlx-server で再読み込みできます。新しい変換は schema 4、schema 3 は起動用資産が必要で、schema 2 は fallback 非対応です。

Fast GGUF は Apple Silicon 内部形式で、llama.cpp 用 GGUF ではありません。fallback の内蔵 MTP は非対応です。llama.cpp の利用や再変換には元の GGUF が必要です。元ファイルと最後の Fast GGUF を両方削除するとモデルを読み込めなくなります。詳細は [Fast GGUF 形式](mlx-server/FGGUF-FORMAT.md) を参照してください。

### Repository 検索とお気に入り

ダウンロード画面でキーワードと GGUF／MLX 形式を指定して Repository を検索し、ダウンロード数、いいね数、名前順に並べ替えられます。「選択」で Repository と Revision `main` を入力してダイアログを閉じます。同じページで再度開くと検索語と結果を保持しますが、ページ再読み込みをまたぐ永続履歴ではありません。

GGUF ファイル名は Repository／Revision のスキャン結果から選択します。既定は `Q4_0`、存在しない場合はソート順の先頭で、`Q4_K_M` 固定ではありません。更新ボタンで再スキャンし、既存の選択が残っていれば維持します。お気に入りボタンと星は形式、Repository、Revision を保存します。解除は確認が必要で、内蔵項目は通常一覧へ戻り、手動項目はお気に入りから削除されます。ダウンロード済みモデルは削除しません。

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
