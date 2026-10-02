package pigrunner

import (
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/components/pig-play/libraries/gamemcp"
)

// classifierLatency is 0.7 s of decision time, what a Jev decision takes in the demo, in frames.
const classifierLatency = 42

// ruleFromText decides as a text classifier would from the instructions: it reads only the
// state's words (decide_now and the obstacle kind), never numbers it would have to compute.
func ruleFromText(state map[string]any) string {
	action, _ := fakeJev(state)
	return action
}

// With 0.7 s per decision, one decision in flight, the rule-based player survives at least
// 20 s on every seed (here a full minute), and meets obstacles while it does.
func TestRulePlayerWithSlowDecisionsSurvivesTheRun(t *testing.T) {
	for seed := range uint64(500) {
		g, passed := simulate(seed, 60, classifierLatency, ruleFromText)
		if g.Over {
			t.Errorf("seed %d: the pig crashed at score %d", seed, g.Score)
		}
		if passed < 20 {
			t.Errorf("seed %d: only %d obstacles passed in a minute", seed, passed)
		}
	}
}

// The pace is what makes the slow player's decisions count: without the hold, the same
// latency and a rule that acts on time_to_impact_ms (the best it can do) crash nearly always.
func TestWithoutThePaceSlowDecisionsArriveTooLate(t *testing.T) {
	crashed := 0
	for seed := range uint64(20) {
		c := newIdleComponent()
		c.game.Start()
		c.game.rng = seededRNG(seed)
		pending, due := "", -1
		for frame := 0; frame < 30*frameHz && !c.game.Over; frame++ {
			if due == frame {
				_ = c.queueAction(pending)
				due = -1
			}
			c.applyQueuedLocked()
			c.game.Update()
			if due < 0 {
				state := c.game.agentFacts()
				next := state["next_obstacle"].(map[string]any)
				pending = "none"
				if next["kind"] != "none" && next["time_to_impact_ms"].(int) <= 700+decisionWindowMs {
					pending = "jump"
					if strings.HasPrefix(next["kind"].(string), "flying_pie_") {
						pending = "duck"
					}
				}
				due = frame + classifierLatency
			}
		}
		if c.game.Over {
			crashed++
		}
	}
	if crashed < 15 {
		t.Fatalf("only %d of 20 unpaced slow players crashed: the hold proves nothing", crashed)
	}
}

func pacedGameWithPieAt(x float64, label string, kind ObstacleKind) *runnerComponent {
	c := newIdleComponent()
	c.attachAgent(&gamemcp.Agent{})
	// followed: no new obstacle spawns behind it, so the first one stays alone.
	c.game.Obstacles = []Obstacle{{Kind: kind, Label: label, X: x, Size: 1, followed: true}}
	c.game.Tick = dinoClearFrames + 1
	c.game.spawned = 1
	return c
}

// The run holds when the next obstacle reaches the decision window, reports decide_now, and
// stays put however long the AI takes. An action sent from a state read before the hold does
// not release it; the next one does, and lands on the frame the run resumes.
func TestRunHoldsAtTheDecisionWindowUntilTheAnswer(t *testing.T) {
	c := pacedGameWithPieAt(120, "pie", ObGround)
	g := c.game
	state := c.agentState()
	if state["decide_now"] != false || state["decision_window_ms"] != decisionWindowMs {
		t.Fatalf("far pie: %v", state)
	}
	far := state["next_obstacle"].(map[string]any)["time_to_impact_ms"].(int)
	if far <= decisionWindowMs {
		t.Fatalf("time_to_impact_ms %d", far)
	}
	for range 600 {
		g.Update()
	}
	state = c.agentState()
	next := state["next_obstacle"].(map[string]any)
	if state["decide_now"] != true || !g.Holding || next["time_to_impact_ms"].(int) > decisionWindowMs {
		t.Fatalf("at the window: %v", state)
	}
	x := g.Obstacles[0].X
	for range 120 {
		if g.Update() {
			t.Fatal("the held run advanced")
		}
	}
	if g.Obstacles[0].X != x {
		t.Fatal("the pie moved during the hold")
	}

	// A fresh hold that nobody has read yet: a stale none does not release it.
	g.holdSeen = false
	_ = c.queueAction("none")
	c.applyQueuedLocked()
	if !g.Holding {
		t.Fatal("an action read before the hold released it")
	}
	c.agentState()
	_ = c.queueAction("jump")
	// Answered: a state read before the next frame applies the jump asks for nothing more, and
	// a second action does not answer again.
	if state := c.agentState(); state["decide_now"] != false || !g.Holding {
		t.Fatalf("after the answer, before the frame: %v", state)
	}
	if g.answerReleases() {
		t.Fatal("a second answer released the hold again")
	}
	c.applyQueuedLocked()
	if g.Holding || g.Pig.VelY >= 0 {
		t.Fatalf("the answer did not resume the run with a jump: holding %v, pig %+v", g.Holding, g.Pig)
	}
	for range 120 {
		g.Update()
	}
	if g.Over {
		t.Fatal("a jump at the decision window crashed")
	}
}

// The AI's answer decides: none at the window crashes into the pie or the flying pie, and a
// duck runs into a pie. Nothing is played for it.
func TestAWrongAnswerAtTheWindowCrashes(t *testing.T) {
	for _, tc := range []struct {
		label  string
		kind   ObstacleKind
		answer string
	}{
		{"pie", ObGround, "none"},
		{"pie-crust", ObGround, "duck"},
		{"flying-pie", ObAir, "none"},
	} {
		c := pacedGameWithPieAt(120, tc.label, tc.kind)
		for range 600 {
			c.game.Update()
		}
		c.agentState()
		_ = c.queueAction(tc.answer)
		c.applyQueuedLocked()
		for range 120 {
			c.game.Update()
		}
		if !c.game.Over {
			t.Errorf("%s answered %s: the pig survived", tc.label, tc.answer)
		}
	}
}

// Stop freezes the run, keeps the score, shows the final score on the HUD, and the next key
// closes the overlay with the score.
func TestStopFreezesTheRunAndAnyKeyCloses(t *testing.T) {
	c := newIdleComponent()
	agent := &gamemcp.Agent{}
	agent.Begin()
	c.attachAgent(agent)
	for range 300 {
		c.game.Update()
	}
	saved, redraws := -1, 0
	c.SetInvalidate(func() {
		redraws++
		if !agent.Stopped() {
			t.Error("the redraw came before the HUD knew the final score")
		}
	})
	p := &runnerPlayer{c: c, done: make(chan struct{}), save: func(high int) error { saved = high; return nil }}
	final := p.Stop()
	if redraws != 1 {
		t.Fatalf("stop asked for %d redraws, want one for the final-score HUD", redraws)
	}
	if final.Score == 0 || final.Score != c.game.Score || saved != final.HighScore || final.HighScore < final.Score {
		t.Fatalf("final %+v, game score %d, saved %d", final, c.game.Score, saved)
	}
	if c.game.Update() {
		t.Fatal("a stopped run advanced")
	}
	if state := c.agentState(); state["stopped"] != true || state["running"] != false {
		t.Fatalf("state after stop: %v", state)
	}
	lines := strings.Join(c.Render(100), "\n")
	if !strings.Contains(lines, "AI stopped") || !strings.Contains(lines, "final score") {
		t.Fatalf("HUD after stop:\n%s", lines)
	}
	result, err := c.HandleInput("x")
	if err != nil || !result.Done || result.Value.(runnerState).Score != final.Score {
		t.Fatalf("key after stop: %+v %v", result, err)
	}
}

func seededRNG(seed uint64) *rand.Rand { return rand.New(rand.NewPCG(seed, 1)) }

// decisionWindowMs is a contract: an answer at the hold clears every obstacle the game makes
// (each type, each group size, both flying-pie speed offsets) at every speed up to the maximum.
func TestDecisionWindowClearsEveryObstacle(t *testing.T) {
	for _, tc := range []struct {
		label  string
		kind   ObstacleKind
		answer string
		sizes  []int
		offset []float64
	}{
		{"pie", ObGround, "jump", []int{1, 2, 3}, []float64{0}},
		{"pie-crust", ObGround, "jump", []int{1, 2, 3}, []float64{0}},
		{"flying-pie", ObAir, "duck", []int{1}, []float64{-0.8, 0.8}},
	} {
		for _, size := range tc.sizes {
			for _, offset := range tc.offset {
				for speed := dinoSpeed; speed <= dinoMaxSpeed; speed += 0.25 {
					c := newIdleComponent()
					c.attachAgent(&gamemcp.Agent{})
					c.game.Speed = speed
					c.game.Obstacles = []Obstacle{{Kind: tc.kind, Label: tc.label, X: 150, Size: size, SpeedOffset: offset, followed: true}}
					for range 600 {
						if c.game.Update(); c.game.Holding {
							break
						}
					}
					if !c.game.Holding {
						t.Fatalf("%s x%d offset %v speed %.2f: the run never held", tc.label, size, offset, speed)
					}
					c.agentState()
					_ = c.queueAction(tc.answer)
					c.applyQueuedLocked()
					for range 120 {
						c.game.Update()
					}
					// Passed: whatever is left on the road spawned after it, at the field's edge.
					passed := !slices.ContainsFunc(c.game.Obstacles, func(ob Obstacle) bool { return ob.X < 140 })
					if c.game.Over || !passed {
						t.Errorf("%s x%d offset %v speed %.2f: %s at the window did not clear it (over %v, obstacles %+v)", tc.label, size, offset, speed, tc.answer, c.game.Over, c.game.Obstacles)
					}
				}
			}
		}
	}
}

// At top speed too (the minute-long run above stays below it), the slow rule player survives.
func TestRulePlayerWithSlowDecisionsSurvivesTopSpeed(t *testing.T) {
	for seed := range uint64(40) {
		g, _ := simulate(seed, 180, classifierLatency, ruleFromText)
		if g.Over {
			t.Errorf("seed %d: the pig crashed at score %d, speed %.2f", seed, g.Score, g.Speed)
		}
		if g.Speed < dinoMaxSpeed {
			t.Fatalf("seed %d: speed %.2f after three minutes, below the top", seed, g.Speed)
		}
	}
}
