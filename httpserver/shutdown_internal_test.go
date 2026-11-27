package httpserver

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func startServe(t *testing.T, h http.Handler, log *slog.Logger) (addr string, cancel context.CancelFunc, done chan error) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done = make(chan error, 1)
	go func() { done <- serve(ctx, log, ln, h) }()
	return ln.Addr().String(), cancel, done
}

func waitDone(t *testing.T, done chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not return")
		return nil
	}
}

// Not parallel: other tests in the package replace shutdownTimeout.
func TestServeBasicAndLogs(t *testing.T) {
	buf := &syncBuf{}
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "pong") })
	addr, cancel, done := startServe(t, h, slog.New(slog.NewJSONHandler(buf, nil)))

	resp, err := http.Get("http://" + addr + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "pong" {
		t.Fatalf("body = %q", body)
	}

	cancel()
	if err := waitDone(t, done); err != nil {
		t.Fatalf("serve = %v", err)
	}
	if c, err := net.Dial("tcp", addr); err == nil {
		c.Close()
		t.Fatal("port still open after shutdown")
	}
	logs := buf.String()
	for _, m := range []string{`"msg":"listening"`, `"msg":"shutting down"`, `"msg":"shutdown complete"`} {
		if !strings.Contains(logs, m) {
			t.Errorf("log lacks %s: %s", m, logs)
		}
	}
	if !strings.Contains(logs, `"addr":"127.0.0.1:`) {
		t.Errorf("no addr in log: %s", logs)
	}
}

func TestServeInFlightRequestCompletes(t *testing.T) {
	sw := &signalWriter{marker: "shutting down", ch: make(chan struct{})}
	entered := make(chan struct{})
	release := make(chan struct{})
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		_, _ = io.WriteString(w, "full")
	})
	addr, cancel, done := startServe(t, h, slog.New(slog.NewJSONHandler(sw, nil)))

	type res struct {
		body string
		err  error
	}
	resCh := make(chan res, 1)
	go func() {
		resp, err := http.Get("http://" + addr + "/")
		if err != nil {
			resCh <- res{err: err}
			return
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		resCh <- res{string(b), err}
	}()
	<-entered
	cancel()
	<-sw.ch

	select {
	case err := <-done:
		t.Fatalf("serve returned early: %v", err)
	default:
	}
	close(release)
	r := <-resCh
	if r.err != nil || r.body != "full" {
		t.Fatalf("client got %q, %v", r.body, r.err)
	}
	if err := waitDone(t, done); err != nil {
		t.Fatalf("serve = %v", err)
	}
}

func TestServeCancelledContextOnEntry(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan error, 1)
	go func() { done <- serve(ctx, nil, ln, http.NewServeMux()) }()
	if err := waitDone(t, done); err != nil {
		t.Fatalf("serve = %v", err)
	}
}

// Not parallel: replaces the package variable shutdownTimeout.
func TestServeShutdownTimeoutForcesClose(t *testing.T) {
	old := shutdownTimeout
	shutdownTimeout = 50 * time.Millisecond
	t.Cleanup(func() { shutdownTimeout = old })

	entered := make(chan struct{})
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-block
	})
	addr, cancel, done := startServe(t, h, slog.New(slog.NewJSONHandler(io.Discard, nil)))

	go func() {
		resp, err := http.Get("http://" + addr + "/")
		if err == nil {
			resp.Body.Close()
		}
	}()
	<-entered
	cancel()
	err := waitDone(t, done)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("serve = %v, want DeadlineExceeded", err)
	}
}

// signalWriter signals on a channel at the first write containing the marker.
type signalWriter struct {
	marker string
	once   sync.Once
	ch     chan struct{}
}

func (s *signalWriter) Write(p []byte) (int, error) {
	if strings.Contains(string(p), s.marker) {
		s.once.Do(func() { close(s.ch) })
	}
	return len(p), nil
}
