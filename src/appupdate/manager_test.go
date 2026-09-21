package appupdate

import (
	"archive/zip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type archiveTestEntry struct {
	name    string
	content string
	mode    os.FileMode
}

func testArchive(t *testing.T, entries []archiveTestEntry) (string, string) {
	t.Helper()
	directory := t.TempDir()
	archivePath := filepath.Join(directory, "update.zip")
	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	writer := zip.NewWriter(file)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Deflate}
		mode := entry.mode
		if mode == 0 {
			mode = 0644
		}
		header.SetMode(mode)
		output, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := output.Write([]byte(entry.content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return archivePath, filepath.Join(directory, "extract")
}

func validArchiveEntries() []archiveTestEntry {
	entries := []archiveTestEntry{
		{name: "Tanpopo/VERSION", content: "1.0.0"},
		{name: "Tanpopo/BUILD_INFO.txt", content: "app=Tanpopo\nplatform=linux-" + runtime.GOARCH + "\napp_display_version=1.0.0"},
	}
	for _, name := range []string{"agent.sample.properties", "install.sh", "run.sh", "website/settings.html", "Tanpopo"} {
		entries = append(entries, archiveTestEntry{name: "Tanpopo/" + name, content: "test fixture", mode: 0755})
	}
	return entries
}

func TestExtractValidUpdateArchive(t *testing.T) {
	archivePath, root := testArchive(t, validArchiveEntries())
	payload, version, err := extractAndValidate(archivePath, root)
	if err != nil {
		t.Fatal(err)
	}
	if payload != filepath.Join(root, "Tanpopo") || version != "1.0.0" {
		t.Fatalf("更新套件辨識失敗：%s %s", payload, version)
	}
}

func TestExtractRejectsUnsafeArchiveEntries(t *testing.T) {
	for _, name := range []string{"../escape", "/absolute/file", `Tanpopo\escape`, "Tanpopo/../../escape"} {
		t.Run(name, func(t *testing.T) {
			archivePath, root := testArchive(t, []archiveTestEntry{{name: name, content: "unsafe"}})
			if _, _, err := extractAndValidate(archivePath, root); err == nil {
				t.Fatal("不安全路徑未被拒絕")
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(root), "escape")); !os.IsNotExist(err) {
				t.Fatal("解壓縮寫入了核准目錄外")
			}
		})
	}
}

func TestExtractRejectsInvalidPackageStructure(t *testing.T) {
	for _, scenario := range []struct {
		name  string
		entry archiveTestEntry
		want  string
	}{
		{"symlink", archiveTestEntry{name: "Tanpopo/link", content: "../../escape", mode: os.ModeSymlink | 0777}, "不支援的檔案類型"},
		{"duplicate", archiveTestEntry{name: "Tanpopo/VERSION"}, "重複路徑"},
		{"multiple-roots", archiveTestEntry{name: "Other/file"}, "一個最上層目錄"},
		{"user-data", archiveTestEntry{name: "Tanpopo/data/settings.json"}, "使用者資料路徑"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			entries := append(validArchiveEntries(), scenario.entry)
			archivePath, root := testArchive(t, entries)
			_, _, err := extractAndValidate(archivePath, root)
			if err == nil || !strings.Contains(err.Error(), scenario.want) {
				t.Fatalf("未回報預期的套件錯誤 %q：%v", scenario.want, err)
			}
		})
	}
}
