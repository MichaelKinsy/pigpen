package pig_snake

import (
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/pig-snake/internal/scene"
	"github.com/MichaelKinsy/pigpen/pig-snake/internal/snake"
	"github.com/MichaelKinsy/pigpen/pig-snake/internal/sprites"
)

// The mouse steers the leader: a left press on the board turns it toward the pointer, the way the arrow key for that direction
// would (PiG forwards the mouse to a component that implements sdk.MouseHandler, in fullscreen mode). The pointer's cell is
// the SDK's: X and Y count from the component's first rendered cell.

var _ sdk.MouseHandler = (*component)(nil)

// headCenter is where the leader's middle is, in content cells, as the renderer drew it (it needs one Render first).
func headCenter(t *testing.T, c *component) (x, y int) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	g := c.game
	viewW, viewH := 78, 22
	pixelH := (viewH - scene.HUDLines) * 2
	size, ok := scene.Fit(c.src.Sizes(), g.W, g.H, viewW, pixelH)
	if !ok {
		t.Fatal("the board does not fit")
	}
	ox, oy := scene.BoardOrigin(viewW, pixelH, g.W, g.H, size)
	px, py := ox+g.Body[0].X*size+size/2, oy+g.Body[0].Y*size+size/2
	return px, py / 2
}

// press is a left press at the content cell (x, y).
func press(c *component, x, y int) (sdk.RemoteComponentResult, error) {
	return c.HandleMouse(sdk.MouseEvent{Type: sdk.MousePress, Button: sdk.MouseButtonLeft, X: x, Y: y, Width: 78, Height: 22})
}

func rendered(t *testing.T, h int) *component {
	t.Helper()
	c := manual(t, highScores{}, height(h))
	c.Render(80)
	return c
}

func TestAClickSteersTheLeaderTowardTheCell(t *testing.T) {
	cells := map[string]struct {
		dx, dy int
		want   snake.Dir
	}{
		"up": {0, -4, snake.Up}, "down": {0, 4, snake.Down}, "right": {6, 0, snake.Right},
	}
	for name, tc := range cells {
		c := rendered(t, 24)
		_, _ = c.HandleInput(" ") // running, heading right
		hx, hy := headCenter(t, c)
		if _, err := press(c, hx+tc.dx, hy+tc.dy); err != nil {
			t.Fatal(err)
		}
		c.advance(200 * time.Millisecond)
		if _, _, dir, _ := c.snapshot(); dir != tc.want {
			t.Errorf("%s: heading %v, want %v", name, dir, tc.want)
		}
	}
	// A turn across the heading is a turn: facing down, a click to the left turns left.
	c := rendered(t, 24)
	_, _ = c.HandleInput("s")
	hx, hy := headCenter(t, c)
	_, _ = press(c, hx-8, hy)
	c.advance(200 * time.Millisecond)
	if _, _, dir, _ := c.snapshot(); dir != snake.Left {
		t.Errorf("left of a down-heading leader: heading %v", dir)
	}
}

func TestTheLargerOffsetDecidesTheDirection(t *testing.T) {
	c := rendered(t, 24)
	_, _ = c.HandleInput("d")
	hx, hy := headCenter(t, c)
	_, _ = press(c, hx+2, hy+5) // 2 cells right, 5 rows (10 pixel rows) down: mostly down
	c.advance(200 * time.Millisecond)
	if _, _, dir, _ := c.snapshot(); dir != snake.Down {
		t.Fatalf("heading %v, want down", dir)
	}
	_, _ = press(c, hx+30, hy+6) // far right, a little down: mostly right
	c.advance(200 * time.Millisecond)
	if _, _, dir, _ := c.snapshot(); dir != snake.Right {
		t.Fatalf("heading %v, want right", dir)
	}
}

func TestAClickOnTheTitleScreenStartsTheGameFacingTheClick(t *testing.T) {
	c := rendered(t, 24)
	hx, hy := headCenter(t, c)
	_, _ = press(c, hx, hy-5)
	status, _, dir, _ := c.snapshot()
	if status != snake.Playing || dir != snake.Up {
		t.Fatalf("status %v heading %v, want playing up", status, dir)
	}
}

func TestAClickCannotReverseTheHerdOrTurnTwiceAtOnce(t *testing.T) {
	c := rendered(t, 24)
	c.mu.Lock()
	c.game.Start()
	c.game.Body = append(c.game.Body, snake.Point{X: c.game.Body[0].X - 1, Y: c.game.Body[0].Y}) // a follower behind the leader
	c.mu.Unlock()
	hx, hy := headCenter(t, c)
	_, _ = press(c, hx-9, hy) // back into the herd
	c.advance(200 * time.Millisecond)
	if status, _, dir, _ := c.snapshot(); dir != snake.Right || status != snake.Playing {
		t.Fatalf("heading %v status %v: a click must not turn the leader into its herd", dir, status)
	}
}

func TestOnlyALeftPressSteers(t *testing.T) {
	for name, ev := range map[string]sdk.MouseEvent{
		"right button": {Type: sdk.MousePress, Button: sdk.MouseButtonRight},
		"middle":       {Type: sdk.MousePress, Button: sdk.MouseButtonMiddle},
		"release":      {Type: sdk.MouseRelease, Button: sdk.MouseButtonLeft},
		"click":        {Type: sdk.MouseClick, Button: sdk.MouseButtonLeft},
		"move":         {Type: sdk.MouseMove, Button: sdk.MouseButtonNone},
		"drag":         {Type: sdk.MouseDrag, Button: sdk.MouseButtonLeft},
		"wheel":        {Type: sdk.MouseWheel, Button: sdk.MouseButtonNone, WheelDelta: 1},
	} {
		c := rendered(t, 24)
		_, _ = c.HandleInput(" ")
		hx, hy := headCenter(t, c)
		ev.X, ev.Y = hx, hy+6
		res, err := c.HandleMouse(ev)
		if err != nil || res.Done {
			t.Fatalf("%s: result %+v err %v", name, res, err)
		}
		c.advance(200 * time.Millisecond)
		if _, _, dir, _ := c.snapshot(); dir != snake.Right {
			t.Errorf("%s steered the leader: heading %v", name, dir)
		}
	}
}

func TestAClickDoesNothingWhilePausedOverOrOffTheBoard(t *testing.T) {
	// paused
	c := rendered(t, 24)
	_, _ = c.HandleInput(" ")
	_, _ = c.HandleInput("p")
	hx, hy := headCenter(t, c)
	_, _ = press(c, hx, hy+6)
	if status, _, dir, _ := c.snapshot(); status != snake.Paused || dir != snake.Right {
		t.Errorf("paused: status %v heading %v", status, dir)
	}
	// on the pig itself, and on the two HUD lines
	c = rendered(t, 24)
	_, _ = c.HandleInput(" ")
	hx, hy = headCenter(t, c)
	_, _ = press(c, hx, hy)
	_, _ = press(c, hx, 21) // the last HUD line of the 22-line view
	_, _ = press(c, hx, 20)
	c.advance(200 * time.Millisecond)
	if _, _, dir, _ := c.snapshot(); dir != snake.Right {
		t.Errorf("a click on the pig or the HUD turned the leader: %v", dir)
	}
	// game over
	c = rendered(t, 24)
	c.mu.Lock()
	c.game.Start()
	c.game.Status = snake.Over
	c.mu.Unlock()
	_, _ = press(c, hx, hy+6)
	if status, _, _, _ := c.snapshot(); status != snake.Over {
		t.Errorf("a click restarted a finished game: %v", status)
	}
}

func TestAClickBeforeTheFirstRenderOrOnATooSmallTerminalIsIgnored(t *testing.T) {
	c := manual(t, highScores{}, height(24))
	_, _ = c.HandleInput(" ")
	_, _ = press(c, 10, 3)
	c.advance(200 * time.Millisecond)
	if _, _, dir, _ := c.snapshot(); dir != snake.Right {
		t.Errorf("a click before any frame turned the leader: %v", dir)
	}
	small := manual(t, highScores{}, height(8))
	small.Render(80)
	_, _ = small.HandleInput(" ")
	_, _ = press(small, 10, 1)
	if status, _, dir, _ := small.snapshot(); dir != snake.Right || status == snake.Over {
		t.Errorf("too small: heading %v status %v", dir, status)
	}
}

func TestThePointerOffsetIsMeasuredFromTheMiddleOfTheLeader(t *testing.T) {
	g := snake.New(13, 6, snake.Walls, 0, fixedRand{})
	src := sprites.Shared("")
	viewW, viewH := 78, 22
	size, _ := scene.Fit(src.Sizes(), g.W, g.H, viewW, (viewH-scene.HUDLines)*2)
	ox, oy := scene.BoardOrigin(viewW, (viewH-scene.HUDLines)*2, g.W, g.H, size)
	hx, hy := ox+g.Body[0].X*size+size/2, (oy+g.Body[0].Y*size+size/2)/2
	dx, dy, ok := scene.PointerOffset(g, src, viewW, viewH, hx+3, hy)
	if !ok || dx != 3 || (dy != 0 && dy != -1) { // a text row is two pixel rows; the pointer is taken at the upper one
		t.Fatalf("offset = %d, %d, %v", dx, dy, ok)
	}
	if _, _, ok := scene.PointerOffset(g, src, viewW, viewH, hx, viewH-1); ok {
		t.Error("the HUD lines are not the board")
	}
	if _, _, ok := scene.PointerOffset(g, src, 4, 4, 1, 1); ok {
		t.Error("a view too small for the board has no pointer offset")
	}
}

func TestPointerDirectionBreaksTiesHorizontallyAndIgnoresThePig(t *testing.T) {
	for _, tc := range []struct {
		dx, dy int
		want   snake.Dir
		steer  bool
	}{
		{5, 5, snake.Right, true}, {-5, 5, snake.Left, true}, {5, -5, snake.Right, true},
		{2, 9, snake.Down, true}, {-2, -9, snake.Up, true}, {9, 2, snake.Right, true}, {-9, -2, snake.Left, true},
		{2, -2, 0, false}, {-2, 2, 0, false},
	} {
		got, steer := pointerDirection(tc.dx, tc.dy, 6)
		if steer != tc.steer || (steer && got != tc.want) {
			t.Errorf("pointerDirection(%d, %d, 6) = %v, %v; want %v, %v", tc.dx, tc.dy, got, steer, tc.want, tc.steer)
		}
	}
}
