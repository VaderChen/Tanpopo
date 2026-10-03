package distributed_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// 以 Go 產生固定的小型 safetensors；用於數值與服務流程驗證，不測語意品質。
func writeFixture(t *testing.T, directory, architecture string) {
	t.Helper()
	must(t, os.MkdirAll(directory, 0700))
	configuration := map[string]any{
		"model_type": architecture, "hidden_size": 64, "intermediate_size": 128,
		"num_hidden_layers": 2, "num_attention_heads": 4, "num_key_value_heads": 2,
		"head_dim": 16, "rms_norm_eps": 1e-5, "vocab_size": 128,
		"max_position_embeddings": 4096, "rope_theta": 10000,
		"tie_word_embeddings": false, "attention_bias": false, "eos_token_id": 2,
	}
	hybrid := architecture == "qwen3_5" || architecture == "qwen3_5_text"
	if hybrid {
		configuration["full_attention_interval"] = 2
		configuration["linear_num_value_heads"] = 2
		configuration["linear_num_key_heads"] = 1
		configuration["linear_key_head_dim"] = 64
		configuration["linear_value_head_dim"] = 64
		configuration["linear_conv_kernel_dim"] = 4
	}
	if architecture == "qwen3_5" {
		configuration["model_type"] = "qwen3_5_text"
		configuration = map[string]any{"model_type": architecture, "text_config": configuration,
			"vision_config": map[string]any{"model_type": architecture}}
	}
	writeJSON(t, filepath.Join(directory, "config.json"), configuration)
	type tensor struct {
		shape []int
		data  []byte
	}
	arrays := map[string]tensor{}
	add := func(name string, shape []int, norm, zero bool) {
		count := 1
		for _, dimension := range shape {
			count *= dimension
		}
		hash := sha256.Sum256([]byte(name))
		phase := float64(binary.LittleEndian.Uint16(hash[:2])) / 1000
		data := make([]byte, count*4)
		for index := 0; index < count; index++ {
			value := float32(math.Sin(float64(index)*0.07+phase) * 0.05)
			if norm {
				value = 1
			}
			if zero {
				value = 0
			}
			binary.LittleEndian.PutUint32(data[index*4:], math.Float32bits(value))
		}
		arrays[name] = tensor{shape, data}
	}
	add("model.embed_tokens.weight", []int{128, 64}, false, false)
	add("model.norm.weight", []int{64}, true, false)
	add("lm_head.weight", []int{128, 64}, false, false)
	for index := 0; index < 2; index++ {
		base := fmt.Sprintf("model.layers.%d.", index)
		if hybrid && index == 0 {
			// 同一個模型同時包含循環狀態、深度卷積與完整注意力，驗證一般線性層
			// 分散後不改變非線性層及 MambaCache 的原有行為。
			for name, shape := range map[string][]int{"in_proj_qkv": {256, 64}, "in_proj_z": {128, 64},
				"in_proj_b": {2, 64}, "in_proj_a": {2, 64}, "out_proj": {64, 128}, "conv1d": {256, 4, 1}} {
				add(base+"linear_attn."+name+".weight", shape, false, false)
			}
			add(base+"linear_attn.dt_bias", []int{2}, false, true)
			add(base+"linear_attn.A_log", []int{2}, false, true)
			add(base+"linear_attn.norm.weight", []int{64}, true, false)
		} else {
			projections := map[string]int{"q_proj": 64, "k_proj": 32, "v_proj": 32, "o_proj": 64}
			if hybrid {
				projections["q_proj"] = 128
			}
			for name, rows := range projections {
				add(base+"self_attn."+name+".weight", []int{rows, 64}, false, false)
				if architecture == "qwen2" && name != "o_proj" {
					add(base+"self_attn."+name+".bias", []int{rows}, false, true)
				}
			}
			if architecture == "qwen3" || hybrid {
				for _, name := range []string{"q_norm", "k_norm"} {
					add(base+"self_attn."+name+".weight", []int{16}, true, false)
				}
			}
		}
		for name, shape := range map[string][]int{"gate_proj": {128, 64}, "up_proj": {128, 64}, "down_proj": {64, 128}} {
			add(base+"mlp."+name+".weight", shape, false, false)
		}
		for _, name := range []string{"input_layernorm", "post_attention_layernorm"} {
			add(base+name+".weight", []int{64}, true, false)
		}
	}
	names := make([]string, 0, len(arrays))
	for name := range arrays {
		names = append(names, name)
	}
	sort.Strings(names)
	header, payload := map[string]any{}, new(bytes.Buffer)
	for _, name := range names {
		value := arrays[name]
		header[name] = map[string]any{"dtype": "F32", "shape": value.shape,
			"data_offsets": []int{payload.Len(), payload.Len() + len(value.data)}}
		payload.Write(value.data)
	}
	encoded, err := json.Marshal(header)
	must(t, err)
	for len(encoded)%8 != 0 {
		encoded = append(encoded, ' ')
	}
	var file bytes.Buffer
	must(t, binary.Write(&file, binary.LittleEndian, uint64(len(encoded))))
	file.Write(encoded)
	file.Write(payload.Bytes())
	must(t, os.WriteFile(filepath.Join(directory, "model.safetensors"), file.Bytes(), 0600))
	vocab := map[string]int{"<unk>": 0, "<bos>": 1, "<eos>": 2}
	for character := '!'; character <= '~'; character++ {
		vocab[string(character)] = len(vocab)
	}
	for _, character := range []string{"Ġ", "Ċ"} {
		vocab[character] = len(vocab)
	}
	for len(vocab) < 128 {
		vocab[fmt.Sprintf("<t%d>", len(vocab))] = len(vocab)
	}
	writeJSON(t, filepath.Join(directory, "tokenizer.json"), map[string]any{
		"version": "1.0", "added_tokens": []any{}, "normalizer": nil, "post_processor": nil,
		"pre_tokenizer": map[string]any{"type": "ByteLevel", "add_prefix_space": false, "trim_offsets": true, "use_regex": true},
		"decoder":       map[string]any{"type": "ByteLevel", "add_prefix_space": false, "trim_offsets": true, "use_regex": true},
		"model":         map[string]any{"type": "BPE", "vocab": vocab, "merges": []any{}, "unk_token": "<unk>", "fuse_unk": false},
	})
	writeJSON(t, filepath.Join(directory, "tokenizer_config.json"), map[string]any{
		"tokenizer_class": "PreTrainedTokenizer", "unk_token": "<unk>", "bos_token": "<bos>", "eos_token": "<eos>",
		"chat_template": "{% for message in messages %}{{ message['content'] }}{% endfor %}", "model_max_length": 4096,
	})
}

func writeJSON(t *testing.T, name string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	must(t, err)
	must(t, os.WriteFile(name, data, 0600))
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
