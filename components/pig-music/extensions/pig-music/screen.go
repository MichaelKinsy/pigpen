package pig_music

import (
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/pig-music/teahost"
	"github.com/MichaelKinsy/pigpen/pig-music/ui"
)

// screen is the sdk.RemoteComponent of the player: it hands PiG's input to the
// model and the model's view back, and closes when the model quits. Closing it is
// hiding: Dispose leaves the model and the player alone.
type screen struct {
	app    *app
	host   *teahost.Host
	height func() int

	mu   sync.Mutex
	stop chan struct{}
	done chan struct{}
	poll time.Duration
}

func newScreen(a *app, host *teahost.Host, height func() int) *screen {
	return &screen{app: a, host: host, height: height, poll: pollEvery}
}

func (s *screen) rows() int {
	if s.height != nil {
		if h := s.height(); h > 0 {
			return h
		}
	}
	return fallbackRows
}

// Render draws the model at the terminal's size.
func (s *screen) Render(width int) []string { return s.host.Lines(width, s.rows()) }

// HandleInput gives the model a key and closes when the model quits.
func (s *screen) HandleInput(data string) (sdk.RemoteComponentResult, error) {
	msg, ok := keyMsg(data)
	if !ok {
		return sdk.RemoteComponentResult{}, nil
	}
	if s.host.Input(msg) {
		return sdk.RemoteComponentResult{Done: true}, nil
	}
	return sdk.RemoteComponentResult{}, nil
}

// SetInvalidate receives the SDK's render callback while the screen is open. The
// model's changes (a new player state, search results) request a redraw through it,
// and a poll notices a changed terminal height, which PiG does not push.
func (s *screen) SetInvalidate(invalidate func()) {
	s.mu.Lock()
	stop, done := s.stop, s.done
	s.stop, s.done = nil, nil
	if invalidate != nil {
		s.stop, s.done = make(chan struct{}), make(chan struct{})
		go s.watch(invalidate, s.stop, s.done)
	}
	s.mu.Unlock()
	if stop != nil {
		close(stop)
		<-done
	}
	if invalidate != nil {
		s.app.setRedraw(invalidate)
		s.host.Send(ui.OpenedMsg{}) // a Library tab that cannot list anything gives way to Search
		s.host.Show()
	} else {
		s.app.setRedraw(nil)
		s.host.Send(ui.ClosedMsg{}) // the disc stops turning while nobody can see it
		s.host.Hide()
	}
}

func (s *screen) watch(invalidate func(), stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	t := time.NewTicker(s.poll)
	defer t.Stop()
	last := s.rows()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			if rows := s.rows(); rows != last {
				last = rows
				invalidate()
			}
		}
	}
}

// Dispose runs when the overlay closes. The model and the player stay.
func (s *screen) Dispose() {}
