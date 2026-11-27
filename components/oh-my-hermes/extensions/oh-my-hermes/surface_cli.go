package ohmyhermes

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/clihook"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/cli"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/usage"
)

const (
	// Underscore-only SystemPromptSections names (PiG v0.8.0 rejects dotted section names).
	SectionOMHAwareness          = "omh_awareness"
	SectionOMHDelegationProtocol = "omh_delegation_protocol"
	SectionOMHChildCalibration   = "omh_child_calibration"
	SectionOMHMainCalibration    = "omh_main_calibration"

	omhMessageType = "omh-surface"
)

// OMHTheme describes one of the 5 shipped OMH TUI color themes.
type OMHTheme struct {
	Name    string `json:"name"`
	Accent  string `json:"accent"`
	Surface string `json:"surface"`
	Summary string `json:"summary"`
}

var builtinThemes = []OMHTheme{
	{Name: "sky", Accent: "#38BDF8", Surface: "#0B1E28", Summary: "Sky turquoise on deep teal (OMH default)."},
	{Name: "amber", Accent: "#F59E0B", Surface: "#1F160A", Summary: "Amber gold on deep bronze."},
	{Name: "crimson", Accent: "#EF4444", Surface: "#220D10", Summary: "Ember red-orange on deep crimson."},
	{Name: "mono", Accent: "#E5E7EB", Surface: "#111111", Summary: "Neutral grayscale with white accents."},
	{Name: "kanagawa", Accent: "#7E9CD8", Surface: "#1F1F28", Summary: "Kanagawa Wave: indigo, warm ivory, muted blue and violet."},
}

// BuiltinThemes returns the 5 shipped OMH themes.
func BuiltinThemes() []OMHTheme {
	out := make([]OMHTheme, len(builtinThemes))
	copy(out, builtinThemes)
	return out
}

// LookupTheme resolves a theme name (accepting "default" -> "sky").
func LookupTheme(name string) (OMHTheme, bool) {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" || n == "default" || n == "omh-sky" {
		n = "sky"
	}
	n = strings.TrimPrefix(n, "omh-")
	for _, th := range builtinThemes {
		if th.Name == n {
			return th, true
		}
	}
	return OMHTheme{}, false
}

type themeFile struct {
	Schema    string `json:"schema"`
	Theme     string `json:"theme"`
	UpdatedAt string `json:"updated_at"`
}

type surfaceState struct {
	mu      sync.RWMutex
	paths   func() OMHPaths
	router  *RouteRouter
	dags    *DAGStore
	teams   *TeamStore
	loops   *LoopStore
	theme   string
}

func newSurfaceState(pathsFn func() OMHPaths, router *RouteRouter, dags *DAGStore, teams *TeamStore, loops *LoopStore) *surfaceState {
	st := &surfaceState{
		paths:  pathsFn,
		router: router,
		dags:   dags,
		teams:  teams,
		loops:  loops,
		theme:  "sky",
	}
	var tf themeFile
	if ok, err := readJSON(pathsFn().ThemeFile, &tf); err == nil && ok {
		if th, found := LookupTheme(tf.Theme); found {
			st.theme = th.Name
		}
	}
	return st
}

func (s *surfaceState) currentTheme() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.theme == "" {
		return "sky"
	}
	return s.theme
}

func (s *surfaceState) setTheme(name string) (OMHTheme, error) {
	th, ok := LookupTheme(name)
	if !ok {
		return OMHTheme{}, fmt.Errorf("unknown OMH theme %q (valid: sky, amber, crimson, mono, kanagawa)", name)
	}
	s.mu.Lock()
	s.theme = th.Name
	s.mu.Unlock()
	_ = atomicWriteJSON(s.paths().ThemeFile, themeFile{
		Schema:    "omh_tui_theme/v1",
		Theme:     th.Name,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	})
	return th, nil
}

func (s *surfaceState) statusText() string {
	dagList, _ := s.dags.List()
	activeDAGs := 0
	for _, d := range dagList {
		if d.Status == "active" {
			activeDAGs++
		}
	}
	teamList, _ := s.teams.List()
	activeTeams := 0
	for _, t := range teamList {
		if t.Status == "active" {
			activeTeams++
		}
	}
	return fmt.Sprintf("OMH[%s] dags:%d teams:%d", s.currentTheme(), activeDAGs, activeTeams)
}

func (s *surfaceState) show(ctx sdk.Context, text string) {
	no := false
	if err := ctx.SendMessage(omhMessageType, text, true, sdk.SendMessageOptions{TriggerTurn: &no}); err != nil {
		ctx.Notify(text, "info")
	}
}

func (s *surfaceState) renderHUD() string {
	dagList, _ := s.dags.List()
	teamList, _ := s.teams.List()
	loopList, _ := s.loops.List()
	inTok, outTok, cost, turns, toolCalls := usage.Default().Totals()
	var b strings.Builder
	fmt.Fprintf(&b, "OMH HUD | theme=%s | home=%s\n", s.currentTheme(), s.paths().Home)
	fmt.Fprintf(&b, "  DAGs: %d | Teams: %d | Loops: %d | Delegations: %d\n",
		len(dagList), len(teamList), len(loopList), len(s.teams.Delegations()))
	fmt.Fprintf(&b, "  Usage: in=%d out=%d cost=$%.4f turns=%d tools=%d",
		inTok, outTok, cost, turns, toolCalls)
	return b.String()
}

func (s *surfaceState) renderDAGView(runID string) string {
	if runID != "" {
		run, err := s.dags.Get(runID)
		if err != nil {
			return "DAG error: " + err.Error()
		}
		var b strings.Builder
		fmt.Fprintf(&b, "DAG %s (%s) — %s\n", run.RunID, run.Status, run.Title)
		ready := ReadyNodes(run)
		fmt.Fprintf(&b, "Ready frontier: [%s]\n", strings.Join(ready, ", "))
		for _, n := range run.Nodes {
			deps := "-"
			if len(n.DependsOn) > 0 {
				deps = strings.Join(n.DependsOn, ",")
			}
			fmt.Fprintf(&b, "  [%s] %s (%s) deps=[%s]\n", n.Status, n.ID, n.Title, deps)
		}
		return strings.TrimRight(b.String(), "\n")
	}
	runs, err := s.dags.List()
	if err != nil {
		return "DAG list error: " + err.Error()
	}
	if len(runs) == 0 {
		return "No OMH DAG runs recorded."
	}
	var b strings.Builder
	b.WriteString("OMH DAG Runs:\n")
	for _, r := range runs {
		fmt.Fprintf(&b, "  - %s [%s] %s (%d nodes, ready=%d)\n",
			r.RunID, r.Status, r.Title, len(r.Nodes), len(ReadyNodes(r)))
	}
	return strings.TrimRight(b.String(), "\n")
}

func (s *surfaceState) renderBotsBoard() string {
	sessions := usage.Default().Snapshot()
	delegations := s.teams.Delegations()
	var b strings.Builder
	b.WriteString("OMH Bot Board (/bots)\n")
	fmt.Fprintf(&b, "  Active/recorded sessions: %d | Delegations: %d\n", len(sessions), len(delegations))
	for _, rec := range sessions {
		fmt.Fprintf(&b, "  - session=%s parent=%s model=%s tok=%d/%d cost=$%.4f\n",
			rec.SessionID, rec.ParentID, rec.Model, rec.InputTokens, rec.OutputTokens, rec.Cost)
	}
	for _, d := range delegations {
		fmt.Fprintf(&b, "  - task=%s child=%s [%s] cat=%s model=%s\n",
			d.TaskID, d.ChildSessionID, d.Status, d.Category, d.Model)
	}
	return strings.TrimRight(b.String(), "\n")
}

func (s *surfaceState) renderModelChains() string {
	chains, overridden, err := s.router.AllChains()
	if err != nil {
		return "Model chains error: " + err.Error()
	}
	var b strings.Builder
	b.WriteString("OMH Category Model Chains:\n")
	for _, spec := range chains {
		mark := ""
		if overridden[spec.Category] {
			mark = " *"
		}
		var hops []string
		for _, c := range spec.Chain {
			if c.ThinkingLevel != "" {
				hops = append(hops, c.Model+"("+c.ThinkingLevel+")")
			} else {
				hops = append(hops, c.Model)
			}
		}
		fmt.Fprintf(&b, "  %-20s%s -> %s\n", spec.Category, mark, strings.Join(hops, " -> "))
	}
	return strings.TrimRight(b.String(), "\n")
}

// --- Slash Commands & Lifecycle Hooks ---

func registerSurfaceAndHooks(ext *sdk.Extension, st *surfaceState) {
	// Interactive slash commands: /dag, /bots, /omh-model, /omh-hud, /omh-theme
	ext.RegisterCommand("dag", sdk.CommandOptions{
		Description: "Inspect OMH DAG runs and dependency frontiers (/dag [run_id])",
		Handler: func(ctx sdk.Context, args string) error {
			st.show(ctx, st.renderDAGView(strings.TrimSpace(args)))
			return nil
		},
	})

	ext.RegisterCommand("bots", sdk.CommandOptions{
		Description: "Show the OMH multi-session bot and delegation board",
		Handler: func(ctx sdk.Context, args string) error {
			st.show(ctx, st.renderBotsBoard())
			return nil
		},
	})

	ext.RegisterCommand("omh-model", sdk.CommandOptions{
		Description: "Inspect or set OMH category model chains (/omh-model [category model [thinking]])",
		Handler: func(ctx sdk.Context, args string) error {
			fields := strings.Fields(args)
			if len(fields) == 0 {
				st.show(ctx, st.renderModelChains())
				return nil
			}
			if len(fields) >= 2 {
				cat := fields[0]
				model := fields[1]
				thinking := ""
				if len(fields) >= 3 {
					thinking = fields[2]
				}
				if err := st.router.SetOverride(cat, []ChainEntry{{Model: model, ThinkingLevel: thinking}}); err != nil {
					st.show(ctx, "omh-model error: "+err.Error())
					return nil
				}
				st.show(ctx, fmt.Sprintf("Updated OMH model chain for %q -> %s (%s)", cat, model, thinking))
				return nil
			}
			chain, overridden, err := st.router.ChainForCategory(fields[0])
			if err != nil || len(chain) == 0 {
				st.show(ctx, fmt.Sprintf("No chain found for category %q", fields[0]))
				return nil
			}
			st.show(ctx, fmt.Sprintf("Category %q (overridden=%v): %+v", fields[0], overridden, chain))
			return nil
		},
	})

	ext.RegisterCommand("omh-hud", sdk.CommandOptions{
		Description: "Display the OMH orchestration HUD summary",
		Handler: func(ctx sdk.Context, args string) error {
			st.show(ctx, st.renderHUD())
			return nil
		},
	})

	ext.RegisterCommand("omh-theme", sdk.CommandOptions{
		Description: "List or switch the OMH theme (sky, amber, crimson, mono, kanagawa)",
		Handler: func(ctx sdk.Context, args string) error {
			arg := strings.TrimSpace(args)
			if arg == "" {
				var b strings.Builder
				fmt.Fprintf(&b, "Active OMH theme: %s\n", st.currentTheme())
				for _, th := range BuiltinThemes() {
					mark := "  "
					if th.Name == st.currentTheme() {
						mark = "* "
					}
					fmt.Fprintf(&b, "%s%-10s %s (%s)\n", mark, th.Name, th.Summary, th.Accent)
				}
				st.show(ctx, strings.TrimRight(b.String(), "\n"))
				return nil
			}
			th, err := st.setTheme(arg)
			if err != nil {
				st.show(ctx, err.Error())
				return nil
			}
			ctx.SetStatus("omh", st.statusText())
			st.show(ctx, fmt.Sprintf("OMH theme set to %s (%s)", th.Name, th.Summary))
			return nil
		},
	})

	// OnSessionStart: set OMH status bar item.
	ext.OnSessionStart(func(ctx sdk.Context, data map[string]any) (any, error) {
		ctx.SetStatus("omh", st.statusText())
		return nil, nil
	})

	// OnBeforeAgentStart: append OMH underscore-only prompt sections in-place
	// without wiping any existing hermes_* sections.
	ext.OnEvent(sdk.EventBeforeAgentStart, func(ctx sdk.Context, data map[string]any) (any, error) {
		sections := extractOMHPromptSections(data)
		if sections == nil {
			return nil, nil
		}
		sections.Set(SectionOMHAwareness, omhAwarenessPrompt)
		sections.Set(SectionOMHDelegationProtocol, omhDelegationProtocolPrompt)
		if isChildAgentContext(data) {
			sections.Set(SectionOMHChildCalibration, omhChildCalibrationPrompt)
		} else {
			sections.Set(SectionOMHMainCalibration, omhMainCalibrationPrompt)
		}
		return nil, nil
	})
}

const (
	omhAwarenessPrompt = "Oh My Hermes (OMH) orchestration layer is active. Available orchestration tools: " +
		"omh_dag (topological work DAGs), omh_team (parallel lanes), omh_loop (bounded loops), " +
		"omh_delegate_route (category/identity model routing), omh_todo (unified session checklist with plan_stage), " +
		"omh_completion (prepared-vs-observed evidence boundary), omh_run_summary (usage & ledger summary), " +
		"omh_remember (episodic notes), and omh_roles (9 built-in roles + custom identities)."

	omhDelegationProtocolPrompt = "When delegating work, choose an OMH category (deep, quick, ultrabrain, architect, " +
		"review, research, writing, forensics, visual-engineering, artistry) or role/identity so OMH routes the subagent " +
		"through the configured model chain. Never claim completion from prepared evidence alone; record observed command/exit proof."

	omhMainCalibrationPrompt = "As the main orchestrator, decompose multi-step work into non-overlapping DAG/team lanes, " +
		"keep the unified todo checklist accurate, and verify observed test/build receipts before certifying completion."

	omhChildCalibrationPrompt = "As a delegated child worker, stay strictly within your assigned write scope and role boundary, " +
		"report observed command results concisely, and escalate blockers rather than silently widening scope."
)

func extractOMHPromptSections(data map[string]any) *sdk.SystemPromptSections {
	if data == nil {
		return nil
	}
	opts, ok := data["systemPromptOptions"].(map[string]any)
	if !ok || opts == nil {
		return nil
	}
	if sec, ok := opts["sections"].(*sdk.SystemPromptSections); ok && sec != nil {
		return sec
	}
	if sec, ok := opts["sections"].(sdk.SystemPromptSections); ok {
		cp := sec
		opts["sections"] = &cp
		return &cp
	}
	sec := &sdk.SystemPromptSections{}
	opts["sections"] = sec
	return sec
}

func isChildAgentContext(data map[string]any) bool {
	if data == nil {
		return false
	}
	if b, ok := data["isSubagent"].(bool); ok && b {
		return true
	}
	if p, ok := data["platform"].(string); ok && strings.EqualFold(strings.TrimSpace(p), "subagent") {
		return true
	}
	return false
}

// --- CLI Subcommands & Flag Preprocessor (registered via pig-hermes-core/cli ONLY) ---

var registerCLIOnces sync.Once

func init() {
	registerOMHCLI()
}

// registerOMHCLI registers OMH CLI subcommands and the --omh-home flag preprocessor
// into pig-hermes-core/cli without ever calling clihook.Register directly.
func registerOMHCLI() {
	registerCLIOnces.Do(func() {
		cli.RegisterFlagPreprocessor(omhHomeFlagPreprocessor)
		cli.RegisterSubcommand("hud", "Show the Oh My Hermes (OMH) orchestration HUD", runHUDSubcommand)
		cli.RegisterSubcommand("model-chains", "Inspect or override OMH category model chains", runModelChainsSubcommand)
		cli.RegisterSubcommand("dag", "List or inspect OMH DAG runs", runDAGSubcommand)
		cli.RegisterSubcommand("omh-doctor", "Check OMH state root, routing overrides, DAGs, and memory notes", runOMHDoctorSubcommand)
	})
}

func cliOut(env clihook.Env) io.Writer {
	if env.Stdout != nil {
		return env.Stdout
	}
	return io.Discard
}

func cliErr(env clihook.Env) io.Writer {
	if env.Stderr != nil {
		return env.Stderr
	}
	return io.Discard
}

// omhHomeFlagPreprocessor extracts --omh-home <dir> or --omh-home=<dir> before PiG parses flags.
func omhHomeFlagPreprocessor(env clihook.Env, args []string) ([]string, bool, int) {
	if len(args) == 0 {
		return args, false, 0
	}
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			out = append(out, args[i:]...)
			break
		}
		if strings.HasPrefix(arg, "--omh-home=") {
			val := strings.TrimSpace(strings.TrimPrefix(arg, "--omh-home="))
			if val == "" {
				fmt.Fprintf(cliErr(env), "error: --omh-home requires a directory path\n")
				return nil, true, 2
			}
			SetOMHHomeOverride(val)
			_ = os.Setenv(EnvOMHHome, val)
			continue
		}
		if arg == "--omh-home" {
			if i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" {
				fmt.Fprintf(cliErr(env), "error: --omh-home requires a directory path\n")
				return nil, true, 2
			}
			val := strings.TrimSpace(args[i+1])
			SetOMHHomeOverride(val)
			_ = os.Setenv(EnvOMHHome, val)
			i++
			continue
		}
		out = append(out, arg)
	}
	return out, false, 0
}

func runHUDSubcommand(env clihook.Env, args []string) int {
	pathsFn := func() OMHPaths { return ResolvePaths("") }
	st := newSurfaceState(pathsFn, NewRouteRouter(pathsFn, nil), NewDAGStore(pathsFn), NewTeamStore(pathsFn), NewLoopStore(pathsFn))
	fmt.Fprintln(cliOut(env), st.renderHUD())
	return 0
}

func runModelChainsSubcommand(env clihook.Env, args []string) int {
	pathsFn := func() OMHPaths { return ResolvePaths("") }
	router := NewRouteRouter(pathsFn, nil)
	if len(args) >= 3 && args[0] == "set" {
		cat := args[1]
		model := args[2]
		thinking := ""
		if len(args) >= 4 {
			thinking = args[3]
		}
		if err := router.SetOverride(cat, []ChainEntry{{Model: model, ThinkingLevel: thinking}}); err != nil {
			fmt.Fprintf(cliErr(env), "error: %v\n", err)
			return 1
		}
		fmt.Fprintf(cliOut(env), "set %s -> %s (%s)\n", cat, model, thinking)
		return 0
	}
	st := newSurfaceState(pathsFn, router, NewDAGStore(pathsFn), NewTeamStore(pathsFn), NewLoopStore(pathsFn))
	fmt.Fprintln(cliOut(env), st.renderModelChains())
	return 0
}

func runDAGSubcommand(env clihook.Env, args []string) int {
	pathsFn := func() OMHPaths { return ResolvePaths("") }
	st := newSurfaceState(pathsFn, NewRouteRouter(pathsFn, nil), NewDAGStore(pathsFn), NewTeamStore(pathsFn), NewLoopStore(pathsFn))
	runID := ""
	if len(args) > 0 {
		runID = args[0]
	}
	fmt.Fprintln(cliOut(env), st.renderDAGView(runID))
	return 0
}

func runOMHDoctorSubcommand(env clihook.Env, args []string) int {
	p := ResolvePaths("")
	out := cliOut(env)
	fmt.Fprintf(out, "OMH Doctor\n")
	fmt.Fprintf(out, "  OMH_HOME:         %s\n", p.Home)
	fmt.Fprintf(out, "  model-chains:     %s (exists=%v)\n", p.ModelChainsFile, fileExists(p.ModelChainsFile))
	fmt.Fprintf(out, "  identities:       %s (exists=%v)\n", p.IdentitiesFile, fileExists(p.IdentitiesFile))
	fmt.Fprintf(out, "  completion:       %s (exists=%v)\n", p.CompletionRecords, fileExists(p.CompletionRecords))
	fmt.Fprintf(out, "  memory notes:     %s (exists=%v)\n", p.MemoryNotesFile, fileExists(p.MemoryNotesFile))
	dags, _ := NewDAGStore(func() OMHPaths { return p }).List()
	teams, _ := NewTeamStore(func() OMHPaths { return p }).List()
	loops, _ := NewLoopStore(func() OMHPaths { return p }).List()
	fmt.Fprintf(out, "  runtime counts:   dags=%d teams=%d loops=%d roles=%d themes=%d\n",
		len(dags), len(teams), len(loops), len(BuiltinRoles()), len(BuiltinThemes()))
	return 0
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
