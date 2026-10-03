package api

import (
	"encoding/json"
	"testing"
)

func TestChatContentPreservesTextAndInlineImages(t *testing.T) {
	for _, content := range []string{`"說明這個模型"`, `[{"type":"text","text":"說明圖片"},{"type":"image_url","image_url":{"url":"data:image/png;base64,eA=="}}]`} {
		input := `{"role":"user","content":` + content + `}`
		var message chatMessage
		if err := json.Unmarshal([]byte(input), &message); err != nil {
			t.Fatal(err)
		}
		if err := validateChatMessages([]chatMessage{message}); err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(message)
		if err != nil {
			t.Fatal(err)
		}
		var result struct{ Content json.RawMessage }
		if err := json.Unmarshal(encoded, &result); err != nil {
			t.Fatal(err)
		}
		if string(result.Content) != content {
			t.Fatalf("內容被改寫：%s", encoded)
		}
	}
}

func TestChatContentRejectsInvalidAndRemoteImages(t *testing.T) {
	for _, input := range []string{
		`{"role":"user","content":[]}`,
		`{"role":"user","content":42}`,
		`{"role":"user","content":[{"type":"image_url","image_url":{"url":"http://127.0.0.1/private"}}]}`,
		`{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,invalid!"}}]}`,
		`{"role":"user","content":[{"type":"executable","text":"run"}]}`,
		`{"role":"user","content":[{"type":"text","text":"hello","unexpected":true}]}`,
		`{"role":"system","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,eA=="}}]}`,
	} {
		var message chatMessage
		if err := json.Unmarshal([]byte(input), &message); err == nil && validateChatMessages([]chatMessage{message}) == nil {
			t.Fatalf("接受無效訊息：%s", input)
		}
	}
}
