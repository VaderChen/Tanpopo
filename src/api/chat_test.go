package api

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type failedFlushWriter struct {
	*httptest.ResponseRecorder
	flushCount int
	failAt     int
}

func (w *failedFlushWriter) FlushError() error {
	w.flushCount++
	if w.flushCount >= w.failAt {
		return errors.New("client disconnected")
	}
	return nil
}

type countedStreamReader struct{ reads int }

func (r *countedStreamReader) Read(buffer []byte) (int, error) {
	r.reads++
	if r.reads > 2 {
		return 0, io.EOF
	}
	return copy(buffer, "data: {}\n\n"), nil
}

func TestProxyRuntimeChatStreamStopsOnFlushFailure(t *testing.T) {
	for _, failAt := range []int{1, 2} {
		body := &countedStreamReader{}
		writer := &failedFlushWriter{ResponseRecorder: httptest.NewRecorder(), failAt: failAt}
		proxyRuntimeChatStream(writer, &http.Response{Body: io.NopCloser(body)})
		if body.reads != failAt-1 {
			t.Errorf("第 %d 次 Flush 失敗後仍讀取上游：reads=%d", failAt, body.reads)
		}
	}
}

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
