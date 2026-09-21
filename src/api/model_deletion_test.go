package api

import (
	"LlamaLoader/src/domain"
	"LlamaLoader/src/llamacpp"
	"testing"
)

func TestModelDeletionMatchesOnlyOwnedActiveAssets(t *testing.T) {
	status := domain.LlamaStatus{Runtime: domain.RuntimeLlamaServer, Model: "family/a/model.gguf", DraftModel: "family/draft/model.gguf", MMProj: "family/vision/mmproj.gguf"}
	for _, path := range []string{status.Model, status.DraftModel, status.MMProj} {
		if !llamacpp.ActiveModelMatches(status, "gguf", path) {
			t.Fatalf("未保護使用中的資產：%s", path)
		}
	}
	if llamacpp.ActiveModelMatches(status, "gguf", "family/b/model.gguf") {
		t.Fatal("兄弟模型被誤認為使用中")
	}
	status = domain.LlamaStatus{Runtime: domain.RuntimeMLXServer, Model: "gguf:family/a/model.gguf", DraftModel: "draft/model"}
	if !llamacpp.ActiveModelMatches(status, "gguf", "family/a/model.gguf") || !llamacpp.ActiveModelMatches(status, "mlx", "draft") {
		t.Fatal("MLX 的 GGUF 與 Draft 資產保護失敗")
	}
}
