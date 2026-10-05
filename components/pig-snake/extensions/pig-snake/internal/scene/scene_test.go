package scene

import (
	"fmt"
	"image/color"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/pig-snake/internal/pixel"
	"github.com/MichaelKinsy/pigpen/pig-snake/internal/snake"
	"github.com/MichaelKinsy/pigpen/pig-snake/internal/sprites"
)

var sgr = regexp.MustCompile("\x1b\\[[0-9;]*m")

func plain(line string) string { return sgr.ReplaceAllString(line, "") }

func cells(line string) int {
	n := 0
	for _, r := range plain(line) {
		n += pixel.CellWidth(r)
	}
	return n
}

type fixedRand struct{}

func (fixedRand) Intn(int) int { return 0 }

// fakeSource proves the renderer draws through the sprites.Source seam only:
// one 6-pixel head, and member m has body color (10+20m, 0, 0).
type fakeSource struct{}

func (*fakeSource) Sizes() []int { return []int{6} }
func (*fakeSource) Head(size int) []string {
	rows := make([]string, size)
	for i := range rows {
		rows[i] = strings.Repeat("P", size)
	}
	return rows
}
func (*fakeSource) Palette(member int) map[byte]color.RGBA {
	return map[byte]color.RGBA{'P': {uint8(10 + 20*member), 0, 0, 255}}
}

var builtinSizes = []int{8, 6, 4}

func TestGridForPicksTheLargestComfortableHead(t *testing.T) {
	for _, tc := range []struct {
		w, h       int // content area (terminal minus the overlay box)
		cols, rows int
		size       int
		ok         bool
	}{
		{78, 22, 13, 6, 6, true},    // 80x24 terminal
		{78, 26, 13, 8, 6, true},    // 9x6 at size 8 would fit but is not comfortable
		{118, 38, 14, 9, 8, true},   // 120x40
		{38, 22, 9, 10, 4, true},    // 40x24: only the small head gives a playable board
		{32, 14, 8, 6, 4, true},     // the smallest playable view
		{31, 14, 0, 0, 0, false},    // one column short
		{32, 13, 0, 0, 0, false},    // one line short
		{400, 200, 40, 22, 8, true}, // huge terminal: the board is capped
		{1, 1, 0, 0, 0, false},
		{78, 2, 0, 0, 0, false}, // no room for a HUD
	} {
		pixelH := (tc.h - HUDLines) * 2
		cols, rows, size, ok := GridFor(builtinSizes, tc.w, pixelH)
		if ok != tc.ok || cols != tc.cols || rows != tc.rows || size != tc.size {
			t.Errorf("view %dx%d: got %dx%d size %d ok %v, want %dx%d size %d ok %v",
				tc.w, tc.h, cols, rows, size, ok, tc.cols, tc.rows, tc.size, tc.ok)
		}
	}
}

func TestFitKeepsTheBoardAndShrinksTheHeadsWhenTheViewShrinks(t *testing.T) {
	// A 13x6 board chosen at size 6 needs 78x36 pixels.
	for _, tc := range []struct {
		w, pixelH int
		size      int
		ok        bool
	}{
		{78, 36, 6, true}, {200, 200, 8, true}, {77, 36, 4, true}, {52, 24, 4, true}, {51, 24, 0, false}, {52, 23, 0, false},
	} {
		size, ok := Fit(builtinSizes, 13, 6, tc.w, tc.pixelH)
		if size != tc.size || ok != tc.ok {
			t.Errorf("view %dx%d px: size %d ok %v, want %d %v", tc.w, tc.pixelH, size, ok, tc.size, tc.ok)
		}
	}
}

func TestMinViewIsTheSmallestPlayableBoardPlusHUD(t *testing.T) {
	if w, h := MinView(builtinSizes); w != 32 || h != 14 {
		t.Fatalf("min view %dx%d", w, h)
	}
	if w, h := MinView([]int{6}); w != 48 || h != 20 {
		t.Fatalf("min view for a single 6px source: %dx%d", w, h)
	}
}

func TestBoardOriginCentersTheBoard(t *testing.T) {
	x, y := BoardOrigin(78, 40, 13, 6, 6)
	if x != 0 || y != 2 {
		t.Fatalf("origin %d,%d", x, y)
	}
	x, y = BoardOrigin(100, 50, 10, 5, 8)
	if x != 10 || y != 5 {
		t.Fatalf("origin %d,%d", x, y)
	}
}

func playingGame(herd int) *snake.Game {
	g := snake.New(13, 6, snake.Walls, 42, fixedRand{})
	g.Start()
	g.Body = g.Body[:1]
	g.Body[0] = snake.Point{X: 9, Y: 2}
	for i := 1; i < herd; i++ {
		g.Body = append(g.Body, snake.Point{X: 9 - i, Y: 2})
	}
	g.Food = snake.Point{X: 12, Y: 5}
	g.Score = herd - 1
	return g
}

func TestEveryHerdMemberIsDrawnAsItsOwnPigHead(t *testing.T) {
	g := playingGame(5)
	src := &fakeSource{}
	var c pixel.Canvas
	c.Resize(78, 36)
	Draw(&c, g, src, 6)
	ox, oy := BoardOrigin(c.W, c.H, g.W, g.H, 6)
	for member, p := range g.Body {
		got := c.At(ox+p.X*6+3, oy+p.Y*6+3)
		if want := (color.RGBA{uint8(10 + 20*member), 0, 0, 255}); got != want {
			t.Errorf("member %d at %v: %v, want %v", member, p, got, want)
		}
	}
	// A free cell is board, not a pig.
	if got := c.At(ox+0*6+3, oy+0*6+3); got.R >= 10 && got.G == 0 && got.B == 0 {
		t.Errorf("empty cell drew a pig color: %v", got)
	}
}

func TestTheAppleIsDrawnWhereTheFoodIs(t *testing.T) {
	g := playingGame(1)
	var c pixel.Canvas
	c.Resize(78, 36)
	Draw(&c, g, &fakeSource{}, 6)
	ox, oy := BoardOrigin(c.W, c.H, g.W, g.H, 6)
	cx, cy := ox+g.Food.X*6, oy+g.Food.Y*6
	distinct := map[color.RGBA]bool{}
	for y := range 6 {
		for x := range 6 {
			distinct[c.At(cx+x, cy+y)] = true
		}
	}
	if len(distinct) < 3 {
		t.Fatalf("apple cell has only %d colors, want art (body, stalk, leaf or shading)", len(distinct))
	}
}

func TestTheBuiltinSourceDrawsAtEverySize(t *testing.T) {
	src := sprites.Builtin("pink")
	for _, size := range []int{8, 6, 4} {
		g := playingGame(4)
		var c pixel.Canvas
		c.Resize(g.W*size, g.H*size)
		Draw(&c, g, src, size)
		ox, oy := BoardOrigin(c.W, c.H, g.W, g.H, size)
		leader := c.At(ox+9*size+size/2, oy+2*size+size/2)
		body := src.Palette(0)['P']
		found := false
		for y := range size {
			for x := range size {
				if c.At(ox+9*size+x, oy+2*size+y) == body {
					found = true
				}
			}
		}
		if !found {
			t.Errorf("size %d: leader cell has no pink body pixel (center %v)", size, leader)
		}
	}
}

func TestGameOverTintsTheLeaderRed(t *testing.T) {
	// The leader stands on the top row, clear of the centered game-over card.
	g := playingGame(3)
	g.Body = []snake.Point{{X: 9, Y: 0}, {X: 8, Y: 0}, {X: 7, Y: 0}}
	var a, b pixel.Canvas
	a.Resize(78, 36)
	b.Resize(78, 36)
	Draw(&a, g, &fakeSource{}, 6)
	g.Status, g.Cause = snake.Over, snake.HitHerd
	Draw(&b, g, &fakeSource{}, 6)
	ox, oy := BoardOrigin(78, 36, g.W, g.H, 6)
	x, y := ox+9*6+3, oy+0*6+3
	if a.At(x, y) == b.At(x, y) {
		t.Fatal("the crashed leader looks the same as a live one")
	}
	if b.At(x, y).R <= a.At(x, y).R {
		t.Fatalf("crashed leader should be redder: %v -> %v", a.At(x, y), b.At(x, y))
	}
	// The herd behind it keeps its colors.
	fx, fy := ox+8*6+3, oy+0*6+3
	if a.At(fx, fy) != b.At(fx, fy) {
		t.Fatalf("a follower changed color on game over: %v -> %v", a.At(fx, fy), b.At(fx, fy))
	}
}

func hudText(g *snake.Game) string {
	lines := HUD(g, 100, true)
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = plain(l)
	}
	return strings.Join(out, "\n")
}

func TestHUDShowsScoreHerdHighModeAndStatus(t *testing.T) {
	g := playingGame(4)
	g.High = 12
	text := hudText(g)
	for _, want := range []string{"PIG SNAKE", "score 0003", "herd 4", "high 0012", "walls", "steer"} {
		if !strings.Contains(text, want) {
			t.Errorf("HUD lacks %q:\n%s", want, text)
		}
	}
	g.Mode = snake.Wrap
	if !strings.Contains(hudText(g), "wrap") {
		t.Error("wrap mode not shown")
	}
	for status, want := range map[snake.Status]string{
		snake.Waiting: "space start", snake.Paused: "PAUSED", snake.Over: "r retry", snake.Won: "HERD COMPLETE",
	} {
		g.Status = status
		if !strings.Contains(hudText(g), want) {
			t.Errorf("status %v: HUD lacks %q:\n%s", status, want, hudText(g))
		}
	}
	g.Status, g.Cause = snake.Over, snake.HitWall
	if !strings.Contains(hudText(g), "into the wall") {
		t.Error("cause of death (wall) missing")
	}
	g.Cause = snake.HitHerd
	if !strings.Contains(hudText(g), "into the herd") {
		t.Error("cause of death (herd) missing")
	}
	if got := HUD(g, 100, true); len(got) != HUDLines {
		t.Fatalf("%d HUD lines", len(got))
	}
}

func TestHUDLinesAreExactlyTheRequestedWidth(t *testing.T) {
	g := playingGame(3)
	for _, w := range []int{1, 5, 20, 33, 60, 100, 250} {
		for _, tc := range []bool{true, false} {
			for i, line := range HUD(g, w, tc) {
				if cells(line) != w {
					t.Errorf("width %d line %d has %d cells", w, i, cells(line))
				}
			}
		}
	}
}

func statuses() map[string]*snake.Game {
	waiting := snake.New(13, 6, snake.Walls, 3, fixedRand{})
	paused := playingGame(5)
	paused.Status = snake.Paused
	over := playingGame(5)
	over.Status, over.Cause = snake.Over, snake.HitWall
	won := playingGame(5)
	won.Status = snake.Won
	return map[string]*snake.Game{"waiting": waiting, "playing": playingGame(5), "paused": paused, "over": over, "won": won}
}

func TestRenderFillsEveryViewportExactly(t *testing.T) {
	widths := []int{1, 2, 5, 10, 20, 31, 32, 33, 34, 40, 60, 78, 80, 100, 118, 160, 200, 300}
	heights := []int{1, 2, 3, 10, 13, 14, 15, 22, 24, 38, 60}
	for name, g := range statuses() {
		for _, src := range []sprites.Source{sprites.Builtin("green"), &fakeSource{}} {
			for _, w := range widths {
				for _, h := range heights {
					var b Buffers
					lines := Render(&b, g, src, w, h, nil)
					if len(lines) != h {
						t.Fatalf("%s %dx%d: %d lines", name, w, h, len(lines))
					}
					for i, line := range lines {
						if got := cells(line); got != w {
							t.Fatalf("%s %dx%d: line %d has %d cells: %q", name, w, h, i, got, plain(line))
						}
					}
				}
			}
		}
	}
}

func TestRenderShowsTheBoardWhenItFitsAndTheHintWhenItDoesNot(t *testing.T) {
	g := playingGame(3) // 13x6 board
	var b Buffers
	src := sprites.Builtin("")
	fits := Render(&b, g, src, 78, 22, nil)
	if !strings.Contains(strings.Join(fits, "\n"), "▀") {
		t.Fatal("no half-block cells drawn at 78x22")
	}
	if !strings.Contains(plain(fits[len(fits)-2]), "PIG SNAKE") {
		t.Fatalf("HUD first line is %q", plain(fits[len(fits)-2]))
	}
	small := Render(&b, g, src, 40, 10, nil)
	joined := plain(strings.Join(small, "\n"))
	if strings.Contains(joined, "▀") || !strings.Contains(joined, "too small") {
		t.Fatalf("40x10 should show the resize hint:\n%s", joined)
	}
	// A 13x6 board at the smallest (4 px) pig needs 52x12 cells plus the HUD
	// and the overlay box: 54x16, not the 34x16 of the smallest new board.
	if !strings.Contains(joined, "needs 54x16") || !strings.Contains(joined, "have 42x12") {
		t.Fatalf("hint should name the needed and current size:\n%s", joined)
	}
}

// Review rev-pigpen-pig-snake: the hint of a game that is under way must name
// the size its own board needs. Naming the minimum new board (34x16) told a
// player in a 40x16 terminal "needs 34x16, have 40x16" while nothing fit.
func TestTheTooSmallHintNamesTheSizeTheBoardInPlayNeeds(t *testing.T) {
	src := sprites.Builtin("")
	for _, tc := range []struct {
		cols, rows   int
		status       snake.Status
		viewW, viewH int
		needs        string
	}{
		{13, 6, snake.Playing, 38, 14, "needs 54x16, have 40x16"},
		{13, 6, snake.Paused, 38, 14, "needs 54x16, have 40x16"},
		{13, 6, snake.Over, 38, 14, "needs 54x16, have 40x16"},
		{12, 6, snake.Paused, 28, 8, "needs 50x16, have 30x10"},
		{MaxCols, MaxRows, snake.Paused, 98, 28, "needs 162x48, have 100x30"},
		{9, 7, snake.Won, 30, 20, "needs 38x18, have 32x22"},
		// A waiting game takes the board of the terminal it is shown in, so it
		// needs only the smallest board.
		{13, 6, snake.Waiting, 30, 8, "needs 34x16, have 32x10"},
	} {
		g := snake.New(tc.cols, tc.rows, snake.Walls, 0, fixedRand{})
		g.Status = tc.status
		var b Buffers
		lines := Render(&b, g, src, tc.viewW, tc.viewH, nil)
		joined := plain(strings.Join(lines, "\n"))
		if !strings.Contains(joined, "too small") || !strings.Contains(joined, tc.needs) {
			t.Errorf("%dx%d board, status %v, view %dx%d: want %q in\n%s", tc.cols, tc.rows, tc.status, tc.viewW, tc.viewH, tc.needs, joined)
		}
		// The size named is enough: at exactly that terminal the board is drawn.
		var needW, needH int
		if _, err := fmt.Sscanf(tc.needs, "needs %dx%d", &needW, &needH); err != nil {
			t.Fatal(err)
		}
		if g.Status == snake.Waiting {
			continue
		}
		if _, ok := Fit(src.Sizes(), g.W, g.H, needW-2, (needH-2-HUDLines)*2); !ok {
			t.Errorf("%dx%d board does not fit the %dx%d terminal the hint asks for", tc.cols, tc.rows, needW, needH)
		}
		if _, ok := Fit(src.Sizes(), g.W, g.H, needW-3, (needH-2-HUDLines)*2); ok {
			t.Errorf("%dx%d board already fits one column less than the %dx%d the hint asks for", tc.cols, tc.rows, needW, needH)
		}
		if _, ok := Fit(src.Sizes(), g.W, g.H, needW-2, (needH-3-HUDLines)*2); ok {
			t.Errorf("%dx%d board already fits one row less than the %dx%d the hint asks for", tc.cols, tc.rows, needW, needH)
		}
	}
}

func TestRenderShrinksHeadsInsteadOfDroppingTheBoard(t *testing.T) {
	g := playingGame(3) // board chosen at 13x6
	var b Buffers
	src := sprites.Builtin("")
	big := Render(&b, g, src, 118, 38, nil)
	mid := Render(&b, g, src, 78, 22, nil)
	tiny := Render(&b, g, src, 52, 14, nil) // 13x6 at 4px = 52x24px
	for name, lines := range map[string][]string{"big": big, "mid": mid, "tiny": tiny} {
		if strings.Contains(plain(strings.Join(lines, "\n")), "too small") {
			t.Errorf("%s view lost the board", name)
		}
	}
	if strings.Join(big, "") == strings.Join(mid, "") {
		t.Error("view size did not change the scene")
	}
}

func TestTooSmallMessageFitsNarrowWidths(t *testing.T) {
	for w := 1; w <= 40; w++ {
		msg := TooSmall(w, 5, builtinSizes)
		if len(msg) == 0 {
			t.Fatalf("width %d: empty message", w)
		}
		var b Buffers
		lines := Render(&b, snake.New(13, 6, snake.Walls, 0, fixedRand{}), sprites.Builtin(""), w, 5, nil)
		for i, l := range lines {
			if cells(l) != w {
				t.Fatalf("width %d line %d: %d cells", w, i, cells(l))
			}
		}
	}
	if joined := strings.Join(TooSmall(30, 10, builtinSizes), "\n"); !strings.Contains(joined, "34x16") || !strings.Contains(joined, "32x12") {
		t.Fatalf("message %q", joined)
	}
}

// Review rev-pigpen-pig-snake (mutation survivor: the frame colour). Walls
// mode draws a solid accent wall round the board; wrap mode, which has no wall,
// a dim one.
func TestTheBoardFrameShowsWhetherTheEdgeIsAWall(t *testing.T) {
	for _, tc := range []struct {
		mode snake.Mode
		want color.RGBA
	}{{snake.Walls, accent}, {snake.Wrap, dimStar}} {
		g := playingGame(1)
		g.Mode = tc.mode
		var c pixel.Canvas
		c.Resize(90, 40)
		Draw(&c, g, &fakeSource{}, 6)
		ox, oy := BoardOrigin(90, 40, g.W, g.H, 6)
		for _, p := range [][2]int{{ox - 1, oy - 1}, {ox + g.W*6, oy + 5}, {ox + 7, oy + g.H*6}} {
			if got := c.At(p[0], p[1]); got != tc.want {
				t.Errorf("%v mode: frame pixel %v = %v, want %v", tc.mode, p, got, tc.want)
			}
		}
	}
}

func TestCardPixels(t *testing.T) {
	// The card title is drawn in the accent color inside the board. The frame
	// around the board is accent too, so look strictly inside it.
	src := &fakeSource{}
	inkInside := func(g *snake.Game) int {
		var c pixel.Canvas
		c.Resize(118, 60)
		Draw(&c, g, src, 6)
		ox, oy := BoardOrigin(c.W, c.H, g.W, g.H, 6)
		n := 0
		for y := oy; y < oy+g.H*6; y++ {
			for x := ox; x < ox+g.W*6; x++ {
				if c.At(x, y) == accent {
					n++
				}
			}
		}
		return n
	}
	states := statuses()
	if n := inkInside(states["playing"]); n != 0 {
		t.Errorf("a running game drew %d card pixels", n)
	}
	for _, name := range []string{"waiting", "paused", "over", "won"} {
		if n := inkInside(states[name]); n < 20 {
			t.Errorf("%s: only %d card ink pixels", name, n)
		}
	}
}

func TestRenderSwitchesSourceWithoutStalePalettes(t *testing.T) {
	var b Buffers
	g := playingGame(3)
	first := strings.Join(Render(&b, g, sprites.Builtin("pink"), 78, 22, nil), "")
	second := strings.Join(Render(&b, g, sprites.Builtin("lavender"), 78, 22, nil), "")
	if first == second {
		t.Fatal("a different source drew the same pigs: stale palette cache")
	}
	again := strings.Join(Render(&b, g, sprites.Builtin("pink"), 78, 22, nil), "")
	if again != first {
		t.Fatal("switching back did not restore the first picture")
	}
}

func TestTheHUDFollowsTheGameBetweenFrames(t *testing.T) {
	var b Buffers
	g := playingGame(3)
	src := sprites.Builtin("")
	before := plain(strings.Join(Render(&b, g, src, 78, 22, nil), "\n"))
	g.Score = 7 // only the score changes: the cache must notice it alone
	after := plain(strings.Join(Render(&b, g, src, 78, 22, nil), "\n"))
	if !strings.Contains(before, "score 0002") || !strings.Contains(after, "score 0007") {
		t.Fatalf("cached HUD went stale:\n%s\n%s", before, after)
	}
	g.Mode = snake.Wrap
	if !strings.Contains(plain(strings.Join(Render(&b, g, src, 78, 22, nil), "\n")), "wrap") {
		t.Fatal("HUD did not follow the mode")
	}
	g.Status = snake.Paused
	if !strings.Contains(plain(strings.Join(Render(&b, g, src, 78, 22, nil), "\n")), "PAUSED") {
		t.Fatal("HUD did not follow the status")
	}
	g.Status, g.Cause = snake.Over, snake.HitWall
	if !strings.Contains(plain(strings.Join(Render(&b, g, src, 78, 22, nil), "\n")), "into the wall") {
		t.Fatal("HUD did not follow the cause")
	}
}

func TestRenderReusesBuffersAcrossFrames(t *testing.T) {
	var b Buffers
	g := playingGame(6)
	src := sprites.Builtin("")
	dst := make([]string, 0, 30)
	dst = Render(&b, g, src, 78, 22, dst[:0])
	first := append([]string(nil), dst...)
	dst = Render(&b, g, src, 78, 22, dst[:0])
	if strings.Join(first, "") != strings.Join(dst, "") {
		t.Fatal("the same game rendered twice differs")
	}
	if allocs := testing.AllocsPerRun(10, func() { dst = Render(&b, g, src, 78, 22, dst[:0]) }); allocs > 4 {
		t.Fatalf("steady-state render allocates %.0f times", allocs)
	}
}

// Twin of pigrunner TestRenderDownsamplesWithoutTrueColor (PiG d86eb93).
func TestRenderDownsamplesWithoutTrueColor(t *testing.T) {
	t.Setenv("COLORTERM", "")
	t.Setenv("WT_SESSION", "")
	var b Buffers
	lines := Render(&b, playingGame(4), sprites.Builtin(""), 78, 22, nil)
	frame := strings.Join(lines, "\n")
	if strings.Contains(frame, "38;2;") || !strings.Contains(frame, "\x1b[38;5;") {
		t.Fatal("a frame without 24-bit color support must use 256-color SGR")
	}
	t.Setenv("COLORTERM", "truecolor")
	b = Buffers{}
	frame = strings.Join(Render(&b, playingGame(4), sprites.Builtin(""), 78, 22, nil), "\n")
	if !strings.Contains(frame, "38;2;") {
		t.Fatal("a true-color terminal must get 24-bit SGR")
	}
}

// Twin of arcade TestDrawCardCentersTheTitleOrDeclines (PiG d86eb93).
func TestDrawCardCentersTheTitleOrDeclines(t *testing.T) {
	g := snake.New(13, 6, snake.Walls, 0, fixedRand{})
	var c pixel.Canvas
	c.Resize(78, 36)
	c.Rect(0, 0, 78, 36, roadDark)
	ox, oy := BoardOrigin(c.W, c.H, g.W, g.H, 6)
	card(&c, ox, oy, g, 6, "GAME OVER", "R RETRY")
	left, right := c.W, -1
	for y := range c.H {
		for x := range c.W {
			if c.At(x, y) == accent {
				left, right = min(left, x), max(right, x)
			}
		}
	}
	if right < 0 || abs((left+right)/2-c.W/2) > 2 {
		t.Fatalf("title spans %d..%d, not centered in %d", left, right, c.W)
	}
	small := snake.New(8, 6, snake.Walls, 0, fixedRand{})
	var d pixel.Canvas
	d.Resize(32, 24)
	d.Rect(0, 0, 32, 24, roadDark)
	before := append([]color.RGBA(nil), d.Px...)
	card(&d, 0, 0, small, 4, "GAME OVER", "R RETRY")
	for i := range before {
		if d.Px[i] != before[i] {
			t.Fatal("the card drew on a canvas too small for it")
		}
	}
}

func abs(v int) int { return max(v, -v) }

// Twin in spirit of pigrunner TestPieSpritesHaveUniformWidth: the apple art.
func TestAppleArtHasUniformRowsAndOnlyKnownSymbols(t *testing.T) {
	for size, art := range map[int][]string{8: apple8, 6: apple6, 4: apple4} {
		if len(art) != size {
			t.Errorf("apple%d has %d rows", size, len(art))
		}
		for i, row := range art {
			if len(row) != size {
				t.Errorf("apple%d row %d is %q", size, i, row)
			}
			for j := range len(row) {
				if row[j] != '.' && applePal[row[j]].A == 0 {
					t.Errorf("apple%d row %d: symbol %q has no color", size, i, row[j])
				}
			}
		}
		if got := appleFor(size); len(got) != size {
			t.Errorf("appleFor(%d) returned %d rows", size, len(got))
		}
	}
	if len(appleFor(12)) != 8 || len(appleFor(5)) != 4 {
		t.Error("appleFor must fall back to the nearest smaller art")
	}
}
