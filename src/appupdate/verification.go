package appupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"LlamaLoader/src/appversion"
)

var releaseIdentifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// 信任根來自已安裝程式的建置設定，不能由上傳套件的 update_repository 覆寫。
// GitHub 以 HTTPS 提供完整發布 ZIP 的 SHA-256；無摘要或無法連線均拒絕更新。
func verifyOfficialArchive(archivePath, payloadDir string) error {
	tag, err := payloadReleaseTag(payloadDir)
	if err != nil {
		return err
	}
	client := &http.Client{
		Timeout:       20 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("發布驗證不接受重新導向") },
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return verifyReleaseDigest(ctx, client, "https://api.github.com", appversion.RepositoryName(), tag, archivePath)
}

func payloadReleaseTag(payloadDir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(payloadDir, "BUILD_INFO.txt"))
	if err != nil {
		return "", err
	}
	values := parseBuildInfo(string(data))
	version := values["app_version"]
	if version == "" {
		data, err = os.ReadFile(filepath.Join(payloadDir, "VERSION"))
		if err != nil {
			return "", err
		}
		version = strings.TrimSpace(string(data))
	}
	if !releaseIdentifier.MatchString(version) {
		return "", errors.New("更新套件缺少有效的發布版本")
	}
	if build := values["app_build"]; build != "" {
		if !regexp.MustCompile(`^[0-9]+$`).MatchString(build) {
			return "", errors.New("更新套件 build 無效")
		}
		version += "-build-" + build
	}
	return version, nil
}

// API 位址與 Client 僅供內部依賴注入；產品入口固定使用 GitHub HTTPS。
func verifyReleaseDigest(ctx context.Context, client *http.Client, apiBase, repository, tag, archivePath string) error {
	parts := strings.Split(repository, "/")
	if len(parts) != 2 || !releaseIdentifier.MatchString(parts[0]) || !releaseIdentifier.MatchString(parts[1]) || !releaseIdentifier.MatchString(tag) {
		return errors.New("可信發布來源設定無效")
	}
	endpoint := apiBase + "/repos/" + repository + "/releases/tags/" + url.PathEscape(tag)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "Tanpopo-Update-Verifier")
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("無法驗證官方發布來源: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("官方發布驗證失敗（HTTP %d）", response.StatusCode)
	}
	const maximumResponse = 2 << 20
	data, err := io.ReadAll(io.LimitReader(response.Body, maximumResponse+1))
	if err != nil {
		return err
	}
	if len(data) > maximumResponse {
		return errors.New("官方發布資料超過上限")
	}
	var release struct {
		Tag    string `json:"tag_name"`
		Draft  bool   `json:"draft"`
		Assets []struct {
			Name   string `json:"name"`
			State  string `json:"state"`
			Size   int64  `json:"size"`
			Digest string `json:"digest"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(data, &release); err != nil {
		return fmt.Errorf("官方發布資料無效: %w", err)
	}
	if release.Tag != tag || release.Draft {
		return errors.New("官方發布版本不符或尚未公開")
	}
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return err
	}
	digest := "sha256:" + hex.EncodeToString(hash.Sum(nil))
	for _, asset := range release.Assets {
		if asset.State == "uploaded" && strings.HasSuffix(strings.ToLower(asset.Name), ".zip") && asset.Size == size && asset.Digest == digest {
			return nil
		}
	}
	return errors.New("更新 ZIP 的 SHA-256 與可信發布來源不符，或該發布未提供摘要；未執行任何套件程式")
}
