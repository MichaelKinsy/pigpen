// Package usage aggregates session and child-session token/cost/elapsed metrics
// for /usage, /status, and Phase 2's omh_run_summary and /bots board.
package usage

import (
	"sync"
	"time"
)

// Record tracks token and cost totals for one session or child agent.
type Record struct {
	SessionID    string    `json:"session_id"`
	ParentID     string    `json:"parent_id,omitempty"`
	Model        string    `json:"model,omitempty"`
	InputTokens  int       `json:"input_tokens"`
	OutputTokens int       `json:"output_tokens"`
	Cost         float64   `json:"cost"`
	Turns        int       `json:"turns"`
	ToolCalls    int       `json:"tool_calls"`
	StartedAt    time.Time `json:"started_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Ledger is a thread-safe in-memory usage tracker.
type Ledger struct {
	mu      sync.RWMutex
	records map[string]*Record
}

func NewLedger() *Ledger {
	return &Ledger{records: make(map[string]*Record)}
}

var defaultLedger = NewLedger()

// Default returns the shared process-wide usage ledger.
func Default() *Ledger {
	return defaultLedger
}

// Add records incremental token/cost/turn/tool usage for a session.
func (l *Ledger) Add(sessionID, parentID, model string, inputTok, outputTok int, cost float64, turns, toolCalls int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now().UTC()
	rec, ok := l.records[sessionID]
	if !ok {
		rec = &Record{SessionID: sessionID, ParentID: parentID, Model: model, StartedAt: now}
		l.records[sessionID] = rec
	}
	if model != "" {
		rec.Model = model
	}
	if parentID != "" && rec.ParentID == "" {
		rec.ParentID = parentID
	}
	rec.InputTokens += inputTok
	rec.OutputTokens += outputTok
	rec.Cost += cost
	rec.Turns += turns
	rec.ToolCalls += toolCalls
	rec.UpdatedAt = now
}

// Snapshot returns a copy of all recorded session usage entries.
func (l *Ledger) Snapshot() []Record {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]Record, 0, len(l.records))
	for _, rec := range l.records {
		out = append(out, *rec)
	}
	return out
}

// Totals sums usage across all sessions in the ledger.
func (l *Ledger) Totals() (inputTok, outputTok int, cost float64, turns, toolCalls int) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	for _, rec := range l.records {
		inputTok += rec.InputTokens
		outputTok += rec.OutputTokens
		cost += rec.Cost
		turns += rec.Turns
		toolCalls += rec.ToolCalls
	}
	return
}
