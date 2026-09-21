package download

import (
	"math"
	"testing"
)

func TestRangePlannerHandlesMaximumLength(t *testing.T) {
	defer func() {
		if failure := recover(); failure != nil {
			t.Fatalf("合法整數邊界造成 panic：%v", failure)
		}
	}()
	// 只要求第一個區塊，不應隨遠端 total 配置整份工作清單。
	for item := range planDownloadRanges(math.MaxInt64, defaultDownloadChunk) {
		if item.Start != 0 || item.End != defaultDownloadChunk-1 {
			t.Fatal(item)
		}
		break
	}
}
