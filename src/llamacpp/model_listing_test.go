package llamacpp

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestHasGenerativeHead(t *testing.T) {
	cases := []struct {
		name          string
		architectures []string
		want          bool
	}{
		{"因果語言模型", []string{"Qwen3ForCausalLM"}, true},
		{"多模態", []string{"Qwen3_5ForConditionalGeneration"}, true},
		{"舊式 LM head", []string{"GPT2LMHeadModel"}, true},
		{"嵌入／基礎模型沒有輸出頭", []string{"Qwen3Model"}, false},
		{"DFlash Draft 不算生成模型", []string{"DFlashDraftModel"}, false},
		{"缺欄位時不判斷", nil, true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := hasGenerativeHead(testCase.architectures); got != testCase.want {
				t.Fatalf("got %v, want %v", got, testCase.want)
			}
		})
	}
}

func TestIsCompositeModelComponent(t *testing.T) {
	base := t.TempDir()
	standalone := filepath.Join(base, "Qwen3-4B")
	pipeline := filepath.Join(base, "z-image-turbo")
	component := filepath.Join(pipeline, "text_encoder")
	nested := filepath.Join(base, "vendor", "Qwen3-8B")
	for _, directory := range []string{standalone, component, nested} {
		if err := os.MkdirAll(directory, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(pipeline, "model_index.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}

	if isCompositeModelComponent(base, standalone) {
		t.Fatal("根目錄下的獨立模型不該被當成零件")
	}
	if !isCompositeModelComponent(base, component) {
		t.Fatal("pipeline 子目錄應該被當成零件")
	}
	if isCompositeModelComponent(base, nested) {
		t.Fatal("單純的分層目錄不該被當成零件")
	}
}

func TestGGUFProfileIsLanguageModel(t *testing.T) {
	cases := []struct {
		name    string
		profile ggufModelProfile
		want    bool
	}{
		{"一般 LLM", ggufModelProfile{Architecture: "qwen35", HasBlockCount: true, HasTokenizer: true}, true},
		{"缺 transformer 層數（影片／音樂模型）", ggufModelProfile{Architecture: "ltxv", HasTokenizer: true}, false},
		{"缺 tokenizer", ggufModelProfile{Architecture: "ace-step", HasBlockCount: true}, false},
		{"嵌入模型帶 pooling_type", ggufModelProfile{Architecture: "bert", HasBlockCount: true, HasTokenizer: true, HasPoolingType: true}, false},
		{"純 encoder", ggufModelProfile{Architecture: "t5encoder", HasBlockCount: true, HasTokenizer: true}, false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := testCase.profile.isLanguageModel(); got != testCase.want {
				t.Fatalf("got %v, want %v", got, testCase.want)
			}
		})
	}
}

// writeMinimalGGUF 寫出只有 metadata、沒有 tensor 的 GGUF，用來測 metadata 解析。
func writeMinimalGGUF(t *testing.T, path, architecture string, extraKeys map[string]uint32) {
	t.Helper()
	var buffer bytes.Buffer
	writeString := func(value string) {
		_ = binary.Write(&buffer, binary.LittleEndian, uint64(len(value)))
		buffer.WriteString(value)
	}
	buffer.WriteString("GGUF")
	_ = binary.Write(&buffer, binary.LittleEndian, uint32(3))
	_ = binary.Write(&buffer, binary.LittleEndian, uint64(0)) // tensor count
	_ = binary.Write(&buffer, binary.LittleEndian, uint64(2+len(extraKeys)))

	writeString("general.architecture")
	_ = binary.Write(&buffer, binary.LittleEndian, ggufTypeString)
	writeString(architecture)

	writeString("tokenizer.ggml.tokens")
	_ = binary.Write(&buffer, binary.LittleEndian, ggufTypeArray)
	_ = binary.Write(&buffer, binary.LittleEndian, ggufTypeString)
	_ = binary.Write(&buffer, binary.LittleEndian, uint64(1))
	writeString("<pad>")

	for key, value := range extraKeys {
		writeString(key)
		_ = binary.Write(&buffer, binary.LittleEndian, ggufTypeUint32)
		_ = binary.Write(&buffer, binary.LittleEndian, value)
	}
	if err := os.WriteFile(path, buffer.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestReadGGUFModelProfile(t *testing.T) {
	directory := t.TempDir()

	languageModel := filepath.Join(directory, "llm.gguf")
	writeMinimalGGUF(t, languageModel, "qwen35", map[string]uint32{"qwen35.block_count": 32})
	profile, err := readGGUFModelProfile(languageModel)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Architecture != "qwen35" || !profile.HasBlockCount || !profile.HasTokenizer {
		t.Fatalf("語言模型欄位讀取錯誤：%+v", profile)
	}
	if !profile.isLanguageModel() {
		t.Fatal("應判定為語言模型")
	}

	videoModel := filepath.Join(directory, "video.gguf")
	writeMinimalGGUF(t, videoModel, "ltxv", nil)
	profile, err = readGGUFModelProfile(videoModel)
	if err != nil {
		t.Fatal(err)
	}
	if profile.HasBlockCount {
		t.Fatalf("影片模型不該有 block_count：%+v", profile)
	}
	if profile.isLanguageModel() {
		t.Fatal("影片模型不該被判定為語言模型")
	}

	embedding := filepath.Join(directory, "embedding.gguf")
	writeMinimalGGUF(t, embedding, "bert", map[string]uint32{
		"bert.block_count":  12,
		"bert.pooling_type": 1,
	})
	profile, err = readGGUFModelProfile(embedding)
	if err != nil {
		t.Fatal(err)
	}
	if !profile.HasPoolingType || profile.isLanguageModel() {
		t.Fatalf("嵌入模型應被排除：%+v", profile)
	}
}

func TestListModelsSkipsNonLanguageGGUF(t *testing.T) {
	directory := t.TempDir()
	writeMinimalGGUF(t, filepath.Join(directory, "chat.gguf"), "qwen35",
		map[string]uint32{"qwen35.block_count": 32})
	writeMinimalGGUF(t, filepath.Join(directory, "video.gguf"), "ltxv", nil)
	// mmproj 沒有語言模型欄位，但 UI 由同一份清單挑選，必須保留。
	writeMinimalGGUF(t, filepath.Join(directory, "mmproj-BF16.gguf"), "clip", nil)

	models, err := ListModels(directory)
	if err != nil {
		t.Fatal(err)
	}
	listed := make(map[string]bool, len(models))
	for _, model := range models {
		listed[model.Path] = true
	}
	if !listed["chat.gguf"] {
		t.Fatal("語言模型應出現在列表")
	}
	if !listed["mmproj-BF16.gguf"] {
		t.Fatal("mmproj 應保留在列表")
	}
	if listed["video.gguf"] {
		t.Fatal("影片模型不該出現在列表")
	}
}

func TestListMLXModelsSkipsComponentsAndEmbeddings(t *testing.T) {
	base := t.TempDir()
	writeMLXModel := func(directory, modelType, architecture string) {
		t.Helper()
		if err := os.MkdirAll(directory, 0755); err != nil {
			t.Fatal(err)
		}
		configuration := `{"model_type":"` + modelType + `","architectures":["` + architecture + `"]}`
		if err := os.WriteFile(filepath.Join(directory, "config.json"), []byte(configuration), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "model.safetensors"), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	writeMLXModel(filepath.Join(base, "Qwen3-4B"), "qwen3", "Qwen3ForCausalLM")
	writeMLXModel(filepath.Join(base, "Qwen3-Embedding"), "qwen3", "Qwen3Model")
	pipeline := filepath.Join(base, "z-image-turbo")
	writeMLXModel(filepath.Join(pipeline, "text_encoder"), "qwen3", "Qwen3ForCausalLM")
	if err := os.WriteFile(filepath.Join(pipeline, "model_index.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}

	models, err := ListMLXModels(base)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].Path != "Qwen3-4B" {
		t.Fatalf("只應列出可對話的模型，實際：%+v", models)
	}
}
