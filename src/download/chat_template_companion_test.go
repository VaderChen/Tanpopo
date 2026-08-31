package download

import (
	"encoding/json"
	"testing"
)

func TestSelectCompanionFilenamesIncludesChatTemplate(t *testing.T) {
	info := repositoryInfo{}
	for _, name := range []string{"Model-Q4_K_M.gguf", "mmproj-BF16.gguf",
		"chat_template.jinja", "config.json", "README.md"} {
		info.Siblings = append(info.Siblings, struct {
			Filename string `json:"rfilename"`
		}{Filename: name})
	}
	got := selectCompanionFilenames("Model-Q4_K_M.gguf", info)
	found := map[string]bool{}
	for _, name := range got {
		found[name] = true
	}
	if !found["mmproj-BF16.gguf"] {
		t.Fatalf("mmproj 應被配對，實際：%v", got)
	}
	if !found["chat_template.jinja"] {
		t.Fatalf("chat_template.jinja 應被配對，實際：%v", got)
	}
	// config.json 帶有 layer_types、gating 等 GGUF metadata 無法完整表達的欄位。
	if !found["config.json"] {
		t.Fatalf("config.json 應被配對，實際：%v", got)
	}
	if found["README.md"] {
		t.Fatalf("不相關檔案不應被配對：%v", got)
	}
}

// mmproj 或 dflash 本身作為主檔時不再配對其他檔案，這個既有行為不可被破壞。
func TestCompanionSelectionSkipsForProjectorMainFile(t *testing.T) {
	info := repositoryInfo{}
	info.Siblings = append(info.Siblings, struct {
		Filename string `json:"rfilename"`
	}{Filename: "chat_template.jinja"})
	if got := selectCompanionFilenames("mmproj-BF16.gguf", info); len(got) != 0 {
		t.Fatalf("mmproj 主檔不應配對，實際：%v", got)
	}
}

func TestChooseChatTemplatePrefersSameDirectory(t *testing.T) {
	candidates := []string{"chat_template.jinja", "gguf/chat_template.jinja"}
	if got := chooseChatTemplate("gguf/Model-Q4_K_M.gguf", candidates); got != "gguf/chat_template.jinja" {
		t.Fatalf("應優先同目錄，實際：%s", got)
	}
	if got := chooseChatTemplate("Model.gguf", []string{"deep/nested/chat_template.jinja",
		"chat_template.jinja"}); got != "chat_template.jinja" {
		t.Fatalf("無同目錄時應取路徑最淺者，實際：%s", got)
	}
}

func TestMissingRuntimeAssetsDetectsGaps(t *testing.T) {
	if got := missingRuntimeAssets([]string{"mmproj-BF16.gguf"}); len(got) != 2 {
		t.Fatalf("兩個語意檔案都缺時應回報 2 個，實際：%v", got)
	}
	if got := missingRuntimeAssets([]string{"config.json"}); len(got) != 1 ||
		got[0] != "chat_template.jinja" {
		t.Fatalf("只缺 template 時應僅回報它，實際：%v", got)
	}
	if got := missingRuntimeAssets([]string{"config.json", "chat_template.jinja"}); len(got) != 0 {
		t.Fatalf("都齊全時不應回報，實際：%v", got)
	}
}

func TestBaseModelRepositoryParsesCardData(t *testing.T) {
	cases := []struct {
		name string
		json string
		want string
	}{
		{"字串形式", `{"id":"unsloth/X-GGUF","cardData":{"base_model":"Qwen/Qwen3.8-27B"}}`, "Qwen/Qwen3.8-27B"},
		{"陣列形式", `{"id":"unsloth/X-GGUF","cardData":{"base_model":["Qwen/Qwen3.8-27B"]}}`, "Qwen/Qwen3.8-27B"},
		{"完整網址", `{"id":"a/b","cardData":{"base_model":"https://huggingface.co/Qwen/Qwen3.8-27B"}}`, "Qwen/Qwen3.8-27B"},
		{"未標註", `{"id":"a/b"}`, ""},
		{"指向自己時忽略", `{"id":"a/b","cardData":{"base_model":"a/b"}}`, ""},
		{"格式不符時忽略", `{"id":"a/b","cardData":{"base_model":{"x":1}}}`, ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			var info repositoryInfo
			if err := json.Unmarshal([]byte(testCase.json), &info); err != nil {
				t.Fatal(err)
			}
			if got := baseModelRepository(info); got != testCase.want {
				t.Fatalf("got %q, want %q", got, testCase.want)
			}
		})
	}
}
