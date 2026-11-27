package sse_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/darmoise/go-kit/sse"
)

var errBoom = errors.New("boom")

// ev is the expected event; c = true for a comment event.
type ev struct {
	id, name, data string
	c              bool
}

// feeds are three ways of supplying one input.
var feeds = []struct {
	name string
	wrap func(string) io.Reader
}{
	{"whole", func(s string) io.Reader { return strings.NewReader(s) }},
	{"onebyte", func(s string) io.Reader { return iotest.OneByteReader(strings.NewReader(s)) }},
	{"dataerr", func(s string) io.Reader { return iotest.DataErrReader(strings.NewReader(s)) }},
}

// readAll reads events until the first error.
func readAll(rd *sse.Reader) ([]ev, error) {
	var out []ev
	for {
		e, err := rd.Next()
		if err != nil {
			return out, err
		}
		out = append(out, ev{e.ID, e.Name, string(e.Data), e.Comment})
	}
}

func equalEvs(a, b []ev) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// check runs the input three ways and compares the events and the final error.
func check(t *testing.T, in string, max int, comments bool, want []ev, wantErr error) {
	t.Helper()
	for _, f := range feeds {
		rd := sse.NewReader(f.wrap(in), max)
		rd.Comments = comments
		got, err := readAll(rd)
		if !equalEvs(got, want) || !errors.Is(err, wantErr) {
			t.Errorf("%s: in=%q: got %v err=%v, want %v err=%v", f.name, in, got, err, want, wantErr)
		}
		if _, err2 := rd.Next(); err2 != err {
			t.Errorf("%s: in=%q: sticky: got %v, want %v", f.name, in, err2, err)
		}
	}
}

func TestReaderTable(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want []ev
	}{
		{"basic", "data: a\n\n", []ev{{data: "a"}}},
		{"id event", "id: 7\nevent: delta\ndata: {\"x\":1}\n\n", []ev{{"7", "delta", `{"x":1}`, false}}},
		{"multiline", "data: a\ndata: b\n\n", []ev{{data: "a\nb"}}},
		{"multiline empty middle", "data: a\ndata:\ndata: c\n\n", []ev{{data: "a\n\nc"}}},
		{"trailing empty line", "data: a\ndata:\n\n", []ev{{data: "a\n"}}},
		{"space none", "data:a\n\n", []ev{{data: "a"}}},
		{"space one", "data: a\n\n", []ev{{data: "a"}}},
		{"space two", "data:  a\n\n", []ev{{data: " a"}}},
		{"field without colon", "data\n\n", []ev{{}}},
		{"event without colon", "event\ndata: x\n\n", []ev{{data: "x"}}},
		{"empty data", "data:\n\n", []ev{{}}},
		{"comment only", ": ping\n\n", nil},
		{"comment before", ": c\ndata: a\n\n", []ev{{data: "a"}}},
		{"comment inside", "data: a\n: c\ndata: b\n\n", []ev{{data: "a\nb"}}},
		{"comment between", "data: a\n\n: c\n\ndata: b\n\n", []ev{{data: "a"}, {data: "b"}}},
		{"retry", "retry: 1000\ndata: a\n\n", []ev{{data: "a"}}},
		{"retry only", "retry: 1000\n\n", nil},
		{"unknown", "foo: bar\ndata: a\n\n", []ev{{data: "a"}}},
		{"lf", "data: a\n\n", []ev{{data: "a"}}},
		{"crlf", "data: a\r\n\r\n", []ev{{data: "a"}}},
		{"cr", "data: a\r\r", []ev{{data: "a"}}},
		{"mixed", "data: a\r\ndata: b\n\r\n", []ev{{data: "a\nb"}}},
		{"cr empty line", "data: a\r\r", []ev{{data: "a"}}},
		{"two crlf", "data: a\r\n\r\ndata: b\r\n\r\n", []ev{{data: "a"}, {data: "b"}}},
		{"bom", "\xEF\xBB\xBFdata: a\n\n", []ev{{data: "a"}}},
		{"bom two events", "\xEF\xBB\xBFdata: a\n\ndata: b\n\n", []ev{{data: "a"}, {data: "b"}}},
		{"bom middle", "data: a\n\n\xEF\xBB\xBFdata: b\n\n", []ev{{data: "a"}}},
		{"bom partial lf", "\xEF\xBB\n\n", nil},
		{"bom partial other", "\xEF\xBBxdata: a\n\ndata: b\n\n", []ev{{data: "b"}}},
		{"bom then lf", "\xEF\xBB\xBF\ndata: a\n\n", []ev{{data: "a"}}},
		{"bom then cr", "\xEF\xBB\xBF\rdata: a\n\n", []ev{{data: "a"}}},
		{"double bom", "\xEF\xBB\xBF\xEF\xBB\xBFdata: a\n\ndata: b\n\n", []ev{{data: "b"}}},
		{"id not inherited", "id: 1\ndata: a\n\ndata: b\n\n", []ev{{"1", "", "a", false}, {data: "b"}}},
		{"id replaced", "id: 1\ndata: a\n\nid: 2\ndata: b\n\n", []ev{{"1", "", "a", false}, {"2", "", "b", false}}},
		{"id empty", "id:\ndata: a\n\n", []ev{{data: "a"}}},
		{"id last wins", "id: 1\nid: 2\ndata: a\n\n", []ev{{"2", "", "a", false}}},
		{"id nul", "id: a\x00b\ndata: x\n\n", []ev{{data: "x"}}},
		{"no data not leaked", "event: x\nid: 5\n\ndata: a\n\n", []ev{{data: "a"}}},
		{"unterminated last", "data: a\n\ndata: b", []ev{{data: "a"}}},
		{"unterminated last nl", "data: a\n\ndata: b\n", []ev{{data: "a"}}},
		{"unterminated single", "data: a\n", nil},
		{"truncated json", "id: 1\nevent: x\ndata: {\"trunc", nil},
		{"empty input", "", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			check(t, c.in, 0, false, c.want, io.EOF)
		})
	}
}

func TestReaderEmptyDataLen(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"data\n\n", "data:\n\n"} {
		e, err := sse.NewReader(strings.NewReader(in), 0).Next()
		if err != nil || len(e.Data) != 0 || e.Comment {
			t.Errorf("%q: got %+v err=%v", in, e, err)
		}
	}
}

func TestReaderComments(t *testing.T) {
	t.Parallel()
	C := ev{c: true}
	cases := []struct {
		name string
		in   string
		want []ev
	}{
		{"single", ": ping\n\n", []ev{C}},
		{"two", ": a\n: b\n", []ev{C, C}},
		{"no blank line", ": keep-alive\n", []ev{C}},
		{"inside event", "data: a\n: c\ndata: b\n\n", []ev{C, {data: "a\nb"}}},
		{"state kept", "id: 1\n: c\nevent: x\ndata: a\n\n", []ev{C, {"1", "x", "a", false}}},
		{"between events", "id: 1\ndata: a\n\n: c\ndata: b\n\n", []ev{{"1", "", "a", false}, C, {data: "b"}}},
		{"unterminated", "data: a\n\n: c", []ev{{data: "a"}}},
		{"crlf", ": c\r\ndata: a\r\n\r\n", []ev{C, {data: "a"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			check(t, c.in, 0, true, c.want, io.EOF)
		})
	}
}

// The same sequence of non-comment events with Comments false and true.
func TestReaderCommentsSameEvents(t *testing.T) {
	t.Parallel()
	in := ": a\nid: 1\n: b\ndata: x\n\n: c\nevent: e\ndata: y\ndata: z\n\n: d\n"
	var plain, withC []ev
	for _, comments := range []bool{false, true} {
		rd := sse.NewReader(strings.NewReader(in), 0)
		rd.Comments = comments
		got, err := readAll(rd)
		if err != io.EOF {
			t.Fatal(err)
		}
		if !comments {
			for _, e := range got {
				if e.c {
					t.Fatal("comment event with Comments=false")
				}
			}
			plain = got
			continue
		}
		for _, e := range got {
			if !e.c {
				withC = append(withC, e)
			}
		}
	}
	if !equalEvs(plain, withC) || len(plain) != 2 {
		t.Fatalf("plain=%v withC=%v", plain, withC)
	}
}

func TestReaderLimit(t *testing.T) {
	t.Parallel()
	d := func(n int) string { return "data: " + strings.Repeat("x", n) }
	x := func(n int) string { return strings.Repeat("x", n) }
	t.Run("exact", func(t *testing.T) {
		t.Parallel()
		check(t, d(14)+"\n\n", 20, false, []ev{{data: x(14)}}, io.EOF)
	})
	t.Run("over", func(t *testing.T) {
		t.Parallel()
		check(t, d(15)+"\n\n", 20, false, nil, sse.ErrEventTooLarge)
	})
	t.Run("sum", func(t *testing.T) {
		t.Parallel()
		check(t, d(10)+"\n"+d(10)+"\n\n", 20, false, nil, sse.ErrEventTooLarge)
	})
	t.Run("reset between events", func(t *testing.T) {
		t.Parallel()
		check(t, d(9)+"\n\n"+d(9)+"\n\n", 20, false, []ev{{data: x(9)}, {data: x(9)}}, io.EOF)
	})
	t.Run("comments counted", func(t *testing.T) {
		t.Parallel()
		check(t, ": "+x(10)+"\n"+d(10)+"\n\n", 20, false, nil, sse.ErrEventTooLarge)
	})
	t.Run("events before error delivered", func(t *testing.T) {
		t.Parallel()
		check(t, d(5)+"\n\n"+d(30)+"\n\n", 20, false, []ev{{data: x(5)}}, sse.ErrEventTooLarge)
	})
	t.Run("crlf not counted", func(t *testing.T) {
		t.Parallel()
		check(t, d(14)+"\r\n\r\n", 20, false, []ev{{data: x(14)}}, io.EOF)
	})
	// Comments = true.
	t.Run("c comment too long", func(t *testing.T) {
		t.Parallel()
		check(t, ":"+x(20)+"\n", 20, true, nil, sse.ErrEventTooLarge)
	})
	t.Run("c comment exact", func(t *testing.T) {
		t.Parallel()
		check(t, ":"+x(19)+"\n", 20, true, []ev{{c: true}}, io.EOF)
	})
	t.Run("c accumulated plus comment", func(t *testing.T) {
		t.Parallel()
		check(t, d(10)+"\n:"+x(4)+"\n", 20, true, nil, sse.ErrEventTooLarge)
	})
	t.Run("c many comments", func(t *testing.T) {
		t.Parallel()
		in := strings.Repeat(":"+x(9)+"\n", 100)
		want := make([]ev, 100)
		for i := range want {
			want[i] = ev{c: true}
		}
		check(t, in, 20, true, want, io.EOF)
	})
}

// endless yields 'a' forever and counts what was read.
type endless struct{ n int }

func (e *endless) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'a'
	}
	e.n += len(p)
	return len(p), nil
}

func TestReaderLimitMemory(t *testing.T) {
	t.Parallel()
	src := &endless{}
	rd := sse.NewReader(src, 1024)
	if _, err := rd.Next(); !errors.Is(err, sse.ErrEventTooLarge) {
		t.Fatalf("err=%v", err)
	}
	if src.n > 1024+4096 {
		t.Fatalf("read %d bytes from source", src.n)
	}
	if _, err := rd.Next(); !errors.Is(err, sse.ErrEventTooLarge) {
		t.Fatalf("not sticky: %v", err)
	}
	// A comment without a terminator with Comments=true is limited too.
	src = &endless{}
	rd = sse.NewReader(io.MultiReader(strings.NewReader(":"), src), 1024)
	rd.Comments = true
	if _, err := rd.Next(); !errors.Is(err, sse.ErrEventTooLarge) || src.n > 1024+4096 {
		t.Fatalf("comment: err=%v read=%d", err, src.n)
	}
}

func TestReaderDefaultLimit(t *testing.T) {
	t.Parallel()
	for _, max := range []int{0, -1} {
		ok := "data: " + strings.Repeat("x", 512<<10) + "\n\n"
		e, err := sse.NewReader(strings.NewReader(ok), max).Next()
		if err != nil || len(e.Data) != 512<<10 {
			t.Fatalf("max=%d: %v len=%d", max, err, len(e.Data))
		}
		big := "data: " + strings.Repeat("x", sse.DefaultMaxEventBytes+1) + "\n\n"
		if _, err := sse.NewReader(strings.NewReader(big), max).Next(); !errors.Is(err, sse.ErrEventTooLarge) {
			t.Fatalf("max=%d: %v", max, err)
		}
	}
}

func TestReaderReadErrors(t *testing.T) {
	t.Parallel()
	for _, boom := range []error{errBoom, io.ErrUnexpectedEOF, context.Canceled} {
		src := io.MultiReader(strings.NewReader("data: a\n\ndata: b\n"), iotest.ErrReader(boom))
		rd := sse.NewReader(src, 0)
		e, err := rd.Next()
		if err != nil || string(e.Data) != "a" {
			t.Fatalf("%v: %+v %v", boom, e, err)
		}
		for range 2 {
			e, err = rd.Next()
			if err != boom || e.Data != nil || e.ID != "" {
				t.Fatalf("%v: got %+v err=%v (want unwrapped)", boom, e, err)
			}
		}
	}
}

func TestReaderDataCopy(t *testing.T) {
	t.Parallel()
	rd := sse.NewReader(strings.NewReader("data: abc\n\ndata: xyz\n\n"), 0)
	e1, _ := rd.Next()
	e1.Data[0] = 'Z'
	e2, _ := rd.Next()
	if string(e2.Data) != "xyz" {
		t.Fatalf("e2=%q", e2.Data)
	}
	e3 := sse.NewReader(strings.NewReader("data: abc\n\ndata: xyz\n\n"), 0)
	a, _ := e3.Next()
	keep := bytes.Clone(a.Data)
	_, _ = e3.Next()
	if !bytes.Equal(a.Data, keep) {
		t.Fatalf("data changed: %q", a.Data)
	}
}

const fixtureNamed = `event: message_start
data: {"type":"message_start","message":{"id":"id_1","model":"model-x","usage":{"input_tokens":3,"output_tokens":1}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: ping
data: {"type": "ping"}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" world — ✓"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}

event: message_stop
data: {"type":"message_stop"}

`

const fixtureData = `data: {"id":"id_1","model":"model-x","choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}

data: {"id":"id_1","model":"model-x","choices":[{"index":0,"delta":{"content":"Hi"}}]}

data: {"id":"id_1","model":"model-x","choices":[{"index":0,"delta":{"content":" there"}}]}

data: {"id":"id_1","model":"model-x","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}

data: {"id":"id_1","model":"model-x","choices": [],"usage":{"prompt_tokens":3,"completion_tokens":2}}

data: [DONE]

`

func TestFixtureNamed(t *testing.T) {
	t.Parallel()
	names := []string{"message_start", "content_block_start", "ping", "content_block_delta",
		"content_block_delta", "content_block_stop", "message_delta", "message_stop"}
	for _, crlf := range []bool{false, true} {
		in := fixtureNamed
		if crlf {
			in = strings.ReplaceAll(in, "\n", "\r\n")
		}
		for _, f := range feeds {
			rd := sse.NewReader(f.wrap(in), 0)
			for i, n := range names {
				e, err := rd.Next()
				if err != nil || e.Name != n || e.ID != "" || !json.Valid(e.Data) {
					t.Fatalf("%s crlf=%v #%d: %+v %v", f.name, crlf, i, e, err)
				}
			}
			if _, err := rd.Next(); err != io.EOF {
				t.Fatalf("%s: %v", f.name, err)
			}
		}
	}
}

func fixtureDataChecks(t *testing.T, rd *sse.Reader, n int, wantDone bool, comments int) {
	t.Helper()
	events, gotComments := 0, 0
	for {
		e, err := rd.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if e.Comment {
			gotComments++
			continue
		}
		events++
		if e.Name != "" {
			t.Fatalf("name %q", e.Name)
		}
		if string(e.Data) == "[DONE]" {
			if !wantDone {
				t.Fatal("unexpected DONE")
			}
		} else if !json.Valid(e.Data) {
			t.Fatalf("invalid json %q", e.Data)
		}
	}
	if events != n || gotComments != comments {
		t.Fatalf("events=%d comments=%d, want %d/%d", events, gotComments, n, comments)
	}
}

func TestFixtureDataOnly(t *testing.T) {
	t.Parallel()
	for _, crlf := range []bool{false, true} {
		in := fixtureData
		if crlf {
			in = strings.ReplaceAll(in, "\n", "\r\n")
		}
		for _, f := range feeds {
			fixtureDataChecks(t, sse.NewReader(f.wrap(in), 0), 6, true, 0)
			// The last event is [DONE].
			rd := sse.NewReader(f.wrap(in), 0)
			got, _ := readAll(rd)
			if got[len(got)-1].data != "[DONE]" {
				t.Fatalf("last=%q", got[len(got)-1].data)
			}
		}
	}
}

func TestFixtureTruncated(t *testing.T) {
	t.Parallel()
	in := strings.TrimSuffix(fixtureData, "[DONE]\n\n") + `{"id":"id_1","choi`
	for _, f := range feeds {
		got, err := readAll(sse.NewReader(f.wrap(in), 0))
		if err != io.EOF || len(got) != 5 {
			t.Fatalf("%s: n=%d err=%v", f.name, len(got), err)
		}
	}
}

func TestFixtureKeepAlive(t *testing.T) {
	t.Parallel()
	in := ": keep-alive\n\n" + strings.ReplaceAll(fixtureData, "\n\n", "\n\n: keep-alive\n\n")
	n := strings.Count(in, ": keep-alive")
	for _, f := range feeds {
		fixtureDataChecks(t, sse.NewReader(f.wrap(in), 0), 6, true, 0)
		rd := sse.NewReader(f.wrap(in), 0)
		rd.Comments = true
		fixtureDataChecks(t, rd, 6, true, n)
	}
}

func TestReaderBOMSmallLimit(t *testing.T) {
	t.Parallel()
	check(t, "\xEF\xBB\xBFd\n\n", 1, false, nil, io.EOF)
	check(t, "\xEF\xBB\xBFdata:a\n\n", 6, false, []ev{{data: "a"}}, io.EOF)
}
