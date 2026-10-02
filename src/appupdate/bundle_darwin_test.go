package appupdate

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// 額外指定既有正式 DMG 時，使用隔離目錄驗證掛載、簽章、Gatekeeper 與複製。
// 不替換真實 App、不啟動 UI，也不寫入使用者設定。
func TestMacOfficialDMGPreparationSmoke(t *testing.T) {
	archive := os.Getenv("TANPOPO_UPDATE_SMOKE_DMG")
	if archive == "" {
		t.Skip("設定 TANPOPO_UPDATE_SMOKE_DMG 可驗證正式 macOS 套件")
	}
	root := t.TempDir()
	mount := filepath.Join(root, "source")
	os.Mkdir(mount, 0700)
	if output, err := exec.Command("/usr/bin/hdiutil", "attach", "-readonly", "-nobrowse", "-mountpoint", mount, archive).CombinedOutput(); err != nil {
		t.Fatalf("掛載：%s %v", output, err)
	}
	defer exec.Command("/usr/bin/hdiutil", "detach", mount).Run()
	source := filepath.Join(mount, "Tanpopo.app")
	current := filepath.Join(root, "Tanpopo Installed.app")
	if output, err := exec.Command("/usr/bin/ditto", source, current).CombinedOutput(); err != nil {
		t.Fatalf("複製目前版本：%s %v", output, err)
	}
	if output, err := exec.Command("/usr/bin/hdiutil", "detach", mount).CombinedOutput(); err != nil {
		t.Fatalf("卸載測試來源：%s %v", output, err)
	}
	value := func(key string) string {
		output, err := exec.Command("/usr/bin/plutil", "-extract", key, "raw", "-o", "-", filepath.Join(current, "Contents", "Info.plist")).Output()
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(output))
	}
	tag := value("CFBundleShortVersionString") + "-build-" + value("CFBundleVersion")
	workspace := filepath.Join(root, "update")
	os.Mkdir(workspace, 0700)
	payload, err := prepareMacBundle(automaticPlan{Target: current, Workspace: workspace, Tag: tag}, archive)
	if err != nil {
		t.Fatal(err)
	}
	if !isRegularFile(filepath.Join(payload, "Contents", "MacOS", "Tanpopo")) {
		t.Fatal("未產生驗證後 App")
	}
	helper, err := stageUpdateHelper(filepath.Join(current, "Contents", "MacOS", "Tanpopo"), current, workspace, "darwin")
	if err != nil {
		t.Fatal(err)
	}
	if !isRegularFile(helper) {
		t.Fatal("未建立完整 App 助手")
	}
	if output, err := exec.Command("/usr/bin/codesign", "--verify", "--deep", "--strict", filepath.Join(workspace, "Updater.app")).CombinedOutput(); err != nil {
		t.Fatalf("助手 App 簽章失效：%s %v", output, err)
	}
	if err := verifyMacBundle(current, payload, "99.0.0-build-0001"); err == nil {
		t.Fatal("接受版本不符的 App")
	}
}

func TestMacSignedHelperBundleLaunchSmoke(t *testing.T) {
	root := t.TempDir()
	bundle := filepath.Join(root, "Fixture.app")
	main := filepath.Join(bundle, "Contents", "MacOS", "Tanpopo")
	if err := os.MkdirAll(filepath.Dir(main), 0700); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := copyRegularFile(executable, main, 0700); err != nil {
		t.Fatal(err)
	}
	plist := `<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>CFBundleExecutable</key><string>Tanpopo</string><key>CFBundleIdentifier</key><string>test.tanpopo.update</string><key>CFBundlePackageType</key><string>APPL</string></dict></plist>`
	if err := os.WriteFile(filepath.Join(bundle, "Contents", "Info.plist"), []byte(plist), 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("/usr/bin/codesign", "--force", "--sign", "-", "--timestamp=none", bundle).CombinedOutput(); err != nil {
		t.Fatalf("建立隔離簽章測試 App：%s %v", output, err)
	}
	workspace := filepath.Join(root, "stage")
	os.Mkdir(workspace, 0700)
	helper, err := stageUpdateHelper(main, bundle, workspace, "darwin")
	if err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(helper, "-test.run=^$").CombinedOutput(); err != nil {
		t.Fatalf("簽章助手啟動失敗：%s %v", output, err)
	}
}
