package pig_music

import (
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
	"github.com/MichaelKinsy/pigpen/pig-music/teahost"
	"github.com/MichaelKinsy/pigpen/pig-music/ui"
)

// stateCounter counts the states it is handed.
type stateCounter struct{ n *atomic.Int32 }

func (c stateCounter) Init() tea.Cmd { return nil }
func (c stateCounter) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(ui.StateMsg); ok {
		c.n.Add(1)
	}
	return c, nil
}
func (c stateCounter) View() tea.View { return tea.NewView("") }

// TestTheSettingsBoxIsHandedOnlyWhatItShowsAndOnlyWhileOpen: mpv reports the position many times a second; the box shows it
// to the second, so it is handed a state when that second, or another thing it shows, changes, and nothing once it closed.
func TestTheSettingsBoxIsHandedOnlyWhatItShowsAndOnlyWhileOpen(t *testing.T) {
	var n atomic.Int32
	host := teahost.New(stateCounter{&n}, teahost.Options{})
	host.Start(60, quickRows)
	defer host.Stop()
	track := &music.Track{ID: "a", Title: "A"}
	st := music.State{Connected: true, Track: track, Position: 10 * time.Second, Duration: time.Minute, Volume: 50, Index: 0}
	var box quickBox
	box.state(st) // no box open
	box.open(host, st)
	for _, d := range []time.Duration{100, 200, 300, 400} { // the same second
		st.Position = 10*time.Second + d*time.Millisecond
		box.state(st)
	}
	host.Peek(func(tea.Model) {})
	if got := n.Load(); got != 0 {
		t.Fatalf("handed %d states that show the same", got)
	}
	st.Position = 11 * time.Second
	box.state(st)
	st.Volume = 55
	box.state(st)
	st.Shuffle = true
	box.state(st)
	st.Paused = true
	box.state(st)
	host.Peek(func(tea.Model) {})
	if got := n.Load(); got != 4 {
		t.Fatalf("handed %d states for four changes", got)
	}
	box.close()
	st.Volume = 60
	box.state(st)
	host.Peek(func(tea.Model) {})
	if got := n.Load(); got != 4 {
		t.Fatalf("a closed box was handed a state (%d)", got)
	}
}
