package webhook_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/darmoise/go-kit/webhook"
)

var (
	t0   = time.Unix(1700000000, 0)
	body = []byte(`{"event":"demo"}`)
)

func TestSignFormat(t *testing.T) {
	t.Parallel()
	s := webhook.Sign("sec", t0, body)
	if !regexp.MustCompile(`^t=1700000000,v1=[0-9a-f]{64}$`).MatchString(s) {
		t.Fatalf("format: %s", s)
	}
}

func TestSignVector(t *testing.T) {
	t.Parallel()
	m := hmac.New(sha256.New, []byte("whsec_demo"))
	m.Write([]byte("1700000000." + string(body)))
	want := "t=1700000000,v1=" + hex.EncodeToString(m.Sum(nil))
	if got := webhook.Sign("whsec_demo", t0, body); got != want {
		t.Fatalf("got %s want %s", got, want)
	}
	// A vector computed independently (openssl dgst -sha256 -hmac).
	const fixed = "t=1700000000,v1=c157ba8953c2bf910fb9ff7ae9c28d43272054c5998d4301ca48eaee03d52e6f"
	if got := webhook.Sign("whsec_demo", t0, body); got != fixed {
		t.Fatalf("got %s want %s", got, fixed)
	}
}

func TestVerifyRoundTrip(t *testing.T) {
	t.Parallel()
	h := webhook.Sign("sec", t0, body)
	tol := time.Minute
	for _, now := range []time.Time{t0, t0.Add(tol), t0.Add(-tol), t0.Add(30 * time.Second)} {
		if err := webhook.Verify("sec", h, body, now, tol); err != nil {
			t.Errorf("now=%v: %v", now, err)
		}
	}
	if err := webhook.Verify("sec", h, body, t0.Add(tol+time.Second), tol); !errors.Is(err, webhook.ErrTimestampOutOfRange) {
		t.Errorf("late: %v", err)
	}
	if err := webhook.Verify("sec", h, body, t0.Add(-tol-time.Second), tol); !errors.Is(err, webhook.ErrTimestampOutOfRange) {
		t.Errorf("future: %v", err)
	}
}

func TestVerifyInvalid(t *testing.T) {
	t.Parallel()
	h := webhook.Sign("sec", t0, body)
	tamperedT := strings.Replace(h, "t=1700000000", "t=1700000001", 1)
	cases := map[string]struct {
		secret, header string
		body           []byte
	}{
		"other secret": {"other", h, body},
		"other body":   {"sec", h, []byte(`{"event":"x"}`)},
		"tampered t":   {"sec", tamperedT, body},
		"empty secret": {"", h, body},
		"empty":        {"sec", "", body},
		"garbage":      {"sec", "garbage", body},
		"bad t":        {"sec", "t=abc,v1=00", body},
		"no v1":        {"sec", "t=1", body},
		"no t":         {"sec", "v1=aa", body},
		"non-hex v1":   {"sec", "t=1,v1=zz", body},
		"plus t":       {"sec", "t=+1700000000,v1=00", body},
		"two t":        {"sec", h + ",t=1700000000", body},
	}
	for name, tc := range cases {
		if err := webhook.Verify(tc.secret, tc.header, tc.body, t0, time.Hour); !errors.Is(err, webhook.ErrInvalidSignature) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Wrong signature with an expired time: still ErrInvalidSignature.
	if err := webhook.Verify("other", h, body, t0.Add(24*time.Hour), time.Minute); !errors.Is(err, webhook.ErrInvalidSignature) {
		t.Errorf("wrong sig + old: %v", err)
	}
}

func TestVerifyMultipleV1(t *testing.T) {
	t.Parallel()
	good := webhook.Sign("sec", t0, body)
	wrong := webhook.Sign("old", t0, body)
	wrongSig := wrong[strings.Index(wrong, "v1="):]
	goodSig := good[strings.Index(good, "v1="):]

	for name, h := range map[string]string{
		"wrong then good":   "t=1700000000," + wrongSig + ", " + goodSig,
		"non-hex then good": "t=1700000000,v1=zz," + goodSig,
		"extra keys":        "foo=bar, t=1700000000, " + goodSig + ", v2=abc",
	} {
		if err := webhook.Verify("sec", h, body, t0, time.Minute); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestSend(t *testing.T) {
	t.Parallel()
	for _, status := range []int{200, 204, 400, 429, 500} {
		var gotBody []byte
		var hdr http.Header
		var method string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotBody, _ = io.ReadAll(r.Body)
			hdr, method = r.Header.Clone(), r.Method
			w.WriteHeader(status)
		}))
		got, err := webhook.Send(context.Background(), srv.Client(), srv.URL, "sec", "evt-1", body)
		srv.Close()
		if err != nil || got != status {
			t.Fatalf("status %d: got %d, %v", status, got, err)
		}
		if method != "POST" || string(gotBody) != string(body) {
			t.Fatalf("method=%s body=%s", method, gotBody)
		}
		if hdr.Get("Content-Type") != "application/json" || hdr.Get("X-Event-Id") != "evt-1" {
			t.Fatalf("headers: %v", hdr)
		}
		if err := webhook.Verify("sec", hdr.Get("X-Signature"), gotBody, time.Now(), time.Minute); err != nil {
			t.Fatalf("signature: %v", err)
		}
	}
}

func TestSendNilClient(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	if got, err := webhook.Send(context.Background(), nil, srv.URL, "sec", "e", body); err != nil || got != 200 {
		t.Fatalf("%d %v", got, err)
	}
}

func TestSendUnreachableHidesURL(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL + "/hook?token=secret-in-url"
	srv.Close()
	status, err := webhook.Send(context.Background(), nil, url, "sec", "e", body)
	if err == nil || status != 0 {
		t.Fatalf("status=%d err=%v", status, err)
	}
	if strings.Contains(err.Error(), "secret-in-url") {
		t.Fatalf("url leaked: %v", err)
	}
}

func TestSendInvalidURLHidden(t *testing.T) {
	t.Parallel()
	_, err := webhook.Send(context.Background(), nil, "http://bad host/?token=secret-in-url", "sec", "e", body)
	if err == nil || strings.Contains(err.Error(), "secret-in-url") {
		t.Fatalf("err = %v", err)
	}
}

func TestSendCancelledContext(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if status, err := webhook.Send(ctx, nil, srv.URL, "sec", "e", body); err == nil || status != 0 {
		t.Fatalf("status=%d err=%v", status, err)
	}
}

func TestSendEmptySecretOrEventID(t *testing.T) {
	t.Parallel()
	hit := make(chan struct{}, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit <- struct{}{} }))
	defer srv.Close()
	if _, err := webhook.Send(context.Background(), nil, srv.URL, "", "e", body); err == nil {
		t.Error("empty secret accepted")
	}
	if _, err := webhook.Send(context.Background(), nil, srv.URL, "sec", "", body); err == nil {
		t.Error("empty event id accepted")
	}
	if len(hit) != 0 {
		t.Fatal("request reached server")
	}
}
