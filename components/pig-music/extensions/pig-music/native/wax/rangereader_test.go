package wax

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/colespringer/waxtap/v3"
)

func payload(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*7 + i>>8)
	}
	return b
}

// origin serves data by a `range=a-b` query (googlevideo style) or a Range header.
type origin struct {
	data      []byte
	query     bool
	reqs      atomic.Int64
	fail      func(n int64, r *http.Request) int // status to answer instead, 0 = serve
	ignore    bool                               // ignore ranges and send everything
	token     atomic.Value                       // required value of ?t=, "" = any
	maxActive atomic.Int64
	active    atomic.Int64
	delay     time.Duration
}

func (o *origin) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	n := o.reqs.Add(1)
	a := o.active.Add(1)
	defer o.active.Add(-1)
	for {
		m := o.maxActive.Load()
		if a <= m || o.maxActive.CompareAndSwap(m, a) {
			break
		}
	}
	if o.delay > 0 {
		time.Sleep(o.delay)
	}
	if want, _ := o.token.Load().(string); want != "" && r.URL.Query().Get("t") != want {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	if o.fail != nil {
		if code := o.fail(n, r); code != 0 {
			w.WriteHeader(code)
			return
		}
	}
	if o.ignore {
		_, _ = w.Write(o.data)
		return
	}
	var start, end int64
	if o.query {
		fmt.Sscanf(r.URL.Query().Get("range"), "%d-%d", &start, &end)
		w.WriteHeader(http.StatusOK)
	} else {
		fmt.Sscanf(strings.TrimPrefix(r.Header.Get("Range"), "bytes="), "%d-%d", &start, &end)
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(o.data)))
		w.WriteHeader(http.StatusPartialContent)
	}
	end = min(end, int64(len(o.data))-1)
	_, _ = w.Write(o.data[start : end+1])
}

func open(t *testing.T, o *origin, srv *httptest.Server, refresh RefreshFunc, opts RangeOptions) *RangeReader {
	t.Helper()
	src := RangeSource{URL: srv.URL + "/v?t=a", Size: int64(len(o.data)), QueryRange: o.query}
	r, err := OpenRange(context.Background(), srv.Client(), src, refresh, opts)
	if err != nil {
		t.Fatalf("OpenRange: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

func TestRangeReaderReadsAnywhere(t *testing.T) {
	for _, query := range []bool{true, false} {
		t.Run(fmt.Sprint("query=", query), func(t *testing.T) {
			o := &origin{data: payload(1_000_003), query: query}
			srv := httptest.NewServer(o)
			defer srv.Close()
			r := open(t, o, srv, nil, RangeOptions{BlockSize: 64 << 10})
			if r.Size() != int64(len(o.data)) {
				t.Fatalf("size %d", r.Size())
			}
			for _, c := range []struct{ off, n int }{{0, 10}, {65530, 20}, {500_000, 200_000}, {1_000_000, 3}, {10, 300_000}} {
				buf := make([]byte, c.n)
				n, err := r.ReadAt(buf, int64(c.off))
				if n != c.n || (err != nil && err != io.EOF) {
					t.Fatalf("ReadAt(%d,%d) = %d, %v", c.off, c.n, n, err)
				}
				if !bytes.Equal(buf, o.data[c.off:c.off+c.n]) {
					t.Fatalf("ReadAt(%d,%d): wrong bytes", c.off, c.n)
				}
			}
			// Past the end.
			n, err := r.ReadAt(make([]byte, 10), int64(len(o.data)-4))
			if n != 4 || err != io.EOF {
				t.Fatalf("tail read = %d, %v", n, err)
			}
			if n, err := r.ReadAt(make([]byte, 1), int64(len(o.data))); n != 0 || err != io.EOF {
				t.Fatalf("read at the end = %d, %v", n, err)
			}
		})
	}
}

func TestRangeReaderReadsAheadSequentially(t *testing.T) {
	o := &origin{data: payload(2 << 20), query: true, delay: 20 * time.Millisecond}
	srv := httptest.NewServer(o)
	defer srv.Close()
	r := open(t, o, srv, nil, RangeOptions{BlockSize: 64 << 10, Ahead: 3})
	buf := make([]byte, 4096)
	start := time.Now()
	for off := int64(0); off < 1<<20; off += int64(len(buf)) {
		if _, err := r.ReadAt(buf, off); err != nil {
			t.Fatal(err)
		}
	}
	// 16 blocks at 20 ms each, one after the other, would be 320 ms; read-ahead overlaps them.
	if d := time.Since(start); d > 250*time.Millisecond {
		t.Errorf("sequential read took %v: no read-ahead", d)
	}
	if o.maxActive.Load() < 2 {
		t.Errorf("requests never overlapped (max %d)", o.maxActive.Load())
	}
}

func TestRangeReaderBoundsMemory(t *testing.T) {
	o := &origin{data: payload(4 << 20), query: true}
	srv := httptest.NewServer(o)
	defer srv.Close()
	r := open(t, o, srv, nil, RangeOptions{BlockSize: 64 << 10, MaxBlocks: 12})
	buf := make([]byte, 64<<10)
	for off := int64(0); off < int64(len(o.data)); off += int64(len(buf)) {
		if _, err := r.ReadAt(buf, off); err != nil && err != io.EOF {
			t.Fatal(err)
		}
	}
	if n := r.Stats().Cached; n > 12 {
		t.Fatalf("%d blocks cached, limit 12", n)
	}
}

func TestRangeReaderRefreshesOnForbidden(t *testing.T) {
	o := &origin{data: payload(300_000), query: true}
	o.token.Store("a")
	srv := httptest.NewServer(o)
	defer srv.Close()
	var refreshes atomic.Int64
	refresh := func(ctx context.Context) (RangeSource, error) {
		refreshes.Add(1)
		o.token.Store("b")
		return RangeSource{URL: srv.URL + "/v?t=b", Size: int64(len(o.data)), QueryRange: true}, nil
	}
	r := open(t, o, srv, refresh, RangeOptions{NoReadAhead: true, BlockSize: 64 << 10})
	o.token.Store("b") // the first link expires after the open
	buf := make([]byte, 1000)
	if _, err := r.ReadAt(buf, 200_000); err != nil {
		t.Fatalf("read after expiry: %v", err)
	}
	if !bytes.Equal(buf, o.data[200_000:201_000]) {
		t.Fatal("wrong bytes after refresh")
	}
	if refreshes.Load() != 1 {
		t.Fatalf("%d refreshes, want 1", refreshes.Load())
	}
	if r.Stats().Refreshes != 1 {
		t.Fatalf("stats say %d refreshes", r.Stats().Refreshes)
	}
}

func TestRangeReaderGivesUpWhenRefreshDoesNotHelp(t *testing.T) {
	o := &origin{data: payload(300_000), query: true, fail: func(n int64, _ *http.Request) int {
		if n > 1 {
			return http.StatusForbidden
		}
		return 0
	}}
	srv := httptest.NewServer(o)
	defer srv.Close()
	var refreshes atomic.Int64
	refresh := func(ctx context.Context) (RangeSource, error) {
		refreshes.Add(1)
		return RangeSource{URL: srv.URL + "/v?t=a", Size: int64(len(o.data)), QueryRange: true}, nil
	}
	r := open(t, o, srv, refresh, RangeOptions{NoReadAhead: true, BlockSize: 64 << 10})
	_, err := r.ReadAt(make([]byte, 10), 200_000)
	if !errors.Is(err, waxtap.ErrURLExpired) {
		t.Fatalf("err = %v, want ErrURLExpired", err)
	}
	if got := refreshes.Load(); got < 1 || got > 3 {
		t.Fatalf("%d refreshes: must try, and stop within 3", got)
	}
}

func TestRangeReaderWithoutRefreshReportsExpiry(t *testing.T) {
	o := &origin{data: payload(100_000), query: true}
	o.token.Store("a")
	srv := httptest.NewServer(o)
	defer srv.Close()
	r := open(t, o, srv, nil, RangeOptions{NoReadAhead: true, BlockSize: 64 << 10})
	o.token.Store("zzz")
	if _, err := r.ReadAt(make([]byte, 10), 90_000); !errors.Is(err, waxtap.ErrURLExpired) {
		t.Fatalf("err = %v", err)
	}
}

func TestRangeReaderRetriesServerErrors(t *testing.T) {
	var fails atomic.Int64
	o := &origin{data: payload(200_000), query: true}
	o.fail = func(n int64, _ *http.Request) int {
		if n > 1 && fails.Add(1) <= 2 {
			return http.StatusBadGateway
		}
		return 0
	}
	srv := httptest.NewServer(o)
	defer srv.Close()
	r := open(t, o, srv, nil, RangeOptions{NoReadAhead: true, BlockSize: 64 << 10, Backoff: time.Millisecond})
	buf := make([]byte, 100)
	if _, err := r.ReadAt(buf, 150_000); err != nil {
		t.Fatalf("a transient 502 must be retried: %v", err)
	}
}

func TestRangeReaderRateLimited(t *testing.T) {
	o := &origin{data: payload(200_000), query: true}
	o.fail = func(n int64, _ *http.Request) int {
		if n > 1 {
			return http.StatusTooManyRequests
		}
		return 0
	}
	srv := httptest.NewServer(o)
	defer srv.Close()
	r := open(t, o, srv, nil, RangeOptions{NoReadAhead: true, BlockSize: 64 << 10, Backoff: time.Millisecond})
	if _, err := r.ReadAt(make([]byte, 10), 150_000); !errors.Is(err, waxtap.ErrRateLimited) {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenRangeRejectsAnOriginThatIgnoresRanges(t *testing.T) {
	o := &origin{data: payload(300_000), ignore: true}
	srv := httptest.NewServer(o)
	defer srv.Close()
	src := RangeSource{URL: srv.URL + "/v", Size: int64(len(o.data))}
	_, err := OpenRange(context.Background(), srv.Client(), src, nil, RangeOptions{BlockSize: 64 << 10})
	if err == nil || !strings.Contains(err.Error(), "range") {
		t.Fatalf("err = %v, want a range complaint", err)
	}
}

func TestRangeReaderErrorsCarryNoURL(t *testing.T) {
	o := &origin{data: payload(100_000), query: true}
	srv := httptest.NewServer(o)
	r := open(t, o, srv, nil, RangeOptions{NoReadAhead: true, BlockSize: 64 << 10, Backoff: time.Millisecond, Retries: 1})
	srv.Close() // the origin goes away
	_, err := r.ReadAt(make([]byte, 10), 90_000)
	if err == nil {
		t.Fatal("expected an error")
	}
	if s := err.Error(); strings.Contains(s, "127.0.0.1") || strings.Contains(s, "http://") || strings.Contains(s, "t=a") {
		t.Fatalf("error leaks the URL or address: %s", s)
	}
}

func TestRangeReaderCloseStopsFetching(t *testing.T) {
	o := &origin{data: payload(2 << 20), query: true, delay: 30 * time.Millisecond}
	srv := httptest.NewServer(o)
	defer srv.Close()
	r := open(t, o, srv, nil, RangeOptions{BlockSize: 64 << 10, Ahead: 4})
	_, _ = r.ReadAt(make([]byte, 10), 0)
	_ = r.Close()
	time.Sleep(100 * time.Millisecond)
	n := o.reqs.Load()
	time.Sleep(150 * time.Millisecond)
	if o.reqs.Load() != n {
		t.Fatal("requests continued after Close")
	}
	if _, err := r.ReadAt(make([]byte, 10), 0); err == nil {
		t.Fatal("read after Close must fail")
	}
}

func TestRangeReaderConcurrentReads(t *testing.T) {
	o := &origin{data: payload(1 << 20), query: true}
	srv := httptest.NewServer(o)
	defer srv.Close()
	r := open(t, o, srv, nil, RangeOptions{BlockSize: 32 << 10})
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				off := (g*131_071 + i*40_009) % (len(o.data) - 5000)
				buf := make([]byte, 5000)
				if _, err := r.ReadAt(buf, int64(off)); err != nil {
					t.Error(err)
					return
				}
				if !bytes.Equal(buf, o.data[off:off+5000]) {
					t.Errorf("bad bytes at %d", off)
					return
				}
			}
		}(g)
	}
	wg.Wait()
	_ = strconv.Itoa
}
