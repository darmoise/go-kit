// Package httpserver runs an HTTP server with graceful shutdown on context
// cancellation and provides a ready-made healthz handler.
package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// shutdownTimeout is how long to wait for in-flight requests to finish. Changed
// only in an internal test.
var shutdownTimeout = 10 * time.Second

// Serve listens on addr and serves h until ctx is cancelled. On cancellation it waits
// up to 10 s for in-flight requests to finish, then closes the remaining connections
// and returns an error. A clean stop returns nil. A Listen error
// is returned but not logged. log == nil means slog.Default().
// Request contexts do not depend on ctx: in-flight requests are not interrupted.
func Serve(ctx context.Context, log *slog.Logger, addr string, h http.Handler) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	return serve(ctx, log, ln, h)
}

func serve(ctx context.Context, log *slog.Logger, ln net.Listener, h http.Handler) error {
	if log == nil {
		log = slog.Default()
	}
	srv := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       120 * time.Second,
		// WriteTimeout is intentionally not set: the application may serve long-lived
		// SSE streams, which a global write timeout would cut off.
	}
	log.Info("listening", "addr", ln.Addr().String())

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()

	select {
	case err := <-errCh:
		// Serve always returns non-nil; before Shutdown this is a real error.
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}

	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	err := srv.Shutdown(shutdownCtx)
	if err != nil && errors.Is(err, context.DeadlineExceeded) {
		_ = srv.Close()
	}
	<-errCh // the srv.Serve goroutine has finished (ErrServerClosed)
	if err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	log.Info("shutdown complete")
	return nil
}

// Healthz returns a handler that responds 200 and
// {"service":"<service>","status":"ok"}. Method restriction is left to the caller's
// mux ("GET /healthz").
func Healthz(service string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "service": service})
	}
}
