package httpx_test

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/darmoise/go-kit/httpx"
)

type payload struct {
	A int `json:"a"`
}

func decode(ct, body string, max int64) (payload, error) {
	req := httptest.NewRequest("POST", "/", strings.NewReader(body))
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	var p payload
	err := httpx.DecodeJSON(httptest.NewRecorder(), req, &p, max)
	return p, err
}

func TestWriteJSON(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	httpx.WriteJSON(rec, 201, map[string]int{"a": 1})
	if rec.Code != 201 || rec.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatalf("%d %v", rec.Code, rec.Header())
	}
	if rec.Body.String() != `{"a":1}` {
		t.Fatalf("body = %s", rec.Body)
	}
}

func TestWriteJSONMarshalFailure(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	httpx.WriteJSON(rec, 200, make(chan int))
	if rec.Code != 500 || !strings.Contains(rec.Body.String(), `"internal"`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestDecodeJSONOK(t *testing.T) {
	t.Parallel()
	for _, ct := range []string{"application/json", "application/json; charset=utf-8", "Application/JSON"} {
		p, err := decode(ct, `{"a":5,"unknown":true}`, 0)
		if err != nil || p.A != 5 {
			t.Errorf("%q: %v %v", ct, p, err)
		}
	}
}

func TestDecodeJSONErrors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, ct, body string
		max            int64
		status         int
		code           string
	}{
		{"text/plain", "text/plain", `{"a":1}`, 0, 400, "invalid_request"},
		{"no content type", "", `{"a":1}`, 0, 400, "invalid_request"},
		{"too large", "application/json", `{"a":1,"pad":"` + strings.Repeat("x", 100) + `"}`, 20, 413, "payload_too_large"},
		{"empty", "application/json", ``, 0, 400, "invalid_request"},
		{"garbage", "application/json", `SECRETGARBAGE`, 0, 400, "invalid_request"},
		{"two objects", "application/json", `{"a":1} {"a":2}`, 0, 400, "invalid_request"},
		{"trailing garbage", "application/json", `{"a":1}garbage`, 0, 400, "invalid_request"},
		{"wrong type", "application/json", `{"a":"SECRETVALUE"}`, 0, 400, "invalid_request"},
	}
	for _, tc := range cases {
		_, err := decode(tc.ct, tc.body, tc.max)
		e, ok := err.(*httpx.Error)
		if !ok {
			t.Errorf("%s: err = %v", tc.name, err)
			continue
		}
		if e.Status != tc.status || e.Code != tc.code {
			t.Errorf("%s: got %d %s", tc.name, e.Status, e.Code)
		}
		if strings.Contains(e.Message, "SECRET") {
			t.Errorf("%s: message leaks body: %s", tc.name, e.Message)
		}
	}
}
