package wax

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/native"
)

// Probe resolves one video and reads 64 KiB of its stream: the whole path
// from extraction to bytes, with the time it took.
// ProbeOptions asks the health check to resolve one video and read 64 KiB of it (this uses the network).
type ProbeOptions struct {
	Resolver Resolver
	HTTP     *http.Client
	VideoID  string
}

// Probe is the stream probe check.
func Probe(ctx context.Context, p ProbeOptions) native.Check {
	c := native.Check{Name: "stream probe"}
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	client := p.HTTP
	if client == nil {
		client = native.NewHTTPClient()
	}
	start := time.Now()
	src, err := resolveFor(ctx, p.Resolver, p.VideoID, itagPreference[0])
	if err != nil {
		c.Detail = native.Explain(err).Error()
		c.Fix = "try again in a minute; if it keeps failing, use the mpv engine (engine = mpv) and update pig-music"
		return c
	}
	took := time.Since(start)
	rr, err := OpenRange(ctx, client, src, nil, RangeOptions{BlockSize: 64 << 10, NoReadAhead: true, Retries: 1})
	if err != nil {
		c.Detail = native.Explain(err).Error()
		return c
	}
	defer rr.Close()
	n, err := rr.ReadAt(make([]byte, 64<<10), 0)
	if err != nil && err != io.EOF {
		c.Detail = native.Explain(err).Error()
		return c
	}
	c.OK = true
	c.Detail = fmt.Sprintf("resolved in %d ms, read %d bytes (64 KiB range)", took.Milliseconds(), n)
	if n == 64<<10 {
		c.Detail = fmt.Sprintf("resolved in %d ms, read 64 KiB by range", took.Milliseconds())
	}
	return c
}
