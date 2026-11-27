package httpx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
)

// Middleware is a wrapper around http.Handler.
type Middleware func(http.Handler) http.Handler

// Chain wraps h in middleware: the first one is the outermost, that is
// Chain(h, a, b) = a(b(h)). Nil elements are skipped.
func Chain(h http.Handler, mw ...Middleware) http.Handler {
	for i := len(mw) - 1; i >= 0; i-- {
		if mw[i] != nil {
			h = mw[i](h)
		}
	}
	return h
}

type ctxKey int

const (
	requestIDKey ctxKey = iota
	logHolderKey
)

// RequestID accepts an incoming X-Request-Id (up to 64 characters from [A-Za-z0-9._-])
// or generates a new one (32 hex), and puts it into the context and the response header.
func RequestID() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get("X-Request-Id")
			if !validRequestID(id) {
				id = newRequestID()
			}
			w.Header().Set("X-Request-Id", id)
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, id)))
		})
	}
}

// RequestIDFrom returns the request identifier from the context or "".
func RequestIDFrom(ctx context.Context) string {
	s, _ := ctx.Value(requestIDKey).(string)
	return s
}

func validRequestID(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '.', c == '_', c == '-':
		default:
			return false
		}
	}
	return true
}

func newRequestID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Recover catches a handler panic, logs it with a stack trace and responds
// 500 internal. http.ErrAbortHandler is re-panicked without a response or log.
func Recover(log *slog.Logger) Middleware {
	if log == nil {
		log = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				p := recover()
				if p == nil {
					return
				}
				if p == http.ErrAbortHandler {
					panic(p)
				}
				log.Error("panic recovered",
					"request_id", RequestIDFrom(r.Context()),
					"panic", fmt.Sprint(p),
					"stack", string(debug.Stack()))
				WriteError(w, r, NewError(http.StatusInternalServerError, "internal", "Internal server error"))
			}()
			next.ServeHTTP(w, r)
		})
	}
}
