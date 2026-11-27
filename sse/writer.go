package sse

import (
	"fmt"
	"net/http"
	"strings"
)

// Writer writes events to an http.ResponseWriter. Not safe for concurrent use: all calls
// must come from one goroutine (the caller serializes pings and events).
type Writer struct {
	w   http.ResponseWriter
	rc  *http.ResponseController
	err error
}

// NewWriter sets the headers Content-Type: text/event-stream, Cache-Control: no-cache,
// X-Accel-Buffering: no, calls WriteHeader(200) and flushes the buffer.
// It returns an error if Flush failed (including http.ErrNotSupported:
// the check goes through http.ResponseController, wrappers with Unwrap are supported).
// On error the 200 status may have already been sent: this is a programmer error, not a normal path.
func NewWriter(w http.ResponseWriter) (*Writer, error) {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	if err := rc.Flush(); err != nil {
		return nil, fmt.Errorf("sse: flush: %w", err)
	}
	return &Writer{w: w, rc: rc}, nil
}

// Event writes the id, event and data fields in one Write and does a Flush. Empty id and name
// are not written. Data is split into lines by \r\n, \n and \r; empty data yields one
// line "data:". An id or name with \r or \n (id also with NUL) yields ErrInvalidField, nothing
// is written, and the error is not remembered. A Write or Flush error is remembered:
// it is also returned by all subsequent Event and Comment calls without writing.
func (w *Writer) Event(id, name string, data []byte) error {
	if strings.ContainsAny(id, "\r\n\x00") {
		return fmt.Errorf("%w: id", ErrInvalidField)
	}
	if strings.ContainsAny(name, "\r\n") {
		return fmt.Errorf("%w: event", ErrInvalidField)
	}
	if w.err != nil {
		return w.err
	}
	buf := make([]byte, 0, len(data)+len(id)+len(name)+32)
	if id != "" {
		buf = append(buf, "id: "...)
		buf = append(buf, id...)
		buf = append(buf, '\n')
	}
	if name != "" {
		buf = append(buf, "event: "...)
		buf = append(buf, name...)
		buf = append(buf, '\n')
	}
	start := 0
	line := func(end int) {
		if end > start {
			buf = append(buf, "data: "...)
			buf = append(buf, data[start:end]...)
		} else {
			buf = append(buf, "data:"...)
		}
		buf = append(buf, '\n')
	}
	for i := 0; i < len(data); i++ {
		switch data[i] {
		case '\r':
			line(i)
			if i+1 < len(data) && data[i+1] == '\n' {
				i++
			}
			start = i + 1
		case '\n':
			line(i)
			start = i + 1
		}
	}
	line(len(data))
	buf = append(buf, '\n')
	return w.send(buf)
}

// Comment writes a comment line ": text" (for ping) and does a Flush. text with \r or \n
// yields ErrInvalidField (without writing or remembering). Write errors are remembered, as in Event.
func (w *Writer) Comment(text string) error {
	if strings.ContainsAny(text, "\r\n") {
		return fmt.Errorf("%w: comment", ErrInvalidField)
	}
	if w.err != nil {
		return w.err
	}
	if text == "" {
		return w.send([]byte(":\n\n"))
	}
	return w.send([]byte(": " + text + "\n\n"))
}

// send does one Write and one Flush, remembering the first error.
func (w *Writer) send(b []byte) error {
	if _, err := w.w.Write(b); err != nil {
		w.err = err
		return err
	}
	if err := w.rc.Flush(); err != nil {
		w.err = err
		return err
	}
	return nil
}
