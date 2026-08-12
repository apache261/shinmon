package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/apache261/Shinmon/internal/health"
)

func TestServeGracefullyCompletesInflightRequest(t *testing.T) {
	listener := listenLocal(t)
	readiness := &health.Readiness{}
	started := make(chan struct{})
	release := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		w.WriteHeader(http.StatusNoContent)
	})
	server := &http.Server{Handler: handler}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- Serve(ctx, server, listener, readiness, time.Second, discardLogger())
	}()
	waitFor(t, readiness.IsReady)

	requestResult := make(chan error, 1)
	go func() {
		response, err := http.Get("http://" + listener.Addr().String())
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode != http.StatusNoContent {
				err = errors.New("unexpected response status")
			}
		}
		requestResult <- err
	}()
	<-started
	cancel()
	waitFor(t, func() bool { return !readiness.IsReady() })
	close(release)

	if err := <-requestResult; err != nil {
		t.Fatalf("request: %v", err)
	}
	if err := <-result; err != nil {
		t.Fatalf("Serve: %v", err)
	}
}

func TestServeShutdownTimeoutIsBounded(t *testing.T) {
	listener := listenLocal(t)
	readiness := &health.Readiness{}
	started := make(chan struct{})
	release := make(chan struct{})
	handler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		close(started)
		<-release
	})
	server := &http.Server{Handler: handler}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- Serve(ctx, server, listener, readiness, 40*time.Millisecond, discardLogger())
	}()
	waitFor(t, readiness.IsReady)
	go func() {
		response, err := http.Get("http://" + listener.Addr().String())
		if err == nil {
			_ = response.Body.Close()
		}
	}()
	<-started
	begin := time.Now()
	cancel()
	err := <-result
	elapsed := time.Since(begin)
	close(release)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Serve error = %v", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("shutdown exceeded bound: %v", elapsed)
	}
	if readiness.IsReady() {
		t.Fatal("server remained ready during shutdown")
	}
}

func listenLocal(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	return listener
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("condition was not met")
		}
		time.Sleep(time.Millisecond)
	}
}
