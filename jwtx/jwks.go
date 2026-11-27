package jwtx

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
)

// JWK is an Ed25519 public key in JWK format (RFC 8037).
type JWK struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
}

// JWKS is a set of keys.
type JWKS struct {
	Keys []JWK `json:"keys"`
}

// PublicJWK returns the JWK for a public key.
func PublicJWK(pub ed25519.PublicKey) JWK {
	return JWK{
		Kty: "OKP",
		Crv: "Ed25519",
		X:   base64.RawURLEncoding.EncodeToString(pub),
		Kid: KeyID(pub),
		Use: "sig",
		Alg: "EdDSA",
	}
}

// NewJWKS builds a set from keys, preserving the argument order (the active
// key goes first) and dropping duplicates by kid.
func NewJWKS(pubs ...ed25519.PublicKey) JWKS {
	set := JWKS{Keys: []JWK{}}
	seen := map[string]bool{}
	for _, p := range pubs {
		j := PublicJWK(p)
		if seen[j.Kid] {
			continue
		}
		seen[j.Kid] = true
		set.Keys = append(set.Keys, j)
	}
	return set
}

// JWKSHandler returns a handler that serves a JWKS with the given keys.
// The JSON is serialized once at creation. The request method is not checked.
func JWKSHandler(pubs ...ed25519.PublicKey) http.Handler {
	body, err := json.Marshal(NewJWKS(pubs...))
	if err != nil {
		panic("jwtx: marshal jwks: " + err.Error())
	}
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=300")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	})
}

// parseJWKS parses a JWKS response and returns valid Ed25519 keys by kid;
// other keys are skipped.
func parseJWKS(data []byte) (map[string]ed25519.PublicKey, error) {
	var set JWKS
	if err := json.Unmarshal(data, &set); err != nil {
		return nil, err
	}
	keys := make(map[string]ed25519.PublicKey, len(set.Keys))
	for _, k := range set.Keys {
		if k.Kty != "OKP" || k.Crv != "Ed25519" || k.Kid == "" || k.X == "" {
			continue
		}
		raw, err := base64.RawURLEncoding.DecodeString(k.X)
		if err != nil || len(raw) != ed25519.PublicKeySize {
			continue
		}
		keys[k.Kid] = ed25519.PublicKey(raw)
	}
	return keys, nil
}
