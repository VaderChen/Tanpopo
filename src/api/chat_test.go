package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProxyRuntimeChatStreamPreservesSSEAndFlushes(t *testing.T) {
	const stream = "data: {\"choices\":[{\"delta\":{\"content\":\"Hello\"}}]}\n\n" +
		"data: [DONE]\n\n"
	response := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(stream)),
		Header:     make(http.Header),
	}
	recorder := httptest.NewRecorder()

	proxyRuntimeChatStream(recorder, response)

	if !recorder.Flushed {
		t.Fatal("SSE 回應沒有呼叫 Flush")
	}
	if contentType := recorder.Header().Get("Content-Type"); contentType != "text/event-stream; charset=utf-8" {
		t.Fatalf("Content-Type = %q", contentType)
	}
	if recorder.Body.String() != stream {
		t.Fatalf("SSE 內容遭到改寫：%q", recorder.Body.String())
	}
}
