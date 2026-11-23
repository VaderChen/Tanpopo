package appupdate

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsPortableZIP(t *testing.T) {
	entries := []archiveTestEntry{{name: "Tanpopo/BUILD_INFO.txt", content: "app=Tanpopo\nplatform=windows-amd64\napp_version=1.0.0\napp_build=0001"}}
	for _, name := range []string{"VERSION", "Tanpopo.exe", "agent.sample.properties", "netpass-client/NetPassClient.exe", "llama-runtime/prebuilt/windows-amd64/bin/llama-server.exe", "website/settings.html"} {
		entries = append(entries, archiveTestEntry{name: "Tanpopo/" + name, content: "updated"})
	}
	archive, root := testArchive(t, entries)
	payload, _, err := extractPlatformArchive(archive, root, "windows-amd64")
	if err != nil {
		t.Fatal(err)
	}
	if tag, err := payloadReleaseTag(payload); err != nil || tag != "1.0.0-build-0001" {
		t.Fatalf("套件版本不符: %s, %v", tag, err)
	}
	for _, platform := range []string{"linux-amd64", "windows-arm64"} {
		if _, _, err := extractPlatformArchive(archive, t.TempDir(), platform); err == nil {
			t.Fatalf("接受了錯誤平台 %s", platform)
		}
	}
}

func TestPortableUpdateAndRollback(t *testing.T) {
	for _, scenario := range []string{"success", "file-conflict", "startup-failure"} {
		t.Run(scenario, func(t *testing.T) {
			target, payload, backup := t.TempDir(), t.TempDir(), t.TempDir()
			write := func(dir, name, value string) {
				t.Helper()
				path := filepath.Join(dir, name)
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(value), 0600); err != nil {
					t.Fatal(err)
				}
			}
			write(target, "Tanpopo.exe", "old")
			write(target, "data/settings.json", "keep")
			write(target, "website/custom.txt", "keep")
			write(target, "agent.properties", "keep")
			write(payload, "Tanpopo.exe", "new")
			write(payload, "website/settings.html", "new")
			if scenario == "file-conflict" {
				write(target, "z-dir", "keep")
				write(payload, "z-dir/file", "new")
			}
			var launches []bool
			err := installPortableFiles(target, payload, backup, func(updated bool) error {
				launches = append(launches, updated)
				if updated && scenario == "startup-failure" {
					return errors.New("模擬新版啟動失敗")
				}
				return nil
			})
			success := scenario == "success"
			if (err == nil) != success || len(launches) == 0 || launches[len(launches)-1] != success {
				t.Fatalf("更新結果或重啟版本不符: %v %v", launches, err)
			}
			version := "old"
			if success {
				version = "new"
			} else if _, err := os.Stat(filepath.Join(target, "website/settings.html")); !os.IsNotExist(err) {
				t.Fatalf("還原時新增檔案應移除: %v", err)
			}
			for name, want := range map[string]string{"Tanpopo.exe": version, "agent.properties": "keep", "data/settings.json": "keep", "website/custom.txt": "keep"} {
				data, err := os.ReadFile(filepath.Join(target, name))
				if err != nil || string(data) != want {
					t.Fatalf("%s 內容不符: %s %v", name, data, err)
				}
			}
		})
	}
}

func TestPublishedWindowsZIPSmoke(t *testing.T) {
	archive := os.Getenv("TANPOPO_UPDATE_SMOKE_WINDOWS_ZIP")
	if archive == "" {
		t.Skip("設定 TANPOPO_UPDATE_SMOKE_WINDOWS_ZIP 以驗證實際發行包")
	}
	payload, _, err := extractPlatformArchive(archive, t.TempDir(), "windows-amd64")
	if err != nil {
		t.Fatal(err)
	}
	tag, err := payloadReleaseTag(payload)
	if err != nil || filepath.Base(archive) != "Tanpopo-"+tag+"-windows-x64.zip" {
		t.Fatalf("內部版本與附件名稱不符: %s, %v", tag, err)
	}
	target := t.TempDir()
	if err := installPortableFiles(target, payload, t.TempDir(), func(updated bool) error {
		if !updated || installedTarget(filepath.Join(target, "Tanpopo.exe"), "windows-amd64") != target {
			return errors.New("更新後未能辨識可攜安裝目錄")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
