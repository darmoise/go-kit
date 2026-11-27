package httpx

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
	"unicode"
)

func normalizeIP(s string) (string, bool) {
	a, err := netip.ParseAddr(s)
	if err != nil {
		return "", false
	}
	return a.Unmap().String(), true
}

// ClientIP returns the client IP. With trustProxy=false only
// RemoteAddr is used. With trustProxy=true the last X-Forwarded-For element is taken
// (added by a trusted proxy); if it is empty or not an IP, RemoteAddr is used.
// If RemoteAddr cannot be parsed either, its raw value is returned.
func ClientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if vals := r.Header.Values("X-Forwarded-For"); len(vals) > 0 {
			parts := strings.Split(strings.Join(vals, ","), ",")
			last := strings.TrimSpace(parts[len(parts)-1])
			if ip, ok := normalizeIP(last); ok {
				return ip
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip, ok := normalizeIP(host); ok {
		return ip
	}
	return r.RemoteAddr
}

// BearerToken extracts the token from "Authorization: Bearer <token>" (the scheme
// is case-insensitive, exactly one space, the token has no whitespace characters).
func BearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	const prefix = "bearer "
	if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return "", false
	}
	tok := h[len(prefix):]
	if strings.IndexFunc(tok, unicode.IsSpace) >= 0 {
		return "", false
	}
	return tok, true
}
