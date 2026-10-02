//go:build !windows

package appupdate

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// 使用獨立子程序回報 PID、nonce 與 HTTP health，實際切換含空白字元的安裝目錄。
func TestAutomaticSwapRestartAndRollbackSmoke(t *testing.T) {
	for _, failNew := range []bool{false, true} {
		t.Run(strconv.FormatBool(failNew), func(t *testing.T) {
			root := t.TempDir()
			target := filepath.Join(root, "Tanpopo installed")
			payload := filepath.Join(root, "update stage")
			backup := filepath.Join(root, "backups", "old")
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
			script := "#!/bin/sh\nexport TANPOPO_READY_TEST_HELPER=1\necho $$ > child.pid\nexec " + quote(executable) + " -test.run=^TestReadinessHelperProcess$\n"
			for _, dir := range []string{target, payload} {
				os.Mkdir(dir, 0700)
				os.WriteFile(filepath.Join(dir, "run.sh"), []byte(script), 0700)
			}
			os.WriteFile(filepath.Join(target, "version"), []byte("old"), 0600)
			os.WriteFile(filepath.Join(payload, "version"), []byte("new"), 0600)
			if failNew {
				os.WriteFile(filepath.Join(payload, "run.sh"), []byte("#!/bin/sh\nexit 42\n"), 0700)
			}
			defer func() {
				for _, dir := range []string{target, backup, payload + ".failed"} {
					data, _ := os.ReadFile(filepath.Join(dir, "child.pid"))
					pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
					if pid > 1 {
						killProcessGroup(pid)
					}
				}
			}()
			var starts []bool
			err = swapAndRestart(target, payload, backup, func(newVersion bool) error {
				starts = append(starts, newVersion)
				return launchTargetWithTimeout(target, 5*time.Second)
			})
			version, _ := os.ReadFile(filepath.Join(target, "version"))
			if failNew {
				if err == nil || !strings.Contains(err.Error(), "已還原") || string(version) != "old" || len(starts) != 2 || !starts[0] || starts[1] {
					t.Fatalf("還原流程失敗：%s %v %+v", version, err, starts)
				}
			} else {
				if err != nil || string(version) != "new" || len(starts) != 1 {
					t.Fatalf("更新流程失敗：%s %v", version, err)
				}
				old, _ := os.ReadFile(filepath.Join(backup, "version"))
				if string(old) != "old" {
					t.Fatal("未保留舊版備份")
				}
			}
		})
	}
}

func TestSwapFailureRestartsOriginalWithoutLosingFiles(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "installed")
	os.Mkdir(target, 0700)
	os.WriteFile(filepath.Join(target, "original"), []byte("keep"), 0600)
	restarted := false
	err := swapAndRestart(target, filepath.Join(root, "missing-payload"), filepath.Join(root, "backup"), func(newVersion bool) error {
		if newVersion {
			return errors.New("不應啟動新版")
		}
		restarted = true
		return nil
	})
	if err == nil || !restarted || !isRegularFile(filepath.Join(target, "original")) {
		t.Fatalf("切換失敗未恢復：%v", err)
	}
}

func TestRestartRejectsWrongRunningVersion(t *testing.T) {
	root := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
	script := filepath.Join(root, "run.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexport TANPOPO_READY_TEST_HELPER=1\nexec "+quote(executable)+" -test.run=^TestReadinessHelperProcess$\n"), 0700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(script)
	cmd.Dir = root
	err = launchReadyCommand(cmd, filepath.Join(root, "data"), 5*time.Second, "v99.0.0-build-0001")
	if err == nil || !strings.Contains(err.Error(), "版本不符") {
		t.Fatalf("未拒絕錯誤版本：%v", err)
	}
}

func TestPersistentPathsPreserveCustomDataWithoutCopyingExternalModels(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "installed")
	paths, err := persistentPaths(automaticPlan{Target: target, Directory: target, PreservePaths: []string{"data/settings.json", "custom/config.json", "models/local", filepath.Join(root, "external-models")}})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(paths, "|")
	if got != "agent.properties|custom/config.json|data|models/local" {
		t.Fatalf("保存路徑錯誤：%s", got)
	}
	if _, err := persistentPaths(automaticPlan{Target: target, Directory: target, PreservePaths: []string{target}}); err == nil {
		t.Fatal("接受整個安裝目錄為資料目錄")
	}
}
