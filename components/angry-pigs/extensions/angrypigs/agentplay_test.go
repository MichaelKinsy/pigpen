package angrypigs

import (
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/components/pig-play/libraries/gamemcp"
)

// classifierLatency is 0.7 s of decision time, what a Jev decision takes in the demo, in
// physics steps.
const classifierLatency = 84

// playWithLatency plays through the agent path (game_state, then game_act on the component's
// queue) with one decision in flight that lands latency steps after its state was read, while
// the game keeps running. It stops when the level is done, the game is over, or after
// maxSeconds of game time.
func playWithLatency(c *component, latency int, maxSeconds float64, decide func(map[string]any) string) (steps, decisions int) {
	pending, due := "", -1
	for ; float64(steps)*stepSeconds < maxSeconds; steps++ {
		if due == steps {
			_ = c.queueAction(pending)
			decisions++
			due = -1
		}
		c.mu.Lock()
		c.applyQueuedLocked()
		c.game.step()
		done := c.game.levelDone || c.game.gameOver
		c.mu.Unlock()
		if done {
			return steps, decisions
		}
		if due < 0 {
			pending, due = decide(c.agentState()), steps+latency
		}
	}
	return steps, decisions
}

// The rule-based player, deciding from the state's words with 0.7 s per decision, clears
// level 1: it knocks out the bird before it runs out of pigs.
func TestRulePlayerWithSlowDecisionsClearsLevelOne(t *testing.T) {
	c := idleComponent(t)
	c.attachAgent(&gamemcp.Agent{})
	steps, decisions := playWithLatency(c, classifierLatency, 120, func(state map[string]any) string {
		action, _ := fakeJev(state)
		return action
	})
	g := c.game
	if !g.levelDone || g.gameOver {
		t.Fatalf("level 1 not cleared after %.1f s, %d decisions: score %d, pigs left %d, %s", float64(steps)*stepSeconds, decisions, g.score, g.pigsLeft, g.lastEvent)
	}
	t.Logf("level 1 cleared in %.1f s with %d decisions, %d pigs used, score %d", float64(steps)*stepSeconds, decisions, levelPigs[0]-g.pigsLeft, g.score)
}

// Firing whenever a shot can be aimed, at the start aim, never reaches the bird: the rule's
// adjustments are what clear the level.
func TestFiringAtTheStartAimDoesNotClearLevelOne(t *testing.T) {
	c := idleComponent(t)
	c.attachAgent(&gamemcp.Agent{})
	playWithLatency(c, classifierLatency, 120, func(state map[string]any) string {
		if state["aim_landing"] != nil {
			return "fire"
		}
		return "none"
	})
	if c.game.levelDone {
		t.Fatal("level 1 cleared without aiming")
	}
}

// landing_vs_target says, in words, where the aimed shot ends against the nearest bird.
func TestLandingVersusTargetInWords(t *testing.T) {
	g := newGame(80, 0)
	state := g.agentFacts()
	landing := state["aim_landing"].(map[string]any)
	bird := state["targets"].([]map[string]any)[0]
	if bird["material"] != "bird" {
		t.Fatalf("first target %v", bird)
	}
	short := bird["x"].(int) - landing["x"].(int)
	if want := "short by " + itoa(short) + " px"; short <= 0 || state["landing_vs_target"] != want || state["landing_vs_target_px"] != -short {
		t.Fatalf("start aim: landing %v, bird %v, landing_vs_target %q (%v), want %q", landing, bird, state["landing_vs_target"], state["landing_vs_target_px"], want)
	}
	// power_up moves the landing further, as the instructions say.
	g.aim(0, powerStep)
	further := g.agentFacts()["aim_landing"].(map[string]any)["x"].(int)
	if further <= landing["x"].(int) {
		t.Fatalf("power_up moved the landing from %v to %d", landing["x"], further)
	}
	for _, c := range []struct {
		x    int
		hit  string
		want string
		px   int
	}{
		{189, "bird", "on target", 0},
		{150, "ground", "short by 39 px", -39},
		{200, "ground", "long by 11 px", 11},
		{180, "wood", "short by 9 px", -9},
	} {
		got, px := landingVersusTarget(c.x, c.hit, []map[string]any{{"x": 189}})
		if got != c.want || px != c.px {
			t.Errorf("landing %d hit %s: %q %d, want %q %d", c.x, c.hit, got, px, c.want, c.px)
		}
	}
	if got, _ := landingVersusTarget(100, "ground", nil); got != "no bird left" {
		t.Errorf("no bird: %q", got)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

// Stop freezes the game, shows the final score on the HUD, and the next key closes the overlay
// with the score.
func TestStopFreezesTheGameAndAnyKeyCloses(t *testing.T) {
	c := idleComponent(t)
	agent := &gamemcp.Agent{}
	agent.Begin()
	c.attachAgent(agent)
	c.game.score = 550
	saved, redraws := -1, 0
	c.SetInvalidate(func() {
		redraws++
		if !agent.Stopped() {
			t.Error("the redraw came before the HUD knew the final score")
		}
	})
	p := &player{c: c, done: make(chan struct{}), save: func(high int) error { saved = high; return nil }}
	final := p.Stop()
	if redraws != 1 {
		t.Fatalf("stop asked for %d redraws, want one for the final-score HUD", redraws)
	}
	if final.Score != 550 || final.HighScore != 550 || saved != 550 {
		t.Fatalf("final %+v, saved %d", final, saved)
	}
	hud := strings.Join(c.Render(100), "\n")
	if !strings.Contains(hud, "AI stopped") || !strings.Contains(hud, "final score 550") {
		t.Fatalf("HUD after stop:\n%s", hud)
	}
	result, err := c.HandleInput("x")
	if err != nil || !result.Done || result.Value.(gameState).Score != 550 {
		t.Fatalf("key after stop: %+v %v", result, err)
	}
}
