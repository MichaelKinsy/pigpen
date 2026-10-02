// Package snake is the Pig Snake rules: a herd of pigs in a line on a grid.
//
// The leader is Body[0]; every apple it eats adds one more pig to the end of
// the line. The package is pure and deterministic given its random source: no
// timers, no terminal, no PiG.
package snake

import "time"

// Point is a grid cell. X grows to the right, Y grows downwards.
type Point struct{ X, Y int }

// Dir is a heading.
type Dir int

const (
	Up Dir = iota
	Right
	Down
	Left
)

// Opposite is the reverse heading.
func (d Dir) Opposite() Dir { return (d + 2) % 4 }

// Delta is the grid step of one move in this direction.
func (d Dir) Delta() Point {
	switch d {
	case Up:
		return Point{0, -1}
	case Right:
		return Point{1, 0}
	case Down:
		return Point{0, 1}
	}
	return Point{-1, 0}
}

// Mode is how the board edge behaves.
type Mode int

const (
	// Walls end the game when the leader runs into the edge.
	Walls Mode = iota
	// Wrap carries the leader to the opposite edge.
	Wrap
)

func (m Mode) String() string {
	if m == Wrap {
		return "wrap"
	}
	return "walls"
}

// Other is the other mode.
func (m Mode) Other() Mode {
	if m == Wrap {
		return Walls
	}
	return Wrap
}

// Status is the play state.
type Status int

const (
	Waiting Status = iota
	Playing
	Paused
	Over
	Won
)

// Death says why a game ended.
type Death int

const (
	Alive Death = iota
	HitWall
	HitHerd
)

// Rand is the random source for food placement. *math/rand.Rand implements it.
type Rand interface{ Intn(n int) int }

// Result reports one Step.
type Result struct {
	Moved bool
	Ate   bool
	Death Death
}

// maxQueuedTurns bounds the keys buffered between two steps. Two lets a quick
// "up, left" around a corner work without letting a key mash run away.
const maxQueuedTurns = 2

// Game is one Pig Snake game.
type Game struct {
	W, H   int
	Mode   Mode
	Body   []Point // Body[0] is the leader; the rest is the herd in line order
	Dir    Dir     // the heading of the last step
	Food   Point   // the apple; {-1,-1} when the board is full
	Score  int     // apples eaten
	High   int     // best score of this mode
	Status Status
	Cause  Death // why the game is Over

	queue []Dir
	rng   Rand
}

// New returns a waiting game: one leader in the middle facing right.
func New(w, h int, mode Mode, high int, rng Rand) *Game {
	g := &Game{W: w, H: h, Mode: mode, High: high, rng: rng}
	g.reset()
	return g
}

func (g *Game) reset() {
	g.Body = []Point{{g.W / 2, g.H / 2}}
	g.Dir = Right
	g.Score = 0
	g.Cause = Alive
	g.queue = g.queue[:0]
	g.placeFood()
}

// Herd is the number of pigs in the line, the leader included.
func (g *Game) Herd() int { return len(g.Body) }

// Start begins a waiting game.
func (g *Game) Start() {
	if g.Status == Waiting {
		g.Status = Playing
	}
}

// TogglePause pauses a running game and resumes a paused one.
func (g *Game) TogglePause() {
	switch g.Status {
	case Playing:
		g.Status = Paused
	case Paused:
		g.Status = Playing
	}
}

// Turn queues a heading change for the next steps. It refuses (returns false)
// outside play, a repeat of the current heading, a full queue, and, once the
// leader has a herd behind it, a reversal into that herd.
func (g *Game) Turn(d Dir) bool {
	if g.Status != Playing || len(g.queue) >= maxQueuedTurns {
		return false
	}
	last := g.Dir
	if n := len(g.queue); n > 0 {
		last = g.queue[n-1]
	}
	if d == last || len(g.Body) > 1 && d == last.Opposite() {
		return false
	}
	g.queue = append(g.queue, d)
	return true
}

// Step advances the game one cell.
func (g *Game) Step() Result {
	if g.Status != Playing {
		return Result{}
	}
	if len(g.queue) > 0 {
		g.Dir, g.queue = g.queue[0], g.queue[1:]
	}
	delta := g.Dir.Delta()
	next := Point{g.Body[0].X + delta.X, g.Body[0].Y + delta.Y}
	if next.X < 0 || next.X >= g.W || next.Y < 0 || next.Y >= g.H {
		if g.Mode == Walls {
			g.Status, g.Cause = Over, HitWall
			return Result{Death: HitWall}
		}
		next.X = (next.X + g.W) % g.W
		next.Y = (next.Y + g.H) % g.H
	}
	ate := next == g.Food
	// The last pig leaves its cell on this step unless the herd grows.
	solid := g.Body
	if !ate {
		solid = g.Body[:len(g.Body)-1]
	}
	for _, p := range solid {
		if p == next {
			g.Status, g.Cause = Over, HitHerd
			return Result{Death: HitHerd}
		}
	}
	if ate {
		g.Body = append([]Point{next}, g.Body...)
		g.Score++
		g.High = max(g.High, g.Score)
		if len(g.Body) == g.W*g.H {
			g.Status, g.Food = Won, Point{-1, -1}
		} else {
			g.placeFood()
		}
	} else {
		copy(g.Body[1:], g.Body)
		g.Body[0] = next
	}
	return Result{Moved: true, Ate: ate}
}

// Occupied reports whether a pig stands on p.
func (g *Game) Occupied(p Point) bool {
	for _, b := range g.Body {
		if b == p {
			return true
		}
	}
	return false
}

// placeFood puts the apple on a uniformly chosen free cell.
func (g *Game) placeFood() {
	free := g.W*g.H - len(g.Body)
	if free <= 0 {
		g.Food = Point{-1, -1}
		return
	}
	taken := make([]bool, g.W*g.H)
	for _, p := range g.Body {
		if p.X >= 0 && p.X < g.W && p.Y >= 0 && p.Y < g.H {
			taken[p.Y*g.W+p.X] = true
		}
	}
	n := g.rng.Intn(free)
	for i, t := range taken {
		if t {
			continue
		}
		if n == 0 {
			g.Food = Point{i % g.W, i / g.W}
			return
		}
		n--
	}
}

// SetMode picks the edge behaviour before the game starts.
func (g *Game) SetMode(m Mode) bool {
	if g.Status != Waiting {
		return false
	}
	g.Mode = m
	return true
}

// Resize changes the board before the game starts and puts the leader back in
// the middle.
func (g *Game) Resize(w, h int) bool {
	if g.Status != Waiting {
		return false
	}
	g.W, g.H = w, h
	g.reset()
	return true
}

// Restart starts a fresh game on the same board and mode, keeping the high
// score.
func (g *Game) Restart() {
	g.reset()
	g.Status = Playing
}

// Interval is the time between steps for the current score.
func (g *Game) Interval() time.Duration { return Interval(g.Score) }

// Interval is the time between steps for a score: 160 ms with the leader
// alone, 5 ms faster for every pig added, never faster than 70 ms.
func Interval(score int) time.Duration {
	return max(160*time.Millisecond-time.Duration(score)*5*time.Millisecond, 70*time.Millisecond)
}

// StartFacing begins a waiting game already heading d, for a game started by
// a direction key. The leader is alone and has not moved, so any heading is
// safe.
func (g *Game) StartFacing(d Dir) {
	if g.Status == Waiting {
		g.Status, g.Dir = Playing, d
		g.queue = g.queue[:0]
	}
}
