package httpx_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/darmoise/go-kit/httpx"
)

type errBody struct {
	Error map[string]any `json:"error"`
}

func decodeErr(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var b errBody
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatalf("body is not JSON: %v: %s", err, rec.Body.String())
	}
	return b.Error
}

func TestWriteErrorBasic(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	httpx.WriteError(rec, nil, httpx.NewError(400, "bad", "Bad thing"))
	if rec.Code != 400 {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("content-type = %q", ct)
	}
	e := decodeErr(t, rec)
	if e["code"] != "bad" || e["message"] != "Bad thing" {
		t.Fatalf("error = %v", e)
	}
	if _, ok := e["request_id"]; ok {
		t.Fatal("request_id must be omitted")
	}
	if _, ok := e["retry_after"]; ok {
		t.Fatal("retry_after must be omitted")
	}
	if rec.Header().Get("Retry-After") != "" {
		t.Fatal("Retry-After must be absent")
	}
}

func TestWriteErrorRequestID(t *testing.T) {
	t.Parallel()
	var got string
	h := httpx.Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = httpx.RequestIDFrom(r.Context())
		httpx.WriteError(w, r, httpx.NewError(404, "nf", "Not found"))
	}), httpx.RequestID())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if e := decodeErr(t, rec); e["request_id"] != got || got == "" {
		t.Fatalf("request_id = %v, ctx = %q", e["request_id"], got)
	}

	// Without RequestID in the chain the field is omitted.
	rec = httptest.NewRecorder()
	httpx.WriteError(rec, httptest.NewRequest("GET", "/", nil), httpx.NewError(404, "nf", "Not found"))
	if _, ok := decodeErr(t, rec)["request_id"]; ok {
		t.Fatal("request_id must be omitted without middleware")
	}
}

func TestWriteErrorExtra(t *testing.T) {
	t.Parallel()
	e := httpx.NewError(429, "limited", "Slow down")
	e.Extra = map[string]any{
		"attempts_left": 3,
		"code":          "hacked",
		"message":       "hacked",
		"request_id":    "hacked",
		"retry_after":   999,
	}
	rec := httptest.NewRecorder()
	httpx.WriteError(rec, nil, e)
	got := decodeErr(t, rec)
	if got["attempts_left"] != float64(3) {
		t.Fatalf("attempts_left = %v", got["attempts_left"])
	}
	if got["code"] != "limited" || got["message"] != "Slow down" {
		t.Fatalf("reserved keys overwritten: %v", got)
	}
	if _, ok := got["request_id"]; ok {
		t.Fatalf("request_id from Extra leaked: %v", got)
	}
	if _, ok := got["retry_after"]; ok {
		t.Fatalf("retry_after from Extra leaked: %v", got)
	}
}

func TestWriteErrorRetryAfter(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{{1500 * time.Millisecond, "2"}, {42 * time.Second, "42"}, {time.Millisecond, "1"}} {
		e := httpx.NewError(429, "limited", "Slow down")
		e.RetryAfter = tc.d
		rec := httptest.NewRecorder()
		httpx.WriteError(rec, nil, e)
		if h := rec.Header().Get("Retry-After"); h != tc.want {
			t.Errorf("%v: header = %q, want %q", tc.d, h, tc.want)
		}
		if v := decodeErr(t, rec)["retry_after"]; fmt.Sprint(v) != tc.want {
			t.Errorf("%v: body retry_after = %v, want %s", tc.d, v, tc.want)
		}
	}
}

func TestWriteErrorBadStatus(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	httpx.WriteError(rec, nil, httpx.NewError(200, "x", "y"))
	if rec.Code != 500 {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestWriteErrorWrapped(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	httpx.WriteError(rec, nil, fmt.Errorf("x: %w", httpx.NewError(409, "conflict", "Conflict")))
	if rec.Code != 409 || decodeErr(t, rec)["code"] != "conflict" {
		t.Fatalf("wrapped not recognized: %d %s", rec.Code, rec.Body)
	}
}

// Not parallel: replaces slog.Default.
func TestWriteErrorUnknownHidden(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	rec := httptest.NewRecorder()
	httpx.WriteError(rec, nil, errors.New("db password=hunter2 failed"))
	if rec.Code != 500 || decodeErr(t, rec)["code"] != "internal" {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "hunter2") {
		t.Fatal("internal error text leaked to client")
	}
	if !strings.Contains(buf.String(), "hunter2") {
		t.Fatalf("error not logged: %s", buf.String())
	}
}
