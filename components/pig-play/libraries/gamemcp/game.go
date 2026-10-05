package gamemcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Game is what one Pigpen game offers an agent.
type Game struct {
	// Name is the id game_start takes, for example "pig-runner".
	Name string
	// Description is one sentence: what the game is and what the agent does in it.
	Description string
	// Instructions say how to play well from game_state: what each action does to the
	// state, and when each is right. games_list returns them and the server's MCP
	// instructions carry them.
	Instructions string
	// Actions are the values game_act accepts besides "none" and "stop".
	Actions []string
	// ScoreFile is where the game saves its high score, relative to the config home, for
	// example "state/pig-standard/pigrunner.json". game_score names it.
	ScoreFile string
	// SavedHighScore reads the saved high score; game_score uses it when no game is open.
	SavedHighScore func(configHome string) int
	// Open opens the game's overlay in the TUI, the same overlay the user gets from the
	// game's command, and returns once the game is live. The overlay stays open until the user
	// closes it. The game shows agent.Line() in its HUD.
	Open func(ctx sdk.Context, agent *Agent) (Instance, error)
}

// Score is the result of game_score without the decision count, which the server keeps.
type Score struct {
	Score     int  `json:"score"`
	HighScore int  `json:"high_score"`
	GameOver  bool `json:"game_over"`
}

// Instance is one open game. Its methods are called from the server's goroutines while the
// game's own ticker runs, so each must be safe for concurrent use.
type Instance interface {
	// State is a compact snapshot of what a person watching would see, taken under the game's
	// lock. It must encode as a JSON object.
	State() map[string]any
	// Act queues an action for the game's next frame. It applies what the matching key press
	// would; "none" presses nothing but still counts as the agent's answer (the Runner
	// releases its decision hold on it). The server has checked that action is one of
	// Game.Actions or "none".
	Act(action string) error
	// Stop ends the agent's play: the game stops advancing, saves its high score and shows
	// the final score (it calls the Agent's Stop before it redraws), and the next key press
	// closes the overlay. It returns the final score.
	Stop() Score
	// Score is the current and high score.
	Score() Score
	// Closed reports whether the overlay has been closed, by the user or by the game.
	Closed() bool
}

// Session serves one game's tools. It keeps the one open Instance and the agent's decision
// count.
type Session struct {
	game  Game
	agent *Agent

	mu    sync.Mutex
	ctx   sdk.Context
	hasUI func() bool
	inst  Instance
	// stopped is set once the agent said stop; last is the final score then.
	stopped bool
	last    *Score
}

// NewSession serves game. SetContext must be called before game_start can open the overlay.
func NewSession(game Game) *Session {
	return &Session{game: game, agent: &Agent{}, hasUI: func() bool { return true }}
}

// SetContext sets the retained context game_start opens the overlay with.
func (s *Session) SetContext(ctx sdk.Context) {
	s.mu.Lock()
	s.ctx, s.hasUI = ctx, ctx.HasUI
	s.mu.Unlock()
}

// Agent is the HUD state of the open game.
func (s *Session) Agent() *Agent { return s.agent }

var (
	errNoGame  = errors.New("no game is open: call game_start first")
	errStopped = errors.New("the game is stopped: its overlay shows the final score until a key closes it; game_score reads the score")
)

func (s *Session) open() (Instance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inst == nil || s.inst.Closed() {
		s.inst = nil
		return nil, errNoGame
	}
	return s.inst, nil
}

type info struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Instructions string   `json:"instructions,omitempty"`
	Actions      []string `json:"actions"`
}

func (s *Session) info() info {
	return info{Name: s.game.Name, Description: s.game.Description, Instructions: s.game.Instructions, Actions: append(slices.Clone(s.game.Actions), "none", "stop")}
}

// ServerInstructions are the MCP server's instructions: the description, then how to play.
func (g Game) ServerInstructions() string {
	if g.Instructions == "" {
		return g.Description
	}
	return g.Description + "\n\n" + g.Instructions
}

// Tools are the five MCP tools.
func (s *Session) Tools() []Tool {
	object := func(properties map[string]any, required ...string) map[string]any {
		schema := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
		if len(required) > 0 {
			schema["required"] = required
		}
		return schema
	}
	return []Tool{
		{
			Name:        "games_list",
			Description: "List the games an AI can play here: [{name, description, instructions, actions}]. The instructions say what each action does and when it is right.",
			InputSchema: object(map[string]any{}),
			OutputSchema: object(map[string]any{"games": map[string]any{"type": "array", "items": object(map[string]any{
				"name": map[string]any{"type": "string"}, "description": map[string]any{"type": "string"}, "instructions": map[string]any{"type": "string"},
				"actions": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			})}}),
			Call: func(json.RawMessage) (Result, error) {
				list := []info{s.info()}
				return Result{Value: list, Structured: map[string]any{"games": list}}, nil
			},
		},
		{
			Name:         "game_start",
			Description:  "Open a game's overlay in the terminal, the same one the user gets, and start playing. Returns {ok}.",
			InputSchema:  object(map[string]any{"name": map[string]any{"type": "string", "description": "A game name from games_list."}}, "name"),
			OutputSchema: object(map[string]any{"ok": map[string]any{"type": "boolean"}, "game": map[string]any{"type": "string"}}),
			Call:         s.start,
		},
		{
			Name:        "game_state",
			Description: "A compact snapshot of what a person watching the open game would see: the facts needed to decide the next action.",
			InputSchema: object(map[string]any{}),
			Call: func(json.RawMessage) (Result, error) {
				inst, err := s.open()
				if err != nil {
					return Result{}, err
				}
				state := inst.State()
				return Result{Value: state, Structured: state}, nil
			},
		},
		{
			Name:        "game_act",
			Description: "Apply one action, exactly what the matching key press would do, on the game's next frame. Optional confidence (0 to 1) is shown on the game's HUD. \"stop\" ends the play: the game stops, saves its high score and shows the final score until a key closes the overlay; it returns {ok, stopped, score, high_score, decisions}.",
			InputSchema: object(map[string]any{
				"action":     map[string]any{"type": "string", "enum": s.info().Actions},
				"confidence": map[string]any{"type": "number", "minimum": 0, "maximum": 1, "description": "The classifier's probability for this action."},
			}, "action"),
			OutputSchema: object(map[string]any{
				"ok": map[string]any{"type": "boolean"}, "decisions": map[string]any{"type": "integer"}, "stopped": map[string]any{"type": "boolean"},
				"score": map[string]any{"type": "integer"}, "high_score": map[string]any{"type": "integer"},
			}),
			Call: s.act,
		},
		{
			Name:        "game_score",
			Description: s.scoreDescription(),
			InputSchema: object(map[string]any{
				"wait_closed_ms": map[string]any{"type": "integer", "minimum": 0, "maximum": maxWaitClosed.Milliseconds(), "description": "Wait up to this long for the game's overlay to close (after stop, a key closes it) before answering."},
			}),
			OutputSchema: object(map[string]any{
				"score": map[string]any{"type": "integer"}, "high_score": map[string]any{"type": "integer"},
				"decisions": map[string]any{"type": "integer"}, "game_over": map[string]any{"type": "boolean"},
				"open": map[string]any{"type": "boolean"}, "stopped": map[string]any{"type": "boolean"},
			}),
			Call: s.score,
		},
	}
}

func (s *Session) scoreDescription() string {
	d := "The game's score: {score, high_score, decisions, game_over, open, stopped}. It answers after the game is stopped or closed too: then score is the last game's final score and high_score the saved one. wait_closed_ms waits for the overlay to close first."
	if s.game.ScoreFile != "" {
		d += " The high score is saved in <config home>/" + s.game.ScoreFile + " as {\"highScore\": N}."
	}
	return d
}

// closedPoll is how often game_score looks whether the overlay closed while it waits.
const closedPoll = 50 * time.Millisecond

// maxWaitClosed bounds game_score's wait_closed_ms, well inside the server's write timeout.
const maxWaitClosed = 20 * time.Second

func (s *Session) score(args json.RawMessage) (Result, error) {
	var in struct {
		WaitClosedMs int64 `json:"wait_closed_ms"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return Result{}, fmt.Errorf("game_score arguments: %w", err)
	}
	if in.WaitClosedMs < 0 || in.WaitClosedMs > maxWaitClosed.Milliseconds() {
		return Result{}, fmt.Errorf("wait_closed_ms %d is outside 0 to %d", in.WaitClosedMs, maxWaitClosed.Milliseconds())
	}
	s.mu.Lock()
	waitOn := s.inst
	s.mu.Unlock()
	if waitOn != nil {
		for until := time.Now().Add(time.Duration(in.WaitClosedMs) * time.Millisecond); !waitOn.Closed() && time.Now().Before(until); {
			time.Sleep(closedPoll)
		}
	}
	s.mu.Lock()
	inst, stopped, last, ctx := s.inst, s.stopped, s.last, s.ctx
	if inst != nil && inst.Closed() {
		inst, s.inst = nil, nil
	}
	s.mu.Unlock()
	var sc Score
	switch {
	case inst != nil && !stopped:
		sc = inst.Score()
	case last != nil:
		sc = *last
	default:
		sc = Score{}
	}
	if s.game.SavedHighScore != nil {
		sc.HighScore = max(sc.HighScore, s.game.SavedHighScore(ctx.ConfigHome()))
	}
	out := map[string]any{"score": sc.Score, "high_score": sc.HighScore, "decisions": s.agent.Decisions(), "game_over": sc.GameOver, "open": inst != nil, "stopped": stopped}
	return Result{Value: out, Structured: out}, nil
}

func (s *Session) start(args json.RawMessage) (Result, error) {
	var in struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return Result{}, fmt.Errorf("game_start arguments: %w", err)
	}
	if in.Name != s.game.Name {
		return Result{}, fmt.Errorf("unknown game %q: this server offers %q", in.Name, s.game.Name)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inst != nil && !s.inst.Closed() {
		out := map[string]any{"ok": true, "game": s.game.Name}
		return Result{Value: out, Structured: out}, nil
	}
	if !s.hasUI() {
		return Result{}, errors.New("this session has no terminal UI to open the game in")
	}
	s.agent.Begin()
	inst, err := s.game.Open(s.ctx, s.agent)
	if err != nil {
		return Result{}, err
	}
	s.inst, s.stopped, s.last = inst, false, nil
	out := map[string]any{"ok": true, "game": s.game.Name}
	return Result{Value: out, Structured: out}, nil
}

func (s *Session) act(args json.RawMessage) (Result, error) {
	var in struct {
		Action     string   `json:"action"`
		Confidence *float64 `json:"confidence"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return Result{}, fmt.Errorf("game_act arguments: %w", err)
	}
	if in.Confidence != nil && (*in.Confidence < 0 || *in.Confidence > 1) {
		return Result{}, fmt.Errorf("confidence %v is outside 0 to 1", *in.Confidence)
	}
	if in.Action != "none" && in.Action != "stop" && !slices.Contains(s.game.Actions, in.Action) {
		return Result{}, fmt.Errorf("unknown action %q: use one of %v, none or stop", in.Action, s.game.Actions)
	}
	inst, err := s.open()
	if err != nil {
		return Result{}, err
	}
	s.mu.Lock()
	stopped := s.stopped
	s.mu.Unlock()
	if stopped {
		return Result{}, errStopped
	}
	if in.Action == "stop" {
		return s.stop(inst), nil
	}
	if err := inst.Act(in.Action); err != nil {
		return Result{}, err
	}
	s.agent.Note(in.Action, in.Confidence)
	out := map[string]any{"ok": true, "decisions": s.agent.Decisions()}
	return Result{Value: out, Structured: out}, nil
}

// stop ends the agent's play and keeps the final score for game_score.
func (s *Session) stop(inst Instance) Result {
	final := inst.Stop()
	s.mu.Lock()
	s.stopped, s.last = true, &final
	s.mu.Unlock()
	s.agent.Stop(final)
	out := map[string]any{"ok": true, "stopped": true, "score": final.Score, "high_score": final.HighScore, "decisions": s.agent.Decisions()}
	return Result{Value: out, Structured: out}
}
