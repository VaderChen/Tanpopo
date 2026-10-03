package llamacpp

import "testing"

func TestRingArchitectureUsesRuntimeCapabilities(t *testing.T) {
	capability := RingCapabilities{GenericLinearSharding: true, TextModelTypes: []string{"qwen3_5", "future_model"}}
	for _, data := range []string{
		`{"model_type":"qwen3_5","vision_config":{"hidden_size":64}}`,
		`{"model_type":"future_model"}`,
	} {
		if _, err := inspectRingArchitecture([]byte(data), capability); err != nil {
			t.Fatal(err)
		}
	}
	for _, data := range []string{`{`, `{}`, `{"model_type":"qwen3_vl"}`} {
		if _, err := inspectRingArchitecture([]byte(data), capability); err == nil {
			t.Fatalf("接受未註冊架構：%s", data)
		}
	}
	if _, err := inspectRingArchitecture([]byte(`{"model_type":"qwen3_5"}`), RingCapabilities{}); err == nil {
		t.Fatal("舊 Runtime 不應被宣稱支援通用分散推論")
	}
}
