package websearch

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type failingReader struct {
	data []byte
	err  error
}

func (f *failingReader) Read(p []byte) (int, error) {
	if len(f.data) == 0 {
		return 0, f.err
	}
	n := copy(p, f.data)
	f.data = f.data[n:]
	return n, nil
}

func TestReadBodySizesAndLimits(t *testing.T) {
	payload := func(n int) string { return strings.Repeat("0123456789", n/10+1)[:n] }
	cases := []struct {
		name          string
		body          string
		contentLength int64
		want          string
	}{
		{"announced exactly", payload(2500), 2500, payload(2500)},
		{"announced zero but present (stub)", payload(300), 0, payload(300)},
		{"unknown length, small", payload(300), -1, payload(300)},
		{"unknown length, past the first buffer", payload(3*bodyReadHint + 17), -1, payload(3*bodyReadHint + 17)},
		{"announced shorter than sent", payload(9000), 100, payload(9000)},
		{"announced longer than sent", payload(100), 9000, payload(100)},
		{"empty", "", 0, ""},
		{"empty announced", "", 12, ""},
		{"beyond the limit is cut", payload(maxProviderBody + 1000), -1, payload(maxProviderBody)},
		{"announced beyond the limit is cut", payload(maxProviderBody + 1000), maxProviderBody + 1000, payload(maxProviderBody)},
	}
	for _, c := range cases {
		resp := &http.Response{StatusCode: 200, ContentLength: c.contentLength, Body: io.NopCloser(strings.NewReader(c.body))}
		got, err := readBody(resp)
		if err != nil || got != c.want {
			t.Errorf("%s: got %d bytes, err %v; want %d bytes", c.name, len(got), err, len(c.want))
		}
	}
}

func TestReadBodyReturnsWhatWasReadWithTheError(t *testing.T) {
	boom := errors.New("connection reset")
	resp := &http.Response{ContentLength: -1, Body: io.NopCloser(&failingReader{data: bytes.Repeat([]byte("x"), 5000), err: boom})}
	got, err := readBody(resp)
	if !errors.Is(err, boom) || len(got) != 5000 {
		t.Fatalf("got %d bytes and %v, want 5000 bytes and the read error", len(got), err)
	}
}

func TestReadBodyClosesTheBody(t *testing.T) {
	closed := false
	resp := &http.Response{Body: closeHookReader{strings.NewReader("{}"), func() { closed = true }}}
	if _, err := readBody(resp); err != nil || !closed {
		t.Fatalf("err %v, closed %v", err, closed)
	}
}

type closeHookReader struct {
	io.Reader
	onClose func()
}

func (r closeHookReader) Close() error { r.onClose(); return nil }
