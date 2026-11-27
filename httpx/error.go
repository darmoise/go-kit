// Package httpx provides HTTP helpers: JSON responses and errors in a single format,
// middleware (RequestID, Recover, AccessLog), ClientIP and BearerToken.
package httpx

import (
	"errors"
	"log/slog"
	"math"
	"net/http"
	"time"
)

// Error is an API error that WriteError sends to the client as is.
type Error struct {
	Status     int            // HTTP status (400..599, otherwise treated as 500)
	Code       string         // machine-readable code, for example "invalid_request"
	Message    string         // message for the client
	RetryAfter time.Duration  // >0: Retry-After header and retry_after field
	Extra      map[string]any // extra fields of the error object (except reserved ones)
}

// Error returns Message.
func (e *Error) Error() string { return e.Message }

// NewError creates an API error with a status, code and message.
func NewError(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

// WriteError writes the error in the format {"error":{"code","message","request_id",...}}.
// If err (including a wrapped one) is an *Error, it is written as is. Any other
// error (and nil) is logged and turned into 500 internal; its text is not
// sent to the client. r may be nil.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	var e *Error
	var reqID string
	if r != nil {
		reqID = RequestIDFrom(r.Context())
	}
	if !errors.As(err, &e) || e == nil {
		slog.Default().Error("request failed", "error", err, "request_id", reqID)
		e = NewError(http.StatusInternalServerError, "internal", "Internal server error")
	}

	status := e.Status
	if status < 400 || status > 599 {
		status = http.StatusInternalServerError
	}

	obj := make(map[string]any, len(e.Extra)+4)
	for k, v := range e.Extra {
		switch k {
		case "code", "message", "request_id", "retry_after":
			continue
		}
		obj[k] = v
	}
	obj["code"] = e.Code
	obj["message"] = e.Message
	if reqID != "" {
		obj["request_id"] = reqID
	}
	if e.RetryAfter > 0 {
		secs := int64(math.Ceil(e.RetryAfter.Seconds()))
		if secs < 1 {
			secs = 1
		}
		obj["retry_after"] = secs
		w.Header().Set("Retry-After", itoa(secs))
	}
	WriteJSON(w, status, map[string]any{"error": obj})
}
