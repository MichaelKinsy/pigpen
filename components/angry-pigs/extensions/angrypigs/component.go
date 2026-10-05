package angrypigs

import (
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/components/pig-play/libraries/gamemcp"
	"github.com/MichaelKinsy/pigpen/components/pig-play/libraries/pixel"
	"github.com/MichaelKinsy/pigpen/components/pig-play/libraries/sprite"
	"github.com/MichaelKinsy/pigpen/components/pig-play/libraries/termgame"
)

const (
	// frameInterval matches the SDK's render coalescing interval.
	frameInterval = 16 * time.Millisecond
	// idleInterval redraws the drifting clouds when nothing else moves.
	idleInterval = 250 * time.Millisecond
	// maxCatchUp bounds the simulated time after a stall.
	maxCatchUp = 100 * time.Millisecond
	// stepDuration is stepSeconds as a duration.
	stepDuration = time.Second / 120
	// powerStep is the power change per Up or Down press, in percent.
	powerStep = 3
)

type gameState struct {
	Score     int  `json:"score"`
	HighScore int  `json:"highScore"`
	Level     int  `json:"level"`
	GameOver  bool `json:"gameOver"`
}

// component is the full-terminal remote component. Its loop advances the
// physics at a fixed step off the TUI loop, ticking every frame while the
// scene moves and a few times a second while it idles. The SDK coalesces the
// resulting invalidations to its frame interval.
type component struct {
	mu         sync.Mutex
	game       *game
	scene      *scene
	height     func() int
	lastHeight int
	lastFrame  time.Time
	wasActive  bool
	invalidate func()
	stop       chan struct{}
	stopped    chan struct{}
	stopOnce   sync.Once
	wake       chan struct{}
	dragging   bool
	// waiting holds the title screen until the player presses space.
	waiting bool
	// agent and queue serve an AI player (see mcp.go): the queue holds the key presses it
	// asked for, which the next frame applies.
	agent *gamemcp.Agent
	queue []string
	// stopped freezes the game once the agent stopped; saveErr is a failure to save the high
	// score then, shown in the HUD.
	agentStopped bool
	saveErr      error
}

func newComponent(highScore int, variant sprite.Variant, height func() int) *component {
	c := &component{
		game:    newGame(80, highScore),
		scene:   newScene(pixel.NewPalette(sprite.MascotPalette(variant))),
		waiting: true,
		height:  height,
		stop:    make(chan struct{}),
		stopped: make(chan struct{}),
		wake:    make(chan struct{}, 1),
	}
	go c.run()
	return c
}

func (c *component) run() {
	defer close(c.stopped)
	ticker := time.NewTicker(frameInterval)
	defer ticker.Stop()
	interval := frameInterval
	last := time.Now()
	var carry time.Duration
	for {
		select {
		case <-ticker.C:
		case <-c.wake:
		case <-c.stop:
			return
		}
		now := time.Now()
		carry += min(now.Sub(last), maxCatchUp)
		last = now
		height := c.height()

		c.mu.Lock()
		queued := c.applyQueuedLocked()
		stepped := false
		if c.waiting || c.agentStopped {
			carry = 0
		}
		for ; carry >= stepDuration; carry -= stepDuration {
			c.game.step()
			stepped = true
		}
		active := c.game.animating()
		redraw := queued || stepped && (active || c.wasActive || now.Sub(c.lastFrame) >= idleInterval) || height != c.lastHeight
		c.wasActive = active
		if redraw {
			c.lastFrame = now
		}
		c.lastHeight = height
		invalidate := c.invalidate
		c.mu.Unlock()

		if redraw && invalidate != nil {
			invalidate()
		}
		want := idleInterval
		if active {
			want = frameInterval
		}
		if want != interval {
			interval = want
			ticker.Reset(interval)
		}
	}
}

// Render draws the game to fill the overlay.
func (c *component) Render(width int) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	w, h := termgame.Viewport(width, c.height())
	c.game.resize(w)
	c.scene.waiting = c.waiting
	c.scene.agentLine = ""
	if c.agent != nil {
		c.scene.agentLine = c.agent.Line()
		if c.saveErr != nil {
			c.scene.agentLine += " · high score not saved: " + c.saveErr.Error()
		}
	}
	return c.scene.render(c.game, w, h, make([]string, 0, h))
}

func (c *component) HandleInput(data string) (sdk.RemoteComponentResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.inputLocked(data), nil
}

// inputLocked applies one input. The keyboard and an AI player's actions both come here.
func (c *component) inputLocked(data string) sdk.RemoteComponentResult {
	if c.agentStopped {
		// The agent stopped: the final score is on the HUD, and any key closes.
		return sdk.RemoteComponentResult{Done: true, Value: c.stateLocked()}
	}
	if c.waiting {
		key, text := termgame.ParseKey(data)
		switch {
		case key == termgame.KeyEscape || key == termgame.KeyText && (text == 'q' || text == 'Q'):
			return sdk.RemoteComponentResult{Done: true, Value: c.stateLocked()}
		case key == termgame.KeyEnter || key == termgame.KeyText && text == ' ':
			c.waiting = false
			c.wakeLoop()
		}
		return sdk.RemoteComponentResult{}
	}
	if mouse, ok := termgame.ParseMouse(data); ok {
		c.handleMouse(mouse)
		return sdk.RemoteComponentResult{}
	}
	key, text := termgame.ParseKey(data)
	switch {
	case key == termgame.KeyEscape || key == termgame.KeyText && (text == 'q' || text == 'Q'):
		return sdk.RemoteComponentResult{Done: true, Value: c.stateLocked()}
	case key == termgame.KeyUp:
		c.game.aim(0, powerStep)
	case key == termgame.KeyDown:
		c.game.aim(0, -powerStep)
	case key == termgame.KeyLeft:
		c.game.aim(1, 0)
	case key == termgame.KeyRight:
		c.game.aim(-1, 0)
	case key == termgame.KeyEnter || key == termgame.KeyText && text == ' ':
		c.game.launch()
	case key == termgame.KeyText && (text == 'n' || text == 'N'):
		c.game.nextLevel()
	case key == termgame.KeyText && (text == 'r' || text == 'R'):
		c.game.restart()
	default:
		return sdk.RemoteComponentResult{}
	}
	// Input can start motion while the loop idles; wake it for smooth frames.
	c.wakeLoop()
	return sdk.RemoteComponentResult{}
}

// handleMouse aims by dragging, as in the real game: press on the view,
// drag to pull the band back, and release to launch. Terminals report mouse
// input only when the host enables mouse tracking.
func (c *component) handleMouse(m termgame.Mouse) {
	if m.Button != 0 {
		return
	}
	cx, cy := termgame.ViewCell(m)
	x, y := c.scene.world(cx, cy*2+1)
	switch {
	case m.Release:
		if c.dragging {
			c.dragging = false
			c.game.launch()
			c.wakeLoop()
		}
	case m.Motion && !c.dragging:
	default:
		c.dragging = c.game.drag(x, y)
	}
}

func (c *component) wakeLoop() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

func (c *component) SetInvalidate(invalidate func()) {
	c.mu.Lock()
	c.invalidate = invalidate
	c.mu.Unlock()
}

func (c *component) Dispose() {
	c.stopOnce.Do(func() { close(c.stop) })
	<-c.stopped
}

func (c *component) State() gameState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stateLocked()
}

func (c *component) stateLocked() gameState {
	return gameState{
		Score:     c.game.score,
		HighScore: max(c.game.highScore, c.game.score),
		Level:     c.game.level + 1,
		GameOver:  c.game.gameOver,
	}
}
