package main

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestShutdownTimeoutStillCleansRuntimeAndDownloads(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-release })}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go server.Serve(listener)
	defer server.Close()
	defer close(release)
	go func() {
		response, err := http.Get("http://" + listener.Addr().String())
		if err == nil {
			response.Body.Close()
		}
	}()
	<-entered
	calls := 0
	cleanup := func(ctx context.Context) error {
		if ctx.Err() != nil {
			t.Error("清理沿用已逾時的 context")
		}
		calls++
		return nil
	}
	if err := shutdownServices(server, 10*time.Millisecond, cleanup, cleanup); err == nil {
		t.Fatal("HTTP 逾時應回報錯誤")
	}
	if calls != 2 {
		t.Fatalf("未執行所有清理：%d", calls)
	}
}
