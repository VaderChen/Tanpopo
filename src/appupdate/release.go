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
	"strings"
	"time"

	"LlamaLoader/src/appversion"
	"LlamaLoader/src/updatecheck"
)

type releaseAsset struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
	URL    string `json:"browser_download_url"`
}

type officialRelease struct {
	Tag        string         `json:"tag_name"`
	Draft      bool           `json:"draft"`
	Prerelease bool           `json:"prerelease"`
	Assets     []releaseAsset `json:"assets"`
}

type releaseSource struct {
	client              *http.Client
	apiBase, repository string
}

func officialSource() releaseSource {
	return releaseSource{
		apiBase: "https://api.github.com", repository: appversion.RepositoryName(),
		client: &http.Client{Timeout: 30 * time.Minute, CheckRedirect: func(r *http.Request, via []*http.Request) error {
			if len(via) >= 8 {
				return errors.New("更新下載重新導向次數過多")
			}
			if r.URL.Scheme != "https" || r.URL.User != nil {
				return errors.New("更新下載必須使用 HTTPS")
			}
			switch r.URL.Host {
			case "github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com":
				return nil
			default:
				return errors.New("更新下載重新導向至非 GitHub 主機")
			}
		}},
	}
}

func (s releaseSource) release(ctx context.Context, tag string) (officialRelease, error) {
	var release officialRelease
	parts := strings.Split(s.repository, "/")
	if len(parts) != 2 || !releaseIdentifier.MatchString(parts[0]) || !releaseIdentifier.MatchString(parts[1]) {
		return release, errors.New("可信發布來源設定無效")
	}
	endpoint := s.apiBase + "/repos/" + s.repository + "/releases/"
	if tag == "" {
		endpoint += "latest"
	} else {
		if !releaseIdentifier.MatchString(tag) {
			return release, errors.New("發布版本無效")
		}
		endpoint += "tags/" + url.PathEscape(tag)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return release, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "Tanpopo-Automatic-Updater")
	response, err := s.client.Do(request)
	if err != nil {
		return release, fmt.Errorf("無法取得官方更新資訊: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return release, fmt.Errorf("官方更新資訊回傳 HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if err != nil {
		return release, err
	}
	if len(data) > 2<<20 {
		return release, errors.New("官方更新資訊超過上限")
	}
	if err := json.Unmarshal(data, &release); err != nil {
		return release, err
	}
	if !releaseIdentifier.MatchString(release.Tag) || release.Draft || release.Prerelease || (tag != "" && tag != release.Tag) {
		return release, errors.New("官方更新版本不符或不是穩定發布")
	}
	return release, nil
}

func selectReleaseAsset(release officialRelease, platform string) (releaseAsset, error) {
	suffixes := map[string]string{"darwin-arm64": "arm64.dmg", "darwin-amd64": "x64.dmg", "windows-amd64": "x64.msi", "windows-arm64": "arm64.msi", "linux-amd64": "linux-x64.zip", "linux-arm64": "linux-arm64.zip"}
	suffix, ok := suffixes[platform]
	if !ok {
		return releaseAsset{}, errors.New("目前平台不支援自動更新")
	}
	name := "Tanpopo-" + strings.TrimPrefix(release.Tag, "v") + "-" + suffix
	var matches []releaseAsset
	for _, asset := range release.Assets {
		if asset.Name == name {
			matches = append(matches, asset)
		}
	}
	if len(matches) != 1 {
		return releaseAsset{}, fmt.Errorf("此版本沒有唯一對應的 %s 更新套件", platform)
	}
	asset := matches[0]
	digest, err := hex.DecodeString(strings.TrimPrefix(asset.Digest, "sha256:"))
	if asset.State != "uploaded" || asset.Size <= 0 || asset.Size > MaxUploadBytes || !strings.HasPrefix(asset.Digest, "sha256:") || err != nil || len(digest) != sha256.Size {
		return releaseAsset{}, errors.New("官方更新套件缺少有效大小或 SHA-256 摘要")
	}
	return asset, nil
}

func (s releaseSource) validateAssetURL(asset releaseAsset, tag string) error {
	u, err := url.Parse(asset.URL)
	expectedPath := "/" + s.repository + "/releases/download/" + tag + "/" + asset.Name
	if err != nil || u.Scheme != "https" || u.Host != "github.com" || u.User != nil || u.Path != expectedPath || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("更新套件不是官方 GitHub Release 下載網址")
	}
	return nil
}

func (s releaseSource) latest(ctx context.Context, platform, current string) (officialRelease, releaseAsset, error) {
	release, err := s.release(ctx, "")
	if err != nil {
		return release, releaseAsset{}, err
	}
	comparison, err := updatecheck.CompareVersions(release.Tag, current)
	if err != nil {
		return release, releaseAsset{}, err
	}
	if comparison <= 0 {
		return release, releaseAsset{}, errors.New("目前已是最新版本。")
	}
	asset, err := selectReleaseAsset(release, platform)
	if err == nil {
		err = s.validateAssetURL(asset, release.Tag)
	}
	return release, asset, err
}

func (s releaseSource) download(ctx context.Context, asset releaseAsset, target string, progress func(int64)) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.URL, nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", "Tanpopo-Automatic-Updater")
	response, err := s.client.Do(request)
	if err != nil {
		return fmt.Errorf("下載更新失敗: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("下載更新回傳 HTTP %d", response.StatusCode)
	}
	if response.ContentLength >= 0 && response.ContentLength != asset.Size {
		return errors.New("更新下載大小與發布資訊不符")
	}
	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	w := &downloadProgress{target: io.MultiWriter(file, hash), progress: progress}
	n, err := io.Copy(w, io.LimitReader(response.Body, asset.Size+1))
	if err != nil {
		return fmt.Errorf("下載更新中斷: %w", err)
	}
	if n != asset.Size || "sha256:"+hex.EncodeToString(hash.Sum(nil)) != asset.Digest {
		return errors.New("更新下載的大小或 SHA-256 不符，已取消更新")
	}
	if err := file.Sync(); err != nil {
		return err
	}
	progress(n)
	return file.Close()
}

type downloadProgress struct {
	target   io.Writer
	progress func(int64)
	total    int64
	last     time.Time
}

func (w *downloadProgress) Write(p []byte) (int, error) {
	n, err := w.target.Write(p)
	w.total += int64(n)
	if time.Since(w.last) >= 250*time.Millisecond {
		w.progress(w.total)
		w.last = time.Now()
	}
	return n, err
}

func verifyDownloadedAsset(fileName string, asset releaseAsset) error {
	file, err := os.Open(fileName)
	if err != nil {
		return err
	}
	defer file.Close()
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(file, asset.Size+1))
	if err != nil {
		return err
	}
	if n != asset.Size || "sha256:"+hex.EncodeToString(h.Sum(nil)) != asset.Digest {
		return errors.New("更新套件在交接後的 SHA-256 不符")
	}
	return nil
}
