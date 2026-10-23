package frontend

import (
	"io"
	"time"
)

// Tern tells a working agent from an idle one by OSC 9;4 terminal progress:
// cx.agents:list reports the state and cx.agents:wait returns once the agent
// is idle. Pi writes the same sequences only with showTerminalProgress, from
// the events that show its working indicator (tui terminal.ts setProgress);
// this member writes them while PiG reports a Working node, so Tern sees the
// state whatever that setting says. Like Pi's terminal, it repeats the active
// sequence every second while the work lasts.
const (
	progressActive    = "\x1b]9;4;3\x07"
	progressClear     = "\x1b]9;4;0\x07"
	progressKeepalive = time.Second
)

// setProgress starts or stops the terminal progress report. It runs on PiG's
// owner loop; the keepalive writes through the terminal writer, whose lock
// keeps each sequence whole between the SDK's messages.
func (s *session) setProgress(active bool) {
	if active == s.progress {
		return
	}
	s.progress = active
	if active {
		s.startKeepalive()
		return
	}
	s.stopKeepalive()
	_, _ = io.WriteString(s.out, progressClear)
}

// startKeepalive reports the progress and repeats it until stopKeepalive.
func (s *session) startKeepalive() {
	if s.progressStop != nil {
		return
	}
	_, _ = io.WriteString(s.out, progressActive)
	stop, done := make(chan struct{}), make(chan struct{})
	s.progressStop, s.progressDone = stop, done
	keepalive := s.keepalive
	go func() {
		defer close(done)
		ticker := time.NewTicker(keepalive)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				_, _ = io.WriteString(s.out, progressActive)
			}
		}
	}()
}

// stopKeepalive stops the repeats; once it returns, the keepalive writes
// nothing more.
func (s *session) stopKeepalive() {
	if s.progressStop == nil {
		return
	}
	close(s.progressStop)
	<-s.progressDone
	s.progressStop, s.progressDone = nil, nil
}
