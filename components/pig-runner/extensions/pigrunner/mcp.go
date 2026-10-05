package pigrunner

import (
	"fmt"
	"math"
	"slices"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/components/pig-play/libraries/gamemcp"
	"github.com/MichaelKinsy/pigpen/components/pig-play/libraries/sprite"
)

// The agent interface of PiG Runner. It is opt-in (see gamemcp): nothing here runs until the
// user enables the MCP server.

const (
	mcpServer = "games-runner"
	// mcpName is the game's id for game_start.
	mcpName = "pig-runner"
	// maxQueued bounds the actions waiting for the next frame. A caller faster than the
	// frame rate overflows it, and the oldest actions are dropped: they are the stalest, except the
	// one that answers the run's decision hold.
	maxQueued = 64
)

// The keys an action presses. They go through the same input path as the keyboard.
// "none" presses nothing.
var actionKeys = map[string]string{
	"jump":    "\x1b[A",
	"duck":    "\x1b[B",
	"restart": "r",
	"none":    "",
}

// mcpInstructions say how to play from game_state. They use the state's own words.
const mcpInstructions = `How to play PiG Runner from game_state:
- next_obstacle.kind is pie_small or pie_large (pies on the road: jump over them), flying_pie_low, flying_pie_mid or flying_pie_high (pies in the air: duck under them), or none.
- next_obstacle.time_to_impact_ms is how long until it reaches the pig. decision_window_ms is when to act: jump or duck once time_to_impact_ms is at or below it, never earlier (an early jump lands before the pie).
- While an AI plays, the run waits for you at each obstacle: when time_to_impact_ms reaches decision_window_ms, decide_now turns true and the game holds until your next game_act, which it applies as the run resumes. So a slow answer is in time; a wrong one is not: answer jump for a pie, duck for a flying pie, and none when decide_now is false.
- Play one decision at a time: read game_state, then game_act. An action from a state read before the hold began does not release it.
- running is false after a crash (game_over true): answer restart. game_act stop ends the play and shows the final score.`

func mcpGame() gamemcp.Game {
	return gamemcp.Game{
		Name:           mcpName,
		Description:    "PiG Runner: a pig runs along a road; jump over pies, duck under flying pies. Read game_state, then act with jump, duck or none (restart after a crash, stop to end).",
		Instructions:   mcpInstructions,
		Actions:        []string{"jump", "duck", "restart"},
		ScoreFile:      "state/pig-standard/pigrunner.json",
		SavedHighScore: loadHighScore,
		Open:           openForAgent,
	}
}

// openForAgent opens the same overlay /runner opens, past the title screen, with the agent
// HUD line showing.
func openForAgent(ctx sdk.Context, agent *gamemcp.Agent) (gamemcp.Instance, error) {
	configHome := ctx.ConfigHome()
	component := newRunnerComponent(loadHighScore(configHome), sprite.ActiveVariant(configHome), ctx.Height)
	component.attachAgent(agent)
	player := &runnerPlayer{c: component, done: make(chan struct{}), save: func(high int) error { return saveHighScore(configHome, high) }}
	go func() {
		defer close(player.done)
		if err := show(ctx, component); err != nil {
			ctx.Notify(fmt.Sprintf("PiG Runner: %v", err), "error")
		}
	}()
	return player, nil
}

// attachAgent starts the game for an agent, paced for it, and shows its HUD line.
func (c *runnerComponent) attachAgent(agent *gamemcp.Agent) {
	c.mu.Lock()
	c.agent = agent
	c.game.Start()
	c.game.AIPaced = true
	c.mu.Unlock()
}

// queuedAction is one agent action waiting for the next frame: the key it presses ("" for
// none) and whether it answers the run's decision hold.
type queuedAction struct {
	key     string
	release bool
}

// queueAction asks for a key press on the next frame.
func (c *runnerComponent) queueAction(action string) error {
	key, ok := actionKeys[action]
	if !ok {
		return fmt.Errorf("unknown action %q", action)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.queue) >= maxQueued {
		// The stalest action goes, but never the one that answers the hold: it carries the key that
		// clears the obstacle and the release that resumes the run, and nothing can answer a hold
		// twice. At most one queued action answers.
		drop := slices.IndexFunc(c.queue, func(a queuedAction) bool { return !a.release })
		c.queue = slices.Delete(c.queue, drop, drop+1)
	}
	c.queue = append(c.queue, queuedAction{key: key, release: c.game.answerReleases()})
	return nil
}

// applyQueuedLocked presses the queued keys, in order, at the start of a frame, resumes a
// held run when one of them answers the hold, and reports whether any was waiting.
func (c *runnerComponent) applyQueuedLocked() bool {
	if len(c.queue) == 0 {
		return false
	}
	for _, action := range c.queue {
		if action.key != "" {
			c.inputLocked(action.key)
		}
		if action.release {
			c.game.releaseHold()
		}
	}
	c.queue = c.queue[:0]
	return true
}

// stopForAgent freezes the run where it is, redraws it once for the final-score HUD (a
// frozen run asks for no frame of its own), and returns the final score.
func (c *runnerComponent) stopForAgent() runnerState {
	c.mu.Lock()
	c.queue = c.queue[:0]
	c.game.Stopped, c.game.Holding = true, false
	c.game.HighScore = max(c.game.HighScore, c.game.Score)
	state, invalidate := c.stateLocked(), c.invalidate
	if c.agent != nil {
		c.agent.Stop(gamemcp.Score{Score: state.Score, HighScore: state.HighScore, GameOver: state.GameOver})
	}
	c.mu.Unlock()
	if invalidate != nil {
		invalidate()
	}
	return state
}

type runnerPlayer struct {
	c    *runnerComponent
	done chan struct{}
	save func(highScore int) error
}

// Stop ends the agent's play: the run freezes, the high score is saved and the HUD shows the
// final score until a key closes the overlay.
func (p *runnerPlayer) Stop() gamemcp.Score {
	s := p.c.stopForAgent()
	if p.save != nil {
		if err := p.save(s.HighScore); err != nil {
			p.c.noteSaveError(err)
		}
	}
	return gamemcp.Score{Score: s.Score, HighScore: s.HighScore, GameOver: s.GameOver}
}

func (p *runnerPlayer) Closed() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

func (p *runnerPlayer) Act(action string) error { return p.c.queueAction(action) }

func (p *runnerPlayer) Score() gamemcp.Score {
	s := p.c.State()
	return gamemcp.Score{Score: s.Score, HighScore: s.HighScore, GameOver: s.GameOver}
}

func (p *runnerPlayer) State() map[string]any { return p.c.agentState() }

// agentState is game_state, taken under the game's lock.
func (c *runnerComponent) agentState() map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.game.observeForAgent()
}

// The obstacle kinds game_state reports, in the game's own words: the pies on the road (the
// Dino's small and large cacti) and the flying pies (its pterodactyls), by height.
const (
	kindNone          = "none"
	kindPieSmall      = "pie_small"
	kindPieLarge      = "pie_large"
	kindFlyingPieLow  = "flying_pie_low"
	kindFlyingPieMid  = "flying_pie_mid"
	kindFlyingPieHigh = "flying_pie_high"
)

// agentFacts is what a person watching would see: whether the run is going or crashed, the
// score and speed, the next obstacle and how soon it reaches the pig, what the pig is doing,
// and whether the run holds for the AI's decision. Distances and heights are playfield pixels;
// speed is playfield pixels per second. The caller holds the game's lock.
func (g *Game) agentFacts() map[string]any {
	next := map[string]any{"kind": kindNone, "distance_px": 0, "time_to_impact_ms": 0, "height": 0, "width_px": 0}
	if ob := g.nextObstacle(); ob != nil {
		top, bottom := obBoundsY(*ob)
		kind := kindPieSmall
		switch {
		case ob.Kind == ObGround && ob.Label == "pie-crust":
			kind = kindPieLarge
		case ob.Kind == ObAir:
			switch lift := groundY - bottom; {
			case lift < 6:
				kind = kindFlyingPieLow
			case lift < 12:
				kind = kindFlyingPieMid
			default:
				kind = kindFlyingPieHigh
			}
		}
		next = map[string]any{
			"kind":              kind,
			"distance_px":       distanceToPig(ob),
			"time_to_impact_ms": g.timeToImpactMs(ob),
			"height":            groundY - top,
			"width_px":          ob.width(),
		}
	}
	return map[string]any{
		"running":            !g.Waiting && !g.Over && !g.Paused && !g.Stopped,
		"game_over":          g.Over,
		"stopped":            g.Stopped,
		"score":              g.Score,
		"speed":              int(math.Round(g.pixelSpeed() * frameHz)),
		"next_obstacle":      next,
		"decision_window_ms": decisionWindowMs,
		"decide_now":         g.decideNow(),
		"pig":                map[string]any{"jumping": g.Pig.Y < groundY, "ducking": g.Pig.Ducked > 0},
	}
}
