package httpx_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/darmoise/go-kit/httpx"
)

func newLogger(w io.Writer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, nil))
}

func lastEntry(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	var m map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &m); err != nil {
		t.Fatalf("bad log %q: %v", buf.String(), err)
	}
	return m
}

func TestAccessLogRoute(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	l := newLogger(&buf)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /items/{id}", func(w http.ResponseWriter, r *http.Request) {
		var wg sync.WaitGroup
		for _, k := range []string{"a", "b", "c"} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				httpx.AddLogAttrs(r.Context(), slog.String("k_"+k, "v"))
			}()
		}
		wg.Wait()
		httpx.AddLogAttrs(r.Context(), slog.String("user_id", "u1"))
		_, _ = w.Write([]byte("ok"))
	})
	h := httpx.Chain(mux, httpx.RequestID(), httpx.AccessLog(l))

	req := httptest.NewRequest("GET", "/items/secret-42?token=abc", nil)
	req.Header.Set("X-Request-Id", "rid-1")
	h.ServeHTTP(httptest.NewRecorder(), req)

	e := lastEntry(t, &buf)
	if e["msg"] != "request" || e["request_id"] != "rid-1" || e["method"] != "GET" ||
		e["route"] != "GET /items/{id}" || e["status"] != float64(200) || e["user_id"] != "u1" {
		t.Fatalf("entry = %v", e)
	}
	if _, ok := e["duration_ms"]; !ok {
		t.Fatal("no duration_ms")
	}
	for _, k := range []string{"k_a", "k_b", "k_c"} {
		if e[k] != "v" {
			t.Fatalf("missing %s: %v", k, e)
		}
	}
	if strings.Contains(buf.String(), "secret-42") || strings.Contains(buf.String(), "token") {
		t.Fatalf("path param or query leaked: %s", buf.String())
	}
	if n := strings.Count(strings.TrimSpace(buf.String()), "\n"); n != 0 {
		t.Fatalf("expected one record, got %d lines", n+1)
	}
}

func TestAccessLogUnmatched(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	h := httpx.Chain(http.NewServeMux(), httpx.RequestID(), httpx.AccessLog(newLogger(&buf)))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/nope", nil))
	e := lastEntry(t, &buf)
	if e["route"] != "unmatched" || e["status"] != float64(404) {
		t.Fatalf("entry = %v", e)
	}
}

func TestAccessLogPanicStatus(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	l := newLogger(&buf)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /boom", func(http.ResponseWriter, *http.Request) { panic("boom") })
	h := httpx.Chain(mux, httpx.RequestID(), httpx.Recover(l), httpx.AccessLog(l))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/boom", nil))
	if rec.Code != 500 {
		t.Fatalf("status = %d", rec.Code)
	}
	var found map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatal(err)
		}
		if m["msg"] == "request" {
			found = m
		}
	}
	if found == nil || found["status"] != float64(500) || found["route"] != "GET /boom" {
		t.Fatalf("access entry = %v", found)
	}
}

func TestAddLogAttrsWithoutAccessLog(t *testing.T) {
	t.Parallel()
	httpx.AddLogAttrs(httptest.NewRequest("GET", "/", nil).Context(), slog.String("a", "b"))
}

func TestAccessLogFlush(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	var flushErr error
	var flusherOK bool
	h := httpx.Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f, ok := w.(http.Flusher)
		flusherOK = ok
		if ok {
			f.Flush()
		}
		flushErr = http.NewResponseController(w).Flush()
	}), httpx.AccessLog(newLogger(&buf)))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if !flusherOK || flushErr != nil || !rec.Flushed {
		t.Fatalf("flusher=%v err=%v flushed=%v", flusherOK, flushErr, rec.Flushed)
	}
}
