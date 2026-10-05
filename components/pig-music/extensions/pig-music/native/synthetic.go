package native

import (
	"context"
	"io"
	"math"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// SyntheticOpener "plays" a 440 Hz tone of the track's length (30 s when the
// track states none) instead of fetching anything. `pigmusic serve --opener
// synthetic` uses it to check the audio device without a network, and the tests
// use it to run the whole daemon offline.
type SyntheticOpener struct{}

// Open implements Opener.
func (SyntheticOpener) Open(ctx context.Context, t music.Track) (Decoded, error) {
	d := t.Duration
	if d <= 0 {
		d = 30 * time.Second
	}
	return &tone{total: int64(d) * OutputRate / int64(time.Second)}, nil
}

type tone struct{ pos, total int64 }

func (t *tone) Read(p []float32) (int, error) {
	n := min(int64(len(p)/2), t.total-t.pos)
	if n <= 0 {
		return 0, io.EOF
	}
	for i := int64(0); i < n; i++ {
		v := float32(0.2 * math.Sin(2*math.Pi*440*float64(t.pos+i)/OutputRate))
		p[2*i], p[2*i+1] = v, v
	}
	t.pos += n
	return int(n), nil
}

func (t *tone) SeekFrame(f int64) error { t.pos = min(max(f, 0), t.total); return nil }
func (t *tone) Frames() int64           { return t.total }
func (t *tone) Close() error            { return nil }
