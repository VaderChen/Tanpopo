package startupcommand

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"LlamaLoader/src/domain"
)

func TestDiskProfilesMigrationPreservesCustomizedCommands(t *testing.T) {
	path := filepath.Join(t.TempDir(), "commands.json")
	fallback := domain.StartupCommand{ServerHost: "0.0.0.0", ServerPort: 8080, ContextSize: 8192, GPULayers: -1}
	old := builtinCommands(fallback, time.Now())[0]
	old.ContextSize = 4096
	old.ExtraArgs = []string{"--temperature", "0.3"}
	data, err := json.Marshal(fileData{Version: 15, Commands: []domain.StartupCommand{old}})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(path, fallback)
	if err != nil {
		t.Fatal(err)
	}
	if store.commands[0].ContextSize != 4096 || !argumentsEqual(store.commands[0].ExtraArgs, old.ExtraArgs) {
		t.Fatal("新增預設不得覆寫既有參數")
	}
	for _, id := range []string{"mlx-disk-offload", "llama-disk-offload"} {
		count := 0
		for _, command := range store.commands {
			if command.ID == id {
				count++
				if command.ContextSize != 8192 {
					t.Fatal("磁碟預設應使用 8K")
				}
			}
		}
		if count != 1 {
			t.Fatalf("%s count=%d", id, count)
		}
	}
	reopened, err := NewStore(path, fallback)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.commands) != len(store.commands) {
		t.Fatal("重開不應重複建立預設")
	}
}
