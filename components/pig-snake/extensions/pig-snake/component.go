package pig_snake

import (
	"math/rand"
	"sync"
	"time"
	"unicode"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/pig-snake/internal/scene"
	"github.com/MichaelKinsy/pigpen/pig-snake/internal/snake"
	"github.com/MichaelKinsy/pigpen/pig-snake/internal/sprites"
	"github.com/MichaelKinsy/pigpen/pig-snake/internal/termgame"
)

const (
	// tickRate is the resolution of the game clock. The SDK coalesces the
	// invalidations it causes to its own frame interval.
	tickRate = 10 * time.Millisecond
	// maxCatchUp bounds how many steps a late tick may take, so a stalled
	// process does not fast-forward the snake into a wall.
	maxCatchUp = 3
	// firstViewWidth is the width assumed before the host renders once.
	firstViewWidth = 80
)

// options tune a component for tests.
type options struct {
	// manual builds the component without its timer goroutine; the test
	// drives it with advance.
	manual bool
	// speed multiplies the game clock. Zero means 1.
	speed float64
	// rng is the food random source. Nil seeds one from the clock.
	rng snake.Rand
}

// component runs Pig Snake as a full-terminal remote component. A fixed clock
// steps the game off the TUI loop; nothing here waits for, or blocks, the
// agent.
type component struct {
	mu         sync.Mutex
	game       *snake.Game
	src        sprites.Source
	height     func() int
	highs      highScores
	buffers    scene.Buffers
	speed      float64
	carry      time.Duration
	lastHeight int
	invalidate func()
	stop       chan struct{}
	stopped    chan struct{}
	stopOnce   sync.Once
}

func newComponent(highs highScores, src sprites.Source, height func() int) *component {
	return newComponentWith(highs, src, height, options{})
}

func newComponentWith(highs highScores, src sprites.Source, height func() int, opts options) *component {
	if opts.rng == nil {
		opts.rng = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	if opts.speed <= 0 {
		opts.speed = 1
	}
	c := &component{
		src:        src,
		height:     height,
		highs:      highs,
		speed:      opts.speed,
		lastHeight: height(),
		stop:       make(chan struct{}),
		stopped:    make(chan struct{}),
	}
	viewW, viewH := termgame.Viewport(firstViewWidth, c.lastHeight)
	cols, rows, _, ok := scene.GridFor(src.Sizes(), viewW, (viewH-scene.HUDLines)*2)
	if !ok {
		cols, rows = scene.MinCols, scene.MinRows
	}
	c.game = snake.New(cols, rows, snake.Walls, highs.High, opts.rng)
	if opts.manual {
		close(c.stopped)
	} else {
		go c.run()
	}
	return c
}

func (c *component) run() {
	defer close(c.stopped)
	ticker := time.NewTicker(tickRate)
	defer ticker.Stop()
	last := time.Now()
	for {
		select {
		case now := <-ticker.C:
			elapsed := now.Sub(last)
			last = now
			height := c.height()
			c.mu.Lock()
			changed := c.advanceLocked(elapsed) || height != c.lastHeight
			c.lastHeight = height
			invalidate := c.invalidate
			c.mu.Unlock()
			if changed && invalidate != nil {
				invalidate()
			}
		case <-c.stop:
			return
		}
	}
}

// advance moves the game clock forward by elapsed and reports whether the
// picture changed. A game that is not running does not advance.
func (c *component) advance(elapsed time.Duration) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.advanceLocked(elapsed)
}

func (c *component) advanceLocked(elapsed time.Duration) bool {
	if c.game.Status != snake.Playing {
		c.carry = 0
		return false
	}
	interval := time.Duration(float64(c.game.Interval()) / c.speed)
	c.carry = min(c.carry+elapsed, maxCatchUp*interval)
	changed := false
	for c.carry >= interval && c.game.Status == snake.Playing {
		c.carry -= interval
		c.game.Step()
		changed = true
		interval = time.Duration(float64(c.game.Interval()) / c.speed)
	}
	return changed
}

// Render fills the overlay: the playfield above the two HUD lines, or the
// resize hint when the board does not fit. A game that has not started follows
// the terminal size; a running one keeps its board and shrinks its pigs, and is
// paused when even the smallest pigs do not fit.
func (c *component) Render(width int) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	viewW, viewH := termgame.Viewport(width, c.height())
	pixelH := (viewH - scene.HUDLines) * 2
	if c.game.Status == snake.Waiting {
		if cols, rows, _, ok := scene.GridFor(c.src.Sizes(), viewW, pixelH); ok && (cols != c.game.W || rows != c.game.H) {
			c.game.Resize(cols, rows)
		}
	}
	// A running game whose board no longer fits is paused, not left to run: the
	// player enlarges the window and resumes with p instead of finding the herd
	// crashed into a wall.
	if _, fits := scene.Fit(c.src.Sizes(), c.game.W, c.game.H, viewW, pixelH); !fits && c.game.Status == snake.Playing {
		c.game.TogglePause()
	}
	return scene.Render(&c.buffers, c.game, c.src, viewW, viewH, make([]string, 0, viewH))
}

func (c *component) HandleInput(data string) (sdk.RemoteComponentResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key, text := termgame.ParseKey(data)
	letter := unicode.ToLower(text)
	if key != termgame.KeyText {
		letter = 0
	}
	if key == termgame.KeyEscape || letter == 'q' {
		return sdk.RemoteComponentResult{Done: true, Value: c.stateLocked()}, nil
	}
	dir, steer := direction(key, letter)
	g := c.game
	switch g.Status {
	case snake.Waiting:
		switch {
		case steer:
			g.StartFacing(dir)
		case key == termgame.KeyEnter || letter == ' ':
			g.Start()
		case letter == 'm':
			c.syncHighLocked()
			g.SetMode(g.Mode.Other())
			g.High = c.highForLocked(g.Mode)
		}
	case snake.Playing:
		switch {
		case steer:
			g.Turn(dir)
		case letter == 'p':
			g.TogglePause()
		}
	case snake.Paused:
		if letter == 'p' {
			g.TogglePause()
		}
	case snake.Over, snake.Won:
		if letter == 'r' {
			c.syncHighLocked()
			g.Restart()
			c.carry = 0
		}
	}
	return sdk.RemoteComponentResult{}, nil
}

// direction decodes a steering key: arrows, WASD and vi keys.
func direction(key termgame.Key, letter rune) (snake.Dir, bool) {
	switch {
	case key == termgame.KeyUp || letter == 'w' || letter == 'k':
		return snake.Up, true
	case key == termgame.KeyDown || letter == 's' || letter == 'j':
		return snake.Down, true
	case key == termgame.KeyLeft || letter == 'a' || letter == 'h':
		return snake.Left, true
	case key == termgame.KeyRight || letter == 'd' || letter == 'l':
		return snake.Right, true
	}
	return 0, false
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

// State is the score and best scores so far.
func (c *component) State() gameState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stateLocked()
}

func (c *component) stateLocked() gameState {
	c.syncHighLocked()
	g := c.game
	return gameState{
		Score:     g.Score,
		HighScore: c.highs.High,
		WrapHigh:  c.highs.WrapHigh,
		Herd:      g.Herd(),
		Wrap:      g.Mode == snake.Wrap,
		GameOver:  g.Status == snake.Over || g.Status == snake.Won,
	}
}

// syncHighLocked folds the game's high score into the best score of its mode.
func (c *component) syncHighLocked() {
	best := &c.highs.High
	if c.game.Mode == snake.Wrap {
		best = &c.highs.WrapHigh
	}
	*best = max(*best, c.game.High, c.game.Score)
}

func (c *component) highForLocked(mode snake.Mode) int {
	if mode == snake.Wrap {
		return c.highs.WrapHigh
	}
	return c.highs.High
}
