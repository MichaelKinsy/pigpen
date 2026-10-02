package pigrunner

import (
	"math/rand/v2"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/components/pig-play/libraries/gamemcp"
	"github.com/MichaelKinsy/pigpen/components/pig-play/libraries/sprite"
)

// idleComponent is a runner component whose ticker is stopped, so a test advances the game
// itself and nothing moves behind its back.
func idleComponent(t *testing.T) *runnerComponent {
	t.Helper()
	return newIdleComponent()
}

func newIdleComponent() *runnerComponent {
	component := newRunnerComponent(0, sprite.FindVariant(""), func() int { return 24 })
	component.SetInvalidate(nil)
	component.Dispose()
	return component
}

func facts(t *testing.T, g *Game) (map[string]any, map[string]any, map[string]any) {
	t.Helper()
	state := g.agentFacts()
	return state, state["next_obstacle"].(map[string]any), state["pig"].(map[string]any)
}

func TestNextObstacleKindsDistanceAndHeight(t *testing.T) {
	cases := []struct {
		name         string
		ob           Obstacle
		kind         string
		distance     int
		height, wide int
	}{
		{"pie", Obstacle{Kind: ObGround, Label: "pie", X: 60, Size: 1}, "pie_small", 60 + 1 - (pigX + 10), 2, 10},
		{"group of pies", Obstacle{Kind: ObGround, Label: "pie", X: 80, Size: 3}, "pie_small", 80 + 1 - (pigX + 10), 2, 30},
		{"pie crust", Obstacle{Kind: ObGround, Label: "pie-crust", X: 40, Size: 2}, "pie_large", 40 + 1 - (pigX + 10), 2, 20},
		{"flying pie", Obstacle{Kind: ObAir, Label: "flying-pie", X: 70, Size: 1}, "flying_pie_mid", 70 + 1 - (pigX + 10), 13, 10},
		{"touching the pig", Obstacle{Kind: ObGround, Label: "pie", X: pigX, Size: 1}, "pie_small", 0, 2, 10},
	}
	for _, c := range cases {
		g := NewGame(80, 0)
		g.Waiting = false
		g.Obstacles = []Obstacle{c.ob}
		_, next, _ := facts(t, g)
		if next["kind"] != c.kind || next["distance_px"] != c.distance || next["height"] != c.height || next["width_px"] != c.wide {
			t.Errorf("%s: next_obstacle = %v, want kind %s distance %d height %d width %d", c.name, next, c.kind, c.distance, c.height, c.wide)
		}
	}
}

func TestNextObstacleSkipsWhatIsBehindThePig(t *testing.T) {
	g := NewGame(80, 0)
	g.Waiting = false
	_, next, _ := facts(t, g)
	if next["kind"] != "none" || next["distance_px"] != 0 {
		t.Fatalf("empty road: %v", next)
	}
	g.Obstacles = []Obstacle{
		{Kind: ObGround, Label: "pie", X: -4, Size: 1}, // its right edge is past the pig's back
		{Kind: ObAir, Label: "flying-pie", X: 50, Size: 1},
		{Kind: ObGround, Label: "pie", X: 90, Size: 1},
	}
	_, next, _ = facts(t, g)
	if next["kind"] != "flying_pie_mid" {
		t.Fatalf("next obstacle = %v, want the flying pie ahead", next)
	}
}

func TestFactsReportTheRunAndThePig(t *testing.T) {
	g := NewGame(80, 0)
	state, _, pig := facts(t, g)
	if state["running"] != true || state["game_over"] != false || state["score"] != 0 || pig["jumping"] != false || pig["ducking"] != false {
		t.Fatalf("fresh game: %v", state)
	}
	// 6 Dino px/frame at 14/44 scale and 60 frames/s: about 114 playfield px/s.
	if state["speed"] != 115 {
		t.Errorf("speed = %v", state["speed"])
	}

	g.Waiting = true
	if state, _, _ = facts(t, g); state["running"] != false {
		t.Errorf("title screen is running: %v", state)
	}
	g.Waiting, g.Paused = false, true
	if state, _, _ = facts(t, g); state["running"] != false {
		t.Errorf("paused game is running: %v", state)
	}
	g.Paused, g.Over = false, true
	if state, _, _ = facts(t, g); state["running"] != false || state["game_over"] != true {
		t.Errorf("crashed game: %v", state)
	}

	g = NewGame(80, 0)
	g.Jump()
	g.Update()
	if _, _, pig = facts(t, g); pig["jumping"] != true {
		t.Errorf("after Jump: %v", pig)
	}
	g = NewGame(80, 0)
	g.Duck()
	if _, _, pig = facts(t, g); pig["ducking"] != true || pig["jumping"] != false {
		t.Errorf("after Duck: %v", pig)
	}
	g.Score = 42
	if state, _, _ = facts(t, g); state["score"] != 42 {
		t.Errorf("score = %v", state["score"])
	}
}

// Every action goes through the keyboard's input path: the same key press gives the same game.
func TestActionsApplyWhatTheKeyboardWould(t *testing.T) {
	for _, c := range []struct {
		action, key string
		over        bool
	}{
		{"jump", "\x1b[A", false},
		{"duck", "\x1b[B", false},
		{"restart", "r", true},
		{"restart", "r", false},
	} {
		byAgent, byKeys := idleComponent(t), idleComponent(t)
		for _, component := range []*runnerComponent{byAgent, byKeys} {
			component.game.Waiting = false
			component.game.Over = c.over
		}
		if err := byAgent.queueAction(c.action); err != nil {
			t.Fatal(err)
		}
		byAgent.mu.Lock()
		if applied := byAgent.applyQueuedLocked(); !applied {
			t.Errorf("%s: nothing was applied", c.action)
		}
		byAgent.mu.Unlock()
		if _, err := byKeys.HandleInput(c.key); err != nil {
			t.Fatal(err)
		}
		if a, b := byAgent.game.Pig, byKeys.game.Pig; a != b || byAgent.game.Over != byKeys.game.Over {
			t.Errorf("%s (over %v): agent got pig %+v over %v, keyboard got pig %+v over %v", c.action, c.over, a, byAgent.game.Over, b, byKeys.game.Over)
		}
	}
	component := idleComponent(t)
	if err := component.queueAction("fly"); err == nil {
		t.Error("an unknown action was queued")
	}
	if err := component.queueAction("quit"); err == nil {
		t.Error("an agent can quit the user's overlay")
	}
}

func TestActionWaitsForTheNextFrame(t *testing.T) {
	component := idleComponent(t)
	component.game.Waiting = false
	if err := component.queueAction("jump"); err != nil {
		t.Fatal(err)
	}
	if component.game.Pig.VelY != 0 || len(component.queue) != 1 {
		t.Fatalf("the action ran before a frame: velocity %v, queue %d", component.game.Pig.VelY, len(component.queue))
	}
	component.mu.Lock()
	component.applyQueuedLocked()
	component.mu.Unlock()
	if component.game.Pig.VelY >= 0 || len(component.queue) != 0 {
		t.Fatalf("the frame did not apply it: velocity %v, queue %d", component.game.Pig.VelY, len(component.queue))
	}
}

func TestLiveTickerAppliesQueuedActions(t *testing.T) {
	component := newRunnerComponent(0, sprite.FindVariant(""), func() int { return 24 })
	defer component.Dispose()
	component.SetInvalidate(nil)
	component.attachAgent(&gamemcp.Agent{})
	if err := component.queueAction("jump"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, _, pig := facts(t, lockedGame(component)); pig["jumping"] == true {
			return
		}
		time.Sleep(tickRate)
	}
	t.Fatal("the pig did not jump within 2 s of the action")
}

// lockedGame returns a copy of the game, taken under the component's lock, so a test can read
// facts without racing the ticker.
func lockedGame(c *runnerComponent) *Game {
	c.mu.Lock()
	defer c.mu.Unlock()
	copied := *c.game
	copied.Obstacles = append([]Obstacle(nil), c.game.Obstacles...)
	return &copied
}

func TestQueueIsBoundedAndKeepsTheNewestActions(t *testing.T) {
	component := idleComponent(t)
	for range maxQueued {
		if err := component.queueAction("jump"); err != nil {
			t.Fatal(err)
		}
	}
	if err := component.queueAction("duck"); err != nil {
		t.Fatal(err)
	}
	if len(component.queue) != maxQueued || component.queue[len(component.queue)-1].key != actionKeys["duck"] {
		t.Fatalf("queue = %d long, newest %+v", len(component.queue), component.queue[len(component.queue)-1])
	}
}

func TestAttachAgentLeavesTheTitleAndShowsTheHUDLine(t *testing.T) {
	component := idleComponent(t)
	if !component.game.Waiting {
		t.Fatal("a new component should show the title screen")
	}
	if got := strings.Join(component.Render(80), "\n"); strings.Contains(got, "AI playing") {
		t.Fatal("the HUD says an AI plays before one does")
	}
	agent := &gamemcp.Agent{}
	agent.Begin()
	component.attachAgent(agent)
	if component.game.Waiting {
		t.Fatal("game_start left the title screen up")
	}
	agent.Note("jump", new(0.93))
	hud := strings.Join(component.Render(80), "\n")
	if !strings.Contains(hud, "AI playing: last decision jump p=0.93 · decisions 1") {
		t.Fatalf("HUD lacks the agent line:\n%s", hud)
	}
	agent.Note("duck", nil)
	if hud = strings.Join(component.Render(80), "\n"); !strings.Contains(hud, "AI playing: last decision duck · decisions 2") {
		t.Fatalf("HUD did not follow the new decision:\n%s", hud)
	}
	// The line shares the status row with a crash notice, and does not hide it.
	component.game.Over = true
	if hud = strings.Join(component.Render(80), "\n"); !strings.Contains(hud, "CRASHED") || !strings.Contains(hud, "AI playing") {
		t.Fatalf("crash HUD:\n%s", hud)
	}
}

// Actions, snapshots, scores and frames come from different goroutines while the game's own
// ticker runs; run this under -race.
func TestAgentCallsRaceFreeWithTheTicker(t *testing.T) {
	component := newRunnerComponent(0, sprite.FindVariant(""), func() int { return 24 })
	defer component.Dispose()
	component.SetInvalidate(func() {})
	agent := &gamemcp.Agent{}
	agent.Begin()
	component.attachAgent(agent)
	player := &runnerPlayer{c: component, done: make(chan struct{})}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	time.AfterFunc(300*time.Millisecond, func() { close(stop) })
	run := func(f func(i int)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
					f(i)
				}
			}
		}()
	}
	run(func(i int) {
		_ = player.Act([]string{"jump", "duck", "restart"}[i%3])
		agent.Note("jump", nil)
		time.Sleep(time.Millisecond)
	})
	run(func(int) { _ = player.State() })
	run(func(int) { _ = player.Score() })
	run(func(int) { _ = component.Render(80) })
	run(func(int) { _, _ = component.HandleInput("\x1b[A"); time.Sleep(time.Millisecond) })
	wg.Wait()
}

// simulate plays the headless game through the agent path with one decision in flight, as the
// demo script plays: read game_state, decide, send game_act latency frames of wall time later,
// and read the next state as soon as game_act returns, before the next frame applies the
// action. The run is AI-paced, so a held frame passes wall time but no game time. It runs for
// the given seconds of game time and reports the game and how many obstacles the pig left
// behind.
func simulate(seed uint64, seconds, latency int, decide func(map[string]any) string) (g *Game, passed int) {
	c := newIdleComponent()
	c.attachAgent(&gamemcp.Agent{})
	c.game.rng = rand.New(rand.NewPCG(seed, 1))
	pending, due := decide(c.agentState()), max(latency, 1)
	for frame, played := 0, 0; played < seconds*frameHz && !c.game.Over && frame < 100*seconds*frameHz; frame++ {
		if due == frame {
			if err := c.queueAction(pending); err != nil {
				panic(err)
			}
			pending, due = decide(c.agentState()), frame+max(latency, 1)
		}
		c.mu.Lock()
		c.applyQueuedLocked()
		if c.game.Update() {
			played++
		}
		c.mu.Unlock()
	}
	return c.game, max(c.game.spawned-len(c.game.Obstacles), 0)
}

// The fake classifier plays the game from game_state alone, with a decision every three
// frames (50 ms: far slower than the MCP round trip), and survives two minutes of obstacles.
func TestFakeClassifierRuleSurvivesFromTheFactsAlone(t *testing.T) {
	for seed := range uint64(20) {
		g, passed := simulate(seed, 120, 3, func(state map[string]any) string {
			action, _ := fakeJev(state)
			return action
		})
		if g.Over {
			t.Errorf("seed %d: the pig crashed", seed)
		}
		if passed < 20 {
			t.Errorf("seed %d: only %d obstacles passed in two minutes", seed, passed)
		}
	}
}

// The same game kills a pig that never acts, so the test above proves something.
func TestIdlePigCrashes(t *testing.T) {
	crashed := 0
	for seed := range uint64(20) {
		if g, _ := simulate(seed, 120, 3, func(map[string]any) string { return "none" }); g.Over {
			crashed++
		}
	}
	if crashed != 20 {
		t.Fatalf("%d of 20 idle pigs crashed", crashed)
	}
}
