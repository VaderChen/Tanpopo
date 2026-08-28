// Package directorybrowser 提供已登入管理介面使用的服務主機目錄瀏覽資料。
package directorybrowser

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

const maxDirectories = 2000

type Entry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type Listing struct {
	Path        string  `json:"path"`
	Parent      string  `json:"parent,omitempty"`
	Roots       []Entry `json:"roots"`
	Directories []Entry `json:"directories"`
}

func List(requestedPath string) (Listing, error) {
	home, _ := os.UserHomeDir()
	requestedPath = expandHome(strings.TrimSpace(requestedPath), home)
	if requestedPath == "" {
		requestedPath = home
	}
	if requestedPath == "" {
		requestedPath = string(filepath.Separator)
	}

	absolute, err := filepath.Abs(requestedPath)
	if err != nil {
		return Listing{}, err
	}
	absolute = filepath.Clean(absolute)
	info, err := os.Stat(absolute)
	if err != nil {
		return Listing{}, err
	}
	if !info.IsDir() {
		return Listing{}, errors.New("指定路徑不是目錄")
	}

	entries, err := os.ReadDir(absolute)
	if err != nil {
		return Listing{}, err
	}
	directories := make([]Entry, 0)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		entryPath := filepath.Join(absolute, entry.Name())
		entryInfo, infoErr := os.Stat(entryPath)
		if infoErr != nil || !entryInfo.IsDir() {
			continue
		}
		directories = append(directories, Entry{Name: entry.Name(), Path: filepath.Clean(entryPath)})
		if len(directories) >= maxDirectories {
			break
		}
	}
	sort.Slice(directories, func(i, j int) bool {
		return strings.ToLower(directories[i].Name) < strings.ToLower(directories[j].Name)
	})

	parent := filepath.Dir(absolute)
	if parent == absolute {
		parent = ""
	}
	return Listing{
		Path:        absolute,
		Parent:      parent,
		Roots:       directoryRoots(home),
		Directories: directories,
	}, nil
}

func expandHome(value, home string) string {
	if value == "~" {
		return home
	}
	if strings.HasPrefix(value, "~/") || strings.HasPrefix(value, `~\`) {
		return filepath.Join(home, value[2:])
	}
	return value
}

func directoryRoots(home string) []Entry {
	result := make([]Entry, 0, 4)
	seen := make(map[string]struct{})
	add := func(name, path string) {
		if path == "" {
			return
		}
		path = filepath.Clean(path)
		if _, exists := seen[path]; exists {
			return
		}
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			return
		}
		seen[path] = struct{}{}
		result = append(result, Entry{Name: name, Path: path})
	}

	add("Home", home)
	if runtime.GOOS == "windows" {
		add("目前磁碟", filepath.VolumeName(home)+string(filepath.Separator))
		return result
	}
	add("檔案系統", string(filepath.Separator))
	if runtime.GOOS == "darwin" {
		add("掛載磁碟", "/Volumes")
	} else {
		add("掛載磁碟", "/mnt")
		add("外接媒體", "/media")
	}
	return result
}
