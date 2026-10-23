package resource

import (
	"errors"
	"io"
	"strings"
	"testing"
)

type countedReader struct{ remaining, read int }

func (r *countedReader) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	n := len(p)
	if n > r.remaining {
		n = r.remaining
	}
	for i := 0; i < n; i++ {
		p[i] = 'x'
	}
	r.remaining -= n
	r.read += n
	return n, nil
}

type failingReader struct{}

func (failingReader) Read(p []byte) (int, error) { return copy(p, "abc"), io.ErrUnexpectedEOF }
func TestReadAllResourceBoundary(t *testing.T) {
	for _, n := range []int{7, 8, 9, 100} {
		r := &countedReader{remaining: n}
		b, err := ReadAll(r, 8, Request)
		if n <= 8 {
			if err != nil || len(b) != n {
				t.Fatalf("n=%d len=%d err=%v", n, len(b), err)
			}
		} else {
			var limit *LimitError
			if !errors.As(err, &limit) || b != nil || limit.Scope != Request || r.read != 9 {
				t.Fatalf("n=%d len=%d read=%d err=%v", n, len(b), r.read, err)
			}
		}
	}
	b, err := ReadAll(failingReader{}, 8, Response)
	if string(b) != "abc" || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("partial transport read %q %v", b, err)
	}
	b, err = ReadAll(io.MultiReader(strings.NewReader("1234"), strings.NewReader("56789")), 8, Response)
	var limit *LimitError
	if b != nil || !errors.As(err, &limit) || limit.Scope != Response {
		t.Fatalf("chunked %q %v", b, err)
	}
}
func TestDepthResourceBoundary(t *testing.T) {
	for _, s := range []string{`{"a":[1]}`, `{"a":"[[[\\\"{{{"}`} {
		if err := CheckDepth([]byte(s), 2, Request); err != nil {
			t.Fatalf("quoted or exact depth %s: %v", s, err)
		}
	}
	var limit *LimitError
	if err := CheckDepth([]byte(`{"a":[{}]}`), 2, Request); !errors.As(err, &limit) || limit.Kind != "json_depth" {
		t.Fatalf("depth overflow %v", err)
	}
}
func TestDefaultsAndSizeBoundary(t *testing.T) {
	if got := (Limits{}).Normalize(); got != Defaults() {
		t.Fatalf("defaults %+v", got)
	}
	if err := CheckSize(8, 8, Stream, "tool_arguments_bytes"); err != nil {
		t.Fatal(err)
	}
	var limit *LimitError
	if err := CheckSize(9, 8, Stream, "tool_arguments_bytes"); !errors.As(err, &limit) || limit.Limit != 8 {
		t.Fatalf("overflow %v", err)
	}
}
