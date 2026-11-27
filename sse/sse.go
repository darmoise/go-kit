// Package sse implements Server-Sent Events (text/event-stream): writing to http.ResponseWriter
// (Writer) and parsing the stream on the client side (Reader). stdlib only.
package sse

import "errors"

// DefaultMaxEventBytes is the Reader event size limit if maxEventBytes <= 0 is passed (1 MiB).
const DefaultMaxEventBytes = 1 << 20

// ErrEventTooLarge is returned by Reader.Next if the size of the current event exceeded the limit.
var ErrEventTooLarge = errors.New("sse: event too large")

// ErrInvalidField is returned by Writer for an invalid id, name or comment text
// (containing \r or \n; for id also NUL).
var ErrInvalidField = errors.New("sse: invalid field")

// Event is a single SSE event.
type Event struct {
	// ID is the value of the id field; empty if there was no id field in THIS event.
	ID string
	// Name is the value of the event field; empty = "message" (Reader does not substitute it).
	Name string
	// Data is the data lines joined with "\n"; always a fresh copy, the caller may keep it.
	Data []byte
	// Comment is true only for a "comment event", which Reader emits when
	// Reader.Comments == true: ID, Name and Data are empty, the comment text
	// is not returned. In all other cases it is always false.
	Comment bool
}
