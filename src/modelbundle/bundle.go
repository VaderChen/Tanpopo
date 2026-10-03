// Package modelbundle 以檔案內容識別模型，提供可取消的區網模型同步。
package modelbundle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

const MaxManifestBytes = 2 << 20
const maxFiles = 4096

type File struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type Manifest struct {
	Files  []File `json:"files"`
	Digest string `json:"digest"`
	Bytes  int64  `json:"bytes"`
}

type Progress struct {
	Phase      string `json:"phase"`
	BytesDone  int64  `json:"bytes_done"`
	BytesTotal int64  `json:"bytes_total"`
}

type Report func(Progress)
type Fetch func(context.Context, int) (io.ReadCloser, error)

// Snapshot 不包含可由對端任意指定的本機路徑；傳輸只能以清單索引讀取。
type Snapshot struct {
	Manifest       Manifest
	root, relative string
}

func relevant(name string) bool {
	switch path.Ext(name) {
	case ".safetensors", ".json", ".model", ".jinja", ".txt", ".tiktoken":
		return true
	}
	return false
}

func validPath(name string) bool {
	if !fs.ValidPath(name) || name == "." || len(name) > 1024 || strings.ContainsAny(name, "\\:\x00") {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if strings.HasPrefix(part, ".") || strings.TrimSpace(part) != part {
			return false
		}
	}
	return true
}

func digest(files []File) string {
	data, _ := json.Marshal(files)
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func (m Manifest) Validate() error {
	if len(m.Files) == 0 || len(m.Files) > maxFiles {
		return errors.New("模型檔案數量無效")
	}
	var total int64
	config, weights := false, false
	for i, file := range m.Files {
		hash, err := hex.DecodeString(file.SHA256)
		if !validPath(file.Path) || !relevant(file.Path) || file.Size < 0 || file.Size > 1<<50 ||
			err != nil || len(hash) != 32 || (i > 0 && m.Files[i-1].Path >= file.Path) {
			return errors.New("模型檔案清單格式無效")
		}
		total += file.Size
		config = config || file.Path == "config.json"
		weights = weights || path.Ext(file.Path) == ".safetensors"
	}
	if !config || !weights || total != m.Bytes || digest(m.Files) != m.Digest {
		return errors.New("模型清單摘要或必要檔案不符")
	}
	encoded, _ := json.Marshal(m)
	if len(encoded) > MaxManifestBytes {
		return errors.New("模型清單超過傳輸大小限制")
	}
	return nil
}

func openDirectory(root, relative string) (*os.Root, error) {
	if !validPath(filepath.ToSlash(relative)) {
		return nil, errors.New("模型目錄格式無效")
	}
	base, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer base.Close()
	return base.OpenRoot(relative)
}

func inventory(root *os.Root) ([]File, error) {
	var files []File
	err := fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if name == "." {
			return nil
		}
		if strings.HasPrefix(entry.Name(), ".") {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.IsDir() || !relevant(name) {
			return nil
		}
		if !validPath(name) {
			return errors.New("模型包含不可攜的檔案路徑")
		}
		info, err := root.Stat(name)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("模型檔案不是一般檔案：%s", name)
		}
		files = append(files, File{Path: name, Size: info.Size()})
		if len(files) > maxFiles {
			return errors.New("模型檔案數量超限")
		}
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, err
}

// 每次分塊讀取都檢查取消，避免大模型雜湊阻塞停止叢集。
type reader struct {
	ctx  context.Context
	r    io.Reader
	done func(int64)
}

func (r reader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.r.Read(p)
	if r.done != nil {
		r.done(int64(n))
	}
	return n, err
}

func Create(ctx context.Context, root, relative string, report Report) (*Snapshot, error) {
	directory, err := openDirectory(root, relative)
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	files, err := inventory(directory)
	if err != nil {
		return nil, err
	}
	var total, done int64
	for _, file := range files {
		total += file.Size
	}
	progress := func(n int64) {
		done += n
		if report != nil {
			report(Progress{"verifying", done, total})
		}
	}
	progress(0)
	for i := range files {
		file, err := directory.Open(files[i].Path)
		if err != nil {
			return nil, err
		}
		before, err := file.Stat()
		if err != nil {
			file.Close()
			return nil, err
		}
		h := sha256.New()
		n, err := io.CopyBuffer(h, reader{ctx, file, progress}, make([]byte, 1<<20))
		after, statErr := file.Stat()
		file.Close()
		if err != nil {
			return nil, err
		}
		if statErr != nil || n != files[i].Size || !before.ModTime().Equal(after.ModTime()) {
			return nil, fmt.Errorf("模型在核對時變更：%s", files[i].Path)
		}
		files[i].SHA256 = hex.EncodeToString(h.Sum(nil))
	}
	manifest := Manifest{files, digest(files), total}
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	return &Snapshot{Manifest: manifest, root: root, relative: relative}, nil
}

func (s *Snapshot) Open(index int) (*os.File, error) {
	if index < 0 || index >= len(s.Manifest.Files) {
		return nil, errors.New("模型檔案索引無效")
	}
	directory, err := openDirectory(s.root, s.relative)
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	file, err := directory.Open(s.Manifest.Files[index].Path)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != s.Manifest.Files[index].Size {
		file.Close()
		return nil, errors.New("發起端模型已變更，請重新啟動叢集")
	}
	return file, nil
}

// Matches 先檢查清單，再從小檔案開始核對，避免為不同設定讀取數十 GB 權重。
func Matches(ctx context.Context, root, relative string, want Manifest, report Report) bool {
	directory, err := openDirectory(root, relative)
	if err != nil {
		return false
	}
	defer directory.Close()
	files, err := inventory(directory)
	if err != nil || len(files) != len(want.Files) {
		return false
	}
	for i, file := range files {
		if file.Path != want.Files[i].Path || file.Size != want.Files[i].Size {
			return false
		}
	}
	order := append([]File(nil), want.Files...)
	sort.SliceStable(order, func(i, j int) bool { return order[i].Size < order[j].Size })
	var done int64
	for _, item := range order {
		file, err := directory.Open(item.Path)
		if err != nil {
			return false
		}
		h := sha256.New()
		n, err := io.CopyBuffer(h, reader{ctx, file, func(n int64) {
			done += n
			if report != nil {
				report(Progress{"verifying", done, want.Bytes})
			}
		}}, make([]byte, 1<<20))
		file.Close()
		if err != nil || n != item.Size || hex.EncodeToString(h.Sum(nil)) != item.SHA256 {
			return false
		}
	}
	return ctx.Err() == nil
}

func Ensure(ctx context.Context, root, preferred string, manifest Manifest, fetch Fetch, report Report) (string, error) {
	if err := manifest.Validate(); err != nil {
		return "", err
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		return "", err
	}
	base, err := os.OpenRoot(root)
	if err != nil {
		return "", err
	}
	defer base.Close()
	destination := "cluster-models/" + manifest.Digest
	candidates := []string{preferred, destination}
	err = fs.WalkDir(base.FS(), ".", func(name string, entry fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return nil
		} // 無權讀取的不相關目錄不應阻止建立新副本。
		if name != "." && entry.IsDir() && strings.HasPrefix(entry.Name(), ".") {
			return fs.SkipDir
		}
		if !entry.IsDir() && entry.Name() == "config.json" {
			candidates = append(candidates, path.Dir(name))
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	seen := map[string]bool{}
	for _, candidate := range candidates {
		if seen[candidate] {
			continue
		}
		seen[candidate] = true
		if Matches(ctx, root, candidate, manifest, report) {
			return candidate, nil
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
	}
	// 隱藏暫存目錄不會出現在模型清單。成功後才原子搬入可載入的目錄。
	temp, err := os.MkdirTemp(root, ".cluster-download-")
	if err != nil {
		return "", err
	}
	stage := filepath.Base(temp)
	defer base.RemoveAll(stage)
	var done int64
	if report != nil {
		report(Progress{"downloading", 0, manifest.Bytes})
	}
	for i, item := range manifest.Files {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		name := path.Join(stage, item.Path)
		if err := base.MkdirAll(path.Dir(name), 0755); err != nil {
			return "", err
		}
		out, err := base.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err != nil {
			return "", err
		}
		in, err := fetch(ctx, i)
		if err != nil {
			out.Close()
			return "", err
		}
		h := sha256.New()
		n, err := io.CopyBuffer(io.MultiWriter(out, h), reader{ctx, io.LimitReader(in, item.Size+1), func(n int64) {
			done += n
			if report != nil {
				report(Progress{"downloading", done, manifest.Bytes})
			}
		}}, make([]byte, 1<<20))
		in.Close()
		if err == nil {
			err = out.Sync()
		}
		closeErr := out.Close()
		if err != nil {
			return "", err
		}
		if closeErr != nil {
			return "", closeErr
		}
		if n != item.Size || hex.EncodeToString(h.Sum(nil)) != item.SHA256 {
			return "", fmt.Errorf("模型下載內容不符：%s", item.Path)
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := base.MkdirAll("cluster-models", 0755); err != nil {
		return "", err
	}
	// 舊的損壞副本或使用者檔案一律保留；新副本使用另一個名稱。
	if _, err := base.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
		destination += "-" + strings.TrimPrefix(stage, ".cluster-download-")
	}
	if err := base.Rename(stage, destination); err != nil {
		return "", err
	}
	return destination, nil
}
