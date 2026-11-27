// Package jwtx signs and verifies access JWTs (EdDSA/Ed25519), publishes
// and parses JWKS, and provides a verifier with a key cache.
package jwtx

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/golang-jwt/jwt/v5"
)

// Claims are the claims of an access token.
//
// A single aud is serialized by the library as an array of one element
// (RFC 7519 allows this); Verify accepts both a string and an array.
type Claims struct {
	jwt.RegisteredClaims
	// AuthorizedParty is azp, the client the token was issued for.
	AuthorizedParty string `json:"azp"`
	// SessionID is sid, the session identifier (optional).
	SessionID string `json:"sid,omitempty"`
	// Email is the user's email (optional).
	Email string `json:"email,omitempty"`
}

// KeyID returns the key identifier: base64url of the first 8 bytes of sha256(pub).
func KeyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return base64.RawURLEncoding.EncodeToString(sum[:8])
}

// Sign signs claims with key priv (EdDSA) with the header kid = KeyID(pub).
// Sign does not fill the iat, exp, jti fields; the caller sets them.
func Sign(priv ed25519.PrivateKey, c Claims) (string, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return "", errors.New("jwtx: invalid private key size")
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, c)
	tok.Header["kid"] = KeyID(priv.Public().(ed25519.PublicKey))
	s, err := tok.SignedString(priv)
	if err != nil {
		return "", fmt.Errorf("jwtx: sign: %w", err)
	}
	return s, nil
}
