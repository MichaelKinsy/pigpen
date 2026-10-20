package pi_subagents

import "fmt"

// Port of src/runs/shared/spawn-budget.ts.

const maxGrantHistory = 20

// grantRecord is one entry of the grant history.
type grantRecord struct {
	SessionID     string
	Amount        int
	GrantedAt     int64
	PreviousLimit int
	Limit         int
}

// spawnCounters is the original's SubagentState.subagentSpawns. ConfiguredLimit nil is the original's null (unlimited);
// ConfiguredResolved false is its undefined (not yet read from the config).
type spawnCounters struct {
	SessionID          string
	Count              int
	ConfiguredLimit    *int
	ConfiguredResolved bool
	Granted            int
	GrantHistory       []grantRecord
}

// subagentState is the part of the original's SubagentState the spawn budget reads. A null session id is "".
type subagentState struct {
	CurrentSessionID string
	Spawns           *spawnCounters
}

// extensionConfig is the part of the original's ExtensionConfig the run policy reads.
type extensionConfig struct {
	MaxSubagentSpawnsPerSession any
	MaxSubagentSpawnsPerRun     any
	ToolTimeoutMs               any
	TimeoutMs                   any
}

// spawnBudgetSnapshot is the original's SpawnBudgetSnapshot (nil pointers are null).
type spawnBudgetSnapshot struct {
	Used            int
	ConfiguredLimit *int
	Granted         int
	Limit           *int
	Remaining       *int
	GrantRemaining  *int
	GrantHistory    []grantRecord
}

func intPtr(n int) *int { return &n }

func configuredSessionLimit(config extensionConfig, getenv envLookup) *int {
	if n, ok := resolveMaxSubagentSpawnsPerSession(config.MaxSubagentSpawnsPerSession, getenv); ok {
		return intPtr(n)
	}
	return nil
}

func spawnSessionState(state *subagentState, config extensionConfig, sessionID string, getenv envLookup) *spawnCounters {
	c := state.Spawns
	if c == nil || c.SessionID != sessionID {
		c = &spawnCounters{SessionID: sessionID, ConfiguredLimit: configuredSessionLimit(config, getenv), ConfiguredResolved: true, GrantHistory: []grantRecord{}}
		state.Spawns = c
	}
	if !c.ConfiguredResolved {
		c.ConfiguredLimit, c.ConfiguredResolved = configuredSessionLimit(config, getenv), true
	}
	if c.GrantHistory == nil {
		c.GrantHistory = []grantRecord{}
	}
	return c
}

// getSpawnBudgetSnapshot reads (and starts, for a new session) the counters of a session.
func getSpawnBudgetSnapshot(state *subagentState, config extensionConfig, sessionID string, getenv envLookup) spawnBudgetSnapshot {
	c := spawnSessionState(state, config, sessionID, getenv)
	snap := spawnBudgetSnapshot{Used: c.Count, ConfiguredLimit: c.ConfiguredLimit, GrantHistory: append([]grantRecord{}, c.GrantHistory...)}
	if c.ConfiguredLimit != nil {
		snap.Granted = c.Granted
		limit := *c.ConfiguredLimit + c.Granted
		snap.Limit = &limit
		rem := max(0, limit-c.Count)
		snap.Remaining = &rem
		gr := max(0, *c.ConfiguredLimit-c.Granted)
		snap.GrantRemaining = &gr
	}
	return snap
}

func formatSpawnBudgetSummary(s spawnBudgetSnapshot) string {
	if s.Limit == nil {
		return "unlimited"
	}
	return fmt.Sprintf("%d/%d used, %d remaining (configured %d; granted %d; grant allowance %d)", s.Used, *s.Limit, *s.Remaining, *s.ConfiguredLimit, s.Granted, *s.GrantRemaining)
}

func formatSpawnBudget(s spawnBudgetSnapshot) string {
	return "Spawn budget: " + formatSpawnBudgetSummary(s)
}

// preflightSpawnBudget checks that requested spawns fit; the error (empty if they fit) is the original's.
func preflightSpawnBudget(state *subagentState, config extensionConfig, sessionID string, requested int, getenv envLookup) (spawnBudgetSnapshot, string) {
	s := getSpawnBudgetSnapshot(state, config, sessionID, getenv)
	if requested <= 0 || s.Limit == nil || requested <= *s.Remaining {
		return s, ""
	}
	return s, fmt.Sprintf("Subagent spawn limit reached for this session (%d/%d used, %d requested). %d remaining; the declared run cannot fit, so no children were started. Grant budget explicitly from the root interactive session or start a new session.", s.Used, *s.Limit, requested, *s.Remaining)
}

// reserveSpawnBudget counts requested spawns against the session cap.
func reserveSpawnBudget(state *subagentState, config extensionConfig, sessionID string, requested int, getenv envLookup) (spawnBudgetSnapshot, string) {
	checked, errMsg := preflightSpawnBudget(state, config, sessionID, requested, getenv)
	if errMsg != "" || requested <= 0 || checked.Limit == nil {
		return checked, errMsg
	}
	state.Spawns.Count += requested
	return getSpawnBudgetSnapshot(state, config, sessionID, getenv), ""
}

// preflightSpawnBudgetGrant validates a grant request; additional is the raw value (a number).
func preflightSpawnBudgetGrant(state *subagentState, config extensionConfig, sessionID string, additional any, getenv envLookup) (spawnBudgetSnapshot, string) {
	s := getSpawnBudgetSnapshot(state, config, sessionID, getenv)
	if f, _ := asNumber(additional); !isInteger(additional) || f <= 0 {
		return s, "action='grant-spawn-budget' requires additional to be a positive integer."
	}
	if s.ConfiguredLimit == nil || s.Limit == nil {
		return s, "The current session has no configured spawn cap, so it does not need a budget grant."
	}
	f, _ := asNumber(additional)
	if int(f) > *s.GrantRemaining {
		return s, fmt.Sprintf("Spawn budget grant rejected: %d requested but only %d of the original configured limit remains grantable.", int(f), *s.GrantRemaining)
	}
	return s, ""
}

// grantSpawnBudget raises the session cap by additional (at most the configured cap in total), recording the grant.
func grantSpawnBudget(state *subagentState, config extensionConfig, sessionID string, additional any, nowMs int64, getenv envLookup) (spawnBudgetSnapshot, string) {
	checked, errMsg := preflightSpawnBudgetGrant(state, config, sessionID, additional, getenv)
	if errMsg != "" {
		return checked, errMsg
	}
	if checked.Limit == nil {
		return checked, "The current session has no configured spawn cap, so it does not need a budget grant."
	}
	f, _ := asNumber(additional)
	n := int(f)
	c := state.Spawns
	c.Granted += n
	c.GrantHistory = append(c.GrantHistory, grantRecord{SessionID: sessionID, Amount: n, GrantedAt: nowMs, PreviousLimit: *checked.Limit, Limit: *checked.Limit + n})
	if len(c.GrantHistory) > maxGrantHistory {
		c.GrantHistory = c.GrantHistory[len(c.GrantHistory)-maxGrantHistory:]
	}
	return getSpawnBudgetSnapshot(state, config, sessionID, getenv), ""
}
