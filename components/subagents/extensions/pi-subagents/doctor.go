package pi_subagents

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The doctor report (src/extension/doctor.ts) and the spawn budget it reports (src/runs/shared/spawn-budget.ts).

const (
	defaultMaxSubagentSpawnsPerRun = 64
	maxSpawnGrantHistory           = 20
)

// doctorConfig is the part of the extension config (config.json) the report reads; nil is unset.
type doctorConfig struct {
	DefaultSessionDir            string
	MaxSubagentSpawnsPerSession  any
	MaxSubagentSpawnsPerRun      any
	MaxActiveAsyncRunsPerSession any
	IntercomBridgeMode           string
}

type spawnGrant struct {
	SessionID            string
	Amount               int
	GrantedAt            int64
	PreviousLimit, Limit int
}

// spawnCounters is SubagentState.subagentSpawns.
type spawnCounters struct {
	SessionID       string
	Count           int
	ConfiguredLimit *int
	Granted         int
	GrantHistory    []spawnGrant
}

type spawnBudgetSnapshot struct {
	Used            int
	ConfiguredLimit *int
	Granted         int
	Limit           *int
	Remaining       *int
	GrantRemaining  *int
	GrantHistory    []spawnGrant
}

// normalizeNonNegativeInteger is types.ts:2883 (a number or a numeric string).
func normalizeNonNegativeInteger(v any) *int {
	var f float64
	switch x := v.(type) {
	case float64:
		f = x
	case int:
		f = float64(x)
	case string:
		t := jsTrim(x)
		if t == "" {
			f = 0
		} else {
			p, err := strconv.ParseFloat(t, 64)
			if err != nil {
				return nil
			}
			f = p
		}
	default:
		return nil
	}
	if math.IsNaN(f) || math.IsInf(f, 0) || f != math.Trunc(f) || f < 0 {
		return nil
	}
	n := int(f)
	return &n
}

// resolveMaxSubagentSpawnsPerSession is types.ts:2939: the environment wins; 0 means unlimited (nil).
func resolveMaxSubagentSpawnsPerSession(config any) *int {
	raw, set := os.LookupEnv("PI_SUBAGENT_MAX_SPAWNS_PER_SESSION")
	if env := normalizeNonNegativeInteger(raw); set && env != nil {
		if *env == 0 {
			return nil
		}
		return env
	}
	c := normalizeNonNegativeInteger(config)
	if c != nil && *c == 0 {
		return nil
	}
	return c
}

func normalizeMaxSubagentSpawnsPerRun(v any) *int {
	n := normalizeNonNegativeInteger(v)
	if n != nil && *n > 0 {
		return n
	}
	return nil
}

// spawnBudgetSnapshotOf is getSpawnBudgetSnapshot: counters of another session are replaced by fresh ones.
func spawnBudgetSnapshotOf(counters **spawnCounters, config doctorConfig, sessionID string) spawnBudgetSnapshot {
	c := *counters
	if c == nil || c.SessionID != sessionID {
		c = &spawnCounters{SessionID: sessionID, ConfiguredLimit: resolveMaxSubagentSpawnsPerSession(config.MaxSubagentSpawnsPerSession)}
		*counters = c
	}
	s := spawnBudgetSnapshot{Used: c.Count, ConfiguredLimit: c.ConfiguredLimit, GrantHistory: append([]spawnGrant(nil), c.GrantHistory...)}
	if c.ConfiguredLimit != nil {
		s.Granted = c.Granted
		limit := *c.ConfiguredLimit + c.Granted
		remaining := max(0, limit-c.Count)
		grantRemaining := max(0, *c.ConfiguredLimit-c.Granted)
		s.Limit, s.Remaining, s.GrantRemaining = &limit, &remaining, &grantRemaining
	}
	return s
}

func formatSpawnBudgetSummary(s spawnBudgetSnapshot) string {
	if s.Limit == nil {
		return "unlimited"
	}
	return fmt.Sprintf("%d/%d used, %d remaining (configured %d; granted %d; grant allowance %d)", s.Used, *s.Limit, *s.Remaining, *s.ConfiguredLimit, s.Granted, *s.GrantRemaining)
}

// intercomDiagnostic is IntercomBridgeDiagnostic.
type intercomDiagnostic struct {
	Active, WantsIntercom, SupervisorChannelAvailable bool
	Mode, Reason, ExtensionDir, OrchestratorTarget    string
}

// doctorPaths are the directories the filesystem section checks.
type doctorPaths struct{ TempRoot, Async, Results, ChainRuns string }

// doctorDeps are the checks a report runs; a nil one is the port's own.
type doctorDeps struct {
	IsAsyncAvailable        func() bool
	DiscoverAgentsAll       func(cwd string) (DiscoveryAll, error)
	DiscoverAvailableSkills func(cwd string) ([]AvailableSkill, error)
	DiagnoseIntercomBridge  func() intercomDiagnostic
}

// doctorInput is DoctorReportInput.
type doctorInput struct {
	Cwd                 string
	Config              doctorConfig
	Spawns              **spawnCounters
	CurrentSessionID    string
	Context             string
	RequestedSessionDir string
	CurrentSessionFile  string
	OrchestratorTarget  string
	SessionError        string
	ExpandTilde         func(string) string
	Paths               *doctorPaths
	Deps                doctorDeps
}

func errorText(err error) string { return "Error: " + err.Error() }

func doctorTempRoot() string {
	if v := jsTrim(os.Getenv("PI_SUBAGENTS_TEMP_ROOT")); v != "" {
		p, _ := filepath.Abs(v)
		return p
	}
	return filepath.Join(os.TempDir(), "pi-subagents-"+tempScopeID())
}

// tempScopeID scopes the temp root to the user (types.ts resolveTempScopeId, without the Windows SID lookup of Node).
func tempScopeID() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "shared"
	}
	var b strings.Builder
	for _, r := range home {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return "home-" + b.String()
}

func defaultDoctorPaths() doctorPaths {
	root := doctorTempRoot()
	return doctorPaths{TempRoot: root, Async: filepath.Join(root, "async-subagent-runs"), Results: filepath.Join(root, "async-subagent-results"), ChainRuns: filepath.Join(root, "chain-runs")}
}

func lineFromCheck(label string, check func() (string, error)) string {
	text, err := check()
	if err != nil {
		return fmt.Sprintf("- %s: failed — %s", label, errorText(err))
	}
	return text
}

func formatExistingDirectory(label, dir string) string {
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Sprintf("- %s: missing (%s)", label, dir)
		}
		return fmt.Sprintf("- %s: failed (%s) — %s", label, dir, errorText(err))
	}
	if !info.IsDir() {
		return fmt.Sprintf("- %s: failed (%s) — %s", label, dir, errorText(fmt.Errorf("not a directory: %s", dir)))
	}
	if err := accessReadWrite(dir); err != nil {
		return fmt.Sprintf("- %s: failed (%s) — %s", label, dir, errorText(err))
	}
	return fmt.Sprintf("- %s: ok (%s)", label, dir)
}

var skillSourceOrder = []skillSource{skillProject, skillProjectSettings, skillProjectPackage, skillUser, skillUserSettings, skillUserPackage, skillExtension, skillBuiltin, skillUnknown}

func formatSkillSourceCounts(skills []AvailableSkill) string {
	counts := map[skillSource]int{}
	for _, s := range skills {
		counts[skillSource(s.Source)]++
	}
	var parts []string
	for _, src := range skillSourceOrder {
		if counts[src] > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", src, counts[src]))
		}
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ", ")
}

func (in doctorInput) configuredSessionDir() string {
	dir := in.RequestedSessionDir
	if dir == "" {
		dir = in.Config.DefaultSessionDir
	}
	if dir == "" {
		return "not configured"
	}
	if in.ExpandTilde != nil {
		dir = in.ExpandTilde(dir)
	}
	abs, _ := filepath.Abs(dir)
	return abs
}

func buildDoctorReport(in doctorInput) string {
	paths := defaultDoctorPaths()
	if in.Paths != nil {
		paths = *in.Paths
	}
	deps := in.Deps
	if deps.IsAsyncAvailable == nil {
		deps.IsAsyncAvailable = func() bool { return false }
	}
	if deps.DiscoverAgentsAll == nil {
		deps.DiscoverAgentsAll = func(cwd string) (DiscoveryAll, error) {
			return discoverAgentsAll(cwd, "", adv.options())
		}
	}
	if deps.DiscoverAvailableSkills == nil {
		deps.DiscoverAvailableSkills = discoverAvailableSkills
	}
	if deps.DiagnoseIntercomBridge == nil {
		deps.DiagnoseIntercomBridge = func() intercomDiagnostic {
			return intercomDiagnostic{Mode: "off", ExtensionDir: "none"}
		}
	}
	sessionFile, sessionDir := "not available", "not available"
	if in.CurrentSessionFile != "" {
		sessionFile, sessionDir = in.CurrentSessionFile, filepath.Dir(in.CurrentSessionFile)
	}
	sessionID := in.CurrentSessionID
	idText := sessionID
	if idText == "" {
		idText = "not available"
	}
	lines := []string{
		"Subagents doctor report", "", "Runtime", "- cwd: " + in.Cwd,
		lineFromCheck("async support", func() (string, error) {
			if deps.IsAsyncAvailable() {
				return "- async support: available", nil
			}
			return "- async support: unavailable", nil
		}),
		"- configured session dir: " + in.configuredSessionDir(),
		"- current session file: " + sessionFile,
		"- current session dir: " + sessionDir,
		"- current session id: " + idText,
	}
	if in.SessionError != "" {
		lines = append(lines, "- session manager: failed — "+in.SessionError)
	}
	lines = append(lines, "", "Filesystem",
		formatExistingDirectory("temp root", paths.TempRoot), formatExistingDirectory("async runs", paths.Async),
		formatExistingDirectory("results", paths.Results), formatExistingDirectory("chain runs", paths.ChainRuns),
		"", "Discovery",
		lineFromCheck("agents", func() (string, error) {
			d, err := deps.DiscoverAgentsAll(in.Cwd)
			if err != nil {
				return "", err
			}
			out := []string{fmt.Sprintf("- agents: total %d (builtin %d, package %d, user %d, project %d)", len(d.Builtin)+len(d.Package)+len(d.User)+len(d.Project), len(d.Builtin), len(d.Package), len(d.User), len(d.Project))}
			for _, diag := range d.Diagnostics {
				name := diag.Name
				if name == "" {
					name = diag.FilePath
				}
				out = append(out, fmt.Sprintf("- invalid agent %s (%s): %s", name, diag.Source, diag.Error))
			}
			return strings.Join(out, "\n"), nil
		}),
		lineFromCheck("skills", func() (string, error) {
			skills, err := deps.DiscoverAvailableSkills(in.Cwd)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("- skills: total %d (%s)", len(skills), formatSkillSourceCounts(skills)), nil
		}),
		"", "Spawn budget")
	var holder *spawnCounters
	spawns := in.Spawns
	if spawns == nil {
		spawns = &holder
	}
	snap := spawnBudgetSnapshotOf(spawns, in.Config, sessionID)
	grants := "none"
	if len(snap.GrantHistory) > 0 {
		var g []string
		for _, h := range snap.GrantHistory {
			g = append(g, fmt.Sprintf("+%d at %s (%d → %d)", h.Amount, time.UnixMilli(h.GrantedAt).UTC().Format("2006-01-02T15:04:05.000Z"), h.PreviousLimit, h.Limit))
		}
		grants = strings.Join(g, "; ")
	}
	lines = append(lines, "- usage: "+formatSpawnBudgetSummary(snap), "- recent grants: "+grants, "- reset boundary: a new parent session resets usage and grants; compaction does not")

	configured := defaultMaxSubagentSpawnsPerRun
	source := "default"
	if n := normalizeMaxSubagentSpawnsPerRun(os.Getenv("PI_SUBAGENT_MAX_SPAWNS_PER_RUN")); n != nil {
		configured, source = *n, "environment"
	} else if n := normalizeMaxSubagentSpawnsPerRun(in.Config.MaxSubagentSpawnsPerRun); n != nil {
		configured, source = *n, "config"
	}
	lines = append(lines, "", "Run fan-out budget", fmt.Sprintf("- configured limit: %d (%s)", configured, source), "- usage: available after a run starts",
		"- reset boundary: cumulative claims are never released; a new top-level run creates a new budget")

	limit := resolveMaxActiveAsyncRunsPerSession(in.Config.MaxActiveAsyncRunsPerSession)
	limitText := "unlimited"
	if limit > 0 {
		limitText = strconv.Itoa(limit)
	}
	lines = append(lines, "", "Active async capacity",
		// The port runs no background runs, so no slot is ever used.
		"- usage: 0/"+limitText+" used",
		"- scope: top-level async runs in the current parent session; foreground and nested workflow children are not charged again",
		"- release: terminal state plus matching observed process-terminal proof, or abandoned-timeout for failed runs with a dead runner PID and stale activity when enabled; false keeps unknown-proof slots",
		"", "Workflow script",
		"- helpers: runs.run, runs.all, runs.steer, runs.status, runs.ref/refs, emit, console",
		"- recovery: if runs.all is missing, reload or update pi-subagents; await Promise.all([runs.run(...)]) is also supported",
		"", "Permission system")
	parent := jsTrim(os.Getenv("PI_SUBAGENT_PARENT_SESSION"))
	isChild := os.Getenv("PI_SUBAGENT_CHILD") == "1"
	switch {
	case isChild && parent != "":
		lines = append(lines, fmt.Sprintf("- runner parent session: set (%s) — explicit target for this dedicated child process", parent))
	case isChild:
		lines = append(lines, "- runner parent session: not set — this child process has no forwarding target")
	case parent != "":
		lines = append(lines, "- root parent session: ignored legacy process-global value — root sessions do not use it for routing")
	default:
		lines = append(lines, "- root parent session: not set (healthy) — detached forwarding is attached at runner launch")
	}
	if !isChild {
		lines = append(lines, "- foreground external ask forwarding: unavailable until the permission extension supports a session-scoped target")
	}
	if isChild {
		lines = append(lines, "- subagent process: yes (PI_SUBAGENT_CHILD=1)")
	} else {
		lines = append(lines, "- subagent process: no")
	}
	lines = append(lines, "", "Intercom bridge")
	diag := deps.DiagnoseIntercomBridge()
	reason := ""
	if diag.Reason != "" {
		reason = " (" + diag.Reason + ")"
	}
	state := "inactive"
	if diag.Active {
		state = "active"
	}
	ctxText := in.Context
	if ctxText == "" {
		ctxText = "unspecified"
	}
	target := diag.OrchestratorTarget
	if target == "" {
		target = "not available"
	}
	channel := "unavailable"
	if diag.SupervisorChannelAvailable {
		channel = "available"
	}
	lines = append(lines, fmt.Sprintf("- bridge: %s%s", state, reason), fmt.Sprintf("- mode: %s; context: %s", diag.Mode, ctxText),
		"- orchestrator target: "+target, fmt.Sprintf("- supervisor channel: %s (%s)", channel, diag.ExtensionDir))
	return strings.Join(lines, "\n")
}

// resolveMaxActiveAsyncRunsPerSession is active-async-capacity.ts:81 (0 is unlimited).
func resolveMaxActiveAsyncRunsPerSession(v any) int {
	f, ok := v.(float64)
	if !ok {
		if i, isInt := v.(int); isInt {
			f, ok = float64(i), true
		}
	}
	if !ok || f != math.Trunc(f) || f < 0 {
		return 0
	}
	return int(f)
}

// loadDoctorConfig reads the extension config file for the report (an unreadable file is an empty config).
func loadDoctorConfig() doctorConfig {
	var c doctorConfig
	b, err := os.ReadFile(subagentConfigPath())
	if err != nil {
		return c
	}
	v, msg := jsJSONParse(string(b))
	o, _ := v.(*jsObject)
	if msg != "" || o == nil {
		return c
	}
	c.DefaultSessionDir, _ = o.str("defaultSessionDir")
	c.MaxSubagentSpawnsPerSession = o.vals["maxSubagentSpawnsPerSession"]
	c.MaxSubagentSpawnsPerRun = o.vals["maxSubagentSpawnsPerRun"]
	c.MaxActiveAsyncRunsPerSession = o.vals["maxActiveAsyncRunsPerSession"]
	if bridge := o.obj("intercomBridge"); bridge != nil {
		c.IntercomBridgeMode, _ = bridge.str("mode")
	}
	return c
}

// expandTilde is utils.ts expandTilde for a configured session directory.
func expandTilde(p string) string {
	if p == "~" {
		return homeDir()
	}
	if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		return filepath.Join(homeDir(), p[2:])
	}
	return p
}

// doctorAction runs the doctor action and the /subagents-doctor command.
func doctorReport(ctx sdk.Context, context, sessionDir string) (string, spawnBudgetSnapshot) {
	cfg := loadDoctorConfig()
	sessionID, serr := ctx.GetSessionID()
	in := doctorInput{Cwd: ctx.Cwd(), Config: cfg, Spawns: &doctorSpawns, CurrentSessionID: sessionID, Context: context, RequestedSessionDir: sessionDir, ExpandTilde: expandTilde}
	if serr != nil {
		in.SessionError = errorText(serr)
	}
	if f, err := ctx.GetSessionFile(); err == nil && f != nil {
		in.CurrentSessionFile = *f
	}
	return buildDoctorReport(in), spawnBudgetSnapshotOf(&doctorSpawns, cfg, sessionID)
}

// doctorSpawns is the session's spawn counters (the port starts no budgeted children, so they stay at zero).
var doctorSpawns *spawnCounters

func init() {
	registerAction("doctor", []string{"context", "sessionDir"}, func(ctx sdk.Context, params map[string]any) (any, error) {
		context, _ := params["context"].(string)
		if context == "profile" {
			context = ""
		}
		sessionDir, _ := params["sessionDir"].(string)
		report, snap := doctorReport(ctx, context, sessionDir)
		budget := obj("used", float64(snap.Used))
		setLimit := func(k string, v *int) {
			if v == nil {
				budget.set(k, nil)
			} else {
				budget.set(k, float64(*v))
			}
		}
		setLimit("configuredLimit", snap.ConfiguredLimit)
		budget.set("granted", float64(snap.Granted))
		setLimit("limit", snap.Limit)
		setLimit("remaining", snap.Remaining)
		setLimit("grantRemaining", snap.GrantRemaining)
		history := []any{}
		for _, g := range snap.GrantHistory {
			history = append(history, obj("sessionId", g.SessionID, "amount", float64(g.Amount), "grantedAt", float64(g.GrantedAt), "previousLimit", float64(g.PreviousLimit), "limit", float64(g.Limit)))
		}
		budget.set("grantHistory", history)
		return manageOutcome(manageResult{text: report, details: obj("spawnBudget", budget, "activeAsyncCapacity", obj("used", float64(0), "limit", float64(resolveMaxActiveAsyncRunsPerSession(loadDoctorConfig().MaxActiveAsyncRunsPerSession))))})
	})
	registerAction("guide", []string{"topic"}, func(ctx sdk.Context, params map[string]any) (any, error) {
		topic := "overview"
		if s, ok := params["topic"].(string); ok {
			topic = s
		}
		text, err := readSubagentGuide(topic)
		if err != nil {
			return nil, sdk.NewToolError(err.Error())
		}
		return manageOutcome(manageResult{text: text})
	})
}
