package gamemcp

import (
	"math"
	"strconv"
	"sync"
)

// Agent counts the decisions an AI player makes and renders the HUD line that says an agent
// is playing. Games hold one Agent per open game and show Line in their HUD.
type Agent struct {
	mu        sync.Mutex
	active    bool
	decisions int
	last      string
	prob      *float64
	// final is the score the agent stopped at; nil while it plays.
	final *Score
	// waiting is set while the game holds for the agent's decision.
	waiting bool
}

// Begin marks an agent as playing, with no decision yet.
func (a *Agent) Begin() {
	a.mu.Lock()
	a.active, a.decisions, a.last, a.prob, a.final, a.waiting = true, 0, "", nil, nil, false
	a.mu.Unlock()
}

// Stop records the final score; the HUD line shows it from then on.
func (a *Agent) Stop(final Score) {
	a.mu.Lock()
	a.final, a.waiting = &final, false
	a.mu.Unlock()
}

// Waiting tells the HUD that the game holds for the agent's decision.
func (a *Agent) Waiting(waiting bool) {
	a.mu.Lock()
	a.waiting = waiting
	a.mu.Unlock()
}

// Note records one decision. A nil confidence leaves the probability out of the HUD.
func (a *Agent) Note(action string, confidence *float64) {
	a.mu.Lock()
	a.active = true
	a.decisions++
	a.last = action
	a.prob = nil
	if confidence != nil {
		p := *confidence
		a.prob = &p
	}
	a.mu.Unlock()
}

// Decisions is the number of decisions noted since Begin.
func (a *Agent) Decisions() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.decisions
}

// Line is the HUD text, or "" while no agent is playing:
//
//	AI playing: last decision jump p=0.93 · decisions 12
//	AI playing: last decision none · decisions 12 · waiting for the AI's decision
//	AI stopped · final score 41 · high score 57 · decisions 80 · any key closes
func (a *Agent) Line() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.active {
		return ""
	}
	if a.final != nil {
		return "AI stopped · final score " + strconv.Itoa(a.final.Score) + " · high score " + strconv.Itoa(a.final.HighScore) + " · decisions " + strconv.Itoa(a.decisions) + " · any key closes"
	}
	wait := ""
	if a.waiting {
		wait = " · waiting for the AI's decision"
	}
	if a.decisions == 0 {
		return "AI playing: no decision yet · decisions 0" + wait
	}
	line := "AI playing: last decision " + a.last
	if a.prob != nil {
		line += " p=" + strconv.FormatFloat(math.Round(*a.prob*100)/100, 'f', -1, 64)
	}
	return line + " · decisions " + strconv.Itoa(a.decisions) + wait
}

// Stopped reports whether the agent stopped playing.
func (a *Agent) Stopped() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.final != nil
}
