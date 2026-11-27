package jwtx_test

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/darmoise/go-kit/jwtx"
	"github.com/golang-jwt/jwt/v5"
)

func decodeSegment(t *testing.T, tok string, idx int) map[string]any {
	t.Helper()
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("token parts = %d", len(parts))
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[idx])
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestSignShortKey(t *testing.T) {
	t.Parallel()
	if _, err := jwtx.Sign(ed25519.PrivateKey{1, 2, 3}, jwtx.Claims{}); err == nil {
		t.Fatal("want error")
	}
}

func TestSignHeaderAndClaims(t *testing.T) {
	t.Parallel()
	pub, priv := newKey(t)
	now := time.Unix(1_700_000_000, 0)
	c := jwtx.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "https://issuer.example.com",
			Subject:   "user-1",
			Audience:  jwt.ClaimStrings{"a"},
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute)),
			IssuedAt:  jwt.NewNumericDate(now),
			ID:        "jti-1",
		},
		AuthorizedParty: "demo",
		SessionID:       "s1",
		Email:           "u@example.com",
	}
	tok, err := jwtx.Sign(priv, c)
	if err != nil {
		t.Fatal(err)
	}
	h := decodeSegment(t, tok, 0)
	if h["alg"] != "EdDSA" || h["typ"] != "JWT" || h["kid"] != jwtx.KeyID(pub) {
		t.Errorf("header = %v", h)
	}
	p := decodeSegment(t, tok, 1)
	if p["azp"] != "demo" || p["sid"] != "s1" || p["email"] != "u@example.com" || p["sub"] != "user-1" || p["jti"] != "jti-1" {
		t.Errorf("payload = %v", p)
	}
	// A single aud is serialized as an array of one element (9.1).
	aud, ok := p["aud"].([]any)
	if !ok || len(aud) != 1 || aud[0] != "a" {
		t.Errorf("aud = %#v", p["aud"])
	}

	c.SessionID, c.Email = "", ""
	c.Audience = jwt.ClaimStrings{"a", "b"}
	tok, err = jwtx.Sign(priv, c)
	if err != nil {
		t.Fatal(err)
	}
	p = decodeSegment(t, tok, 1)
	if _, ok := p["sid"]; ok {
		t.Error("sid must be omitted")
	}
	if _, ok := p["email"]; ok {
		t.Error("email must be omitted")
	}
	if aud, ok := p["aud"].([]any); !ok || len(aud) != 2 {
		t.Errorf("aud = %#v", p["aud"])
	}
}
