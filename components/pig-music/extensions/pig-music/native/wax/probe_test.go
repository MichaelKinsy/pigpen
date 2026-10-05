package wax

import (
	"context"
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/pig-music/native"
	"github.com/colespringer/waxtap/v3"
)

func TestProbeResolvesAndReadsARange(t *testing.T) {
	res, _, srv := opusOrigin(t)
	last := Probe(context.Background(), ProbeOptions{Resolver: res, HTTP: srv.Client(), VideoID: "dQw4w9WgXcQ"})
	if last.Name != "stream probe" || !last.OK || !strings.Contains(last.Detail, "64 KiB") {
		t.Fatalf("%+v", last)
	}
	r := native.CheckHealth(context.Background(), native.HealthOptions{
		GOOS: "darwin", Getenv: func(string) string { return "" }, ServeFound: func() error { return nil },
		Probe: func(ctx context.Context) native.Check {
			return Probe(ctx, ProbeOptions{Resolver: res, HTTP: srv.Client(), VideoID: "dQw4w9WgXcQ"})
		},
	})
	if !r.OK() {
		t.Fatalf("%s", r.String())
	}
}

func TestProbeFailureIsExplained(t *testing.T) {
	res, _, srv := opusOrigin(t)
	res.general = waxtap.ErrNeedsPOToken
	last := Probe(context.Background(), ProbeOptions{Resolver: res, HTTP: srv.Client(), VideoID: "dQw4w9WgXcQ"})
	if last.OK || !strings.Contains(last.Detail, "proof-of-origin") {
		t.Fatalf("%+v", last)
	}
}
