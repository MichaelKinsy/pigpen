package wax

import (
	"context"
	"errors"
	"io"
	"math"
	"os"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
	"github.com/MichaelKinsy/pigpen/pig-music/native"
)

// TestLiveStream opens a real track through WaxTap and WaxFlow: set
// PIG_MUSIC_LIVE=1 (network, no credentials).
func TestLiveStream(t *testing.T) {
	if os.Getenv("PIG_MUSIC_LIVE") != "1" {
		t.Skip("set PIG_MUSIC_LIVE=1 to run against YouTube")
	}
	c, err := NewWaxClient(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	op := NewWaxOpener(WaxOpenerConfig{Resolver: c})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	start := time.Now()
	d, err := op.Open(ctx, music.Track{ID: "MJoSyNdffGo"})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	t.Logf("opened in %v, %d frames (%.1f s)", time.Since(start), d.Frames(), float64(d.Frames())/native.OutputRate)
	buf := make([]float32, 2*4800)
	total := 0
	peak := 0.0
	for total < 10*native.OutputRate {
		n, err := d.Read(buf)
		for _, v := range buf[:2*n] {
			peak = math.Max(peak, math.Abs(float64(v)))
		}
		total += n
		if err != nil {
			t.Fatalf("read after %d frames: %v", total, err)
		}
	}
	t.Logf("10 s decoded, peak %.3f, in %v", peak, time.Since(start))
	if peak < 0.01 {
		t.Fatal("silence")
	}
	s := time.Now()
	if err := d.SeekFrame(d.Frames() / 2); err != nil {
		t.Fatal(err)
	}
	n, err := d.Read(buf)
	t.Logf("seek to the middle + read: %d frames, %v, took %v", n, err, time.Since(s))
	if n == 0 && !errors.Is(err, io.EOF) {
		t.Fatal("nothing after seek")
	}
}

// TestLiveEngine plays real tracks through the engine and a paced software
// output, and watches the position move in real time.
func TestLiveEngine(t *testing.T) {
	if os.Getenv("PIG_MUSIC_LIVE") != "1" {
		t.Skip("set PIG_MUSIC_LIVE=1 to run against YouTube")
	}
	c, err := NewWaxClient(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var logs []string
	e, err := native.NewEngine(native.EngineConfig{Opener: NewWaxOpener(WaxOpenerConfig{Resolver: c}), Output: native.NewNullOutput(), Log: func(s string) { logs = append(logs, s) }})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	_ = e.Replace(context.Background(), []music.Track{{ID: "MJoSyNdffGo", Title: "A"}, {ID: "XFkzRNyygfk", Title: "B"}}, 0)
	for i := 0; i < 12; i++ {
		time.Sleep(time.Second)
		st := e.State()
		t.Logf("t=%2ds index=%d pos=%v dur=%v paused=%v err=%q", i+1, st.Index, st.Position.Round(10*time.Millisecond), st.Duration, st.Paused, e.LastError())
	}
	for _, l := range logs {
		t.Log("log:", l)
	}
}
