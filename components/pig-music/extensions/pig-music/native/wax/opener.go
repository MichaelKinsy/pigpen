package wax

import (
	"context"
	"errors"
	"fmt"
	"github.com/MichaelKinsy/pigpen/pig-music/native"
	"net/http"
	"strings"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
	"github.com/colespringer/waxtap/v3"
)

// Resolver is the part of a WaxTap client the engine uses. *waxtap.Client is one.
type Resolver interface {
	Resolve(ctx context.Context, url string, sel waxtap.AudioSelector, opts ...waxtap.ReadOption) (waxtap.ResolvedStream, error)
}

// itags the engine plays, in order of preference: Opus in WebM, then AAC-LC in
// M4A. Both are served as plain ranged downloads by WaxTap's default clients.
var itagPreference = []int{251, 140}

// WaxOpenerConfig configures a WaxOpener.
type WaxOpenerConfig struct {
	Resolver Resolver
	// HTTP reads the stream; nil builds a client with sensible timeouts.
	HTTP  *http.Client
	Range RangeOptions
}

// WaxOpener is the Opener over WaxTap (resolving a stream URL) and WaxFlow
// (demuxing and decoding it). It resolves and streams from the same process and
// host, as a signed URL is bound to the address that asked for it, and it
// resolves again when a link expires.
type WaxOpener struct {
	cfg WaxOpenerConfig
}

// NewWaxOpener returns an Opener.
func NewWaxOpener(cfg WaxOpenerConfig) *WaxOpener {
	if cfg.HTTP == nil {
		cfg.HTTP = native.NewHTTPClient()
	}
	return &WaxOpener{cfg: cfg}
}

// Open implements Opener.
func (w *WaxOpener) Open(ctx context.Context, t music.Track) (native.Decoded, error) {
	if !native.ValidVideoID(t.ID) {
		return nil, native.Errorf(native.KindUnavailable, "%q is not a YouTube video ID", t.ID)
	}
	d, err := w.open(ctx, t.ID)
	if err != nil && native.Retryable(err) && ctx.Err() == nil {
		select { // one more try, after a moment: a dropped connection or a link that went stale
		case <-time.After(300 * time.Millisecond):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		d, err = w.open(ctx, t.ID)
	}
	return d, err
}

func (w *WaxOpener) open(ctx context.Context, id string) (native.Decoded, error) {
	var lastErr error
	for _, itag := range itagPreference {
		d, err := w.openItag(ctx, id, itag)
		if err == nil {
			return d, nil
		}
		lastErr = err
		if !errors.Is(err, waxtap.ErrRequestedFormatUnavailable) {
			break // another itag meets the same failure
		}
	}
	return nil, native.Explain(lastErr)
}

// resolveFor resolves one video's stream at one itag.
func resolveFor(ctx context.Context, r Resolver, id string, itag int) (RangeSource, error) {
	rs, err := r.Resolve(ctx, id, waxtap.Itag(itag))
	if err != nil {
		return RangeSource{}, err
	}
	if rs.IsSABR || rs.URL == "" {
		return RangeSource{}, fmt.Errorf("%w: the stream is only offered over SABR, which the native engine does not speak", waxtap.ErrExtractionFailed)
	}
	return RangeSource{URL: rs.URL, Size: rs.ContentLength, Header: rs.Headers, QueryRange: isGoogleVideo(rs.URL)}, nil
}

func (w *WaxOpener) openItag(ctx context.Context, id string, itag int) (native.Decoded, error) {
	resolve := func(ctx context.Context) (RangeSource, error) { return resolveFor(ctx, w.cfg.Resolver, id, itag) }
	src, err := resolve(ctx)
	if err != nil {
		return nil, err
	}
	rr, err := OpenRange(ctx, w.cfg.HTTP, src, resolve, w.cfg.Range)
	if err != nil {
		return nil, err
	}
	dec, err := OpenDecoded(rr, "")
	if err != nil {
		_ = rr.Close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, native.Explain(native.RedactError(err))
	}
	return &rangedDecoded{Decoded: dec, rr: rr}, nil
}

func isGoogleVideo(raw string) bool {
	i := strings.Index(raw, "://")
	if i < 0 {
		return false
	}
	host := raw[i+3:]
	if j := strings.IndexAny(host, "/?:"); j >= 0 {
		host = host[:j]
	}
	return strings.HasSuffix(strings.ToLower(host), "googlevideo.com")
}

// rangedDecoded closes the range reader with the decoder.
type rangedDecoded struct {
	native.Decoded
	rr *RangeReader
}

func (r *rangedDecoded) Close() error {
	_ = r.rr.Close() // first, so that a read in flight stops
	return r.Decoded.Close()
}
