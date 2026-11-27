package httpx

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

type logHolder struct {
	mu    sync.Mutex
	attrs []slog.Attr
}

// AddLogAttrs appends attributes to the current request's access log record.
// Safe for concurrent use; a no-op without AccessLog in the chain.
func AddLogAttrs(ctx context.Context, attrs ...slog.Attr) {
	h, _ := ctx.Value(logHolderKey).(*logHolder)
	if h == nil {
		return
	}
	h.mu.Lock()
	h.attrs = append(h.attrs, attrs...)
	h.mu.Unlock()
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	if code >= 200 || code == http.StatusSwitchingProtocols {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

// Flush forwards the buffer flush (SSE).
func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap is needed by http.ResponseController.
func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// AccessLog writes one Info "request" record per request: request_id, method,
// route, status, duration_ms and attributes from AddLogAttrs. The query, path with
// parameters, IP, headers and bodies are not logged.
//
// route is taken from r.Pattern, which ServeMux sets on the received
// *http.Request, so AccessLog must be the innermost middleware
// (closest to the mux): Chain(mux, RequestID(), Recover(l), AccessLog(l)).
// Any middleware with r.WithContext between AccessLog and the mux yields route=unmatched.
// On panic the record is written with status=500, after which the panic continues.
func AccessLog(log *slog.Logger) Middleware {
	if log == nil {
		log = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			holder := &logHolder{}
			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			r2 := r.WithContext(context.WithValue(r.Context(), logHolderKey, holder))

			write := func(status int) {
				route := r2.Pattern
				if route == "" {
					route = "unmatched"
				}
				attrs := []slog.Attr{
					slog.String("request_id", RequestIDFrom(r.Context())),
					slog.String("method", r.Method),
					slog.String("route", route),
					slog.Int("status", status),
					slog.Int64("duration_ms", time.Since(start).Milliseconds()),
				}
				holder.mu.Lock()
				attrs = append(attrs, holder.attrs...)
				holder.mu.Unlock()
				log.LogAttrs(r.Context(), slog.LevelInfo, "request", attrs...)
			}
			defer func() {
				if p := recover(); p != nil {
					write(http.StatusInternalServerError)
					panic(p)
				}
			}()
			next.ServeHTTP(sw, r2)
			write(sw.status)
		})
	}
}
