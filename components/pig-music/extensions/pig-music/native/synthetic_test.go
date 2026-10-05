package native

import (
	"context"
	"errors"
	"io"
	"math"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

func TestSyntheticOpenerPlaysATone(t *testing.T) {
	o := SyntheticOpener{}
	d, err := o.Open(context.Background(), music.Track{ID: "t1", Duration: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if d.Frames() != 2*OutputRate {
		t.Fatalf("frames %d", d.Frames())
	}
	pcm := drain(t, d)
	if len(pcm) != 2*2*OutputRate {
		t.Fatalf("%d samples", len(pcm))
	}
	// 440 Hz: about 880 zero crossings per second on the left channel.
	crossings := 0
	for i := 2; i < 2*OutputRate; i += 2 {
		if (pcm[i-2] < 0) != (pcm[i] < 0) {
			crossings++
		}
	}
	if crossings < 870 || crossings > 890 {
		t.Fatalf("%d zero crossings in the first second, want about 880", crossings)
	}
	peak := 0.0
	for _, v := range pcm {
		peak = math.Max(peak, math.Abs(float64(v)))
	}
	if peak < 0.05 || peak > 0.5 {
		t.Fatalf("peak %v: a test tone must be audible but not loud", peak)
	}
	if err := d.SeekFrame(OutputRate); err != nil {
		t.Fatal(err)
	}
	if n, err := d.Read(make([]float32, 20)); n != 10 || !errors.Is(err, nil) {
		t.Fatalf("read after seek: %d %v", n, err)
	}
	// Default length.
	d2, _ := o.Open(context.Background(), music.Track{ID: "x"})
	if d2.Frames() != 30*OutputRate {
		t.Fatalf("default length %d", d2.Frames())
	}
	_ = io.EOF
}
