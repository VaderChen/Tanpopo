package download

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"LlamaLoader/src/domain"
)

func TestRangeDownloadUsesBoundedWorkersAndPreservesContent(t *testing.T) {
	t.Parallel()
	content := bytes.Repeat([]byte("Tanpopo-Range-"), 512)
	var active atomic.Int32
	var maximum atomic.Int32
	var segmentRequests atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Accept-Ranges", "bytes")
		response.Header().Set("Content-Type", "application/octet-stream")
		response.Header().Set("Content-Length", strconv.Itoa(len(content)))
		if request.Method == http.MethodHead {
			return
		}
		rangeHeader := request.Header.Get("Range")
		var start, end int
		if _, err := fmt.Sscanf(rangeHeader, "bytes=%d-%d", &start, &end); err != nil || start < 0 || end < start || end >= len(content) {
			http.Error(response, "invalid range", http.StatusRequestedRangeNotSatisfiable)
			return
		}
		response.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(content)))
		response.WriteHeader(http.StatusPartialContent)
		if end > 0 {
			segmentRequests.Add(1)
			current := active.Add(1)
			for {
				observed := maximum.Load()
				if current <= observed || maximum.CompareAndSwap(observed, current) {
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
			defer active.Add(-1)
		}
		_, _ = response.Write(content[start : end+1])
	}))
	defer server.Close()

	manager := NewManager(1)
	manager.chunkSize = 1024
	manager.chunkWorkers = 4
	result, err := manager.Start(context.Background(), Request{
		Runtime:        domain.RuntimeLlamaServer,
		Repository:     "fixture/range-model",
		Filename:       "range-model-Q4_K_M.gguf",
		Revision:       "main",
		ModelDirectory: t.TempDir(),
		Endpoint:       server.URL,
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := manager.Wait(context.Background()); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	downloaded, err := os.ReadFile(result.Destination)
	if err != nil {
		t.Fatalf("讀取下載結果失敗: %v", err)
	}
	if !bytes.Equal(downloaded, content) {
		t.Fatal("分段下載後的檔案內容不一致")
	}
	if maximum.Load() > 4 {
		t.Fatalf("同時下載執行緒 = %d，超過上限 4", maximum.Load())
	}
	if maximum.Load() < 2 || segmentRequests.Load() < 2 {
		t.Fatalf("未實際使用並行分段下載：maximum=%d requests=%d", maximum.Load(), segmentRequests.Load())
	}
	if jobs := manager.List(); len(jobs) != 0 {
		t.Fatalf("已完成工作應自動清除，實際仍有：%+v", jobs)
	}
}

func TestPlanDownloadRangesUses64MiBChunks(t *testing.T) {
	t.Parallel()
	ranges := planDownloadRanges(defaultDownloadChunk*2+17, defaultDownloadChunk)
	if len(ranges) != 3 {
		t.Fatalf("分段數 = %d，預期 3", len(ranges))
	}
	if ranges[0] != (downloadByteRange{Start: 0, End: defaultDownloadChunk - 1}) ||
		ranges[2] != (downloadByteRange{Start: defaultDownloadChunk * 2, End: defaultDownloadChunk*2 + 16}) {
		t.Fatalf("64 MiB 分段規劃錯誤：%+v", ranges)
	}
}

func TestStartWithCompanionsDownloadsExternalDFlashGGUF(t *testing.T) {
	t.Parallel()
	server := newHuggingFaceFixture(t)
	defer server.Close()

	directory := t.TempDir()
	manager := NewManager(4)
	result, err := manager.StartWithCompanions(context.Background(), Request{
		Runtime:        domain.RuntimeLlamaServer,
		Repository:     "quantized/Target-GGUF",
		Filename:       "Target-Q4_K_M.gguf",
		Revision:       "main",
		ModelDirectory: directory,
		Endpoint:       server.URL,
	})
	if err != nil {
		t.Fatalf("StartWithCompanions() error = %v", err)
	}
	if err := manager.Wait(context.Background()); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if len(result.Jobs) != 2 {
		t.Fatalf("下載工作數 = %d，預期主模型與 Draft 共 2 個；warnings=%v", len(result.Jobs), result.Warnings)
	}
	if result.Jobs[1].Repository != "draft/Target-DFlash-GGUF" || result.Jobs[1].Filename != "Target-DFlash-Q8_0.gguf" {
		t.Fatalf("Draft 工作配對錯誤：%+v", result.Jobs[1])
	}
	for _, job := range result.Jobs {
		if _, err := os.Stat(job.Destination); err != nil {
			t.Fatalf("目的檔案不存在 %s: %v", job.Destination, err)
		}
	}
	if jobs := manager.List(); len(jobs) != 0 {
		t.Fatalf("已完成工作應自動清除，實際仍有：%+v", jobs)
	}
	if filepath.Dir(result.Jobs[0].Destination) != filepath.Dir(result.Jobs[1].Destination) {
		t.Fatalf("主模型與 Draft 未放在同一群組：%s / %s", result.Jobs[0].Destination, result.Jobs[1].Destination)
	}
}

func TestStartRepositoryDownloadsCompatibleDFlashMLX(t *testing.T) {
	t.Parallel()
	server := newHuggingFaceFixture(t)
	defer server.Close()

	directory := t.TempDir()
	manager := NewManager(8)
	result, err := manager.StartRepository(context.Background(), Request{
		Runtime:        domain.RuntimeMLXServer,
		Repository:     "vendor/Target",
		Revision:       "main",
		ModelDirectory: directory,
		Endpoint:       server.URL,
	})
	if err != nil {
		t.Fatalf("StartRepository() error = %v", err)
	}
	if err := manager.Wait(context.Background()); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}

	var targetJobs, draftJobs int
	for _, job := range result.Jobs {
		switch job.Repository {
		case "vendor/Target":
			targetJobs++
		case "draft/Target-DFlash":
			draftJobs++
		}
	}
	if targetJobs != 3 || draftJobs != 2 {
		t.Fatalf("MLX 工作數錯誤：target=%d draft=%d warnings=%v jobs=%+v", targetJobs, draftJobs, result.Warnings, result.Jobs)
	}
	if jobs := manager.List(); len(jobs) != 0 {
		t.Fatalf("已完成工作應自動清除，實際仍有：%+v", jobs)
	}
}

func newHuggingFaceFixture(t *testing.T) *httptest.Server {
	t.Helper()
	targetConfiguration := map[string]any{
		"architectures":     []string{"Qwen3ForCausalLM"},
		"model_type":        "qwen3",
		"hidden_size":       2560,
		"vocab_size":        151936,
		"num_hidden_layers": 36,
	}
	draftConfiguration := map[string]any{
		"architectures":     []string{"DFlashDraftModel"},
		"model_type":        "qwen3",
		"hidden_size":       2560,
		"vocab_size":        151936,
		"num_hidden_layers": 5,
		"num_target_layers": 36,
		"layer_types":       []string{"full_attention"},
	}

	return httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/models/quantized/Target-GGUF/revision/main":
			writeFixtureJSON(t, response, map[string]any{
				"id":       "quantized/Target-GGUF",
				"tags":     []string{"gguf", "base_model:vendor/Target", "base_model:quantized:vendor/Target"},
				"siblings": []map[string]string{{"rfilename": "Target-Q4_K_M.gguf"}},
			})
		case "/api/models/vendor/Target/revision/main":
			writeFixtureJSON(t, response, map[string]any{
				"id":   "vendor/Target",
				"tags": []string{"safetensors", "qwen3"},
				"siblings": []map[string]string{
					{"rfilename": "config.json"},
					{"rfilename": "model.safetensors"},
					{"rfilename": "tokenizer.json"},
				},
			})
		case "/api/models":
			if !strings.Contains(strings.ToLower(request.URL.Query().Get("search")), "dflash") {
				t.Fatalf("搜尋未包含 DFlash：%s", request.URL.RawQuery)
			}
			writeFixtureJSON(t, response, []map[string]any{
				{
					"id":       "draft/Target-DFlash-GGUF",
					"tags":     []string{"gguf", "dflash", "base_model:vendor/Target"},
					"siblings": []map[string]string{{"rfilename": "Target-DFlash-Q8_0.gguf"}},
				},
				{
					"id":   "draft/Target-DFlash",
					"tags": []string{"safetensors", "dflash", "base_model:vendor/Target"},
					"siblings": []map[string]string{
						{"rfilename": "config.json"},
						{"rfilename": "model.safetensors"},
					},
				},
			})
		case "/vendor/Target/resolve/main/config.json":
			writeFixtureJSON(t, response, targetConfiguration)
		case "/draft/Target-DFlash/resolve/main/config.json":
			writeFixtureJSON(t, response, draftConfiguration)
		case "/quantized/Target-GGUF/resolve/main/Target-Q4_K_M.gguf",
			"/draft/Target-DFlash-GGUF/resolve/main/Target-DFlash-Q8_0.gguf",
			"/vendor/Target/resolve/main/model.safetensors",
			"/vendor/Target/resolve/main/tokenizer.json",
			"/draft/Target-DFlash/resolve/main/model.safetensors":
			response.Header().Set("Content-Type", "application/octet-stream")
			_, _ = response.Write([]byte("fixture"))
		default:
			http.Error(response, "unexpected fixture path: "+request.URL.Path, http.StatusNotFound)
		}
	}))
}

func writeFixtureJSON(t *testing.T, response http.ResponseWriter, value any) {
	t.Helper()
	if err := json.NewEncoder(response).Encode(value); err != nil {
		t.Fatalf("fixture JSON encoding failed: %v", err)
	}
}
