(() => {
  const LANGUAGE_KEY = "tanpopo.uiLanguage";
  const byId = (id) => document.getElementById(id);

  // 介面以繁體中文作為字串來源，避免各頁面各自維護一套翻譯流程。
  const rows = [
    ["執行狀態", "Runtime", "実行状態", "실행 상태"],
    ["啟動命令", "Launch profiles", "起動プロファイル", "시작 프로필"],
    ["簡易對話", "Chat", "チャット", "간단 대화"],
    ["模型下載", "Model download", "モデルのダウンロード", "모델 다운로드"],
    ["環境設定", "Settings", "環境設定", "환경 설정"],
    ["登出", "Sign out", "ログアウト", "로그아웃"],
    ["重新整理", "Refresh", "更新", "새로 고침"],
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
    ["正在取得模型服務狀態…", "Retrieving model service status…", "モデルサービスの状態を取得しています…", "모델 서비스 상태를 가져오는 중…"],
    ["Runtime", "Runtime", "Runtime", "Runtime"],
    ["啟動參數", "Launch profile", "起動プロファイル", "시작 프로필"],
    ["模型", "Model", "モデル", "모델"],
    ["選擇 llama-server 支援的 GGUF 模型", "Select a GGUF model supported by llama-server", "llama-server 対応の GGUF モデルを選択", "llama-server가 지원하는 GGUF 모델 선택"],
    ["模型清單讀取中…", "Loading model list…", "モデル一覧を読み込んでいます…", "모델 목록을 불러오는 중…"],
    ["選擇 mmproj（選用）", "Select mmproj (optional)", "mmproj を選択（任意）", "mmproj 선택(선택 사항)"],
    ["mmproj 清單讀取中…", "Loading mmproj list…", "mmproj 一覧を読み込んでいます…", "mmproj 목록을 불러오는 중…"],
    ["選擇模型後會自動檢查是否支援。", "Support is checked automatically after selecting a model.", "モデル選択後に対応状況を自動確認します。", "모델 선택 후 지원 여부를 자동으로 확인합니다."],
    ["啟用", "On", "オン", "켜기"],
    ["關閉", "Off", "オフ", "끄기"],
    ["載入並啟動", "Load and start", "読み込んで起動", "불러와서 시작"],
    ["停止服務", "Stop service", "サービスを停止", "서비스 중지"],
    ["模型服務日誌", "Model service log", "モデルサービスログ", "모델 서비스 로그"],
    ["保留最近 128 KiB", "Keeping the latest 128 KiB", "最新 128 KiB を保持", "최근 128 KiB 유지"],
    ["尚無日誌。", "No logs yet.", "ログはまだありません。", "아직 로그가 없습니다."],
    ["參數列表", "Profiles", "プロファイル一覧", "프로필 목록"],
    ["編輯啟動參數", "Edit launch profile", "起動プロファイルを編集", "시작 프로필 편집"],
    ["參數名稱", "Profile name", "プロファイル名", "프로필 이름"],
    ["Draft GGUF（選用）", "Draft GGUF (optional)", "Draft GGUF（任意）", "Draft GGUF(선택 사항)"],
    ["監聽 Host", "Listen host", "待受ホスト", "수신 호스트"],
    ["監聽 Port", "Listen port", "待受ポート", "수신 포트"],
    ["Threads（0 = 自動）", "Threads (0 = auto)", "スレッド（0 = 自動）", "스레드(0 = 자동)"],
    ["額外參數（每行一個 argument）", "Extra arguments (one per line)", "追加引数（1 行につき 1 つ）", "추가 인수(한 줄에 하나)"],
    ["命令預覽", "Command preview", "コマンドプレビュー", "명령 미리 보기"],
    ["下載模型", "Download models", "モデルをダウンロード", "모델 다운로드"],
    ["模型格式／Runtime", "Model format / Runtime", "モデル形式／Runtime", "모델 형식/Runtime"],
    ["GGUF 檔名", "GGUF filename", "GGUF ファイル名", "GGUF 파일 이름"],
    ["目的檔案存在時覆寫", "Overwrite existing destination files", "既存の保存先ファイルを上書き", "대상 파일이 있으면 덮어쓰기"],
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
    ["桌面模式", "Desktop mode", "デスクトップモード", "데스크톱 모드"],
    ["控制圖形介面關閉後，Tanpopo 是否繼續在後台提供服務。", "Choose whether Tanpopo keeps serving in the background after its window closes.", "ウィンドウを閉じた後も Tanpopo をバックグラウンドで動作させるか設定します。", "창을 닫은 뒤에도 Tanpopo가 백그라운드에서 계속 서비스할지 설정합니다."],
    ["常駐", "Keep running", "常駐", "백그라운드 실행"],
    ["開啟後顯示於系統選單列；關閉視窗只會隱藏 UI。", "When enabled, Tanpopo appears in the system menu bar; closing the window hides only the UI.", "有効にするとシステムメニューバーに表示され、ウィンドウを閉じても UI のみ非表示になります。", "켜면 시스템 메뉴 막대에 표시되며 창을 닫아도 UI만 숨겨집니다."],
    ["模型存放位置", "Model storage", "モデル保存先", "모델 저장 위치"],
    ["GGUF 模型目錄（llama-server）", "GGUF model directory (llama-server)", "GGUF モデルフォルダー（llama-server）", "GGUF 모델 폴더(llama-server)"],
    ["MLX 模型目錄（Apple Silicon）", "MLX model directory (Apple Silicon)", "MLX モデルフォルダー（Apple Silicon）", "MLX 모델 폴더(Apple Silicon)"],
    ["預設 Revision", "Default revision", "既定の Revision", "기본 Revision"],
    ["清除已保存的 Token", "Clear saved token", "保存済み Token を消去", "저장된 Token 지우기"],
    ["儲存設定", "Save settings", "設定を保存", "설정 저장"],
    ["管理介面登入", "Admin sign-in", "管理画面ログイン", "관리 화면 로그인"],
    ["帳密只保存於本機服務設定", "Credentials are stored only in the local service configuration", "認証情報はローカルサービス設定にのみ保存されます", "로그인 정보는 로컬 서비스 설정에만 저장됩니다"],
    ["管理帳號密碼", "Administrator credentials", "管理者認証情報", "관리자 로그인 정보"],
    ["管理帳號", "Administrator account", "管理者アカウント", "관리자 계정"],
    ["目前密碼", "Current password", "現在のパスワード", "현재 비밀번호"],
    ["新密碼（留空維持不變）", "New password (leave blank to keep unchanged)", "新しいパスワード（空欄なら変更なし）", "새 비밀번호(비워 두면 변경하지 않음)"],
    ["確認新密碼", "Confirm new password", "新しいパスワードを確認", "새 비밀번호 확인"],
    ["儲存登入設定", "Save sign-in settings", "ログイン設定を保存", "로그인 설정 저장"],
    ["模型 API 安全性", "Model API security", "モデル API セキュリティ", "모델 API 보안"],
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
    ["尚無可用的 MLX 模型。", "No MLX models are available.", "利用可能な MLX モデルがありません。", "사용 가능한 MLX 모델이 없습니다."],
    ["尚無可用的 GGUF 模型。", "No GGUF models are available.", "利用可能な GGUF モデルがありません。", "사용 가능한 GGUF 모델이 없습니다."],
    ["請先選擇 Target 模型。", "Select a Target model first.", "先に Target モデルを選択してください。", "먼저 Target 모델을 선택하세요."],
    ["模型服務已停止", "Model service stopped", "モデルサービスを停止しました", "모델 서비스를 중지했습니다"],
    ["模型服務日誌已清除", "Model service log cleared", "モデルサービスログを消去しました", "모델 서비스 로그를 지웠습니다"],
    ["API Base URL 已複製", "API Base URL copied", "API Base URL をコピーしました", "API Base URL을 복사했습니다"],
    ["資料已更新", "Data refreshed", "データを更新しました", "데이터를 새로 고쳤습니다"]
    ,["簡易對話 · Tanpopo", "Chat · Tanpopo", "チャット · Tanpopo", "간단 대화 · Tanpopo"]
    ,["啟動命令 · Tanpopo", "Launch profiles · Tanpopo", "起動プロファイル · Tanpopo", "시작 프로필 · Tanpopo"]
    ,["模型下載 · Tanpopo", "Model download · Tanpopo", "モデルのダウンロード · Tanpopo", "모델 다운로드 · Tanpopo"]
    ,["環境設定 · Tanpopo", "Settings · Tanpopo", "環境設定 · Tanpopo", "환경 설정 · Tanpopo"]
    ,["Tanpopo 登入", "Sign in · Tanpopo", "Tanpopo ログイン", "Tanpopo 로그인"]
    ,["儲存參數組合，啟動時再與選定的 GGUF 動態組合", "Save reusable parameters and combine them with the selected GGUF at launch", "再利用可能なパラメーターを保存し、起動時に選択した GGUF と組み合わせます", "재사용할 매개변수를 저장하고 시작할 때 선택한 GGUF와 결합합니다"]
    ,["llama-server（跨平台／GGUF）", "llama-server (cross-platform / GGUF)", "llama-server（クロスプラットフォーム／GGUF）", "llama-server(크로스 플랫폼/GGUF)"]
    ,["DFlash 等需要 Draft 模型的模式，請選擇模型目錄內相容且配對的 GGUF；MTP 不需要填寫。", "For DFlash and other modes that require a Draft model, select a compatible paired GGUF from the model directory. MTP does not require one.", "DFlash など Draft モデルが必要なモードでは、モデルフォルダー内の互換性がある GGUF を選択してください。MTP では不要です。", "DFlash처럼 Draft 모델이 필요한 모드는 모델 폴더에서 호환되는 GGUF를 선택하세요. MTP에는 필요하지 않습니다."]
    ,["使用官方 repository / resolve / revision / filename 下載方式", "Uses the official repository / resolve / revision / filename download path", "公式の repository / resolve / revision / filename 方式でダウンロードします", "공식 repository / resolve / revision / filename 방식으로 다운로드합니다"]
    ,["每個主 GGUF 會建立獨立目錄；下載前也會檢查相同 repository／revision，將配對的 mmproj 與 DFlash Draft 放進同一目錄。", "Each primary GGUF gets its own folder. Matching mmproj and DFlash Draft files from the same repository and revision are placed with it.", "各メイン GGUF に専用フォルダーを作成し、同じ repository／revision の mmproj と DFlash Draft も同じ場所に保存します。", "각 기본 GGUF마다 별도 폴더를 만들고 같은 repository/revision의 mmproj 및 DFlash Draft를 함께 저장합니다."]
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
        [/^(.+) 已就緒$/, "$1 ready"], [/^(.+) 執行中$/, "$1 running"],
        [/^(.+) 已停止$/, "$1 stopped"], [/^(.+) 日誌$/, "$1 log"],
        [/^(.+) 已啟動$/, "$1 started"], [/^修改於 (.+)$/, "Modified $1"],
        [/^更新於 (.+)$/, "Updated $1"]
      ],
      ja: [
        [/^(.+) 已就緒$/, "$1 準備完了"], [/^(.+) 執行中$/, "$1 実行中"],
        [/^(.+) 已停止$/, "$1 停止"], [/^(.+) 日誌$/, "$1 ログ"],
        [/^(.+) 已啟動$/, "$1 を起動しました"], [/^修改於 (.+)$/, "更新日時 $1"],
        [/^更新於 (.+)$/, "更新日時 $1"]
      ],
      ko: [
        [/^(.+) 已就緒$/, "$1 준비됨"], [/^(.+) 執行中$/, "$1 실행 중"],
        [/^(.+) 已停止$/, "$1 중지됨"], [/^(.+) 日誌$/, "$1 로그"],
        [/^(.+) 已啟動$/, "$1 시작됨"], [/^修改於 (.+)$/, "수정: $1"],
        [/^更新於 (.+)$/, "업데이트: $1"]
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

  const observer = new MutationObserver((mutations) => {
    mutations.forEach((mutation) => {
      if (mutation.type === "characterData") translateTextNode(mutation.target);
      mutation.addedNodes.forEach((node) => translateTree(node));
    });
  });

  applyLanguage();
  observer.observe(document.documentElement, { childList: true, characterData: true, subtree: true });
  byId("logoutButton")?.addEventListener("click", logout);
  updateAuthenticationControls();
  window.LlamaLoader = {
    api, byId, showMessage, formatBytes, formatTime, t, setLanguage, getLanguage
  };
})();
