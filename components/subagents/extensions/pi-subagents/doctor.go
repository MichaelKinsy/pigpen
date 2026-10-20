package pi_subagents

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The doctor report (src/extension/doctor.ts) and the spawn budget it reports (src/runs/shared/spawn-budget.ts).

// doctorConfig is the part of the extension config (config.json) the report reads.
type doctorConfig struct {
	DefaultSessionDir            string
	MaxActiveAsyncRunsPerSession any
	AbandonedSlotReleaseAfterMs  any
	Spawn                        extensionConfig
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
	State               *subagentState
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
	state := in.State
	if state == nil {
		state = &subagentState{}
	}
	snap := getSpawnBudgetSnapshot(state, in.Config.Spawn, sessionID, osEnv)
	grants := "none"
	if len(snap.GrantHistory) > 0 {
		var g []string
		for _, h := range snap.GrantHistory {
			g = append(g, fmt.Sprintf("+%d at %s (%d → %d)", h.Amount, time.UnixMilli(h.GrantedAt).UTC().Format("2006-01-02T15:04:05.000Z"), h.PreviousLimit, h.Limit))
		}
		grants = strings.Join(g, "; ")
	}
	lines = append(lines, "- usage: "+formatSpawnBudgetSummary(snap), "- recent grants: "+grants, "- reset boundary: a new parent session resets usage and grants; compaction does not")

	configured := resolveMaxSubagentSpawnsPerRun(in.Config.Spawn.MaxSubagentSpawnsPerRun, osEnv)
	source := "default"
	if v, set := osEnv("PI_SUBAGENT_MAX_SPAWNS_PER_RUN"); set && isNormalizedRunLimit(v) {
		source = "environment"
	} else if isNormalizedRunLimit(in.Config.Spawn.MaxSubagentSpawnsPerRun) {
		source = "config"
	}
	lines = append(lines, "", "Run fan-out budget", fmt.Sprintf("- configured limit: %d (%s)", configured, source), "- usage: available after a run starts",
		"- reset boundary: cumulative claims are never released; a new top-level run creates a new budget")

	limit := resolveMaxActiveAsyncRunsPerSession(in.Config.MaxActiveAsyncRunsPerSession)
	used := 0
	if sessionID != "" {
		release, disabled := resolveAbandonedSlotReleaseAfterMs(in.Config.AbandonedSlotReleaseAfterMs)
		opts := CapacityOptions{AbandonedSlotReleaseAfterMs: release}
		if disabled {
			opts.AbandonedSlotReleaseAfterMs = false
		}
		used = getActiveAsyncCapacitySnapshot(sessionID, limit, opts).Used
	}
	limitText := "unlimited"
	if limit != nil {
		limitText = strconv.Itoa(*limit)
	}
	lines = append(lines, "", "Active async capacity",
		fmt.Sprintf("- usage: %d/%s used", used, limitText),
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
	bridge := "inactive"
	if diag.Active {
		bridge = "active"
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
	lines = append(lines, fmt.Sprintf("- bridge: %s%s", bridge, reason), fmt.Sprintf("- mode: %s; context: %s", diag.Mode, ctxText),
		"- orchestrator target: "+target, fmt.Sprintf("- supervisor channel: %s (%s)", channel, diag.ExtensionDir))
	return strings.Join(lines, "\n")
}

func isNormalizedRunLimit(v any) bool {
	_, ok := normalizeMaxSubagentSpawnsPerRun(v)
	return ok
}

// loadDoctorConfig reads the extension config file for the report (an unreadable file is an empty config).
func loadDoctorConfig() doctorConfig {
	c := doctorConfig{Spawn: loadExtensionConfig()}
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
	c.MaxActiveAsyncRunsPerSession = o.vals["maxActiveAsyncRunsPerSession"]
	if capacity := o.obj("capacity"); capacity != nil {
		c.AbandonedSlotReleaseAfterMs = capacity.vals["abandonedSlotReleaseAfterMs"]
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
	in := doctorInput{Cwd: ctx.Cwd(), Config: cfg, State: runState, CurrentSessionID: sessionID, Context: context, RequestedSessionDir: sessionDir, ExpandTilde: expandTilde}
	if serr != nil {
		in.SessionError = errorText(serr)
	}
	if f, err := ctx.GetSessionFile(); err == nil && f != nil {
		in.CurrentSessionFile = *f
	}
	return buildDoctorReport(in), getSpawnBudgetSnapshot(runState, cfg.Spawn, sessionID, osEnv)
}

func init() {
	registerAction("doctor", []string{"context", "sessionDir"}, func(ctx sdk.Context, params map[string]any) (any, error) {
		context, _ := params["context"].(string)
		if context == "profile" {
			context = ""
		}
		sessionDir, _ := params["sessionDir"].(string)
		report, snap := doctorReport(ctx, context, sessionDir)
		budget := spawnBudgetDetails(snap)
		return manageOutcome(manageResult{text: report, details: obj("spawnBudget", budget, "activeAsyncCapacity", capacityDetails(loadDoctorConfig(), sessionIDOf(ctx)))})
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

// capacityDetails is the activeAsyncCapacity detail of the doctor result.
func capacityDetails(cfg doctorConfig, sessionID string) *jsObject {
	limit := resolveMaxActiveAsyncRunsPerSession(cfg.MaxActiveAsyncRunsPerSession)
	used, shown := 0, 0
	if limit != nil {
		shown = *limit
	}
	if sessionID != "" {
		release, disabled := resolveAbandonedSlotReleaseAfterMs(cfg.AbandonedSlotReleaseAfterMs)
		opts := CapacityOptions{AbandonedSlotReleaseAfterMs: release}
		if disabled {
			opts.AbandonedSlotReleaseAfterMs = false
		}
		used = getActiveAsyncCapacitySnapshot(sessionID, limit, opts).Used
	}
	return obj("used", float64(used), "limit", float64(shown))
}
