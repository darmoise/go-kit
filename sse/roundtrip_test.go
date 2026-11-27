package sse_test

import (
	"bytes"
	"io"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/darmoise/go-kit/sse"
)

type rtItem struct {
	id, name  string
	data      string
	comment   string // comment text when isComment
	isComment bool
	want      string // expected Data
}

func TestRoundTrip(t *testing.T) {
	t.Parallel()
	items := []rtItem{
		{id: "1", name: "a", data: "x", want: "x"},
		{data: "", want: ""},
		{id: "2", data: "\x00", want: "\x00"}, // NUL in data is allowed
		{isComment: true, comment: "ping"},
		{data: "a\nb", want: "a\nb"},
		{data: "a\rb", want: "a\nb"},
		{data: "a\r\nb\rc\nd", want: "a\nb\nc\nd"},
		{id: "3", name: "n", data: "a\n", want: "a\n"},
		{isComment: true},
		{data: " lead", want: " lead"},
		{data: `{"k":"v","n":[1,2]}`, want: `{"k":"v","n":[1,2]}`},
		{data: "hello, 世界 ✓", want: "hello, 世界 ✓"},
		{id: "7", data: "p", want: "p"},
		{id: "8", data: "q", want: "q"},
		{data: "no id", want: "no id"},
		{id: "9", name: "only", data: "\n", want: "\n"},
	}
	for _, comments := range []bool{false, true} {
		rec := httptest.NewRecorder()
		w, _ := sse.NewWriter(rec)
		for _, it := range items {
			var err error
			if it.isComment {
				err = w.Comment(it.comment)
			} else {
				err = w.Event(it.id, it.name, []byte(it.data))
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		_ = w.Comment("") // final comment
		rd := sse.NewReader(rec.Body, 0)
		rd.Comments = comments
		for i, it := range items {
			if it.isComment {
				if comments {
					e, err := rd.Next()
					if err != nil || !e.Comment {
						t.Fatalf("#%d: %+v %v", i, e, err)
					}
				}
				continue
			}
			e, err := rd.Next()
			if err != nil || e.Comment || e.ID != it.id || e.Name != it.name || !bytes.Equal(e.Data, []byte(it.want)) {
				t.Fatalf("comments=%v #%d: %+v err=%v want %+v", comments, i, e, err, it)
			}
		}
		if comments {
			if e, err := rd.Next(); err != nil || !e.Comment {
				t.Fatalf("trailing comment: %+v %v", e, err)
			}
		}
		if _, err := rd.Next(); err != io.EOF {
			t.Fatalf("want EOF, got %v", err)
		}
	}
}

func TestRoundTripMany(t *testing.T) {
	t.Parallel()
	const n = 10000
	rec := httptest.NewRecorder()
	w, _ := sse.NewWriter(rec)
	for i := range n {
		if err := w.Event(strconv.Itoa(i), "delta", []byte("v"+strconv.Itoa(i))); err != nil {
			t.Fatal(err)
		}
	}
	rd := sse.NewReader(rec.Body, 0)
	for i := range n {
		e, err := rd.Next()
		if err != nil || e.ID != strconv.Itoa(i) || string(e.Data) != "v"+strconv.Itoa(i) {
			t.Fatalf("#%d: %+v %v", i, e, err)
		}
	}
	if _, err := rd.Next(); err != io.EOF {
		t.Fatal(err)
	}
}
