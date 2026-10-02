package api

import (
	"encoding/json"
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

func TestRuntimeChatErrorsPreserveClientStatus(t *testing.T) {
	for _, test := range []struct {
		upstream int
		body     string
		want     int
	}{
		{400, `{"error":{"message":"bad request"}}`, 400},
		{401, `{"error":{"message":"invalid key"}}`, 401},
		{403, `{"error":{"message":"IP denied"}}`, 403},
		{413, `{"error":{"message":"context too large"}}`, 413},
		{429, `{"error":{"message":"busy"}}`, 429},
		{429, `busy`, 429},
		{500, `{"error":{"message":"inference failed"}}`, 502},
		{503, `{"error":{"message":"model is loading"}}`, 409},
	} {
		recorder := httptest.NewRecorder()
		response := &http.Response{StatusCode: test.upstream,
			Body: io.NopCloser(strings.NewReader(test.body)), Header: http.Header{"Retry-After": {"2"}}}
		writeRuntimeChatError(recorder, response)
		if recorder.Code != test.want {
			t.Fatalf("Runtime HTTP %d，代理回傳 %d，預期 %d", test.upstream, recorder.Code, test.want)
		}
		if test.want == 429 && recorder.Header().Get("Retry-After") != "2" {
			t.Fatal("名額已滿時遺失 Retry-After")
		}
		if test.want == 401 {
			var result struct {
				Error struct {
					Source string `json:"source"`
				} `json:"error"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil || result.Error.Source != "runtime" {
				t.Fatal("Runtime 金鑰拒絕不能被當成管理 Session 失效")
			}
		}
	}
}
