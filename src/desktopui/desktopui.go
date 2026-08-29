// Package desktopui 負責判斷目前是否為支援的圖形工作階段，並啟動原生 UI 殼層。
package desktopui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	modeAuto  = "auto"
	modeGUI   = "gui"
	modeShell = "shell"
)

// Options 定義原生 UI 視窗的啟動參數。
type Options struct {
	URL       string
	Title     string
	Resident  bool
	Version   string
	APIURL    string
	GitHubURL string
}

// Launch 在支援的圖形工作階段啟動原生 UI。第二個回傳值代表是否成功啟動；
// done 會在使用者關閉視窗或 UI 程序結束時收到結果。
func Launch(ctx context.Context, options Options) (done <-chan error, launched bool, err error) {
	mode, err := requestedMode()
	if err != nil {
		return nil, false, err
	}
	if mode == modeShell {
		return nil, false, nil
	}
	if mode == modeAuto && !hasGraphicalSession() {
		return nil, false, nil
	}
	if mode == modeGUI && !supportsNativeUI() {
		return nil, false, fmt.Errorf("目前平台尚未提供原生 UI：%s/%s", runtime.GOOS, runtime.GOARCH)
	}

	binaryPath, err := resolveBinary()
	if err != nil {
		return nil, false, err
	}
	arguments := []string{
		"--url", strings.TrimSpace(options.URL),
		"--title", strings.TrimSpace(options.Title),
		"--resident", fmt.Sprintf("%t", options.Resident),
		"--version", strings.TrimSpace(options.Version),
		"--api-url", strings.TrimSpace(options.APIURL),
		"--github-url", strings.TrimSpace(options.GitHubURL),
	}
	if iconPath := resolveIcon(binaryPath); iconPath != "" {
		arguments = append(arguments, "--icon", iconPath)
	}
	cmd := exec.CommandContext(ctx, binaryPath, arguments...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, false, fmt.Errorf("啟動原生 UI 失敗: %w", err)
	}

	result := make(chan error, 1)
	go func() {
		result <- cmd.Wait()
		close(result)
	}()
	return result, true, nil
}

func requestedMode() (string, error) {
	value := firstEnvironmentValue("TANPOPO_UI", "OPEN_LOADER_UI", "LLAMA_LOADER_UI")
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "", modeAuto:
		return modeAuto, nil
	case modeGUI, "desktop", "window":
		return modeGUI, nil
	case modeShell, "cli", "headless", "off", "0":
		return modeShell, nil
	default:
		return "", fmt.Errorf("TANPOPO_UI 僅支援 auto、gui 或 shell，實際為 %q", value)
	}
}

func supportsNativeUI() bool {
	return runtime.GOOS == "darwin"
}

func resolveBinary() (string, error) {
	if configured := strings.TrimSpace(firstEnvironmentValue(
		"TANPOPO_DESKTOP_UI_BINARY",
		"OPEN_LOADER_DESKTOP_UI_BINARY",
		"LLAMA_LOADER_DESKTOP_UI_BINARY",
	)); configured != "" {
		if executableFile(configured) {
			return configured, nil
		}
		return "", fmt.Errorf("指定的原生 UI 執行檔不存在或不可執行：%s", configured)
	}

	platform := runtime.GOOS + "-" + runtime.GOARCH
	candidates := []string{
		filepath.Join("desktop-ui", "prebuilt", platform, "TanpopoUI"),
		filepath.Join("desktop-ui", "TanpopoUI"),
		filepath.Join("desktop-ui", "prebuilt", platform, "OpenLoaderUI"),
		filepath.Join("desktop-ui", "OpenLoaderUI"),
	}
	if executablePath, err := os.Executable(); err == nil {
		executableDirectory := filepath.Dir(executablePath)
		candidates = append(candidates,
			filepath.Join(executableDirectory, "desktop-ui", "TanpopoUI"),
			filepath.Join(executableDirectory, "..", "desktop-ui", "TanpopoUI"),
			filepath.Join(executableDirectory, "desktop-ui", "OpenLoaderUI"),
			filepath.Join(executableDirectory, "..", "desktop-ui", "OpenLoaderUI"),
		)
	}

	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		absolutePath, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		if _, exists := seen[absolutePath]; exists {
			continue
		}
		seen[absolutePath] = struct{}{}
		if executableFile(absolutePath) {
			return absolutePath, nil
		}
	}
	return "", errors.New("找不到原生 UI 執行檔；請先執行 run.command 或 build.command 建立")
}

func resolveIcon(binaryPath string) string {
	candidates := []string{
		filepath.Join(filepath.Dir(binaryPath), "TanpopoIcon.png"),
		filepath.Join("desktop-ui", "assets", "TanpopoIcon.png"),
	}
	for _, candidate := range candidates {
		absolutePath, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		if info, err := os.Stat(absolutePath); err == nil && info.Mode().IsRegular() {
			return absolutePath
		}
	}
	return ""
}

func firstEnvironmentValue(names ...string) string {
	for _, name := range names {
		if value := os.Getenv(name); strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func executableFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}
