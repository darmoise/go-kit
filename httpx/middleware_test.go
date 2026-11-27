package httpx_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/darmoise/go-kit/httpx"
)

func TestChainOrder(t *testing.T) {
	t.Parallel()
	var order []string
	mk := func(name string) httpx.Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name)
				next.ServeHTTP(w, r)
			})
		}
	}
	h := httpx.Chain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		order = append(order, "h")
	}), mk("a"), nil, mk("b"))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	if strings.Join(order, ",") != "a,b,h" {
		t.Fatalf("order = %v", order)
	}
}

func TestRequestID(t *testing.T) {
	t.Parallel()
	hex32 := regexp.MustCompile(`^[0-9a-f]{32}$`)
	long := strings.Repeat("a", 65)
	cases := []struct {
		in    string
		keeps bool
	}{
		{"abc-DEF_1.2", true},
		{strings.Repeat("a", 64), true},
		{long, false},
		{"has space", false},
		{"bad/char", false},
		{"", false},
	}
	for _, tc := range cases {
		var ctxID string
		h := httpx.Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctxID = httpx.RequestIDFrom(r.Context())
		}), httpx.RequestID())
		req := httptest.NewRequest("GET", "/", nil)
		if tc.in != "" {
			req.Header.Set("X-Request-Id", tc.in)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Header().Get("X-Request-Id") != ctxID {
			t.Errorf("%q: header %q != ctx %q", tc.in, rec.Header().Get("X-Request-Id"), ctxID)
		}
		if tc.keeps && ctxID != tc.in {
			t.Errorf("%q: replaced by %q", tc.in, ctxID)
		}
		if !tc.keeps && !hex32.MatchString(ctxID) {
			t.Errorf("%q: generated %q is not 32 hex", tc.in, ctxID)
		}
	}
}

func TestRecover(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	l := newLogger(&buf)
	h := httpx.Chain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}), httpx.RequestID(), httpx.Recover(l))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 500 {
		t.Fatalf("status = %d", rec.Code)
	}
	if decodeErr(t, rec)["code"] != "internal" {
		t.Fatalf("body = %s", rec.Body)
	}
	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatal(err)
	}
	if entry["msg"] != "panic recovered" || entry["panic"] != "boom" || entry["stack"] == "" || entry["request_id"] == "" {
		t.Fatalf("log = %v", entry)
	}
}

func TestRecoverAbortHandler(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	h := httpx.Recover(newLogger(&buf))(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}))
	rec := httptest.NewRecorder()
	func() {
		defer func() {
			if p := recover(); p != http.ErrAbortHandler {
				t.Errorf("recovered %v, want ErrAbortHandler", p)
			}
		}()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	}()
	if buf.Len() != 0 || rec.Body.Len() != 0 {
		t.Fatalf("log/body written: %q %q", buf.String(), rec.Body.String())
	}
}
