package llamacpp

import (
	"reflect"
	"testing"

	"LlamaLoader/src/domain"
)

func TestWithManagedKVCacheQuantizationMLXInjectsQuantizedStart(t *testing.T) {
	got := withManagedKVCacheQuantization(
		[]string{"--prefill-step-size", "2048"},
		domain.RuntimeMLXServer,
		domain.KVCacheQuantizationQ4,
	)
	want := []string{
		"--prefill-step-size", "2048",
		"--kv-bits", "4",
		"--kv-group-size", "64",
		"--quantized-kv-start", "2048",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("受管 KV 量化參數不符\n got: %v\nwant: %v", got, want)
	}
}

// KV Cache 量化關閉時（DFlash 啟用即為此情況，兩者由 Start 互斥），
// 啟動參數裡手動加的 KV 旗標也必須清掉，否則 mlx-server 會退回標準生成。
func TestWithManagedKVCacheQuantizationNoneStripsManualFlags(t *testing.T) {
	got := withManagedKVCacheQuantization(
		[]string{
			"--kv-bits", "4",
			"--kv-scheme", "affine4",
			"--quantized-kv-start", "2048",
			"--dflash-block-size", "5",
		},
		domain.RuntimeMLXServer,
		domain.KVCacheQuantizationNone,
	)
	want := []string{"--dflash-block-size", "5"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("未清乾淨受管 KV 參數\n got: %v\nwant: %v", got, want)
	}
}

func TestWithoutMLXRotatingKVArguments(t *testing.T) {
	cases := []struct {
		name  string
		input []string
		want  []string
	}{
		{
			name:  "分開的 --max-kv-size",
			input: []string{"--max-kv-size", "8192", "--temperature", "0"},
			want:  []string{"--temperature", "0"},
		},
		{
			name:  "分開的 --ctx-size",
			input: []string{"--ctx-size", "262144", "--dflash-block-size", "5"},
			want:  []string{"--dflash-block-size", "5"},
		},
		{
			name:  "等號形式",
			input: []string{"--ctx-size=8192", "--max-kv-size=4096", "--top-k", "20"},
			want:  []string{"--top-k", "20"},
		},
		{
			name:  "沒有 rotating KV 參數時原樣保留",
			input: []string{"--prefill-step-size", "2048"},
			want:  []string{"--prefill-step-size", "2048"},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := withoutMLXRotatingKVArguments(testCase.input)
			if !reflect.DeepEqual(got, testCase.want) {
				t.Fatalf("got: %v\nwant: %v", got, testCase.want)
			}
		})
	}
}
