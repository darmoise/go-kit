package httpx_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/darmoise/go-kit/httpx"
)

func TestClientIP(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		remote string
		xff    []string
		trust  bool
		want   string
	}{
		{"ipv4", "1.2.3.4:5678", nil, false, "1.2.3.4"},
		{"ipv6", "[::1]:80", nil, false, "::1"},
		{"mapped", "[::ffff:1.2.3.4]:80", nil, false, "1.2.3.4"},
		{"xff ignored", "1.2.3.4:5678", []string{"9.9.9.9"}, false, "1.2.3.4"},
		{"xff last", "1.2.3.4:5678", []string{"a, b, 9.9.9.9"}, true, "9.9.9.9"},
		{"xff mapped", "1.2.3.4:5678", []string{"::ffff:9.9.9.9"}, true, "9.9.9.9"},
		{"two headers", "1.2.3.4:5678", []string{"7.7.7.7", "8.8.8.8"}, true, "8.8.8.8"},
		{"garbage last", "1.2.3.4:5678", []string{"1.2.3.4, evil"}, true, "1.2.3.4"},
		{"garbage last other remote", "5.5.5.5:1", []string{"9.9.9.9, evil"}, true, "5.5.5.5"},
		{"empty xff", "1.2.3.4:5678", []string{""}, true, "1.2.3.4"},
		{"no xff", "1.2.3.4:5678", nil, true, "1.2.3.4"},
		{"no port", "1.2.3.4", nil, false, "1.2.3.4"},
		{"garbage remote", "garbage", nil, false, "garbage"},
		{"empty remote", "", nil, false, ""},
	}
	for _, tc := range cases {
		r := &http.Request{RemoteAddr: tc.remote, Header: http.Header{}}
		for _, v := range tc.xff {
			r.Header.Add("X-Forwarded-For", v)
		}
		if got := httpx.ClientIP(r, tc.trust); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestBearerToken(t *testing.T) {
	t.Parallel()
	cases := []struct {
		h    string
		want string
		ok   bool
	}{
		{"Bearer abc", "abc", true},
		{"bearer abc", "abc", true},
		{"BEARER abc", "abc", true},
		{"", "", false},
		{"Basic abc", "", false},
		{"Bearer", "", false},
		{"Bearer ", "", false},
		{"Bearer  abc", "", false},
		{"Bearer a b", "", false},
		{"Bearer a\u00a0b", "", false},
	}
	for _, tc := range cases {
		r := httptest.NewRequest("GET", "/", nil)
		if tc.h != "" {
			r.Header.Set("Authorization", tc.h)
		}
		got, ok := httpx.BearerToken(r)
		if got != tc.want || ok != tc.ok {
			t.Errorf("%q: got (%q,%v), want (%q,%v)", tc.h, got, ok, tc.want, tc.ok)
		}
	}
}
