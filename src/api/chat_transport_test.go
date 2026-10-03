package api

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRuntimeChatPoolReusesConnectionsWithoutSharingCredentials(t *testing.T) {
	var connections atomic.Int64
	waiting := make(chan struct{})
	upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/unwanted", http.StatusFound)
			return
		}
		if r.URL.Path == "/wait" {
			close(waiting)
			<-r.Context().Done()
			return
		}
		w.Header().Set("Set-Cookie", "secret=private")
		if r.Header.Get("Cookie") != "" {
			t.Error("不應保存上游 Cookie")
		}
		_, _ = io.WriteString(w, r.Header.Get("X-OpenLoader-Key"))
	}))
	upstream.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	upstream.Start()
	defer upstream.Close()
	server := &Server{}
	client := server.runtimeChatHTTPClient()
	defer client.CloseIdleConnections()
	for _, key := range []string{"first-key", "second-key", ""} {
		req, _ := http.NewRequest("POST", upstream.URL, strings.NewReader("request"))
		req.Header.Set("X-OpenLoader-Key", key)
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || string(data) != key {
			t.Fatalf("金鑰交錯：%q %v", data, err)
		}
	}
	if connections.Load() != 1 {
		t.Fatalf("連線未重用：%d", connections.Load())
	}
	response, err := client.Get(upstream.URL + "/redirect")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusFound {
		t.Fatal("不得跟隨重新導向")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", upstream.URL+"/wait", nil)
	result := make(chan error, 1)
	go func() {
		response, err := client.Do(req)
		if response != nil {
			response.Body.Close()
		}
		result <- err
	}()
	select {
	case <-waiting:
		cancel()
	case <-ctx.Done():
		t.Fatal("上游未收到取消測試請求")
	}
	if err := <-result; err == nil {
		t.Fatal("取消必須中止請求")
	}
	response, err = client.Get(upstream.URL)
	if err != nil {
		t.Fatal("取消影響其他請求", err)
	}
	response.Body.Close()
	if client.Transport.(*http.Transport).Proxy != nil {
		t.Fatal("本機 Runtime 不應走環境代理")
	}
}

// LegacyPerRequest 保留原本每次建立／關閉連線池的呼叫方式，與共用池使用相同請求。
func BenchmarkRuntimeChatTransport(b *testing.B) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = io.WriteString(w, `{"content":"ready"}`)
	}))
	defer upstream.Close()
	for _, reuse := range []bool{false, true} {
		name := "LegacyPerRequest"
		if reuse {
			name = "SharedPool"
		}
		b.Run(name, func(b *testing.B) {
			server := &Server{}
			defer server.runtimeChatHTTPClient().CloseIdleConnections()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				var client *http.Client
				if reuse {
					client = server.runtimeChatHTTPClient()
				} else {
					client = newRuntimeChatHTTPClient()
				}
				response, err := client.Post(upstream.URL, "application/json", strings.NewReader(`{"messages":[]}`))
				if err != nil {
					b.Fatal(err)
				}
				_, err = io.Copy(io.Discard, response.Body)
				response.Body.Close()
				if !reuse {
					client.CloseIdleConnections()
				}
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
