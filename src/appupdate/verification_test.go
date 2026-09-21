package appupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestReleaseDigestTrustsOnlyMatchingPublishedArchive(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "update.zip")
	body := []byte("發布 ZIP 測試內容")
	if err := os.WriteFile(archive, body, 0600); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(hash[:])
	for _, scenario := range []struct {
		name, digest, tag string
		size              int
		draft             bool
		status            int
	}{
		{"valid", digest, "1.0.0", len(body), false, 200},
		{"tampered", "sha256:" + strings.Repeat("0", 64), "1.0.0", len(body), false, 200},
		{"missing-digest", "", "1.0.0", len(body), false, 200},
		{"wrong-version", digest, "2.0.0", len(body), false, 200},
		{"wrong-size", digest, "1.0.0", 1, false, 200},
		{"draft", digest, "1.0.0", len(body), true, 200},
		{"unavailable", digest, "1.0.0", len(body), false, 403},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/repos/trusted/project/releases/tags/1.0.0" {
					t.Errorf("錯誤的驗證來源：%s", r.URL)
				}
				w.WriteHeader(scenario.status)
				json.NewEncoder(w).Encode(map[string]any{"tag_name": scenario.tag, "draft": scenario.draft, "assets": []any{map[string]any{"name": "Tanpopo.zip", "state": "uploaded", "size": scenario.size, "digest": scenario.digest}}})
			}))
			defer server.Close()
			err := verifyReleaseDigest(context.Background(), server.Client(), server.URL, "trusted/project", "1.0.0", archive)
			if (err == nil) != (scenario.name == "valid") {
				t.Fatalf("驗證結果錯誤：%v", err)
			}
		})
	}
}

func TestPayloadReleaseTagIgnoresUntrustedRepository(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "BUILD_INFO.txt"), []byte("app_version=1.2.3\napp_build=0004\nupdate_repository=attacker/project"), 0600)
	tag, err := payloadReleaseTag(dir)
	if err != nil || tag != "1.2.3-build-0004" {
		t.Fatalf("版本解析失敗：%q %v", tag, err)
	}
}

type fixtureTransport func(*http.Request) (*http.Response, error)

func (f fixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestStartRejectsUntrustedArchiveBeforeLaunchingHelper(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ZIP 更新使用 POSIX 跨程序鎖，Windows 不提供此功能")
	}
	archivePath, _ := testArchive(t, validArchiveEntries())
	archive, err := os.Open(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	transport := http.DefaultTransport
	http.DefaultTransport = fixtureTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "https" || r.URL.Host != "api.github.com" {
			t.Errorf("非預期的信任來源：%s", r.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"tag_name":"1.0.0","assets":[]}`)), Header: make(http.Header)}, nil
	})
	defer func() { http.DefaultTransport = transport }()
	manager := &Manager{available: true, targetDir: filepath.Join(t.TempDir(), "installed"), executablePath: "/must-not-launch"}
	if _, err := manager.Start(archive); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("來源驗證未阻止 Helper 啟動：%v", err)
	}
	if manager.active {
		t.Fatal("失敗後未解除更新鎖定")
	}
}
