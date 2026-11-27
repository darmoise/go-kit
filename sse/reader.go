package sse

import (
	"bufio"
	"bytes"
	"io"
)

var bom = []byte{0xEF, 0xBB, 0xBF}

// Reader parses an SSE stream (client side). Not safe for concurrent use.
type Reader struct {
	// Comments enables emitting comment lines (": text") as Event{Comment: true}.
	// False by default: comments are ignored. Set before the first call to Next.
	// When true a comment does not change the accumulated state of the current event and is not
	// its boundary; an emitted comment is not added to the size counter.
	Comments bool

	br      *bufio.Reader
	max     int
	line    []byte
	data    []byte
	hasData bool
	id      string
	name    string
	size    int
	atStart bool
	skipLF  bool
	err     error
}

// NewReader creates a Reader on top of r. maxEventBytes is the size limit of one event
// (the sum of its line lengths without terminators, including field names and comments);
// <= 0 means DefaultMaxEventBytes.
func NewReader(r io.Reader, maxEventBytes int) *Reader {
	if maxEventBytes <= 0 {
		maxEventBytes = DefaultMaxEventBytes
	}
	return &Reader{br: bufio.NewReader(r), max: maxEventBytes, atStart: true}
}

// Next returns the next event.
//
// Line terminators: \n, \r\n, \r; a BOM at the start of the stream is stripped. An event
// is dispatched by an empty line if it had at least one data line
// (including an empty one). ID is taken only from the current event (not inherited from the previous
// one), the retry field and unknown fields are ignored, an id with NUL is ignored.
//
// An event not terminated by an empty line by the end of the stream (including a truncated
// last line) is not emitted: Next returns Event{}, io.EOF. Comments
// are ignored by default; with Comments == true every comment
// terminated by a terminator is returned as Event{Comment: true}, and the accumulated
// state of the current event is preserved. An unterminated comment at the end of the stream is not emitted.
//
// Limit: the sum of the event's line lengths (without terminators) must not exceed
// maxEventBytes, otherwise Next immediately returns ErrEventTooLarge without reading the rest.
// A comment is checked together with the accumulated sum; with Comments == true an emitted
// comment is not included in the sum.
//
// Errors from the underlying io.Reader are returned as is, without wrapping
// (errors.Is distinguishes cancellation, timeout and disconnect); a partially accumulated event
// is not emitted. All errors (io.EOF, read, ErrEventTooLarge) are sticky.
func (r *Reader) Next() (Event, error) {
	if r.err != nil {
		return Event{}, r.err
	}
	for {
		line, err := r.readLine()
		if err != nil {
			r.err = err
			return Event{}, err
		}
		if len(line) == 0 {
			if r.hasData {
				ev := Event{ID: r.id, Name: r.name, Data: bytes.Clone(r.data[:len(r.data)-1])}
				r.reset()
				return ev, nil
			}
			r.reset()
			continue
		}
		if line[0] == ':' {
			if r.Comments {
				return Event{Comment: true}, nil
			}
			r.size += len(line)
			continue
		}
		r.size += len(line)
		field, value := line, []byte(nil)
		if i := bytes.IndexByte(line, ':'); i >= 0 {
			field, value = line[:i], line[i+1:]
			if len(value) > 0 && value[0] == ' ' {
				value = value[1:]
			}
		}
		switch string(field) {
		case "data":
			r.data = append(r.data, value...)
			r.data = append(r.data, '\n')
			r.hasData = true
		case "event":
			r.name = string(value)
		case "id":
			if bytes.IndexByte(value, 0) < 0 {
				r.id = string(value)
			}
		}
	}
}

func (r *Reader) reset() {
	r.id, r.name, r.size = "", "", 0
	r.data = r.data[:0]
	r.hasData = false
}

// readLine reads one line without the terminator byte by byte, checking the limit as it reads.
// The returned slice is valid until the next call.
func (r *Reader) readLine() ([]byte, error) {
	r.line = r.line[:0]
	for {
		b, err := r.br.ReadByte()
		if err != nil {
			return nil, err
		}
		if r.skipLF {
			r.skipLF = false
			if b == '\n' {
				continue
			}
		}
		if b == '\n' || b == '\r' {
			r.skipLF = b == '\r'
			r.atStart = false
			if r.size+len(r.line) > r.max {
				return nil, ErrEventTooLarge
			}
			return r.line, nil
		}
		r.line = append(r.line, b)
		if r.atStart && len(r.line) == 3 {
			r.atStart = false
			if bytes.Equal(r.line, bom) {
				r.line = r.line[:0]
			}
		}
		if !r.atStart && r.size+len(r.line) > r.max {
			return nil, ErrEventTooLarge
		}
	}
}
