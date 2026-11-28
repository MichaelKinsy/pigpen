package advisor

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/surface"
)

// upstream: packages/coding-agent/src/advisor/runtime.ts
// upstream: packages/coding-agent/src/session/session-advisors.ts
// upstream: packages/coding-agent/src/slash-commands/builtin-collaboration.ts:63-179
// upstream: packages/coding-agent/src/prompts/advisor/system.md

// Cross-package hook variables wired by the integrator (`wiring.go`).
var (
	// Setting resolves omp settings (e.g. `advisor.enabled`, `advisor.reviewMode`).
	Setting func(ctx sdk.Context, key string) any

	// SetSetting updates a session-scoped or persisted setting (`settings.Set`).
	SetSetting func(ctx sdk.Context, key string, value any, persist bool) error

	// ResolveModelRole resolves a role selector (e.g. `"@advisor"`, `"@slow"`).
	ResolveModelRole func(ctx sdk.Context, selector string) (provider, id, thinking string, ok bool)

	// IsSubagent reports whether ctx belongs to a spawned child subagent session.
	// Advisors never attach to subagents when `scope != "all"`.
	IsSubagent func(ctx sdk.Context) bool
	// RegisterChildSession registers a spawned advisor child session before
	// BindExtensions so parent/subagent guards and tool scoping recognize it.
	RegisterChildSession func(childSessionID, parentSessionID string, explicitTools []string)

	// UnregisterChildSession unregisters a disposed advisor child session.
	UnregisterChildSession func(childSessionID string)

	// AgentDirForContext resolves the user agent directory for ctx.
	AgentDirForContext func(ctx sdk.Context) string

	// IsProjectTrusted reports whether project-level WATCHDOG.yml/WATCHDOG.md
	// files under cwd are trusted. When nil, defaults to true.
	IsProjectTrusted func(ctx sdk.Context) bool

	// SpawnAdvisorSubagent optionally overrides how an advisor review turn is
	// executed. When nil, the runtime uses `ctx.CreateAgentSession` directly
	// with read-only tools + the child-only `advise` custom tool.
	SpawnAdvisorSubagent func(ctx context.Context, sdkCtx sdk.Context, req ReviewRequest) (ReviewResult, error)
)

// ReviewRequest carries the inputs for one background advisor review pass.
type ReviewRequest struct {
	AdvisorName   string
	AdvisorSlug   string
	Provider      string
	ModelID       string
	ThinkingLevel string
	Tools         []string
	SystemPrompt  string
	DeltaPrompt   string
	CoveredTurn   int
	Guard         *EmissionGuard
	OnNote        func(Note)
}

// ReviewResult reports the outcome and token/cost accounting of one review pass.
type ReviewResult struct {
	AssistantText string
	InputTokens   int
	OutputTokens  int
	Cost          float64
	QuotaHit      bool
}

const (
	// fusedExtensionPath matches internal/agents.FusedExtensionPath.
	fusedExtensionPath = "builtin:oh-my-pig"

	// maxHistoryEntries caps in-memory transcript entries per advisor.
	maxHistoryEntries = 256

	// advisorReviewTimeout bounds a single background review call.
	advisorReviewTimeout = 90 * time.Second

	// endSessionWaitTimeout bounds how long endSession waits for in-flight
	// advisor goroutines so lifecycle.End never deadlocks on a hung child.
	endSessionWaitTimeout = 2 * time.Second

	// maxDeltaEntryRunes and maxDeltaTotalRunes bound review delta prompts.
	maxDeltaEntryRunes = 4096
	maxDeltaTotalRunes = 24 * 1024
)

// advisorSystemPrompt mirrors packages/coding-agent/src/prompts/advisor/system.md.
const advisorSystemPromptBase = `User, code-quality, robustness advocate; peer-shadow main agent.
- Sharpen strategy, problem-solving, judgment; identify cleaner approach.
- Challenge premature "done", thin verification, skipped reasoning.
- Enforce user ask; flag drift immediately.
- Prevent rabbit holes, overthinking, baked-in edge cases.

Cover skipped angles; NEVER re-run reasoning agent already has. Advise before wrong-direction work.

<workflow>
Receive incremental agent transcript, including thoughts.
Verify suspicions with session-granted tools. Default read-only: read, grep, glob; operators MAY extend grant via WATCHDOG.yml.
Per advise: 2–3 tool calls. Critical bugs MAY need deeper verification before a blocker.
</workflow>

<communication>
- Surface commentary via advise: max %d non-blockers/update (blocker exempt).
- Silence preferred when agent on track.
- Address agent directly; offer alternatives, not lectures.
- NEVER restate information agent has, including seen errors.
- NEVER repeat prior advice or send identical advice twice.
</communication>`

// Quarantine hazard patterns matching advisor/runtime.ts:137-148.
var outputHazardPatterns = []struct {
	label string
	re    *regexp.Regexp
}{
	{"account-deletion claim", regexp.MustCompile(`(?i)\buser\b.{0,80}\b(?:deleted|erased)\b.{0,80}\baccount\b`)},
	{"instruction override", regexp.MustCompile(`(?i)\bignore\s+(?:all\s+)?(?:prior|previous|earlier)\s+(?:user\s+)?instructions\b`)},
	{"destructive shell command", regexp.MustCompile(`(?i)\brm\s+(?:-[a-z]*r[a-z]*\s+-[a-z]*f[a-z]*|-[a-z]*f[a-z]*\s+-[a-z]*r[a-z]*|-[a-z]*(?:rf|fr)[a-z]*)\b`)},
	{"denial instruction", regexp.MustCompile(`(?i)\bdeny\s+(?:this|it|the\s+request)\s+if\s+(?:asked|questioned)\b`)},
}

// QuarantineUnsafeOutput checks whether generated advisor text contains
// output-only destructive directives absent from sourceText (runtime.ts:164-213).
func QuarantineUnsafeOutput(generatedText, sourceText string) (string, bool) {
	if strings.TrimSpace(generatedText) == "" {
		return "", false
	}
	var matched, outputOnly []string
	for _, h := range outputHazardPatterns {
		if !h.re.MatchString(generatedText) {
			continue
		}
		matched = append(matched, h.label)
		if !h.re.MatchString(sourceText) {
			outputOnly = append(outputOnly, h.label)
		}
	}
	hasMatchedDestructive := false
	hasOutputOverride := false
	hasOutputDestructive := false
	for _, m := range matched {
		if m == "destructive shell command" {
			hasMatchedDestructive = true
		}
	}
	for _, l := range outputOnly {
		if l == "instruction override" {
			hasOutputOverride = true
		}
		if l == "destructive shell command" {
			hasOutputDestructive = true
		}
	}
	if hasMatchedDestructive && hasOutputOverride && !hasOutputDestructive {
		outputOnly = append(outputOnly, "destructive shell command")
		hasOutputDestructive = true
	}
	if hasOutputDestructive || len(outputOnly) >= 3 {
		return "Advisor response quarantined: generated output-only destructive directives: " + strings.Join(outputOnly, ", "), true
	}
	return "", false
}

// advisorInstance holds per-advisor state within a single primary session.
type advisorInstance struct {
	cfg           AdvisorConfig
	provider      string
	modelID       string
	thinking      string
	status        RuntimeStatus
	guard         *EmissionGuard
	recorder      *TranscriptRecorder
	history       []TranscriptEntry
	eligibleCount int
	lastBranchLen int
	inputTokens   int
	outputTokens  int
	cost          float64
	lastError     string
	inFlight      bool
}

type sessionState struct {
	sessionID       string
	cwd             string
	sessionFile     string
	enabledOverride *bool // fallback when SetSetting is nil (e.g. unit tests)
	advisors        []*advisorInstance
	sharedInst      string
	watchdogBlocks  []string
	rosterLoaded    bool
	completedTurns  int
	immuneTurnStart *int
	pendingNotes    []Note
	ended           bool
	ctxCancel       context.CancelFunc
	runCtx          context.Context
	wg              sync.WaitGroup
}

type manager struct {
	mu       sync.Mutex
	sessions map[string]*sessionState
	now      func() int64
}

func newManager() *manager {
	return &manager{
		sessions: make(map[string]*sessionState),
		now:      func() int64 { return time.Now().UnixMilli() },
	}
}

var (
	defaultMu  sync.RWMutex
	defaultMgr = newManager()
)

// Register wires the `/advisor` slash command, turn/agent review hooks, and
// lifecycle cleanup onto e. Safe to call repeatedly per PiG session/reload.
// NEVER registers `advise` as a top-level active tool on primary sessions.
func Register(e *sdk.Extension) {
	defaultMu.RLock()
	m := defaultMgr
	defaultMu.RUnlock()
	registerWithManager(e, m)
}

func registerWithManager(e *sdk.Extension, m *manager) {
	lifecycle.OnEnd("advisor", m.endSession)

	e.RegisterCommand("advisor", sdk.CommandOptions{
		Description: "Toggle the advisor (a second model that reviews each turn and injects notes)",
		GetArgumentCompletions: func(prefix string) ([]sdk.AutocompleteItem, error) {
			if strings.Contains(prefix, " ") {
				return nil, nil
			}
			items := []sdk.AutocompleteItem{
				{Value: "on ", Label: "on", Description: "Enable the advisor"},
				{Value: "off ", Label: "off", Description: "Disable the advisor"},
				{Value: "status ", Label: "status", Description: "Show advisor status"},
				{Value: "dump ", Label: "dump", Description: "Copy the advisor's transcript to clipboard"},
				{Value: "configure ", Label: "configure", Description: "Show WATCHDOG.yml path and configuration schema"},
			}
			lower := strings.ToLower(prefix)
			var out []sdk.AutocompleteItem
			for _, it := range items {
				if strings.HasPrefix(it.Label, lower) {
					out = append(out, it)
				}
			}
			return out, nil
		},
		Handler: m.handleAdvisorCommand,
	})

	e.OnSessionStart(m.onSessionStart)
	e.OnSessionShutdown(m.onSessionShutdown)
	e.OnEvent(sdk.EventBeforeAgentStart, m.onBeforeAgentStart)
	e.OnEvent(sdk.EventTurnEnd, m.onTurnEnd)
	e.OnEvent(sdk.EventAgentEnd, m.onAgentEnd)
}

func strictSessionID(ctx sdk.Context) string {
	id, err := ctx.GetSessionID()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(id)
}

func isSubagentSession(ctx sdk.Context) bool {
	return IsSubagent != nil && IsSubagent(ctx)
}

func readBoolSetting(ctx sdk.Context, key string, fallback bool) bool {
	if Setting == nil {
		return fallback
	}
	if b, ok := Setting(ctx, key).(bool); ok {
		return b
	}
	return fallback
}

func readStringSetting(ctx sdk.Context, key, fallback string) string {
	if Setting == nil {
		return fallback
	}
	if s, ok := Setting(ctx, key).(string); ok && strings.TrimSpace(s) != "" {
		return strings.TrimSpace(s)
	}
	return fallback
}

func readIntSetting(ctx sdk.Context, key string, fallback int) int {
	if Setting == nil {
		return fallback
	}
	switch v := Setting(ctx, key).(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		if !math.IsNaN(v) && !math.IsInf(v, 0) {
			return int(math.Floor(v))
		}
	}
	return fallback
}

func (m *manager) isEnabledLocked(ctx sdk.Context, st *sessionState) bool {
	if st != nil && st.enabledOverride != nil && SetSetting == nil {
		return *st.enabledOverride
	}
	return readBoolSetting(ctx, "advisor.enabled", false)
}

func sessionFileString(ctx sdk.Context) string {
	sf, err := ctx.GetSessionFile()
	if err != nil || sf == nil {
		return ""
	}
	return *sf
}

func (m *manager) stateLocked(ctx sdk.Context, sid string) *sessionState {
	st, ok := m.sessions[sid]
	if !ok {
		rctx, cancel := context.WithCancel(context.Background())
		sf := sessionFileString(ctx)
		st = &sessionState{
			sessionID:   sid,
			cwd:         ctx.Cwd(),
			sessionFile: sf,
			runCtx:      rctx,
			ctxCancel:   cancel,
		}
		m.sessions[sid] = st
	}
	return st
}

func (m *manager) onSessionStart(ctx sdk.Context, _ map[string]any) (any, error) {
	if isSubagentSession(ctx) {
		return nil, nil
	}
	sid := strictSessionID(ctx)
	if sid == "" {
		return nil, nil
	}
	if !readBoolSetting(ctx, "advisor.enabled", false) {
		return nil, nil
	}
	sf := sessionFileString(ctx)
	persisted := LoadTranscriptCosts(sf)
	var totalPersisted float64
	for _, c := range persisted {
		totalPersisted += c
	}
	if totalPersisted > 0 {
		publishAdvisorSpend(totalPersisted)
	}
	return nil, nil
}

func (m *manager) onSessionShutdown(ctx sdk.Context, _ map[string]any) (any, error) {
	sid := strictSessionID(ctx)
	if sid != "" {
		m.endSession(sid)
	}
	return nil, nil
}

func (m *manager) endSession(sessionID string) {
	if sessionID == "" {
		return
	}
	m.mu.Lock()
	st := m.sessions[sessionID]
	if st != nil {
		st.ended = true
	}
	delete(m.sessions, sessionID)
	m.mu.Unlock()
	if st == nil {
		return
	}
	if st.ctxCancel != nil {
		st.ctxCancel()
	}
	done := make(chan struct{})
	go func() {
		st.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(endSessionWaitTimeout):
	}
}

func resolveAgentDir(ctx sdk.Context) string {
	if AgentDirForContext != nil {
		if d := strings.TrimSpace(AgentDirForContext(ctx)); d != "" {
			return d
		}
	}
	return DefaultAgentDir()
}

func isProjectTrusted(ctx sdk.Context) bool {
	if IsProjectTrusted == nil {
		return true
	}
	return IsProjectTrusted(ctx)
}

type rosterSnapshot struct {
	cwd             string
	sessionFile     string
	persistedCosts  map[string]float64
	discovered      DiscoveredAdvisors
	watchdogBlocks  []string
	defaultBudget   int
	defaultMode     ReviewMode
	defaultInterval int
	resolvedModels  []resolvedModelInfo
}

type resolvedModelInfo struct {
	provider string
	modelID  string
	thinking string
	ok       bool
}

func loadRosterSnapshot(ctx sdk.Context, cwd, sf string) rosterSnapshot {
	if cwd == "" {
		cwd = ctx.Cwd()
	}
	if sf == "" {
		sf = sessionFileString(ctx)
	}
	agentDir := resolveAgentDir(ctx)
	trusted := isProjectTrusted(ctx)
	persistedCosts := LoadTranscriptCosts(sf)
	discovered := DiscoverAdvisorConfigsWithTrust(cwd, agentDir, trusted)
	watchdogBlocks := DiscoverWatchdogFilesWithTrust(cwd, agentDir, trusted)

	defaultBudget := readIntSetting(ctx, "advisor.maxNotesPerUpdate", DefaultBudgetPerUpdate)
	if discovered.SharedMaxNotesPerUpdate > 0 {
		defaultBudget = discovered.SharedMaxNotesPerUpdate
	}
	defaultMode := ReviewMode(readStringSetting(ctx, "advisor.reviewMode", string(ReviewModeTurn)))
	if defaultMode != ReviewModeAgentEnd {
		defaultMode = ReviewModeTurn
	}
	defaultInterval := readIntSetting(ctx, "advisor.reviewInterval", 1)
	if defaultInterval < 1 {
		defaultInterval = 1
	}

	configs := discovered.Advisors
	if len(configs) == 0 {
		configs = []AdvisorConfig{{
			Name:           "Advisor",
			Slug:           "",
			ReviewMode:     defaultMode,
			ReviewInterval: defaultInterval,
		}}
	}
	discovered.Advisors = configs

	resolved := make([]resolvedModelInfo, len(configs))
	for i, cfg := range configs {
		p, id, th, ok := resolveAdvisorModel(ctx, cfg.Model)
		resolved[i] = resolvedModelInfo{provider: p, modelID: id, thinking: th, ok: ok}
	}

	return rosterSnapshot{
		cwd:             cwd,
		sessionFile:     sf,
		persistedCosts:  persistedCosts,
		discovered:      discovered,
		watchdogBlocks:  watchdogBlocks,
		defaultBudget:   defaultBudget,
		defaultMode:     defaultMode,
		defaultInterval: defaultInterval,
		resolvedModels:  resolved,
	}
}

// applyRosterLocked merges a pre-loaded rosterSnapshot into st while m.mu is held.
func (m *manager) applyRosterLocked(st *sessionState, snap rosterSnapshot) {
	st.rosterLoaded = true
	st.cwd = snap.cwd
	st.sessionFile = snap.sessionFile
	st.sharedInst = snap.discovered.SharedInstructions
	st.watchdogBlocks = snap.watchdogBlocks

	existingBySlug := make(map[string]*advisorInstance, len(st.advisors))
	for _, a := range st.advisors {
		existingBySlug[a.cfg.Slug] = a
	}

	next := make([]*advisorInstance, 0, len(snap.discovered.Advisors))
	for i, cfg := range snap.discovered.Advisors {
		if cfg.ReviewMode == "" {
			cfg.ReviewMode = snap.defaultMode
		}
		if cfg.ReviewInterval < 1 {
			cfg.ReviewInterval = snap.defaultInterval
		}
		budget := cfg.MaxNotesPerUpdate
		if budget <= 0 {
			budget = snap.defaultBudget
		}

		rm := snap.resolvedModels[i]
		status := StatusRunning
		if cfg.Enabled != nil && !*cfg.Enabled {
			status = StatusPaused
		} else if !rm.ok || rm.modelID == "" {
			status = StatusNoModel
		}

		if prev, found := existingBySlug[cfg.Slug]; found {
			prev.cfg = cfg
			prev.provider = rm.provider
			prev.modelID = rm.modelID
			prev.thinking = rm.thinking
			if prev.status != StatusQuotaExhausted {
				prev.status = status
			}
			next = append(next, prev)
			continue
		}

		inst := &advisorInstance{
			cfg:      cfg,
			provider: rm.provider,
			modelID:  rm.modelID,
			thinking: rm.thinking,
			status:   status,
			guard:    NewEmissionGuard(budget, DefaultHistoryCapacity),
			recorder: NewTranscriptRecorder(snap.sessionFile, st.sessionID, cfg.Slug),
			cost:     snap.persistedCosts[cfg.Slug],
		}
		next = append(next, inst)
	}
	st.advisors = next
}

// ensureRoster ensures st's roster is loaded, performing filesystem and model
// resolution outside m.mu.
func (m *manager) ensureRoster(ctx sdk.Context, sid string, forceReload bool) {
	m.mu.Lock()
	st := m.stateLocked(ctx, sid)
	if st.rosterLoaded && !forceReload {
		m.mu.Unlock()
		return
	}
	cwd := st.cwd
	sf := st.sessionFile
	m.mu.Unlock()

	snap := loadRosterSnapshot(ctx, cwd, sf)

	m.mu.Lock()
	if cur := m.sessions[sid]; cur == st && !cur.ended {
		if !cur.rosterLoaded || forceReload {
			m.applyRosterLocked(cur, snap)
		}
	}
	m.mu.Unlock()
}

func resolveAdvisorModel(ctx sdk.Context, explicit string) (provider, id, thinking string, ok bool) {
	sel := strings.TrimSpace(explicit)
	if sel == "" {
		sel = "@advisor"
	}
	if ResolveModelRole != nil {
		if p, m, th, found := ResolveModelRole(ctx, sel); found && m != "" {
			return p, m, th, true
		}
	}
	// If an explicit `provider/model` string was provided in WATCHDOG.yml, accept it.
	if explicit != "" && !strings.HasPrefix(sel, "@") && !strings.HasPrefix(sel, "pi/") && sel != "*" {
		if slash := strings.IndexByte(sel, '/'); slash > 0 && slash < len(sel)-1 {
			return sel[:slash], sel[slash+1:], "", true
		}
		return "", sel, "", true
	}
	return "", "", "", false
}

func (m *manager) hasActiveAdvisorLocked(st *sessionState) bool {
	for _, a := range st.advisors {
		if a.status == StatusRunning && a.modelID != "" {
			return true
		}
	}
	return false
}

// handleAdvisorCommand implements `/advisor [on|off|toggle|status|dump [raw]|configure]`.
// upstream: packages/coding-agent/src/slash-commands/builtin-collaboration.ts:63-179
func (m *manager) handleAdvisorCommand(ctx sdk.Context, args string) error {
	sid := strictSessionID(ctx)
	if sid == "" {
		ctx.Notify("Advisor requires an active session.", "warning")
		return nil
	}

	verb, rest := parseSubcommand(args)

	switch verb {
	case "", "toggle":
		m.mu.Lock()
		st := m.stateLocked(ctx, sid)
		cur := m.isEnabledLocked(ctx, st)
		m.mu.Unlock()
		return m.setAdvisorEnabled(ctx, sid, !cur)

	case "on":
		return m.setAdvisorEnabled(ctx, sid, true)

	case "off":
		return m.setAdvisorEnabled(ctx, sid, false)

	case "status":
		ctx.Notify(m.formatStatus(ctx, sid), "info")
		return nil

	case "dump":
		isRaw := strings.EqualFold(strings.TrimSpace(rest), "raw")
		text := m.formatHistory(ctx, sid, !isRaw)
		if text == "" {
			ctx.Notify("Advisor has no recorded history for this session.", "info")
			return nil
		}
		if ctx.HasUI() {
			if err := ctx.CopyToClipboard(text); err == nil {
				ctx.Notify("Advisor history copied to clipboard.", "info")
				return nil
			}
		}
		ctx.Notify(text, "info")
		return nil

	case "configure":
		agentDir := resolveAgentDir(ctx)
		trusted := isProjectTrusted(ctx)
		editPath := ResolveEditPath(ctx.Cwd())
		userPath := ResolveEditPath(agentDir)
		discovered := DiscoverAdvisorConfigsWithTrust(ctx.Cwd(), agentDir, trusted)
		var b strings.Builder
		fmt.Fprintf(&b, "Advisor WATCHDOG.yml configuration\n")
		fmt.Fprintf(&b, "  Project path: %s\n", editPath)
		if !trusted {
			b.WriteString("  (project workspace is untrusted — project WATCHDOG files are ignored)\n")
		}
		if userPath != "" {
			fmt.Fprintf(&b, "  User path:    %s\n", userPath)
		}
		if len(discovered.SourcePaths) > 0 {
			fmt.Fprintf(&b, "  Active files: %s\n", strings.Join(discovered.SourcePaths, ", "))
		} else {
			b.WriteString("  Active files: (none — using default single advisor on @advisor role)\n")
		}
		if len(discovered.Warnings) > 0 {
			fmt.Fprintf(&b, "  Warnings:\n    - %s\n", strings.Join(discovered.Warnings, "\n    - "))
		}
		b.WriteString("\nExample WATCHDOG.yml:\n")
		b.WriteString(WatchdogTemplate)
		ctx.Notify(strings.TrimRight(b.String(), "\n"), "info")
		return nil

	default:
		ctx.Notify("Usage: /advisor [on|off|status|dump [raw]|configure]", "info")
		return nil
	}
}

func (m *manager) setAdvisorEnabled(ctx sdk.Context, sid string, enabled bool) error {
	if SetSetting != nil {
		if err := SetSetting(ctx, "advisor.enabled", enabled, false); err != nil {
			ctx.Notify(fmt.Sprintf("Failed to update advisor.enabled: %v", err), "error")
			return nil
		}
	}

	var snap *rosterSnapshot
	if enabled {
		m.mu.Lock()
		st := m.stateLocked(ctx, sid)
		cwd := st.cwd
		sf := st.sessionFile
		m.mu.Unlock()
		s := loadRosterSnapshot(ctx, cwd, sf)
		snap = &s
	}

	m.mu.Lock()
	st := m.stateLocked(ctx, sid)
	val := enabled
	st.enabledOverride = &val
	active := false
	if enabled {
		if snap != nil {
			m.applyRosterLocked(st, *snap)
		}
		active = m.hasActiveAdvisorLocked(st)
	} else {
		// Cancel in-flight background advisor work on disable; keep recorded history.
		if st.ctxCancel != nil {
			st.ctxCancel()
			st.runCtx, st.ctxCancel = context.WithCancel(context.Background())
		}
		st.pendingNotes = nil
	}
	m.mu.Unlock()

	if !enabled {
		ctx.Notify("Advisor disabled.", "info")
		return nil
	}
	if active {
		ctx.Notify("Advisor enabled.", "info")
	} else {
		ctx.Notify("Advisor setting enabled, but no model is assigned to the 'advisor' role.", "info")
	}
	return nil
}

// formatStatus mirrors SessionAdvisors.formatAdvisorStatus (session-advisors.ts:3149-3188).
func (m *manager) formatStatus(ctx sdk.Context, sid string) string {
	m.mu.Lock()
	st := m.stateLocked(ctx, sid)
	configured := m.isEnabledLocked(ctx, st)
	needRoster := configured && !st.rosterLoaded
	m.mu.Unlock()
	if !configured {
		return "Advisor is disabled."
	}
	if needRoster {
		m.ensureRoster(ctx, sid, false)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	st = m.sessions[sid]
	if st == nil || !m.isEnabledLocked(ctx, st) {
		return "Advisor is disabled."
	}
	turns := st.completedTurns
	pending := len(st.pendingNotes)

	if len(st.advisors) == 0 {
		return "Advisor setting is enabled, but no model is assigned to the 'advisor' role."
	}
	if len(st.advisors) == 1 {
		a := st.advisors[0]
		if a.status == StatusNoModel || a.modelID == "" {
			return "Advisor setting is enabled, but no model is assigned to the 'advisor' role."
		}
		if a.status != StatusRunning {
			return fmt.Sprintf("Advisor %q is %s.", a.cfg.Name, strings.ReplaceAll(string(a.status), "_", " "))
		}
		modelLabel := formatModelID(a.provider, a.modelID)
		return fmt.Sprintf(
			"Advisor is enabled (%s). Turns reviewed: %d (pending notes: %d). Spend: %d input, %d output, $%.4f.",
			modelLabel, turns, pending, a.inputTokens, a.outputTokens, a.cost,
		)
	}

	lines := []string{fmt.Sprintf("Advisors enabled (%d):", len(st.advisors))}
	var totalIn, totalOut int
	var totalCost float64
	for _, a := range st.advisors {
		totalIn += a.inputTokens
		totalOut += a.outputTokens
		totalCost += a.cost
		desc := fmt.Sprintf("[%s]", a.status)
		if a.status == StatusRunning && a.modelID != "" {
			desc = fmt.Sprintf("(%s)", formatModelID(a.provider, a.modelID))
		}
		lines = append(lines, fmt.Sprintf("  • %s %s — %s/%d, $%.4f", a.cfg.Name, desc, a.cfg.ReviewMode, a.cfg.ReviewInterval, a.cost))
	}
	lines = append(lines, fmt.Sprintf("Totals: %d input, %d output, $%.4f.", totalIn, totalOut, totalCost))
	return strings.Join(lines, "\n")
}

func (m *manager) formatHistory(ctx sdk.Context, sid string, compact bool) string {
	m.mu.Lock()
	st := m.sessions[sid]
	if st == nil || len(st.advisors) == 0 {
		m.mu.Unlock()
		return ""
	}
	type snap struct {
		name    string
		model   string
		entries []TranscriptEntry
	}
	snaps := make([]snap, 0, len(st.advisors))
	for _, a := range st.advisors {
		if len(a.history) == 0 {
			continue
		}
		cp := make([]TranscriptEntry, len(a.history))
		copy(cp, a.history)
		snaps = append(snaps, snap{
			name:    a.cfg.Name,
			model:   formatModelID(a.provider, a.modelID),
			entries: cp,
		})
	}
	m.mu.Unlock()

	if len(snaps) == 0 {
		return ""
	}
	if len(snaps) == 1 {
		return FormatHistoryText(snaps[0].entries, compact)
	}
	parts := make([]string, 0, len(snaps))
	for _, s := range snaps {
		parts = append(parts, fmt.Sprintf("### Advisor: %s (%s)\n\n%s", s.name, s.model, FormatHistoryText(s.entries, compact)))
	}
	return strings.Join(parts, "\n\n")
}

// onBeforeAgentStart drains any pending `<advisory>` notes collected from
// completed background advisor reviews and injects them as a hidden custom
// message on the next turn. Zero work when advisor.enabled == false.
func (m *manager) onBeforeAgentStart(ctx sdk.Context, _ map[string]any) (any, error) {
	if isSubagentSession(ctx) {
		return nil, nil
	}
	sid := strictSessionID(ctx)
	if sid == "" {
		return nil, nil
	}

	m.mu.Lock()
	st := m.sessions[sid]
	if st == nil || !m.isEnabledLocked(ctx, st) || len(st.pendingNotes) == 0 {
		m.mu.Unlock()
		return nil, nil
	}
	notes := st.pendingNotes
	st.pendingNotes = nil
	curTurn := st.completedTurns
	m.mu.Unlock()

	body := FormatBatchContent(notes, &curTurn)
	if body == "" {
		return nil, nil
	}
	return map[string]any{
		"message": map[string]any{
			"customType": "advisor-notes",
			"content":    body,
			"display":    false,
		},
	}, nil
}

// onTurnEnd increments primary turn counters and triggers `reviewMode == "turn"`
// background advisor reviews when advisor.enabled is true.
func (m *manager) onTurnEnd(ctx sdk.Context, _ map[string]any) (any, error) {
	m.scheduleReviews(ctx, ReviewModeTurn)
	return nil, nil
}

// onAgentEnd triggers `reviewMode == "agent-end"` background advisor reviews
// when advisor.enabled is true.
func (m *manager) onAgentEnd(ctx sdk.Context, _ map[string]any) (any, error) {
	m.scheduleReviews(ctx, ReviewModeAgentEnd)
	return nil, nil
}

func (m *manager) scheduleReviews(ctx sdk.Context, mode ReviewMode) {
	if isSubagentSession(ctx) {
		return
	}
	sid := strictSessionID(ctx)
	if sid == "" {
		return
	}

	// Fast path: when disabled, touch zero session state or branch data.
	m.mu.Lock()
	st := m.sessions[sid]
	if st == nil {
		if !readBoolSetting(ctx, "advisor.enabled", false) {
			m.mu.Unlock()
			return
		}
		st = m.stateLocked(ctx, sid)
	}
	if !m.isEnabledLocked(ctx, st) {
		m.mu.Unlock()
		return
	}
	needRoster := !st.rosterLoaded
	m.mu.Unlock()

	if needRoster {
		m.ensureRoster(ctx, sid, false)
	}

	immuneTurns := readIntSetting(ctx, "advisor.immuneTurns", 3)
	defaultBudget := readIntSetting(ctx, "advisor.maxNotesPerUpdate", DefaultBudgetPerUpdate)

	m.mu.Lock()
	st = m.sessions[sid]
	if st == nil || st.ended || !m.isEnabledLocked(ctx, st) {
		m.mu.Unlock()
		return
	}
	if mode == ReviewModeTurn {
		st.completedTurns++
	}
	curTurn := st.completedTurns
	sharedInst := st.sharedInst
	watchdogBlocks := append([]string(nil), st.watchdogBlocks...)
	runCtx := st.runCtx
	multiAdvisor := len(st.advisors) > 1

	type launchItem struct {
		inst         *advisorInstance
		advisorName  string
		advisorSlug  string
		provider     string
		modelID      string
		thinking     string
		guard        *EmissionGuard
		multiAdvisor bool
		parentSID    string
		startIdx     int
		sysPrompt    string
		tools        []string
		budget       int
		coveredTurn  int
	}
	var toLaunch []launchItem
	for _, a := range st.advisors {
		if a.status != StatusRunning || a.modelID == "" || a.inFlight {
			continue
		}
		if a.cfg.ReviewMode != mode {
			continue
		}
		a.eligibleCount++
		interval := a.cfg.ReviewInterval
		if interval < 1 {
			interval = 1
		}
		if a.eligibleCount%interval != 0 {
			continue
		}
		a.inFlight = true
		var tools []string
		if a.cfg.ToolsExplicit || a.cfg.Tools != nil {
			tools = slices.Clone(a.cfg.Tools)
			if tools == nil {
				tools = []string{}
			}
		} else {
			tools = slices.Clone(DefaultAdvisorToolNames)
		}
		budget := a.cfg.MaxNotesPerUpdate
		if budget <= 0 {
			budget = defaultBudget
		}
		sysPrompt := buildAdvisorSystemPrompt(budget, sharedInst, a.cfg.Instructions, watchdogBlocks)
		toLaunch = append(toLaunch, launchItem{
			inst:         a,
			advisorName:  a.cfg.Name,
			advisorSlug:  a.cfg.Slug,
			provider:     a.provider,
			modelID:      a.modelID,
			thinking:     a.thinking,
			guard:        a.guard,
			multiAdvisor: multiAdvisor,
			parentSID:    sid,
			startIdx:     a.lastBranchLen,
			sysPrompt:    sysPrompt,
			tools:        tools,
			budget:       budget,
			coveredTurn:  curTurn,
		})
	}
	if len(toLaunch) == 0 {
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()

	branch, err := ctx.GetBranch()
	if err != nil || len(branch) == 0 {
		m.mu.Lock()
		for _, item := range toLaunch {
			item.inst.inFlight = false
		}
		m.mu.Unlock()
		return
	}

	type readyItem struct {
		li        launchItem
		deltaText string
		branchLen int
	}
	var ready []readyItem
	for _, item := range toLaunch {
		startIdx := item.startIdx
		// Reset cursor if the branch was rewound (e.g., `/branch` or `/tree`).
		if startIdx > len(branch) {
			startIdx = 0
		}
		delta := renderBranchDelta(branch, startIdx)
		if delta == "" {
			m.mu.Lock()
			if item.inst.lastBranchLen > len(branch) {
				item.inst.lastBranchLen = len(branch)
			}
			item.inst.inFlight = false
			m.mu.Unlock()
			continue
		}
		ready = append(ready, readyItem{li: item, deltaText: delta, branchLen: len(branch)})
	}
	if len(ready) == 0 {
		return
	}

	// Re-lock m.mu to check session liveness and call st.wg.Add(1) safely
	// before endSession can observe st.ended and call st.wg.Wait().
	m.mu.Lock()
	if m.sessions[sid] != st || st.ended || st.runCtx.Err() != nil {
		for _, r := range ready {
			r.li.inst.inFlight = false
		}
		m.mu.Unlock()
		return
	}
	st.wg.Add(len(ready))
	m.mu.Unlock()

	for _, r := range ready {
		go func(li launchItem, deltaText string, branchLen int) {
			defer st.wg.Done()
			defer func() {
				m.mu.Lock()
				li.inst.inFlight = false
				m.mu.Unlock()
			}()

			reviewCtx, cancel := context.WithTimeout(runCtx, advisorReviewTimeout)
			defer cancel()

			li.guard.BeginUpdate()
			var collected []Note
			var noteMu sync.Mutex
			onNote := func(n Note) {
				if n.Advisor == "" && li.multiAdvisor {
					n.Advisor = li.advisorName
				}
				noteMu.Lock()
				collected = append(collected, n)
				noteMu.Unlock()
			}

			req := ReviewRequest{
				AdvisorName:   li.advisorName,
				AdvisorSlug:   li.advisorSlug,
				Provider:      li.provider,
				ModelID:       li.modelID,
				ThinkingLevel: li.thinking,
				Tools:         slices.Clone(li.tools),
				SystemPrompt:  li.sysPrompt,
				DeltaPrompt:   deltaText,
				CoveredTurn:   li.coveredTurn,
				Guard:         li.guard,
				OnNote:        onNote,
			}

			res, err := executeAdvisorReview(reviewCtx, ctx, li.parentSID, req)
			nowMs := m.now()

			m.mu.Lock()
			// Record token/cost accounting whenever a review produced spend, even on
			// quarantine or error.
			if res.InputTokens > 0 || res.OutputTokens > 0 || res.Cost > 0 {
				li.inst.inputTokens += res.InputTokens
				li.inst.outputTokens += res.OutputTokens
				li.inst.cost += res.Cost
			}

			if err != nil {
				if reviewCtx.Err() == nil {
					li.inst.lastError = err.Error()
					if isQuotaError(err) {
						li.inst.status = StatusQuotaExhausted
					} else {
						// Non-retryable error: advance cursor so we don't re-review
						// the exact same failing delta forever.
						li.inst.lastBranchLen = branchLen
					}
				}
				var totalSpend float64
				for _, a := range st.advisors {
					totalSpend += a.cost
				}
				m.mu.Unlock()
				if totalSpend > 0 {
					publishAdvisorSpend(totalSpend)
				}
				return
			}
			if res.QuotaHit {
				li.inst.status = StatusQuotaExhausted
				m.mu.Unlock()
				return
			}

			// Quarantine unsafe output-only directives before admitting collected notes.
			if reason, bad := QuarantineUnsafeOutput(res.AssistantText, deltaText); bad {
				li.inst.lastError = reason
				li.inst.lastBranchLen = branchLen
				var totalSpend float64
				for _, a := range st.advisors {
					totalSpend += a.cost
				}
				m.mu.Unlock()
				if totalSpend > 0 {
					publishAdvisorSpend(totalSpend)
				}
				return
			}

			li.inst.lastBranchLen = branchLen

			userEntry := TranscriptEntry{
				Timestamp: nowMs,
				Role:      "user",
				Advisor:   li.advisorName,
				Turn:      li.coveredTurn,
				Content:   deltaText,
			}
			asstEntry := TranscriptEntry{
				Timestamp:    nowMs,
				Role:         "assistant",
				Advisor:      li.advisorName,
				Model:        li.modelID,
				Provider:     li.provider,
				Turn:         li.coveredTurn,
				Content:      res.AssistantText,
				InputTokens:  res.InputTokens,
				OutputTokens: res.OutputTokens,
				Cost:         res.Cost,
			}
			appendHistoryLocked(li.inst, userEntry)
			appendHistoryLocked(li.inst, asstEntry)

			noteMu.Lock()
			notes := append([]Note(nil), collected...)
			noteMu.Unlock()

			immuneActive := IsInterruptImmuneActive(li.coveredTurn, st.immuneTurnStart, immuneTurns)
			for _, n := range notes {
				if reason, bad := QuarantineUnsafeOutput(n.Note, deltaText); bad {
					li.inst.lastError = reason
					continue
				}
				if immuneActive && n.Severity == SeverityConcern {
					// Cooldown downgrades non-blocker concerns to non-interrupting aside notes.
					n.Severity = SeverityNit
				}
				if IsInterruptingSeverity(n.Severity) {
					t := li.coveredTurn
					st.immuneTurnStart = &t
				}
				st.pendingNotes = append(st.pendingNotes, n)
				appendHistoryLocked(li.inst, TranscriptEntry{
					Timestamp: nowMs,
					Role:      "advisory",
					Advisor:   n.Advisor,
					Turn:      li.coveredTurn,
					Severity:  string(n.Severity),
					Content:   n.Note,
				})
			}

			var totalSpend float64
			for _, a := range st.advisors {
				totalSpend += a.cost
			}
			rec := li.inst.recorder
			m.mu.Unlock()

			if rec != nil {
				_ = rec.Record(userEntry)
				_ = rec.Record(asstEntry)
			}
			if totalSpend > 0 {
				publishAdvisorSpend(totalSpend)
			}
		}(r.li, r.deltaText, r.branchLen)
	}
}

func appendHistoryLocked(inst *advisorInstance, entry TranscriptEntry) {
	inst.history = append(inst.history, entry)
	if len(inst.history) > maxHistoryEntries {
		inst.history = inst.history[len(inst.history)-maxHistoryEntries:]
	}
}

func buildAdvisorSystemPrompt(budget int, sharedInst, advisorInst string, watchdogBlocks []string) string {
	var parts []string
	parts = append(parts, fmt.Sprintf(advisorSystemPromptBase, budget))
	if strings.TrimSpace(sharedInst) != "" {
		parts = append(parts, strings.TrimSpace(sharedInst))
	}
	if strings.TrimSpace(advisorInst) != "" {
		parts = append(parts, strings.TrimSpace(advisorInst))
	}
	parts = append(parts, watchdogBlocks...)
	return strings.Join(parts, "\n\n")
}

func renderBranchDelta(branch []sdk.BranchEntry, startIdx int) string {
	if startIdx < 0 {
		startIdx = 0
	}
	if startIdx >= len(branch) {
		return ""
	}
	var b strings.Builder
	for _, e := range branch[startIdx:] {
		if e.Type != "message" {
			continue
		}
		text := strings.TrimSpace(e.Content)
		if text == "" && len(e.ToolCalls) == 0 {
			continue
		}
		if text != "" {
			text = truncateRunes(text, maxDeltaEntryRunes)
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "### %s\n", strings.ToUpper(e.Role))
		if text != "" {
			b.WriteString(text)
		}
		for _, tc := range e.ToolCalls {
			fmt.Fprintf(&b, "\n[tool_call: %s %s]", tc.Name, truncateInline(tc.Args, 400))
		}
	}
	out := strings.TrimSpace(b.String())
	return truncateRunes(out, maxDeltaTotalRunes)
}

func truncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "…"
}

func truncateInline(s string, maxChars int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	return truncateRunes(s, maxChars)
}

func executeAdvisorReview(goCtx context.Context, sdkCtx sdk.Context, parentSID string, req ReviewRequest) (ReviewResult, error) {
	if SpawnAdvisorSubagent != nil {
		return SpawnAdvisorSubagent(goCtx, sdkCtx, req)
	}

	var modelObj any
	if req.Provider != "" && req.ModelID != "" {
		if found, err := sdkCtx.ModelRegistry().Find(req.Provider, req.ModelID); err == nil && found != nil {
			modelObj = found
		}
	}
	if modelObj == nil && req.ModelID != "" {
		modelObj = map[string]any{"provider": req.Provider, "id": req.ModelID}
	}

	adviseTool := BuildAdviseToolDefinition(req.Guard, req.AdvisorName, req.CoveredTurn, req.OnNote)
	var toolsList []string
	if req.Tools == nil {
		toolsList = slices.Clone(DefaultAdvisorToolNames)
	} else {
		toolsList = slices.Clone(req.Tools)
	}
	if !slices.Contains(toolsList, "advise") {
		toolsList = append(toolsList, "advise")
	}

	opts := sdk.CreateAgentSessionOptions{
		Cwd:           sdkCtx.Cwd(),
		Model:         modelObj,
		ThinkingLevel: req.ThinkingLevel,
		Tools:         toolsList,
		ExcludeTools:  []string{"ask", "todo", "task", "propose", "goal"},
		NoTools:       "builtin",
		CustomTools:   []sdk.ToolDefinition{adviseTool},
		InMemory:      true,
		Resources: sdk.ResourceOptions{
			NoExtensions:             true,
			AdditionalExtensionPaths: []string{fusedExtensionPath},
			NoPromptTemplates:        true,
			NoThemes:                 true,
			NoContextFiles:           true,
			AppendSystemPrompt:       []string{req.SystemPrompt},
		},
	}

	child, err := sdkCtx.CreateAgentSession(opts)
	if err != nil {
		return ReviewResult{}, err
	}
	childSID := child.SessionID
	if RegisterChildSession != nil && childSID != "" {
		RegisterChildSession(childSID, parentSID, slices.Clone(toolsList))
	}
	defer func() {
		child.Dispose()
		if UnregisterChildSession != nil && childSID != "" {
			UnregisterChildSession(childSID)
		}
		if childSID != "" {
			lifecycle.End(childSID)
		}
	}()

	if err := child.BindExtensions(); err != nil {
		return ReviewResult{}, err
	}
	var res ReviewResult
	var resMu sync.Mutex
	unsub := child.Subscribe(func(ev map[string]any) {
		if typ, _ := ev["type"].(string); typ != "message_end" {
			return
		}
		msg, _ := ev["message"].(map[string]any)
		if msg == nil {
			return
		}
		if role, _ := msg["role"].(string); role != "assistant" {
			return
		}
		usage, _ := msg["usage"].(map[string]any)
		resMu.Lock()
		if usage != nil {
			res.InputTokens += intFromAny(usage["input"])
			res.OutputTokens += intFromAny(usage["output"])
			if costMap, ok := usage["cost"].(map[string]any); ok {
				if c, ok := costMap["total"].(float64); ok && c > 0 {
					res.Cost += c
				}
			}
		}
		resMu.Unlock()
	})
	defer unsub()

	promptText := "Session update (turn " + fmt.Sprintf("%d", req.CoveredTurn) + "):\n\n" + req.DeltaPrompt
	if err := child.Prompt(goCtx, promptText); err != nil {
		return ReviewResult{}, err
	}
	if st, err := child.State(); err == nil && st.LastAssistantText != nil {
		resMu.Lock()
		res.AssistantText = *st.LastAssistantText
		resMu.Unlock()
	}
	return res, nil
}

func publishAdvisorSpend(totalCost float64) {
	surface.UpdateComposerMeta(func(m *surface.ComposerMeta) {
		m.AdvisorCost = totalCost
	})
}

func isQuotaError(err error) bool {
	if err == nil {
		return false
	}
	low := strings.ToLower(err.Error())
	return strings.Contains(low, "rate limit") || strings.Contains(low, "quota") || strings.Contains(low, "429")
}

func intFromAny(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	default:
		return 0
	}
}

func formatModelID(provider, id string) string {
	if provider != "" && id != "" {
		return provider + "/" + id
	}
	return id
}

func parseSubcommand(args string) (verb, rest string) {
	trimmed := strings.TrimSpace(args)
	if trimmed == "" {
		return "", ""
	}
	idx := strings.IndexAny(trimmed, " \t\r\n")
	if idx < 0 {
		return strings.ToLower(trimmed), ""
	}
	return strings.ToLower(trimmed[:idx]), strings.TrimSpace(trimmed[idx+1:])
}
