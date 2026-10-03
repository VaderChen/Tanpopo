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
	"strings"
	"testing"
)

// 以 Go 產生固定的小型 safetensors；用於數值與服務流程驗證，不測語意品質。
func writeFixture(t *testing.T, directory, architecture string) {
	t.Helper()
	must(t, os.MkdirAll(directory, 0700))
	bits := 0
	switch architecture {
	case "llama_q4":
		architecture, bits = "llama", 4
	case "llama_q8":
		architecture, bits = "llama", 8
	}
	configuration := map[string]any{
		"model_type": architecture, "hidden_size": 64, "intermediate_size": 128,
		"num_hidden_layers": 2, "num_attention_heads": 4, "num_key_value_heads": 2,
		"head_dim": 16, "rms_norm_eps": 1e-5, "vocab_size": 128,
		"max_position_embeddings": 4096, "rope_theta": 10000,
		"tie_word_embeddings": false, "attention_bias": false, "eos_token_id": 2,
	}
	if bits != 0 {
		configuration["quantization"] = map[string]any{"bits": bits, "group_size": 64}
	}
	if architecture == "qwen3_moe" {
		configuration["num_experts"] = 4
		configuration["num_experts_per_tok"] = 2
		configuration["decoder_sparse_step"] = 1
		configuration["mlp_only_layers"] = []int{1}
		configuration["moe_intermediate_size"] = 128
	}
	if architecture == "phi3" {
		configuration["original_max_position_embeddings"] = 4096
	}
	if architecture == "gemma2" {
		configuration["attn_logit_softcapping"] = 50
		configuration["final_logit_softcapping"] = 30
		configuration["query_pre_attn_scalar"] = 16
		configuration["tie_word_embeddings"] = true
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
		dtype string
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
		arrays[name] = tensor{shape, data, "F32"}
	}
	add("model.embed_tokens.weight", []int{128, 64}, false, false)
	add("model.norm.weight", []int{64}, true, false)
	if architecture != "gemma2" {
		add("lm_head.weight", []int{128, 64}, false, false)
	}
	if architecture == "starcoder2" {
		add("model.norm.bias", []int{64}, false, true)
	}
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
			if architecture == "phi3" {
				// 合併 QKV 投影仍由相同 Linear 分片機制處理。
				projections = map[string]int{"qkv_proj": 128, "o_proj": 64}
			}
			if hybrid {
				projections["q_proj"] = 128
			}
			for name, rows := range projections {
				add(base+"self_attn."+name+".weight", []int{rows, 64}, false, false)
				if architecture == "qwen2" && name != "o_proj" {
					add(base+"self_attn."+name+".bias", []int{rows}, false, true)
				}
				if architecture == "starcoder2" {
					add(base+"self_attn."+name+".bias", []int{rows}, false, false)
				}
			}
			if architecture == "qwen3" || architecture == "qwen3_moe" || hybrid {
				for _, name := range []string{"q_norm", "k_norm"} {
					add(base+"self_attn."+name+".weight", []int{16}, true, false)
				}
			}
		}
		mlp := map[string][]int{"gate_proj": {128, 64}, "up_proj": {128, 64}, "down_proj": {64, 128}}
		if architecture == "phi3" {
			mlp = map[string][]int{"gate_up_proj": {256, 64}, "down_proj": {64, 128}}
		} else if architecture == "starcoder2" {
			mlp = map[string][]int{"c_fc": {128, 64}, "c_proj": {64, 128}}
		} else if architecture == "qwen3_moe" && index == 0 {
			// SwitchLinear 專家留在主節點，只有路由及注意力的一般 Linear 分片。
			mlp = map[string][]int{"gate": {4, 64}, "switch_mlp.gate_proj": {4, 128, 64},
				"switch_mlp.up_proj": {4, 128, 64}, "switch_mlp.down_proj": {4, 64, 128}}
		}
		for name, shape := range mlp {
			add(base+"mlp."+name+".weight", shape, false, false)
			if architecture == "starcoder2" {
				add(base+"mlp."+name+".bias", []int{shape[0]}, false, false)
			}
		}
		norms := []string{"input_layernorm", "post_attention_layernorm"}
		if architecture == "gemma2" {
			norms = append(norms, "pre_feedforward_layernorm", "post_feedforward_layernorm")
		}
		for _, name := range norms {
			add(base+name+".weight", []int{64}, true, false)
			if architecture == "starcoder2" {
				add(base+name+".bias", []int{64}, false, true)
			}
		}
	}
	if bits != 0 {
		// 直接寫入 MLX affine 的 U32 packed weights／F32 scales／biases；
		// 保留 Embedding 與 Norm 為 F32，驗證同一模型的混合資料型別載入。
		for name, value := range arrays {
			if len(value.shape) != 2 || value.dtype != "F32" || !strings.HasSuffix(name, ".weight") || name == "model.embed_tokens.weight" {
				continue
			}
			rows, columns := value.shape[0], value.shape[1]
			packedColumns := columns * bits / 32
			packed := make([]byte, rows*packedColumns*4)
			scales, biases := make([]byte, rows*(columns/64)*4), make([]byte, rows*(columns/64)*4)
			maximum := (1 << bits) - 1
			scale, bias := float32(0.1/float64(maximum)), float32(-0.05)
			for i := 0; i < len(scales)/4; i++ {
				binary.LittleEndian.PutUint32(scales[i*4:], math.Float32bits(scale))
				binary.LittleEndian.PutUint32(biases[i*4:], math.Float32bits(bias))
			}
			for row := 0; row < rows; row++ {
				for column := 0; column < columns; column++ {
					value := math.Float32frombits(binary.LittleEndian.Uint32(value.data[(row*columns+column)*4:]))
					q := uint32(min(max(int(math.Round(float64((value-bias)/scale))), 0), maximum))
					offset := (row*packedColumns + column/(32/bits)) * 4
					word := binary.LittleEndian.Uint32(packed[offset:]) | q<<uint((column%(32/bits))*bits)
					binary.LittleEndian.PutUint32(packed[offset:], word)
				}
			}
			arrays[name] = tensor{[]int{rows, packedColumns}, packed, "U32"}
			base := strings.TrimSuffix(name, ".weight")
			arrays[base+".scales"] = tensor{[]int{rows, columns / 64}, scales, "F32"}
			arrays[base+".biases"] = tensor{[]int{rows, columns / 64}, biases, "F32"}
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
		header[name] = map[string]any{"dtype": value.dtype, "shape": value.shape,
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
