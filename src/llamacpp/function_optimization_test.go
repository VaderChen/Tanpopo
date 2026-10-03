package llamacpp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

func TestLogBufferMatchesBoundedTail(t *testing.T) {
	random := rand.New(rand.NewSource(42))
	for _, limit := range []int{0, 1, 31, 1024} {
		buffer := newLogBuffer(limit)
		var expected []byte
		for step := 0; step < 1000; step++ {
			value := make([]byte, random.Intn(2048))
			for i := range value {
				value[i] = byte(random.Intn(12) + 1)
			}
			expected = append(expected, value...)
			if len(expected) > limit {
				start := len(expected) - limit
				if newline := bytes.IndexByte(expected[start:], '\n'); newline >= 0 {
					start += newline + 1
				}
				expected = expected[start:]
			}
			n, err := buffer.Write(value)
			if n != len(value) || err != nil || buffer.String() != string(expected) {
				t.Fatalf("limit=%d step=%d：日誌內容不符", limit, step)
			}
			if step%127 == 0 {
				buffer.Reset()
				expected = nil
			}
		}
	}
}

func TestGGUFBufferedReadsPreserveTruncationAndPosition(t *testing.T) {
	for count := 0; count < 20; count++ {
		data := bytes.Repeat([]byte{7}, count)
		for _, width := range []int{4, 8} {
			plain := bytes.NewReader(data)
			buffered := bufio.NewReader(bytes.NewReader(data))
			var left, right uint64
			var a, b error
			if width == 4 {
				x, e := readGGUFUint32(plain)
				y, f := readGGUFUint32(buffered)
				left, right, a, b = uint64(x), uint64(y), e, f
			} else {
				left, a = readGGUFUint64(plain)
				right, b = readGGUFUint64(buffered)
			}
			if left != right || !errors.Is(a, b) {
				t.Fatalf("count=%d width=%d：%v／%v", count, width, a, b)
			}
			remaining, _ := io.ReadAll(buffered)
			if len(remaining) != plain.Len() {
				t.Fatal("截斷後位置不一致")
			}
		}
		for _, skip := range []uint64{0, 1, 8, 32} {
			plain, buffered := bytes.NewReader(data), bufio.NewReader(bytes.NewReader(data))
			if a, b := skipGGUFBytes(plain, skip), skipGGUFBytes(buffered, skip); !errors.Is(a, b) {
				t.Fatalf("skip=%d：%v／%v", skip, a, b)
			}
		}
	}
}

func TestShardInventoryRechecksMissingAndUnsafeFiles(t *testing.T) {
	directory := t.TempDir()
	for name, content := range map[string]string{"config.json": `{}`, "first.safetensors": "data", "second.safetensors": "data"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	weights := map[string]string{"a": "first.safetensors", "b": "first.safetensors", "c": "second.safetensors"}
	write := func() {
		data, _ := json.Marshal(map[string]any{"weight_map": weights})
		if err := os.WriteFile(filepath.Join(directory, "model.safetensors.index.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write()
	if !isMLXModelDirectory(directory) {
		t.Fatal("有效索引未被接受")
	}
	if err := os.Remove(filepath.Join(directory, "second.safetensors")); err != nil {
		t.Fatal(err)
	}
	if isMLXModelDirectory(directory) {
		t.Fatal("刪除後不應沿用先前結果")
	}
	weights["c"] = "../first.safetensors"
	write()
	if isMLXModelDirectory(directory) {
		t.Fatal("越界索引不應接受")
	}
}
