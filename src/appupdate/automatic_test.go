package appupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func releaseFixture(body []byte, platform string) (officialRelease, releaseAsset) {
	digest := sha256.Sum256(body)
	release := officialRelease{Tag: "v9.0.0-build-0100"}
	suffix := map[string]string{"darwin-arm64": "arm64.dmg", "darwin-amd64": "x64.dmg", "linux-amd64": "linux-x64.zip", "linux-arm64": "linux-arm64.zip", "windows-amd64": "x64.msi"}[platform]
	name := "Tanpopo-9.0.0-build-0100-" + suffix
	asset := releaseAsset{Name: name, State: "uploaded", Size: int64(len(body)), Digest: "sha256:" + hex.EncodeToString(digest[:]), URL: "https://github.com/trusted/project/releases/download/" + release.Tag + "/" + name}
	release.Assets = []releaseAsset{asset}
	return release, asset
}

func TestAutomaticSelectsExactPlatformAndRejectsAmbiguousOrUnverifiedAssets(t *testing.T) {
	for _, platform := range []string{"darwin-arm64", "linux-amd64", "windows-amd64"} {
		t.Run(platform, func(t *testing.T) {
			release, expected := releaseFixture([]byte("package"), platform)
			release.Assets = append(release.Assets, releaseAsset{Name: "source.zip", State: "uploaded"})
			got, err := selectReleaseAsset(release, platform)
			if err != nil || got != expected {
				t.Fatalf("選取錯誤：%+v %v", got, err)
			}
			release.Assets = append(release.Assets, expected)
			if _, err := selectReleaseAsset(release, platform); err == nil {
				t.Fatal("接受重複附件")
			}
			expected.Digest = ""
			release.Assets = []releaseAsset{expected}
			if _, err := selectReleaseAsset(release, platform); err == nil {
				t.Fatal("接受無摘要附件")
			}
		})
	}
}

func TestAutomaticRejectsForeignSourcesAndDowngrades(t *testing.T) {
	release, asset := releaseFixture([]byte("package"), "darwin-arm64")
	source := releaseSource{repository: "trusted/project"}
	for _, raw := range []string{strings.Replace(asset.URL, "https:", "http:", 1), strings.Replace(asset.URL, "github.com", "attacker.invalid", 1), strings.Replace(asset.URL, "trusted/project", "other/project", 1), asset.URL + "?redirect=bad"} {
		bad := asset
		bad.URL = raw
		if source.validateAssetURL(bad, release.Tag) == nil {
			t.Fatalf("接受非官方 URL：%s", raw)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(release) }))
	defer server.Close()
	source.apiBase = server.URL
	source.client = server.Client()
	if _, _, err := source.latest(context.Background(), "darwin-arm64", "10.0.0"); err == nil {
		t.Fatal("允許降級")
	}
	if _, _, err := source.latest(context.Background(), "darwin-arm64", release.Tag); err == nil {
		t.Fatal("重裝同版本")
	}
	if _, _, err := source.latest(context.Background(), "darwin-arm64", "1.0.0"); err != nil {
		t.Fatal(err)
	}
}

func TestAutomaticDownloadVerifiesCompleteBytesAndCancellation(t *testing.T) {
	body := []byte("完整更新套件內容")
	_, asset := releaseFixture(body, "darwin-arm64")
	for _, scenario := range []string{"ok", "tampered", "truncated", "oversized", "http-error", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				data := append([]byte(nil), body...)
				switch scenario {
				case "http-error":
					w.WriteHeader(503)
					return
				case "tampered":
					data[0] ^= 1
				case "truncated":
					data = data[:len(data)-1]
				case "oversized":
					data = append(data, 0)
				}
				w.Write(data)
			}))
			defer server.Close()
			candidate := asset
			candidate.URL = server.URL
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if scenario == "cancelled" {
				cancel()
			}
			source := releaseSource{client: server.Client()}
			var progress int64
			path := filepath.Join(t.TempDir(), "download")
			err := source.download(ctx, candidate, path, func(n int64) { progress = n })
			if (err == nil) != (scenario == "ok") {
				t.Fatalf("下載結果錯誤：%v", err)
			}
			if scenario == "ok" {
				if progress != asset.Size {
					t.Fatal("缺少完成進度")
				}
				if err := verifyDownloadedAsset(path, asset); err != nil {
					t.Fatal(err)
				}
				os.WriteFile(path, []byte("tampered"), 0600)
				if verifyDownloadedAsset(path, asset) == nil {
					t.Fatal("交接後遭修改仍被接受")
				}
			}
		})
	}
}

func TestAutomaticFailureKeepsOriginalRunningAndAllowsRetry(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("跨程序鎖在 Windows runner 驗證")
	}
	root := t.TempDir()
	target := filepath.Join(root, "Installed")
	os.Mkdir(target, 0700)
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-release; w.WriteHeader(503) }))
	defer server.Close()
	stopped := false
	a := &Automatic{ctx: context.Background(), options: AutomaticOptions{Shutdown: func() { stopped = true }}, target: target, statusDir: filepath.Join(target, "data"), source: releaseSource{apiBase: server.URL, repository: "trusted/project", client: server.Client()}}
	if status, err := a.Start(); err != nil || status.State != "checking" {
		t.Fatalf("啟動更新失敗：%v", err)
	}
	<-entered
	if _, err := a.Start(); !errors.Is(err, errUpdateInProgress) {
		t.Fatalf("未阻止重複啟動：%v", err)
	}
	if lock, err := acquireUpdateLock(target, 0); err == nil {
		lock.Close()
		t.Fatal("下載期間未持有鎖")
	}
	close(release)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		a.mu.Lock()
		active := a.active
		a.mu.Unlock()
		if !active {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if status := a.Status(); status.State != "failed" {
		t.Fatalf("未保存失敗：%+v", status)
	}
	if stopped {
		t.Fatal("下載前失敗卻關閉程式")
	}
	lock, err := acquireUpdateLock(target, 0)
	if err != nil {
		t.Fatalf("失敗後未釋放鎖：%v", err)
	}
	lock.Close()
}

func TestInstalledTargetNeverReplacesSourceWorkspace(t *testing.T) {
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, "website"), 0700)
	for _, file := range []string{"go.mod", "run.sh", "BUILD_INFO.txt", "agent.sample.properties", "website/settings.html"} {
		os.WriteFile(filepath.Join(root, file), []byte("fixture"), 0600)
	}
	for _, entry := range []struct{ exe, platform string }{{"Tanpopo", "linux-amd64"}, {"Tanpopo.exe", "windows-amd64"}} {
		if got := installedTarget(filepath.Join(root, entry.exe), entry.platform); got != "" {
			t.Fatalf("允許覆寫原始碼：%s", got)
		}
	}
}

func TestRedirectPolicyRejectsUntrustedAndInsecureHosts(t *testing.T) {
	client := officialSource().client
	for _, url := range []string{"http://github.com/package", "https://github.com.attacker.invalid/package", "https://user@github.com/package"} {
		req, _ := http.NewRequest("GET", url, nil)
		if client.CheckRedirect(req, nil) == nil {
			t.Fatalf("接受不可信重新導向：%s", url)
		}
	}
	req, _ := http.NewRequest("GET", "https://release-assets.githubusercontent.com/package", nil)
	if err := client.CheckRedirect(req, nil); err != nil {
		t.Fatal(err)
	}
}

func TestReleaseMetadataFailureCannotLaunchDownloadedCode(t *testing.T) {
	source := releaseSource{apiBase: "https://api.github.com", repository: "trusted/project", client: &http.Client{Transport: fixtureTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"tag_name":"v9.0.0","prerelease":true}`))}, nil
	})}}
	if _, err := source.release(context.Background(), ""); err == nil {
		t.Fatal("接受非穩定版")
	}
}
