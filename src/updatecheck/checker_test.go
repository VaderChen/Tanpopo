package updatecheck

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestCheckerDetectsNewReleaseAndCachesResult(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.URL.Path != "/repos/VaderChen/Tanpopo/releases/latest" {
			t.Errorf("未預期的路徑：%s", request.URL.Path)
			http.NotFound(response, request)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		response.Write([]byte(`{
			"tag_name":"v1.26.0830",
			"name":"Tanpopo 1.26.0830",
			"html_url":"https://github.com/VaderChen/Tanpopo/releases/tag/v1.26.0830",
			"published_at":"2026-08-29T00:00:00Z"
		}`))
	}))
	defer server.Close()

	checker := New(Options{
		CurrentVersion: "1.26.0829",
		DisplayVersion: "1.26.0829 build 1430",
		Repository:     "VaderChen/Tanpopo",
		APIBaseURL:     server.URL,
		Client:         server.Client(),
		Interval:       time.Hour,
	})
	status := checker.Check(context.Background(), false)
	if !status.UpdateAvailable || status.LatestVersion != "v1.26.0830" {
		t.Fatalf("應使用 Release 版本偵測新版，實際狀態：%+v", status)
	}
	if status.CurrentVersion != "1.26.0829 build 1430" {
		t.Fatalf("應回傳完整顯示版本，實際狀態：%+v", status)
	}

	// 快取與強制更新的行為不受顯示用 build 編號影響。
	if status.ReleaseURL == "" || status.RepositoryURL == "" || status.CheckError != "" {
		t.Fatalf("Release 資訊不完整：%+v", status)
	}
	checker.Check(context.Background(), false)
	if requests.Load() != 1 {
		t.Fatalf("快取期間不應重複請求，實際 %d 次", requests.Load())
	}
	checker.Check(context.Background(), true)
	if requests.Load() != 2 {
		t.Fatalf("強制檢查應重新請求，實際 %d 次", requests.Load())
	}
}

func TestCheckerHandlesRepositoryWithoutRelease(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		http.NotFound(response, nil)
	}))
	defer server.Close()

	checker := New(Options{
		CurrentVersion: "1.0.0",
		Repository:     "VaderChen/Tanpopo",
		APIBaseURL:     server.URL,
		Client:         server.Client(),
	})
	status := checker.Check(context.Background(), true)
	if status.CheckError != "GitHub 尚未發布 Release" || status.CheckedAt.IsZero() {
		t.Fatalf("應保留可顯示的檢查錯誤，實際狀態：%+v", status)
	}
}

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		candidate string
		current   string
		expected  int
	}{
		{"v1.2.0", "1.1.9", 1},
		{"1.0.0", "v1.0.0", 0},
		{"1.0.0-beta.2", "1.0.0-beta.1", 1},
		{"1.0.0", "1.0.0-rc.1", 1},
		{"2.0", "2.0.1", -1},
		{"v1.26.0830", "1.26.0829", 1},
	}
	for _, test := range tests {
		actual, err := CompareVersions(test.candidate, test.current)
		if err != nil {
			t.Fatalf("比較 %s 與 %s 失敗：%v", test.candidate, test.current, err)
		}
		if actual != test.expected {
			t.Fatalf("比較 %s 與 %s：預期 %d，實際 %d", test.candidate, test.current, test.expected, actual)
		}
	}
}
