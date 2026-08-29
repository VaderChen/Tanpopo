package download

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"LlamaLoader/src/domain"
)

const (
	maxRememberedJobs      = 50
	defaultDownloadChunk   = int64(64 << 20)
	defaultDownloadWorkers = 4
	progressReportBatch    = int64(1 << 20)
)

var (
	repositoryPattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*$`)
	filenameTokenPattern = regexp.MustCompile(`[^a-z0-9]+`)
	contentRangePattern  = regexp.MustCompile(`(?i)^bytes\s+(\d+)-(\d+)/(\d+)$`)
)

type Request struct {
	Runtime        string
	Repository     string
	Filename       string
	Revision       string
	ModelDirectory string
	Endpoint       string
	Token          string
	Overwrite      bool
	LocalDirectory string
}

type BatchResult struct {
	Jobs     []domain.DownloadJob `json:"jobs"`
	Detected []string             `json:"detected_companions"`
	Skipped  []string             `json:"skipped_companions,omitempty"`
	Warnings []string             `json:"warnings,omitempty"`
}

type repositoryInfo struct {
	ID       string   `json:"id"`
	Tags     []string `json:"tags"`
	Siblings []struct {
		Filename string `json:"rfilename"`
	} `json:"siblings"`
}

type modelConfiguration struct {
	Architectures   []string `json:"architectures"`
	ModelType       string   `json:"model_type"`
	HiddenSize      int      `json:"hidden_size"`
	VocabularySize  int      `json:"vocab_size"`
	NumberOfLayers  int      `json:"num_hidden_layers"`
	NumberOfTargets int      `json:"num_target_layers"`
}

type dflashRepository struct {
	Info          repositoryInfo
	Configuration modelConfiguration
	Revision      string
}

type Manager struct {
	mu            sync.RWMutex
	wg            sync.WaitGroup
	jobs          map[string]domain.DownloadJob
	activeTargets map[string]string
	cancellations map[string]context.CancelFunc
	client        *http.Client
	slots         chan struct{}
	rangeSlots    chan struct{}
	chunkSize     int64
	chunkWorkers  int
}

func NewManager(maxConcurrent int) *Manager {
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          16,
		MaxIdleConnsPerHost:   defaultDownloadWorkers,
		MaxConnsPerHost:       defaultDownloadWorkers,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	}
	return &Manager{
		jobs:          make(map[string]domain.DownloadJob),
		activeTargets: make(map[string]string),
		cancellations: make(map[string]context.CancelFunc),
		client:        &http.Client{Transport: transport},
		slots:         make(chan struct{}, maxConcurrent),
		rangeSlots:    make(chan struct{}, defaultDownloadWorkers),
		chunkSize:     defaultDownloadChunk,
		chunkWorkers:  defaultDownloadWorkers,
	}
}

func (m *Manager) Start(ctx context.Context, request Request) (domain.DownloadJob, error) {
	request = normalizeRequest(request)
	if err := validateRequest(request); err != nil {
		return domain.DownloadJob{}, err
	}
	destination, err := SafeJoin(request.ModelDirectory, localDestination(request))
	if err != nil {
		return domain.DownloadJob{}, err
	}
	return m.startValidated(ctx, request, destination)
}

func (m *Manager) StartWithCompanions(ctx context.Context, request Request) (BatchResult, error) {
	request = normalizeRequest(request)
	if err := validateRequest(request); err != nil {
		return BatchResult{}, err
	}

	companions, targetInfo, discoveryErr := m.discoverCompanions(ctx, request)
	result := BatchResult{Detected: append([]string(nil), companions...)}
	if discoveryErr != nil {
		result.Warnings = append(result.Warnings, "附屬檔案檢查失敗: "+discoveryErr.Error())
	}

	mainJob, err := m.Start(ctx, request)
	if err != nil {
		return BatchResult{}, err
	}
	result.Jobs = append(result.Jobs, mainJob)

	for _, filename := range companions {
		companionRequest := request
		companionRequest.Filename = filename
		destination, joinErr := SafeJoin(companionRequest.ModelDirectory, localDestination(companionRequest))
		if joinErr != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s: %v", filename, joinErr))
			continue
		}
		if !companionRequest.Overwrite {
			if info, statErr := os.Stat(destination); statErr == nil && info.Mode().IsRegular() {
				result.Skipped = append(result.Skipped, filename)
				continue
			}
		}
		job, startErr := m.startValidated(ctx, companionRequest, destination)
		if startErr != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s: %v", filename, startErr))
			continue
		}
		result.Jobs = append(result.Jobs, job)
	}

	if !containsDFlashFilename(companions) && discoveryErr == nil {
		draft, draftErr := m.discoverDFlashRepository(ctx, request, targetInfo)
		if draftErr != nil {
			result.Warnings = append(result.Warnings, "DFlash Draft 自動偵測失敗: "+draftErr.Error())
		} else if draft != nil {
			filename := selectDFlashGGUF(request.Filename, draft.Info)
			if filename != "" {
				result.Detected = append(result.Detected, draft.Info.ID+"/"+filename)
				draftRequest := request
				draftRequest.Repository = draft.Info.ID
				draftRequest.Revision = draft.Revision
				draftRequest.Filename = filename
				draftRequest.LocalDirectory = request.LocalDirectory
				m.queueCompanion(ctx, &result, draftRequest, filename)
			}
		}
	}
	return result, nil
}

// StartRepository 下載可由 MLX Swift 直接載入的 Hugging Face 模型快照。
// 所有必要檔案會保留 repository 相對路徑，並放在同一個獨立目錄。
func (m *Manager) StartRepository(ctx context.Context, request Request) (BatchResult, error) {
	request = normalizeRequest(request)
	request.Runtime = domain.RuntimeMLXServer
	request.Filename = "placeholder"
	if err := validateRequest(request); err != nil {
		return BatchResult{}, err
	}

	info, err := m.fetchRepositoryInfo(ctx, request)
	if err != nil {
		return BatchResult{}, err
	}
	files, err := selectMLXRepositoryFiles(info)
	if err != nil {
		return BatchResult{}, err
	}
	localDirectory := mlxStorageDirectory(request.Repository, request.Revision)
	result := BatchResult{Detected: append([]string(nil), files...)}
	m.queueRepositoryFiles(ctx, &result, request, files, localDirectory, "")

	targetConfiguration, configurationErr := m.fetchModelConfiguration(ctx, request, info)
	if configurationErr == nil && isSupportedDFlashTargetConfiguration(targetConfiguration) {
		draft, draftErr := m.discoverDFlashRepositoryWithConfiguration(
			ctx,
			request,
			info,
			targetConfiguration,
		)
		if draftErr != nil {
			result.Warnings = append(result.Warnings, "DFlash Draft 自動偵測失敗: "+draftErr.Error())
		} else if draft != nil {
			draftFiles, selectErr := selectMLXRepositoryFiles(draft.Info)
			if selectErr != nil {
				result.Warnings = append(result.Warnings, "DFlash Draft 檔案清單無效: "+selectErr.Error())
			} else {
				draftRequest := request
				draftRequest.Repository = draft.Info.ID
				draftRequest.Revision = draft.Revision
				draftDirectory := mlxStorageDirectory(draftRequest.Repository, draftRequest.Revision)
				m.queueRepositoryFiles(
					ctx,
					&result,
					draftRequest,
					draftFiles,
					draftDirectory,
					draft.Info.ID+"/",
				)
			}
		}
	}
	if len(result.Jobs) == 0 && len(result.Skipped) == 0 {
		return BatchResult{}, errors.New("repository 沒有可下載的 MLX 模型檔案")
	}
	return result, nil
}

func (m *Manager) queueRepositoryFiles(
	ctx context.Context,
	result *BatchResult,
	request Request,
	files []string,
	localDirectory string,
	displayPrefix string,
) {
	for _, filename := range files {
		fileRequest := request
		fileRequest.Filename = filename
		fileRequest.LocalDirectory = localDirectory
		result.Detected = appendUnique(result.Detected, displayPrefix+filename)
		m.queueCompanion(ctx, result, fileRequest, displayPrefix+filename)
	}
}

func (m *Manager) queueCompanion(
	ctx context.Context,
	result *BatchResult,
	request Request,
	displayName string,
) {
	destination, joinErr := SafeJoin(request.ModelDirectory, localDestination(request))
	if joinErr != nil {
		result.Warnings = append(result.Warnings, fmt.Sprintf("%s: %v", displayName, joinErr))
		return
	}
	if !request.Overwrite {
		if existing, statErr := os.Stat(destination); statErr == nil && existing.Mode().IsRegular() {
			result.Skipped = append(result.Skipped, displayName)
			return
		}
	}
	job, startErr := m.startValidated(ctx, request, destination)
	if startErr != nil {
		result.Warnings = append(result.Warnings, fmt.Sprintf("%s: %v", displayName, startErr))
		return
	}
	result.Jobs = append(result.Jobs, job)
}

func normalizeRequest(request Request) Request {
	request.Runtime = strings.TrimSpace(request.Runtime)
	if request.Runtime == "" {
		request.Runtime = domain.RuntimeLlamaServer
	}
	request.Repository = strings.TrimSpace(request.Repository)
	request.Filename = strings.TrimSpace(strings.ReplaceAll(request.Filename, "\\", "/"))
	request.Revision = strings.TrimSpace(request.Revision)
	request.ModelDirectory = strings.TrimSpace(request.ModelDirectory)
	request.Endpoint = strings.TrimRight(strings.TrimSpace(request.Endpoint), "/")
	if request.Revision == "" {
		request.Revision = "main"
	}
	request.LocalDirectory = strings.Trim(strings.TrimSpace(strings.ReplaceAll(request.LocalDirectory, "\\", "/")), "/")
	if request.LocalDirectory == "" {
		request.LocalDirectory = modelStorageDirectory(request.Repository, request.Revision, request.Filename)
	}
	return request
}

func localDestination(request Request) string {
	return path.Join(request.LocalDirectory, request.Filename)
}

func modelStorageDirectory(repository, revision, filename string) string {
	stem := strings.TrimSuffix(path.Base(filename), path.Ext(filename))
	rawName := strings.ReplaceAll(repository, "/", "--") + "--" + strings.ReplaceAll(revision, "/", "--") + "--" + stem
	var cleanName strings.Builder
	lastWasSeparator := false
	for _, character := range rawName {
		allowed := character >= 'A' && character <= 'Z' ||
			character >= 'a' && character <= 'z' ||
			character >= '0' && character <= '9' ||
			character == '.' || character == '_' || character == '-'
		if allowed {
			cleanName.WriteRune(character)
			lastWasSeparator = false
			continue
		}
		if !lastWasSeparator {
			cleanName.WriteByte('-')
			lastWasSeparator = true
		}
	}
	name := strings.Trim(cleanName.String(), ".-_")
	if name == "" {
		name = "model"
	}
	if len(name) > 160 {
		name = strings.TrimRight(name[:160], ".-_")
	}
	digest := sha256.Sum256([]byte(repository + "\x00" + revision + "\x00" + filename))
	return name + "-" + hex.EncodeToString(digest[:4])
}

func mlxStorageDirectory(repository, revision string) string {
	rawName := strings.ReplaceAll(repository, "/", "--") + "--" + strings.ReplaceAll(revision, "/", "--")
	name := strings.Trim(filenameTokenPattern.ReplaceAllString(strings.ToLower(rawName), "-"), "-")
	if name == "" {
		name = "mlx-model"
	}
	if len(name) > 150 {
		name = strings.TrimRight(name[:150], "-._")
	}
	digest := sha256.Sum256([]byte(repository + "\x00" + revision))
	return name + "-" + hex.EncodeToString(digest[:4])
}

func (m *Manager) startValidated(ctx context.Context, request Request, destination string) (domain.DownloadJob, error) {
	if destinationInfo, statErr := os.Stat(destination); statErr == nil {
		if !destinationInfo.Mode().IsRegular() {
			return domain.DownloadJob{}, errors.New("目的路徑已存在且不是一般檔案")
		}
		if !request.Overwrite {
			return domain.DownloadJob{}, errors.New("目的檔案已存在；若需取代請啟用覆寫")
		}
	} else if !os.IsNotExist(statErr) {
		return domain.DownloadJob{}, statErr
	}
	id, err := randomID()
	if err != nil {
		return domain.DownloadJob{}, err
	}
	now := time.Now()
	job := domain.DownloadJob{
		ID:          id,
		Runtime:     request.Runtime,
		Repository:  request.Repository,
		Filename:    request.Filename,
		Revision:    request.Revision,
		Destination: destination,
		State:       "queued",
		BytesTotal:  -1,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	jobContext, cancel := context.WithCancel(ctx)
	m.mu.Lock()
	if _, exists := m.activeTargets[destination]; exists {
		m.mu.Unlock()
		cancel()
		return domain.DownloadJob{}, errors.New("相同目的檔案已有下載工作進行中")
	}
	m.pruneLocked()
	m.jobs[id] = job
	m.activeTargets[destination] = id
	m.cancellations[id] = cancel
	m.mu.Unlock()
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer func() {
			cancel()
			m.mu.Lock()
			delete(m.cancellations, id)
			m.mu.Unlock()
		}()
		m.run(jobContext, id, request, destination)
	}()
	return job, nil
}

// Cancel 只取消指定工作。該工作建立的所有 Range request 共用同一個
// context，因此按一次即可停止所有分段，其他下載不受影響。
func (m *Manager) Cancel(id string) error {
	m.mu.Lock()
	job, exists := m.jobs[id]
	cancel := m.cancellations[id]
	if !exists {
		m.mu.Unlock()
		return errors.New("找不到下載工作")
	}
	if (job.State != "queued" && job.State != "downloading") || cancel == nil {
		m.mu.Unlock()
		return errors.New("下載工作目前無法取消")
	}
	job.State = "cancelling"
	job.UpdatedAt = time.Now()
	m.jobs[id] = job
	m.mu.Unlock()
	cancel()
	return nil
}

func (m *Manager) discoverCompanions(ctx context.Context, request Request) ([]string, repositoryInfo, error) {
	info, err := m.fetchRepositoryInfo(ctx, request)
	if err != nil {
		return nil, repositoryInfo{}, err
	}
	return selectCompanionFilenames(request.Filename, info), info, nil
}

func (m *Manager) fetchRepositoryInfo(ctx context.Context, request Request) (repositoryInfo, error) {
	metadataURL, err := buildRepositoryInfoURL(request.Endpoint, request.Repository, request.Revision)
	if err != nil {
		return repositoryInfo{}, err
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, metadataURL, nil)
	if err != nil {
		return repositoryInfo{}, err
	}
	httpRequest.Header.Set("Accept", "application/json")
	httpRequest.Header.Set("User-Agent", "Tanpopo/1.0")
	if request.Token != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+request.Token)
	}
	response, err := m.client.Do(httpRequest)
	if err != nil {
		return repositoryInfo{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return repositoryInfo{}, fmt.Errorf("Hugging Face metadata 回傳 %s: %s", response.Status, strings.TrimSpace(string(message)))
	}
	var info repositoryInfo
	if err := json.NewDecoder(io.LimitReader(response.Body, 8*1024*1024)).Decode(&info); err != nil {
		return repositoryInfo{}, fmt.Errorf("解析 Hugging Face repository 檔案清單失敗: %w", err)
	}
	if strings.TrimSpace(info.ID) == "" {
		info.ID = request.Repository
	}
	return info, nil
}

func (m *Manager) fetchModelConfiguration(
	ctx context.Context,
	request Request,
	info repositoryInfo,
) (modelConfiguration, error) {
	if !repositoryHasFile(info, "config.json") {
		return modelConfiguration{}, errors.New("repository 缺少 config.json")
	}
	configurationURL, err := buildURL(
		request.Endpoint,
		info.ID,
		request.Revision,
		"config.json",
	)
	if err != nil {
		return modelConfiguration{}, err
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, configurationURL, nil)
	if err != nil {
		return modelConfiguration{}, err
	}
	setHuggingFaceHeaders(httpRequest, request.Token)
	response, err := m.client.Do(httpRequest)
	if err != nil {
		return modelConfiguration{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return modelConfiguration{}, fmt.Errorf("config.json 回傳 %s", response.Status)
	}
	var configuration modelConfiguration
	if err := json.NewDecoder(io.LimitReader(response.Body, 2*1024*1024)).Decode(&configuration); err != nil {
		return modelConfiguration{}, fmt.Errorf("解析 config.json 失敗: %w", err)
	}
	return configuration, nil
}

func setHuggingFaceHeaders(request *http.Request, token string) {
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "Tanpopo/1.0")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
}

func (m *Manager) discoverDFlashRepository(
	ctx context.Context,
	request Request,
	targetInfo repositoryInfo,
) (*dflashRepository, error) {
	targetConfiguration, err := m.resolveTargetConfiguration(ctx, request, targetInfo)
	if err != nil || !isSupportedDFlashTargetConfiguration(targetConfiguration) {
		return nil, nil
	}
	return m.discoverDFlashRepositoryWithConfiguration(
		ctx,
		request,
		targetInfo,
		targetConfiguration,
	)
}

func (m *Manager) resolveTargetConfiguration(
	ctx context.Context,
	request Request,
	targetInfo repositoryInfo,
) (modelConfiguration, error) {
	if configuration, err := m.fetchModelConfiguration(ctx, request, targetInfo); err == nil {
		return configuration, nil
	}
	for _, repository := range baseModelRepositories(targetInfo.Tags) {
		baseRequest := request
		baseRequest.Repository = repository
		baseRequest.Revision = "main"
		info, err := m.fetchRepositoryInfo(ctx, baseRequest)
		if err != nil {
			continue
		}
		if configuration, err := m.fetchModelConfiguration(ctx, baseRequest, info); err == nil {
			return configuration, nil
		}
	}
	return modelConfiguration{}, errors.New("無法取得 Target 模型架構")
}

func (m *Manager) discoverDFlashRepositoryWithConfiguration(
	ctx context.Context,
	request Request,
	targetInfo repositoryInfo,
	targetConfiguration modelConfiguration,
) (*dflashRepository, error) {
	if !isSupportedDFlashTargetConfiguration(targetConfiguration) {
		return nil, nil
	}
	targetRepositories := append([]string{request.Repository}, baseModelRepositories(targetInfo.Tags)...)
	targetIdentities := make(map[string]bool)
	for _, repository := range targetRepositories {
		if identity := dflashPairIdentity(repository); identity != "" {
			targetIdentities[identity] = true
		}
	}

	candidates := make(map[string]repositoryInfo)
	for _, repository := range targetRepositories {
		name := path.Base(repository)
		if name == "" || name == "." {
			continue
		}
		found, err := m.searchRepositories(ctx, request, name+" DFlash")
		if err != nil {
			continue
		}
		for _, candidate := range found {
			candidates[candidate.ID] = candidate
		}
	}

	var selected *dflashRepository
	selectedScore := -1
	for _, candidate := range candidates {
		if candidate.ID == "" || strings.EqualFold(candidate.ID, request.Repository) || !isDFlashRepository(candidate) {
			continue
		}
		score := dflashCandidateScore(candidate, targetRepositories, targetIdentities)
		if score < 0 {
			continue
		}
		candidateRequest := request
		candidateRequest.Repository = candidate.ID
		candidateRequest.Revision = "main"
		if request.Runtime == domain.RuntimeLlamaServer {
			if selectDFlashGGUF(request.Filename, candidate) == "" {
				continue
			}
		} else {
			configuration, err := m.fetchModelConfiguration(ctx, candidateRequest, candidate)
			if err != nil || !isCompatibleDFlashConfiguration(targetConfiguration, configuration) {
				continue
			}
			if score > selectedScore {
				selected = &dflashRepository{Info: candidate, Configuration: configuration, Revision: "main"}
				selectedScore = score
			}
			continue
		}
		if score > selectedScore {
			selected = &dflashRepository{Info: candidate, Revision: "main"}
			selectedScore = score
		}
	}
	return selected, nil
}

func (m *Manager) searchRepositories(
	ctx context.Context,
	request Request,
	query string,
) ([]repositoryInfo, error) {
	searchURL, err := buildModelSearchURL(request.Endpoint, query)
	if err != nil {
		return nil, err
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
		return nil, err
	}
	setHuggingFaceHeaders(httpRequest, request.Token)
	response, err := m.client.Do(httpRequest)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("Hugging Face 模型搜尋回傳 %s", response.Status)
	}
	var repositories []repositoryInfo
	if err := json.NewDecoder(io.LimitReader(response.Body, 16*1024*1024)).Decode(&repositories); err != nil {
		return nil, fmt.Errorf("解析 Hugging Face 模型搜尋結果失敗: %w", err)
	}
	return repositories, nil
}

func baseModelRepositories(tags []string) []string {
	result := make([]string, 0)
	for _, tag := range tags {
		value, found := strings.CutPrefix(strings.TrimSpace(tag), "base_model:")
		if !found {
			continue
		}
		for {
			prefix, remainder, hasRelationship := strings.Cut(value, ":")
			if !hasRelationship || strings.Contains(prefix, "/") {
				break
			}
			value = remainder
		}
		if repositoryPattern.MatchString(value) {
			result = appendUnique(result, value)
		}
	}
	return result
}

func isDFlashRepository(info repositoryInfo) bool {
	if strings.Contains(strings.ToLower(info.ID), "dflash") {
		return true
	}
	for _, tag := range info.Tags {
		normalized := strings.ToLower(strings.TrimSpace(tag))
		if normalized == "dflash" || normalized == "dflash2" || normalized == "speculative-decoding-draft" {
			return true
		}
	}
	return false
}

func dflashCandidateScore(
	candidate repositoryInfo,
	targetRepositories []string,
	targetIdentities map[string]bool,
) int {
	score := -1
	for _, base := range baseModelRepositories(candidate.Tags) {
		for _, target := range targetRepositories {
			if strings.EqualFold(base, target) {
				score = max(score, 10_000)
			}
		}
		if targetIdentities[dflashPairIdentity(base)] {
			score = max(score, 8_000)
		}
	}
	if targetIdentities[dflashPairIdentity(candidate.ID)] {
		score = max(score, 5_000)
	}
	if score < 0 {
		return -1
	}
	if strings.HasPrefix(strings.ToLower(candidate.ID), "z-lab/") {
		score += 100
	}
	if hasTag(candidate.Tags, "dflash2") {
		score += 10
	}
	return score
}

func dflashPairIdentity(repository string) string {
	name := strings.ToLower(path.Base(strings.TrimSpace(repository)))
	if index := strings.Index(name, "dflash"); index >= 0 {
		name = name[:index]
	}
	parts := filenameTokenPattern.Split(name, -1)
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		switch {
		case part == "", part == "gguf", part == "mlx", part == "model", part == "draft":
			continue
		case part == "bf16", part == "fp16", part == "f16", part == "b16":
			continue
		case strings.HasSuffix(part, "bit"):
			continue
		}
		filtered = append(filtered, part)
	}
	if len(filtered) > 1 && filtered[0] == filtered[1] {
		filtered = filtered[1:]
	}
	return strings.Join(filtered, "")
}

func isSupportedDFlashTargetConfiguration(configuration modelConfiguration) bool {
	if isDFlashDraftConfiguration(configuration) {
		return false
	}
	switch normalizeDFlashArchitecture(configuration.ModelType) {
	case "qwen3", "qwen3moe", "qwen35", "qwen35moe":
		return true
	default:
		return false
	}
}

func isCompatibleDFlashConfiguration(target, draft modelConfiguration) bool {
	if !isDFlashDraftConfiguration(draft) || !isSupportedDFlashTargetConfiguration(target) {
		return false
	}
	if draft.NumberOfTargets > 0 && target.NumberOfLayers > 0 && draft.NumberOfTargets != target.NumberOfLayers {
		return false
	}
	if draft.HiddenSize > 0 && target.HiddenSize > 0 && draft.HiddenSize != target.HiddenSize {
		return false
	}
	if draft.VocabularySize > 0 && target.VocabularySize > 0 && draft.VocabularySize != target.VocabularySize {
		return false
	}
	return true
}

func isDFlashDraftConfiguration(configuration modelConfiguration) bool {
	for _, architecture := range configuration.Architectures {
		if architecture == "DFlashDraftModel" || architecture == "DFlash2DraftModel" {
			return true
		}
	}
	return false
}

func normalizeDFlashArchitecture(value string) string {
	value = strings.NewReplacer("_", "", "-", "", ".", "").Replace(strings.ToLower(strings.TrimSpace(value)))
	return value
}

func hasTag(tags []string, expected string) bool {
	for _, tag := range tags {
		if strings.EqualFold(strings.TrimSpace(tag), expected) {
			return true
		}
	}
	return false
}

func repositoryHasFile(info repositoryInfo, expected string) bool {
	for _, sibling := range info.Siblings {
		if strings.EqualFold(strings.TrimSpace(sibling.Filename), expected) {
			return true
		}
	}
	return false
}

func containsDFlashFilename(filenames []string) bool {
	for _, filename := range filenames {
		if strings.Contains(strings.ToLower(path.Base(filename)), "dflash") {
			return true
		}
	}
	return false
}

func selectDFlashGGUF(mainFilename string, info repositoryInfo) string {
	candidates := make([]string, 0)
	for _, sibling := range info.Siblings {
		filename := strings.TrimSpace(strings.ReplaceAll(sibling.Filename, "\\", "/"))
		if filename == "" || !strings.EqualFold(path.Ext(filename), ".gguf") {
			continue
		}
		if _, err := SafeJoin(".", filename); err != nil {
			continue
		}
		candidates = append(candidates, filename)
	}
	return chooseBestCompanion(mainFilename, candidates)
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func selectMLXRepositoryFiles(info repositoryInfo) ([]string, error) {
	files := make([]string, 0, len(info.Siblings))
	seen := make(map[string]bool)
	hasConfig := false
	hasWeights := false
	for _, sibling := range info.Siblings {
		filename := strings.TrimSpace(strings.ReplaceAll(sibling.Filename, "\\", "/"))
		if filename == "" || seen[filename] || !isMLXRuntimeFile(filename) {
			continue
		}
		if _, err := SafeJoin(".", filename); err != nil {
			continue
		}
		seen[filename] = true
		files = append(files, filename)
		base := strings.ToLower(path.Base(filename))
		hasConfig = hasConfig || base == "config.json"
		hasWeights = hasWeights || strings.HasSuffix(base, ".safetensors")
	}
	if !hasConfig || !hasWeights {
		return nil, errors.New("repository 不是完整的 MLX 模型：必須同時包含 config.json 與 safetensors")
	}
	sort.SliceStable(files, func(i, j int) bool {
		leftPriority := mlxFilePriority(files[i])
		rightPriority := mlxFilePriority(files[j])
		if leftPriority != rightPriority {
			return leftPriority < rightPriority
		}
		return strings.ToLower(files[i]) < strings.ToLower(files[j])
	})
	return files, nil
}

func isMLXRuntimeFile(filename string) bool {
	base := strings.ToLower(path.Base(filename))
	if base == ".gitattributes" || strings.HasPrefix(base, "readme") || strings.HasPrefix(base, "license") {
		return false
	}
	switch strings.ToLower(path.Ext(base)) {
	case ".safetensors", ".json", ".model", ".txt", ".tiktoken", ".jinja", ".yaml", ".yml":
		return true
	default:
		return false
	}
}

func mlxFilePriority(filename string) int {
	base := strings.ToLower(path.Base(filename))
	switch {
	case base == "config.json":
		return 0
	case strings.HasSuffix(base, ".json") || strings.HasSuffix(base, ".jinja"):
		return 1
	case strings.HasSuffix(base, ".safetensors"):
		return 3
	default:
		return 2
	}
}

func selectCompanionFilenames(mainFilename string, info repositoryInfo) []string {
	mainFilename = strings.TrimSpace(strings.ReplaceAll(mainFilename, "\\", "/"))
	mainBase := strings.ToLower(path.Base(mainFilename))
	if strings.Contains(mainBase, "mmproj") || strings.Contains(mainBase, "dflash") {
		return nil
	}

	mmprojCandidates := make([]string, 0)
	dflashCandidates := make([]string, 0)
	seen := make(map[string]bool)
	for _, sibling := range info.Siblings {
		filename := strings.TrimSpace(strings.ReplaceAll(sibling.Filename, "\\", "/"))
		if filename == "" || filename == mainFilename || !strings.EqualFold(path.Ext(filename), ".gguf") || seen[filename] {
			continue
		}
		seen[filename] = true
		base := strings.ToLower(path.Base(filename))
		switch {
		case strings.Contains(base, "mmproj"):
			mmprojCandidates = append(mmprojCandidates, filename)
		case strings.Contains(base, "dflash"):
			dflashCandidates = append(dflashCandidates, filename)
		}
	}

	selected := make([]string, 0, 2)
	if filename := chooseBestCompanion(mainFilename, mmprojCandidates); filename != "" {
		selected = append(selected, filename)
	}
	if filename := chooseBestCompanion(mainFilename, dflashCandidates); filename != "" {
		selected = append(selected, filename)
	}
	return selected
}

func chooseBestCompanion(mainFilename string, candidates []string) string {
	if len(candidates) == 0 {
		return ""
	}
	mainTokens := filenameTokenSet(mainFilename)
	mainDirectory := path.Dir(mainFilename)
	sort.Slice(candidates, func(left, right int) bool {
		leftScore := companionScore(mainDirectory, mainTokens, candidates[left])
		rightScore := companionScore(mainDirectory, mainTokens, candidates[right])
		if leftScore != rightScore {
			return leftScore > rightScore
		}
		return strings.ToLower(candidates[left]) < strings.ToLower(candidates[right])
	})
	return candidates[0]
}

func companionScore(mainDirectory string, mainTokens map[string]bool, filename string) int {
	score := 0
	if path.Dir(filename) == mainDirectory {
		score += 1000
	}
	for token := range filenameTokenSet(filename) {
		if mainTokens[token] {
			score += len(token)
		}
	}
	return score
}

func filenameTokenSet(filename string) map[string]bool {
	parts := filenameTokenPattern.Split(strings.ToLower(strings.TrimSuffix(path.Base(filename), path.Ext(filename))), -1)
	result := make(map[string]bool, len(parts))
	for _, part := range parts {
		if part == "" || part == "gguf" || part == "model" || part == "draft" || strings.HasPrefix(part, "mmproj") || strings.HasPrefix(part, "dflash") {
			continue
		}
		result[part] = true
	}
	return result
}

func (m *Manager) Wait(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Manager) List() []domain.DownloadJob {
	m.mu.RLock()
	result := make([]domain.DownloadJob, 0, len(m.jobs))
	for _, job := range m.jobs {
		result = append(result, job)
	}
	m.mu.RUnlock()
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.After(result[j].CreatedAt) })
	return result
}

func (m *Manager) run(ctx context.Context, id string, request Request, destination string) {
	defer m.releaseDestination(destination, id)
	select {
	case m.slots <- struct{}{}:
		defer func() { <-m.slots }()
	case <-ctx.Done():
		m.cancelled(id, destination)
		return
	}

	m.update(id, func(job *domain.DownloadJob) { job.State = "downloading" })
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		m.fail(id, err.Error())
		return
	}
	partPath := destination + ".part-" + id
	file, err := os.OpenFile(partPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		m.fail(id, err.Error())
		return
	}
	removePart := true
	defer func() {
		file.Close()
		if removePart {
			os.Remove(partPath)
		}
	}()

	downloadURL, err := buildURL(request.Endpoint, request.Repository, request.Revision, request.Filename)
	if err != nil {
		m.fail(id, err.Error())
		return
	}
	remote := m.inspectRemote(ctx, downloadURL, request.Token)
	if remote.RangeSupported && remote.Size > m.chunkSize {
		err = m.downloadChunked(ctx, id, downloadURL, request.Token, file, remote.Size)
	} else {
		err = m.downloadSequential(ctx, id, downloadURL, request.Token, file)
	}
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, context.Canceled) {
			m.cancelled(id, destination)
			return
		}
		m.fail(id, err.Error())
		return
	}
	if ctx.Err() != nil {
		m.cancelled(id, destination)
		return
	}
	if err := file.Sync(); err != nil {
		m.fail(id, err.Error())
		return
	}
	if err := file.Close(); err != nil {
		m.fail(id, err.Error())
		return
	}
	if ctx.Err() != nil {
		m.cancelled(id, destination)
		return
	}
	if err := replaceFile(partPath, destination, request.Overwrite, id); err != nil {
		m.fail(id, err.Error())
		return
	}
	removePart = false
	m.complete(id, destination)
}

type remoteDownloadInfo struct {
	Size           int64
	RangeSupported bool
}

type downloadByteRange struct {
	Start int64
	End   int64
}

// inspectRemote 只把 Range 當成可選加速能力。遠端無法探測、檔案太小或不支援
// Range 時，呼叫端會自動退回既有的單一串流下載。
func (m *Manager) inspectRemote(ctx context.Context, downloadURL, token string) remoteDownloadInfo {
	info := remoteDownloadInfo{Size: -1}
	request, err := newDownloadHTTPRequest(ctx, http.MethodHead, downloadURL, token)
	if err == nil {
		response, requestErr := m.client.Do(request)
		if requestErr == nil {
			response.Body.Close()
			if response.StatusCode >= 200 && response.StatusCode < 300 {
				info.Size = response.ContentLength
			}
		}
	}
	if info.Size > 0 && info.Size <= m.chunkSize {
		return info
	}

	request, err = newDownloadHTTPRequest(ctx, http.MethodGet, downloadURL, token)
	if err != nil {
		return info
	}
	request.Header.Set("Range", "bytes=0-0")
	response, err := m.client.Do(request)
	if err != nil {
		return info
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusPartialContent {
		return info
	}
	start, end, total, ok := parseContentRange(response.Header.Get("Content-Range"))
	if !ok || start != 0 || end != 0 || total < 1 {
		return info
	}
	info.Size = total
	info.RangeSupported = true
	return info
}

func (m *Manager) downloadSequential(
	ctx context.Context,
	id string,
	downloadURL string,
	token string,
	file *os.File,
) error {
	request, err := newDownloadHTTPRequest(ctx, http.MethodGet, downloadURL, token)
	if err != nil {
		return err
	}
	response, err := m.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return downloadResponseError(response)
	}
	m.update(id, func(job *domain.DownloadJob) {
		job.BytesDone = 0
		job.BytesTotal = response.ContentLength
	})
	progress := &progressReader{reader: response.Body, report: func(delta int64) {
		m.update(id, func(job *domain.DownloadJob) { job.BytesDone += delta })
	}}
	_, err = io.Copy(file, progress)
	progress.Flush()
	return err
}

func (m *Manager) downloadChunked(
	ctx context.Context,
	id string,
	downloadURL string,
	token string,
	file *os.File,
	total int64,
) error {
	if err := file.Truncate(total); err != nil {
		return err
	}
	m.update(id, func(job *domain.DownloadJob) {
		job.BytesDone = 0
		job.BytesTotal = total
	})
	ranges := planDownloadRanges(total, m.chunkSize)
	workerCount := m.chunkWorkers
	if workerCount < 1 {
		workerCount = 1
	}
	if workerCount > len(ranges) {
		workerCount = len(ranges)
	}

	workerContext, cancel := context.WithCancel(ctx)
	defer cancel()
	queue := make(chan downloadByteRange, len(ranges))
	for _, item := range ranges {
		queue <- item
	}
	close(queue)

	var workers sync.WaitGroup
	var failureOnce sync.Once
	var failure error
	for workerIndex := 0; workerIndex < workerCount; workerIndex++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for {
				select {
				case <-workerContext.Done():
					return
				case item, ok := <-queue:
					if !ok {
						return
					}
					if err := m.downloadRange(workerContext, id, downloadURL, token, file, item, total); err != nil {
						failureOnce.Do(func() {
							failure = err
							cancel()
						})
						return
					}
				}
			}
		}()
	}
	workers.Wait()
	if failure != nil {
		return failure
	}
	return ctx.Err()
}

func (m *Manager) downloadRange(
	ctx context.Context,
	id string,
	downloadURL string,
	token string,
	file *os.File,
	item downloadByteRange,
	total int64,
) error {
	select {
	case m.rangeSlots <- struct{}{}:
		defer func() { <-m.rangeSlots }()
	case <-ctx.Done():
		return ctx.Err()
	}
	request, err := newDownloadHTTPRequest(ctx, http.MethodGet, downloadURL, token)
	if err != nil {
		return err
	}
	request.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", item.Start, item.End))
	response, err := m.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("遠端伺服器未依 Range 回傳分段內容: %s", response.Status)
	}
	start, end, responseTotal, ok := parseContentRange(response.Header.Get("Content-Range"))
	if !ok || start != item.Start || end != item.End || responseTotal != total {
		return fmt.Errorf("遠端伺服器回傳無效的 Content-Range: %q", response.Header.Get("Content-Range"))
	}
	expected := item.End - item.Start + 1
	progress := &progressReader{reader: response.Body, report: func(delta int64) {
		m.update(id, func(job *domain.DownloadJob) { job.BytesDone += delta })
	}}
	written, err := io.CopyN(
		io.NewOffsetWriter(file, item.Start),
		progress,
		expected,
	)
	progress.Flush()
	if err != nil {
		return err
	}
	if written != expected {
		return fmt.Errorf("分段下載大小不符: 取得 %d bytes，預期 %d bytes", written, expected)
	}
	return nil
}

func planDownloadRanges(total, chunkSize int64) []downloadByteRange {
	if total < 1 || chunkSize < 1 {
		return nil
	}
	ranges := make([]downloadByteRange, 0, (total+chunkSize-1)/chunkSize)
	for start := int64(0); start < total; start += chunkSize {
		end := start + chunkSize - 1
		if end >= total {
			end = total - 1
		}
		ranges = append(ranges, downloadByteRange{Start: start, End: end})
	}
	return ranges
}

func parseContentRange(value string) (start, end, total int64, ok bool) {
	parts := contentRangePattern.FindStringSubmatch(strings.TrimSpace(value))
	if len(parts) != 4 {
		return 0, 0, 0, false
	}
	start, startErr := strconv.ParseInt(parts[1], 10, 64)
	end, endErr := strconv.ParseInt(parts[2], 10, 64)
	total, totalErr := strconv.ParseInt(parts[3], 10, 64)
	if startErr != nil || endErr != nil || totalErr != nil || start < 0 || end < start || total <= end {
		return 0, 0, 0, false
	}
	return start, end, total, true
}

func newDownloadHTTPRequest(ctx context.Context, method, downloadURL, token string) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, method, downloadURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/octet-stream")
	request.Header.Set("User-Agent", "Tanpopo/1.0")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	return request, nil
}

func downloadResponseError(response *http.Response) error {
	message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	return fmt.Errorf("Hugging Face 回傳 %s: %s", response.Status, strings.TrimSpace(string(message)))
}

// complete 會在檔案安全落地後立即清除已完成工作，避免下載佇列持續累積。
// 失敗工作仍會保留，讓使用者可以查看錯誤原因。
func (m *Manager) complete(id, destination string) {
	m.mu.Lock()
	delete(m.jobs, id)
	if m.activeTargets[destination] == id {
		delete(m.activeTargets, destination)
	}
	m.mu.Unlock()
}

func (m *Manager) cancelled(id, destination string) {
	m.mu.Lock()
	delete(m.jobs, id)
	if m.activeTargets[destination] == id {
		delete(m.activeTargets, destination)
	}
	m.mu.Unlock()
}

func (m *Manager) releaseDestination(destination, id string) {
	m.mu.Lock()
	if m.activeTargets[destination] == id {
		delete(m.activeTargets, destination)
	}
	m.mu.Unlock()
}

func (m *Manager) update(id string, mutate func(*domain.DownloadJob)) {
	m.mu.Lock()
	job, ok := m.jobs[id]
	if ok {
		mutate(&job)
		job.UpdatedAt = time.Now()
		m.jobs[id] = job
	}
	m.mu.Unlock()
}

func (m *Manager) fail(id, message string) {
	m.update(id, func(job *domain.DownloadJob) {
		job.State = "failed"
		job.Error = message
	})
}

func (m *Manager) pruneLocked() {
	if len(m.jobs) < maxRememberedJobs {
		return
	}
	var oldestID string
	var oldestTime time.Time
	for id, job := range m.jobs {
		if job.State == "queued" || job.State == "downloading" {
			continue
		}
		if oldestID == "" || job.CreatedAt.Before(oldestTime) {
			oldestID, oldestTime = id, job.CreatedAt
		}
	}
	if oldestID != "" {
		delete(m.jobs, oldestID)
	}
}

func validateRequest(request Request) error {
	if request.Runtime != domain.RuntimeLlamaServer && request.Runtime != domain.RuntimeMLXServer {
		return errors.New("runtime 僅支援 llama-server 或 mlx-server")
	}
	if !repositoryPattern.MatchString(request.Repository) {
		return errors.New("repository 必須為 owner/model 格式")
	}
	if request.Filename == "" || request.Filename == "." {
		return errors.New("filename 不可為空")
	}
	if request.ModelDirectory == "" {
		return errors.New("請先設定模型存放目錄")
	}
	if strings.ContainsAny(request.Revision, "\r\n\x00") || request.Revision == "" {
		return errors.New("revision 格式錯誤")
	}
	parsed, err := url.Parse(request.Endpoint)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return errors.New("Hugging Face Endpoint 格式錯誤")
	}
	if _, err = SafeJoin(request.ModelDirectory, request.Filename); err != nil {
		return err
	}
	_, err = SafeJoin(request.ModelDirectory, localDestination(request))
	return err
}

func SafeJoin(base, relative string) (string, error) {
	if strings.TrimSpace(base) == "" {
		return "", errors.New("基底目錄不可為空")
	}
	relative = filepath.Clean(filepath.FromSlash(relative))
	if relative == "." || filepath.IsAbs(relative) {
		return "", errors.New("檔名必須是模型目錄下的相對路徑")
	}
	baseAbs, err := filepath.Abs(base)
	if err != nil {
		return "", err
	}
	target := filepath.Join(baseAbs, relative)
	rel, err := filepath.Rel(baseAbs, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("檔名不可離開模型目錄")
	}
	return target, nil
}

func buildURL(endpoint, repository, revision, filename string) (string, error) {
	base, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	basePath := strings.TrimRight(base.Path, "/")
	baseEscapedPath := strings.TrimRight(base.EscapedPath(), "/")
	base.Path = basePath + "/" + repository + "/resolve/" + revision + "/" + filename
	base.RawPath = baseEscapedPath + "/" + escapeURLPath(repository) + "/resolve/" + url.PathEscape(revision) + "/" + escapeURLPath(filename)
	query := base.Query()
	query.Set("download", "true")
	base.RawQuery = query.Encode()
	return base.String(), nil
}

func buildRepositoryInfoURL(endpoint, repository, revision string) (string, error) {
	base, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	basePath := strings.TrimRight(base.Path, "/")
	baseEscapedPath := strings.TrimRight(base.EscapedPath(), "/")
	base.Path = basePath + "/api/models/" + repository + "/revision/" + revision
	base.RawPath = baseEscapedPath + "/api/models/" + escapeURLPath(repository) + "/revision/" + url.PathEscape(revision)
	base.RawQuery = ""
	return base.String(), nil
}

func buildModelSearchURL(endpoint, query string) (string, error) {
	base, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/api/models"
	base.RawPath = strings.TrimRight(base.EscapedPath(), "/") + "/api/models"
	parameters := base.Query()
	parameters.Set("search", strings.TrimSpace(query))
	parameters.Set("limit", "50")
	parameters.Set("full", "true")
	base.RawQuery = parameters.Encode()
	return base.String(), nil
}

func escapeURLPath(value string) string {
	parts := strings.Split(strings.ReplaceAll(value, "\\", "/"), "/")
	for index, part := range parts {
		parts[index] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

func replaceFile(partPath, destination string, overwrite bool, id string) error {
	destinationInfo, statErr := os.Stat(destination)
	if os.IsNotExist(statErr) {
		return os.Rename(partPath, destination)
	}
	if statErr != nil {
		return statErr
	}
	if !destinationInfo.Mode().IsRegular() {
		return errors.New("下載期間目的路徑已建立，且不是一般檔案")
	}
	if !overwrite {
		return errors.New("下載期間目的檔案已建立；未啟用覆寫")
	}

	backupPath := destination + ".backup-" + id
	if err := os.Rename(destination, backupPath); err != nil {
		return fmt.Errorf("建立舊模型暫存備份失敗: %w", err)
	}
	if err := os.Rename(partPath, destination); err != nil {
		if restoreErr := os.Rename(backupPath, destination); restoreErr != nil {
			return fmt.Errorf("替換模型失敗: %v；還原舊模型亦失敗: %v（備份位於 %s）", err, restoreErr, backupPath)
		}
		return fmt.Errorf("替換模型失敗，已還原舊模型: %w", err)
	}
	if err := os.Remove(backupPath); err != nil {
		return fmt.Errorf("模型已替換，但無法移除暫存備份 %s: %w", backupPath, err)
	}
	return nil
}

func randomID() (string, error) {
	value := make([]byte, 8)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

type progressReader struct {
	reader  io.Reader
	report  func(int64)
	pending int64
}

func (r *progressReader) Read(buffer []byte) (int, error) {
	count, err := r.reader.Read(buffer)
	if count > 0 {
		r.pending += int64(count)
	}
	if r.pending >= progressReportBatch || err != nil {
		r.Flush()
	}
	return count, err
}

func (r *progressReader) Flush() {
	if r.pending <= 0 {
		return
	}
	r.report(r.pending)
	r.pending = 0
}
