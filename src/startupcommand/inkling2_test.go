package startupcommand

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"LlamaLoader/src/domain"
)

func TestInklingPresetMigrationPreservesUserConfiguration(t *testing.T) {
	file := filepath.Join(t.TempDir(), "commands.json")
	fallback := domain.StartupCommand{ServerHost: "127.0.0.1", ServerPort: 8080, GPULayers: -1}
	custom := builtinCommands(fallback, time.Now())[0]
	custom.ContextSize = 4096
	custom.ExtraArgs = []string{"--temperature", "0.4"}
	data, err := json.Marshal(fileData{Version: 16, Commands: []domain.StartupCommand{custom}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(file, fallback)
	if err != nil {
		t.Fatal(err)
	}
	if store.commands[0].ContextSize != custom.ContextSize || !argumentsEqual(store.commands[0].ExtraArgs, custom.ExtraArgs) {
		t.Fatal("新增模型設定不可覆寫使用者參數")
	}
	found := 0
	for _, command := range store.commands {
		if command.ID != "inkling2-mini" {
			continue
		}
		found++
		if command.Runtime != domain.RuntimeLlamaServer || command.ContextSize != 8192 || command.DraftModel != "" ||
			!argumentsEqual(command.ExtraArgs, []string{"--parallel", "1", "--batch-size", "128", "--ubatch-size", "64", "--jinja"}) {
			t.Fatalf("保守啟動參數不正確：%+v", command)
		}
	}
	if found != 1 {
		t.Fatalf("預設數量=%d", found)
	}
	reopened, err := NewStore(file, fallback)
	if err != nil {
		t.Fatal(err)
	}
	if len(store.commands) != len(reopened.commands) {
		t.Fatal("重開後預設不可重複")
	}
}
