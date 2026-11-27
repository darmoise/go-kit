package httpx

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
)

const defaultMaxBody = 1 << 20 // 1 MiB

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// WriteJSON writes v as JSON with the given status. If serialization
// fails, it responds 500 internal and logs the error.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		slog.Default().Error("json marshal failed", "error", err)
		status = http.StatusInternalServerError
		b = []byte(`{"error":{"code":"internal","message":"Internal server error"}}`)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

// DecodeJSON reads the request body into dst. It does not write a response: on error
// it returns *Error for WriteError. maxBytes <= 0 means 1 MiB.
// Unknown fields are ignored (forward compatibility). Error text does not
// contain body fragments.
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any, maxBytes int64) error {
	if maxBytes <= 0 {
		maxBytes = defaultMaxBody
	}
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mt != "application/json" {
		return NewError(http.StatusBadRequest, "invalid_request", "Content-Type must be application/json")
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBytes))
	if err := dec.Decode(dst); err != nil {
		return decodeError(err)
	}
	// After the first value only the end of the stream is allowed.
	var extra json.RawMessage
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return invalidBody()
		}
		return decodeError(err)
	}
	return nil
}

func invalidBody() *Error {
	return NewError(http.StatusBadRequest, "invalid_request", "Invalid JSON body")
}

func decodeError(err error) *Error {
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		return NewError(http.StatusRequestEntityTooLarge, "payload_too_large", "Request body too large")
	}
	return invalidBody()
}
