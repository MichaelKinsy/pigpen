package wax

import (
	"context"
	"errors"
	"fmt"
	"github.com/MichaelKinsy/pigpen/pig-music/native"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
	"github.com/colespringer/waxtap/v3"
)

type fakeResolver struct {
	mu      sync.Mutex
	url     string
	size    int64
	calls   []string // "id:itag"
	itags   map[int]error
	token   string
	general error
}

func (f *fakeResolver) Resolve(ctx context.Context, url string, sel waxtap.AudioSelector, _ ...waxtap.ReadOption) (waxtap.ResolvedStream, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	itag := 0
	for _, i := range []int{251, 140} {
		if sel == waxtap.Itag(i) {
			itag = i
		}
	}
	f.calls = append(f.calls, fmt.Sprintf("%s:%d", url, itag))
	if f.general != nil {
		return waxtap.ResolvedStream{}, f.general
	}
	if err := f.itags[itag]; err != nil {
		return waxtap.ResolvedStream{}, err
	}
	return waxtap.ResolvedStream{URL: f.url + "?t=" + f.token, ContentLength: f.size}, nil
}

func (f *fakeResolver) callCount() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.calls) }

func opusOrigin(t *testing.T) (*fakeResolver, *origin, *httptest.Server) {
	t.Helper()
	data, err := os.ReadFile("testdata/opus-stereo.webm")
	if err != nil {
		t.Fatal(err)
	}
	o := &origin{data: data}
	o.token.Store("a")
	srv := httptest.NewServer(o)
	t.Cleanup(srv.Close)
	return &fakeResolver{url: srv.URL + "/videoplayback", size: int64(len(data)), token: "a", itags: map[int]error{}}, o, srv
}

func newWax(t *testing.T, r Resolver, srv *httptest.Server) *WaxOpener {
	t.Helper()
	w := NewWaxOpener(WaxOpenerConfig{Resolver: r, HTTP: srv.Client(), Range: RangeOptions{Backoff: 1_000_000, NoReadAhead: true}})
	return w
}

func TestWaxOpenerDecodesAStream(t *testing.T) {
	res, _, srv := opusOrigin(t)
	w := newWax(t, res, srv)
	d, err := w.Open(context.Background(), music.Track{ID: "dQw4w9WgXcQ"})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if d.Frames() < 5000 {
		t.Fatalf("frames %d", d.Frames())
	}
	pcm := drain(t, d)
	if len(pcm)/2 < 5000 {
		t.Fatalf("decoded %d frames", len(pcm)/2)
	}
	if got := res.calls[0]; !strings.HasSuffix(got, ":251") || !strings.Contains(got, "dQw4w9WgXcQ") {
		t.Fatalf("first resolve %q: want itag 251 for the video", got)
	}
}

func TestWaxOpenerFallsBackToAAC(t *testing.T) {
	res, _, srv := opusOrigin(t)
	res.itags[251] = waxtap.ErrRequestedFormatUnavailable
	w := newWax(t, res, srv)
	d, err := w.Open(context.Background(), music.Track{ID: "dQw4w9WgXcQ"})
	if err != nil {
		t.Fatal(err)
	}
	_ = d.Close()
	if len(res.calls) != 2 || !strings.HasSuffix(res.calls[1], ":140") {
		t.Fatalf("calls %v", res.calls)
	}
}

func TestWaxOpenerMapsTypedErrors(t *testing.T) {
	for _, c := range []struct {
		err  error
		kind native.Kind
	}{{waxtap.ErrNeedsPOToken, native.KindNeedsToken}, {waxtap.ErrAgeRestricted, native.KindLogin}, {waxtap.ErrVideoUnavailable, native.KindUnavailable}, {waxtap.ErrCipherSolve, native.KindBroken}} {
		res, _, srv := opusOrigin(t)
		res.general = fmt.Errorf("extract: %w", c.err)
		_, err := newWax(t, res, srv).Open(context.Background(), music.Track{ID: "dQw4w9WgXcQ"})
		var e *native.Error
		if !errors.As(err, &e) || e.Kind != c.kind {
			t.Errorf("%v: got %v", c.err, err)
		}
		if len(res.calls) != 1 {
			t.Errorf("%v: %d resolves; a failure that cannot heal must not be retried or tried with another itag", c.err, len(res.calls))
		}
	}
}

func TestWaxOpenerRetriesOnceOnANetworkFailure(t *testing.T) {
	res, _, srv := opusOrigin(t)
	n := 0
	w := NewWaxOpener(WaxOpenerConfig{Resolver: resolverFunc(func(ctx context.Context, u string, s waxtap.AudioSelector, o ...waxtap.ReadOption) (waxtap.ResolvedStream, error) {
		n++
		if n == 1 {
			return waxtap.ResolvedStream{}, errors.New("dial tcp 203.0.113.9:443: i/o timeout")
		}
		return res.Resolve(ctx, u, s, o...)
	}), HTTP: srv.Client(), Range: RangeOptions{NoReadAhead: true}})
	d, err := w.Open(context.Background(), music.Track{ID: "dQw4w9WgXcQ"})
	if err != nil {
		t.Fatalf("one dropped connection must not fail the track: %v", err)
	}
	_ = d.Close()
}

type resolverFunc func(context.Context, string, waxtap.AudioSelector, ...waxtap.ReadOption) (waxtap.ResolvedStream, error)

func (f resolverFunc) Resolve(ctx context.Context, u string, s waxtap.AudioSelector, o ...waxtap.ReadOption) (waxtap.ResolvedStream, error) {
	return f(ctx, u, s, o...)
}

func TestWaxOpenerRefusesSABRAndBadIDs(t *testing.T) {
	res, _, srv := opusOrigin(t)
	sabr := resolverFunc(func(context.Context, string, waxtap.AudioSelector, ...waxtap.ReadOption) (waxtap.ResolvedStream, error) {
		return waxtap.ResolvedStream{IsSABR: true, ContentLength: 10}, nil
	})
	if _, err := newWax(t, sabr, srv).Open(context.Background(), music.Track{ID: "dQw4w9WgXcQ"}); err == nil {
		t.Error("a SABR stream has no URL and must be an error")
	}
	// A SABR stream that does come with a URL is still not a ranged download.
	sabrURL := resolverFunc(func(context.Context, string, waxtap.AudioSelector, ...waxtap.ReadOption) (waxtap.ResolvedStream, error) {
		return waxtap.ResolvedStream{IsSABR: true, URL: res.url + "?t=a", ContentLength: res.size}, nil
	})
	var nerr *native.Error
	if _, err := newWax(t, sabrURL, srv).Open(context.Background(), music.Track{ID: "dQw4w9WgXcQ"}); !errors.As(err, &nerr) || nerr.Kind != native.KindBroken {
		t.Errorf("a SABR stream with a URL: %v, want the extractor-needs-an-update error", err)
	}
	if _, err := newWax(t, res, srv).Open(context.Background(), music.Track{ID: "x y; rm -rf"}); err == nil {
		t.Error("a malformed ID must be refused before anything is resolved")
	}
	if len(res.calls) != 0 {
		t.Error("resolved a malformed ID")
	}
}

func TestWaxOpenerRenewsAnExpiredLinkMidStream(t *testing.T) {
	res, o, srv := opusOrigin(t)
	w := newWax(t, res, srv)
	d, err := w.Open(context.Background(), music.Track{ID: "dQw4w9WgXcQ"})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	// The link expires after the open: the origin wants a new token, which a re-resolve hands out.
	o.token.Store("b")
	res.mu.Lock()
	res.token = "b"
	res.mu.Unlock()
	if err := d.SeekFrame(0); err != nil {
		t.Fatalf("seek after expiry: %v", err)
	}
	pcm := drain(t, d)
	if len(pcm) == 0 {
		t.Fatal("nothing decoded after the link was renewed")
	}
}

func TestWaxOpenerErrorsCarryNoURLs(t *testing.T) {
	res, _, srv := opusOrigin(t)
	res.general = errors.New(`Get "https://rr1.googlevideo.com/videoplayback?sig=SECRET&ip=203.0.113.9": EOF`)
	_, err := newWax(t, res, srv).Open(context.Background(), music.Track{ID: "dQw4w9WgXcQ"})
	if err == nil || strings.Contains(err.Error(), "SECRET") || strings.Contains(err.Error(), "googlevideo") || strings.Contains(err.Error(), "203.0.113.9") {
		t.Fatalf("err = %v", err)
	}
	_ = http.StatusOK
}
