//go:build !windows

package appupdate

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestLaunchRejectsEarlyExitAndTimeout(t *testing.T) {
	for _, script := range []string{"exit 42", "exec sleep 30"} {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "run.sh"), []byte("#!/bin/sh\n"+script+"\n"), 0755)
		if err := launchTargetWithTimeout(dir, 150*time.Millisecond); err == nil {
			t.Fatal("未就緒的程序被當成成功")
		}
	}
}

func TestReadinessHelperProcess(t *testing.T) {
	if os.Getenv("TANPOPO_READY_TEST_HELPER") != "1" {
		return
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"status":"ok"}`)) }))
	if err := ReportReady(server.URL + "/"); err != nil {
		os.Exit(2)
	}
	select {}
}

func TestLaunchRequiresNewInstanceHealthAcknowledgement(t *testing.T) {
	dir := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// exec 保留 run.sh 的 PID，與正式發布腳本相同。
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	script := "#!/bin/sh\nexport TANPOPO_READY_TEST_HELPER=1\necho $$ > child.pid\nexec " + quote(executable) + " -test.run=^TestReadinessHelperProcess$\n"
	os.WriteFile(filepath.Join(dir, "run.sh"), []byte(script), 0755)
	defer func() {
		data, _ := os.ReadFile(filepath.Join(dir, "child.pid"))
		pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
		if pid > 1 {
			killProcessGroup(pid)
		}
	}()
	if err := launchTargetWithTimeout(dir, 5*time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestReportReadyRejectsUnhealthyResponse(t *testing.T) {
	for _, response := range []string{`{"status":"failed"}`, `invalid`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(response)) }))
		path := filepath.Join(t.TempDir(), "ready.json")
		t.Setenv(readyPathEnv, path)
		t.Setenv(readyTokenEnv, "fixture")
		if err := ReportReady(server.URL + "/"); err == nil {
			t.Fatal("接受了不健康的回應")
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("不健康的服務寫入 ready")
		}
		server.Close()
	}
}

func TestReadyRecordIsBoundToPIDAndNonce(t *testing.T) {
	dir := t.TempDir()
	script := `#!/bin/sh
printf '{"pid":1,"token":"wrong"}' > "$TANPOPO_UPDATE_READY_PATH"
exec sleep 30
`
	os.WriteFile(filepath.Join(dir, "run.sh"), []byte(script), 0755)
	if err := launchTargetWithTimeout(dir, 150*time.Millisecond); err == nil {
		t.Fatal("接受了其他實例的 ready")
	}
}
