package native

import (
	"io"
	"sync"
	"time"
)

// NullOutput is a software device: it pulls audio at real-time speed and throws
// it away. It exercises the whole path, timing included, on a machine with no
// sound hardware (a server, a container, CI).
type NullOutput struct {
	mu      sync.Mutex
	src     io.Reader
	running bool
	gain    float64
	lead    time.Duration // how far ahead of the clock it keeps the buffer, like a device buffer
	// pulled and played count frames since the last flush; played follows the clock while running.
	pulled  int64
	played  float64
	last    time.Time
	stop    chan struct{}
	done    chan struct{}
	started bool
}

// NewNullOutput returns a paced output with a 100 ms buffer.
func NewNullOutput() *NullOutput { return &NullOutput{lead: 100 * time.Millisecond, gain: 1} }

func (n *NullOutput) Start(src io.Reader) error {
	n.src = src
	n.stop, n.done = make(chan struct{}), make(chan struct{})
	n.started = true
	go n.run()
	return nil
}

func (n *NullOutput) run() {
	defer close(n.done)
	t := time.NewTicker(10 * time.Millisecond)
	defer t.Stop()
	buf := make([]byte, 8*OutputRate/10)
	for {
		select {
		case <-n.stop:
			return
		case <-t.C:
		}
		n.mu.Lock()
		if !n.running {
			n.mu.Unlock()
			continue
		}
		now := time.Now()
		n.played += now.Sub(n.last).Seconds() * OutputRate
		n.last = now
		if n.played > float64(n.pulled) {
			n.played = float64(n.pulled) // underrun
		}
		want := int64(n.played) + int64(n.lead.Seconds()*OutputRate) - n.pulled
		n.mu.Unlock()
		for want >= 480 {
			chunk := min(int(want), len(buf)/8)
			got, _ := n.src.Read(buf[:chunk*8])
			if got == 0 {
				break
			}
			n.mu.Lock()
			if n.running {
				n.pulled += int64(got / 8)
			}
			n.mu.Unlock()
			want -= int64(got / 8)
		}
	}
}

func (n *NullOutput) Resume() {
	n.mu.Lock()
	if !n.running {
		n.running = true
		n.last = time.Now()
	}
	n.mu.Unlock()
}

func (n *NullOutput) Pause() {
	n.mu.Lock()
	if n.running {
		n.played += time.Since(n.last).Seconds() * OutputRate
		n.played = min(n.played, float64(n.pulled))
		n.running = false
	}
	n.mu.Unlock()
}

func (n *NullOutput) SetGain(g float64) { n.mu.Lock(); n.gain = g; n.mu.Unlock() }

// Gain is the last gain set (for tests and diagnostics).
func (n *NullOutput) Gain() float64 { n.mu.Lock(); defer n.mu.Unlock(); return n.gain }

func (n *NullOutput) Buffered() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	played := n.played
	if n.running {
		played += time.Since(n.last).Seconds() * OutputRate
	}
	return int(max(float64(n.pulled)-played, 0)) * 8
}

func (n *NullOutput) Flush() {
	n.mu.Lock()
	n.pulled, n.played = 0, 0
	n.last = time.Now()
	n.mu.Unlock()
}

func (n *NullOutput) Close() error {
	n.mu.Lock()
	started := n.started
	n.started = false
	n.running = false
	n.mu.Unlock()
	if started {
		close(n.stop)
		<-n.done
	}
	return nil
}
