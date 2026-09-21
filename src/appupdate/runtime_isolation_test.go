//go:build !windows

package appupdate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUpdateInstallerKeepsRuntimeInsideSwitchableDirectory(t *testing.T) {
	root := t.TempDir()
	existing := filepath.Join(root, "shared-runtime")
	if err := os.MkdirAll(existing, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(existing, "current")
	if err := os.WriteFile(marker, []byte("old runtime"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LLAMA_CPP_INSTALL_DIR", existing)
	payload := filepath.Join(root, "payload")
	if err := os.Mkdir(payload, 0700); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
set -eu
mkdir -p "$LLAMA_CPP_INSTALL_DIR/versions/fixture/bin"
printf 'new runtime' > "$LLAMA_CPP_INSTALL_DIR/versions/fixture/bin/llama-server"
ln -s versions/fixture "$LLAMA_CPP_INSTALL_DIR/current"
`
	if err := os.WriteFile(filepath.Join(payload, "install.sh"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if err := updateInstaller(payload).Run(); err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(marker); err != nil || string(content) != "old runtime" {
		t.Fatalf("更新準備改動了共用 Runtime：%q %v", content, err)
	}
	target := filepath.Join(root, "installed")
	if err := os.Rename(payload, target); err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(filepath.Join(target, "llama.cpp/current/bin/llama-server")); err != nil || string(content) != "new runtime" {
		t.Fatalf("切換後 Runtime 連結失效：%q %v", content, err)
	}
}
