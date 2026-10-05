package pig_snake

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-snake/internal/snake"
	"github.com/MichaelKinsy/pigpen/pig-snake/internal/sprites"
)

// Twin of pigrunner TestExtensionRegistersRunnerCommandsOnly (PiG d86eb93):
// the wire registration is exactly the two game commands, nothing else.
func TestExtensionRegistersSnakeCommandsOnly(t *testing.T) {
	extensionConn, hostConn := net.Pipe()
	defer func() { _ = hostConn.Close() }()
	done := make(chan error, 1)
	go func() { done <- Extension().RunWithConn(extensionConn) }()

	var header [4]byte
	if _, err := io.ReadFull(hostConn, header[:]); err != nil {
		t.Fatal(err)
	}
	frame := make([]byte, binary.BigEndian.Uint32(header[:]))
	if _, err := io.ReadFull(hostConn, frame); err != nil {
		t.Fatal(err)
	}
	var registration struct {
		Register struct {
			Name     string `json:"name"`
			Commands []struct {
				Name string `json:"name"`
			} `json:"commands"`
			Handlers []json.RawMessage `json:"handlers"`
			Tools    []json.RawMessage `json:"tools"`
			Flags    []json.RawMessage `json:"flags"`
		} `json:"register"`
	}
	if err := json.Unmarshal(frame, &registration); err != nil {
		t.Fatal(err)
	}
	reg := registration.Register
	if reg.Name != "pig-snake" {
		t.Fatalf("extension name = %q", reg.Name)
	}
	if len(reg.Commands) != 2 || reg.Commands[0].Name != "pig-snake" || reg.Commands[1].Name != "snake" {
		t.Fatalf("registered commands = %+v", reg.Commands)
	}
	if len(reg.Handlers)+len(reg.Tools)+len(reg.Flags) != 0 {
		t.Fatalf("bundling is not activating: extra registrations %s", frame)
	}
	writeFrame := func(value any) {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		binary.BigEndian.PutUint32(header[:], uint32(len(data)))
		if _, err := hostConn.Write(header[:]); err != nil {
			t.Fatal(err)
		}
		if _, err := hostConn.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	writeFrame(map[string]any{"type": "ready", "ready": map[string]any{"width": 80}})
	writeFrame(map[string]any{"type": "shutdown", "shutdown": map[string]any{"reason": "test"}})
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("extension did not stop after shutdown")
	}
}

// fixedRand keeps food placement deterministic.
type fixedRand struct{}

func (fixedRand) Intn(int) int { return 0 }

func height(h int) func() int { return func() int { return h } }

// manual builds a component with no timer goroutine, driven by advance.
func manual(t *testing.T, highs highScores, h func() int) *component {
	t.Helper()
	c := newComponentWith(highs, sprites.Builtin(""), h, options{manual: true, rng: fixedRand{}})
	t.Cleanup(c.Dispose)
	return c
}

func (c *component) snapshot() (snake.Status, snake.Point, snake.Dir, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.game.Status, c.game.Body[0], c.game.Dir, len(c.game.Body)
}

func TestSpaceOrEnterStartsAndAnyDirectionStartsAndSteers(t *testing.T) {
	for input, want := range map[string]snake.Dir{" ": snake.Right, "\r": snake.Right, "\x1b[B": snake.Down, "w": snake.Up, "a": snake.Left} {
		c := manual(t, highScores{}, height(24))
		_, _ = c.HandleInput(input)
		status, _, dir, _ := c.snapshot()
		if status != snake.Playing || dir != want {
			t.Errorf("%q: status %v dir %v, want Playing %v", input, status, dir, want)
		}
	}
}

func TestSteeringKeysArrowsWasdAndVi(t *testing.T) {
	keys := map[string]snake.Dir{
		"\x1b[A": snake.Up, "\x1bOB": snake.Down, "\x1b[C": snake.Right, "\x1b[D": snake.Left,
		"w": snake.Up, "W": snake.Up, "s": snake.Down, "a": snake.Left, "d": snake.Right,
		"k": snake.Up, "j": snake.Down, "h": snake.Left, "l": snake.Right,
		"\x1b[119u": snake.Up, // kitty encoding of w
	}
	for input, want := range keys {
		c := manual(t, highScores{}, height(24))
		_, _ = c.HandleInput(" ")
		_, _ = c.HandleInput(input)
		c.advance(time.Second) // one step: the queued turn applies
		_, _, dir, _ := c.snapshot()
		if dir != want {
			t.Errorf("%q steers %v, want %v", input, dir, want)
		}
	}
}

func TestKeyReleasesAndOtherKeysDoNothing(t *testing.T) {
	c := manual(t, highScores{}, height(24))
	_, _ = c.HandleInput(" ")
	for _, in := range []string{"\x1b[1;1:3A", "x", "\x03", "\x1b[113;5u", ""} {
		res, err := c.HandleInput(in)
		if err != nil || res.Done {
			t.Fatalf("%q: %+v %v", in, res, err)
		}
	}
	if _, _, dir, _ := c.snapshot(); dir != snake.Right {
		t.Fatalf("dir %v", dir)
	}
}

// Twin of pigrunner TestRunnerComponentControlsAndQuit.
func TestSnakeComponentControlsAndQuit(t *testing.T) {
	component := manual(t, highScores{High: 11}, height(24))
	_, _ = component.HandleInput(" ")

	_, _ = component.HandleInput("p")
	if status, _, _, _ := component.snapshot(); status != snake.Paused {
		t.Fatal("p did not pause the game")
	}
	_, _ = component.HandleInput("p")
	if status, _, _, _ := component.snapshot(); status != snake.Playing {
		t.Fatal("second p did not resume")
	}
	_, _ = component.HandleInput("\x1b[A")
	component.advance(time.Second)
	if _, _, dir, _ := component.snapshot(); dir != snake.Up {
		t.Fatalf("up arrow did not steer up: dir=%v", dir)
	}

	result, err := component.HandleInput("q")
	if err != nil || !result.Done {
		t.Fatalf("quit result = %+v, err=%v", result, err)
	}
	state, ok := result.Value.(gameState)
	if !ok || state.HighScore < 11 {
		t.Fatalf("quit state = %#v", result.Value)
	}
}

func TestEscapeAndQuitLeaveFromEveryState(t *testing.T) {
	for _, input := range []string{"q", "Q", "\x1b", "\x1b[27u"} {
		for _, prep := range []func(*component){
			func(*component) {},
			func(c *component) { _, _ = c.HandleInput(" ") },
			func(c *component) { _, _ = c.HandleInput(" "); _, _ = c.HandleInput("p") },
			func(c *component) { c.mu.Lock(); c.game.Status = snake.Over; c.mu.Unlock() },
		} {
			c := manual(t, highScores{}, height(24))
			prep(c)
			if res, _ := c.HandleInput(input); !res.Done {
				t.Errorf("%q did not quit", input)
			}
		}
	}
}

func TestPauseFreezesTheHerd(t *testing.T) {
	c := manual(t, highScores{}, height(24))
	_, _ = c.HandleInput(" ")
	_, _ = c.HandleInput("p")
	_, before, _, _ := c.snapshot()
	if c.advance(5 * time.Second) {
		t.Fatal("a paused game reported change")
	}
	if _, after, _, _ := c.snapshot(); after != before {
		t.Fatalf("paused leader moved %v -> %v", before, after)
	}
	// While paused, steering keys do not resume or steer.
	_, _ = c.HandleInput("\x1b[A")
	_, _ = c.HandleInput("p")
	c.advance(time.Second)
	if _, _, dir, _ := c.snapshot(); dir != snake.Right {
		t.Fatalf("a key pressed while paused steered: %v", dir)
	}
}

func TestAdvanceStepsOncePerIntervalAndEatsForwardFood(t *testing.T) {
	c := manual(t, highScores{}, height(24))
	_, _ = c.HandleInput(" ")
	c.mu.Lock()
	start := c.game.Body[0]
	c.game.Food = snake.Point{X: start.X + 2, Y: start.Y}
	interval := c.game.Interval()
	c.mu.Unlock()
	if c.advance(interval / 2) {
		t.Fatal("half an interval must not step")
	}
	if !c.advance(interval/2 + interval) { // total 2 intervals
		t.Fatal("two intervals must step")
	}
	status, head, _, herd := c.snapshot()
	if status != snake.Playing || head != (snake.Point{X: start.X + 2, Y: start.Y}) || herd != 2 {
		t.Fatalf("status %v head %v herd %d", status, head, herd)
	}
	if st := c.State(); st.Score != 1 || st.Herd != 2 || st.HighScore != 1 {
		t.Fatalf("state %+v", st)
	}
}

func TestAStalledClockDoesNotFastForwardTheGame(t *testing.T) {
	c := manual(t, highScores{}, height(24))
	_, _ = c.HandleInput(" ")
	_, start, _, _ := c.snapshot()
	c.advance(time.Hour)
	_, head, _, _ := c.snapshot()
	moved := head.X - start.X
	if moved > 4 {
		t.Fatalf("an hour of stall moved the leader %d cells; catch-up must be bounded", moved)
	}
}

func TestGameOverStopsStepsAndOnlyRetryAndQuitWork(t *testing.T) {
	c := manual(t, highScores{High: 3}, height(24))
	_, _ = c.HandleInput(" ")
	for range 200 {
		c.advance(time.Second)
	}
	status, _, _, _ := c.snapshot()
	if status != snake.Over {
		t.Fatalf("walking into the wall should end the game, status %v", status)
	}
	_, head, _, _ := c.snapshot()
	_, _ = c.HandleInput("\x1b[A")
	_, _ = c.HandleInput("p")
	c.advance(time.Second)
	if s, h, _, _ := c.snapshot(); s != snake.Over || h != head {
		t.Fatalf("game over changed: %v %v", s, h)
	}
	if st := c.State(); !st.GameOver || st.HighScore != 3 {
		t.Fatalf("state %+v", st)
	}
}

// Twin of pigrunner TestRunnerRestartKeepsHighScoreAndSprite.
func TestSnakeRestartKeepsHighScoreModeAndSprite(t *testing.T) {
	src := sprites.Builtin("sheriff")
	component := newComponentWith(highScores{High: 5, WrapHigh: 8}, src, height(24), options{manual: true, rng: fixedRand{}})
	defer component.Dispose()
	_, _ = component.HandleInput("m") // wrap mode while waiting
	_, _ = component.HandleInput(" ")
	component.mu.Lock()
	component.game.Status, component.game.High = snake.Over, 90
	component.mu.Unlock()
	_, _ = component.HandleInput("r")
	component.mu.Lock()
	defer component.mu.Unlock()
	if component.game.Status != snake.Playing || component.game.High != 90 || component.game.Mode != snake.Wrap || component.src != src {
		t.Fatalf("restart = %v, high %d, mode %v, sprite kept %v", component.game.Status, component.game.High, component.game.Mode, component.src == src)
	}
}

func TestRetryIsIgnoredWhileAGameIsRunning(t *testing.T) {
	c := manual(t, highScores{}, height(24))
	_, _ = c.HandleInput(" ")
	c.advance(time.Second)
	_, head, _, _ := c.snapshot()
	_, _ = c.HandleInput("r")
	if _, after, _, _ := c.snapshot(); after != head {
		t.Fatal("r restarted a live game")
	}
}

func TestModeKeyTogglesWallsAndWrapBeforeStartAndSwapsTheHighScore(t *testing.T) {
	c := manual(t, highScores{High: 5, WrapHigh: 9}, height(24))
	if st := c.State(); st.Wrap || st.HighScore != 5 {
		t.Fatalf("initial %+v", st)
	}
	_, _ = c.HandleInput("m")
	c.mu.Lock()
	mode, high := c.game.Mode, c.game.High
	c.mu.Unlock()
	if mode != snake.Wrap || high != 9 {
		t.Fatalf("after m: mode %v high %d", mode, high)
	}
	_, _ = c.HandleInput("M")
	c.mu.Lock()
	mode, high = c.game.Mode, c.game.High
	c.mu.Unlock()
	if mode != snake.Walls || high != 5 {
		t.Fatalf("after M: mode %v high %d", mode, high)
	}
	_, _ = c.HandleInput("m")
	_, _ = c.HandleInput(" ")
	_, _ = c.HandleInput("m") // ignored once running
	if st := c.State(); !st.Wrap {
		t.Fatal("mode changed mid-game")
	}
}

func TestStateReportsTheModesOwnHighScoreOnly(t *testing.T) {
	c := manual(t, highScores{High: 5, WrapHigh: 9}, height(24))
	_, _ = c.HandleInput("m")
	_, _ = c.HandleInput(" ")
	c.mu.Lock()
	c.game.Score, c.game.High = 12, 12
	c.mu.Unlock()
	st := c.State()
	if st.WrapHigh != 12 || st.HighScore != 5 || !st.Wrap || st.high() != 12 {
		t.Fatalf("state %+v", st)
	}
}

func TestATooSmallTerminalPausesTheGameAndItResumesOnlyWhenTheUserSaysSo(t *testing.T) {
	h := 24
	c := manual(t, highScores{}, func() int { return h })
	lines := c.Render(80)
	if len(lines) != 22 || !strings.Contains(strings.Join(lines, ""), "▀") {
		t.Fatalf("80x24: %d lines", len(lines))
	}
	_, _ = c.HandleInput(" ")
	c.advance(time.Second)
	_, head, _, _ := c.snapshot()

	h = 10 // too short for the board
	small := c.Render(80)
	if !strings.Contains(strings.Join(small, "\n"), "too small") {
		t.Fatal("no resize hint")
	}
	if status, _, _, _ := c.snapshot(); status != snake.Paused {
		t.Fatalf("status %v: a game that no longer fits must pause", status)
	}
	for frame := range 4 { // every further frame at this size keeps it paused
		c.Render(80)
		if status, _, _, _ := c.snapshot(); status != snake.Paused {
			t.Fatalf("status %v after %d more frames", status, frame+1)
		}
	}
	if c.advance(10 * time.Second) {
		t.Fatal("a game that does not fit must not advance")
	}
	if res, _ := c.HandleInput("q"); !res.Done {
		t.Fatal("q must still leave a too-small view")
	}

	h = 24
	c.Render(80)
	if status, _, _, _ := c.snapshot(); status != snake.Paused {
		t.Fatalf("status %v: growing the window must not resume the game by itself", status)
	}
	if c.advance(time.Second) {
		t.Fatal("a paused game advanced")
	}
	_, _ = c.HandleInput("p")
	c.advance(time.Second)
	if _, now, _, _ := c.snapshot(); now == head {
		t.Fatal("game did not resume after p")
	}
}

// Review rev-pigpen-pig-snake: a game started in a 100x30 terminal (a 12x6
// board of 8 px pigs) that is shrunk to 40x16 asks for the 50x16 its board
// needs at 4 px pigs, not for the 34x16 of the smallest new board.
func TestATooSmallRunningGameAsksForTheSizeOfItsOwnBoard(t *testing.T) {
	h := 30
	c := manual(t, highScores{}, func() int { return h })
	c.Render(100)
	_, _ = c.HandleInput(" ")
	c.mu.Lock()
	w, rows := c.game.W, c.game.H
	c.mu.Unlock()
	if w != 12 || rows != 6 {
		t.Fatalf("100x30 board %dx%d", w, rows)
	}
	h = 16
	joined := strings.Join(c.Render(40), "\n")
	if !strings.Contains(joined, "needs 50x16, have 40x16") {
		t.Fatalf("hint:\n%s", joined)
	}
	h = 16
	if joined := strings.Join(c.Render(50), "\n"); strings.Contains(joined, "too small") {
		t.Fatalf("the 50x16 the hint asked for does not fit the board:\n%s", joined)
	}
}

func TestATooSmallTitleScreenStaysWaiting(t *testing.T) {
	c := manual(t, highScores{}, height(10))
	c.Render(80)
	if status, _, _, _ := c.snapshot(); status != snake.Waiting {
		t.Fatalf("status %v", status)
	}
	if res, _ := c.HandleInput(" "); res.Done {
		t.Fatal("space must not quit")
	}
	c.Render(80) // starting while too small: the next frame pauses it
	if status, _, _, _ := c.snapshot(); status != snake.Paused {
		t.Fatalf("status %v", status)
	}
}

func TestBoardFollowsTheTerminalWhileWaitingAndIsFixedOnceRunning(t *testing.T) {
	h := 40 // a 120x40 terminal
	c := manual(t, highScores{}, func() int { return h })
	c.Render(120)
	c.mu.Lock()
	w1, h1 := c.game.W, c.game.H
	c.mu.Unlock()
	if w1 != 14 || h1 != 9 {
		t.Fatalf("120x40 board %dx%d", w1, h1)
	}
	h = 24
	c.Render(80)
	c.mu.Lock()
	w2, h2 := c.game.W, c.game.H
	c.mu.Unlock()
	if w2 != 13 || h2 != 6 {
		t.Fatalf("80x24 board %dx%d", w2, h2)
	}
	_, _ = c.HandleInput(" ")
	h = 40
	c.Render(120)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.game.W != 13 || c.game.H != 6 {
		t.Fatalf("running board changed to %dx%d", c.game.W, c.game.H)
	}
}

func TestRenderHandlesEveryNarrowWidth(t *testing.T) {
	for w := 1; w <= 60; w++ {
		c := manual(t, highScores{}, height(24))
		lines := c.Render(w)
		want := max(w-2, 1)
		if len(lines) != 22 {
			t.Fatalf("terminal width %d: %d lines", w, len(lines))
		}
		for _, l := range lines {
			if got := cellsOf(l); got != want {
				t.Fatalf("terminal width %d: line has %d cells, want %d", w, got, want)
			}
		}
	}
}

// Twin of pigrunner TestRunnerComponentTimerInvalidatesAndStops. The timer
// runs at 40x speed so the twin does not wait for real 160 ms steps.
func TestSnakeComponentTimerInvalidatesAndStops(t *testing.T) {
	component := newComponentWith(highScores{High: 7}, sprites.Builtin(""), height(24), options{speed: 40, rng: fixedRand{}})
	_, _ = component.HandleInput("m") // wrap mode: at 40x speed a wall would end the game within a few ticks
	_, _ = component.HandleInput(" ")
	invalidated := make(chan struct{}, 8)
	component.SetInvalidate(func() {
		select {
		case invalidated <- struct{}{}:
		default:
		}
	})

	select {
	case <-invalidated:
	case <-time.After(time.Second):
		t.Fatal("snake timer did not request a frame")
	}
	head := func() snake.Point {
		component.mu.Lock()
		defer component.mu.Unlock()
		return component.game.Body[0]
	}
	before := head()
	select {
	case <-invalidated:
	case <-time.After(time.Second):
		t.Fatal("snake timer stopped before disposal")
	}
	if after := head(); after == before {
		t.Fatalf("snake did not advance: before=%v after=%v", before, after)
	}

	component.SetInvalidate(nil)
	component.Dispose()
	for len(invalidated) > 0 {
		<-invalidated
	}
	select {
	case <-invalidated:
		t.Fatal("snake requested a frame after invalidation detach and disposal")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestDisposeIsIdempotentAndTheWaitingGameNeverTicks(t *testing.T) {
	component := newComponentWith(highScores{}, sprites.Builtin(""), height(24), options{speed: 40, rng: fixedRand{}})
	invalidated := make(chan struct{}, 8)
	component.SetInvalidate(func() {
		select {
		case invalidated <- struct{}{}:
		default:
		}
	})
	select {
	case <-invalidated:
		t.Fatal("the title screen requested frames")
	case <-time.After(150 * time.Millisecond):
	}
	component.Dispose()
	component.Dispose()
}

func TestTimerInvalidatesWhenTheTerminalHeightChanges(t *testing.T) {
	var h atomic.Int64
	h.Store(24)
	component := newComponentWith(highScores{}, sprites.Builtin(""), func() int { return int(h.Load()) }, options{rng: fixedRand{}})
	defer component.Dispose()
	invalidated := make(chan struct{}, 8)
	component.SetInvalidate(func() {
		select {
		case invalidated <- struct{}{}:
		default:
		}
	})
	time.Sleep(60 * time.Millisecond)
	for len(invalidated) > 0 {
		<-invalidated
	}
	h.Store(30)
	select {
	case <-invalidated:
	case <-time.After(time.Second):
		t.Fatal("a height change did not request a frame")
	}
}

func cellsOf(line string) int {
	n := 0
	inEsc := false
	for _, r := range line {
		switch {
		case inEsc:
			if r == 'm' {
				inEsc = false
			}
		case r == '\x1b':
			inEsc = true
		case r >= 0x1F000:
			n += 2
		default:
			n++
		}
	}
	return n
}

func TestTheLeaderWearsTheSpriteChosenWithSprite(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "state", "pig-standard")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "login.json"), []byte(`{"variant":"lavender"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, want := newSprites(home).Palette(0)['P'], sprites.Builtin("lavender").Palette(0)['P']; got != want {
		t.Fatalf("leader body %v, want the lavender pig %v", got, want)
	}
	if got, want := newSprites(t.TempDir()).Palette(0)['P'], sprites.Builtin("").Palette(0)['P']; got != want {
		t.Fatalf("no saved sprite: leader body %v, want the default %v", got, want)
	}
}

func TestAPausedGameDoesNotBankTimeToSpendAfterResume(t *testing.T) {
	c := manual(t, highScores{}, height(24))
	_, _ = c.HandleInput(" ")
	c.mu.Lock()
	interval := c.game.Interval()
	c.mu.Unlock()
	_, _ = c.HandleInput("p")
	c.advance(10 * time.Second) // a long pause
	_, _ = c.HandleInput("p")
	_, before, _, _ := c.snapshot()
	if c.advance(interval / 2) {
		t.Fatal("time spent paused was banked and stepped the game right after resume")
	}
	if _, after, _, _ := c.snapshot(); after != before {
		t.Fatalf("leader moved %v -> %v", before, after)
	}
}

func TestNothingFiresAfterDisposeReturns(t *testing.T) {
	for i := range 30 {
		component := newComponentWith(highScores{}, sprites.Builtin(""), height(24), options{speed: 40, rng: fixedRand{}})
		_, _ = component.HandleInput("m") // wrap: keeps ticking
		_, _ = component.HandleInput(" ")
		var fired atomic.Int64
		component.SetInvalidate(func() { fired.Add(1) })
		for deadline := time.Now().Add(2 * time.Second); fired.Load() == 0; time.Sleep(time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("run %d: the timer never fired", i)
			}
		}
		component.Dispose()
		at := fired.Load()
		time.Sleep(25 * time.Millisecond)
		if got := fired.Load(); got != at {
			t.Fatalf("run %d: the timer fired %d more time(s) after Dispose returned", i, got-at)
		}
	}
}

func TestDisposeWaitsForAFrameRequestInFlight(t *testing.T) {
	component := newComponentWith(highScores{}, sprites.Builtin(""), height(24), options{speed: 40, rng: fixedRand{}})
	_, _ = component.HandleInput("m")
	_, _ = component.HandleInput(" ")
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	component.SetInvalidate(func() {
		once.Do(func() { close(entered) })
		<-release
	})
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("the timer never requested a frame")
	}
	disposed := make(chan struct{})
	go func() { component.Dispose(); close(disposed) }()
	select {
	case <-disposed:
		t.Fatal("Dispose returned while the timer goroutine was still inside a frame request")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case <-disposed:
	case <-time.After(2 * time.Second):
		t.Fatal("Dispose did not return after the frame request finished")
	}
}

var sgrPattern = regexp.MustCompile("\x1b\\[[0-9;]*m")

// Twin in spirit of angrypigs TestAngryPigsSharesTheRunnerStyle (PiG d86eb93):
// the HUD text and the title-screen status follow the shared arcade layout.
func TestSnakeSharesTheRunnerStyle(t *testing.T) {
	t.Setenv("COLORTERM", "truecolor")
	c := manual(t, highScores{}, height(40))
	lines := c.Render(120)
	hud := sgrPattern.ReplaceAllString(lines[len(lines)-2], "")
	if !strings.HasPrefix(hud, " PIG SNAKE  score 0000  herd 1  high 0000  walls  wasd/arrows steer") {
		t.Fatalf("HUD title line = %q", hud)
	}
	if status := sgrPattern.ReplaceAllString(lines[len(lines)-1], ""); !strings.Contains(status, "space start") {
		t.Fatalf("title-screen status = %q", status)
	}
	if !strings.Contains(lines[len(lines)-2], "\x1b[38;2;1;169;130mPIG SNAKE") {
		t.Fatal("the title is not in the runner's accent green")
	}
}

// Twin in spirit of pigrunner TestRunnerTitleAndGameOverScreens (PiG d86eb93).
func TestSnakeTitleAndGameOverScreens(t *testing.T) {
	t.Setenv("COLORTERM", "truecolor")
	component := newComponentWith(highScores{}, sprites.Builtin(""), height(40), options{speed: 40, rng: fixedRand{}})
	defer component.Dispose()
	lines := component.Render(120)
	if status := sgrPattern.ReplaceAllString(lines[len(lines)-1], ""); !strings.Contains(status, "space start") {
		t.Fatalf("title status = %q", status)
	}
	time.Sleep(10 * tickRate)
	component.mu.Lock()
	moved, score := component.game.Body[0], component.game.Score
	component.mu.Unlock()
	if moved != (snake.Point{X: component.game.W / 2, Y: component.game.H / 2}) || score != 0 {
		t.Fatal("the game ran on the title screen")
	}
	_, _ = component.HandleInput(" ")
	component.mu.Lock()
	component.game.Status, component.game.Cause = snake.Over, snake.HitWall
	component.mu.Unlock()
	lines = component.Render(120)
	if !strings.Contains(lines[len(lines)-2], "\x1b[91;1mscore") || !strings.Contains(lines[len(lines)-1], "CRASHED") {
		t.Fatalf("game-over HUD = %q", lines[len(lines)-2:])
	}
}
