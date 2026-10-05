package angrypigs

import (
	"fmt"
	"math"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/components/pig-play/libraries/gamemcp"
	"github.com/MichaelKinsy/pigpen/components/pig-play/libraries/sprite"
)

// The agent interface of Angry Pigs. It is opt-in (see gamemcp): nothing here runs until the
// user enables the MCP server.

const (
	mcpServer = "games-angry-pigs"
	// mcpName is the game's id for game_start.
	mcpName = "angry-pigs"
	// maxQueued bounds the actions waiting for the next frame. A caller faster than the
	// frame rate overflows it, and the oldest actions are dropped: they are the stalest.
	maxQueued = 64
)

// The keys an action presses. They go through the same input path as the keyboard: Up and
// Down pull the band (power), Left and Right aim (the angle in degrees).
var actionKeys = map[string]string{
	"power_up":   "\x1b[A",
	"power_down": "\x1b[B",
	"aim_up":     "\x1b[D",
	"aim_down":   "\x1b[C",
	"fire":       " ",
	"next_level": "n",
	"restart":    "r",
	"none":       "",
}

// mcpInstructions say what each action does to the shot, in game_state's words.
const mcpInstructions = `How to play Angry Pigs from game_state:
- aim_landing is where the aimed shot ends: x (world pixels, growing right from the slingshot side), y, and hit, what stops it (bird, wood, stone, ice or ground). landing_vs_target compares it with the nearest bird: "on target", "short by N px" or "long by N px" (landing_vs_target_px is the same, negative when short).
- power_up pulls the band 3 percent further: the shot flies faster, so it lands further away. This is the main way to reach a bird that is too far. power_down brings the landing closer.
- aim_up raises the angle one degree: the arc gets higher (over a tower in the way); above 45 degrees it also lands a little closer. aim_down lowers it: a flatter arc. Angle changes move the landing far less than power does.
- fire launches the pig. Fire only when aim_landing.hit is bird (landing_vs_target is "on target"); otherwise adjust: short means power_up (at full power, aim_up), long means power_down.
- While a pig flies (flying true) wait with none. When level_done is true answer next_level; when game_over is true answer restart, or stop. game_act stop ends the play and shows the final score.`

func mcpGame() gamemcp.Game {
	return gamemcp.Game{
		Name:           mcpName,
		Description:    "Angry Pigs: launch pigs from a slingshot to knock the birds off their towers. Read game_state, then adjust the power (power_up/power_down, three percent: the landing moves further or closer) or the aim (aim_up/aim_down, one degree: the arc rises or flattens) and fire when the aimed shot ends at a bird. next_level and restart follow a result, stop ends the play.",
		Instructions:   mcpInstructions,
		Actions:        []string{"aim_up", "aim_down", "power_up", "power_down", "fire", "next_level", "restart"},
		ScoreFile:      "state/pig-standard/angrypigs.json",
		SavedHighScore: loadHighScore,
		Open:           openForAgent,
	}
}

// openForAgent opens the same overlay /angry-pigs opens, past the title screen, with the
// agent HUD line showing.
func openForAgent(ctx sdk.Context, agent *gamemcp.Agent) (gamemcp.Instance, error) {
	configHome := ctx.ConfigHome()
	component := newComponent(loadHighScore(configHome), sprite.ActiveVariant(configHome), ctx.Height)
	component.attachAgent(agent)
	inst := &player{c: component, done: make(chan struct{}), save: func(high int) error { return saveHighScore(configHome, high) }}
	go func() {
		defer close(inst.done)
		if err := show(ctx, component); err != nil {
			ctx.Notify(fmt.Sprintf("Angry Pigs: %v", err), "error")
		}
	}()
	return inst, nil
}

// attachAgent starts the game for an agent and shows its HUD line.
func (c *component) attachAgent(agent *gamemcp.Agent) {
	c.mu.Lock()
	c.agent = agent
	c.waiting = false
	c.mu.Unlock()
	c.wakeLoop()
}

// queueAction asks for a key press on the next frame. "none" presses nothing.
func (c *component) queueAction(action string) error {
	key, ok := actionKeys[action]
	if !ok {
		return fmt.Errorf("unknown action %q", action)
	}
	if key == "" {
		return nil
	}
	c.mu.Lock()
	if len(c.queue) >= maxQueued {
		c.queue = append(c.queue[:0], c.queue[1:]...)
	}
	c.queue = append(c.queue, key)
	c.mu.Unlock()
	c.wakeLoop()
	return nil
}

// applyQueuedLocked presses the queued keys, in order, at the start of a frame, and reports
// whether any was waiting.
func (c *component) applyQueuedLocked() bool {
	if len(c.queue) == 0 {
		return false
	}
	for _, key := range c.queue {
		c.inputLocked(key)
	}
	c.queue = c.queue[:0]
	return true
}

// stopForAgent freezes the game where it is, redraws it once for the final-score HUD (a frozen
// game asks for no frame of its own), and returns the final score.
func (c *component) stopForAgent() gameState {
	c.mu.Lock()
	c.queue = c.queue[:0]
	c.agentStopped = true
	c.game.highScore = max(c.game.highScore, c.game.score)
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

// noteSaveError shows a failed high-score save on the HUD.
func (c *component) noteSaveError(err error) {
	c.mu.Lock()
	c.saveErr = err
	invalidate := c.invalidate
	c.mu.Unlock()
	if invalidate != nil {
		invalidate()
	}
}

type player struct {
	c    *component
	done chan struct{}
	save func(highScore int) error
}

// Stop ends the agent's play: the game freezes, the high score is saved and the HUD shows the
// final score until a key closes the overlay.
func (p *player) Stop() gamemcp.Score {
	s := p.c.stopForAgent()
	if p.save != nil {
		if err := p.save(s.HighScore); err != nil {
			p.c.noteSaveError(err)
		}
	}
	return gamemcp.Score{Score: s.Score, HighScore: s.HighScore, GameOver: s.GameOver}
}

func (p *player) Closed() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

func (p *player) Act(action string) error { return p.c.queueAction(action) }

func (p *player) Score() gamemcp.Score {
	s := p.c.State()
	return gamemcp.Score{Score: s.Score, HighScore: s.HighScore, GameOver: s.GameOver}
}

func (p *player) State() map[string]any { return p.c.agentState() }

func cellKindName(kind cellKind) string {
	switch kind {
	case cellWood:
		return "wood"
	case cellStone:
		return "stone"
	case cellIce:
		return "ice"
	case cellBird:
		return "bird"
	}
	return ""
}

// agentState is the game's agentFacts under its lock: the pigs and score, the birds and the
// tops of the towers, the pull and aim, where the aimed shot ends, and how the last shot went.
// All but the end of the aimed shot are on screen. That one is the dotted preview carried on
// past its last dot, which a person judges by eye (see the README). x and y are world pixels: x grows
// right from the left edge, y grows up from the ground, and the slingshot rests at
// (44, 15). A target is the centre of its 6-pixel cell.
func (c *component) agentState() map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.game.agentFacts()
}

// agentFacts is the snapshot game_state returns; the caller holds the game's lock.
func (g *game) agentFacts() map[string]any {
	type target = map[string]any
	var birds, tops []target
	for cx := range gridCols {
		topSeen := false
		for cy := gridRows - 1; cy >= 0; cy-- {
			cell := g.grid[cy][cx]
			if cell.kind == cellEmpty {
				continue
			}
			t := target{"x": cx*cellSize + cellSize/2, "y": cy*cellSize + cellSize/2, "material": cellKindName(cell.kind)}
			switch {
			case cell.kind == cellBird:
				birds = append(birds, t)
			case !topSeen:
				tops = append(tops, t)
			}
			topSeen = true
		}
	}
	targets := append(birds, tops...)
	if targets == nil {
		targets = []target{}
	}
	var landing any
	versus, versusPx := "no shot to aim", 0
	if g.canAim() {
		end, hit := g.aimedFlight(func(int, point) bool { return true })
		x := int(math.Round(end.x))
		landing = map[string]any{"x": x, "y": int(math.Round(end.y)), "hit": hit}
		versus, versusPx = landingVersusTarget(x, hit, birds)
	}
	return map[string]any{
		"shots_left":           g.pigsLeft,
		"score":                g.score,
		"level":                g.level + 1,
		"level_done":           g.levelDone,
		"game_over":            g.gameOver,
		"flying":               g.flying,
		"targets":              targets,
		"pig":                  map[string]any{"power": g.power, "angle": g.angle},
		"aim_landing":          landing,
		"landing_vs_target":    versus,
		"landing_vs_target_px": versusPx,
		"last_shot_result":     map[string]any{"outcome": g.lastShot.Outcome, "birds_knocked": g.lastShot.Birds},
	}
}

// landingVersusTarget compares where the aimed shot ends with the nearest bird (the first in
// birds, which run left to right): "on target" when the shot ends at a bird, otherwise "short by
// N px" or "long by N px", and the signed difference, negative when short.
func landingVersusTarget(x int, hit string, birds []map[string]any) (string, int) {
	switch {
	case len(birds) == 0:
		return "no bird left", 0
	case hit == "bird":
		return "on target", 0
	}
	d := x - birds[0]["x"].(int)
	switch {
	case d < 0:
		return fmt.Sprintf("short by %d px", -d), d
	case d > 0:
		return fmt.Sprintf("long by %d px", d), d
	}
	// Level with the bird but stopped by something else, a block above or below it.
	return "short by 0 px", 0
}
