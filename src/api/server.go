package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"LlamaLoader/src/accesscontrol"
	"LlamaLoader/src/appupdate"
	"LlamaLoader/src/appversion"
	"LlamaLoader/src/config"
	"LlamaLoader/src/directorybrowser"
	"LlamaLoader/src/domain"
	"LlamaLoader/src/download"
	"LlamaLoader/src/llamacpp"
	"LlamaLoader/src/netpass"
	"LlamaLoader/src/session"
	"LlamaLoader/src/startupcommand"
	"LlamaLoader/src/systemmetrics"
	"LlamaLoader/src/updatecheck"
)

type Server struct {
	ctx             context.Context
	webPath         string
	reportPath      string
	agentConfigPath string
	settings        *config.Store
	startupCommands *startupcommand.Store
	accessControl   *accesscontrol.Store
	sessions        *session.Store
	downloads       *download.Manager
	llama           *llamacpp.Manager
	updates         *updatecheck.Checker
	localUpdates    *appupdate.Manager
	metrics         *systemmetrics.Collector
	netPass         *netpass.Manager
	credentialsMu   sync.Mutex
}

func NewServer(
	ctx context.Context,
	webPath string,
	agentConfigPath string,
	settings *config.Store,
	startupCommands *startupcommand.Store,
	accessControl *accesscontrol.Store,
	sessions *session.Store,
	downloads *download.Manager,
	llama *llamacpp.Manager,
) *Server {
	updates := updatecheck.New(updatecheck.Options{
		CurrentVersion: appversion.Tag(),
		DisplayVersion: appversion.Current(),
		Repository:     appversion.RepositoryName(),
	})
	updates.Start(ctx)
	metrics := systemmetrics.NewCollector()
	metrics.Start(ctx, 3*time.Second)
	llama.SetMemorySnapshotProvider(func() llamacpp.MemorySnapshot {
		info := metrics.Info()
		snapshot := metrics.Snapshot()
		availableBytes := uint64(0)
		if info.MemoryBytes > 0 && snapshot.Memory.Available {
			availableBytes = min(info.MemoryBytes, snapshot.MemoryAvailableBytes)
		}
		return llamacpp.MemorySnapshot{
			TotalBytes:     info.MemoryBytes,
			AvailableBytes: availableBytes,
		}
	})
	netPassConfigPath := filepath.Join(filepath.Dir(agentConfigPath), "data", "netpass.json")
	netPass := netpass.NewManager(ctx, netPassConfigPath, loadManagementPort(agentConfigPath))
	return &Server{
		ctx:             ctx,
		webPath:         webPath,
		reportPath:      resolveReportPath(webPath),
		agentConfigPath: agentConfigPath,
		settings:        settings,
		startupCommands: startupCommands,
		accessControl:   accessControl,
		sessions:        sessions,
		downloads:       downloads,
		llama:           llama,
		updates:         updates,
		localUpdates:    appupdate.NewManager(),
		metrics:         metrics,
		netPass:         netPass,
	}
}

// resolveReportPath 同時支援原始碼工作區與封裝後目錄：開發模式的
// reports 位於 website 的同層，正式封裝則放在 website/reports。
func resolveReportPath(webPath string) string {
	candidates := []string{
		filepath.Join(webPath, "reports"),
		filepath.Join(filepath.Dir(webPath), "reports"),
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}
	return ""
}

func loadManagementPort(agentConfigPath string) int {
	agentConfig, err := config.LoadAgentConfig(agentConfigPath)
	if err != nil {
		return config.DefaultAgentConfig().HTTPPort
	}
	return agentConfig.HTTPPort
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleRoot)
	mux.HandleFunc("GET /login.html", s.handleLoginPage)
	for _, pageName := range []string{"main.html", "commands.html", "chat.html", "download.html", "settings.html"} {
		mux.HandleFunc("GET /"+pageName, s.requirePage(s.pageHandler(pageName)))
	}
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServer(http.Dir(filepath.Join(s.webPath, "assets")))))
	if s.reportPath != "" {
		reportFiles := http.StripPrefix("/reports/", http.FileServer(http.Dir(s.reportPath)))
		mux.HandleFunc("GET /reports/{$}", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/reports/model-compatibility.html", http.StatusFound)
		})
		mux.Handle("GET /reports/", reportFiles)
	}

	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/system/metrics", s.handleSystemMetrics)
	mux.HandleFunc("GET /api/system/info", s.requireAPI(s.handleSystemInfo))
	mux.HandleFunc("GET /api/netpass/status", s.requireAPI(s.handleNetPassStatus))
	mux.HandleFunc("PUT /api/netpass/config", s.requireAPI(s.handleNetPassConfigUpdate))
	mux.HandleFunc("POST /api/netpass/start", s.requireAPI(s.handleNetPassStart))
	mux.HandleFunc("POST /api/netpass/stop", s.requireAPI(s.handleNetPassStop))
	mux.HandleFunc("GET /api/app-version", s.handleAppVersion)
	mux.HandleFunc("POST /api/app-version/check", s.requireAPI(s.handleAppVersionCheck))
	mux.HandleFunc("GET /api/app-update/status", s.requireAPI(s.handleAppUpdateStatus))
	mux.HandleFunc("POST /api/app-update/upload", s.requireAPI(s.handleAppUpdateUpload))
	mux.HandleFunc("GET /api/session", s.handleSession)
	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("POST /api/logout", s.handleLogout)
	mux.HandleFunc("GET /api/admin-credentials", s.requireAPI(s.handleAdminCredentials))
	mux.HandleFunc("PUT /api/admin-credentials", s.requireAPI(s.handleAdminCredentialsUpdate))
	mux.HandleFunc("GET /api/settings", s.requireAPI(s.handleSettings))
	mux.HandleFunc("PUT /api/settings", s.requireAPI(s.handleSettingsUpdate))
	mux.HandleFunc("PUT /api/settings/resident-mode", s.requireAPI(s.handleResidentModeUpdate))
	mux.HandleFunc("GET /api/access-control", s.requireAPI(s.handleAccessControl))
	mux.HandleFunc("PUT /api/access-control", s.requireAPI(s.handleAccessControlUpdate))
	mux.HandleFunc("POST /api/access-control/keys", s.requireAPI(s.handleAccessKeyIssue))
	mux.HandleFunc("DELETE /api/access-control/keys/{id}", s.requireAPI(s.handleAccessKeyRevoke))
	mux.HandleFunc("POST /api/system/directories", s.requireAPI(s.handleDirectories))
	mux.HandleFunc("GET /api/models", s.requireAPI(s.handleModels))
	mux.HandleFunc("DELETE /api/models", s.requireAPI(s.handleModelDelete))
	mux.HandleFunc("DELETE /api/models/conversion-cache", s.requireAPI(s.handleModelConversionCacheDelete))
	mux.HandleFunc("GET /api/startup-commands", s.requireAPI(s.handleStartupCommands))
	mux.HandleFunc("POST /api/startup-commands", s.requireAPI(s.handleStartupCommandCreate))
	mux.HandleFunc("PUT /api/startup-commands/{id}", s.requireAPI(s.handleStartupCommandUpdate))
	mux.HandleFunc("DELETE /api/startup-commands/{id}", s.requireAPI(s.handleStartupCommandDelete))
	mux.HandleFunc("GET /api/downloads", s.requireAPI(s.handleDownloads))
	mux.HandleFunc("GET /api/downloads/repositories", s.requireAPI(s.handleDownloadRepositorySearch))
	mux.HandleFunc("GET /api/downloads/repository-files", s.requireAPI(s.handleDownloadRepositoryFiles))
	mux.HandleFunc("POST /api/downloads", s.requireAPI(s.handleDownloadStart))
	mux.HandleFunc("DELETE /api/downloads/{id}", s.requireAPI(s.handleDownloadCancel))
	mux.HandleFunc("POST /api/download-favorites", s.requireAPI(s.handleDownloadFavoriteAdd))
	mux.HandleFunc("DELETE /api/download-favorites", s.requireAPI(s.handleDownloadFavoriteDelete))
	mux.HandleFunc("GET /api/llama/status", s.requireAPI(s.handleLlamaStatus))
	mux.HandleFunc("GET /api/llama/logs", s.requireAPI(s.handleLlamaLogs))
	mux.HandleFunc("DELETE /api/llama/logs", s.requireAPI(s.handleLlamaLogsClear))
	mux.HandleFunc("POST /api/llama/start", s.requireAPI(s.handleLlamaStart))
	mux.HandleFunc("POST /api/llama/stop", s.requireAPI(s.handleLlamaStop))
	mux.HandleFunc("GET /api/runtime/status", s.requireAPI(s.handleLlamaStatus))
	mux.HandleFunc("GET /api/runtime/logs", s.requireAPI(s.handleLlamaLogs))
	mux.HandleFunc("DELETE /api/runtime/logs", s.requireAPI(s.handleLlamaLogsClear))
	mux.HandleFunc("POST /api/runtime/start", s.requireAPI(s.handleLlamaStart))
	mux.HandleFunc("GET /api/runtime/calibration", s.requireAPI(s.handleRuntimeCalibrationPlan))
	mux.HandleFunc("PUT /api/runtime/calibration", s.requireAPI(s.handleRuntimeCalibrationSave))
	mux.HandleFunc("POST /api/runtime/conversion-preflight", s.requireAPI(s.handleRuntimeConversionPreflight))
	mux.HandleFunc("POST /api/runtime/stop", s.requireAPI(s.handleLlamaStop))
	mux.HandleFunc("POST /api/chat/completions", s.requireAPI(s.handleChatCompletion))
	return s.securityHeaders(mux)
}

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if s.sessions.Authenticated(r) {
		http.Redirect(w, r, "/main.html", http.StatusFound)
		return
	}
	http.Redirect(w, r, "/login.html", http.StatusFound)
}

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if s.sessions.Authenticated(r) {
		http.Redirect(w, r, "/main.html", http.StatusFound)
		return
	}
	s.servePage(w, r, "login.html")
}

func (s *Server) pageHandler(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.servePage(w, r, name)
	}
}

func (s *Server) servePage(w http.ResponseWriter, r *http.Request, name string) {
	path := filepath.Join(s.webPath, name)
	if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
		http.Error(w, "找不到網站檔案: "+name, http.StatusNotFound)
		return
	}
	http.ServeFile(w, r, path)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "time": time.Now()})
}

func (s *Server) handleSystemMetrics(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.metrics.Snapshot())
}

func (s *Server) handleSystemInfo(w http.ResponseWriter, _ *http.Request) {
	info := s.metrics.Info()
	if agentConfig, err := config.LoadAgentConfig(s.agentConfigPath); err == nil {
		info.ManagementURLs = managementURLs(agentConfig.HTTPHost, agentConfig.HTTPPort, info.Network)
	}
	writeJSON(w, http.StatusOK, info)
}

type netPassStatusResponse struct {
	netpass.Status
	AuthenticationEnabled bool   `json:"authentication_enabled"`
	APIKeyEnabled         bool   `json:"api_key_enabled"`
	AccessKeyCount        int    `json:"access_key_count"`
	SecurityReady         bool   `json:"security_ready"`
	SecurityMessage       string `json:"security_message,omitempty"`
}

func (s *Server) netPassStatus() netPassStatusResponse {
	authenticationEnabled := s.sessions.AuthenticationEnabled()
	accessControl := s.accessControl.Public()
	apiKeyEnabled := accessControl.Policy.APIKeyEnabled && len(accessControl.Keys) > 0
	securityReady := authenticationEnabled && apiKeyEnabled
	status := s.netPass.Status()
	if status.Running && !securityReady {
		_ = s.netPass.Stop()
		status = s.netPass.Status()
	}
	message := ""
	if !authenticationEnabled && !apiKeyEnabled {
		message = "請先開啟管理介面帳號密碼與模型 API Access Key 驗證"
	} else if !authenticationEnabled {
		message = "請先開啟管理介面帳號密碼驗證"
	} else if !apiKeyEnabled {
		message = "請先核發 Access Key 並開啟模型 API 金鑰驗證"
	}
	return netPassStatusResponse{
		Status:                status,
		AuthenticationEnabled: authenticationEnabled,
		APIKeyEnabled:         apiKeyEnabled,
		AccessKeyCount:        len(accessControl.Keys),
		SecurityReady:         securityReady,
		SecurityMessage:       message,
	}
}

func (s *Server) handleNetPassStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.netPassStatus())
}

func (s *Server) handleNetPassConfigUpdate(w http.ResponseWriter, r *http.Request) {
	var request netpass.ConfigUpdate
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if _, err := s.netPass.UpdateConfig(request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, s.netPassStatus())
}

func (s *Server) handleNetPassStart(w http.ResponseWriter, r *http.Request) {
	var request struct {
		AcceptUsagePolicy bool `json:"accept_usage_policy"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if !request.AcceptUsagePolicy {
		writeError(w, http.StatusBadRequest, errors.New("請先閱讀並同意 NetPass 使用政策與責任說明"))
		return
	}
	security := s.netPassStatus()
	if !security.SecurityReady {
		writeError(w, http.StatusBadRequest, errors.New(security.SecurityMessage))
		return
	}
	if _, err := s.netPass.Start(); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, s.netPassStatus())
}

func (s *Server) handleNetPassStop(w http.ResponseWriter, _ *http.Request) {
	if err := s.netPass.Stop(); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, s.netPassStatus())
}

// managementURLs 依管理服務的實際監聽設定列出可直接開啟的 HTTP 入口。
// Wildcard 監聽會展開為 Loopback 與各張已連線網卡的位址，避免對 UI 暴露不可連線的 0.0.0.0 或 ::。
func managementURLs(configuredHost string, port int, interfaces []systemmetrics.NetworkInterface) []string {
	configuredHost = strings.Trim(strings.TrimSpace(configuredHost), "[]")
	hosts := make([]string, 0, len(interfaces)+1)
	if configuredHost != "" && configuredHost != "0.0.0.0" && configuredHost != "::" {
		hosts = append(hosts, configuredHost)
	} else {
		hosts = append(hosts, "127.0.0.1")
		for _, networkInterface := range interfaces {
			if !networkInterface.Up {
				continue
			}
			for _, address := range networkInterface.Addresses {
				ip, _, err := net.ParseCIDR(strings.TrimSpace(address))
				if err != nil {
					ip = net.ParseIP(strings.TrimSpace(address))
				}
				if ip == nil || !ip.IsGlobalUnicast() {
					continue
				}
				hosts = append(hosts, ip.String())
			}
		}
	}

	seen := make(map[string]struct{}, len(hosts))
	result := make([]string, 0, len(hosts))
	for _, host := range hosts {
		host = strings.Trim(strings.TrimSpace(host), "[]")
		if host == "" {
			continue
		}
		managementURL := "http://" + net.JoinHostPort(host, strconv.Itoa(port))
		if _, exists := seen[managementURL]; exists {
			continue
		}
		seen[managementURL] = struct{}{}
		result = append(result, managementURL)
	}
	if len(result) > 1 {
		sort.Strings(result[1:])
	}
	return result
}

func (s *Server) handleAppVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.updates.Check(r.Context(), false))
}

func (s *Server) handleAppVersionCheck(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.updates.Check(r.Context(), true))
}

func (s *Server) handleAppUpdateStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.localUpdates.Status())
}

func (s *Server) handleAppUpdateUpload(w http.ResponseWriter, r *http.Request) {
	if !s.sessions.AuthenticationEnabled() {
		writeError(w, http.StatusForbidden, errors.New("透過 ZIP 更新前必須先開啟管理介面登入驗證"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, appupdate.MaxUploadBytes+(1<<20))
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		status := http.StatusBadRequest
		if strings.Contains(strings.ToLower(err.Error()), "too large") {
			status = http.StatusRequestEntityTooLarge
		}
		writeError(w, status, fmt.Errorf("讀取更新 ZIP 失敗: %w", err))
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	archive, header, err := r.FormFile("update_zip")
	if err != nil {
		writeError(w, http.StatusBadRequest, errors.New("請選擇 Tanpopo 發布 ZIP"))
		return
	}
	defer archive.Close()
	if !strings.EqualFold(filepath.Ext(strings.TrimSpace(header.Filename)), ".zip") {
		writeError(w, http.StatusBadRequest, errors.New("更新檔案必須是 ZIP"))
		return
	}
	status, err := s.localUpdates.Start(archive)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusAccepted, status)
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{
		"authenticated":          s.sessions.Authenticated(r),
		"authentication_enabled": s.sessions.AuthenticationEnabled(),
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Account    string `json:"account"`
		Password   string `json:"password"`
		RememberMe bool   `json:"remember_me"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if !s.sessions.Login(w, r, request.Account, request.Password, request.RememberMe) {
		writeError(w, http.StatusUnauthorized, errors.New("帳號或密碼錯誤"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.sessions.Logout(w, r)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleAdminCredentials(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"account":                s.sessions.Account(),
		"authentication_enabled": s.sessions.AuthenticationEnabled(),
	})
}

func (s *Server) handleAdminCredentialsUpdate(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Account                        string `json:"account"`
		CurrentPassword                string `json:"current_password"`
		Password                       string `json:"password"`
		AuthenticationEnabled          bool   `json:"authentication_enabled"`
		DisableAuthenticationConfirmed bool   `json:"disable_authentication_confirmed"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	request.Account = strings.TrimSpace(request.Account)
	if request.Account == "" {
		writeError(w, http.StatusBadRequest, errors.New("管理帳號不可為空"))
		return
	}

	s.credentialsMu.Lock()
	defer s.credentialsMu.Unlock()

	currentAuthenticationEnabled := s.sessions.AuthenticationEnabled()
	disablingAuthenticationOnly := currentAuthenticationEnabled &&
		!request.AuthenticationEnabled &&
		request.DisableAuthenticationConfirmed &&
		request.Password == "" &&
		request.Account == s.sessions.Account()
	if currentAuthenticationEnabled && !disablingAuthenticationOnly && !s.sessions.VerifyPassword(request.CurrentPassword) {
		writeError(w, http.StatusForbidden, errors.New("目前密碼不正確"))
		return
	}
	if currentAuthenticationEnabled && !request.AuthenticationEnabled && !request.DisableAuthenticationConfirmed {
		writeError(w, http.StatusBadRequest, errors.New("關閉登入驗證前必須明確確認"))
		return
	}
	if !currentAuthenticationEnabled && request.AuthenticationEnabled && request.Password == "" {
		writeError(w, http.StatusBadRequest, errors.New("重新啟用登入驗證時，請設定一組新密碼"))
		return
	}

	updated, err := config.UpdateAgentSecurity(
		s.agentConfigPath,
		!request.AuthenticationEnabled,
		request.Account,
		request.Password,
		request.Password != "",
	)
	if err == nil {
		s.sessions.UpdateSecurity(
			!updated.DisableAuthentication,
			updated.DefaultAccount,
			updated.DefaultPassword,
		)
		if updated.DisableAuthentication {
			_ = s.netPass.Stop()
		}
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                     true,
		"account":                updated.DefaultAccount,
		"authentication_enabled": !updated.DisableAuthentication,
	})
}

func (s *Server) handleSettings(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.settings.Public())
}

func (s *Server) handleResidentModeUpdate(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Enabled bool `json:"enabled"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	value := s.settings.Get()
	value.ResidentMode = request.Enabled
	if err := s.settings.Save(value); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, s.settings.Public())
}

type settingsUpdateRequest struct {
	ModelDirectory          string  `json:"model_directory"`
	MLXModelDirectory       string  `json:"mlx_model_directory"`
	ResidentMode            bool    `json:"resident_mode"`
	DefaultFastGGUFEnabled  *bool   `json:"default_fast_gguf_enabled"`
	DefaultFastGGUFStrategy *string `json:"default_fast_gguf_strategy"`
	DefaultKVCacheEnabled   *bool   `json:"default_kv_cache_quantization_enabled"`
	DefaultMMapEnabled      *bool   `json:"default_mmap_enabled"`
	DefaultDFlashEnabled    *bool   `json:"default_dflash_enabled"`
	RemoveOriginalGGUF      *bool   `json:"remove_original_gguf_after_conversion"`
	AutoCalibrationEnabled  *bool   `json:"auto_performance_calibration_enabled"`
	MemoryProtectionEnabled *bool   `json:"memory_pressure_protection_enabled"`
	UILanguage              string  `json:"ui_language"`
	UITheme                 string  `json:"ui_theme"`
	HuggingFaceEndpoint     string  `json:"huggingface_endpoint"`
	HuggingFaceToken        string  `json:"huggingface_token"`
	ClearHuggingFaceToken   bool    `json:"clear_huggingface_token"`
	DefaultRevision         string  `json:"default_revision"`
}

func (s *Server) handleSettingsUpdate(w http.ResponseWriter, r *http.Request) {
	var request settingsUpdateRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	current := s.settings.Get()
	token := current.HuggingFaceToken
	if request.ClearHuggingFaceToken {
		token = ""
	} else if strings.TrimSpace(request.HuggingFaceToken) != "" {
		token = strings.TrimSpace(request.HuggingFaceToken)
	}
	defaultFastGGUFEnabled := current.DefaultFastGGUFEnabled
	if request.DefaultFastGGUFEnabled != nil {
		defaultFastGGUFEnabled = *request.DefaultFastGGUFEnabled
	}
	defaultFastGGUFStrategy := current.DefaultFastGGUFStrategy
	if request.DefaultFastGGUFStrategy != nil {
		defaultFastGGUFStrategy = *request.DefaultFastGGUFStrategy
	}
	defaultKVCacheEnabled := current.DefaultKVCacheEnabled
	if request.DefaultKVCacheEnabled != nil {
		defaultKVCacheEnabled = *request.DefaultKVCacheEnabled
	}
	defaultMMapEnabled := current.DefaultMMapEnabled
	if request.DefaultMMapEnabled != nil {
		defaultMMapEnabled = *request.DefaultMMapEnabled
	}
	defaultDFlashEnabled := current.DefaultDFlashEnabled
	if request.DefaultDFlashEnabled != nil {
		defaultDFlashEnabled = *request.DefaultDFlashEnabled
	}
	removeOriginalGGUF := current.RemoveOriginalGGUF
	if request.RemoveOriginalGGUF != nil {
		removeOriginalGGUF = *request.RemoveOriginalGGUF
	}
	autoCalibrationEnabled := current.AutoCalibrationEnabled
	if request.AutoCalibrationEnabled != nil {
		autoCalibrationEnabled = *request.AutoCalibrationEnabled
	}
	memoryProtectionEnabled := current.MemoryProtectionEnabled
	if request.MemoryProtectionEnabled != nil {
		memoryProtectionEnabled = *request.MemoryProtectionEnabled
	}
	value := domain.Settings{
		ModelDirectory:          request.ModelDirectory,
		MLXModelDirectory:       request.MLXModelDirectory,
		ResidentMode:            request.ResidentMode,
		DefaultFastGGUFEnabled:  defaultFastGGUFEnabled,
		DefaultFastGGUFStrategy: defaultFastGGUFStrategy,
		DefaultKVCacheEnabled:   defaultKVCacheEnabled,
		DefaultMMapEnabled:      defaultMMapEnabled,
		DefaultDFlashEnabled:    defaultDFlashEnabled,
		RemoveOriginalGGUF:      removeOriginalGGUF,
		AutoCalibrationEnabled:  autoCalibrationEnabled,
		MemoryProtectionEnabled: memoryProtectionEnabled,
		UILanguage:              request.UILanguage,
		UITheme:                 request.UITheme,
		HuggingFaceEndpoint:     request.HuggingFaceEndpoint,
		HuggingFaceToken:        token,
		DefaultRevision:         request.DefaultRevision,
		ServerHost:              current.ServerHost,
		ServerPort:              current.ServerPort,
		ContextSize:             current.ContextSize,
		GPULayers:               current.GPULayers,
		Threads:                 current.Threads,
		ExtraArgs:               current.ExtraArgs,
		DownloadFavorites:       current.DownloadFavorites,
		PerformanceCalibrations: current.PerformanceCalibrations,
	}
	if err := s.settings.Save(value); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, s.settings.Public())
}

func (s *Server) handleAccessControl(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.accessControl.Public())
}

func (s *Server) handleAccessControlUpdate(w http.ResponseWriter, r *http.Request) {
	var request struct {
		APIKeyEnabled      bool     `json:"api_key_enabled"`
		IPAllowlistEnabled bool     `json:"ip_allowlist_enabled"`
		IPAllowlist        []string `json:"ip_allowlist"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	view, err := s.accessControl.UpdatePolicy(accesscontrol.Policy{
		APIKeyEnabled:      request.APIKeyEnabled,
		IPAllowlistEnabled: request.IPAllowlistEnabled,
		IPAllowlist:        request.IPAllowlist,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if !view.Policy.APIKeyEnabled {
		_ = s.netPass.Stop()
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) handleAccessKeyIssue(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	issued, err := s.accessControl.IssueKey(request.Name)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, issued)
}

func (s *Server) handleAccessKeyRevoke(w http.ResponseWriter, r *http.Request) {
	if err := s.accessControl.RevokeKey(r.PathValue("id")); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if len(s.accessControl.Public().Keys) == 0 {
		_ = s.netPass.Stop()
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleDirectories(w http.ResponseWriter, r *http.Request) {
	var request struct {
		CurrentPath string `json:"current_path"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	listing, err := directorybrowser.List(request.CurrentPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, listing)
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	settings := s.settings.Get()
	runtimeName := strings.TrimSpace(r.URL.Query().Get("runtime"))
	var models []domain.ModelFile
	var err error
	if runtimeName == domain.RuntimeMLXServer {
		if strings.TrimSpace(r.URL.Query().Get("role")) == "draft" {
			models, err = llamacpp.ListMLXDraftModels(settings.MLXModelDirectory)
		} else {
			models, err = llamacpp.ListMLXRuntimeModels(
				settings.MLXModelDirectory,
				settings.ModelDirectory,
			)
		}
	} else {
		models, err = llamacpp.ListModels(settings.ModelDirectory)
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if runtimeName == domain.RuntimeMLXServer && strings.TrimSpace(r.URL.Query().Get("role")) != "draft" {
		models = s.llama.AttachGGUFConversionCacheInfo(models, settings.ModelDirectory)
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": models})
}

func (s *Server) handleModelDelete(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Format string `json:"format"`
		Path   string `json:"path"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	status := s.llama.Status()
	if status.Running && activeModelMatches(status, request.Format, request.Path) {
		writeError(w, http.StatusConflict, errors.New("此模型正在使用中，請先停止模型服務"))
		return
	}
	settings := s.settings.Get()
	if err := llamacpp.DeleteStoredModel(
		settings.MLXModelDirectory,
		settings.ModelDirectory,
		request.Format,
		request.Path,
	); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleModelConversionCacheDelete(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Path string `json:"path"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	status := s.llama.Status()
	if status.Running && activeModelMatches(status, "gguf", request.Path) {
		writeError(w, http.StatusConflict, errors.New("此模型正在使用中，請先停止模型服務再移除 Fast GGUF"))
		return
	}
	settings := s.settings.Get()
	deletedBytes, deletedCount, err := s.llama.DeleteGGUFConversionCache(
		settings.ModelDirectory,
		request.Path,
	)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":            true,
		"deleted_bytes": deletedBytes,
		"deleted_count": deletedCount,
	})
}

func activeModelMatches(status domain.LlamaStatus, format, modelPath string) bool {
	format = strings.ToLower(strings.TrimSpace(format))
	modelPath = filepath.ToSlash(strings.TrimSpace(modelPath))
	modelPath = strings.TrimPrefix(modelPath, "gguf:")
	activePath := filepath.ToSlash(strings.TrimSpace(status.Model))
	activeFormat := "gguf"
	if status.Runtime == domain.RuntimeMLXServer && !strings.HasPrefix(activePath, "gguf:") {
		activeFormat = "mlx"
	}
	if activeFormat != format {
		return false
	}
	activePath = strings.TrimPrefix(activePath, "gguf:")
	if format == "mlx" {
		return activePath == modelPath || strings.HasPrefix(activePath, modelPath+"/")
	}
	separator := strings.Index(modelPath, "/")
	if separator < 0 {
		return activePath == modelPath
	}
	modelDirectory := modelPath[:separator]
	return activePath == modelDirectory || strings.HasPrefix(activePath, modelDirectory+"/")
}

type startupCommandRequest struct {
	Name                string   `json:"name"`
	Runtime             string   `json:"runtime"`
	DraftModel          string   `json:"draft_model"`
	ServerHost          string   `json:"server_host"`
	ServerPort          int      `json:"server_port"`
	ContextSize         int      `json:"context_size"`
	GPULayers           int      `json:"gpu_layers"`
	Threads             int      `json:"threads"`
	MMapReserveGB       int      `json:"mmap_reserve_gb"`
	KVCacheQuantization string   `json:"kv_cache_quantization"`
	ExtraArgs           []string `json:"extra_args"`
}

func (r startupCommandRequest) command() domain.StartupCommand {
	return domain.StartupCommand{
		Name:                r.Name,
		Runtime:             r.Runtime,
		DraftModel:          r.DraftModel,
		ServerHost:          r.ServerHost,
		ServerPort:          r.ServerPort,
		ContextSize:         r.ContextSize,
		GPULayers:           r.GPULayers,
		Threads:             r.Threads,
		MMapReserveGB:       r.MMapReserveGB,
		KVCacheQuantization: r.KVCacheQuantization,
		ExtraArgs:           r.ExtraArgs,
	}
}

func (s *Server) handleStartupCommands(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"commands": s.startupCommands.List()})
}

func (s *Server) handleStartupCommandCreate(w http.ResponseWriter, r *http.Request) {
	var request startupCommandRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	command, err := s.startupCommands.Create(request.command())
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, command)
}

func (s *Server) handleStartupCommandUpdate(w http.ResponseWriter, r *http.Request) {
	var request startupCommandRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	command, err := s.startupCommands.Update(r.PathValue("id"), request.command())
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, command)
}

func (s *Server) handleStartupCommandDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	status := s.llama.Status()
	if status.Running && status.StartupCommandID == id {
		writeError(w, http.StatusConflict, errors.New("此啟動參數正在使用中，請先停止模型服務"))
		return
	}
	if err := s.startupCommands.Delete(id); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleDownloads(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"downloads": s.downloads.List()})
}

func (s *Server) handleDownloadRepositorySearch(w http.ResponseWriter, r *http.Request) {
	settings := s.settings.Get()
	results, err := s.downloads.SearchRepositories(r.Context(), download.Request{
		Runtime:  strings.TrimSpace(r.URL.Query().Get("runtime")),
		Revision: "main",
		Endpoint: settings.HuggingFaceEndpoint,
		Token:    settings.HuggingFaceToken,
	}, r.URL.Query().Get("query"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"repositories": results})
}

func (s *Server) handleDownloadRepositoryFiles(w http.ResponseWriter, r *http.Request) {
	settings := s.settings.Get()
	revision := strings.TrimSpace(r.URL.Query().Get("revision"))
	if revision == "" {
		revision = settings.DefaultRevision
	}
	files, err := s.downloads.ListGGUFFiles(r.Context(), download.Request{
		Repository: strings.TrimSpace(r.URL.Query().Get("repository")),
		Revision:   revision,
		Endpoint:   settings.HuggingFaceEndpoint,
		Token:      settings.HuggingFaceToken,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": files})
}

func decodeDownloadFavorite(r *http.Request) (domain.DownloadFavorite, error) {
	var favorite domain.DownloadFavorite
	if err := decodeJSON(r, &favorite); err != nil {
		return domain.DownloadFavorite{}, err
	}
	favorite.Runtime = strings.ToLower(strings.TrimSpace(favorite.Runtime))
	favorite.Repository = strings.TrimSpace(favorite.Repository)
	favorite.Revision = strings.TrimSpace(favorite.Revision)
	if favorite.Runtime != domain.RuntimeLlamaServer && favorite.Runtime != domain.RuntimeMLXServer {
		return domain.DownloadFavorite{}, errors.New("runtime 僅支援 llama-server 或 mlx-server")
	}
	if err := download.ValidateRepositoryCoordinates(favorite.Repository, favorite.Revision); err != nil {
		return domain.DownloadFavorite{}, err
	}
	return favorite, nil
}

func sameDownloadFavorite(left, right domain.DownloadFavorite) bool {
	return left.Runtime == right.Runtime &&
		strings.EqualFold(left.Repository, right.Repository) &&
		left.Revision == right.Revision
}

func (s *Server) handleDownloadFavoriteAdd(w http.ResponseWriter, r *http.Request) {
	favorite, err := decodeDownloadFavorite(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	settings := s.settings.Get()
	for _, existing := range settings.DownloadFavorites {
		if sameDownloadFavorite(existing, favorite) {
			writeJSON(w, http.StatusOK, map[string]any{"favorites": settings.DownloadFavorites})
			return
		}
	}
	settings.DownloadFavorites = append([]domain.DownloadFavorite{favorite}, settings.DownloadFavorites...)
	if err := s.settings.Save(settings); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"favorites": s.settings.Get().DownloadFavorites})
}

func (s *Server) handleDownloadFavoriteDelete(w http.ResponseWriter, r *http.Request) {
	favorite, err := decodeDownloadFavorite(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	settings := s.settings.Get()
	filtered := settings.DownloadFavorites[:0]
	for _, existing := range settings.DownloadFavorites {
		if !sameDownloadFavorite(existing, favorite) {
			filtered = append(filtered, existing)
		}
	}
	settings.DownloadFavorites = filtered
	if err := s.settings.Save(settings); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"favorites": s.settings.Get().DownloadFavorites})
}

func (s *Server) handleDownloadCancel(w http.ResponseWriter, r *http.Request) {
	if err := s.downloads.Cancel(strings.TrimSpace(r.PathValue("id"))); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleDownloadStart(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Runtime    string `json:"runtime"`
		Repository string `json:"repository"`
		Filename   string `json:"filename"`
		Revision   string `json:"revision"`
		Overwrite  bool   `json:"overwrite"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	settings := s.settings.Get()
	if strings.TrimSpace(request.Revision) == "" {
		request.Revision = settings.DefaultRevision
	}
	downloadRequest := download.Request{
		Runtime:        request.Runtime,
		Repository:     request.Repository,
		Filename:       request.Filename,
		Revision:       request.Revision,
		ModelDirectory: settings.ModelDirectory,
		Endpoint:       settings.HuggingFaceEndpoint,
		Token:          settings.HuggingFaceToken,
		Overwrite:      request.Overwrite,
	}
	var result download.BatchResult
	var err error
	if strings.TrimSpace(request.Runtime) == domain.RuntimeMLXServer {
		downloadRequest.ModelDirectory = settings.MLXModelDirectory
		result, err = s.downloads.StartRepository(s.ctx, downloadRequest)
	} else {
		downloadRequest.Runtime = domain.RuntimeLlamaServer
		result, err = s.downloads.StartWithCompanions(s.ctx, downloadRequest)
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusAccepted, result)
}

func (s *Server) handleLlamaStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.llama.Status())
}

func (s *Server) handleLlamaLogs(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"logs": s.llama.Logs()})
}

func (s *Server) handleLlamaLogsClear(w http.ResponseWriter, _ *http.Request) {
	s.llama.ClearLogs()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleLlamaStart(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Model                      string                    `json:"model"`
		MMProj                     string                    `json:"mmproj"`
		DraftModel                 string                    `json:"draft_model"`
		DFlashEnabled              bool                      `json:"dflash_enabled"`
		MMapEnabled                bool                      `json:"mmap_enabled"`
		FastGGUF                   bool                      `json:"fast_gguf_enabled"`
		KVCacheQuantizationEnabled bool                      `json:"kv_cache_quantization_enabled"`
		SkipGGUFConversionCache    bool                      `json:"skip_gguf_conversion_cache"`
		ConversionConfirmationKey  string                    `json:"conversion_confirmation_key"`
		StartupCommandID           string                    `json:"startup_command_id"`
		SkipSavedCalibration       bool                      `json:"skip_saved_calibration"`
		CalibrationOverride        *domain.PerformanceTuning `json:"calibration_override"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	startupCommand, err := s.startupCommands.Get(request.StartupCommandID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	calibrationApplied := false
	if request.CalibrationOverride != nil {
		if err := validatePerformanceTuning(startupCommand.Runtime, *request.CalibrationOverride); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		startupCommand = applyPerformanceTuning(startupCommand, *request.CalibrationOverride)
	} else if !request.SkipSavedCalibration {
		settings := s.settings.Get()
		if settings.AutoCalibrationEnabled {
			key, _, _ := performanceCalibrationIdentity(s.metrics.Info(), request.Model, startupCommand)
			if profile, ok := findPerformanceCalibration(settings.PerformanceCalibrations, key); ok {
				startupCommand = applyPerformanceTuning(startupCommand, profile.Tuning)
				calibrationApplied = true
			}
		}
	}
	status, err := s.llama.Start(
		request.Model,
		request.MMProj,
		request.DraftModel,
		request.DFlashEnabled,
		request.MMapEnabled,
		request.FastGGUF,
		request.KVCacheQuantizationEnabled,
		request.SkipGGUFConversionCache,
		request.ConversionConfirmationKey,
		startupCommand,
	)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	status = s.llama.AnnotatePerformanceCalibration(calibrationApplied)
	writeJSON(w, http.StatusAccepted, status)
}

func (s *Server) handleRuntimeConversionPreflight(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Model            string `json:"model"`
		MMProj           string `json:"mmproj"`
		FastGGUF         bool   `json:"fast_gguf_enabled"`
		StartupCommandID string `json:"startup_command_id"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	startupCommand, err := s.startupCommands.Get(request.StartupCommandID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	inspection, err := s.llama.InspectConversion(
		ctx,
		request.Model,
		request.MMProj,
		request.FastGGUF,
		startupCommand,
	)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, inspection)
}

func (s *Server) handleLlamaStop(w http.ResponseWriter, _ *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	if err := s.llama.Stop(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, s.llama.Status())
}

func (s *Server) requireAPI(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.sessions.Authenticated(r) {
			writeError(w, http.StatusUnauthorized, errors.New("登入狀態已失效"))
			return
		}
		next(w, r)
	}
}

func (s *Server) requirePage(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.sessions.Authenticated(r) {
			http.Redirect(w, r, "/login.html", http.StatusFound)
			return
		}
		next(w, r)
	}
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; font-src 'self'; script-src 'self'; img-src 'self' data:; connect-src 'self'")
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasSuffix(r.URL.Path, ".html") {
			w.Header().Set("Cache-Control", "no-store")
		} else if strings.HasPrefix(r.URL.Path, "/assets/") || strings.HasPrefix(r.URL.Path, "/reports/") {
			w.Header().Set("Cache-Control", "no-cache, max-age=0, must-revalidate")
		}
		next.ServeHTTP(w, r)
	})
}

func decodeJSON(r *http.Request, destination any) error {
	defer r.Body.Close()
	decoder := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1024*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("JSON 格式錯誤: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("請求只能包含一個 JSON 物件")
		}
		return fmt.Errorf("JSON 結尾格式錯誤: %w", err)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{"message": err.Error()},
	})
}
