package servicetoken_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/darmoise/go-kit/servicetoken"
)

func call(tokens []string, authHeader string) (code int, called bool, body string, hdr http.Header) {
	h := servicetoken.Middleware(tokens)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	req := httptest.NewRequest("GET", "/", nil)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, called, rec.Body.String(), rec.Header()
}

func TestMiddlewareAccepts(t *testing.T) {
	t.Parallel()
	toks := []string{"tok-one", "tok-two"}
	for _, tok := range toks {
		code, called, _, _ := call(toks, "Bearer "+tok)
		if code != 200 || !called {
			t.Errorf("%s: code=%d called=%v", tok, code, called)
		}
	}
}

func TestMiddlewareRejects(t *testing.T) {
	t.Parallel()
	toks := []string{"tok-one", "tok-two"}
	for _, hdr := range []string{"Bearer wrong-token", "", "Basic tok-one", "Bearer ", "Bearer tok-three"} {
		code, called, body, h := call(toks, hdr)
		if code != 401 || called {
			t.Errorf("%q: code=%d called=%v", hdr, code, called)
			continue
		}
		var b struct {
			Error struct{ Code string } `json:"error"`
		}
		if err := json.Unmarshal([]byte(body), &b); err != nil || b.Error.Code != "unauthorized" {
			t.Errorf("%q: body %s", hdr, body)
		}
		if h.Get("WWW-Authenticate") != "Bearer" {
			t.Errorf("%q: no WWW-Authenticate", hdr)
		}
		if tok := strings.TrimPrefix(hdr, "Bearer "); tok != "" && strings.Contains(body, tok) {
			t.Errorf("%q: presented token echoed", hdr)
		}
	}
}

func TestMiddlewareEmptyLists(t *testing.T) {
	t.Parallel()
	for _, toks := range [][]string{nil, {}, {""}, {"", ""}} {
		for _, hdr := range []string{"Bearer anything", "Bearer ", ""} {
			if code, called, _, _ := call(toks, hdr); code != 401 || called {
				t.Errorf("%q %q: code=%d called=%v", toks, hdr, code, called)
			}
		}
	}
}

func TestMatcher(t *testing.T) {
	t.Parallel()
	m := servicetoken.Matcher([]string{"a1", "b2", "a1"})
	if m("a1") != 0 || m("b2") != 1 || m("c3") != -1 || m("") != -1 {
		t.Fatal("indexes")
	}
	if servicetoken.Matcher([]string{"", "tok"})("tok") != 1 {
		t.Fatal("index with empty token before")
	}
	if servicetoken.Matcher([]string{"", "tok"})("") != -1 {
		t.Fatal("empty never matches")
	}
	if servicetoken.Matcher(nil)("x") != -1 {
		t.Fatal("nil list")
	}
}

func TestSourceSliceMutationIgnored(t *testing.T) {
	t.Parallel()
	toks := []string{"secret-1"}
	m := servicetoken.Matcher(toks)
	mw := servicetoken.Middleware(toks)
	toks[0] = "changed"
	if m("secret-1") != 0 || m("changed") != -1 {
		t.Fatal("Matcher depends on source slice")
	}
	called := false
	h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer secret-1")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if !called {
		t.Fatal("Middleware depends on source slice")
	}
}
