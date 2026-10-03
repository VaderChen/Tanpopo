package llamacpp

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func BenchmarkRuntimeLogAppend(b *testing.B) {
	buffer := newLogBuffer(128 * 1024)
	line := append(bytes.Repeat([]byte("x"), 127), '\n')
	for i := 0; i < 1024; i++ {
		_, _ = buffer.Write(line)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = buffer.Write(line)
	}
}

func BenchmarkMLXShardInventory(b *testing.B) {
	directory := b.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "config.json"), []byte(`{"model_type":"llama"}`), 0600); err != nil {
		b.Fatal(err)
	}
	weights := make(map[string]string)
	for i := 0; i < 4096; i++ {
		weights[fmt.Sprintf("layer.%d.weight", i)] = fmt.Sprintf("model-%d.safetensors", i%4)
	}
	for i := 0; i < 4; i++ {
		if err := os.WriteFile(filepath.Join(directory, fmt.Sprintf("model-%d.safetensors", i)), []byte("fixture"), 0600); err != nil {
			b.Fatal(err)
		}
	}
	data, _ := json.Marshal(map[string]any{"weight_map": weights})
	if err := os.WriteFile(filepath.Join(directory, "model.safetensors.index.json"), data, 0600); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !isMLXModelDirectory(directory) {
			b.Fatal("索引驗證失敗")
		}
	}
}

func BenchmarkGGUFTokenMetadata(b *testing.B) {
	var data bytes.Buffer
	data.WriteString("GGUF")
	write := func(value any) {
		if err := binary.Write(&data, binary.LittleEndian, value); err != nil {
			b.Fatal(err)
		}
	}
	text := func(value string) { write(uint64(len(value))); data.WriteString(value) }
	write(uint32(3))
	write(uint64(0))
	write(uint64(3))
	text("general.architecture")
	write(ggufTypeString)
	text("llama")
	text("llama.block_count")
	write(ggufTypeUint32)
	write(uint32(24))
	text("tokenizer.ggml.tokens")
	write(ggufTypeArray)
	write(ggufTypeString)
	write(uint64(32768))
	for i := 0; i < 32768; i++ {
		text("測試-token")
	}
	file := filepath.Join(b.TempDir(), "fixture.gguf")
	if err := os.WriteFile(file, data.Bytes(), 0600); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		value, err := readGGUFModelProfile(file)
		if err != nil || !value.isLanguageModel() {
			b.Fatalf("%+v %v", value, err)
		}
	}
}
