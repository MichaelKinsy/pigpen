package angrypigs

import (
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/components/pig-play/libraries/gamemcp"
	"github.com/MichaelKinsy/pigpen/components/pig-play/libraries/sprite"
)

// idleComponent is a component whose loop is stopped, so a test advances the game itself and
// nothing moves behind its back.
func idleComponent(t *testing.T) *component {
	t.Helper()
	c := newComponent(0, sprite.FindVariant(""), func() int { return 24 })
	c.SetInvalidate(nil)
	c.Dispose()
	c.waiting = false
	c.game.intro = 0
	return c
}

// settle runs the physics until the shot and everything it moved have come to rest.
func settle(g *game) {
	for i := 0; i < 30*120 && (g.flying || g.animating()); i++ {
		g.step()
	}
}

func targetsOf(state map[string]any) []map[string]any { return state["targets"].([]map[string]any) }

func TestFactsOfALevelNobodyHasShotAt(t *testing.T) {
	g := newGame(80, 0)
	state := g.agentFacts()
	if state["shots_left"] != 4 || state["score"] != 0 || state["level"] != 1 || state["flying"] != false || state["level_done"] != false || state["game_over"] != false {
		t.Fatalf("facts = %v", state)
	}
	if pig := state["pig"].(map[string]any); pig["power"] != 70 || pig["angle"] != 45 {
		t.Errorf("pig = %v", pig)
	}
	if last := state["last_shot_result"].(map[string]any); last["outcome"] != "none" || last["birds_knocked"] != 0 {
		t.Errorf("last_shot_result = %v", last)
	}
	// Level one: one bird on a wooden tower three blocks wide. The targets are the bird and
	// the top of each tower column it can see, at the centre of their 6-pixel cells.
	want := []map[string]any{
		{"x": 189, "y": 21, "material": "bird"},
		{"x": 183, "y": 15, "material": "wood"},
		{"x": 195, "y": 15, "material": "wood"},
	}
	got := targetsOf(state)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("targets = %v, want %v", got, want)
	}
}

func TestTargetsListBirdsFirstAndOnlyTheTopOfEachColumn(t *testing.T) {
	g := newGame(80, 0)
	g.loadLevel(1)
	targets := targetsOf(g.agentFacts())
	birds := 0
	for i, tg := range targets {
		if tg["material"] == "bird" {
			birds++
			if i >= g.birdsLeft() {
				t.Errorf("bird listed after a block: %v", targets)
			}
		}
	}
	if birds != g.birdsLeft() || birds != 3 {
		t.Fatalf("%d birds listed, game has %d", birds, g.birdsLeft())
	}
	seen := map[int]bool{}
	for _, tg := range targets {
		if tg["material"] == "bird" {
			continue
		}
		x := tg["x"].(int)
		if seen[x] {
			t.Errorf("two blocks listed in the column at x=%d", x)
		}
		seen[x] = true
	}
	// A knocked-out bird leaves the list.
	g.grid[0][0] = cell{}
	for cy := range g.grid {
		for cx := range g.grid[cy] {
			if g.grid[cy][cx].kind == cellBird {
				g.grid[cy][cx] = cell{}
			}
		}
	}
	for _, tg := range targetsOf(g.agentFacts()) {
		if tg["material"] == "bird" {
			t.Fatalf("a removed bird is still listed: %v", tg)
		}
	}
}

// aim_landing is the preview's flight carried on to its end, past the dots the scene draws.
func TestAimLandingIsWhereThePreviewFlightEnds(t *testing.T) {
	g := newGame(80, 0)
	for _, aim := range []struct{ angle, power int }{{45, 70}, {30, 100}, {60, 40}, {5, 10}, {85, 100}} {
		g.angle, g.power = aim.angle, aim.power
		landing := g.agentFacts()["aim_landing"].(map[string]any)
		dots := g.preview(make([]point, 0, 400))
		if len(dots) == 0 {
			if landing["hit"] != "ground" {
				t.Errorf("%v: no dots yet landing %v", aim, landing)
			}
			continue
		}
		last := dots[len(dots)-1]
		// The dots are one every previewEvery steps; the landing is the step that stopped them.
		slack := 2 * previewEvery * (maxSpeed / 120) * 1.0
		if math.Abs(float64(landing["x"].(int))-last.x) > slack || math.Abs(float64(landing["y"].(int))-last.y) > slack*2 {
			t.Errorf("%v: landing %v is far from the last dot %+v", aim, landing, last)
		}
	}
	g.angle, g.power = 5, 10
	if landing := g.agentFacts()["aim_landing"].(map[string]any); landing["hit"] != "ground" || landing["x"].(int) >= 100 {
		t.Errorf("a weak flat shot lands %v", landing)
	}
	g.angle, g.power = 45, 88
	if landing := g.agentFacts()["aim_landing"].(map[string]any); landing["hit"] != "bird" {
		t.Errorf("the shot that reaches the bird lands %v", landing)
	}
	// The README says so: that shot reaches the bird after the last dot on screen.
	previewDrawn := len(scene{}.preview)
	if dots := g.preview(make([]point, 0, previewDrawn)); len(dots) != previewDrawn {
		t.Errorf("the shot that reaches the bird shows %d of %d dots: it ends within the preview on screen", len(dots), previewDrawn)
	}
	g.angle, g.power = 25, 100
	if landing := g.agentFacts()["aim_landing"].(map[string]any); landing["hit"] != "wood" {
		t.Errorf("a flat shot should meet the wooden tower: %v", landing)
	}
}

func TestNoAimLandingWhenNoShotCanBeAimed(t *testing.T) {
	g := newGame(80, 0)
	g.launch()
	if g.agentFacts()["aim_landing"] != nil || g.agentFacts()["flying"] != true {
		t.Errorf("while flying: %v", g.agentFacts())
	}
	settle(g)
	g.pigsLeft = 0
	if g.agentFacts()["aim_landing"] != nil {
		t.Error("an aim landing with no pig left")
	}
}

func TestLastShotResultNamesHowTheShotEnded(t *testing.T) {
	for _, c := range []struct {
		angle, power int
		outcome      string
		birds        int
	}{
		{5, 10, "landed", 0},
		{20, 100, "hit_structure", 0},
		{35, 100, "off_field", 0},
		{45, 88, "hit_structure", 1},
	} {
		g := newGame(80, 0)
		g.intro = 0
		g.angle, g.power = c.angle, c.power
		g.launch()
		if g.agentFacts()["last_shot_result"].(map[string]any)["outcome"] != "none" {
			t.Errorf("%v: a flying shot already has a result", c)
		}
		settle(g)
		last := g.agentFacts()["last_shot_result"].(map[string]any)
		if last["outcome"] != c.outcome || last["birds_knocked"] != c.birds {
			t.Errorf("angle %d power %d: last_shot_result = %v, want %s with %d birds", c.angle, c.power, last, c.outcome, c.birds)
		}
	}
}

func TestNextLevelResetsTheLastShot(t *testing.T) {
	g := newGame(80, 0)
	g.intro = 0
	g.angle, g.power = 45, 88
	g.launch()
	settle(g)
	if !g.levelDone {
		t.Fatal("level one is not done")
	}
	g.nextLevel()
	if last := g.agentFacts()["last_shot_result"].(map[string]any); last["outcome"] != "none" || last["birds_knocked"] != 0 {
		t.Errorf("a new level starts with last_shot_result %v", last)
	}
}

// Every action goes through the keyboard's input path: the same key press gives the same game.
func TestActionsApplyWhatTheKeyboardWould(t *testing.T) {
	for action, key := range map[string]string{
		"aim_up":     "\x1b[D", // ← raises the angle
		"aim_down":   "\x1b[C", // → lowers it
		"power_up":   "\x1b[A", // ↑ pulls the band back
		"power_down": "\x1b[B",
		"fire":       " ",
		"next_level": "n",
		"restart":    "r",
	} {
		byAgent, byKeys := idleComponent(t), idleComponent(t)
		for _, c := range []*component{byAgent, byKeys} {
			c.game.levelDone = action == "next_level"
			c.game.score = 40
		}
		if err := byAgent.queueAction(action); err != nil {
			t.Fatal(err)
		}
		byAgent.mu.Lock()
		if !byAgent.applyQueuedLocked() {
			t.Errorf("%s: nothing was applied", action)
		}
		byAgent.mu.Unlock()
		if _, err := byKeys.HandleInput(key); err != nil {
			t.Fatal(err)
		}
		a, b := byAgent.game, byKeys.game
		if a.angle != b.angle || a.power != b.power || a.pigsLeft != b.pigsLeft || a.flying != b.flying || a.level != b.level || a.score != b.score {
			t.Errorf("%s: agent angle/power/pigs/flying/level/score = %d/%d/%d/%v/%d/%d, keyboard %d/%d/%d/%v/%d/%d",
				action, a.angle, a.power, a.pigsLeft, a.flying, a.level, a.score, b.angle, b.power, b.pigsLeft, b.flying, b.level, b.score)
		}
	}
	c := idleComponent(t)
	for _, step := range []struct {
		action       string
		angle, power int
	}{{"aim_up", 46, 70}, {"aim_up", 47, 70}, {"power_up", 47, 73}, {"aim_down", 46, 73}, {"power_down", 46, 70}} {
		_ = c.queueAction(step.action)
		c.mu.Lock()
		c.applyQueuedLocked()
		c.mu.Unlock()
		if c.game.angle != step.angle || c.game.power != step.power {
			t.Fatalf("after %s: angle %d power %d, want %d/%d", step.action, c.game.angle, c.game.power, step.angle, step.power)
		}
	}
	if err := c.queueAction("fly"); err == nil {
		t.Error("an unknown action was queued")
	}
	if err := c.queueAction("quit"); err == nil {
		t.Error("an agent can quit the user's overlay")
	}
}

func TestFireWaitsForTheNextFrameThenLaunches(t *testing.T) {
	c := idleComponent(t)
	if err := c.queueAction("fire"); err != nil {
		t.Fatal(err)
	}
	if c.game.flying || c.game.pigsLeft != 4 || len(c.queue) != 1 {
		t.Fatalf("the shot left before a frame: flying %v, pigs %d, queue %d", c.game.flying, c.game.pigsLeft, len(c.queue))
	}
	c.mu.Lock()
	c.applyQueuedLocked()
	c.mu.Unlock()
	if !c.game.flying || c.game.pigsLeft != 3 || len(c.queue) != 0 {
		t.Fatalf("the frame did not fire: flying %v, pigs %d, queue %d", c.game.flying, c.game.pigsLeft, len(c.queue))
	}
	// Aiming while the pig flies changes nothing, as with the keyboard.
	_ = c.queueAction("power_up")
	c.mu.Lock()
	c.applyQueuedLocked()
	c.mu.Unlock()
	if c.game.power != 70 {
		t.Errorf("power moved to %d while the pig flew", c.game.power)
	}
}

func TestLiveLoopAppliesQueuedActions(t *testing.T) {
	c := newComponent(0, sprite.FindVariant(""), func() int { return 24 })
	defer c.Dispose()
	c.SetInvalidate(nil)
	c.attachAgent(&gamemcp.Agent{})
	if err := c.queueAction("fire"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if c.agentState()["shots_left"] == 3 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the shot was not fired within 2 s of the action")
}

func TestQueueIsBoundedAndKeepsTheNewestActions(t *testing.T) {
	c := idleComponent(t)
	for range maxQueued {
		_ = c.queueAction("aim_up")
	}
	_ = c.queueAction("fire")
	if len(c.queue) != maxQueued || c.queue[len(c.queue)-1] != actionKeys["fire"] {
		t.Fatalf("queue = %d long, newest %q", len(c.queue), c.queue[len(c.queue)-1])
	}
}

func TestAttachAgentLeavesTheTitleAndShowsTheHUDLine(t *testing.T) {
	c := newComponent(0, sprite.FindVariant(""), func() int { return 24 })
	c.SetInvalidate(nil)
	c.Dispose()
	if !c.waiting {
		t.Fatal("a new component should show the title screen")
	}
	if got := strings.Join(c.Render(100), "\n"); strings.Contains(got, "AI playing") {
		t.Fatal("the HUD says an AI plays before one does")
	}
	agent := &gamemcp.Agent{}
	agent.Begin()
	c.attachAgent(agent)
	if c.waiting {
		t.Fatal("game_start left the title screen up")
	}
	agent.Note("power_up", new(0.92))
	hud := strings.Join(c.Render(100), "\n")
	if !strings.Contains(hud, "AI playing: last decision power_up p=0.92 · decisions 1") {
		t.Fatalf("HUD lacks the agent line:\n%s", hud)
	}
	agent.Note("fire", nil)
	if hud = strings.Join(c.Render(100), "\n"); !strings.Contains(hud, "AI playing: last decision fire · decisions 2") {
		t.Fatalf("HUD did not follow the new decision:\n%s", hud)
	}
	c.game.levelDone = true
	if hud = strings.Join(c.Render(100), "\n"); !strings.Contains(hud, "LEVEL CLEAR") || !strings.Contains(hud, "AI playing") {
		t.Fatalf("level clear HUD:\n%s", hud)
	}
}

// The fake classifier plays from game_state alone: it takes the shot that the aim preview
// says reaches the bird, and the game scores. The headless engine, no ticker.
func TestFakeClassifierRuleScoresFromTheFactsAlone(t *testing.T) {
	g := newGame(80, 0)
	g.intro = 0
	decisions, fired := 0, 0
	for ; decisions < 200 && !g.gameOver; decisions++ {
		action, _ := fakeJev(g.agentFacts())
		switch action {
		case "aim_up":
			g.aim(1, 0)
		case "aim_down":
			g.aim(-1, 0)
		case "power_up":
			g.aim(0, powerStep)
		case "power_down":
			g.aim(0, -powerStep)
		case "fire":
			g.launch()
			fired++
			settle(g)
		case "next_level":
			g.nextLevel()
		}
	}
	if g.score < birdPoints || g.level < 1 {
		t.Fatalf("score %d level %d after %d decisions", g.score, g.level+1, decisions)
	}
	if fired > levelPigs[0]+levelPigs[1]+levelPigs[2] {
		t.Fatalf("fired %d shots", fired)
	}
	t.Logf("fake classifier: score %d, level %d, %d shots in %d decisions", g.score, g.level+1, fired, decisions)
}

// The same game gives a player that never acts nothing, so the test above proves something.
func TestIdlePlayerScoresNothing(t *testing.T) {
	g := newGame(80, 0)
	for range 200 {
		settle(g)
	}
	if g.score != 0 {
		t.Fatalf("score %d without a shot", g.score)
	}
}

// Actions, snapshots, scores and frames come from different goroutines while the game's own
// loop runs; run this under -race.
func TestAgentCallsRaceFreeWithTheLoop(t *testing.T) {
	c := newComponent(0, sprite.FindVariant(""), func() int { return 24 })
	defer c.Dispose()
	c.SetInvalidate(func() {})
	agent := &gamemcp.Agent{}
	agent.Begin()
	c.attachAgent(agent)
	p := &player{c: c, done: make(chan struct{})}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	time.AfterFunc(400*time.Millisecond, func() { close(stop) })
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
	actions := []string{"aim_up", "aim_down", "power_up", "power_down", "fire", "next_level", "restart"}
	run(func(i int) {
		_ = p.Act(actions[i%len(actions)])
		agent.Note("fire", nil)
		time.Sleep(time.Millisecond)
	})
	run(func(int) { _ = p.State() })
	run(func(int) { _ = p.Score() })
	run(func(int) { _ = c.Render(100) })
	run(func(int) { _, _ = c.HandleInput("\x1b[A"); time.Sleep(time.Millisecond) })
	wg.Wait()
}
