package sse_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/darmoise/go-kit/httpx"
	"github.com/darmoise/go-kit/sse"
)

func TestNewWriter(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	if _, err := sse.NewWriter(rec); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || !rec.Flushed {
		t.Fatalf("code=%d flushed=%v", rec.Code, rec.Flushed)
	}
	for k, v := range map[string]string{
		"Content-Type": "text/event-stream", "Cache-Control": "no-cache", "X-Accel-Buffering": "no",
	} {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("%s=%q", k, got)
		}
	}
}

func TestNewWriterNoFlush(t *testing.T) {
	t.Parallel()
	_, err := sse.NewWriter(struct{ http.ResponseWriter }{httptest.NewRecorder()})
	if !errors.Is(err, http.ErrNotSupported) {
		t.Fatalf("err=%v", err)
	}
}

func TestWriterBytes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, id, ev string
		data         []byte
		want         string
	}{
		{"full", "1", "delta", []byte("hi"), "id: 1\nevent: delta\ndata: hi\n\n"},
		{"bare", "", "", []byte("hi"), "data: hi\n\n"},
		{"nil data", "5", "usage", nil, "id: 5\nevent: usage\ndata:\n\n"},
		{"empty data", "5", "usage", []byte{}, "id: 5\nevent: usage\ndata:\n\n"},
		{"lf", "", "", []byte("a\nb"), "data: a\ndata: b\n\n"},
		{"crlf", "", "", []byte("a\r\nb"), "data: a\ndata: b\n\n"},
		{"cr", "", "", []byte("a\rb"), "data: a\ndata: b\n\n"},
		{"blank middle", "", "", []byte("a\n\nb"), "data: a\ndata:\ndata: b\n\n"},
		{"trailing lf", "", "", []byte("a\n"), "data: a\ndata:\n\n"},
		{"only crlf", "", "", []byte("\r\n"), "data:\ndata:\n\n"},
		{"mixed", "", "", []byte("a\r\nb\rc\nd"), "data: a\ndata: b\ndata: c\ndata: d\n\n"},
		{"leading space", "", "", []byte(" x"), "data:  x\n\n"},
		{"colons", "", "", []byte("data: a: b"), "data: data: a: b\n\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()
			w, _ := sse.NewWriter(rec)
			if err := w.Event(c.id, c.ev, c.data); err != nil {
				t.Fatal(err)
			}
			out := rec.Body.String()
			if out != c.want || strings.Contains(out, "\r") {
				t.Fatalf("got %q want %q", out, c.want)
			}
		})
	}
	rec := httptest.NewRecorder()
	w, _ := sse.NewWriter(rec)
	_ = w.Comment("ping")
	_ = w.Comment("")
	if got := rec.Body.String(); got != ": ping\n\n:\n\n" {
		t.Fatalf("comments %q", got)
	}
}

func TestWriterInvalid(t *testing.T) {
	t.Parallel()
	calls := map[string]func(w *sse.Writer) error{
		"id lf":      func(w *sse.Writer) error { return w.Event("a\nb", "", nil) },
		"id cr":      func(w *sse.Writer) error { return w.Event("a\rb", "", nil) },
		"id nul":     func(w *sse.Writer) error { return w.Event("a\x00b", "", nil) },
		"name lf":    func(w *sse.Writer) error { return w.Event("", "a\nb", nil) },
		"name cr":    func(w *sse.Writer) error { return w.Event("", "a\rb", nil) },
		"comment lf": func(w *sse.Writer) error { return w.Comment("a\nb") },
		"comment cr": func(w *sse.Writer) error { return w.Comment("a\rb") },
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()
			w, _ := sse.NewWriter(rec)
			err := call(w)
			if !errors.Is(err, sse.ErrInvalidField) || rec.Body.Len() != 0 {
				t.Fatalf("err=%v body=%q", err, rec.Body.String())
			}
			if err := w.Event("1", "", []byte("ok")); err != nil || rec.Body.String() != "id: 1\ndata: ok\n\n" {
				t.Fatalf("after: %v %q", err, rec.Body.String())
			}
		})
	}
}

// logRW logs operations; errors are injected by call number.
type logRW struct {
	hdr        http.Header
	ops        []string
	writes     int
	failWrite  int // number of the Write on which to return errBoom (0 = never)
	failFlush  bool
	flushCalls int
}

func (l *logRW) Header() http.Header { return l.hdr }
func (l *logRW) WriteHeader(int)     {}
func (l *logRW) Write(p []byte) (int, error) {
	l.writes++
	l.ops = append(l.ops, "write")
	if l.writes == l.failWrite {
		return 0, errBoom
	}
	return len(p), nil
}
func (l *logRW) FlushError() error {
	l.flushCalls++
	l.ops = append(l.ops, "flush")
	if l.failFlush && l.flushCalls > 1 {
		return errBoom
	}
	return nil
}

func TestWriterFlushPerCall(t *testing.T) {
	t.Parallel()
	l := &logRW{hdr: http.Header{}}
	w, err := sse.NewWriter(l)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.ops) != 1 || l.ops[0] != "flush" {
		t.Fatalf("after NewWriter: %v", l.ops)
	}
	l.ops = nil
	_ = w.Event("1", "x", []byte("a\nb\nc"))
	_ = w.Comment("ping")
	want := []string{"write", "flush", "write", "flush"}
	if strings.Join(l.ops, ",") != strings.Join(want, ",") {
		t.Fatalf("ops=%v", l.ops)
	}
}

func TestWriterStickyWriteError(t *testing.T) {
	t.Parallel()
	l := &logRW{hdr: http.Header{}, failWrite: 2}
	w, _ := sse.NewWriter(l)
	if err := w.Event("", "", []byte("a")); err != nil {
		t.Fatal(err)
	}
	if err := w.Event("", "", []byte("b")); !errors.Is(err, errBoom) {
		t.Fatalf("err=%v", err)
	}
	n := l.writes
	if err := w.Event("", "", []byte("c")); !errors.Is(err, errBoom) {
		t.Fatalf("err=%v", err)
	}
	if err := w.Comment("x"); !errors.Is(err, errBoom) {
		t.Fatalf("err=%v", err)
	}
	if l.writes != n {
		t.Fatalf("writes grew: %d -> %d", n, l.writes)
	}
}

func TestWriterStickyFlushError(t *testing.T) {
	t.Parallel()
	l := &logRW{hdr: http.Header{}, failFlush: true}
	w, _ := sse.NewWriter(l)
	if err := w.Event("", "", []byte("a")); !errors.Is(err, errBoom) {
		t.Fatalf("err=%v", err)
	}
	n := l.writes
	if err := w.Comment("x"); !errors.Is(err, errBoom) || l.writes != n {
		t.Fatalf("err=%v writes=%d/%d", err, l.writes, n)
	}
}

// Working behind httpx.AccessLog: event 1 reaches the client before the handler finishes.
func TestWriterThroughAccessLog(t *testing.T) {
	t.Parallel()
	got := make(chan struct{})
	h := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		w, err := sse.NewWriter(rw)
		if err != nil {
			t.Error(err)
			return
		}
		_ = w.Event("1", "first", []byte("one"))
		select {
		case <-got:
		case <-r.Context().Done():
			return
		}
		_ = w.Event("2", "second", []byte("two"))
		_ = w.Comment("ping")
	})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpx.Chain(h, httpx.AccessLog(logger)))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("Content-Type") != "text/event-stream" || resp.Header.Get("X-Accel-Buffering") != "no" ||
		resp.Header.Get("Cache-Control") != "no-cache" {
		t.Fatalf("headers %v", resp.Header)
	}
	rd := sse.NewReader(resp.Body, 0)
	e, err := rd.Next()
	if err != nil || e.ID != "1" || e.Name != "first" || string(e.Data) != "one" {
		t.Fatalf("e1=%+v err=%v", e, err)
	}
	close(got)
	e, err = rd.Next()
	if err != nil || e.ID != "2" || e.Name != "second" || string(e.Data) != "two" {
		t.Fatalf("e2=%+v err=%v", e, err)
	}
	if _, err := rd.Next(); err != io.EOF {
		t.Fatalf("want EOF, got %v", err)
	}
}
