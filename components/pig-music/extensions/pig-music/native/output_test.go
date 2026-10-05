package native

import (
	"io"
	"sync/atomic"
	"testing"
	"time"
)

type countSrc struct{ frames atomic.Int64 }

func (c *countSrc) Read(p []byte) (int, error) {
	c.frames.Add(int64(len(p) / 8))
	return len(p), nil
}

func TestNullOutputIsPacedInRealTime(t *testing.T) {
	n := NewNullOutput()
	src := &countSrc{}
	if err := n.Start(src); err != nil {
		t.Fatal(err)
	}
	defer n.Close()
	time.Sleep(50 * time.Millisecond)
	if src.frames.Load() != 0 {
		t.Fatal("pulled while paused")
	}
	n.Resume()
	time.Sleep(500 * time.Millisecond)
	got := float64(src.frames.Load()) / OutputRate
	// 0.5 s of play plus the 100 ms it keeps buffered.
	if got < 0.5 || got > 0.75 {
		t.Fatalf("pulled %.3f s of audio in 0.5 s of wall time", got)
	}
	if b := n.Buffered(); b < 0 || b > 8*OutputRate/5 {
		t.Fatalf("buffered %d bytes", b)
	}
	n.Pause()
	frozen := src.frames.Load()
	time.Sleep(100 * time.Millisecond)
	if src.frames.Load() != frozen {
		t.Fatal("pulled after pause")
	}
	n.Flush()
	if n.Buffered() != 0 {
		t.Fatal("flush left audio buffered")
	}
}

func TestNullOutputCloseStopsPulling(t *testing.T) {
	n := NewNullOutput()
	src := &countSrc{}
	_ = n.Start(src)
	n.Resume()
	time.Sleep(30 * time.Millisecond)
	_ = n.Close()
	_ = n.Close() // twice is fine
	c := src.frames.Load()
	time.Sleep(50 * time.Millisecond)
	if src.frames.Load() != c {
		t.Fatal("kept pulling after Close")
	}
	var _ io.Reader = src
}
