package ohmyhermes

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/clihook"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/cli"
	hermespaths "github.com/MichaelKinsy/pigpen/components/pig-hermes-core/paths"
)

const (
	EnvOMHTUIOnboard = "OMH_TUI_ONBOARD"

	SetupProfileSchema = "omh_setup_profile/v1"
	ManifestSchema     = "omh_manifest/v1"
)

// OMHWorkflowSkill describes one of the 10 bundled OMH workflow skills.
type OMHWorkflowSkill struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Category    string   `json:"category"`
	Summary     string   `json:"summary"`
	Keywords    []string `json:"keywords"`
	Pack        string   `json:"pack"`
	StarterText string   `json:"starter_text"`
}

var bundledOMHSkills = []OMHWorkflowSkill{
	{
		ID:          "ulw-context",
		Name:        "pigpen-omh-ulw-context",
		Category:    "quick",
		Summary:     "Build a compact codebase/task context map and handoff brief before editing.",
		Keywords:    []string{"context", "map", "explore", "orient", "codebase", "overview", "handoff", "summary", "read"},
		Pack:        "core",
		StarterText: "Map this repository and produce a compact handoff context.",
	},
	{
		ID:          "ulw-interview",
		Name:        "pigpen-omh-ulw-interview",
		Category:    "ultrabrain",
		Summary:     "Clarify ambiguous goals, constraints, non-goals, and acceptance criteria.",
		Keywords:    []string{"interview", "clarify", "requirements", "ambiguous", "scope", "questions", "spec", "criteria"},
		Pack:        "core",
		StarterText: "Interview me to nail down scope, non-goals, and acceptance criteria.",
	},
	{
		ID:          "ulw-research",
		Name:        "pigpen-omh-ulw-research",
		Category:    "research",
		Summary:     "Source-backed discovery separating observed evidence, inference, and unknowns.",
		Keywords:    []string{"research", "investigate", "compare", "sources", "docs", "literature", "evidence", "trust"},
		Pack:        "core",
		StarterText: "Research the tradeoffs with source-backed citations and clear unknowns.",
	},
	{
		ID:          "ulw-plan",
		Name:        "pigpen-omh-ulw-plan",
		Category:    "architect",
		Summary:     "Design an explicit execution plan with risks, boundaries, and verification gates.",
		Keywords:    []string{"plan", "design", "architecture", "rfc", "roadmap", "strategy", "blueprint", "milestone"},
		Pack:        "core",
		StarterText: "Draft an execution plan with file boundaries and verification gates.",
	},
	{
		ID:          "ulw-work",
		Name:        "pigpen-omh-ulw-work",
		Category:    "deep",
		Summary:     "Execute bounded implementation changes with targeted tests and clean diffs.",
		Keywords:    []string{"implement", "build", "code", "fix", "refactor", "feature", "edit", "patch", "work"},
		Pack:        "core",
		StarterText: "Implement the planned change cleanly with targeted unit tests.",
	},
	{
		ID:          "dag-ulw",
		Name:        "pigpen-omh-dag-ulw",
		Category:    "architect",
		Summary:     "Decompose multi-step work into a topological dependency DAG and execute frontiers.",
		Keywords:    []string{"dag", "graph", "dependencies", "frontier", "parallel", "waves", "multi-step", "decompose"},
		Pack:        "orchestration",
		StarterText: "Break this work into a dependency DAG and run each ready frontier.",
	},
	{
		ID:          "ulw-maestro",
		Name:        "pigpen-omh-ulw-maestro",
		Category:    "deep",
		Summary:     "Prepare explicit owner-assigned multi-lane handoff packets with write scopes.",
		Keywords:    []string{"maestro", "handoff", "delegate", "team", "lanes", "owner", "codex", "claude", "orchestrate"},
		Pack:        "orchestration",
		StarterText: "Prepare bounded Maestro handoffs with explicit owners and write scopes.",
	},
	{
		ID:          "ulw-loop",
		Name:        "pigpen-omh-ulw-loop",
		Category:    "deep",
		Summary:     "Run a bounded iterative refinement loop with explicit stop conditions.",
		Keywords:    []string{"loop", "iterate", "until", "retry", "converge", "polish", "bounded", "repeat"},
		Pack:        "orchestration",
		StarterText: "Run a bounded verification loop until all tests pass or max iterations hit.",
	},
	{
		ID:          "ulw-qa",
		Name:        "pigpen-omh-ulw-qa",
		Category:    "review",
		Summary:     "Audit diffs, hunt regressions, and verify observed command/test receipts.",
		Keywords:    []string{"qa", "review", "test", "verify", "audit", "regression", "bug", "check", "quality", "security"},
		Pack:        "verification",
		StarterText: "Run a strict QA review and verify observed test receipts before completion.",
	},
	{
		ID:          "ulw-perf",
		Name:        "pigpen-omh-ulw-perf",
		Category:    "forensics",
		Summary:     "Profile, benchmark, and eliminate bottlenecks with before/after measurements.",
		Keywords:    []string{"perf", "performance", "benchmark", "latency", "memory", "profile", "slow", "optimize", "alloc"},
		Pack:        "verification",
		StarterText: "Profile the hot path and prove the speedup with micro-benchmarks.",
	},
}

// OMHCoreRoutingCategories lists the 7 primary OMH routing categories scored by recommend.
var omhCoreRoutingCategories = []string{
	"ultrabrain",
	"deep",
	"architect",
	"quick",
	"research",
	"review",
	"forensics",
}

// SetupProfile is persisted at <omh_home>/setup-profile.json.
type SetupProfile struct {
	Schema          string `json:"schema"`
	OMHHome         string `json:"omh_home"`
	MemoryMode      string `json:"memory_mode"`
	DefaultExecutor string `json:"default_executor"`
	ConfiguredAt    string `json:"configured_at"`
}

// OMHManifest is persisted at <omh_home>/manifest.json.
type OMHManifest struct {
	Schema        string   `json:"schema"`
	Version       string   `json:"version"`
	OMHHome       string   `json:"omh_home"`
	Directories   []string `json:"directories"`
	BundledSkills []string `json:"bundled_skills"`
	UpdatedAt     string   `json:"updated_at"`
}

// RecommendationItem is one ranked skill or category recommendation.
type RecommendationItem struct {
	Kind     string  `json:"kind"` // "skill" or "category"
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Category string  `json:"category,omitempty"`
	Score    float64 `json:"score"`
	Summary  string  `json:"summary"`
	Matched  []string `json:"matched,omitempty"`
}

// RecommendationResult is the structured result for `recommend` / `omh_recommend`.
type RecommendationResult struct {
	Query           string               `json:"query"`
	SuggestedRoute  string               `json:"suggested_route"`
	PrimaryChain    []ChainEntry         `json:"primary_chain,omitempty"`
	Recommendations []RecommendationItem `json:"recommendations"`
}

var (
	registerExtendedCLIOnce sync.Once
	extendedCLIPathsMu      sync.RWMutex
	extendedCLIPathsFn      func() OMHPaths
)

func resolveExtendedPaths() OMHPaths {
	extendedCLIPathsMu.RLock()
	fn := extendedCLIPathsFn
	extendedCLIPathsMu.RUnlock()
	if fn != nil {
		return fn()
	}
	return ResolvePaths("")
}

func init() {
	registerExtendedCLI(nil)
}

// registerExtendedCLI registers the OMH extended CLI subcommands (omh-setup, quickstart,
// recommend, probe) and the --onboard flag preprocessor into pig-hermes-core/cli.
func registerExtendedCLI(pathsFn func() OMHPaths) {
	if pathsFn != nil {
		extendedCLIPathsMu.Lock()
		extendedCLIPathsFn = pathsFn
		extendedCLIPathsMu.Unlock()
	}
	registerExtendedCLIOnce.Do(func() {
		cli.RegisterFlagPreprocessor(omhOnboardFlagPreprocessor)
		cli.RegisterSubcommand("omh-setup", "Initialize ~/.omh directory layout, setup-profile.json, and manifest.json", runOMHSetupSubcommand)
		cli.RegisterSubcommand("quickstart", "Show the Oh My Hermes (OMH) Quickstart card", runOMHQuickstartSubcommand)
		cli.RegisterSubcommand("recommend", "Recommend OMH workflow skills and routing categories for a task", runOMHRecommendSubcommand)
		cli.RegisterSubcommand("probe", "Probe ~/.omh and ~/.hermes state, skills, tools, and parity", runOMHProbeSubcommand)
	})
}

// omhOnboardFlagPreprocessor handles `--onboard <name>` / `--onboard=<name>`.
// Currently only `model-setup` is supported; unknown names exit with code 2.
func omhOnboardFlagPreprocessor(env clihook.Env, args []string) ([]string, bool, int) {
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
		var val string
		matched := false
		if strings.HasPrefix(arg, "--onboard=") {
			val = strings.TrimSpace(strings.TrimPrefix(arg, "--onboard="))
			matched = true
		} else if arg == "--onboard" {
			if i+1 >= len(args) {
				fmt.Fprintf(cliErr(env), "error: --onboard requires a flow name (valid: model-setup)\n")
				return nil, true, 2
			}
			val = strings.TrimSpace(args[i+1])
			i++
			matched = true
		}
		if matched {
			if val != "model-setup" {
				fmt.Fprintf(cliErr(env), "error: unknown --onboard target %q (valid: model-setup)\n", val)
				return nil, true, 2
			}
			_ = os.Setenv(EnvOMHTUIOnboard, val)
			continue
		}
		out = append(out, arg)
	}
	return out, false, 0
}

// registerOnboardSessionHook attaches an OnSessionStart hook that renders the
// guided model-setup onboarding card when OMH_TUI_ONBOARD=model-setup is set.
func registerOnboardSessionHook(ext *sdk.Extension, pathsFn func() OMHPaths, router *RouteRouter) {
	ext.OnSessionStart(func(ctx sdk.Context, data map[string]any) (any, error) {
		if strings.TrimSpace(os.Getenv(EnvOMHTUIOnboard)) != "model-setup" {
			return nil, nil
		}
		card := renderModelSetupOnboardCard(pathsFn(), router)
		no := false
		if err := ctx.SendMessage(omhMessageType, card, true, sdk.SendMessageOptions{TriggerTurn: &no}); err != nil {
			ctx.Notify(card, "info")
		}
		return nil, nil
	})
}

func renderModelSetupOnboardCard(p OMHPaths, router *RouteRouter) string {
	var b strings.Builder
	b.WriteString("OMH Guided Model Setup Onboarding (--onboard model-setup)\n")
	b.WriteString("========================================================\n")
	fmt.Fprintf(&b, "OMH Home: %s\n", p.Home)
	b.WriteString("Steps:\n")
	b.WriteString("  1. Inspect current category chains: /omh-model or `omh model-chains`\n")
	b.WriteString("  2. Override high-reasoning tier:    /omh-model ultrabrain <model> high\n")
	b.WriteString("  3. Override implementation tier:    /omh-model deep <model> medium\n")
	b.WriteString("  4. Override fast scout tier:        /omh-model quick <model> low\n")
	b.WriteString("  5. Verify overrides persisted at:   " + p.ModelChainsFile + "\n")
	b.WriteString("  6. Run `probe --parity` or `quickstart` to confirm readiness.\n")
	if router != nil {
		if chains, overridden, err := router.AllChains(); err == nil {
			fmt.Fprintf(&b, "Configured categories: %d (overridden: %d)\n", len(chains), len(overridden))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// --- Shared Core Logic for Setup, Quickstart, Recommend, Probe ---

func omhSetupDirs(p OMHPaths) []string {
	return []string{
		p.Home,
		p.RoutingDir,
		filepath.Join(p.Home, "dags"),
		filepath.Join(p.Home, "teams"),
		filepath.Join(p.Home, "loops"),
		p.MemoryDir,
		filepath.Join(p.Home, "documents"),
		p.RuntimeDir,
		p.DAGDir,
		p.TeamDir,
		p.LoopsDir,
		p.CompletionDir,
	}
}

type setupOptions struct {
	Yes             bool
	JSON            bool
	DryRun          bool
	MemoryMode      string
	DefaultExecutor string
}

func runOMHSetupSubcommand(env clihook.Env, args []string) int {
	opts := setupOptions{
		MemoryMode:      "review-first",
		DefaultExecutor: "hermes",
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--yes" || arg == "-y":
			opts.Yes = true
		case arg == "--json":
			opts.JSON = true
		case arg == "--dry-run":
			opts.DryRun = true
		case arg == "--memory-mode" && i+1 < len(args):
			i++
			opts.MemoryMode = strings.TrimSpace(args[i])
		case strings.HasPrefix(arg, "--memory-mode="):
			opts.MemoryMode = strings.TrimSpace(strings.TrimPrefix(arg, "--memory-mode="))
		case arg == "--default-executor" && i+1 < len(args):
			i++
			opts.DefaultExecutor = strings.TrimSpace(args[i])
		case strings.HasPrefix(arg, "--default-executor="):
			opts.DefaultExecutor = strings.TrimSpace(strings.TrimPrefix(arg, "--default-executor="))
		default:
			fmt.Fprintf(cliErr(env), "error: unknown setup flag %q\n", arg)
			return 2
		}
	}

	switch opts.MemoryMode {
	case "off", "review-first", "auto-safe":
	default:
		fmt.Fprintf(cliErr(env), "error: invalid --memory-mode %q (valid: off, review-first, auto-safe)\n", opts.MemoryMode)
		return 2
	}
	if opts.DefaultExecutor == "" {
		fmt.Fprintf(cliErr(env), "error: --default-executor cannot be empty\n")
		return 2
	}

	p := resolveExtendedPaths()
	dirs := omhSetupDirs(p)
	now := time.Now().UTC().Format(time.RFC3339)

	skillNames := make([]string, len(bundledOMHSkills))
	for i, s := range bundledOMHSkills {
		skillNames[i] = s.Name
	}

	profile := SetupProfile{
		Schema:          SetupProfileSchema,
		OMHHome:         p.Home,
		MemoryMode:      opts.MemoryMode,
		DefaultExecutor: opts.DefaultExecutor,
		ConfiguredAt:    now,
	}
	manifest := OMHManifest{
		Schema:        ManifestSchema,
		Version:       "1.0.0",
		OMHHome:       p.Home,
		Directories:   []string{"routing", "dags", "teams", "loops", "memory", "documents", "runtime"},
		BundledSkills: skillNames,
		UpdatedAt:     now,
	}

	profilePath := filepath.Join(p.Home, "setup-profile.json")
	manifestPath := filepath.Join(p.Home, "manifest.json")

	if !opts.DryRun {
		for _, d := range dirs {
			if err := os.MkdirAll(d, 0o700); err != nil {
				fmt.Fprintf(cliErr(env), "error: mkdir %s: %v\n", d, err)
				return 1
			}
		}
		if err := atomicWriteJSON(profilePath, profile); err != nil {
			fmt.Fprintf(cliErr(env), "error: write %s: %v\n", profilePath, err)
			return 1
		}
		if err := atomicWriteJSON(manifestPath, manifest); err != nil {
			fmt.Fprintf(cliErr(env), "error: write %s: %v\n", manifestPath, err)
			return 1
		}
	}

	payload := map[string]any{
		"ok":               true,
		"dry_run":          opts.DryRun,
		"yes":              opts.Yes,
		"omh_home":         p.Home,
		"memory_mode":      opts.MemoryMode,
		"default_executor": opts.DefaultExecutor,
		"setup_profile":    profilePath,
		"manifest":         manifestPath,
		"directories":      manifest.Directories,
		"bundled_skills":   len(skillNames),
	}

	if opts.JSON {
		writeCLIJSON(env, payload)
		return 0
	}

	out := cliOut(env)
	modeLabel := "Applied"
	if opts.DryRun {
		modeLabel = "Dry-Run"
	}
	fmt.Fprintf(out, "Oh My Hermes (OMH) Setup — %s\n", modeLabel)
	fmt.Fprintf(out, "  OMH Home:         %s\n", p.Home)
	fmt.Fprintf(out, "  Memory Mode:      %s\n", opts.MemoryMode)
	fmt.Fprintf(out, "  Default Executor: %s\n", opts.DefaultExecutor)
	fmt.Fprintf(out, "  Directories:      %s\n", strings.Join(manifest.Directories, ", "))
	fmt.Fprintf(out, "  Setup Profile:    %s\n", profilePath)
	fmt.Fprintf(out, "  Manifest:         %s\n", manifestPath)
	fmt.Fprintf(out, "  Bundled Skills:   %d\n", len(skillNames))
	return 0
}

func buildQuickstartCard(p OMHPaths, source string) map[string]any {
	if source == "" {
		source = "hermes"
	}
	hp := hermespaths.ResolveProfilePaths("")
	manifestPath := filepath.Join(p.Home, "manifest.json")
	profilePath := filepath.Join(p.Home, "setup-profile.json")
	omhReady := fileExists(manifestPath) || fileExists(profilePath)
	hermesConfigExists := fileExists(hp.ConfigFile)

	var sp SetupProfile
	_, _ = readJSON(profilePath, &sp)
	memMode := sp.MemoryMode
	if memMode == "" {
		memMode = "review-first"
	}

	packs := []map[string]any{
		{"id": "core", "title": "Core Workflow", "skills": []string{"ulw-context", "ulw-interview", "ulw-research", "ulw-plan", "ulw-work"}},
		{"id": "orchestration", "title": "Multi-Agent Orchestration", "skills": []string{"dag-ulw", "ulw-maestro", "ulw-loop"}},
		{"id": "verification", "title": "Evidence & Performance", "skills": []string{"ulw-qa", "ulw-perf"}},
	}
	starters := []string{
		"Map this repo with ulw-context and propose a DAG plan for my feature.",
		"Run omh_recommend on my task before choosing a workflow or model tier.",
		"Use omh_gather_evidence to collect observed test receipts before claiming done.",
	}
	nextActions := []string{}
	if !omhReady {
		nextActions = append(nextActions, "Run `omh-setup --yes` to initialize ~/.omh/ directories, setup-profile.json, and manifest.json.")
	}
	if !hermesConfigExists {
		nextActions = append(nextActions, fmt.Sprintf("Create or initialize Hermes config at %s.", hp.ConfigFile))
	}
	nextActions = append(nextActions,
		"Run `probe --parity` to inspect installed skills, tools, and runtime readiness.",
		"Launch with `--onboard model-setup` or `/omh-model` to tune category model chains.",
	)

	return map[string]any{
		"schema": "omh_quickstart_card/v1",
		"source": source,
		"summary": map[string]any{
			"omh_home":             p.Home,
			"omh_initialized":      omhReady,
			"hermes_profile":       hp.ProfileName,
			"hermes_config":        hp.ConfigFile,
			"hermes_config_exists": hermesConfigExists,
			"memory_mode":          memMode,
			"bundled_skills":       len(bundledOMHSkills),
		},
		"first_value_packs":         packs,
		"natural_language_starters": starters,
		"operator_next_actions":     nextActions,
	}
}

func runOMHQuickstartSubcommand(env clihook.Env, args []string) int {
	asJSON := false
	source := "hermes"
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--json":
			asJSON = true
		case arg == "--source" && i+1 < len(args):
			i++
			source = strings.TrimSpace(args[i])
		case strings.HasPrefix(arg, "--source="):
			source = strings.TrimSpace(strings.TrimPrefix(arg, "--source="))
		default:
			fmt.Fprintf(cliErr(env), "error: unknown quickstart flag %q\n", arg)
			return 2
		}
	}
	if source != "hermes" && source != "terminal" {
		fmt.Fprintf(cliErr(env), "error: invalid --source %q (valid: hermes, terminal)\n", source)
		return 2
	}

	p := resolveExtendedPaths()
	card := buildQuickstartCard(p, source)
	if asJSON {
		writeCLIJSON(env, card)
		return 0
	}

	summary := card["summary"].(map[string]any)
	out := cliOut(env)
	fmt.Fprintf(out, "Oh My Hermes (OMH) Quickstart [%s]\n", source)
	fmt.Fprintf(out, "Summary:\n")
	fmt.Fprintf(out, "  OMH Home:      %s (initialized=%v)\n", summary["omh_home"], summary["omh_initialized"])
	fmt.Fprintf(out, "  Hermes Config: %s (exists=%v, profile=%s)\n", summary["hermes_config"], summary["hermes_config_exists"], summary["hermes_profile"])
	fmt.Fprintf(out, "  Memory Mode:   %s | Bundled Skills: %v\n", summary["memory_mode"], summary["bundled_skills"])
	fmt.Fprintf(out, "\nFirst-value packs:\n")
	for _, pk := range card["first_value_packs"].([]map[string]any) {
		fmt.Fprintf(out, "  - %s (%s): %s\n", pk["title"], pk["id"], strings.Join(pk["skills"].([]string), ", "))
	}
	fmt.Fprintf(out, "\nNatural-language starters:\n")
	for _, s := range card["natural_language_starters"].([]string) {
		fmt.Fprintf(out, "  - %s\n", s)
	}
	fmt.Fprintf(out, "\nOperator next actions:\n")
	for _, a := range card["operator_next_actions"].([]string) {
		fmt.Fprintf(out, "  - %s\n", a)
	}
	return 0
}

// scoreRecommendations ranks the 10 bundled OMH workflow skills and 7 core
// routing categories against query.
func scoreRecommendations(query string, limit int, router *RouteRouter) RecommendationResult {
	if limit < 1 {
		limit = 5
	}
	if limit > 10 {
		limit = 10
	}
	tokens := tokenizeQuery(query)
	tokenSet := make(map[string]bool, len(tokens))
	for _, t := range tokens {
		tokenSet[t] = true
	}

	var items []RecommendationItem
	for _, sk := range bundledOMHSkills {
		score := 0.0
		var matched []string
		hay := strings.ToLower(sk.ID + " " + sk.Summary + " " + sk.Category)
		for _, kw := range sk.Keywords {
			if tokenSet[kw] {
				score += 3.0
				matched = append(matched, kw)
			} else {
				for _, tok := range tokens {
					if strings.Contains(kw, tok) || strings.Contains(tok, kw) {
						score += 1.5
						matched = append(matched, kw)
						break
					}
				}
			}
		}
		for _, tok := range tokens {
			if strings.Contains(hay, tok) {
				score += 0.5
			}
		}
		if score == 0 && len(tokens) == 0 {
			score = 0.5
		}
		items = append(items, RecommendationItem{
			Kind:     "skill",
			ID:       sk.ID,
			Name:     sk.Name,
			Category: sk.Category,
			Score:    score,
			Summary:  sk.Summary,
			Matched:  dedupeStrings(matched),
		})
	}

	catSpecMap := make(map[string]CategorySpec, len(defaultCategories))
	for _, c := range defaultCategories {
		catSpecMap[c.Category] = c
	}
	for _, cat := range omhCoreRoutingCategories {
		spec, ok := catSpecMap[cat]
		if !ok {
			continue
		}
		score := 0.0
		var matched []string
		hay := strings.ToLower(spec.Category + " " + spec.Purpose)
		for _, tok := range tokens {
			if tok == spec.Category {
				score += 4.0
				matched = append(matched, tok)
			} else if strings.Contains(hay, tok) {
				score += 1.5
				matched = append(matched, tok)
			}
		}
		items = append(items, RecommendationItem{
			Kind:     "category",
			ID:       spec.Category,
			Name:     spec.Category,
			Category: spec.Category,
			Score:    score,
			Summary:  spec.Purpose,
			Matched:  dedupeStrings(matched),
		})
	}

	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Score != items[j].Score {
			return items[i].Score > items[j].Score
		}
		if items[i].Kind != items[j].Kind {
			return items[i].Kind == "skill"
		}
		return items[i].ID < items[j].ID
	})

	n := limit
	if n > len(items) {
		n = len(items)
	}
	ranked := items[:n]

	suggestedRoute := "deep"
	if len(ranked) > 0 && ranked[0].Category != "" && ranked[0].Score > 0 {
		suggestedRoute = ranked[0].Category
	}

	var chain []ChainEntry
	if router != nil {
		if ch, _, err := router.ChainForCategory(suggestedRoute); err == nil {
			chain = ch
		}
	}

	return RecommendationResult{
		Query:           strings.TrimSpace(query),
		SuggestedRoute:  suggestedRoute,
		PrimaryChain:    chain,
		Recommendations: ranked,
	}
}

func dedupeStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func runOMHRecommendSubcommand(env clihook.Env, args []string) int {
	limit := 5
	asJSON := false
	var queryParts []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--json":
			asJSON = true
		case arg == "--limit" && i+1 < len(args):
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 1 {
				fmt.Fprintf(cliErr(env), "error: --limit must be a positive integer\n")
				return 2
			}
			limit = n
		case strings.HasPrefix(arg, "--limit="):
			n, err := strconv.Atoi(strings.TrimPrefix(arg, "--limit="))
			if err != nil || n < 1 {
				fmt.Fprintf(cliErr(env), "error: --limit must be a positive integer\n")
				return 2
			}
			limit = n
		case strings.HasPrefix(arg, "-"):
			fmt.Fprintf(cliErr(env), "error: unknown recommend flag %q\n", arg)
			return 2
		default:
			queryParts = append(queryParts, arg)
		}
	}

	query := strings.TrimSpace(strings.Join(queryParts, " "))
	if query == "" {
		fmt.Fprintf(cliErr(env), "usage: recommend <task...> [--limit N] [--json]\n")
		return 2
	}

	pathsFn := resolveExtendedPaths
	router := NewRouteRouter(pathsFn, nil)
	res := scoreRecommendations(query, limit, router)

	if asJSON {
		writeCLIJSON(env, res)
		return 0
	}

	out := cliOut(env)
	fmt.Fprintf(out, "OMH Recommendations for: %s\n", res.Query)
	fmt.Fprintf(out, "Suggested Route: %s\n", res.SuggestedRoute)
	for i, rec := range res.Recommendations {
		fmt.Fprintf(out, "  %d. [%s] %s (cat=%s score=%.1f) — %s\n",
			i+1, rec.Kind, rec.ID, rec.Category, rec.Score, rec.Summary)
	}
	return 0
}

func buildProbePayload(p OMHPaths, includeParity, includeRoadmap bool, dags *DAGStore, teams *TeamStore, loops *LoopStore) map[string]any {
	hp := hermespaths.ResolveProfilePaths("")
	manifestPath := filepath.Join(p.Home, "manifest.json")
	profilePath := filepath.Join(p.Home, "setup-profile.json")

	var installedSkills []string
	if entries, err := os.ReadDir(hp.SkillsDir); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				installedSkills = append(installedSkills, e.Name())
			}
		}
		sort.Strings(installedSkills)
	}

	dagCount, teamCount, loopCount := 0, 0, 0
	if dags != nil {
		if list, err := dags.List(); err == nil {
			dagCount = len(list)
		}
	}
	if teams != nil {
		if list, err := teams.List(); err == nil {
			teamCount = len(list)
		}
	}
	if loops != nil {
		if list, err := loops.List(); err == nil {
			loopCount = len(list)
		}
	}

	activeTools := allOMHToolNames(LSPToolsEnabled())

	payload := map[string]any{
		"schema": "omh_probe/v1",
		"omh": map[string]any{
			"home":                 p.Home,
			"initialized":          fileExists(manifestPath) || fileExists(profilePath),
			"manifest_exists":      fileExists(manifestPath),
			"setup_profile_exists": fileExists(profilePath),
			"model_chains_exists":  fileExists(p.ModelChainsFile),
			"identities_exists":    fileExists(p.IdentitiesFile),
			"memory_notes_exists":  fileExists(p.MemoryNotesFile),
			"dags":                 dagCount,
			"teams":                teamCount,
			"loops":                loopCount,
		},
		"hermes": map[string]any{
			"profile":       hp.ProfileName,
			"home":          hp.Home,
			"config_file":   hp.ConfigFile,
			"config_exists": fileExists(hp.ConfigFile),
			"skills_dir":    hp.SkillsDir,
		},
		"installed_skills": installedSkills,
		"bundled_skills":   len(bundledOMHSkills),
		"active_tools":     activeTools,
		"active_tool_count": len(activeTools),
		"lsp_tools_enabled": LSPToolsEnabled(),
	}

	if includeParity {
		payload["parity"] = map[string]any{
			"omh_tools_core":      21,
			"lsp_tools_optional":  4,
			"bundled_skills":      len(bundledOMHSkills),
			"builtin_roles":       len(BuiltinRoles()),
			"routing_categories":  len(defaultCategories),
			"cli_subcommands":     []string{"hud", "model-chains", "dag", "omh-doctor", "omh-setup", "quickstart", "recommend", "probe"},
			"evidence_boundary":   "enforced",
			"source_trust_gate":   "fail_closed",
		}
	}
	if includeRoadmap {
		payload["roadmap"] = []map[string]any{
			{"area": "decision_gates", "status": "planned", "summary": "Host-authenticated connector approval gates"},
			{"area": "browser_lease", "status": "planned", "summary": "Bounded semantic browser observation bridge"},
			{"area": "codegraph_uml", "status": "planned", "summary": "Project-local codegraph and UML handoff export"},
		}
	}
	return payload
}

func runOMHProbeSubcommand(env clihook.Env, args []string) int {
	includeParity := false
	includeRoadmap := false
	asJSON := false
	for _, arg := range args {
		switch arg {
		case "--parity":
			includeParity = true
		case "--roadmap":
			includeRoadmap = true
		case "--json":
			asJSON = true
		default:
			fmt.Fprintf(cliErr(env), "error: unknown probe flag %q\n", arg)
			return 2
		}
	}

	pathsFn := resolveExtendedPaths
	p := pathsFn()
	payload := buildProbePayload(p, includeParity, includeRoadmap, NewDAGStore(pathsFn), NewTeamStore(pathsFn), NewLoopStore(pathsFn))

	if asJSON {
		writeCLIJSON(env, payload)
		return 0
	}

	omhMap := payload["omh"].(map[string]any)
	hermesMap := payload["hermes"].(map[string]any)
	out := cliOut(env)
	fmt.Fprintf(out, "Oh My Hermes (OMH) Capability Probe\n")
	fmt.Fprintf(out, "  OMH Home:        %s (initialized=%v)\n", omhMap["home"], omhMap["initialized"])
	fmt.Fprintf(out, "  Hermes Config:   %s (exists=%v, profile=%s)\n", hermesMap["config_file"], hermesMap["config_exists"], hermesMap["profile"])
	fmt.Fprintf(out, "  Runtime State:   dags=%v teams=%v loops=%v\n", omhMap["dags"], omhMap["teams"], omhMap["loops"])
	fmt.Fprintf(out, "  Skills:          bundled=%v installed=%d\n", payload["bundled_skills"], len(payload["installed_skills"].([]string)))
	fmt.Fprintf(out, "  Active Tools:    %v (lsp=%v)\n", payload["active_tool_count"], payload["lsp_tools_enabled"])
	if includeParity {
		par := payload["parity"].(map[string]any)
		fmt.Fprintf(out, "  Parity:          omh_tools=%v lsp_tools=%v roles=%v categories=%v\n",
			par["omh_tools_core"], par["lsp_tools_optional"], par["builtin_roles"], par["routing_categories"])
	}
	if includeRoadmap {
		fmt.Fprintf(out, "  Roadmap items:   %d\n", len(payload["roadmap"].([]map[string]any)))
	}
	return 0
}

func allOMHToolNames(includeLSP bool) []string {
	names := []string{
		// Original 10 OMH tools
		"omh_delegate_route",
		"omh_dag",
		"omh_team",
		"omh_loop",
		"omh_todo",
		"omh_completion",
		"omh_run_summary",
		"omh_remember",
		"omh_roles",
		"omh_maestro",
		// 11 Extended OMH tools
		"omh_status",
		"omh_hud",
		"omh_context",
		"omh_recommend",
		"omh_probe",
		"omh_capabilities",
		"omh_memory",
		"omh_resume",
		"omh_gather_evidence",
		"omh_document_plan",
		"omh_source_trust",
	}
	if includeLSP {
		names = append(names, "lsp_definition", "lsp_references", "lsp_rename", "lsp_diagnostics")
	}
	return names
}

func writeCLIJSON(env clihook.Env, payload any) {
	b, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		fmt.Fprintf(cliErr(env), "error: marshal json: %v\n", err)
		return
	}
	fmt.Fprintln(cliOut(env), string(b))
}
