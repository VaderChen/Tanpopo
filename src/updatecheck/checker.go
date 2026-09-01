package updatecheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultAPIBaseURL = "https://api.github.com"
	defaultInterval   = time.Hour
	maxResponseSize   = 1024 * 1024
)

var repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

type Options struct {
	CurrentVersion string
	DisplayVersion string
	Repository     string
	APIBaseURL     string
	Client         *http.Client
	Interval       time.Duration
	Now            func() time.Time
}

type Status struct {
	CurrentVersion  string    `json:"current_version"`
	LatestVersion   string    `json:"latest_version,omitempty"`
	UpdateAvailable bool      `json:"update_available"`
	RepositoryURL   string    `json:"repository_url"`
	ReleaseURL      string    `json:"release_url,omitempty"`
	ReleaseName     string    `json:"release_name,omitempty"`
	PublishedAt     time.Time `json:"published_at,omitempty"`
	CheckedAt       time.Time `json:"checked_at,omitempty"`
	CheckError      string    `json:"check_error,omitempty"`
}

type Checker struct {
	currentVersion string
	displayVersion string
	repository     string
	apiBaseURL     string
	client         *http.Client
	interval       time.Duration
	now            func() time.Time

	checkMu sync.Mutex
	mu      sync.RWMutex
	status  Status
}

type githubRelease struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	HTMLURL     string    `json:"html_url"`
	PublishedAt time.Time `json:"published_at"`
}

func New(options Options) *Checker {
	apiBaseURL := strings.TrimRight(strings.TrimSpace(options.APIBaseURL), "/")
	if apiBaseURL == "" {
		apiBaseURL = defaultAPIBaseURL
	}
	client := options.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	interval := options.Interval
	if interval <= 0 {
		interval = defaultInterval
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	repository := strings.Trim(strings.TrimSpace(options.Repository), "/")
	displayVersion := strings.TrimSpace(options.DisplayVersion)
	if displayVersion == "" {
		displayVersion = strings.TrimSpace(options.CurrentVersion)
	}
	checker := &Checker{
		currentVersion: strings.TrimSpace(options.CurrentVersion),
		displayVersion: displayVersion,
		repository:     repository,
		apiBaseURL:     apiBaseURL,
		client:         client,
		interval:       interval,
		now:            now,
	}
	checker.status = checker.baseStatus()
	return checker
}

func (c *Checker) Start(ctx context.Context) {
	go func() {
		c.Check(ctx, false)
		ticker := time.NewTicker(c.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c.Check(ctx, true)
			}
		}
	}()
}

func (c *Checker) Status() Status {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.status
}

// Check 在 force=false 時沿用一小時內的結果；force=true 供使用者手動檢查。
func (c *Checker) Check(ctx context.Context, force bool) Status {
	c.checkMu.Lock()
	defer c.checkMu.Unlock()

	current := c.Status()
	if !force && !current.CheckedAt.IsZero() && c.now().Before(current.CheckedAt.Add(c.interval)) {
		return current
	}

	status := c.baseStatus()
	status.CheckedAt = c.now().UTC()
	release, err := c.fetchLatestRelease(ctx)
	if err != nil {
		status.CheckError = err.Error()
		c.store(status)
		return status
	}
	status.LatestVersion = strings.TrimSpace(release.TagName)
	status.ReleaseURL = strings.TrimSpace(release.HTMLURL)
	status.ReleaseName = strings.TrimSpace(release.Name)
	status.PublishedAt = release.PublishedAt

	comparison, err := CompareVersions(status.LatestVersion, c.currentVersion)
	if err != nil {
		status.CheckError = err.Error()
	} else {
		status.UpdateAvailable = comparison > 0
	}
	c.store(status)
	return status
}

func (c *Checker) baseStatus() Status {
	repositoryURL := ""
	if c.repository != "" {
		repositoryURL = "https://github.com/" + c.repository
	}
	return Status{
		CurrentVersion: c.displayVersion,
		RepositoryURL:  repositoryURL,
	}
}

func (c *Checker) store(status Status) {
	c.mu.Lock()
	c.status = status
	c.mu.Unlock()
}

func (c *Checker) fetchLatestRelease(ctx context.Context) (githubRelease, error) {
	if !repositoryPattern.MatchString(c.repository) {
		return githubRelease{}, errors.New("GitHub repository 格式錯誤")
	}
	parts := strings.SplitN(c.repository, "/", 2)
	endpoint := fmt.Sprintf(
		"%s/repos/%s/%s/releases/latest",
		c.apiBaseURL,
		url.PathEscape(parts[0]),
		url.PathEscape(parts[1]),
	)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return githubRelease{}, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "Tanpopo-Update-Checker")

	response, err := c.client.Do(request)
	if err != nil {
		return githubRelease{}, fmt.Errorf("無法連線至 GitHub: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return githubRelease{}, errors.New("GitHub 尚未發布 Release")
	}
	if response.StatusCode != http.StatusOK {
		return githubRelease{}, fmt.Errorf("GitHub 回傳 HTTP %d", response.StatusCode)
	}

	var release githubRelease
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxResponseSize))
	if err := decoder.Decode(&release); err != nil {
		return githubRelease{}, fmt.Errorf("GitHub Release 資料格式錯誤: %w", err)
	}
	if strings.TrimSpace(release.TagName) == "" {
		return githubRelease{}, errors.New("GitHub Release 缺少版本標籤")
	}
	return release, nil
}

type semanticVersion struct {
	core       []int64
	prerelease []string
	build      int64
}

// CompareVersions 比較 GitHub tag 與目前版本；回傳 1 表示 candidate 較新。
func CompareVersions(candidate, current string) (int, error) {
	candidateVersion, err := parseVersion(candidate)
	if err != nil {
		return 0, fmt.Errorf("新版版本號無法辨識: %w", err)
	}
	currentVersion, err := parseVersion(current)
	if err != nil {
		return 0, fmt.Errorf("目前 APP 版本號無法辨識: %w", err)
	}
	for index := 0; index < len(candidateVersion.core); index++ {
		if candidateVersion.core[index] > currentVersion.core[index] {
			return 1, nil
		}
		if candidateVersion.core[index] < currentVersion.core[index] {
			return -1, nil
		}
	}
	if comparison := comparePrerelease(candidateVersion.prerelease, currentVersion.prerelease); comparison != 0 {
		return comparison, nil
	}
	if candidateVersion.build > currentVersion.build {
		return 1, nil
	}
	if candidateVersion.build < currentVersion.build {
		return -1, nil
	}
	return 0, nil
}

func parseVersion(value string) (semanticVersion, error) {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(strings.TrimPrefix(value, "v"), "V")
	if buildIndex := strings.Index(value, "+"); buildIndex >= 0 {
		value = value[:buildIndex]
	}
	var build int64
	if buildIndex := strings.LastIndex(value, "-build-"); buildIndex >= 0 {
		buildText := value[buildIndex+len("-build-"):]
		parsedBuild, err := strconv.ParseInt(buildText, 10, 64)
		if err != nil || parsedBuild < 0 {
			return semanticVersion{}, fmt.Errorf("%q 的 build 編號無法辨識", value)
		}
		build = parsedBuild
		value = value[:buildIndex]
	}
	var prerelease []string
	if prereleaseIndex := strings.Index(value, "-"); prereleaseIndex >= 0 {
		prerelease = strings.Split(value[prereleaseIndex+1:], ".")
		value = value[:prereleaseIndex]
	}
	parts := strings.Split(value, ".")
	if len(parts) < 1 || len(parts) > 4 {
		return semanticVersion{}, fmt.Errorf("%q 不是支援的語意版本", value)
	}
	core := make([]int64, 4)
	for index, part := range parts {
		if part == "" {
			return semanticVersion{}, fmt.Errorf("%q 不是支援的語意版本", value)
		}
		number, err := strconv.ParseInt(part, 10, 64)
		if err != nil || number < 0 {
			return semanticVersion{}, fmt.Errorf("%q 不是支援的語意版本", value)
		}
		core[index] = number
	}
	for _, identifier := range prerelease {
		if identifier == "" {
			return semanticVersion{}, fmt.Errorf("%q 的 prerelease 格式錯誤", value)
		}
	}
	return semanticVersion{core: core, prerelease: prerelease, build: build}, nil
}

func comparePrerelease(candidate, current []string) int {
	if len(candidate) == 0 && len(current) == 0 {
		return 0
	}
	if len(candidate) == 0 {
		return 1
	}
	if len(current) == 0 {
		return -1
	}
	limit := len(candidate)
	if len(current) < limit {
		limit = len(current)
	}
	for index := 0; index < limit; index++ {
		candidateNumber, candidateErr := strconv.ParseInt(candidate[index], 10, 64)
		currentNumber, currentErr := strconv.ParseInt(current[index], 10, 64)
		switch {
		case candidateErr == nil && currentErr == nil:
			if candidateNumber > currentNumber {
				return 1
			}
			if candidateNumber < currentNumber {
				return -1
			}
		case candidateErr == nil:
			return -1
		case currentErr == nil:
			return 1
		default:
			if candidate[index] > current[index] {
				return 1
			}
			if candidate[index] < current[index] {
				return -1
			}
		}
	}
	if len(candidate) > len(current) {
		return 1
	}
	if len(candidate) < len(current) {
		return -1
	}
	return 0
}
