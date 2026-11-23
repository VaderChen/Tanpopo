package llamacpp

import (
	"errors"
	"fmt"
	"strings"

	"LlamaLoader/src/domain"
)

// 使用參數語意辨識模式，避免依賴預設命令名稱或模型檔名。
func usesMTP(command domain.StartupCommand) bool {
	if command.Runtime == domain.RuntimeMLXServer {
		return hasAnyArgument(command.ExtraArgs, "--mtp-draft", "--mtp-block-size")
	}
	for i, arg := range command.ExtraArgs {
		if arg == "--spec-type=draft-mtp" || (arg == "--spec-type" && i+1 < len(command.ExtraArgs) && command.ExtraArgs[i+1] == "draft-mtp") {
			return true
		}
	}
	return false
}
func isGGUFMTPDraft(path string) bool {
	p, err := readGGUFModelProfile(path)
	return err == nil && p.MTPDraft
}
func validateGGUFMTPPair(target, draft string) error {
	t, err := readGGUFModelProfile(target)
	if err != nil {
		return err
	}
	d, err := readGGUFModelProfile(draft)
	if err != nil {
		return err
	}
	if !d.MTPDraft || t.MTPDraft || t.Architecture != d.Architecture {
		return errors.New("MTP Target 與 Draft 架構或角色不相容")
	}
	blockKey := t.Architecture + ".block_count"
	if t.Dimensions[blockKey]-int64(t.NextNPredictLayers) != d.Dimensions[blockKey]-int64(d.NextNPredictLayers) {
		return errors.New("MTP 主幹層數不一致")
	}
	for key, value := range t.Dimensions {
		if key == blockKey {
			continue
		}
		// MTP 檔保留主模型設定；PLE 是 Target 專用，不要求存在於 Draft。
		if strings.Contains(key, ".ple.") || strings.HasSuffix(key, ".embedding_length_per_layer_input") {
			continue
		}
		if other, ok := d.Dimensions[key]; ok && other != value {
			return fmt.Errorf("MTP 設定不相容：%s target=%d draft=%d", key, value, other)
		}
	}
	return nil
}
func ListMLXRuntimeDraftModels(mlxDirectory, ggufDirectory string) ([]domain.ModelFile, error) {
	var result []domain.ModelFile
	if strings.TrimSpace(mlxDirectory) != "" {
		models, err := ListMLXDraftModels(mlxDirectory)
		if err != nil {
			return nil, err
		}
		result = append(result, models...)
	}
	if strings.TrimSpace(ggufDirectory) != "" {
		models, err := ListModels(ggufDirectory)
		if err != nil {
			return nil, err
		}
		for _, model := range models {
			if model.MTPDraft {
				model.Path = mlxGGUFPathPrefix + model.Path
				result = append(result, model)
			}
		}
	}
	return result, nil
}
