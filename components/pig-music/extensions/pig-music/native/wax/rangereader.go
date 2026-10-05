package wax

import (
	"context"
	"errors"
	"fmt"
	"github.com/MichaelKinsy/pigpen/pig-music/native"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/colespringer/waxtap/v3"
)

// RangeSource is a stream that can be read in byte ranges.
type RangeSource struct {
	URL    string
	Size   int64
	Header http.Header
	// QueryRange asks for a range as a `&range=a-b` query parameter, which is
	// what googlevideo hosts expect, instead of a Range header.
	QueryRange bool
}

// RefreshFunc returns a replacement source after the origin refused the current
// link (403 or 410): the same stream, signed again.
type RefreshFunc func(ctx context.Context) (RangeSource, error)

// RangeOptions tunes a RangeReader. Zero fields take their defaults.
type RangeOptions struct {
	BlockSize      int           // bytes per request, default 256 KiB
	Ahead          int           // blocks fetched ahead of a sequential read, default 2
	MaxAhead       int           // read-ahead grows to this when the reader keeps waiting, default 8
	MaxBlocks      int           // blocks held in memory, default 64
	Retries        int           // extra attempts for a transient failure, default 3
	Backoff        time.Duration // first retry delay, doubled each time, default 250 ms
	RequestTimeout time.Duration // one request, default 20 s
	NoReadAhead    bool          // fetch only what is asked for
}

func (o *RangeOptions) fill() {
	if o.BlockSize <= 0 {
		o.BlockSize = 256 << 10
	}
	if o.NoReadAhead {
		o.Ahead, o.MaxAhead = 0, 0
	} else {
		if o.Ahead <= 0 {
			o.Ahead = 2
		}
		if o.MaxAhead < o.Ahead {
			o.MaxAhead = max(8, o.Ahead)
		}
	}
	if o.MaxBlocks <= 0 {
		o.MaxBlocks = 64
	}
	if o.MaxBlocks < o.MaxAhead+4 {
		o.MaxBlocks = o.MaxAhead + 4
	}
	if o.Retries == 0 {
		o.Retries = 3
	}
	if o.Backoff <= 0 {
		o.Backoff = 250 * time.Millisecond
	}
	if o.RequestTimeout <= 0 {
		o.RequestTimeout = 20 * time.Second
	}
}

// RangeStats is what the reader has done.
type RangeStats struct {
	Requests  int
	Refreshes int
	Cached    int
}

var (
	errRangeIgnored = errors.New("the origin ignored the byte range")
	errClosed       = errors.New("range reader closed")
)

type block struct {
	done chan struct{}
	data []byte
	err  error
}

// RangeReader is an io.ReaderAt over an HTTP resource that serves byte ranges.
// It fetches fixed blocks on demand, reads ahead of a sequential reader, keeps a
// bounded number of blocks, retries transient failures, and asks for a fresh
// link when the origin answers 403 or 410. Every error it returns has had URLs
// and addresses removed. It is safe for concurrent use.
type RangeReader struct {
	client  *http.Client
	opts    RangeOptions
	refresh RefreshFunc
	size    int64
	nblocks int64

	ctx    context.Context
	cancel context.CancelFunc
	sem    chan struct{}

	renewMu sync.Mutex

	mu        sync.Mutex // guards everything below
	src       RangeSource
	gen       int
	blocks    map[int64]*block
	ahead     int
	quick     int // consecutive reads that did not wait
	requests  int
	refreshes int
	stale     int // refreshes since a block last arrived
	closed    bool
}

// OpenRange opens src and fetches its first block, so that an origin that does
// not serve ranges is found out here and not in the middle of a decode.
func OpenRange(ctx context.Context, client *http.Client, src RangeSource, refresh RefreshFunc, opts RangeOptions) (*RangeReader, error) {
	opts.fill()
	if src.Size <= 0 {
		return nil, native.Errorf(native.KindOther, "the stream states no length, so it cannot be read in ranges")
	}
	if client == nil {
		client = http.DefaultClient
	}
	rctx, cancel := context.WithCancel(ctx)
	r := &RangeReader{
		client: client, opts: opts, refresh: refresh, src: src, size: src.Size,
		nblocks: (src.Size + int64(opts.BlockSize) - 1) / int64(opts.BlockSize),
		ctx:     rctx, cancel: cancel,
		sem:    make(chan struct{}, max(opts.MaxAhead, 1)+1),
		blocks: map[int64]*block{}, ahead: opts.Ahead,
	}
	if _, err := r.read(0); err != nil {
		cancel()
		return nil, err
	}
	return r, nil
}

// Size is the resource's length in bytes.
func (r *RangeReader) Size() int64 { return r.size }

// Stats reports what the reader has done so far.
func (r *RangeReader) Stats() RangeStats {
	r.mu.Lock()
	defer r.mu.Unlock()
	return RangeStats{Requests: r.requests, Refreshes: r.refreshes, Cached: len(r.blocks)}
}

// Close stops every fetch in flight and refuses further reads.
func (r *RangeReader) Close() error {
	r.mu.Lock()
	r.closed = true
	r.mu.Unlock()
	r.cancel()
	return nil
}

// ReadAt implements io.ReaderAt, keeping its contract: a read that reaches the
// end of the resource returns io.EOF with the bytes it got.
func (r *RangeReader) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, errors.New("negative offset")
	}
	if len(p) == 0 {
		return 0, nil
	}
	n := 0
	bs := int64(r.opts.BlockSize)
	for n < len(p) {
		pos := off + int64(n)
		if pos >= r.size {
			return n, io.EOF
		}
		idx := pos / bs
		data, err := r.read(idx)
		if err != nil {
			return n, err
		}
		n += copy(p[n:], data[pos-idx*bs:])
	}
	if off+int64(n) >= r.size {
		return n, io.EOF
	}
	return n, nil
}

// read returns block idx, waiting for it, and keeps the read-ahead going.
func (r *RangeReader) read(idx int64) ([]byte, error) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil, errClosed
	}
	b, fresh := r.ensure(idx)
	waited := fresh
	select {
	case <-b.done:
	default:
		waited = true
	}
	if r.opts.NoReadAhead {
	} else if waited {
		r.ahead = min(r.ahead*2, r.opts.MaxAhead)
		r.quick = 0
	} else if r.quick++; r.quick >= 16 {
		r.ahead = r.opts.Ahead
		r.quick = 0
	}
	ahead := r.ahead
	for j := int64(1); j <= int64(ahead) && idx+j < r.nblocks; j++ {
		r.ensure(idx + j)
	}
	r.evict(idx)
	r.mu.Unlock()

	select {
	case <-b.done:
	case <-r.ctx.Done():
		return nil, r.ctxErr()
	}
	if b.err != nil {
		r.mu.Lock()
		if r.blocks[idx] == b {
			delete(r.blocks, idx) // a later read tries again
		}
		r.mu.Unlock()
		return nil, b.err
	}
	return b.data, nil
}

func (r *RangeReader) ctxErr() error {
	r.mu.Lock()
	closed := r.closed
	r.mu.Unlock()
	if closed {
		return errClosed
	}
	return r.ctx.Err()
}

// ensure returns block idx, starting its fetch when it is not held. The caller holds r.mu.
func (r *RangeReader) ensure(idx int64) (*block, bool) {
	if b, ok := r.blocks[idx]; ok {
		return b, false
	}
	b := &block{done: make(chan struct{})}
	r.blocks[idx] = b
	go func() {
		defer close(b.done)
		select {
		case r.sem <- struct{}{}:
			defer func() { <-r.sem }()
		case <-r.ctx.Done():
			b.err = r.ctxErr()
			return
		}
		b.data, b.err = r.fetch(idx)
	}()
	return b, true
}

// evict drops finished blocks beyond the cap: those behind the reader first,
// then the farthest ahead. The caller holds r.mu.
func (r *RangeReader) evict(cur int64) {
	for len(r.blocks) > r.opts.MaxBlocks {
		victim, worst := int64(-1), int64(-1)
		for i, b := range r.blocks {
			select {
			case <-b.done:
			default:
				continue
			}
			d := i - cur
			if d < 0 {
				d = -d + 1<<20 // behind the reader goes first
			}
			if d > worst {
				victim, worst = i, d
			}
		}
		if victim < 0 {
			return
		}
		delete(r.blocks, victim)
	}
}

// fetch reads block idx, retrying a transient failure and renewing an expired link.
func (r *RangeReader) fetch(idx int64) ([]byte, error) {
	start := idx * int64(r.opts.BlockSize)
	end := min(start+int64(r.opts.BlockSize), r.size) - 1
	delay := r.opts.Backoff
	for attempt := 0; ; attempt++ {
		if err := r.ctx.Err(); err != nil {
			return nil, r.ctxErr()
		}
		r.mu.Lock()
		src, gen := r.src, r.gen
		r.requests++
		r.mu.Unlock()
		data, status, err := r.get(src, start, end)
		if err == nil {
			r.mu.Lock()
			r.stale = 0
			r.mu.Unlock()
			return data, nil
		}
		switch {
		case status == http.StatusForbidden || status == http.StatusGone:
			if rerr := r.renew(gen); rerr != nil {
				return nil, rerr
			}
			attempt-- // a renewal does not spend a retry
			continue
		case status == http.StatusTooManyRequests:
			return nil, fmt.Errorf("%w: the origin answered 429", waxtap.ErrRateLimited)
		case errors.Is(err, errRangeIgnored):
			return nil, native.Errorf(native.KindOther, "the origin does not serve byte ranges for this stream")
		case r.ctx.Err() != nil:
			return nil, r.ctxErr()
		case attempt >= r.opts.Retries:
			return nil, err
		}
		select {
		case <-time.After(delay):
		case <-r.ctx.Done():
			return nil, r.ctxErr()
		}
		delay = min(delay*2, 2*time.Second)
	}
}

// renew replaces the link after a 403 or 410 on generation gen. Calls that find
// the link already replaced return at once and retry with the new one.
func (r *RangeReader) renew(gen int) error {
	r.renewMu.Lock() // one renewal at a time; r.mu is not held while resolving
	defer r.renewMu.Unlock()
	r.mu.Lock()
	if r.gen != gen {
		r.mu.Unlock()
		return nil
	}
	if r.refresh == nil {
		r.mu.Unlock()
		return fmt.Errorf("%w: the origin refused the stream link", waxtap.ErrURLExpired)
	}
	if r.stale >= 2 || r.refreshes >= 8 {
		r.mu.Unlock()
		return fmt.Errorf("%w: a fresh link was refused as well", waxtap.ErrURLExpired)
	}
	r.stale++
	r.refreshes++
	r.mu.Unlock()
	ctx, cancel := context.WithTimeout(r.ctx, 30*time.Second)
	defer cancel()
	src, err := r.refresh(ctx)
	if err != nil {
		return fmt.Errorf("%w: renewing the link failed: %w", waxtap.ErrURLExpired, native.RedactError(err))
	}
	if src.Size > 0 && src.Size != r.size {
		return fmt.Errorf("%w: the renewed stream has a different length", waxtap.ErrURLExpired)
	}
	src.Size = r.size
	r.mu.Lock()
	r.src = src
	r.gen++
	r.mu.Unlock()
	return nil
}

// get performs one ranged request. status is the HTTP status when there was a response.
func (r *RangeReader) get(src RangeSource, start, end int64) (data []byte, status int, err error) {
	want := end - start + 1
	rawURL := src.URL
	if src.QueryRange {
		u, perr := url.Parse(src.URL)
		if perr != nil {
			return nil, 0, native.Errorf(native.KindOther, "the stream link is not a URL")
		}
		q := u.Query()
		q.Set("range", strconv.FormatInt(start, 10)+"-"+strconv.FormatInt(end, 10))
		u.RawQuery = q.Encode()
		rawURL = u.String()
	}
	ctx, cancel := context.WithTimeout(r.ctx, r.opts.RequestTimeout)
	defer cancel()
	req, rerr := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if rerr != nil {
		return nil, 0, native.Errorf(native.KindOther, "the stream link is not a URL")
	}
	for k, v := range src.Header {
		req.Header[k] = v
	}
	if !src.QueryRange {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
	}
	resp, derr := r.client.Do(req)
	if derr != nil {
		return nil, 0, native.Explain(native.RedactError(derr))
	}
	defer resp.Body.Close()
	status = resp.StatusCode
	switch {
	case status == http.StatusPartialContent:
	case status == http.StatusOK && (src.QueryRange || want == r.size):
	case status == http.StatusOK:
		return nil, status, errRangeIgnored
	default:
		return nil, status, native.Explain(fmt.Errorf("the origin answered HTTP %d", status))
	}
	buf := make([]byte, want+1)
	n, rerr2 := io.ReadFull(resp.Body, buf)
	switch {
	case int64(n) > want:
		return nil, status, errRangeIgnored
	case int64(n) == want:
		return buf[:want], status, nil
	}
	if rerr2 == nil || errors.Is(rerr2, io.EOF) || errors.Is(rerr2, io.ErrUnexpectedEOF) {
		return nil, status, native.Explain(fmt.Errorf("%w: a block came up short (%d of %d bytes)", waxtap.ErrIncompleteStream, n, want))
	}
	return nil, status, native.Explain(native.RedactError(rerr2))
}
