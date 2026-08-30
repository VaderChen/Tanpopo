(() => {
  const LANGUAGE_KEY = "tanpopo.uiLanguage";
  const THEME_KEY = "tanpopo.uiTheme";
  const byId = (id) => document.getElementById(id);

  // 介面以繁體中文作為字串來源，避免各頁面各自維護一套翻譯流程。
  const rows = [
    ["執行狀態", "Runtime", "実行状態", "실행 상태"],
    ["啟動參數", "Launch profiles", "起動プロファイル", "시작 프로필"],
    ["簡易對話", "Chat", "チャット", "간단 대화"],
    ["模型下載", "Model download", "モデルのダウンロード", "모델 다운로드"],
    ["系統設定", "System settings", "システム設定", "시스템 설정"],
    ["登出", "Sign out", "ログアウト", "로그아웃"],
    ["重新整理", "Refresh", "更新", "새로 고침"],
    ["正在重新整理", "Refreshing", "更新しています", "새로 고치는 중"],
    ["正在更新 Runtime、模型與日誌資料…", "Updating runtime, model, and log data…", "Runtime、モデル、ログ情報を更新しています…", "Runtime, 모델 및 로그 데이터를 업데이트하는 중…"],
    ["讀取中", "Loading", "読み込み中", "불러오는 중"],
    ["請稍候", "Please wait", "しばらくお待ちください", "잠시 기다려 주세요"],
    ["清除", "Clear", "消去", "지우기"],
    ["取消", "Cancel", "キャンセル", "취소"],
    ["新增", "Add", "追加", "추가"],
    ["刪除", "Delete", "削除", "삭제"],
    ["儲存參數", "Save profile", "プロファイルを保存", "프로필 저장"],
    ["傳送", "Send", "送信", "전송"],
    ["登入", "Sign in", "ログイン", "로그인"],
    ["帳號", "Account", "アカウント", "계정"],
    ["密碼", "Password", "パスワード", "비밀번호"],
    ["記住我", "Remember me", "ログイン状態を保持", "로그인 유지"],
    ["管理介面", "Admin console", "管理画面", "관리 화면"],
    ["歡迎回來", "Welcome back", "おかえりなさい", "다시 오신 것을 환영합니다"],
    ["使用本機設定檔中的管理帳號登入。", "Sign in with the administrator account stored in the local configuration.", "ローカル設定に保存された管理者アカウントでログインします。", "로컬 설정에 저장된 관리자 계정으로 로그인합니다."],
    ["帳號或密碼錯誤", "Incorrect account or password", "アカウントまたはパスワードが正しくありません", "계정 또는 비밀번호가 올바르지 않습니다"],
    ["讀取模型狀態中…", "Loading model status…", "モデルの状態を読み込んでいます…", "모델 상태를 불러오는 중…"],
    ["載入模型中…", "Loading model…", "モデルを読み込んでいます…", "모델을 불러오는 중…"],
    ["正在載入模型", "Loading model", "モデルを読み込んでいます", "모델을 불러오는 중"],
    ["部分模型過大且可能需要轉換，請耐心等候。", "Some models are large and may require conversion. Please wait patiently.", "一部のモデルは大きく、変換が必要な場合があります。しばらくお待ちください。", "일부 모델은 크기가 크고 변환이 필요할 수 있습니다. 잠시 기다려 주세요."],
    ["收合提示", "Hide notice", "通知を閉じる", "안내 닫기"],
    ["模型載入完成後即可測試。", "Testing will be available after the model finishes loading.", "モデルの読み込み完了後にテストできます。", "모델 로딩이 완료되면 테스트할 수 있습니다."],
    ["模型載入完成後即可開始對話。", "Chat will be available after the model finishes loading.", "モデルの読み込み完了後にチャットを開始できます。", "모델 로딩이 완료되면 대화를 시작할 수 있습니다."],
    ["模型服務仍在載入中，請稍候", "The model service is still loading. Please wait.", "モデルサービスを読み込み中です。しばらくお待ちください。", "모델 서비스를 아직 불러오는 중입니다. 잠시 기다려 주세요."],
    ["模型 Runtime 已停止或無法連線，請返回執行狀態確認", "The model runtime has stopped or cannot be reached. Check the Runtime page.", "モデル Runtime が停止したか接続できません。実行状態ページを確認してください。", "모델 Runtime이 중지되었거나 연결할 수 없습니다. 실행 상태 페이지를 확인하세요."],
    ["正在取得模型服務狀態…", "Retrieving model service status…", "モデルサービスの状態を取得しています…", "모델 서비스 상태를 가져오는 중…"],
    ["Runtime", "Runtime", "Runtime", "Runtime"],
    ["啟動參數", "Launch profile", "起動プロファイル", "시작 프로필"],
    ["模型", "Model", "モデル", "모델"],
    ["選擇 llama-server 支援的 GGUF 模型", "Select a GGUF model supported by llama-server", "llama-server 対応の GGUF モデルを選択", "llama-server가 지원하는 GGUF 모델 선택"],
    ["選擇 mlx-server 支援的 MLX 或 GGUF 模型", "Select an MLX or GGUF model supported by mlx-server", "mlx-server 対応の MLX または GGUF モデルを選択", "mlx-server가 지원하는 MLX 또는 GGUF 모델 선택"],
    ["模型清單讀取中…", "Loading model list…", "モデル一覧を読み込んでいます…", "모델 목록을 불러오는 중…"],
    ["選擇 mmproj（選用）", "Select mmproj (optional)", "mmproj を選択（任意）", "mmproj 선택(선택 사항)"],
    ["mmproj 清單讀取中…", "Loading mmproj list…", "mmproj 一覧を読み込んでいます…", "mmproj 목록을 불러오는 중…"],
    ["選擇模型後會自動檢查是否支援。", "Support is checked automatically after selecting a model.", "モデル選択後に対応状況を自動確認します。", "모델 선택 후 지원 여부를 자동으로 확인합니다."],
    ["使用配對的 Draft 模型預測多個 Token，提升生成速度。", "Uses a paired Draft model to predict multiple tokens and improve generation speed.", "対応する Draft モデルで複数の Token を予測し、生成速度を向上させます。", "호환되는 Draft 모델로 여러 Token을 예측해 생성 속도를 높입니다."],
    ["啟用", "On", "オン", "켜기"],
    ["進階設定", "Advanced settings", "詳細設定", "고급 설정"],
    ["設定 KV Cache 量化、MMap 與 DFlash。", "Configure KV Cache quantization, MMap, and DFlash.", "KV Cache 量子化、MMap、DFlash を設定します。", "KV Cache 양자화, MMap 및 DFlash를 설정합니다."],
    ["啟用 DFlash", "Enable DFlash", "DFlash を有効化", "DFlash 켜기"],
    ["將 GGUF 映射為可回收的檔案頁面，降低載入時的記憶體壓力；執行中用量不一定下降。", "Maps GGUF as reclaimable file-backed pages to reduce memory pressure while loading; runtime usage may not decrease.", "GGUF を再利用可能なファイルバックページとしてマッピングし、読み込み時のメモリ負荷を抑えます。実行中の使用量は減らない場合があります。", "GGUF를 회수 가능한 파일 기반 페이지로 매핑해 로딩 시 메모리 부담을 줄입니다. 실행 중 사용량은 줄지 않을 수 있습니다."],
    ["將模型權重映射為可回收的檔案頁面，降低載入時的記憶體壓力；執行中用量不一定下降。", "Maps model weights as reclaimable file-backed pages to reduce memory pressure while loading; runtime usage may not decrease.", "モデル重みを再利用可能なファイルバックページとしてマッピングし、読み込み時のメモリ負荷を抑えます。実行中の使用量は減らない場合があります。", "모델 가중치를 회수 가능한 파일 기반 페이지로 매핑해 로딩 시 메모리 부담을 줄입니다. 실행 중 사용량은 줄지 않을 수 있습니다."],
    ["啟用 MMap", "Enable MMap", "MMap を有効化", "MMap 켜기"],
    ["本次啟動已使用 MMap。", "MMap is enabled for this launch.", "今回の起動では MMap を使用しています。", "이번 실행에는 MMap을 사용합니다."],
    ["本次啟動未使用 MMap。", "MMap is not enabled for this launch.", "今回の起動では MMap を使用していません。", "이번 실행에는 MMap을 사용하지 않습니다."],
    ["KV Cache 量化", "KV Cache quantization", "KV Cache 量子化", "KV Cache 양자화"],
    ["啟用 KV Cache 量化", "Enable KV Cache quantization", "KV Cache 量子化を有効化", "KV Cache 양자화 켜기"],
    ["降低長 Context 的 KV Cache 記憶體用量；量化格式由啟動參數決定。", "Reduces KV Cache memory usage for long contexts; the launch profile selects the quantization format.", "長い Context の KV Cache メモリ使用量を削減します。量子化形式は起動プロファイルで選択します。", "긴 Context의 KV Cache 메모리 사용량을 줄이며 양자화 형식은 시작 프로필에서 선택합니다."],
    ["KV Cache 量化已啟用；兩者不可同時使用。", "KV Cache quantization is enabled; the two features cannot be used together.", "KV Cache 量子化が有効です。両方を同時には使用できません。", "KV Cache 양자화가 켜져 있어 두 기능을 동시에 사용할 수 없습니다."],
    ["DFlash 已啟用；兩者不可同時使用。", "DFlash is enabled; the two features cannot be used together.", "DFlash が有効です。両方を同時には使用できません。", "DFlash가 켜져 있어 두 기능을 동시에 사용할 수 없습니다."],
    ["本次啟動未使用 KV Cache 量化。", "KV Cache quantization is not enabled for this launch.", "今回の起動では KV Cache 量子化を使用していません。", "이번 실행에는 KV Cache 양자화를 사용하지 않습니다."],
    ["本次啟動已使用 KV Cache Q8。", "KV Cache Q8 is enabled for this launch.", "今回の起動では KV Cache Q8 を使用しています。", "이번 실행에는 KV Cache Q8을 사용합니다."],
    ["本次啟動已使用 KV Cache Q4。", "KV Cache Q4 is enabled for this launch.", "今回の起動では KV Cache Q4 を使用しています。", "이번 실행에는 KV Cache Q4를 사용합니다."],
    ["已開啟；本次將使用 Q8。", "Enabled; this launch will use Q8.", "有効です。今回は Q8 を使用します。", "켜짐. 이번 실행에는 Q8을 사용합니다."],
    ["已開啟；本次將使用 Q4。", "Enabled; this launch will use Q4.", "有効です。今回は Q4 を使用します。", "켜짐. 이번 실행에는 Q4를 사용합니다."],
    ["可用格式：Q8（預設關閉）。", "Available format: Q8 (off by default).", "使用可能な形式：Q8（既定ではオフ）。", "사용 가능한 형식: Q8(기본값 꺼짐)."],
    ["可用格式：Q4（預設關閉）。", "Available format: Q4 (off by default).", "使用可能な形式：Q4（既定ではオフ）。", "사용 가능한 형식: Q4(기본값 꺼짐)."],
    ["請先選擇 GGUF 模型。", "Select a GGUF model first.", "先に GGUF モデルを選択してください。", "먼저 GGUF 모델을 선택하세요."],
    ["已開啟；速度會受儲存裝置及分頁壓力影響。", "Enabled. Speed depends on storage performance and paging pressure.", "有効です。速度はストレージ性能とページング負荷に左右されます。", "켜짐. 속도는 저장 장치 성능과 페이징 부하에 따라 달라집니다."],
    ["預設關閉。", "Off by default.", "既定ではオフです。", "기본값은 꺼짐입니다."],
    ["本次啟動未使用 DFlash。", "DFlash is not enabled for this launch.", "今回の起動では DFlash を使用していません。", "이번 실행에는 DFlash를 사용하지 않습니다."],
    ["無法確認模型架構，DFlash 不可用。", "The model architecture could not be identified, so DFlash is unavailable.", "モデル構成を確認できないため、DFlash は使用できません。", "모델 아키텍처를 확인할 수 없어 DFlash를 사용할 수 없습니다."],
    ["模型支援 DFlash，但尚未找到配對的 Draft。", "The model supports DFlash, but no matching Draft was found.", "モデルは DFlash に対応していますが、一致する Draft が見つかりません。", "모델은 DFlash를 지원하지만 일치하는 Draft를 찾지 못했습니다."],
    ["關閉", "Off", "オフ", "끄기"],
    ["載入並啟動", "Load and start", "読み込んで起動", "불러와서 시작"],
    ["停止服務", "Stop service", "サービスを停止", "서비스 중지"],
    ["模型服務日誌", "Model service log", "モデルサービスログ", "모델 서비스 로그"],
    ["保留最近 128 KiB", "Keeping the latest 128 KiB", "最新 128 KiB を保持", "최근 128 KiB 유지"],
    ["展開日誌", "Show logs", "ログを表示", "로그 펼치기"],
    ["收合日誌", "Hide logs", "ログを隠す", "로그 접기"],
    ["尚無日誌。", "No logs yet.", "ログはまだありません。", "아직 로그가 없습니다."],
    ["參數列表", "Profiles", "プロファイル一覧", "프로필 목록"],
    ["編輯啟動參數", "Edit launch profile", "起動プロファイルを編集", "시작 프로필 편집"],
    ["參數名稱", "Profile name", "プロファイル名", "프로필 이름"],
    ["Draft GGUF（選用）", "Draft GGUF (optional)", "Draft GGUF（任意）", "Draft GGUF(선택 사항)"],
    ["監聽 Host", "Listen host", "待受ホスト", "수신 호스트"],
    ["監聽 Port", "Listen port", "待受ポート", "수신 포트"],
    ["Threads（0 = 自動）", "Threads (0 = auto)", "スレッド（0 = 自動）", "스레드(0 = 자동)"],
    ["MMap 記憶體保留目標", "MMap memory reserve target", "MMap メモリ予約目標", "MMap 메모리 예약 목표"],
    ["降低長 Context 的 KV Cache 記憶體用量；Q4 較省記憶體，Q8 保留較高精度。", "Reduces KV Cache memory usage for long contexts. Q4 uses less memory, while Q8 preserves higher precision.", "長い Context の KV Cache メモリ使用量を削減します。Q4 はより省メモリで、Q8 はより高い精度を維持します。", "긴 Context의 KV Cache 메모리 사용량을 줄입니다. Q4는 메모리를 더 절약하고 Q8은 더 높은 정밀도를 유지합니다."],
    ["MMap 保留", "MMap reserve", "MMap 予約", "MMap 예약"],
    ["自動", "Auto", "自動", "자동"],
    ["會嘗試為每個運算裝置保留的記憶體目標，但不是硬性的使用上限。", "Attempts to reserve this amount on each compute device, but it is not a hard usage limit.", "各演算デバイスにこのメモリ量を確保しようとしますが、厳密な使用上限ではありません。", "각 연산 장치에 이 메모리 용량을 남겨 두려고 하지만 엄격한 사용 한도는 아닙니다."],
    ["記憶體保留目標", "Memory reserve target", "メモリ予約目標", "메모리 예약 목표"],
    ["額外參數（每行一個 argument）", "Extra arguments (one per line)", "追加引数（1 行につき 1 つ）", "추가 인수(한 줄에 하나)"],
    ["命令預覽", "Command preview", "コマンドプレビュー", "명령 미리 보기"],
    ["下載模型", "Download models", "モデルをダウンロード", "모델 다운로드"],
    ["模型格式", "Model format", "モデル形式", "모델 형식"],
    ["快速選擇常用模型", "Quickly select a popular model", "よく使われるモデルを選択", "자주 사용하는 모델 빠른 선택"],
    ["常用模型", "Popular models", "よく使われるモデル", "자주 사용하는 모델"],
    ["從外部 JSON 名單選擇後，自動填入下載資訊。", "Select from the external JSON catalog to fill in the download details.", "外部 JSON カタログから選択すると、ダウンロード情報を自動入力します。", "외부 JSON 목록에서 선택하면 다운로드 정보를 자동으로 입력합니다."],
    ["GGUF 模型", "GGUF models", "GGUF モデル", "GGUF 모델"],
    ["MLX 模型", "MLX models", "MLX モデル", "MLX 모델"],
    ["8B 級", "8B class", "8B クラス", "8B급"],
    ["30B 級", "30B class", "30B クラス", "30B급"],
    ["70B 以上", "70B and above", "70B 以上", "70B 이상"],
    ["MLX 模型（Apple Silicon）", "MLX models (Apple Silicon)", "MLX モデル（Apple Silicon）", "MLX 모델(Apple Silicon)"],
    ["清單讀取中…", "Loading catalog…", "カタログを読み込んでいます…", "목록을 불러오는 중…"],
    ["無法載入常用模型清單", "Unable to load the popular-model catalog", "よく使われるモデルのカタログを読み込めません", "자주 사용하는 모델 목록을 불러올 수 없습니다"],
    ["不支援的常用模型清單格式", "Unsupported popular-model catalog format", "未対応のモデルカタログ形式です", "지원하지 않는 모델 목록 형식입니다"],
    ["目前沒有可用的常用模型。", "No popular models are currently available.", "利用可能なモデルはありません。", "현재 사용할 수 있는 추천 모델이 없습니다."],
    ["已填入快速下載資訊", "Download details filled in", "ダウンロード情報を入力しました", "다운로드 정보를 입력했습니다"],
    ["GGUF 檔名", "GGUF filename", "GGUF ファイル名", "GGUF 파일 이름"],
    ["目的檔案存在時覆寫", "Overwrite existing destination files", "既存の保存先ファイルを上書き", "대상 파일이 있으면 덮어쓰기"],
    ["開啟儲存位置", "Open storage location", "保存先を開く", "저장 위치 열기"],
    ["僅能在 Tanpopo 桌面介面使用", "Available only in the Tanpopo desktop app", "Tanpopo デスクトップ画面でのみ使用できます", "Tanpopo 데스크톱 화면에서만 사용할 수 있습니다"],
    ["開始下載", "Start download", "ダウンロード開始", "다운로드 시작"],
    ["下載進度", "Download queue", "ダウンロード状況", "다운로드 진행 상황"],
    ["目前沒有下載工作。", "No downloads yet.", "ダウンロードはありません。", "다운로드 작업이 없습니다."],
    ["只存在目前頁面，重新整理後即清除", "Kept only on this page and cleared on refresh", "このページ内のみ保持され、再読み込みで消去されます", "현재 페이지에만 유지되며 새로 고치면 삭제됩니다"],
    ["清除對話", "Clear chat", "会話を消去", "대화 지우기"],
    ["開始一段本機對話", "Start a local chat", "ローカルチャットを始める", "로컬 대화 시작"],
    ["訊息只會傳送給目前執行中的模型，不會保存對話紀錄。", "Messages are sent only to the running model. Chat history is not saved.", "メッセージは実行中のモデルにのみ送信され、会話履歴は保存されません。", "메시지는 실행 중인 모델에만 전송되며 대화 기록은 저장되지 않습니다."],
    ["訊息", "Message", "メッセージ", "메시지"],
    ["模型 API 金鑰（選用）", "Model API key (optional)", "モデル API キー（任意）", "모델 API 키(선택 사항)"],
    ["僅在 Runtime 啟用金鑰驗證時填寫", "Required only when API-key authentication is enabled for the Runtime", "Runtime で API キー認証が有効な場合のみ入力", "Runtime에서 API 키 인증을 사용하는 경우에만 입력"],
    ["請先於「執行狀態」啟動模型服務。", "Start the model service from Runtime first.", "先に「実行状態」でモデルサービスを起動してください。", "먼저 실행 상태에서 모델 서비스를 시작하세요."],
    ["介面語言", "Interface language", "表示言語", "인터페이스 언어"],
    ["AUTO 會依作業系統與瀏覽器語系自動選擇。", "AUTO follows the operating-system and browser language.", "AUTO は OS とブラウザーの言語に合わせます。", "AUTO는 운영 체제와 브라우저 언어를 따릅니다."],
    ["顯示語言", "Display language", "表示言語", "표시 언어"],
    ["繁體中文", "Traditional Chinese", "繁体字中国語", "번체 중국어"],
    ["介面配色", "Interface colors", "インターフェース配色", "인터페이스 색상"],
    ["選擇全站使用的背景、面板與強調色；切換後會立即預覽。", "Choose the background, panel, and accent colors used throughout the interface. Changes are previewed immediately.", "画面全体の背景、パネル、アクセントカラーを選択します。変更はすぐにプレビューされます。", "전체 화면의 배경, 패널 및 강조 색상을 선택합니다. 변경 사항은 즉시 미리 표시됩니다."],
    ["蒲公英", "Tanpopo", "タンポポ", "민들레"],
    ["目前的暖白與蒲公英綠", "Current warm white and dandelion green", "現在の暖かな白とタンポポグリーン", "현재의 따뜻한 흰색과 민들레 초록색"],
    ["海霧藍", "Ocean mist", "海霧ブルー", "바다 안개 블루"],
    ["清爽冷白與深海藍", "Crisp cool white and deep ocean blue", "爽やかなクールホワイトと深海ブルー", "산뜻한 쿨 화이트와 짙은 바다색"],
    ["櫻花粉", "Sakura", "桜色", "벚꽃 핑크"],
    ["柔和暖粉與莓果紅", "Soft warm pink and berry red", "柔らかなウォームピンクとベリーレッド", "부드러운 웜 핑크와 베리 레드"],
    ["夜藤", "Night wisteria", "夜藤", "밤 등나무"],
    ["深紫黑與柔和紫藤色", "Deep violet black and soft wisteria purple", "深い紫黒と柔らかな藤色", "짙은 보라빛 검정과 부드러운 등나무 보라색"],
    ["桌面模式", "Desktop mode", "デスクトップモード", "데스크톱 모드"],
    ["控制圖形介面關閉後，Tanpopo 是否繼續在後台提供服務。", "Choose whether Tanpopo keeps serving in the background after its window closes.", "ウィンドウを閉じた後も Tanpopo をバックグラウンドで動作させるか設定します。", "창을 닫은 뒤에도 Tanpopo가 백그라운드에서 계속 서비스할지 설정합니다."],
    ["常駐", "Keep running", "常駐", "백그라운드 실행"],
    ["開啟後顯示於系統選單列；關閉視窗只會隱藏 UI。", "When enabled, Tanpopo appears in the system menu bar; closing the window hides only the UI.", "有効にするとシステムメニューバーに表示され、ウィンドウを閉じても UI のみ非表示になります。", "켜면 시스템 메뉴 막대에 표시되며 창을 닫아도 UI만 숨겨집니다."],
    ["模型存放位置", "Model storage", "モデル保存先", "모델 저장 위치"],
    ["GGUF 模型目錄", "GGUF model directory", "GGUF モデルフォルダー", "GGUF 모델 폴더"],
    ["MLX 模型目錄（Apple Silicon）", "MLX model directory (Apple Silicon)", "MLX モデルフォルダー（Apple Silicon）", "MLX 모델 폴더(Apple Silicon)"],
    ["預設 Revision", "Default revision", "既定の Revision", "기본 Revision"],
    ["清除 Access Token", "Clear Access Token", "Access Token を消去", "Access Token 지우기"],
    ["儲存設定", "Save settings", "設定を保存", "설정 저장"],
    ["一般設定", "General", "一般設定", "일반 설정"],
    ["介面、桌面模式與模型位置", "Interface, desktop mode, and model locations", "インターフェース、デスクトップモード、モデル保存先", "인터페이스, 데스크톱 모드 및 모델 위치"],
    ["模型來源", "Model sources", "モデル取得元", "모델 소스"],
    ["Hugging Face 連線與存取憑證", "Hugging Face connection and credentials", "Hugging Face の接続と認証情報", "Hugging Face 연결 및 인증 정보"],
    ["儲存模型來源", "Save model source", "モデル取得元を保存", "모델 소스 저장"],
    ["模型來源已保存", "Model source saved", "モデル取得元を保存しました", "모델 소스를 저장했습니다"],
    ["管理帳號、密碼與登入驗證", "Administrator account, password, and authentication", "管理者アカウント、パスワード、ログイン認証", "관리자 계정, 비밀번호 및 로그인 인증"],
    ["Access Key 與 IP 白名單", "Access keys and IP allowlist", "Access Key と IP 許可リスト", "Access Key 및 IP 허용 목록"],
    ["設定分類", "Settings categories", "設定カテゴリー", "설정 분류"],
    ["管理介面登入", "Admin sign-in", "管理画面ログイン", "관리 화면 로그인"],
    ["帳密只保存於本機服務設定", "Credentials are stored only in the local service configuration", "認証情報はローカルサービス設定にのみ保存されます", "로그인 정보는 로컬 서비스 설정에만 저장됩니다"],
    ["管理帳號密碼", "Administrator credentials", "管理者認証情報", "관리자 로그인 정보"],
    ["管理帳號", "Administrator account", "管理者アカウント", "관리자 계정"],
    ["目前密碼", "Current password", "現在のパスワード", "현재 비밀번호"],
    ["新密碼（留空維持不變）", "New password (leave blank to keep unchanged)", "新しいパスワード（空欄なら変更なし）", "새 비밀번호(비워 두면 변경하지 않음)"],
    ["確認新密碼", "Confirm new password", "新しいパスワードを確認", "새 비밀번호 확인"],
    ["儲存登入設定", "Save sign-in settings", "ログイン設定を保存", "로그인 설정 저장"],
    ["網路安全", "Network security", "ネットワークセキュリティ", "네트워크 보안"],
    ["反向代理", "Reverse proxy", "リバースプロキシ", "리버스 프록시"],
    ["NetPass 公共網路連線", "NetPass public-network connection", "NetPass 公開ネットワーク接続", "NetPass 공용 네트워크 연결"],
    ["反向代理會讓本機暴露在公共網路上", "A reverse proxy exposes this computer to the public internet", "リバースプロキシはこのコンピューターを公開ネットワークに露出させます", "리버스 프록시는 이 컴퓨터를 공용 인터넷에 노출합니다"],
    ["任何取得公開網址的人都可以嘗試連線到這台主機。沒有確切使用目的請勿開啟，使用完畢後請立即停止連線。", "Anyone who obtains the public URL can attempt to connect to this host. Do not enable this without a specific purpose, and stop it immediately after use.", "公開 URL を取得した人は誰でもこのホストへの接続を試みることができます。明確な目的がない場合は有効にせず、使用後は直ちに停止してください。", "공개 URL을 얻은 누구나 이 호스트에 연결을 시도할 수 있습니다. 명확한 목적이 없으면 켜지 말고 사용 후 즉시 중지하세요."],
    ["使用政策與責任說明", "Usage policy and responsibility notice", "利用ポリシーと責任に関する説明", "사용 정책 및 책임 안내"],
    ["請完整閱讀後再確認；未勾選前無法開啟反向代理。", "Read the entire notice before confirming. The reverse proxy cannot be enabled until this is checked.", "内容を最後まで読んでから確認してください。チェックするまでリバースプロキシは有効にできません。", "전체 내용을 읽은 후 확인하세요. 체크하기 전에는 리버스 프록시를 활성화할 수 없습니다."],
    ["NetPass 反向代理為 Tanpopo 與 Mars Semi Corp. 的技術合作成果，僅供技術交流與實驗用途。目前採無償方式提供；Mars Semi Corp. 保留隨時調整使用政策之權利，相關異動將另行公告。", "The NetPass reverse proxy is the result of technical cooperation between Tanpopo and Mars Semi Corp. and is provided solely for technical exchange and experimental use. It is currently offered free of charge. Mars Semi Corp. reserves the right to revise its usage policy at any time, and related changes will be announced separately.", "NetPass リバースプロキシは Tanpopo と Mars Semi Corp. の技術協力による成果であり、技術交流および実験目的に限って提供されます。現在は無償で提供されています。Mars Semi Corp. は利用ポリシーを随時変更する権利を留保し、変更内容は別途告知します。", "NetPass 리버스 프록시는 Tanpopo와 Mars Semi Corp.의 기술 협력 결과물로서 기술 교류 및 실험 목적으로만 제공됩니다. 현재 무료로 제공되며, Mars Semi Corp.는 언제든지 사용 정책을 조정할 권리를 보유하고 관련 변경 사항은 별도로 공지합니다."],
    ["啟用本服務後，本機管理介面與 API 呼叫將可透過公共網路存取。使用者應自行評估使用需求、妥善完成安全設定，並承擔相關網路安全風險。對於因使用本服務所衍生的資安事件、資料外洩或其他損失，Mars Semi Corp. 與本軟體均不承擔任何責任。", "After this service is enabled, the local admin interface and API endpoints become accessible over the public internet. Users must assess their own needs, configure security appropriately, and accept the associated network-security risks. Mars Semi Corp. and this software assume no liability for security incidents, data exposure, or other losses arising from use of the service.", "本サービスを有効にすると、ローカル管理画面およびAPI呼び出しが公開ネットワーク経由で利用可能になります。利用者は必要性を自ら判断し、適切なセキュリティ設定を行ったうえで、関連するネットワークセキュリティ上のリスクを負担するものとします。本サービスの利用に起因するセキュリティ事故、情報漏えい、その他の損失について、Mars Semi Corp. および本ソフトウェアは一切の責任を負いません。", "본 서비스를 활성화하면 로컬 관리 화면과 API 호출에 공용 인터넷을 통해 접근할 수 있습니다. 사용자는 사용 필요성을 직접 판단하고 보안 설정을 적절히 완료한 뒤 관련 네트워크 보안 위험을 부담해야 합니다. 서비스 사용으로 인해 발생하는 보안 사고, 정보 유출 또는 기타 손실에 대해 Mars Semi Corp.와 본 소프트웨어는 어떠한 책임도 지지 않습니다."],
    ["我已閱讀並了解上述使用政策與責任說明。", "I have read and understood the usage policy and responsibility notice above.", "上記の利用ポリシーと責任に関する説明を読み、理解しました。", "위의 사용 정책 및 책임 안내를 읽고 이해했습니다."],
    ["安全性前置檢查", "Security prerequisites", "セキュリティ前提条件", "보안 사전 검사"],
    ["連線前必須同時啟用管理介面帳號密碼，以及至少一組模型 API Access Key。", "Before connecting, enable administrator credentials and at least one model API access key.", "接続前に管理画面のアカウント認証と、少なくとも 1 つのモデル API Access Key を有効にしてください。", "연결 전에 관리 화면 계정 인증과 하나 이상의 모델 API Access Key를 활성화하세요."],
    ["管理介面帳號密碼", "Administrator credentials", "管理画面のアカウント認証", "관리 화면 계정 인증"],
    ["檢查登入驗證", "Checking sign-in authentication", "ログイン認証を確認", "로그인 인증 확인"],
    ["模型 API Access Key", "Model API access key", "モデル API Access Key", "모델 API Access Key"],
    ["檢查金鑰驗證與已核發金鑰", "Checking key authentication and issued keys", "キー認証と発行済みキーを確認", "키 인증 및 발급된 키 확인"],
    ["未啟用", "Disabled", "無効", "사용 안 함"],
    ["沒開啟", "Not enabled", "未有効", "켜지지 않음"],
    ["通過", "Passed", "合格", "통과"],
    ["登入驗證已開啟", "Sign-in authentication is enabled", "ログイン認証は有効です", "로그인 인증이 활성화되었습니다"],
    ["登入驗證尚未開啟", "Sign-in authentication is disabled", "ログイン認証は無効です", "로그인 인증이 비활성화되어 있습니다"],
    ["已核發", "Issued", "発行済み", "발급됨"],
    ["組金鑰", "key(s)", "個のキー", "개 키"],
    ["金鑰驗證尚未開啟", "Key authentication is disabled", "キー認証は無効です", "키 인증이 비활성화되어 있습니다"],
    ["安全性前置檢查已通過。", "Security prerequisites passed.", "セキュリティ前提条件を満たしています。", "보안 사전 검사를 통과했습니다."],
    ["請先完成必要的安全性設定。", "Complete the required security settings first.", "必要なセキュリティ設定を先に完了してください。", "필수 보안 설정을 먼저 완료하세요."],
    ["請先開啟管理介面帳號密碼與模型 API Access Key 驗證", "Enable administrator credentials and model API access-key authentication first.", "管理画面のアカウント認証とモデル API Access Key 認証を先に有効にしてください。", "관리 화면 계정 인증과 모델 API Access Key 인증을 먼저 활성화하세요."],
    ["請先開啟管理介面帳號密碼驗證", "Enable administrator credential authentication first.", "管理画面のアカウント認証を先に有効にしてください。", "관리 화면 계정 인증을 먼저 활성화하세요."],
    ["請先核發 Access Key 並開啟模型 API 金鑰驗證", "Issue an access key and enable model API key authentication first.", "Access Key を発行し、モデル API キー認証を先に有効にしてください。", "Access Key를 발급하고 모델 API 키 인증을 먼저 활성화하세요."],
    ["管理登入設定", "Admin sign-in settings", "管理者ログイン設定", "관리자 로그인 설정"],
    ["Access Key 設定", "Access key settings", "Access Key 設定", "Access Key 설정"],
    ["NetPass 連線", "NetPass connection", "NetPass 接続", "NetPass 연결"],
    ["NetPassClient 以閉源執行檔隨安裝包提供；Tanpopo 不包含或發布其原始碼。", "NetPassClient is supplied as a closed-source executable in the installer; Tanpopo does not include or publish its source code.", "NetPassClient はインストーラーにクローズドソースの実行ファイルとして同梱され、Tanpopo はそのソースコードを含めたり公開したりしません。", "NetPassClient는 설치 프로그램에 비공개 실행 파일로 포함되며 Tanpopo는 해당 소스 코드를 포함하거나 공개하지 않습니다."],
    ["裝置名稱（選用）", "Device name (optional)", "デバイス名（任意）", "장치 이름(선택 사항)"],
    ["留空即保留目前設定", "Leave blank to keep the current setting", "空欄なら現在の設定を保持", "비워 두면 현재 설정 유지"],
    ["清除 NetPass Server API Key", "Clear NetPass Server API key", "NetPass Server API Key を消去", "NetPass Server API Key 지우기"],
    ["確定要清除 NetPass Server API Key？清除後必須重新設定才能連線。", "Clear the NetPass Server API key? You must configure it again before reconnecting.", "NetPass Server API Key を消去しますか？再接続するには、もう一度設定する必要があります。", "NetPass Server API Key를 지우시겠습니까? 다시 연결하려면 키를 다시 설정해야 합니다."],
    ["NetPass Server API Key 已清除", "NetPass Server API key cleared", "NetPass Server API Key を消去しました", "NetPass Server API Key가 지워졌습니다"],
    ["連線狀態", "Connection status", "接続状態", "연결 상태"],
    ["連線成功後會在此揭露可開啟及複製的 NetPass 管理頁面網址。", "After connection, the NetPass admin-page URL will appear here for opening or copying.", "接続後、開いたりコピーしたりできる NetPass 管理画面 URL がここに表示されます。", "연결 후 열거나 복사할 수 있는 NetPass 관리 페이지 URL이 여기에 표시됩니다."],
    ["連線", "Connection", "接続", "연결"],
    ["連線中…", "Connecting…", "接続中…", "연결 중…"],
    ["可使用", "Available", "利用可能", "사용 가능"],
    ["開啟時檢查", "Checked when enabled", "有効化時に確認", "활성화할 때 확인"],
    ["執行中", "Running", "実行中", "실행 중"],
    ["安裝包未包含 NetPassClient", "NetPassClient is not included in this installation", "このインストールには NetPassClient が含まれていません", "이 설치에는 NetPassClient가 포함되어 있지 않습니다"],
    ["NetPassClient 為閉源元件，必須由正式 .app 或 MSI 安裝包提供。", "NetPassClient is closed-source and must be supplied by the official .app or MSI installer.", "NetPassClient はクローズドソースであり、正式な .app または MSI インストーラーから提供する必要があります。", "NetPassClient는 비공개 구성 요소이며 공식 .app 또는 MSI 설치 프로그램에서 제공해야 합니다."],
    ["儲存連線設定", "Save connection settings", "接続設定を保存", "연결 설정 저장"],
    ["停止反向代理", "Stop reverse proxy", "リバースプロキシを停止", "리버스 프록시 중지"],
    ["開啟反向代理", "Enable reverse proxy", "リバースプロキシを有効化", "리버스 프록시 켜기"],
    ["NetPass 連線設定已保存", "NetPass connection settings saved", "NetPass 接続設定を保存しました", "NetPass 연결 설정을 저장했습니다"],
    ["正在建立公共連線…", "Establishing a public connection…", "公開接続を確立しています…", "공개 연결 설정 중…"],
    ["NetPassClient 已啟動，正在等待公開網址。", "NetPassClient started; waiting for the public URL.", "NetPassClient を起動しました。公開 URL を待っています。", "NetPassClient가 시작되었습니다. 공개 URL을 기다리는 중입니다."],
    ["反向代理正在連線", "Reverse proxy is connecting", "リバースプロキシを接続中です", "리버스 프록시 연결 중"],
    ["正在停止反向代理…", "Stopping reverse proxy…", "リバースプロキシを停止しています…", "리버스 프록시 중지 중…"],
    ["反向代理已停止。", "Reverse proxy stopped.", "リバースプロキシを停止しました。", "리버스 프록시가 중지되었습니다."],
    ["反向代理已停止", "Reverse proxy stopped", "リバースプロキシを停止しました", "리버스 프록시 중지됨"],
    ["複製 NetPass 網址", "Copy NetPass URL", "NetPass URL をコピー", "NetPass URL 복사"],
    ["NetPass 網址已複製", "NetPass URL copied", "NetPass URL をコピーしました", "NetPass URL을 복사했습니다"],
    ["系統資訊", "System information", "システム情報", "시스템 정보"],
    ["硬體與作業系統摘要", "Hardware and operating-system summary", "ハードウェアと OS の概要", "하드웨어 및 운영 체제 요약"],
    ["作業系統", "Operating system", "オペレーティングシステム", "운영 체제"],
    ["由服務主機即時讀取的系統版本與執行環境。", "System version and runtime environment read directly from the service host.", "サービスホストから直接読み取ったシステムバージョンと実行環境です。", "서비스 호스트에서 직접 읽은 시스템 버전 및 실행 환경입니다."],
    ["版本", "Version", "バージョン", "버전"],
    ["架構", "Architecture", "アーキテクチャ", "아키텍처"],
    ["主機名稱", "Hostname", "ホスト名", "호스트 이름"],
    ["硬體", "Hardware", "ハードウェア", "하드웨어"],
    ["顯示處理器、圖形處理器、核心數與實體記憶體。", "Shows processors, core counts, and physical memory.", "プロセッサ、コア数、物理メモリを表示します。", "프로세서, 코어 수 및 실제 메모리를 표시합니다."],
    ["處理器", "Processor", "プロセッサ", "프로세서"],
    ["實體核心", "Physical cores", "物理コア", "물리 코어"],
    ["邏輯核心", "Logical cores", "論理コア", "논리 코어"],
    ["圖形處理器", "Graphics processor", "グラフィックスプロセッサ", "그래픽 프로세서"],
    ["實體記憶體", "Physical memory", "物理メモリ", "실제 메모리"],
    ["網路", "Network", "ネットワーク", "네트워크"],
    ["列出非 Loopback 網路介面的連線狀態與位址。", "Lists connection status and addresses for non-loopback network interfaces.", "Loopback 以外のネットワークインターフェースの接続状態とアドレスを表示します。", "Loopback을 제외한 네트워크 인터페이스의 연결 상태와 주소를 표시합니다."],
    ["已連線", "Connected", "接続済み", "연결됨"],
    ["已啟用", "Enabled", "有効", "사용"],
    ["未連線", "Disconnected", "未接続", "연결 안 됨"],
    ["IP 位址", "IP addresses", "IP アドレス", "IP 주소"],
    ["MAC 位址", "MAC address", "MAC アドレス", "MAC 주소"],
    ["沒有可顯示的網路介面。", "No network interfaces to display.", "表示できるネットワークインターフェースがありません。", "표시할 네트워크 인터페이스가 없습니다."],
    ["無法讀取系統資訊", "Unable to read system information", "システム情報を読み取れません", "시스템 정보를 읽을 수 없습니다"],
    ["關於", "About", "このアプリについて", "정보"],
    ["版本資訊與更新檢查", "Version information and updates", "バージョン情報と更新確認", "버전 정보 및 업데이트 확인"],
    ["APP 版本", "App version", "アプリバージョン", "앱 버전"],
    ["GitHub 專案", "GitHub repository", "GitHub リポジトリ", "GitHub 저장소"],
    ["管理頁面網址", "Admin page URLs", "管理画面 URL", "관리 페이지 URL"],
    ["複製管理頁面網址", "Copy admin page URL", "管理画面 URL をコピー", "관리 페이지 URL 복사"],
    ["管理頁面網址已複製", "Admin page URL copied", "管理画面 URL をコピーしました", "관리 페이지 URL을 복사했습니다"],
    ["無法自動複製，請手動選取網址", "Unable to copy automatically; select the URL manually", "自動コピーできません。URL を手動で選択してください", "자동으로 복사할 수 없습니다. URL을 직접 선택하세요"],
    ["最新版本", "Latest version", "最新バージョン", "최신 버전"],
    ["上次檢查", "Last checked", "最終確認", "마지막 확인"],
    ["尚未檢查", "Not checked yet", "未確認", "아직 확인하지 않음"],
    ["正在檢查更新…", "Checking for updates…", "更新を確認しています…", "업데이트 확인 중…"],
    ["檢查更新", "Check for updates", "更新を確認", "업데이트 확인"],
    ["查看新版", "View new version", "新しいバージョンを表示", "새 버전 보기"],
    ["更新檢查失敗", "Update check failed", "更新確認に失敗しました", "업데이트 확인 실패"],
    ["目前已是最新版本。", "You are using the latest version.", "現在のバージョンは最新です。", "현재 최신 버전입니다."],
    ["尚未完成更新檢查。", "The update check has not completed yet.", "更新確認はまだ完了していません。", "업데이트 확인이 아직 완료되지 않았습니다."],
    ["存取策略", "Access policy", "アクセスポリシー", "접근 정책"],
    ["目前不使用額外限制", "No additional restrictions", "追加制限は使用していません", "추가 제한을 사용하지 않음"],
    ["要求存取金鑰", "Require an access key", "アクセスキーを要求", "접근 키 필요"],
    ["啟用 IP 白名單", "Enable IP allowlist", "IP 許可リストを有効化", "IP 허용 목록 사용"],
    ["允許的 IP（每行一筆）", "Allowed IPs (one per line)", "許可する IP（1 行につき 1 件）", "허용할 IP(한 줄에 하나)"],
    ["存取金鑰", "Access keys", "アクセスキー", "접근 키"],
    ["金鑰名稱", "Key name", "キー名", "키 이름"],
    ["核發金鑰", "Issue key", "キーを発行", "키 발급"],
    ["請立即複製，關閉後無法再次查看", "Copy it now; it cannot be displayed again after closing", "今すぐコピーしてください。閉じると再表示できません", "지금 복사하세요. 닫은 뒤에는 다시 볼 수 없습니다"],
    ["複製", "Copy", "コピー", "복사"],
    ["我已保存，關閉", "I saved it; close", "保存しました。閉じる", "저장했음, 닫기"],
    ["儲存安全設定", "Save security settings", "セキュリティ設定を保存", "보안 설정 저장"],
    ["選擇模型目錄", "Select model directory", "モデルフォルダーを選択", "모델 폴더 선택"],
    ["上一層", "Parent folder", "上の階層", "상위 폴더"],
    ["選擇此目錄", "Select this folder", "このフォルダーを選択", "이 폴더 선택"],
    ["請求失敗", "Request failed", "リクエストに失敗しました", "요청 실패"],
    ["登入狀態已失效", "Your session has expired", "ログインセッションの有効期限が切れました", "로그인 세션이 만료되었습니다"],
    ["大小未知", "Unknown size", "サイズ不明", "크기 알 수 없음"],
    ["目前沒有保存 Token", "No token is currently saved", "保存済み Token はありません", "현재 저장된 Token이 없습니다"],
    ["變更帳號或密碼後，所有既有登入工作階段都會失效。", "Changing the account or password invalidates all existing sessions.", "アカウントまたはパスワードを変更すると、既存のセッションはすべて無効になります。", "계정 또는 비밀번호를 변경하면 기존 로그인 세션이 모두 만료됩니다."],
    ["尚未核發任何金鑰。", "No access keys have been issued.", "アクセスキーはまだ発行されていません。", "발급된 접근 키가 없습니다."],
    ["撤銷", "Revoke", "無効化", "취소"],
    ["沒有子目錄", "No subfolders", "サブフォルダーはありません", "하위 폴더가 없습니다"],
    ["正在讀取目錄…", "Loading folder…", "フォルダーを読み込んでいます…", "폴더를 불러오는 중…"],
    ["設定已保存。", "Settings saved.", "設定を保存しました。", "설정을 저장했습니다."],
    ["設定已保存", "Settings saved", "設定を保存しました", "설정을 저장했습니다"],
    ["登入驗證已關閉。", "Sign-in authentication is off.", "ログイン認証を無効にしました。", "로그인 인증을 껐습니다."],
    ["管理介面已改為不需登入", "The admin console no longer requires sign-in", "管理画面のログインを不要にしました", "관리 화면에서 로그인이 필요하지 않습니다"],
    ["新密碼與確認密碼不一致。", "The new passwords do not match.", "新しいパスワードが一致しません。", "새 비밀번호가 일치하지 않습니다."],
    ["模型 API 安全設定已保存", "Model API security settings saved", "モデル API のセキュリティ設定を保存しました", "모델 API 보안 설정을 저장했습니다"],
    ["新金鑰已核發，請立即保存", "A new key was issued. Save it now.", "新しいキーを発行しました。今すぐ保存してください。", "새 키를 발급했습니다. 지금 저장하세요."],
    ["金鑰已複製", "Key copied", "キーをコピーしました", "키를 복사했습니다"],
    ["思考中", "Thinking", "思考中", "생각 중"],
    ["生成中…", "Generating…", "生成中…", "생성 중…"],
    ["你", "You", "あなた", "나"],
    ["錯誤", "Error", "エラー", "오류"],
    ["思考過程", "Reasoning", "思考過程", "사고 과정"],
    ["模型未提供最終回答", "The model did not provide a final answer", "モデルから最終回答がありません", "모델이 최종 답변을 제공하지 않았습니다"],
    ["目前的瀏覽器不支援串流回應", "This browser does not support streaming responses", "このブラウザーはストリーミング応答に対応していません", "이 브라우저는 스트리밍 응답을 지원하지 않습니다"],
    ["模型 Runtime 串流失敗", "Model Runtime streaming failed", "モデル Runtime のストリーミングに失敗しました", "모델 Runtime 스트리밍에 실패했습니다"],
    ["模型 Runtime 沒有回傳對話內容", "The model Runtime returned no chat content", "モデル Runtime から会話内容が返されませんでした", "모델 Runtime이 대화 내용을 반환하지 않았습니다"],
    ["取消中…", "Cancelling…", "キャンセル中…", "취소 중…"],
    ["下載已取消", "Download cancelled", "ダウンロードをキャンセルしました", "다운로드를 취소했습니다"],
    ["測試", "Test", "テスト", "테스트"],
    ["測試中…", "Testing…", "テスト中…", "테스트 중…"],
    ["正在測試模型效能…", "Testing model performance…", "モデル性能をテストしています…", "모델 성능을 테스트하는 중…"],
    ["模型載入中，完成後將自動開始測試…", "Loading the model. The test will start automatically when ready…", "モデルを読み込んでいます。完了後、自動的にテストを開始します…", "모델을 불러오는 중입니다. 준비되면 자동으로 테스트를 시작합니다…"],
    ["模型載入逾時，請查看日誌後重新啟動服務", "Model loading timed out. Check the logs and restart the service.", "モデルの読み込みがタイムアウトしました。ログを確認してサービスを再起動してください。", "모델 로딩 시간이 초과되었습니다. 로그를 확인한 후 서비스를 다시 시작하세요."],
    ["模型效能測試逾時，請稍後再試", "The model performance test timed out. Please try again later.", "モデル性能テストがタイムアウトしました。しばらくしてから再試行してください。", "모델 성능 테스트 시간이 초과되었습니다. 잠시 후 다시 시도하세요."],
    ["模型效能測試結果", "Model performance test results", "モデル性能テスト結果", "모델 성능 테스트 결과"],
    ["模型服務運作正常", "The model service is working correctly", "モデルサービスは正常に動作しています", "모델 서비스가 정상적으로 작동합니다"],
    ["輸入 Tokens", "Input tokens", "入力 Tokens", "입력 Tokens"],
    ["輸出 Tokens", "Output tokens", "出力 Tokens", "출력 Tokens"],
    ["生成速度", "Generation speed", "生成速度", "생성 속도"],
    ["測試時間", "Test duration", "テスト時間", "테스트 시간"],
    ["關閉視窗", "Close", "閉じる", "닫기"],
    ["模型服務尚未啟動", "The model service is not running", "モデルサービスは起動していません", "모델 서비스가 실행 중이 아닙니다"],
    ["請先到執行狀態載入模型", "Load a model from Runtime first", "先に「実行状態」でモデルを読み込んでください", "먼저 실행 상태에서 모델을 불러오세요"],
    ["無法取得模型狀態", "Unable to retrieve model status", "モデル状態を取得できません", "모델 상태를 가져올 수 없습니다"],
    ["等待中", "Queued", "待機中", "대기 중"],
    ["下載中", "Downloading", "ダウンロード中", "다운로드 중"],
    ["完成", "Completed", "完了", "완료"],
    ["失敗", "Failed", "失敗", "실패"],
    ["新增啟動參數", "New launch profile", "起動プロファイルを追加", "새 시작 프로필"],
    ["尚未保存", "Not saved", "未保存", "저장되지 않음"],
    ["建立參數", "Create profile", "プロファイルを作成", "프로필 만들기"],
    ["啟動參數已保存。", "Launch profile saved.", "起動プロファイルを保存しました。", "시작 프로필을 저장했습니다."],
    ["啟動參數已保存", "Launch profile saved", "起動プロファイルを保存しました", "시작 프로필을 저장했습니다"],
    ["啟動參數已刪除", "Launch profile deleted", "起動プロファイルを削除しました", "시작 프로필을 삭제했습니다"],
    ["不使用 mmproj", "Do not use mmproj", "mmproj を使用しない", "mmproj 사용 안 함"],
    ["Qwen3.5 GGUF 需要搭配同目錄的 mmproj 才能由 mlx-server 正確生成內容；請先下載配對的 mmproj", "Qwen3.5 GGUF requires a matching mmproj in the same folder for correct mlx-server output. Download the matching mmproj first.", "Qwen3.5 GGUF を mlx-server で正しく生成するには、同じフォルダーの対応する mmproj が必要です。先に対応する mmproj をダウンロードしてください。", "Qwen3.5 GGUF를 mlx-server에서 올바르게 생성하려면 같은 폴더의 일치하는 mmproj가 필요합니다. 먼저 일치하는 mmproj를 다운로드하세요."],
    ["找到多個 mmproj，請在執行狀態頁選擇與 GGUF 模型配對的檔案", "Multiple mmproj files were found. Select the file matching the GGUF model on the Runtime page.", "複数の mmproj が見つかりました。実行状態ページで GGUF モデルに対応するファイルを選択してください。", "여러 mmproj 파일을 찾았습니다. 실행 상태 페이지에서 GGUF 모델과 일치하는 파일을 선택하세요."],
    ["尚無可用的 MLX 模型。", "No MLX models are available.", "利用可能な MLX モデルがありません。", "사용 가능한 MLX 모델이 없습니다."],
    ["尚無支援的 MLX 或 GGUF 模型", "No supported MLX or GGUF models", "対応する MLX または GGUF モデルがありません", "지원되는 MLX 또는 GGUF 모델이 없습니다"],
    ["尚無可用的 MLX 或 GGUF 模型。", "No MLX or GGUF models are available.", "利用可能な MLX または GGUF モデルがありません。", "사용 가능한 MLX 또는 GGUF 모델이 없습니다."],
    ["尚無可用的 GGUF 模型。", "No GGUF models are available.", "利用可能な GGUF モデルがありません。", "사용 가능한 GGUF 모델이 없습니다."],
    ["請先選擇 Target 模型。", "Select a Target model first.", "先に Target モデルを選択してください。", "먼저 Target 모델을 선택하세요."],
    ["模型服務已停止", "Model service stopped", "モデルサービスを停止しました", "모델 서비스를 중지했습니다"],
    ["模型服務日誌已清除", "Model service log cleared", "モデルサービスログを消去しました", "모델 서비스 로그를 지웠습니다"],
    ["API Base URL 已複製", "API Base URL copied", "API Base URL をコピーしました", "API Base URL을 복사했습니다"],
    ["資料已更新", "Data refreshed", "データを更新しました", "데이터를 새로 고쳤습니다"]
    ,["簡易對話 · Tanpopo", "Chat · Tanpopo", "チャット · Tanpopo", "간단 대화 · Tanpopo"]
    ,["啟動參數 · Tanpopo", "Launch profiles · Tanpopo", "起動プロファイル · Tanpopo", "시작 프로필 · Tanpopo"]
    ,["模型下載 · Tanpopo", "Model download · Tanpopo", "モデルのダウンロード · Tanpopo", "모델 다운로드 · Tanpopo"]
    ,["系統設定 · Tanpopo", "System settings · Tanpopo", "システム設定 · Tanpopo", "시스템 설정 · Tanpopo"]
    ,["Tanpopo 登入", "Sign in · Tanpopo", "Tanpopo ログイン", "Tanpopo 로그인"]
    ,["儲存參數組合，啟動時再與選定的 GGUF 動態組合", "Save reusable parameters and combine them with the selected GGUF at launch", "再利用可能なパラメーターを保存し、起動時に選択した GGUF と組み合わせます", "재사용할 매개변수를 저장하고 시작할 때 선택한 GGUF와 결합합니다"]
    ,["<選擇的 MLX 模型目錄或 GGUF>", "<selected MLX model directory or GGUF>", "<選択した MLX モデルフォルダーまたは GGUF>", "<선택한 MLX 모델 폴더 또는 GGUF>"]
    ,["llama-server（跨平台／GGUF）", "llama-server (cross-platform / GGUF)", "llama-server（クロスプラットフォーム／GGUF）", "llama-server(크로스 플랫폼/GGUF)"]
    ,["DFlash 等需要 Draft 模型的模式，請選擇模型目錄內相容且配對的 GGUF；MTP 不需要填寫。", "For DFlash and other modes that require a Draft model, select a compatible paired GGUF from the model directory. MTP does not require one.", "DFlash など Draft モデルが必要なモードでは、モデルフォルダー内の互換性がある GGUF を選択してください。MTP では不要です。", "DFlash처럼 Draft 모델이 필요한 모드는 모델 폴더에서 호환되는 GGUF를 선택하세요. MTP에는 필요하지 않습니다."]
    ,["使用官方 repository / resolve / revision / filename 下載方式", "Uses the official repository / resolve / revision / filename download path", "公式の repository / resolve / revision / filename 方式でダウンロードします", "공식 repository / resolve / revision / filename 방식으로 다운로드합니다"]
    ,["每個主 GGUF 會建立獨立目錄；下載前會依 Hugging Face metadata 尋找同一或外部 repository 的配對 mmproj 與 DFlash Draft。", "Each primary GGUF gets its own folder. Hugging Face metadata is used to find matching mmproj and DFlash Draft files in the same or an external repository.", "各メイン GGUF に専用フォルダーを作成し、Hugging Face metadata から同一または外部 repository の mmproj と DFlash Draft を検索します。", "각 기본 GGUF마다 별도 폴더를 만들고 Hugging Face metadata로 같은 repository 또는 외부 repository의 mmproj와 DFlash Draft를 찾습니다."]
    ,["會檢查 repository 內的 config.json 與 safetensors，並把 Tokenizer、Processor、Chat Template 及所有權重下載到獨立的 MLX 模型目錄。", "Checks config.json and safetensors, then downloads the Tokenizer, Processor, Chat Template, and all weights into a dedicated MLX model folder.", "repository の config.json と safetensors を確認し、Tokenizer、Processor、Chat Template、全ウェイトを専用の MLX モデルフォルダーへ保存します。", "repository의 config.json과 safetensors를 확인한 뒤 Tokenizer, Processor, Chat Template 및 모든 가중치를 별도 MLX 모델 폴더에 저장합니다."]
    ,["輸入欄位會優先使用英數鍵盤，不限制帳號密碼可用字元。", "Input fields prefer an alphanumeric keyboard but do not restrict credential characters.", "入力欄は英数字キーボードを優先しますが、使用できる文字は制限しません。", "입력란은 영문·숫자 키보드를 우선 사용하지만 로그인 문자에는 제한이 없습니다."]
    ,["帳號密碼由", "Credentials are managed by", "認証情報は", "로그인 정보는"]
    ,["管理；服務不建立使用者資料庫。", ". The service does not create a user database.", "で管理され、ユーザーデータベースは作成しません。", "에서 관리하며 서비스는 사용자 데이터베이스를 만들지 않습니다."]
    ,["儲存於本機", "Stored locally in", "ローカル保存先", "로컬 저장 위치"]
    ,["Runtime 由應用程式自動管理；此處只設定兩種模型格式的掃描與下載目錄。", "Runtimes are managed automatically; configure only the scan and download folders for the two model formats here.", "Runtime はアプリが自動管理します。ここでは 2 種類のモデル形式の検索・保存先のみ設定します。", "Runtime은 앱이 자동 관리합니다. 여기서는 두 모델 형식의 검색 및 다운로드 폴더만 설정합니다."]
    ,["按資料夾按鈕選擇服務主機上的模型目錄，選取後再儲存設定。", "Use the folder button to select a model directory on the service host, then save the settings.", "フォルダーボタンでサービスホスト上のモデルフォルダーを選択し、設定を保存してください。", "폴더 버튼으로 서비스 호스트의 모델 폴더를 선택한 뒤 설정을 저장하세요."]
    ,["Token 僅在存取 gated 或 private repository 時需要。", "A token is required only for gated or private repositories.", "Token は gated または private repository にアクセスする場合のみ必要です。", "Token은 gated 또는 private repository에 접근할 때만 필요합니다."]
    ,["新安裝預設為", "New installations default to", "新規インストールの既定値は", "새 설치의 기본값은"]
    ,["。密碼不會由管理 API 回傳，也不會顯示既有明文。", ". Passwords are never returned by the management API, and stored plaintext is not displayed.", "です。パスワードは管理 API から返されず、保存済みの平文も表示しません。", "입니다. 비밀번호는 관리 API에서 반환되지 않으며 저장된 원문도 표시하지 않습니다."]
    ,["關閉後不需帳號密碼即可進入所有管理頁面", "When off, all admin pages are accessible without credentials", "無効にすると認証情報なしですべての管理ページへアクセスできます", "끄면 로그인 없이 모든 관리 페이지에 접근할 수 있습니다"]
    ,["策略與金鑰雜湊保存於本機安全設定檔", "Policies and key hashes are stored in a local security file", "ポリシーとキーハッシュはローカルのセキュリティ設定に保存されます", "정책과 키 해시는 로컬 보안 설정 파일에 저장됩니다"]
    ,["金鑰與 IP 白名單可各自使用；兩者同時啟用時，請求必須同時通過兩項檢查。", "Access keys and the IP allowlist can be used independently. When both are enabled, requests must pass both checks.", "アクセスキーと IP 許可リストは個別に使用できます。両方を有効にした場合、両方の検証が必要です。", "접근 키와 IP 허용 목록을 각각 사용할 수 있습니다. 둘 다 켜면 두 검사를 모두 통과해야 합니다."]
    ,["接受 Authorization Bearer 或 X-OpenLoader-Key", "Accept Authorization Bearer or X-OpenLoader-Key", "Authorization Bearer または X-OpenLoader-Key を使用", "Authorization Bearer 또는 X-OpenLoader-Key 허용"]
    ,["依實際連線來源 IP 判斷，不信任轉送標頭", "Uses the direct client IP and does not trust forwarded headers", "実際の接続元 IP を使用し、転送ヘッダーは信頼しません", "실제 연결 원본 IP를 사용하며 전달 헤더는 신뢰하지 않습니다"]
    ,["支援完整 IPv4／IPv6、CIDR，以及", "Supports full IPv4/IPv6 addresses, CIDR, and", "IPv4／IPv6、CIDR、および", "전체 IPv4/IPv6, CIDR 및"]
    ,["萬用字元；單獨輸入", "wildcards. Entering", "ワイルドカードに対応します。", "와일드카드를 지원합니다."]
    ,["代表允許所有 IP。", "alone permits every IP.", "だけを入力するとすべての IP を許可します。", "만 입력하면 모든 IP를 허용합니다."]
    ,["金鑰明文只在核發時顯示一次，系統僅保存 SHA-256 雜湊；兩個 Runtime 會在數秒內同步撤銷結果。", "Key plaintext is shown once at issuance; only a SHA-256 hash is saved. Both Runtimes synchronize revocations within seconds.", "キーの平文は発行時に一度だけ表示され、SHA-256 ハッシュのみ保存されます。両 Runtime は数秒以内に失効を反映します。", "키 원문은 발급 시 한 번만 표시되며 SHA-256 해시만 저장됩니다. 두 Runtime은 몇 초 안에 취소 상태를 동기화합니다."]
    ,["讀取中…", "Loading…", "読み込み中…", "불러오는 중…"]
  ];

  const languages = ["zh-Hant", "en", "ja", "ko"];
  const dictionaries = Object.fromEntries(languages.map((language) => [language, new Map()]));
  rows.forEach((row) => languages.forEach((language, index) => dictionaries[language].set(row[0], row[index])));

  function normalizeLanguage(value) {
    const normalized = String(value || "auto").trim().toLowerCase();
    if (normalized === "auto") return "auto";
    if (["zh-hant", "zh-tw", "zh-hk"].includes(normalized)) return "zh-Hant";
    if (normalized.startsWith("ja")) return "ja";
    if (normalized.startsWith("ko")) return "ko";
    if (normalized.startsWith("en")) return "en";
    return "auto";
  }

  function normalizeTheme(value) {
    const normalized = String(value || "tanpopo").trim().toLowerCase();
    return ["tanpopo", "ocean", "sakura", "wisteria"].includes(normalized) ? normalized : "tanpopo";
  }

  function resolvedLanguage(value = selectedLanguage) {
    const normalized = normalizeLanguage(value);
    if (normalized !== "auto") return normalized;
    const candidates = navigator.languages?.length ? navigator.languages : [navigator.language];
    for (const candidate of candidates) {
      const language = String(candidate || "").toLowerCase();
      if (language.startsWith("zh") && (language.includes("hant") || language.includes("tw") || language.includes("hk"))) return "zh-Hant";
      if (language.startsWith("ja")) return "ja";
      if (language.startsWith("ko")) return "ko";
      if (language.startsWith("en")) return "en";
    }
    return "en";
  }

  let selectedLanguage = normalizeLanguage(localStorage.getItem(LANGUAGE_KEY) || "auto");
  let selectedTheme = normalizeTheme(localStorage.getItem(THEME_KEY) || "tanpopo");
  const textSources = new WeakMap();
  const attributeSources = new WeakMap();
  const ignoredSelector = "script, style, code, pre, .chat-message, #chatMessages, #logOutput, #commandPreview";

  function t(source) {
    if (typeof source !== "string" || !source) return source;
    const language = resolvedLanguage();
    const exact = dictionaries[language]?.get(source);
    if (exact) return exact;
    const rules = {
      en: [
        [/^有新版本 (.+) 可用。$/, "New version $1 is available."],
        [/^發現新版本 (.+)，請至「系統設定 → 關於」查看。$/, "New version $1 is available. See System settings → About."],
        [/^(.+) 已就緒$/, "$1 ready"], [/^(.+) 執行中$/, "$1 running"],
        [/^(.+) 已停止$/, "$1 stopped"], [/^(.+) 日誌$/, "$1 log"],
        [/^(.+) 已啟動$/, "$1 started"], [/^修改於 (.+)$/, "Modified $1"],
        [/^更新於 (.+)$/, "Updated $1"],
        [/^已啟用，Draft：(.+)$/, "Enabled. Draft: $1"],
        [/^模型架構 (.+) 不支援 DFlash。$/, "Model architecture $1 does not support DFlash."],
        [/^啟動參數指定的 Draft 不存在：(.+)$/, "The Draft selected by the launch profile does not exist: $1"],
        [/^可用 Draft：(.+)（預設關閉）$/, "Available Draft: $1 (off by default)"]
      ],
      ja: [
        [/^有新版本 (.+) 可用。$/, "新しいバージョン $1 を利用できます。"],
        [/^發現新版本 (.+)，請至「系統設定 → 關於」查看。$/, "新しいバージョン $1 を利用できます。「システム設定 → このアプリについて」を確認してください。"],
        [/^(.+) 已就緒$/, "$1 準備完了"], [/^(.+) 執行中$/, "$1 実行中"],
        [/^(.+) 已停止$/, "$1 停止"], [/^(.+) 日誌$/, "$1 ログ"],
        [/^(.+) 已啟動$/, "$1 を起動しました"], [/^修改於 (.+)$/, "更新日時 $1"],
        [/^更新於 (.+)$/, "更新日時 $1"],
        [/^已啟用，Draft：(.+)$/, "有効、Draft：$1"],
        [/^模型架構 (.+) 不支援 DFlash。$/, "モデル構成 $1 は DFlash に対応していません。"],
        [/^啟動參數指定的 Draft 不存在：(.+)$/, "起動プロファイルで指定された Draft がありません：$1"],
        [/^可用 Draft：(.+)（預設關閉）$/, "利用可能な Draft：$1（既定ではオフ）"]
      ],
      ko: [
        [/^有新版本 (.+) 可用。$/, "새 버전 $1을 사용할 수 있습니다."],
        [/^發現新版本 (.+)，請至「系統設定 → 關於」查看。$/, "새 버전 $1을 사용할 수 있습니다. 시스템 설정 → 정보를 확인하세요."],
        [/^(.+) 已就緒$/, "$1 준비됨"], [/^(.+) 執行中$/, "$1 실행 중"],
        [/^(.+) 已停止$/, "$1 중지됨"], [/^(.+) 日誌$/, "$1 로그"],
        [/^(.+) 已啟動$/, "$1 시작됨"], [/^修改於 (.+)$/, "수정: $1"],
        [/^更新於 (.+)$/, "업데이트: $1"],
        [/^已啟用，Draft：(.+)$/, "켜짐. Draft: $1"],
        [/^模型架構 (.+) 不支援 DFlash。$/, "모델 아키텍처 $1은 DFlash를 지원하지 않습니다."],
        [/^啟動參數指定的 Draft 不存在：(.+)$/, "시작 프로필에서 지정한 Draft가 없습니다: $1"],
        [/^可用 Draft：(.+)（預設關閉）$/, "사용 가능한 Draft: $1(기본값 꺼짐)"]
      ]
    }[language] || [];
    for (const [pattern, replacement] of rules) {
      if (pattern.test(source)) return source.replace(pattern, replacement);
    }
    return source;
  }

  function translateTextNode(node, force = false) {
    if (!node.parentElement || node.parentElement.closest(ignoredSelector)) return;
    const raw = node.nodeValue || "";
    const trimmed = raw.trim();
    if (!trimmed) return;
    const previous = textSources.get(node);
    if (!previous || (!force && raw !== previous.rendered)) {
      textSources.set(node, { source: trimmed, rendered: raw });
    }
    const state = textSources.get(node);
    const translated = t(state.source);
    const prefix = raw.match(/^\s*/)?.[0] || "";
    const suffix = raw.match(/\s*$/)?.[0] || "";
    const rendered = `${prefix}${translated}${suffix}`;
    state.rendered = rendered;
    if (raw !== rendered) node.nodeValue = rendered;
  }

  function translateElement(element, force = false) {
    if (!(element instanceof Element) || element.closest(ignoredSelector)) return;
    const state = attributeSources.get(element) || {};
    ["placeholder", "title", "aria-label"].forEach((attribute) => {
      if (!element.hasAttribute(attribute)) return;
      const raw = element.getAttribute(attribute) || "";
      const previous = state[attribute];
      if (!previous || (!force && raw !== previous.rendered)) state[attribute] = { source: raw, rendered: raw };
      const translated = t(state[attribute].source);
      state[attribute].rendered = translated;
      if (raw !== translated) element.setAttribute(attribute, translated);
    });
    attributeSources.set(element, state);
  }

  function translateTree(root = document, force = false) {
    if (root instanceof Text) {
      translateTextNode(root, force);
      return;
    }
    if (root instanceof Element) translateElement(root, force);
    const walker = document.createTreeWalker(root, NodeFilter.SHOW_ELEMENT | NodeFilter.SHOW_TEXT);
    let node;
    while ((node = walker.nextNode())) {
      if (node instanceof Text) translateTextNode(node, force);
      else translateElement(node, force);
    }
  }

  function applyLanguage() {
    document.documentElement.lang = resolvedLanguage();
    translateTree(document, true);
    document.dispatchEvent(new CustomEvent("tanpopo:languagechange", {
      detail: { selected: selectedLanguage, resolved: resolvedLanguage() }
    }));
  }

  function setLanguage(value) {
    selectedLanguage = normalizeLanguage(value);
    localStorage.setItem(LANGUAGE_KEY, selectedLanguage);
    applyLanguage();
  }

  function getLanguage() {
    return { selected: selectedLanguage, resolved: resolvedLanguage() };
  }

  function applyTheme() {
    document.documentElement.dataset.theme = selectedTheme;
    document.dispatchEvent(new CustomEvent("tanpopo:themechange", {
      detail: { theme: selectedTheme }
    }));
  }

  function setTheme(value) {
    selectedTheme = normalizeTheme(value);
    localStorage.setItem(THEME_KEY, selectedTheme);
    applyTheme();
  }

  function getTheme() {
    return selectedTheme;
  }

  async function api(path, options = {}) {
    const response = await fetch(path, {
      credentials: "same-origin",
      cache: "no-store",
      ...options,
      headers: {
        "Accept": "application/json",
        ...(options.body ? { "Content-Type": "application/json" } : {}),
        ...(options.headers || {})
      }
    });
    const text = await response.text();
    let payload = {};
    if (text) {
      try { payload = JSON.parse(text); } catch (_) { payload = {}; }
    }
    if (response.status === 401) {
      location.replace("/login.html");
      throw new Error(t("登入狀態已失效"));
    }
    if (!response.ok) throw new Error(payload?.error?.message || `${t("請求失敗")} (${response.status})`);
    return payload;
  }

  function showMessage(message, kind = "success") {
    const toast = byId("globalMessage");
    if (!toast) return;
    toast.textContent = t(message);
    toast.className = `toast page-toast ${kind}`;
    toast.hidden = false;
    window.clearTimeout(showMessage.timeout);
    showMessage.timeout = window.setTimeout(() => { toast.hidden = true; }, 4800);
  }

  function formatBytes(value) {
    if (!Number.isFinite(value) || value < 0) return t("大小未知");
    const units = ["B", "KiB", "MiB", "GiB", "TiB"];
    let current = value;
    let index = 0;
    while (current >= 1024 && index < units.length - 1) {
      current /= 1024;
      index += 1;
    }
    return `${current.toFixed(index === 0 ? 0 : 1)} ${units[index]}`;
  }

  function formatTime(value) {
    if (!value || value.startsWith("0001-")) return "—";
    const locale = { "zh-Hant": "zh-TW", en: "en", ja: "ja-JP", ko: "ko-KR" }[resolvedLanguage()];
    return new Intl.DateTimeFormat(locale, {
      month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", second: "2-digit"
    }).format(new Date(value));
  }

  async function logout() {
    try { await api("/api/logout", { method: "POST", body: JSON.stringify({}) }); }
    finally { location.replace("/login.html"); }
  }

  async function updateAuthenticationControls() {
    const logoutButton = byId("logoutButton");
    if (!logoutButton) return;
    try {
      const response = await fetch("/api/session", {
        credentials: "same-origin", cache: "no-store", headers: { "Accept": "application/json" }
      });
      if (!response.ok) return;
      const state = await response.json();
      logoutButton.hidden = !state.authentication_enabled;
    } catch (_) {
      // 頁面主要功能仍可運作；下一次 API 請求會處理登入狀態。
    }
  }

  async function syncInterfacePreferences() {
    try {
      const response = await fetch("/api/settings", {
        credentials: "same-origin", cache: "no-store", headers: { "Accept": "application/json" }
      });
      if (!response.ok) return;
      const settings = await response.json();
      setLanguage(settings.ui_language || "auto");
      if (Object.prototype.hasOwnProperty.call(settings, "ui_theme")) {
        setTheme(settings.ui_theme || "tanpopo");
      }
    } catch (_) {
      // 本機保存的偏好仍可使用；下次成功連線時會再同步服務端設定。
    }
  }

  function updateNoticeWasShown(status) {
    const key = `tanpopo.updateNotice.${status.current_version || "unknown"}.${status.latest_version || "unknown"}`;
    try {
      if (sessionStorage.getItem(key) === "1") return true;
      sessionStorage.setItem(key, "1");
    } catch (_) {
      // 隱私模式可能停用 Web Storage；通知仍可正常顯示。
    }
    return false;
  }

  async function checkForAppUpdate() {
    try {
      const response = await fetch("/api/app-version", {
        credentials: "same-origin",
        cache: "no-store",
        headers: { "Accept": "application/json" }
      });
      if (!response.ok) return;
      const status = await response.json();
      if (!status.update_available || updateNoticeWasShown(status)) return;
      const latest = String(status.latest_version || "").trim() || "—";
      showMessage(`發現新版本 ${latest}，請至「系統設定 → 關於」查看。`);
      window.clearTimeout(showMessage.timeout);
      showMessage.timeout = window.setTimeout(() => {
        const toast = byId("globalMessage");
        if (toast) toast.hidden = true;
      }, 12000);
    } catch (_) {
      // 自動更新檢查失敗不影響主要功能；關於頁面仍可手動重試。
    }
  }

  function metricBand(metric) {
    if (!metric?.available || !Number.isFinite(Number(metric.percent))) return "unavailable";
    const percent = Number(metric.percent);
    if (percent >= 80) return "high";
    if (percent >= 50) return "medium";
    return "low";
  }

  function renderSystemMetric(name, metric) {
    const element = document.querySelector(`[data-system-metric="${name}"]`);
    if (!element) return;
    const available = Boolean(metric?.available) && Number.isFinite(Number(metric?.percent));
    const percent = available ? Math.max(0, Math.min(100, Number(metric.percent))) : 0;
    const band = metricBand(metric);
    element.dataset.band = band;
    element.querySelector(".system-metric-value").textContent = available ? `${percent.toFixed(1)}%` : "N/A";
    const fill = element.querySelector(".system-metric-fill");
    fill.style.width = `${percent}%`;
    const label = element.querySelector(".system-metric-label").textContent;
    element.setAttribute("aria-label", available ? `${label} ${percent.toFixed(1)}%` : `${label} N/A`);
    const device = String(metric?.device || "").trim();
    if (device) element.title = device;
    else element.removeAttribute("title");
  }

  function installSystemStatusBar() {
    if (byId("systemStatusBar")) return;
    const footer = document.createElement("footer");
    footer.id = "systemStatusBar";
    footer.className = "system-status-bar";
    footer.setAttribute("aria-label", "系統資源使用率");
    footer.innerHTML = `
      <div class="system-status-inner" role="status" aria-live="off">
        ${["CPU", "GPU", "MEMORY"].map((label) => `
          <div class="system-metric" data-system-metric="${label.toLowerCase()}" data-band="unavailable">
            <span class="system-metric-label">${label}</span>
            <span class="system-metric-track" aria-hidden="true"><i class="system-metric-fill"></i></span>
            <strong class="system-metric-value">N/A</strong>
          </div>
        `).join("")}
      </div>`;
    document.body.appendChild(footer);
    document.body.classList.add("has-system-status-bar");

    const refresh = async () => {
      try {
        const response = await fetch("/api/system/metrics", {
          credentials: "same-origin",
          cache: "no-store",
          headers: { "Accept": "application/json" }
        });
        if (!response.ok) throw new Error(`HTTP ${response.status}`);
        const snapshot = await response.json();
        renderSystemMetric("gpu", snapshot.gpu);
        renderSystemMetric("cpu", snapshot.cpu);
        renderSystemMetric("memory", snapshot.memory);
      } catch (_) {
        renderSystemMetric("gpu", null);
        renderSystemMetric("cpu", null);
        renderSystemMetric("memory", null);
      }
    };
    refresh();
    window.setInterval(refresh, 3000);
  }

  function installContentScrollBehavior() {
    const scroller = document.querySelector("body:not(.login-page) > .page-shell");
    if (!scroller) return;
    const indicator = document.createElement("i");
    indicator.className = "content-scrollbar";
    indicator.setAttribute("aria-hidden", "true");
    document.body.appendChild(indicator);
    let hideTimer = 0;

    const updateIndicator = () => {
      const maximumScroll = scroller.scrollHeight - scroller.clientHeight;
      if (maximumScroll <= 0) {
        indicator.hidden = true;
        return;
      }
      indicator.hidden = false;
      const bounds = scroller.getBoundingClientRect();
      const trackHeight = Math.max(0, bounds.height - 4);
      const thumbHeight = Math.max(42, trackHeight * (scroller.clientHeight / scroller.scrollHeight));
      const availableTravel = Math.max(0, trackHeight - thumbHeight);
      const offset = availableTravel * (scroller.scrollTop / maximumScroll);
      indicator.style.top = `${bounds.top + 2 + offset}px`;
      indicator.style.height = `${thumbHeight}px`;
    };

    scroller.addEventListener("scroll", () => {
      updateIndicator();
      indicator.classList.add("visible");
      window.clearTimeout(hideTimer);
      hideTimer = window.setTimeout(() => indicator.classList.remove("visible"), 850);
    }, { passive: true });
    window.addEventListener("resize", updateIndicator, { passive: true });
    if (window.ResizeObserver) {
      const resizeObserver = new ResizeObserver(updateIndicator);
      resizeObserver.observe(scroller);
    }
    window.requestAnimationFrame(updateIndicator);
  }

  const observer = new MutationObserver((mutations) => {
    mutations.forEach((mutation) => {
      if (mutation.type === "characterData") translateTextNode(mutation.target);
      mutation.addedNodes.forEach((node) => translateTree(node));
    });
  });

  applyTheme();
  applyLanguage();
  installSystemStatusBar();
  installContentScrollBehavior();
  observer.observe(document.documentElement, { childList: true, characterData: true, subtree: true });
  byId("logoutButton")?.addEventListener("click", logout);
  updateAuthenticationControls();
  syncInterfacePreferences();
  window.setTimeout(checkForAppUpdate, 400);
  window.setInterval(checkForAppUpdate, 5 * 60 * 1000);
  window.LlamaLoader = {
    api, byId, showMessage, formatBytes, formatTime, t, setLanguage, getLanguage, setTheme, getTheme
  };
})();
