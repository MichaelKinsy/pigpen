package wax

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
	"github.com/MichaelKinsy/pigpen/pig-music/native"
)

// Serve is `pigmusic serve` with the real engine: WaxTap resolves, WaxFlow decodes, oto plays.
func Serve(ctx context.Context, args []string, getenv func(string) string, stderr io.Writer) int {
	return native.Serve(ctx, args, getenv, stderr, Deps())
}

// Deps is the production wiring of the native daemon.
func Deps() *native.ServeDeps {
	return &native.ServeDeps{
		NewOpener: NewLazyOpener,
		NewOutput: func(kind string) (native.Output, error) {
			switch kind {
			case "null":
				return native.NewNullOutput(), nil
			case "", "auto", "oto":
				return NewOtoOutput()
			}
			return nil, fmt.Errorf("unknown output %q (use auto, oto or null)", kind)
		},
	}
}

// lazyOpener builds the WaxTap client on the first track, so that the daemon
// starts at once and offline.
type lazyOpener struct {
	once sync.Once
	op   *WaxOpener
	err  error
}

// NewLazyOpener is the production Opener.
func NewLazyOpener() (native.Opener, error) { return &lazyOpener{}, nil }

func (l *lazyOpener) Open(ctx context.Context, t music.Track) (native.Decoded, error) {
	l.once.Do(func() {
		c, err := NewWaxClient("")
		if err != nil {
			l.err = fmt.Errorf("starting the extractor: %w", native.RedactError(err))
			return
		}
		l.op = NewWaxOpener(WaxOpenerConfig{Resolver: c})
	})
	if l.err != nil {
		return nil, l.err
	}
	return l.op.Open(ctx, t)
}

// RunHelper is `pigmusic native <playlist|probe> <id>`: one JSON document on stdout (see native.Helper).
func RunHelper(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) != 2 {
		fmt.Fprintln(stderr, "usage: pigmusic native playlist <playlist id> | probe <video id>")
		return 2
	}
	switch args[0] {
	case "playlist":
		c, err := NewWaxClient("")
		if err != nil {
			native.WriteHelperReply(stdout, nil, nil, err)
			return 1
		}
		entries, err := WaxLister{Client: c}.Playlist(ctx, args[1])
		if err != nil {
			native.WriteHelperReply(stdout, nil, nil, native.Explain(err))
			return 1
		}
		native.WriteHelperReply(stdout, entries, nil, nil)
		return 0
	case "probe":
		c, err := NewWaxClient("")
		if err != nil {
			native.WriteHelperReply(stdout, nil, nil, err)
			return 1
		}
		check := Probe(ctx, ProbeOptions{Resolver: c, VideoID: args[1]})
		native.WriteHelperReply(stdout, nil, &check, nil)
		return 0
	}
	fmt.Fprintf(stderr, "unknown helper %q\n", args[0])
	return 2
}
