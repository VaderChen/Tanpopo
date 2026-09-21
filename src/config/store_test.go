package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"LlamaLoader/src/domain"
)

func TestConcurrentUpdatesPreserveEveryChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	store, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	const updates = 32
	var group sync.WaitGroup
	for range updates {
		group.Go(func() {
			if err := store.Update(func(value *domain.Settings) error {
				value.Threads++
				return nil
			}); err != nil {
				t.Error(err)
			}
		})
	}
	group.Wait()
	if got := store.Get().Threads; got != updates {
		t.Fatalf("更新遺失：Threads=%d，預期 %d", got, updates)
	}
	reloaded, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(store.Get(), reloaded.Get()) {
		t.Fatal("磁碟與記憶體設定不一致")
	}
}

func TestUpdateFailureLeavesSettingsUnchanged(t *testing.T) {
	for _, kind := range []string{"callback", "validation", "disk"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.json")
			store, err := NewStore(path)
			if err != nil {
				t.Fatal(err)
			}
			initial := store.Get()
			initial.ExtraArgs = []string{"--initial"}
			if err := store.Save(initial); err != nil {
				t.Fatal(err)
			}
			before := store.Get()
			content, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "disk" {
				// 以一般檔案充當父目錄，確保寫入失敗且不依賴使用者權限。
				store.path = filepath.Join(path, "cannot-write.json")
			}
			err = store.Update(func(value *domain.Settings) error {
				value.ExtraArgs[0] = "--changed"
				if kind == "callback" {
					return errors.New("abort")
				}
				if kind == "validation" {
					value.ServerPort = -1
				}
				return nil
			})
			if err == nil {
				t.Fatal("預期交易失敗")
			}
			if !reflect.DeepEqual(before, store.Get()) {
				t.Fatal("交易失敗仍改動記憶體設定")
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != string(content) {
				t.Fatal("交易失敗仍改動原始設定檔")
			}
		})
	}
}

func TestSaveDoesNotShareCallerSlices(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	value := store.Get()
	value.ExtraArgs = []string{" --test "}
	if err := store.Save(value); err != nil {
		t.Fatal(err)
	}
	if value.ExtraArgs[0] != " --test " {
		t.Fatal("儲存時修改了呼叫端的 slice")
	}
	value.ExtraArgs[0] = "--changed"
	if store.Get().ExtraArgs[0] != "--test" {
		t.Fatal("呼叫端可透過共用 slice 修改已儲存的設定")
	}
}

func TestNormalizeSettingsPreservesSupportedTheme(t *testing.T) {
	value := DefaultSettings()
	value.UITheme = " WISTERIA "

	actual := normalizeSettings(value)
	if actual.UITheme != "wisteria" {
		t.Fatalf("預期 wisteria，實際為 %q", actual.UITheme)
	}
}

func TestNormalizeSettingsMigratesMissingTheme(t *testing.T) {
	value := DefaultSettings()
	value.UITheme = ""

	actual := normalizeSettings(value)
	if actual.UITheme != "tanpopo" {
		t.Fatalf("舊設定應沿用 tanpopo，實際為 %q", actual.UITheme)
	}
}

func TestValidateSettingsRejectsUnknownTheme(t *testing.T) {
	value := DefaultSettings()
	value.UITheme = "unknown"

	if err := ValidateSettings(domain.Settings(value)); err == nil {
		t.Fatal("不支援的介面配色應回傳錯誤")
	}
}
