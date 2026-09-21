package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"LlamaLoader/src/config"
	"LlamaLoader/src/domain"
)

func newSettingsTestServer(t *testing.T) *Server {
	t.Helper()
	store, err := config.NewStore(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	return &Server{settings: store}
}

func TestSettingsPartialUpdatePreservesUnspecifiedFields(t *testing.T) {
	s := newSettingsTestServer(t)
	if err := s.settings.Update(func(value *domain.Settings) error {
		value.HuggingFaceToken = "test-only-secret"
		value.ResidentMode = true
		value.ExtraArgs = []string{"--test"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, payload := range []string{`{"ui_theme":"ocean"}`, `{"resident_mode":false}`} {
		response := httptest.NewRecorder()
		s.handleSettingsUpdate(response, httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(payload)))
		if response.Code != http.StatusOK {
			t.Fatalf("部分更新失敗：%d %s", response.Code, response.Body.String())
		}
		if strings.Contains(response.Body.String(), "test-only-secret") {
			t.Fatal("公開回應洩露 token")
		}
	}
	value := s.settings.Get()
	if value.UITheme != "ocean" || value.ResidentMode || value.HuggingFaceToken != "test-only-secret" ||
		len(value.ExtraArgs) != 1 || value.ExtraArgs[0] != "--test" || value.ModelDirectory == "" {
		t.Fatal("未指定的設定被覆寫，或 false 未套用")
	}
}

func TestConcurrentSettingsAndFavoritesPreserveChanges(t *testing.T) {
	s := newSettingsTestServer(t)
	var group sync.WaitGroup
	const favorites = 24
	for index := range favorites {
		group.Go(func() {
			response := httptest.NewRecorder()
			payload := fmt.Sprintf(`{"runtime":"mlx-server","repository":"owner/model-%d","revision":"main"}`, index)
			s.handleDownloadFavoriteAdd(response, httptest.NewRequest(http.MethodPost, "/api/download/favorites", strings.NewReader(payload)))
			if response.Code != http.StatusOK {
				t.Errorf("新增收藏失敗：%s", response.Body.String())
			}
		})
	}
	for _, payload := range []string{`{"ui_theme":"ocean"}`, `{"ui_language":"en"}`, `{"resident_mode":true}`} {
		group.Go(func() {
			response := httptest.NewRecorder()
			s.handleSettingsUpdate(response, httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(payload)))
			if response.Code != http.StatusOK {
				t.Errorf("設定更新失敗：%s", response.Body.String())
			}
		})
	}
	group.Wait()
	value := s.settings.Get()
	if len(value.DownloadFavorites) != favorites || value.UITheme != "ocean" || value.UILanguage != "en" || !value.ResidentMode {
		t.Fatalf("並行更新遺失：收藏=%d、主題=%s、語言=%s、常駐=%v", len(value.DownloadFavorites), value.UITheme, value.UILanguage, value.ResidentMode)
	}
}

func TestInvalidSettingsUpdateIsAtomic(t *testing.T) {
	s := newSettingsTestServer(t)
	response := httptest.NewRecorder()
	s.handleSettingsUpdate(response, httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(`{"ui_theme":"ocean","model_directory":""}`)))
	if response.Code != http.StatusBadRequest || s.settings.Get().UITheme != "tanpopo" {
		t.Fatal("無效更新沒有整筆拒絕")
	}
}
