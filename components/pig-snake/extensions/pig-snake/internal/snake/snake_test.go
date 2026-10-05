package snake

import (
	"testing"
	"time"
)

// seqRand returns scripted values, then zeros, so food placement is exact.
type seqRand struct {
	vals []int
	i    int
}

func (r *seqRand) Intn(n int) int {
	v := 0
	if r.i < len(r.vals) {
		v = r.vals[r.i]
	}
	r.i++
	return v % n
}

// newPlaying starts a game with the leader at (x,y) heading dir and food at food.
func newPlaying(w, h int, mode Mode, head Point, dir Dir, food Point) *Game {
	g := New(w, h, mode, 0, &seqRand{})
	g.Start()
	g.Body = []Point{head}
	g.Dir = dir
	g.Food = food
	return g
}

func TestNewStartsWithASoloLeaderCenteredFacingRight(t *testing.T) {
	g := New(10, 6, Walls, 7, &seqRand{})
	if g.Status != Waiting || len(g.Body) != 1 || g.Herd() != 1 {
		t.Fatalf("status %v body %v herd %d", g.Status, g.Body, g.Herd())
	}
	if g.Body[0] != (Point{5, 3}) || g.Dir != Right {
		t.Fatalf("leader %v dir %v", g.Body[0], g.Dir)
	}
	if g.High != 7 || g.Score != 0 {
		t.Fatalf("high %d score %d", g.High, g.Score)
	}
	if g.Food == g.Body[0] || g.Food.X < 0 || g.Food.X >= 10 || g.Food.Y < 0 || g.Food.Y >= 6 {
		t.Fatalf("food %v not on a free cell", g.Food)
	}
}

func TestStepMovesTheLeaderOneCellPerDirection(t *testing.T) {
	for _, tc := range []struct {
		dir  Dir
		want Point
	}{{Up, Point{4, 3}}, {Right, Point{5, 4}}, {Down, Point{4, 5}}, {Left, Point{3, 4}}} {
		g := newPlaying(9, 9, Walls, Point{4, 4}, tc.dir, Point{0, 0})
		r := g.Step()
		if !r.Moved || r.Ate || r.Death != Alive || g.Body[0] != tc.want || len(g.Body) != 1 {
			t.Errorf("dir %v: result %+v body %v", tc.dir, r, g.Body)
		}
	}
}

func TestStepDoesNothingUnlessPlaying(t *testing.T) {
	for _, s := range []Status{Waiting, Paused, Over, Won} {
		g := newPlaying(9, 9, Walls, Point{4, 4}, Right, Point{0, 0})
		g.Status = s
		if r := g.Step(); r.Moved || g.Body[0] != (Point{4, 4}) {
			t.Errorf("status %v moved: %+v %v", s, r, g.Body)
		}
	}
}

func TestEatingAddsOnePigHeadAndScores(t *testing.T) {
	g := newPlaying(9, 9, Walls, Point{4, 4}, Right, Point{5, 4})
	r := g.Step()
	if !r.Ate || g.Score != 1 || g.Herd() != 2 || len(g.Body) != 2 {
		t.Fatalf("result %+v score %d herd %d body %v", r, g.Score, g.Herd(), g.Body)
	}
	if g.Body[0] != (Point{5, 4}) || g.Body[1] != (Point{4, 4}) {
		t.Fatalf("herd in line: %v", g.Body)
	}
	if g.Food == (Point{5, 4}) || g.Occupied(g.Food) {
		t.Fatalf("new food %v is under the herd", g.Food)
	}
}

func TestTheHerdFollowsTheLeaderInLine(t *testing.T) {
	g := newPlaying(12, 12, Walls, Point{2, 2}, Right, Point{3, 2})
	g.Step() // eat, herd 2
	g.Food = Point{4, 2}
	g.Step() // eat, herd 3
	g.Food = Point{11, 11}
	g.Turn(Down)
	g.Step()
	g.Step()
	want := []Point{{4, 4}, {4, 3}, {4, 2}}
	for i, p := range want {
		if g.Body[i] != p {
			t.Fatalf("body %v, want %v", g.Body, want)
		}
	}
	if g.Herd() != 3 || g.Score != 2 {
		t.Fatalf("herd %d score %d", g.Herd(), g.Score)
	}
}

func TestFoodNeverLandsOnTheHerdAndPicksTheNthFreeCell(t *testing.T) {
	// 3x1 board, herd on cells 0 and 1: the only free cell is 2.
	g := New(3, 1, Walls, 0, &seqRand{vals: []int{0}})
	g.Start()
	g.Body = []Point{{1, 0}, {0, 0}}
	g.Dir = Right
	g.placeFood()
	if g.Food != (Point{2, 0}) {
		t.Fatalf("food %v", g.Food)
	}
	// 4x1 board, herd on cell 0: free cells 1,2,3; rng value 1 picks the second (cell 2).
	g = New(4, 1, Walls, 0, &seqRand{vals: []int{0, 1}})
	g.Start()
	g.Body = []Point{{0, 0}}
	g.placeFood()
	if g.Food != (Point{2, 0}) {
		t.Fatalf("nth free cell: food %v", g.Food)
	}
}

func TestFoodIsAlwaysOnAFreeCellAcrossManyGames(t *testing.T) {
	rng := &seqRand{}
	for i := range 200 {
		rng.vals, rng.i = []int{i * 7}, 0
		g := New(6, 5, Wrap, 0, rng)
		g.Start()
		g.Body = []Point{{i % 6, i % 5}, {(i + 1) % 6, i % 5}, {(i + 2) % 6, i % 5}}
		g.placeFood()
		if g.Occupied(g.Food) {
			t.Fatalf("game %d: food %v on herd %v", i, g.Food, g.Body)
		}
	}
}

func TestReverseIsIgnoredOnceThereIsAHerdButAllowedForALoneLeader(t *testing.T) {
	g := newPlaying(9, 9, Walls, Point{4, 4}, Right, Point{0, 0})
	if !g.Turn(Left) {
		t.Fatal("a solo leader may turn around")
	}
	g = newPlaying(9, 9, Walls, Point{4, 4}, Right, Point{0, 0})
	g.Body = []Point{{4, 4}, {3, 4}}
	if g.Turn(Left) {
		t.Fatal("a leader with a herd must not reverse into it")
	}
	if g.Turn(Right) {
		t.Fatal("the current heading is not a turn")
	}
	g.Step()
	if g.Status != Playing || g.Body[0] != (Point{5, 4}) {
		t.Fatalf("status %v body %v", g.Status, g.Body)
	}
}

func TestTwoQuickTurnsBothApplyOnSuccessiveSteps(t *testing.T) {
	// Right, then Up and Left pressed between two ticks: without a queue, the
	// second key would have been judged against the old heading and lost.
	g := newPlaying(9, 9, Walls, Point{4, 4}, Right, Point{0, 0})
	g.Body = []Point{{4, 4}, {3, 4}, {2, 4}}
	if !g.Turn(Up) || !g.Turn(Left) {
		t.Fatal("both turns must be accepted")
	}
	g.Step()
	if g.Body[0] != (Point{4, 3}) {
		t.Fatalf("first turn: %v", g.Body)
	}
	g.Step()
	if g.Body[0] != (Point{3, 3}) || g.Status != Playing {
		t.Fatalf("second turn: %v status %v", g.Body, g.Status)
	}
}

func TestTheTurnQueueHoldsAtMostTwo(t *testing.T) {
	g := newPlaying(9, 9, Walls, Point{4, 4}, Right, Point{0, 0})
	g.Body = []Point{{4, 4}, {3, 4}}
	if !g.Turn(Up) || !g.Turn(Left) {
		t.Fatal("two turns fit")
	}
	if g.Turn(Down) {
		t.Fatal("a third queued turn must be refused")
	}
}

func TestWallsEndTheGameAtEveryEdgeWithoutMovingTheLeader(t *testing.T) {
	for _, tc := range []struct {
		head Point
		dir  Dir
	}{{Point{0, 3}, Left}, {Point{8, 3}, Right}, {Point{4, 0}, Up}, {Point{4, 5}, Down}} {
		g := newPlaying(9, 6, Walls, tc.head, tc.dir, Point{1, 1})
		g.Score = 3
		r := g.Step()
		if r.Moved || r.Death != HitWall || g.Status != Over || g.Cause != HitWall || g.Body[0] != tc.head {
			t.Errorf("%v %v: result %+v status %v head %v", tc.head, tc.dir, r, g.Status, g.Body[0])
		}
	}
}

func TestWrapCarriesTheLeaderAroundEveryEdge(t *testing.T) {
	for _, tc := range []struct {
		head Point
		dir  Dir
		want Point
	}{
		{Point{0, 3}, Left, Point{8, 3}}, {Point{8, 3}, Right, Point{0, 3}},
		{Point{4, 0}, Up, Point{4, 5}}, {Point{4, 5}, Down, Point{4, 0}},
	} {
		g := newPlaying(9, 6, Wrap, tc.head, tc.dir, Point{1, 1})
		r := g.Step()
		if !r.Moved || r.Death != Alive || g.Body[0] != tc.want || g.Status != Playing {
			t.Errorf("%v %v: result %+v head %v want %v", tc.head, tc.dir, r, g.Body[0], tc.want)
		}
	}
}

func TestWrapFoodAcrossTheEdgeIsEaten(t *testing.T) {
	g := newPlaying(9, 6, Wrap, Point{8, 3}, Right, Point{0, 3})
	if r := g.Step(); !r.Ate || g.Herd() != 2 {
		t.Fatalf("result %+v herd %d", r, g.Herd())
	}
}

func TestRunningIntoTheHerdEndsTheGame(t *testing.T) {
	g := newPlaying(9, 9, Walls, Point{4, 4}, Up, Point{0, 0})
	// A hook: leader (4,4) heading Up, herd curls so that (4,3) is a herd cell.
	g.Body = []Point{{4, 4}, {5, 4}, {5, 3}, {4, 3}, {3, 3}}
	r := g.Step()
	if r.Moved || r.Death != HitHerd || g.Status != Over || g.Cause != HitHerd {
		t.Fatalf("result %+v status %v", r, g.Status)
	}
}

func TestTheVacatingTailCellIsFreeWhenNotEating(t *testing.T) {
	// 2x2 loop: leader (0,0), herd (0,1),(1,1),(1,0). Heading Right the leader
	// enters (1,0), the tail cell, which is vacated on the same step.
	g := newPlaying(4, 4, Walls, Point{0, 0}, Right, Point{3, 3})
	g.Body = []Point{{0, 0}, {0, 1}, {1, 1}, {1, 0}}
	r := g.Step()
	if r.Death != Alive || !r.Moved || g.Body[0] != (Point{1, 0}) || len(g.Body) != 4 {
		t.Fatalf("result %+v body %v", r, g.Body)
	}
}

func TestTheTailCellIsNotFreeWhenEating(t *testing.T) {
	g := newPlaying(4, 4, Walls, Point{0, 0}, Right, Point{1, 0})
	g.Body = []Point{{0, 0}, {0, 1}, {1, 1}, {1, 0}}
	r := g.Step()
	if r.Death != HitHerd || g.Status != Over {
		t.Fatalf("eating the tail's cell must collide: %+v", r)
	}
}

func TestFillingTheBoardWins(t *testing.T) {
	g := New(3, 1, Walls, 0, &seqRand{})
	g.Start()
	g.Body = []Point{{1, 0}, {0, 0}}
	g.Dir = Right
	g.Food = Point{2, 0}
	r := g.Step()
	if !r.Ate || g.Status != Won || g.Herd() != 3 || g.Score != 1 {
		t.Fatalf("result %+v status %v herd %d", r, g.Status, g.Herd())
	}
	if g.Step().Moved {
		t.Fatal("a won game must not step")
	}
}

// Review rev-pigpen-pig-snake (mutation survivor: winning one apple early).
// With one free cell left the herd is not full: the game goes on and the apple
// lands on that last cell.
func TestOneFreeCellLeftIsNotAWin(t *testing.T) {
	g := New(4, 1, Walls, 0, &seqRand{})
	g.Start()
	g.Body = []Point{{1, 0}, {0, 0}}
	g.Dir = Right
	g.Food = Point{2, 0}
	if r := g.Step(); !r.Ate || g.Herd() != 3 {
		t.Fatalf("result %+v herd %d", r, g.Herd())
	}
	if g.Status != Playing || g.Food != (Point{3, 0}) {
		t.Fatalf("status %v food %v: three pigs on a 4x1 board is not a full herd", g.Status, g.Food)
	}
	if r := g.Step(); !r.Ate || g.Status != Won {
		t.Fatalf("result %+v status %v", r, g.Status)
	}
}

// Review rev-pigpen-pig-snake (mutation survivor: apples only on the first
// free cells). Every free cell, the last included, can get the apple.
func TestEveryFreeCellCanGetTheApple(t *testing.T) {
	for k := range 4 {
		g := New(5, 1, Walls, 0, &seqRand{vals: []int{0, k}})
		g.Body = []Point{{0, 0}}
		g.placeFood()
		if want := (Point{k + 1, 0}); g.Food != want {
			t.Fatalf("rng %d: food %v, want %v", k, g.Food, want)
		}
	}
}

func TestPauseTogglesOnlyWhilePlaying(t *testing.T) {
	g := New(9, 9, Walls, 0, &seqRand{})
	g.TogglePause()
	if g.Status != Waiting {
		t.Fatal("pause before start")
	}
	g.Start()
	g.TogglePause()
	if g.Status != Paused {
		t.Fatalf("status %v", g.Status)
	}
	if g.Turn(Up) {
		t.Fatal("no turns while paused")
	}
	g.TogglePause()
	if g.Status != Playing {
		t.Fatalf("status %v", g.Status)
	}
	g.Status = Over
	g.TogglePause()
	if g.Status != Over {
		t.Fatal("pause after game over")
	}
}

func TestHighScoreFollowsTheScore(t *testing.T) {
	g := newPlaying(9, 9, Walls, Point{4, 4}, Right, Point{5, 4})
	g.High = 0
	g.Step()
	if g.High != 1 {
		t.Fatalf("high %d", g.High)
	}
	g = newPlaying(9, 9, Walls, Point{4, 4}, Right, Point{5, 4})
	g.High = 10
	g.Step()
	if g.High != 10 {
		t.Fatalf("a lower score must not lower the high score: %d", g.High)
	}
}

func TestIntervalSpeedsUpWithTheHerdAndClamps(t *testing.T) {
	if Interval(0) != 160*time.Millisecond {
		t.Fatalf("start %v", Interval(0))
	}
	if !(Interval(5) < Interval(0)) || Interval(5) != 135*time.Millisecond {
		t.Fatalf("5 pigs %v", Interval(5))
	}
	if Interval(1000) != 70*time.Millisecond || Interval(18) != 70*time.Millisecond || Interval(17) != 75*time.Millisecond {
		t.Fatalf("clamp %v %v %v", Interval(17), Interval(18), Interval(1000))
	}
	g := newPlaying(9, 9, Walls, Point{4, 4}, Right, Point{0, 0})
	g.Score = 5
	if g.Interval() != Interval(5) {
		t.Fatal("Game.Interval must use the score")
	}
}

func TestModeSwitchesOnlyBeforeTheFirstStep(t *testing.T) {
	g := New(9, 9, Walls, 0, &seqRand{})
	if !g.SetMode(Wrap) || g.Mode != Wrap {
		t.Fatal("waiting game must accept a mode")
	}
	g.Start()
	if g.SetMode(Walls) || g.Mode != Wrap {
		t.Fatal("a running game must keep its mode")
	}
	if Walls.Other() != Wrap || Wrap.Other() != Walls || Walls.String() == Wrap.String() || Walls.String() == "" {
		t.Fatal("mode helpers")
	}
}

func TestResizeOnlyWhileWaitingAndKeepsTheHighScore(t *testing.T) {
	g := New(9, 9, Walls, 12, &seqRand{})
	if !g.Resize(20, 10) || g.W != 20 || g.H != 10 || g.Body[0] != (Point{10, 5}) || g.High != 12 {
		t.Fatalf("resize: %dx%d body %v high %d", g.W, g.H, g.Body, g.High)
	}
	if g.Occupied(g.Food) {
		t.Fatal("food under the leader after resize")
	}
	g.Start()
	if g.Resize(30, 30) || g.W != 20 {
		t.Fatal("a running game cannot resize")
	}
}

// Review rev-pigpen-pig-snake (mutation survivor: reset keeping the turn
// queue). A turn still queued when the herd crashed must not steer the next game.
func TestRestartForgetsTurnsQueuedBeforeTheCrash(t *testing.T) {
	g := newPlaying(9, 9, Walls, Point{8, 0}, Right, Point{0, 8})
	if !g.Turn(Up) || !g.Turn(Left) {
		t.Fatal("both turns must be queued")
	}
	if r := g.Step(); r.Death != HitWall {
		t.Fatalf("result %+v", r)
	}
	g.Restart()
	g.Step()
	if g.Body[0] != (Point{5, 4}) || g.Dir != Right {
		t.Fatalf("leader %v heading %v: the new game must start heading right from the middle", g.Body[0], g.Dir)
	}
}

func TestRestartKeepsSizeModeAndHighAndStartsPlaying(t *testing.T) {
	g := newPlaying(9, 9, Wrap, Point{4, 4}, Right, Point{5, 4})
	g.Step()
	g.Step()
	g.Status, g.Cause = Over, HitHerd
	g.High = 9
	g.Restart()
	if g.Status != Playing || g.Score != 0 || g.Herd() != 1 || g.Cause != Alive || g.W != 9 || g.Mode != Wrap || g.High != 9 || g.Dir != Right {
		t.Fatalf("restart: %+v", g)
	}
	if g.Body[0] != (Point{4, 4}) {
		t.Fatalf("leader %v", g.Body[0])
	}
}

func TestDirHelpers(t *testing.T) {
	for _, d := range []Dir{Up, Right, Down, Left} {
		if d.Opposite().Opposite() != d || d.Opposite() == d {
			t.Errorf("opposite of %v", d)
		}
		a, b := d.Delta(), d.Opposite().Delta()
		if a.X+b.X != 0 || a.Y+b.Y != 0 || (a == Point{}) {
			t.Errorf("delta of %v: %v %v", d, a, b)
		}
	}
	if Up.Delta() != (Point{0, -1}) || Right.Delta() != (Point{1, 0}) {
		t.Fatal("screen coordinates: Up is -Y, Right is +X")
	}
}

func TestStartFacingHeadsThatWayBeforeTheFirstStep(t *testing.T) {
	g := New(9, 9, Walls, 0, &seqRand{})
	g.StartFacing(Down)
	if g.Status != Playing || g.Dir != Down {
		t.Fatalf("status %v dir %v", g.Status, g.Dir)
	}
	head := g.Body[0]
	g.Step()
	if g.Body[0] != (Point{head.X, head.Y + 1}) {
		t.Fatalf("first step went to %v", g.Body[0])
	}
	g.StartFacing(Up) // ignored once running
	if g.Dir != Down {
		t.Fatal("StartFacing changed a running game")
	}
}
