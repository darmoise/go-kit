// Package servicetoken checks Bearer tokens of internal (service-to-service)
// HTTP APIs. The package does not check the number or length of tokens: that is up to the
// caller's config. Tokens are not logged and do not appear in error text.
package servicetoken

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"

	"github.com/darmoise/go-kit/httpx"
)

// Matcher returns a function that compares a presented token against all
// tokens in the list in constant time (without early exit) and returns
// the index of the matching token in the original slice (the smallest one for duplicates)
// or -1. Empty tokens never match but preserve the indexes of the others.
// The slice is copied (hashed) at call time.
func Matcher(tokens []string) func(presented string) int {
	hashes := make([][sha256.Size]byte, len(tokens))
	valid := make([]bool, len(tokens))
	for i, t := range tokens {
		if t != "" {
			hashes[i] = sha256.Sum256([]byte(t))
			valid[i] = true
		}
	}
	return func(presented string) int {
		if presented == "" {
			return -1
		}
		h := sha256.Sum256([]byte(presented))
		found := -1
		for i := range hashes {
			if valid[i] && subtle.ConstantTimeCompare(h[:], hashes[i][:]) == 1 && found < 0 {
				found = i
			}
		}
		return found
	}
}

// Middleware lets through requests with the header "Authorization: Bearer <token>",
// where token matches one of tokens; otherwise it responds 401 unauthorized.
// An empty list rejects everything.
func Middleware(tokens []string) httpx.Middleware {
	match := Matcher(tokens)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if tok, ok := httpx.BearerToken(r); ok && match(tok) >= 0 {
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Set("WWW-Authenticate", "Bearer")
			httpx.WriteError(w, r, httpx.NewError(http.StatusUnauthorized, "unauthorized", "Missing or invalid service token"))
		})
	}
}
