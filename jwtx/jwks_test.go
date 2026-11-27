package jwtx_test

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"testing"

	"github.com/darmoise/go-kit/jwtx"
)

func newKey(t testing.TB) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func TestPublicJWKRFC8037Vector(t *testing.T) {
	t.Parallel()
	seed, err := base64.RawURLEncoding.DecodeString("nWGxne_9WmC6hEr0kuwsxERJxWl7MmkZcDusAxyuf2A")
	if err != nil {
		t.Fatal(err)
	}
	priv := ed25519.NewKeyFromSeed(seed)
	pub := priv.Public().(ed25519.PublicKey)
	j := jwtx.PublicJWK(pub)
	if j.X != "11qYAYKxCrfVS_7TyWQHOg7hcvPapiMlrwIaaPcHURo" {
		t.Errorf("x = %s", j.X)
	}
	if j.Kty != "OKP" || j.Crv != "Ed25519" || j.Use != "sig" || j.Alg != "EdDSA" || j.Kid != jwtx.KeyID(pub) {
		t.Errorf("jwk = %+v", j)
	}
}

func TestKeyID(t *testing.T) {
	t.Parallel()
	pubA, _ := newKey(t)
	pubB, _ := newKey(t)
	re := regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)
	if !re.MatchString(jwtx.KeyID(pubA)) {
		t.Errorf("kid = %q", jwtx.KeyID(pubA))
	}
	if jwtx.KeyID(pubA) != jwtx.KeyID(pubA) {
		t.Error("not deterministic")
	}
	if jwtx.KeyID(pubA) == jwtx.KeyID(pubB) {
		t.Error("kid must differ")
	}
}

func TestJWKFields(t *testing.T) {
	t.Parallel()
	pub, _ := newKey(t)
	data, err := json.Marshal(jwtx.PublicJWK(pub))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	want := []string{"alg", "crv", "kid", "kty", "use", "x"}
	if len(keys) != len(want) {
		t.Fatalf("keys = %v", keys)
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Fatalf("keys = %v", keys)
		}
	}
}

func TestNewJWKS(t *testing.T) {
	t.Parallel()
	a, _ := newKey(t)
	b, _ := newKey(t)
	set := jwtx.NewJWKS(a, b, a)
	if len(set.Keys) != 2 || set.Keys[0].Kid != jwtx.KeyID(a) || set.Keys[1].Kid != jwtx.KeyID(b) {
		t.Fatalf("set = %+v", set)
	}
	data, _ := json.Marshal(jwtx.NewJWKS())
	if string(data) != `{"keys":[]}` {
		t.Errorf("empty = %s", data)
	}
}

func TestJWKSHandler(t *testing.T) {
	t.Parallel()
	a, _ := newKey(t)
	b, _ := newKey(t)
	rec := httptest.NewRecorder()
	jwtx.JWKSHandler(a, b).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/jwks.json", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=300" {
		t.Errorf("Cache-Control = %q", got)
	}
	var set jwtx.JWKS
	if err := json.Unmarshal(rec.Body.Bytes(), &set); err != nil || len(set.Keys) != 2 {
		t.Fatalf("body = %s, %v", rec.Body.String(), err)
	}
	rec = httptest.NewRecorder()
	jwtx.JWKSHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Body.String() != `{"keys":[]}` {
		t.Errorf("empty body = %s", rec.Body.String())
	}
}
