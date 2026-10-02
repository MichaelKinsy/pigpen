package snake

import (
	"math/rand"
	"testing"
)

// Twins in spirit of pigrunner bot_playthrough_test.go and human_bot_test.go
// (PiG d86eb93): bots play whole games through the public rules, and after
// every step the invariants of a herd in a line must hold.

func checkHerd(t *testing.T, g *Game, step int) {
	t.Helper()
	if len(g.Body) != 1+g.Score {
		t.Fatalf("step %d: herd %d but score %d", step, len(g.Body), g.Score)
	}
	seen := map[Point]bool{}
	for i, p := range g.Body {
		if p.X < 0 || p.X >= g.W || p.Y < 0 || p.Y >= g.H {
			t.Fatalf("step %d: pig %d off the board at %v", step, i, p)
		}
		if seen[p] {
			t.Fatalf("step %d: two pigs on %v", step, p)
		}
		seen[p] = true
		if i > 0 {
			q := g.Body[i-1]
			dx, dy := abs(p.X-q.X), abs(p.Y-q.Y)
			if g.Mode == Wrap {
				dx, dy = min(dx, g.W-dx), min(dy, g.H-dy)
			}
			if dx+dy != 1 {
				t.Fatalf("step %d: pig %d at %v is not next to pig %d at %v: the line is broken", step, i, p, i-1, q)
			}
		}
	}
	if g.Status == Playing && (g.Food.X < 0 || seen[g.Food]) {
		t.Fatalf("step %d: food %v is off the board or under the herd", step, g.Food)
	}
}

func abs(v int) int { return max(v, -v) }

// step returns where the leader would go for a heading, and whether that is a legal, non-fatal cell.
func (g *Game) look(d Dir, eating bool) (Point, bool) {
	delta := d.Delta()
	next := Point{g.Body[0].X + delta.X, g.Body[0].Y + delta.Y}
	if next.X < 0 || next.X >= g.W || next.Y < 0 || next.Y >= g.H {
		if g.Mode == Walls {
			return next, false
		}
		next.X, next.Y = (next.X+g.W)%g.W, (next.Y+g.H)%g.H
	}
	solid := g.Body
	if !eating {
		solid = g.Body[:len(g.Body)-1]
	}
	for _, p := range solid {
		if p == next {
			return next, false
		}
	}
	return next, true
}

// botTurn is a careful bot: shortest safe path to the apple, else chase the
// tail, else any safe move.
func botTurn(g *Game) (Dir, bool) {
	type node struct {
		p     Point
		first Dir
	}
	search := func(goal Point) (Dir, bool) {
		seen := map[Point]bool{g.Body[0]: true}
		var queue []node
		for _, d := range []Dir{Up, Right, Down, Left} {
			if len(g.Body) > 1 && d == g.Dir.Opposite() {
				continue
			}
			if next, ok := g.look(d, next2(g, d, goal)); ok {
				seen[next] = true
				queue = append(queue, node{next, d})
			}
		}
		for len(queue) > 0 {
			n := queue[0]
			queue = queue[1:]
			if n.p == goal {
				return n.first, true
			}
			for _, d := range []Dir{Up, Right, Down, Left} {
				delta := d.Delta()
				q := Point{n.p.X + delta.X, n.p.Y + delta.Y}
				if q.X < 0 || q.X >= g.W || q.Y < 0 || q.Y >= g.H {
					if g.Mode == Walls {
						continue
					}
					q.X, q.Y = (q.X+g.W)%g.W, (q.Y+g.H)%g.H
				}
				if seen[q] || (g.Occupied(q) && q != goal) {
					continue
				}
				seen[q] = true
				queue = append(queue, node{q, n.first})
			}
		}
		return 0, false
	}
	if d, ok := search(g.Food); ok {
		return d, true
	}
	if d, ok := search(g.Body[len(g.Body)-1]); ok {
		return d, true
	}
	for _, d := range []Dir{Up, Right, Down, Left} {
		if len(g.Body) > 1 && d == g.Dir.Opposite() {
			continue
		}
		if _, ok := g.look(d, false); ok {
			return d, true
		}
	}
	return 0, false
}

func next2(g *Game, d Dir, goal Point) bool {
	delta := d.Delta()
	return Point{g.Body[0].X + delta.X, g.Body[0].Y + delta.Y} == goal && goal == g.Food
}

func TestBotPlaythroughBuildsALongHerdAndKeepsItInLine(t *testing.T) {
	for _, mode := range []Mode{Walls, Wrap} {
		for seed := int64(1); seed <= 4; seed++ {
			g := New(13, 6, mode, 0, rand.New(rand.NewSource(seed)))
			g.Start()
			for step := 0; step < 4000 && g.Status == Playing; step++ {
				if d, ok := botTurn(g); ok {
					g.Turn(d)
				}
				g.Step()
				checkHerd(t, g, step)
			}
			if g.Score < 10 {
				t.Errorf("%v seed %d: the bot only got a herd of %d (status %v)", mode, seed, g.Herd(), g.Status)
			}
			if g.High != g.Score {
				t.Errorf("%v seed %d: high %d, score %d", mode, seed, g.High, g.Score)
			}
		}
	}
}

func TestANaiveBotHitsTheWallInWallsModeAndRunsStraightForeverInWrap(t *testing.T) {
	g := New(13, 6, Walls, 0, rand.New(rand.NewSource(1)))
	g.Start()
	for step := 0; g.Status == Playing; step++ {
		if step > 100 {
			t.Fatal("straight-line play never ended in walls mode")
		}
		g.Step()
	}
	if g.Cause != HitWall {
		t.Fatalf("cause %v", g.Cause)
	}
	w := New(13, 6, Wrap, 0, rand.New(rand.NewSource(1)))
	w.Start()
	w.Food = Point{-1, -1} // nothing to eat: a straight line never ends in wrap mode
	for range 500 {
		w.Step()
	}
	if w.Status != Playing || w.Body[0].Y != 3 {
		t.Fatalf("status %v head %v", w.Status, w.Body[0])
	}
}

func TestRandomPlayNeverBreaksTheHerd(t *testing.T) {
	for seed := int64(1); seed <= 20; seed++ {
		rng := rand.New(rand.NewSource(seed))
		g := New(8, 6, Mode(seed%2), 0, rng)
		g.Start()
		for step := 0; step < 3000 && g.Status == Playing; step++ {
			g.Turn(Dir(rng.Intn(4)))
			g.Step()
			checkHerd(t, g, step)
		}
	}
}
