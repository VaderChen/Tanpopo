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
	"strings"
	"sync"
	"time"

	"LlamaLoader/src/domain"
)

const maxRememberedJobs = 50

var (
	repositoryPattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*$`)
	filenameTokenPattern = regexp.MustCompile(`[^a-z0-9]+`)
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
	Siblings []struct {
		Filename string `json:"rfilename"`
	} `json:"siblings"`
}

type Manager struct {
	mu            sync.RWMutex
	wg            sync.WaitGroup
	jobs          map[string]domain.DownloadJob
	activeTargets map[string]string
	client        *http.Client
	slots         chan struct{}
}

func NewManager(maxConcurrent int) *Manager {
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	}
	return &Manager{
		jobs:          make(map[string]domain.DownloadJob),
		activeTargets: make(map[string]string),
		client:        &http.Client{Transport: transport},
		slots:         make(chan struct{}, maxConcurrent),
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

	companions, discoveryErr := m.discoverCompanions(ctx, request)
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
	for _, filename := range files {
		fileRequest := request
		fileRequest.Filename = filename
		fileRequest.LocalDirectory = localDirectory
		destination, joinErr := SafeJoin(fileRequest.ModelDirectory, localDestination(fileRequest))
		if joinErr != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s: %v", filename, joinErr))
			continue
		}
		if !fileRequest.Overwrite {
			if existing, statErr := os.Stat(destination); statErr == nil && existing.Mode().IsRegular() {
				result.Skipped = append(result.Skipped, filename)
				continue
			}
		}
		job, startErr := m.startValidated(ctx, fileRequest, destination)
		if startErr != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s: %v", filename, startErr))
			continue
		}
		result.Jobs = append(result.Jobs, job)
	}
	if len(result.Jobs) == 0 && len(result.Skipped) == 0 {
		return BatchResult{}, errors.New("repository 沒有可下載的 MLX 模型檔案")
	}
	return result, nil
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
	m.mu.Lock()
	if _, exists := m.activeTargets[destination]; exists {
		m.mu.Unlock()
		return domain.DownloadJob{}, errors.New("相同目的檔案已有下載工作進行中")
	}
	m.pruneLocked()
	m.jobs[id] = job
	m.activeTargets[destination] = id
	m.mu.Unlock()
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		m.run(ctx, id, request, destination)
	}()
	return job, nil
}

func (m *Manager) discoverCompanions(ctx context.Context, request Request) ([]string, error) {
	info, err := m.fetchRepositoryInfo(ctx, request)
	if err != nil {
		return nil, err
	}
	return selectCompanionFilenames(request.Filename, info), nil
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
	return info, nil
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
		m.fail(id, "服務已停止")
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
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		m.fail(id, err.Error())
		return
	}
	httpRequest.Header.Set("Accept", "application/octet-stream")
	httpRequest.Header.Set("User-Agent", "Tanpopo/1.0")
	if request.Token != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+request.Token)
	}
	response, err := m.client.Do(httpRequest)
	if err != nil {
		m.fail(id, err.Error())
		return
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		m.fail(id, fmt.Sprintf("Hugging Face 回傳 %s: %s", response.Status, strings.TrimSpace(string(message))))
		return
	}
	m.update(id, func(job *domain.DownloadJob) { job.BytesTotal = response.ContentLength })

	written, err := io.Copy(file, &progressReader{reader: response.Body, report: func(delta int64) {
		m.update(id, func(job *domain.DownloadJob) { job.BytesDone += delta })
	}})
	if err != nil {
		m.fail(id, err.Error())
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
	if err := replaceFile(partPath, destination, request.Overwrite, id); err != nil {
		m.fail(id, err.Error())
		return
	}
	removePart = false
	m.update(id, func(job *domain.DownloadJob) {
		job.State = "completed"
		job.BytesDone = written
		if job.BytesTotal < 0 {
			job.BytesTotal = written
		}
	})
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
	reader io.Reader
	report func(int64)
}

func (r *progressReader) Read(buffer []byte) (int, error) {
	count, err := r.reader.Read(buffer)
	if count > 0 {
		r.report(int64(count))
	}
	return count, err
}
