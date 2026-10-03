package llamacpp

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestRuntimeFailureUsesCurrentLaunch(t *testing.T) {
	exit := errors.New("exit status 1")
	previous := "\n$ mlx-server old\nmlx-server error: 舊錯誤\n"
	current := "\n$ mlx-server new\nmlx-server error: [ring] Connection refused\n用法：mlx-server --model …\n"
	if actual := runtimeFailure(exit, previous+current); !strings.Contains(actual, "Connection refused") || strings.Contains(actual, "舊錯誤") {
		t.Fatal(actual)
	}
	if actual := runtimeFailure(exit, previous+"\n$ mlx-server new\n"); actual != exit.Error() {
		t.Fatalf("採用了上一次啟動的錯誤：%s", actual)
	}
	if actual := runtimeFailure(errors.New("exit status 70"), "\n$ mlx-server\n分散式群組失敗：傳輸中斷\n"); !strings.Contains(actual, "傳輸中斷") {
		t.Fatalf("未保留執行中發生的群組錯誤：%s", actual)
	}
	actual := runtimeFailure(exit, "\n$ mlx-server\nmlx-server error: "+strings.Repeat("錯誤", 500))
	if len(actual) > 850 || !utf8.ValidString(actual) {
		t.Fatal("過長錯誤應保留有效 UTF-8 並限制長度")
	}
}
