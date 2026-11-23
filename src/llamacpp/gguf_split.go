package llamacpp

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var splitGGUFName = regexp.MustCompile(`(?i)^(.*)-([0-9]{5})-of-([0-9]{5})\.gguf$`)

func isSecondaryGGUFShard(name string) bool {
	parts := splitGGUFName.FindStringSubmatch(filepath.Base(name))
	return len(parts) == 4 && parts[2] != "00001"
}

func ggufSourceFiles(path string) ([]string, error) {
	parts := splitGGUFName.FindStringSubmatch(filepath.Base(path))
	if len(parts) != 4 {
		return []string{path}, nil
	}
	count, err := strconv.Atoi(parts[3])
	if err != nil || count < 1 || count > 1000 {
		return nil, fmt.Errorf("GGUF 分片數量不正確")
	}
	result := make([]string, 0, count)
	for i := 1; i <= count; i++ {
		part := filepath.Join(filepath.Dir(path), fmt.Sprintf("%s-%05d-of-%05d.gguf", parts[1], i, count))
		info, err := os.Stat(part)
		if err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("缺少 GGUF 分片：%s", filepath.Base(part))
		}
		result = append(result, part)
	}
	return result, nil
}

func ggufFootprintBytes(path string) (uint64, error) {
	files, err := ggufSourceFiles(path)
	if err != nil {
		return 0, err
	}
	var total uint64
	for _, file := range files {
		info, err := os.Stat(file)
		if err != nil {
			return 0, err
		}
		if info.Size() < 0 || uint64(info.Size()) > ^uint64(0)-total {
			return 0, fmt.Errorf("GGUF 分片大小溢位")
		}
		total += uint64(info.Size())
	}
	return total, nil
}

func isGGUFPath(path string) bool { return strings.EqualFold(filepath.Ext(path), ".gguf") }
