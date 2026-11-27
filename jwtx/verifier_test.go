package jwtx_test

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/darmoise/go-kit/jwtx"
	"github.com/golang-jwt/jwt/v5"
)

const (
	testIss = "https://issuer.example.com"
	testAud = "demo-api"
	testAzp = "demo"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *clock { return &clock{t: time.Unix(1_700_000_000, 0)} }

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func baseClaims(now time.Time) jwtx.Claims {
	return jwtx.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    testIss,
			Subject:   "user-1",
			Audience:  jwt.ClaimStrings{testAud},
			ExpiresAt: jwt.NewNumericDate(now.Add(15 * time.Minute)),
			IssuedAt:  jwt.NewNumericDate(now),
			ID:        "jti-1",
		},
		AuthorizedParty: testAzp,
		SessionID:       "s1",
		Email:           "u@example.com",
	}
}

func opts(c *clock) jwtx.VerifierOptions {
	return jwtx.VerifierOptions{Issuer: testIss, Audience: testAud, AuthorizedParty: testAzp, Now: c.Now}
}

func signWith(t *testing.T, priv ed25519.PrivateKey, c *clock, mut func(*jwtx.Claims)) string {
	t.Helper()
	cl := baseClaims(c.Now())
	if mut != nil {
		mut(&cl)
	}
	tok, err := jwtx.Sign(priv, cl)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func mustInvalid(t *testing.T, v *jwtx.Verifier, name, tok string) {
	t.Helper()
	_, err := v.Verify(context.Background(), tok)
	if !errors.Is(err, jwtx.ErrInvalidToken) {
		t.Errorf("%s: err = %v, want ErrInvalidToken", name, err)
		return
	}
	if tok != "" && strings.Contains(err.Error(), tok) {
		t.Errorf("%s: error contains token", name)
	}
}

func TestVerifyRoundTrip(t *testing.T) {
	t.Parallel()
	c := newClock()
	pub, priv := newKey(t)
	v := jwtx.NewStaticVerifier(opts(c), pub)
	got, err := v.Verify(context.Background(), signWith(t, priv, c, nil))
	if err != nil {
		t.Fatal(err)
	}
	want := baseClaims(c.Now())
	if got.Subject != want.Subject || got.Issuer != want.Issuer || got.AuthorizedParty != "demo" ||
		got.SessionID != "s1" || got.Email != "u@example.com" || got.ID != "jti-1" {
		t.Errorf("claims = %+v", got)
	}
	if len(got.Audience) != 1 || got.Audience[0] != testAud {
		t.Errorf("aud = %v", got.Audience)
	}
}

func TestVerifyAcceptsAudForms(t *testing.T) {
	t.Parallel()
	c := newClock()
	pub, priv := newKey(t)
	v := jwtx.NewStaticVerifier(opts(c), pub)

	// aud as a string: the token is built by hand, without Sign.
	now := c.Now()
	mc := jwt.MapClaims{
		"iss": testIss, "sub": "user-1", "aud": testAud, "azp": testAzp,
		"iat": now.Unix(), "exp": now.Add(time.Minute).Unix(),
	}
	tk := jwt.NewWithClaims(jwt.SigningMethodEdDSA, mc)
	tk.Header["kid"] = jwtx.KeyID(pub)
	s, err := tk.SignedString(priv)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.Verify(context.Background(), s); err != nil {
		t.Errorf("aud string: %v", err)
	}
	// An array of one and of several elements.
	for _, aud := range []jwt.ClaimStrings{{testAud}, {"other", testAud}} {
		tok := signWith(t, priv, c, func(cl *jwtx.Claims) { cl.Audience = aud })
		if _, err := v.Verify(context.Background(), tok); err != nil {
			t.Errorf("aud %v: %v", aud, err)
		}
	}
}

func TestVerifyRejects(t *testing.T) {
	t.Parallel()
	c := newClock()
	pub, priv := newKey(t)
	_, otherPriv := newKey(t)
	v := jwtx.NewStaticVerifier(opts(c), pub)
	vNoAzp := jwtx.NewStaticVerifier(jwtx.VerifierOptions{Issuer: testIss, Audience: testAud, Now: c.Now}, pub)
	now := c.Now()
	kid := jwtx.KeyID(pub)

	// alg=none.
	none := jwt.NewWithClaims(jwt.SigningMethodNone, baseClaims(now))
	none.Header["kid"] = kid
	noneTok, err := none.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatal(err)
	}
	mustInvalid(t, v, "alg none", noneTok)

	// HS256, secret = the public key bytes.
	hs := jwt.NewWithClaims(jwt.SigningMethodHS256, baseClaims(now))
	hs.Header["kid"] = kid
	hsTok, err := hs.SignedString([]byte(pub))
	if err != nil {
		t.Fatal(err)
	}
	mustInvalid(t, v, "alg HS256", hsTok)

	// Signed with a different key with the kid of a known one.
	forged := jwt.NewWithClaims(jwt.SigningMethodEdDSA, baseClaims(now))
	forged.Header["kid"] = kid
	forgedTok, err := forged.SignedString(otherPriv)
	if err != nil {
		t.Fatal(err)
	}
	mustInvalid(t, v, "wrong key", forgedTok)

	// Modified payload.
	good := signWith(t, priv, c, nil)
	parts := strings.Split(good, ".")
	pl := decodeSegment(t, good, 1)
	pl["sub"] = "admin"
	raw, _ := json.Marshal(pl)
	mustInvalid(t, v, "tampered", parts[0]+"."+base64.RawURLEncoding.EncodeToString(raw)+"."+parts[2])

	cases := map[string]func(*jwtx.Claims){
		"expired":    func(cl *jwtx.Claims) { cl.ExpiresAt = jwt.NewNumericDate(now.Add(-time.Hour)) },
		"nbf future": func(cl *jwtx.Claims) { cl.NotBefore = jwt.NewNumericDate(now.Add(time.Hour)) },
		"iat future": func(cl *jwtx.Claims) { cl.IssuedAt = jwt.NewNumericDate(now.Add(10 * time.Minute)) },
		"no exp":     func(cl *jwtx.Claims) { cl.ExpiresAt = nil },
		"bad iss":    func(cl *jwtx.Claims) { cl.Issuer = "https://evil.example.com" },
		"bad aud":    func(cl *jwtx.Claims) { cl.Audience = jwt.ClaimStrings{"x", "y"} },
		"bad azp":    func(cl *jwtx.Claims) { cl.AuthorizedParty = "other" },
		"empty sub":  func(cl *jwtx.Claims) { cl.Subject = "" },
	}
	for name, mut := range cases {
		mustInvalid(t, v, name, signWith(t, priv, c, mut))
	}

	// azp is not checked if not set.
	if _, err := vNoAzp.Verify(context.Background(), signWith(t, priv, c, func(cl *jwtx.Claims) { cl.AuthorizedParty = "anything" })); err != nil {
		t.Errorf("azp unchecked: %v", err)
	}

	// No kid, unknown kid.
	nk := jwt.NewWithClaims(jwt.SigningMethodEdDSA, baseClaims(now))
	nkTok, _ := nk.SignedString(priv)
	mustInvalid(t, v, "no kid", nkTok)
	_, otherPriv2 := newKey(t)
	mustInvalid(t, v, "unknown kid", signWith(t, otherPriv2, c, nil))

	for _, g := range []string{"", "a.b.c", "not-a-jwt"} {
		mustInvalid(t, v, "garbage "+g, g)
	}
}

func TestVerifyLeeway(t *testing.T) {
	t.Parallel()
	c := newClock()
	pub, priv := newKey(t)
	o := opts(c)
	o.Leeway = 30 * time.Second
	v := jwtx.NewStaticVerifier(o, pub)
	now := c.Now()
	check := func(name string, mut func(*jwtx.Claims), ok bool) {
		_, err := v.Verify(context.Background(), signWith(t, priv, c, mut))
		if ok && err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if !ok && !errors.Is(err, jwtx.ErrInvalidToken) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	check("exp -20s", func(cl *jwtx.Claims) { cl.ExpiresAt = jwt.NewNumericDate(now.Add(-20 * time.Second)) }, true)
	check("exp -40s", func(cl *jwtx.Claims) { cl.ExpiresAt = jwt.NewNumericDate(now.Add(-40 * time.Second)) }, false)
	check("nbf +20s", func(cl *jwtx.Claims) { cl.NotBefore = jwt.NewNumericDate(now.Add(20 * time.Second)) }, true)
	check("nbf +40s", func(cl *jwtx.Claims) { cl.NotBefore = jwt.NewNumericDate(now.Add(40 * time.Second)) }, false)
}

func TestConstructorsPanic(t *testing.T) {
	t.Parallel()
	mustPanic := func(name string, f func()) {
		defer func() {
			if recover() == nil {
				t.Errorf("%s: want panic", name)
			}
		}()
		f()
	}
	pub, _ := newKey(t)
	mustPanic("static no iss", func() { jwtx.NewStaticVerifier(jwtx.VerifierOptions{Audience: "a"}, pub) })
	mustPanic("static no aud", func() { jwtx.NewStaticVerifier(jwtx.VerifierOptions{Issuer: "i"}, pub) })
	mustPanic("no iss", func() { jwtx.NewVerifier(jwtx.VerifierOptions{Audience: "a", JWKSURL: "http://x"}) })
	mustPanic("no aud", func() { jwtx.NewVerifier(jwtx.VerifierOptions{Issuer: "i", JWKSURL: "http://x"}) })
	mustPanic("no url", func() { jwtx.NewVerifier(jwtx.VerifierOptions{Issuer: "i", Audience: "a"}) })
}

func TestStaticVerifierTwoKeys(t *testing.T) {
	t.Parallel()
	c := newClock()
	a, privA := newKey(t)
	b, privB := newKey(t)
	v := jwtx.NewStaticVerifier(opts(c), a, b)
	for _, p := range []ed25519.PrivateKey{privA, privB} {
		if _, err := v.Verify(context.Background(), signWith(t, p, c, nil)); err != nil {
			t.Error(err)
		}
	}
}

// jwksServer is a test JWKS server with controllable content and status.
type jwksServer struct {
	*httptest.Server
	hits   atomic.Int32
	mu     sync.Mutex
	keys   []ed25519.PublicKey
	status int
	body   string // if not empty, served as is
}

func newJWKSServer(t *testing.T, keys ...ed25519.PublicKey) *jwksServer {
	s := &jwksServer{keys: keys, status: 200}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.hits.Add(1)
		s.mu.Lock()
		keys, status, body := s.keys, s.status, s.body
		s.mu.Unlock()
		if status != 200 {
			w.WriteHeader(status)
			return
		}
		if body != "" {
			_, _ = w.Write([]byte(body))
			return
		}
		jwtx.JWKSHandler(keys...).ServeHTTP(w, r)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *jwksServer) set(status int, body string, keys ...ed25519.PublicKey) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status, s.body, s.keys = status, body, keys
}

func (s *jwksServer) count() int { return int(s.hits.Load()) }

func remoteVerifier(c *clock, s *jwksServer) *jwtx.Verifier {
	o := opts(c)
	o.JWKSURL = s.URL
	o.RefreshInterval = 10 * time.Minute
	o.MinRefetchGap = 30 * time.Second
	return jwtx.NewVerifier(o)
}

func TestRemoteCacheAndUnknownKid(t *testing.T) {
	t.Parallel()
	c := newClock()
	pubA, privA := newKey(t)
	_, privB := newKey(t)
	s := newJWKSServer(t, pubA)
	v := remoteVerifier(c, s)
	ctx := context.Background()

	tokA := signWith(t, privA, c, nil)
	if _, err := v.Verify(ctx, tokA); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Verify(ctx, tokA); err != nil {
		t.Fatal(err)
	}
	if s.count() != 1 {
		t.Fatalf("hits = %d, want 1", s.count())
	}
	c.advance(31 * time.Second) // the first load was just now: MinRefetchGap
	tokB := signWith(t, privB, c, nil)
	mustInvalid(t, v, "unknown kid", tokB)
	if s.count() != 2 {
		t.Fatalf("hits = %d, want 2", s.count())
	}
	mustInvalid(t, v, "unknown kid again", tokB)
	if s.count() != 2 {
		t.Fatalf("hits = %d, want 2 (MinRefetchGap)", s.count())
	}
	c.advance(31 * time.Second)
	tokB = signWith(t, privB, c, nil)
	mustInvalid(t, v, "unknown kid after gap", tokB)
	if s.count() != 3 {
		t.Fatalf("hits = %d, want 3", s.count())
	}
}

func TestRemoteRotation(t *testing.T) {
	t.Parallel()
	c := newClock()
	pubA, privA := newKey(t)
	pubB, privB := newKey(t)
	s := newJWKSServer(t, pubA)
	v := remoteVerifier(c, s)
	ctx := context.Background()
	if _, err := v.Verify(ctx, signWith(t, privA, c, nil)); err != nil {
		t.Fatal(err)
	}
	s.set(200, "", pubA, pubB)
	c.advance(31 * time.Second)
	if _, err := v.Verify(ctx, signWith(t, privB, c, nil)); err != nil {
		t.Fatalf("B after rotation: %v", err)
	}
	if _, err := v.Verify(ctx, signWith(t, privA, c, nil)); err != nil {
		t.Fatalf("A after rotation: %v", err)
	}
	// A disappears from the JWKS: after the next load it is rejected.
	s.set(200, "", pubB)
	c.advance(11 * time.Minute)
	if _, err := v.Verify(ctx, signWith(t, privB, c, nil)); err != nil {
		t.Fatal(err)
	}
	mustInvalid(t, v, "removed key", signWith(t, privA, c, nil))
}

func TestRemoteRefreshInterval(t *testing.T) {
	t.Parallel()
	c := newClock()
	pubA, privA := newKey(t)
	s := newJWKSServer(t, pubA)
	v := remoteVerifier(c, s)
	ctx := context.Background()
	if _, err := v.Verify(ctx, signWith(t, privA, c, nil)); err != nil {
		t.Fatal(err)
	}
	c.advance(10 * time.Minute)
	if _, err := v.Verify(ctx, signWith(t, privA, c, nil)); err != nil {
		t.Fatal(err)
	}
	if s.count() != 2 {
		t.Fatalf("hits = %d, want 2", s.count())
	}
}

func TestRemoteServerDownUsesCache(t *testing.T) {
	t.Parallel()
	c := newClock()
	pubA, privA := newKey(t)
	s := newJWKSServer(t, pubA)
	v := remoteVerifier(c, s)
	ctx := context.Background()
	if _, err := v.Verify(ctx, signWith(t, privA, c, nil)); err != nil {
		t.Fatal(err)
	}
	s.set(500, "")
	c.advance(10 * time.Minute)
	if _, err := v.Verify(ctx, signWith(t, privA, c, nil)); err != nil {
		t.Fatalf("stale cache: %v", err)
	}
	if s.count() != 2 {
		t.Fatalf("hits = %d, want 2", s.count())
	}
	if _, err := v.Verify(ctx, signWith(t, privA, c, nil)); err != nil {
		t.Fatal(err)
	}
	if s.count() != 2 {
		t.Fatalf("hits = %d, want 2 (MinRefetchGap after failure)", s.count())
	}
}

func TestRemoteKeysUnavailable(t *testing.T) {
	t.Parallel()
	pubA, privA := newKey(t)
	bad := map[string]struct {
		status int
		body   string
	}{
		"status 500":   {500, ""},
		"invalid json": {200, "{not json"},
		"no valid key": {200, `{"keys":[{"kty":"RSA","kid":"x"},{"kty":"OKP","crv":"Ed25519","kid":"y","x":"AAAA"}]}`},
	}
	for name, b := range bad {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			c := newClock()
			s := newJWKSServer(t, pubA)
			s.set(b.status, b.body)
			v := remoteVerifier(c, s)
			_, err := v.Verify(context.Background(), signWith(t, privA, c, nil))
			if !errors.Is(err, jwtx.ErrKeysUnavailable) || errors.Is(err, jwtx.ErrInvalidToken) {
				t.Fatalf("err = %v", err)
			}
		})
	}
	t.Run("closed server", func(t *testing.T) {
		t.Parallel()
		c := newClock()
		s := newJWKSServer(t, pubA)
		v := remoteVerifier(c, s)
		s.Close()
		_, err := v.Verify(context.Background(), signWith(t, privA, c, nil))
		if !errors.Is(err, jwtx.ErrKeysUnavailable) || errors.Is(err, jwtx.ErrInvalidToken) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestRemoteBadResponseKeepsCache(t *testing.T) {
	t.Parallel()
	c := newClock()
	pubA, privA := newKey(t)
	s := newJWKSServer(t, pubA)
	v := remoteVerifier(c, s)
	ctx := context.Background()
	if _, err := v.Verify(ctx, signWith(t, privA, c, nil)); err != nil {
		t.Fatal(err)
	}
	s.set(200, `{"keys":[]}`)
	c.advance(11 * time.Minute)
	if _, err := v.Verify(ctx, signWith(t, privA, c, nil)); err != nil {
		t.Fatalf("cache must survive empty jwks: %v", err)
	}
}

func TestRemoteConcurrency(t *testing.T) {
	t.Parallel()
	c := newClock()
	pubA, privA := newKey(t)
	_, privB := newKey(t)
	s := newJWKSServer(t, pubA)
	v := remoteVerifier(c, s)
	run := func(tok string, check func(error) bool) {
		var wg sync.WaitGroup
		start := make(chan struct{})
		for range 20 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				if err := func() error { _, err := v.Verify(context.Background(), tok); return err }(); !check(err) {
					t.Errorf("unexpected err: %v", err)
				}
			}()
		}
		close(start)
		wg.Wait()
	}
	run(signWith(t, privA, c, nil), func(err error) bool { return err == nil })
	if s.count() != 1 {
		t.Fatalf("hits = %d, want 1", s.count())
	}
	c.advance(31 * time.Second)
	run(signWith(t, privB, c, nil), func(err error) bool { return errors.Is(err, jwtx.ErrInvalidToken) })
	if s.count() != 2 {
		t.Fatalf("hits = %d, want 2", s.count())
	}
}

func TestRemoteCancelledContext(t *testing.T) {
	t.Parallel()
	c := newClock()
	pubA, privA := newKey(t)
	s := newJWKSServer(t, pubA)
	v := remoteVerifier(c, s)
	tok := signWith(t, privA, c, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := v.Verify(ctx, tok); !errors.Is(err, jwtx.ErrKeysUnavailable) {
		t.Fatalf("err = %v", err)
	}
	before := s.count()
	if _, err := v.Verify(context.Background(), tok); err != nil {
		t.Fatalf("live ctx right after cancel: %v", err)
	}
	if s.count() != before+1 {
		t.Fatalf("hits = %d, want %d", s.count(), before+1)
	}
}

func TestRemoteEndToEndWithHandler(t *testing.T) {
	t.Parallel()
	c := newClock()
	pubA, privA := newKey(t)
	srv := httptest.NewServer(jwtx.JWKSHandler(pubA))
	defer srv.Close()
	o := opts(c)
	o.JWKSURL = srv.URL
	v := jwtx.NewVerifier(o)
	if _, err := v.Verify(context.Background(), signWith(t, privA, c, nil)); err != nil {
		t.Fatal(err)
	}
}
